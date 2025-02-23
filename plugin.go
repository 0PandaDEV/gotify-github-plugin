package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
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

	body, _ := ioutil.ReadAll(resp.Body)
	var notifications []GithubNotification
	json.Unmarshal(body, &notifications)

	for _, notification := range notifications {
		if notification.UpdatedAt.After(c.lastCheckTime) {
			messageID := c.ctx.ID
			name := c.ctx.Name
			fmt.Printf("Notification for user %s with id %d\n", name, messageID)
		}
	}
	c.lastCheckTime = time.Now()
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
	})
}

// NewGotifyPluginInstance creates a plugin instance for a user context.
func NewGotifyPluginInstance(ctx plugin.UserContext) plugin.Plugin {
	return &MyPlugin{
		ctx:          ctx,
		pollInterval: 60 * time.Second,
		enabled:      false,
	}
}

func main() {
	panic("this should be built as go plugin")
}
