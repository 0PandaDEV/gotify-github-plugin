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
	seenNotifications map[string]bool
	seenStars         map[string]bool
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
	} else {
		c.appID = c.ctx.ID
	}
	c.enabled = true
	c.lastCheckTime = time.Now()
	if c.watchStars {
		c.lastStarCheckTime = time.Now()
	}

	c.seenNotifications = make(map[string]bool)
	c.seenStars = make(map[string]bool)

	c.fetchInitialState()

	c.stopChannel = make(chan struct{})
	go c.startPolling()
	return nil
}

func (c *MyPlugin) fetchInitialState() {
	client := &http.Client{}
	req, err := http.NewRequest("GET", "https://api.github.com/notifications", nil)
	if err != nil {
		return
	}
	req.Header.Add("Authorization", "token "+c.githubToken)
	req.Header.Add("Accept", "application/vnd.github.v3+json")
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	var notifications []GithubNotification
	if err := json.NewDecoder(resp.Body).Decode(&notifications); err != nil {
		return
	}

	for _, notification := range notifications {
		c.seenNotifications[notification.ID] = true
	}

	if c.watchStars {
		c.fetchInitialStars()
	}
}

func (c *MyPlugin) fetchInitialStars() {
	client := &http.Client{}
	req, err := http.NewRequest("GET", "https://api.github.com/user/repos", nil)
	if err != nil {
		return
	}
	req.Header.Add("Authorization", "token "+c.githubToken)
	req.Header.Add("Accept", "application/vnd.github.v3+json")
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	var repos []struct {
		FullName string `json:"full_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		return
	}

	for _, repo := range repos {
		repoURL := fmt.Sprintf("https://api.github.com/repos/%s/stargazers", repo.FullName)
		req, err := http.NewRequest("GET", repoURL, nil)
		if err != nil {
			continue
		}
		req.Header.Add("Authorization", "token "+c.githubToken)
		req.Header.Add("Accept", "application/vnd.github.v3.star+json")
		resp, err := client.Do(req)
		if err != nil {
			continue
		}

		var stars []struct {
			StarredAt time.Time `json:"starred_at"`
			User      struct {
				Login string `json:"login"`
			} `json:"user"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&stars); err != nil {
			resp.Body.Close()
			continue
		}
		resp.Body.Close()

		for _, star := range stars {
			starKey := fmt.Sprintf("%s:%s", repo.FullName, star.User.Login)
			c.seenStars[starKey] = true
		}
	}
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
		return
	}
	req.Header.Add("Authorization", "token "+c.githubToken)
	req.Header.Add("Accept", "application/vnd.github.v3+json")
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	var notifications []GithubNotification
	if err := json.Unmarshal(body, &notifications); err != nil {
		return
	}

	for _, notification := range notifications {
		if !c.seenNotifications[notification.ID] {
			log.Printf("New notification found: %s", notification.ID)
			c.seenNotifications[notification.ID] = true

			notificationType := ""
			switch notification.Subject.Type {
			case "Issue":
				notificationType = "Issue"
			case "PullRequest":
				notificationType = "PR"
			case "Release":
				notificationType = "Release"
			case "Discussion":
				notificationType = "Discussion"
			default:
				notificationType = notification.Subject.Type
			}

			msg := &plugin.Message{
				Title:    fmt.Sprintf("[%s] %s", notificationType, notification.Subject.Title),
				Message:  fmt.Sprintf("New %s notification in %s", notificationType, notification.Repository.FullName),
				Priority: 2,
				Extras: map[string]interface{}{
					"client::notification": map[string]interface{}{
						"click": map[string]interface{}{
							"url": notification.Subject.URL,
						},
					},
				},
			}
			if err := c.msgHandler.SendMessage(*msg); err != nil {
				log.Printf("error sending github notification: %v", err)
			} else {
				log.Printf("sent github notification: %s", notification.Subject.Title)
			}
		}
	}
}

func (c *MyPlugin) checkStars() {
	client := &http.Client{}
	req, err := http.NewRequest("GET", "https://api.github.com/user/repos", nil)
	if err != nil {
		return
	}
	req.Header.Add("Authorization", "token "+c.githubToken)
	req.Header.Add("Accept", "application/vnd.github.v3+json")
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var repos []struct {
		FullName string `json:"full_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		return
	}

	for _, repo := range repos {
		repoURL := fmt.Sprintf("https://api.github.com/repos/%s/stargazers", repo.FullName)
		req, err := http.NewRequest("GET", repoURL, nil)
		if err != nil {
			continue
		}
		req.Header.Add("Authorization", "token "+c.githubToken)
		req.Header.Add("Accept", "application/vnd.github.v3.star+json")
		resp, err := client.Do(req)
		if err != nil {
			continue
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			resp.Body.Close()
			continue
		}
		resp.Body.Close()

		var stars []struct {
			StarredAt time.Time `json:"starred_at"`
			User      struct {
				Login string `json:"login"`
			} `json:"user"`
		}

		if err := json.Unmarshal(body, &stars); err != nil {
			continue
		}

		for _, star := range stars {
			starKey := fmt.Sprintf("%s:%s", repo.FullName, star.User.Login)
			if !c.seenStars[starKey] {
				log.Printf("New star detected: %s starred %s", star.User.Login, repo.FullName)
				c.seenStars[starKey] = true

				msg := &plugin.Message{
					Title:    "New Star",
					Message:  fmt.Sprintf("Repo %s received a star from %s", repo.FullName, star.User.Login),
					Priority: 2,
					Extras: map[string]interface{}{
						"client::notification": map[string]interface{}{
							"click": map[string]interface{}{
								"url": "https://github.com/" + repo.FullName,
							},
						},
					},
				}
				if err := c.msgHandler.SendMessage(*msg); err != nil {
					log.Printf("error sending star notification: %v", err)
				} else {
					log.Printf("sent star notification for repo %s", repo.FullName)
				}
			}
		}
	}
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
