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

type MyPlugin struct {
	ctx               plugin.UserContext
	enabled           bool
	stopChannel       chan struct{}
	githubToken       string
	pollInterval      time.Duration
	lastCheckTime     time.Time
	lastStarCheckTime time.Time
	appID             uint
	appToken          string
	watchStars        bool
	msgHandler        plugin.MessageHandler
}

func (c *MyPlugin) DefaultConfig() any {
	return &struct {
		Token       string `json:"token"`
		Interval    int    `json:"interval"`
		AppToken    string `json:"apptoken"`
		WatchStars  bool   `json:"watchStars"`
		Description string `json:"description"`
	}{
		Token:       "",
		Interval:    60,
		AppToken:    "",
		WatchStars:  false,
		Description: "Enter GitHub token, polling interval (seconds), Gotify application token, and enable star notifications",
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
		WatchStars  bool   `json:"watchStars"`
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
	c.watchStars = conf.WatchStars
	return nil
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
			if c.watchStars {
				c.checkStars()
			}
		case <-c.stopChannel:
			return
		}
	}
}

func (c *MyPlugin) checkNotifications() {
	client := &http.Client{}
	req, err := http.NewRequest("GET", "https://api.github.com/notifications", nil)
	if err != nil {
		log.Printf("error creating github notifications request: %v", err)
		return
	}
	req.Header.Add("Authorization", "token "+c.githubToken)
	req.Header.Add("Accept", "application/vnd.github.v3+json")
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("error executing github notifications request: %v", err)
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("error reading github notifications response: %v", err)
		return
	}
	var notifications []GithubNotification
	if err := json.Unmarshal(body, &notifications); err != nil {
		log.Printf("error unmarshalling github notifications: %v", err)
		return
	}
	for _, notification := range notifications {
		if notification.UpdatedAt.After(c.lastCheckTime) {
			msg := &plugin.Message{
				Title:    notification.Subject.Title,
				Message:  fmt.Sprintf("New notification in %s", notification.Repository.FullName),
				Priority: 2,
			}
			if err := c.msgHandler.SendMessage(*msg); err != nil {
				log.Printf("error sending github notification: %v", err)
			} else {
				log.Printf("sent github notification: %s", notification.Subject.Title)
			}
		}
	}
	c.lastCheckTime = time.Now()
}

func (c *MyPlugin) checkStars() {
	client := &http.Client{}
	req, err := http.NewRequest("GET", "https://api.github.com/user/repos", nil)
	if err != nil {
		log.Printf("error creating repos request: %v", err)
		return
	}
	req.Header.Add("Authorization", "token "+c.githubToken)
	req.Header.Add("Accept", "application/vnd.github.v3+json")
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("error executing repos request: %v", err)
		return
	}
	defer resp.Body.Close()
	var repos []struct {
		FullName string `json:"full_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		log.Printf("error decoding repos: %v", err)
		return
	}
	for _, repo := range repos {
		repoURL := fmt.Sprintf("https://api.github.com/repos/%s/stargazers", repo.FullName)
		req, err := http.NewRequest("GET", repoURL, nil)
		if err != nil {
			log.Printf("error creating stargazers request for %s: %v", repo.FullName, err)
			continue
		}
		req.Header.Add("Authorization", "token "+c.githubToken)
		req.Header.Add("Accept", "application/vnd.github.v3.star+json")
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("error executing stargazers request for %s: %v", repo.FullName, err)
			continue
		}
		var stars []struct {
			StarredAt time.Time `json:"starred_at"`
			User      struct {
				Login string `json:"login"`
			} `json:"user"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&stars); err != nil {
			log.Printf("error decoding stargazers for %s: %v", repo.FullName, err)
			resp.Body.Close()
			continue
		}
		resp.Body.Close()
		for _, star := range stars {
			if star.StarredAt.After(c.lastStarCheckTime) {
				msg := &plugin.Message{
					Title:    "New Star",
					Message:  fmt.Sprintf("Repo %s received a star from %s at %s", repo.FullName, star.User.Login, star.StarredAt.Format(time.RFC1123)),
					Priority: 2,
				}
				if err := c.msgHandler.SendMessage(*msg); err != nil {
					log.Printf("error sending star notification: %v", err)
				} else {
					log.Printf("sent star notification for repo %s", repo.FullName)
				}
			}
		}
	}
	c.lastStarCheckTime = time.Now()
}

func GetGotifyPluginInfo() plugin.Info {
	return plugin.Info{
		ModulePath:  "github.com/0pandadev/gotify-github-plugin",
		Version:     "1.0.0",
		Author:      "PandaDEV",
		Website:     "https://github.com",
		Description: "Github notification plugin for Gotify",
		License:     "MIT",
		Name:        "gotify-github-plugin",
	}
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
	return c.ValidateAndSetConfig(config)
}

func (c *MyPlugin) GetDisplay(location *url.URL) string {
	return "Configure your GitHub token and polling interval below to receive notifications"
}

func main() {
	panic("this should be built as go plugin")
}
