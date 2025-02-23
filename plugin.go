package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/gotify/plugin-api"
)

type GithubNotification struct {
	ID         string `json:"id"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Subject struct {
		Title string `json:"title"`
		Type  string `json:"type"`
		URL   string `json:"url"`
	} `json:"subject"`
	UpdatedAt time.Time `json:"updated_at"`
}

func GetGotifyPluginInfo() plugin.Info {
	return plugin.Info{
		ModulePath:  "github.com/gotify/github-plugin",
		Version:     "1.0.0",
		Author:      "Your Name",
		Website:     "https://github.com",
		Description: "Github notification plugin for Gotify",
		License:     "MIT",
		Name:        "gotify-github-plugin",
	}
}

type MyPlugin struct {
	ctx           plugin.UserContext
	enabled       bool
	stopChannel   chan struct{}
	githubToken   string
	pollInterval  time.Duration
	lastCheckTime time.Time
	appID         uint
	appToken      string
	msgHandler    plugin.MessageHandler
}

func (c *MyPlugin) Enable() error {
	if c.appToken != "" {
		log.Printf("Using custom application token")
	} else {
		c.appID = c.ctx.ID
		log.Printf("Using default application with ID: %d", c.appID)
	}
	c.enabled = true
	c.stopChannel = make(chan struct{})
	go c.startPolling()
	return nil
}

func (c *MyPlugin) Disable() error {
	if c.enabled {
		c.enabled = false
		close(c.stopChannel)
	}
	return nil
}

func (c *MyPlugin) startPolling() {
	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.checkNotifications()
		case <-c.stopChannel:
			return
		}
	}
}

func (c *MyPlugin) checkNotifications() {
	client := &http.Client{}
	req, err := http.NewRequest("GET", "https://api.github.com/notifications", nil)
	if err != nil {
		log.Printf("error creating request: %v", err)
		return
	}
	req.Header.Add("Authorization", "token "+c.githubToken)
	req.Header.Add("Accept", "application/vnd.github.v3+json")
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("error executing request: %v", err)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("error reading response body: %v", err)
		return
	}
	var notifications []GithubNotification
	if err := json.Unmarshal(body, &notifications); err != nil {
		log.Printf("error unmarshalling json: %v", err)
		return
	}
	for _, notification := range notifications {
		if notification.UpdatedAt.After(c.lastCheckTime) {
			msg := &plugin.Message{
				Title:    notification.Subject.Title,
				Message:  fmt.Sprintf("New notification in %s", notification.Repository.FullName),
				Priority: 2,
			}
			err := c.msgHandler.SendMessage(*msg)
			if err != nil {
				log.Printf("error sending message: %v", err)
			} else {
				log.Printf("sent notification: %s", notification.Subject.Title)
			}
		}
	}
	c.lastCheckTime = time.Now()
}

func (c *MyPlugin) GetDefaultConfig() any {
	return struct {
		Token       string `json:"token"`
		Interval    int    `json:"interval"`
		AppToken    string `json:"apptoken"`
		Description string `json:"description"`
	}{
		Token:       "",
		Interval:    60,
		AppToken:    "",
		Description: "Enter your GitHub token, polling interval (in seconds), and Gotify application token to manage app settings, e.g. avatar image",
	}
}

func (c *MyPlugin) ValidateConfig(config any) error {
	b, err := json.Marshal(config)
	if err != nil {
		return err
	}
	var conf struct {
		Token       string `json:"token"`
		Interval    int    `json:"interval"`
		AppToken    string `json:"apptoken"`
		Description string `json:"description"`
	}
	if err = json.Unmarshal(b, &conf); err != nil {
		return err
	}
	if conf.Token == "" {
		return fmt.Errorf("GitHub token is required")
	}
	return nil
}

func NewGotifyPluginInstance(ctx plugin.UserContext) plugin.Plugin {
	return &MyPlugin{
		ctx:          ctx,
		pollInterval: 60 * time.Second,
		enabled:      false,
		appID:        ctx.ID,
	}
}

func (c *MyPlugin) SetMessageHandler(h plugin.MessageHandler) {
	c.msgHandler = h
}

func (c *MyPlugin) ApplyConfig(config any) error {
	if configMap, ok := config.(map[string]any); ok {
		if token, exists := configMap["token"].(string); exists {
			c.githubToken = token
		}
		if interval, exists := configMap["interval"].(float64); exists {
			c.pollInterval = time.Duration(interval) * time.Second
		}
	}
	return nil
}

func (c *MyPlugin) GetDisplay(location *url.URL) string {
	return "Configure your GitHub token and polling interval below to receive notifications"
}

func (c *MyPlugin) DefaultConfig() any {
	return &struct {
		Token       string `json:"token"`
		Interval    int    `json:"interval"`
		AppToken    string `json:"apptoken"`
		Description string `json:"description"`
	}{
		Token:       "",
		Interval:    60,
		AppToken:    "",
		Description: "Enter your GitHub token, polling interval (in seconds), and Gotify application token to manage app settings, e.g. avatar image",
	}
}

func (c *MyPlugin) ValidateAndSetConfig(cfg interface{}) error {
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	var conf struct {
		Token       string `json:"token"`
		Interval    int    `json:"interval"`
		AppToken    string `json:"apptoken"`
		Description string `json:"description"`
	}
	if err = json.Unmarshal(b, &conf); err != nil {
		return err
	}
	if conf.Token == "" {
		return fmt.Errorf("GitHub token is required")
	}
	c.githubToken = conf.Token
	c.pollInterval = time.Duration(conf.Interval) * time.Second
	c.appToken = conf.AppToken
	return nil
}

func main() {
	panic("this should be built as go plugin")
}
