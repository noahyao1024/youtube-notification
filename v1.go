package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/youtube/v3"
	"gopkg.in/yaml.v3"
)

// Configuration
type Config struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
	RedirectURL  string `yaml:"redirect_url"`
	WebhookURL   string `yaml:"webhook_url"`
	ChannelID    string `yaml:"channel_id"`
}

var config *Config

var (
	oauthConfig      *oauth2.Config
	state            = "randomstatestring"
	token            *oauth2.Token
	tokenMutex       sync.Mutex
	latestCount      uint64
	latestCountMutex sync.Mutex
)

func init() {
	// Read from yaml file
	data, err := os.ReadFile("config.yaml")
	if err != nil {
		panic(fmt.Sprintf("Read config file error: %v", err))
	}

	config = &Config{}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	err = decoder.Decode(config)
	if err != nil {
		panic(fmt.Sprintf("Decode config file error: %v", err))
	}

	if config.ClientID == "" || config.ClientSecret == "" || config.RedirectURL == "" || config.WebhookURL == "" || config.ChannelID == "" {
		panic("Invalid configuration")
	}

	oauthConfig = &oauth2.Config{
		ClientID:     config.ClientID,
		ClientSecret: config.ClientSecret,
		RedirectURL:  config.RedirectURL,
		Scopes:       []string{youtube.YoutubeReadonlyScope},
		Endpoint:     google.Endpoint,
	}
}

func main() {
	http.HandleFunc("/", handleHome)
	http.HandleFunc("/login", handleLogin)
	http.HandleFunc("/oauth2callback", handleOAuth2Callback)
	go monitorSubscriberCount()
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleHome(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, `<html><body><a href="/login">Login with YouTube</a></body></html>`)
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	url := oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline)
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

func handleOAuth2Callback(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("state") != state {
		http.Error(w, "State parameter doesn't match", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")
	tok, err := oauthConfig.Exchange(context.Background(), code)
	if err != nil {
		http.Error(w, "Failed to exchange token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	tokenMutex.Lock()
	token = tok
	tokenMutex.Unlock()

	fmt.Fprintf(w, "Login successful! You can close this window.")
}

func monitorSubscriberCount() {
	for {
		time.Sleep(10 * time.Second) // Adjust the interval as needed
		tokenMutex.Lock()
		if token == nil {
			tokenMutex.Unlock()
			continue
		}
		client := oauthConfig.Client(context.Background(), token)
		tokenMutex.Unlock()

		service, err := youtube.New(client)
		if err != nil {
			log.Printf("Error creating YouTube service: %v", err)
			continue
		}

		call := service.Channels.List([]string{"statistics"}).Id(config.ChannelID)
		response, err := call.Do()
		if err != nil {
			log.Printf("Error fetching channel statistics: %v", err)
			continue
		}

		if len(response.Items) == 0 {
			log.Printf("No channel found with ID: %s", config.ChannelID)
			continue
		}

		subscriberCount := response.Items[0].Statistics.SubscriberCount
		latestCountMutex.Lock()
		if subscriberCount != latestCount {
			latestCount = subscriberCount
			sendWebhookNotification(subscriberCount)
		}
		latestCountMutex.Unlock()
	}
}

func sendWebhookNotification(subscriberCount uint64) {
	fmt.Println("Sending webhook notification with subscriber count:", subscriberCount)

	payload := map[string]interface{}{
		"subscriber_count": subscriberCount,
	}
	body, _ := json.Marshal(payload)

	resp, err := http.Post(config.WebhookURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		log.Printf("Error sending webhook notification: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Unexpected status code from webhook: %d", resp.StatusCode)
	}
}
