package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
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

// GetGotifyPluginInfo returns gotify plugin info.
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

// MyPlugin is the gotify plugin instance.
type MyPlugin struct {
	ctx           plugin.UserContext
	enabled       bool
	stopChannel   chan struct{}
	githubToken   string
	pollInterval  time.Duration
	lastCheckTime time.Time
	appID         uint
	msgHandler    plugin.MessageHandler
}

// Enable enables the plugin.
func (c *MyPlugin) Enable() error {
	c.enabled = true
	c.appID = 1
	c.stopChannel = make(chan struct{})
	go c.startPolling()
	return nil
}

// Disable disables the plugin.
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
	req, _ := http.NewRequest("GET", "https://api.github.com/notifications", nil)
	req.Header.Add("Authorization", "token "+c.githubToken)
	req.Header.Add("Accept", "application/vnd.github.v3+json")

	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var notifications []GithubNotification
	json.Unmarshal(body, &notifications)

	for _, notification := range notifications {
		if notification.UpdatedAt.After(c.lastCheckTime) {
			msg := &plugin.Message{
				Title:    notification.Subject.Title,
				Message:  fmt.Sprintf("New notification in %s", notification.Repository.FullName),
				Priority: 2,
			}
			c.msgHandler.SendMessage(*msg)
		}
	}
	c.lastCheckTime = time.Now()
}

// GetDefaultConfig implements plugin.Configurer
func (c *MyPlugin) GetDefaultConfig() any {
	return struct {
		Token    string `json:"token"`
		Interval int    `json:"interval"`
	}{
		Token:    "",
		Interval: 60,
	}
}

// ValidateConfig implements plugin.Configurer
func (c *MyPlugin) ValidateConfig(config any) error {
	return nil
}

// RegisterWebhook implements plugin.Webhooker.
func (c *MyPlugin) RegisterWebhook(basePath string, g *gin.RouterGroup) {
	g.POST("/config", func(context *gin.Context) {
		var config struct {
			Token    string `json:"token"`
			Interval int    `json:"interval"`
		}
		context.BindJSON(&config)
		c.githubToken = config.Token
		c.pollInterval = time.Duration(config.Interval) * time.Second
		context.JSON(200, gin.H{"success": true})
	})
}

// NewGotifyPluginInstance creates a plugin instance for a user context.
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

func main() {
	panic("this should be built as go plugin")
}
