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
	log.Printf("DefaultConfig called")
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
	log.Printf("ValidateAndSetConfig called")
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
	log.Printf("Config set: interval=%d, watchStars=%v", conf.Interval, conf.WatchStars)
	return nil
}

func (c *MyPlugin) Enable() error {
	log.Printf("Enable called")
	if c.appToken != "" {
		log.Printf("Using custom application token")
	} else {
		c.appID = c.ctx.ID
		log.Printf("Using default application with ID: %d", c.appID)
	}
	c.enabled = true
	c.lastCheckTime = time.Now()
	if c.watchStars {
		c.lastStarCheckTime = time.Now()
	}

	c.seenNotifications = make(map[string]bool)
	c.seenStars = make(map[string]bool)

	log.Printf("Fetching initial state...")
	c.fetchInitialState()

	c.stopChannel = make(chan struct{})
	log.Printf("Starting polling routine...")
	go c.startPolling()
	return nil
}

func (c *MyPlugin) fetchInitialState() {
	log.Printf("fetchInitialState called")
	client := &http.Client{}
	req, err := http.NewRequest("GET", "https://api.github.com/notifications", nil)
	if err != nil {
		log.Printf("error creating initial github notifications request: %v", err)
		return
	}
	req.Header.Add("Authorization", "token "+c.githubToken)
	req.Header.Add("Accept", "application/vnd.github.v3+json")
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("error executing initial github notifications request: %v", err)
		return
	}
	defer resp.Body.Close()

	var notifications []GithubNotification
	if err := json.NewDecoder(resp.Body).Decode(&notifications); err != nil {
		log.Printf("error decoding initial notifications: %v", err)
		return
	}

	log.Printf("Found %d initial notifications", len(notifications))
	for _, notification := range notifications {
		c.seenNotifications[notification.ID] = true
	}

	if c.watchStars {
		log.Printf("WatchStars is enabled, fetching initial stars")
		c.fetchInitialStars()
	} else {
		log.Printf("WatchStars is disabled, skipping initial stars fetch")
	}
}

func (c *MyPlugin) fetchInitialStars() {
	log.Printf("fetchInitialStars called")
	client := &http.Client{}
	req, err := http.NewRequest("GET", "https://api.github.com/user/repos", nil)
	if err != nil {
		log.Printf("error creating initial repos request: %v", err)
		return
	}
	req.Header.Add("Authorization", "token "+c.githubToken)
	req.Header.Add("Accept", "application/vnd.github.v3+json")
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("error executing initial repos request: %v", err)
		return
	}
	defer resp.Body.Close()

	var repos []struct {
		FullName string `json:"full_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		log.Printf("error decoding initial repos: %v", err)
		return
	}

	log.Printf("Found %d repos to check for initial stars", len(repos))

	for _, repo := range repos {
		repoURL := fmt.Sprintf("https://api.github.com/repos/%s/stargazers", repo.FullName)
		req, err := http.NewRequest("GET", repoURL, nil)
		if err != nil {
			log.Printf("error creating initial stargazers request for %s: %v", repo.FullName, err)
			continue
		}
		req.Header.Add("Authorization", "token "+c.githubToken)
		req.Header.Add("Accept", "application/vnd.github.v3.star+json")
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("error executing initial stargazers request for %s: %v", repo.FullName, err)
			continue
		}

		var stars []struct {
			StarredAt time.Time `json:"starred_at"`
			User      struct {
				Login string `json:"login"`
			} `json:"user"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&stars); err != nil {
			log.Printf("error decoding initial stargazers for %s: %v", repo.FullName, err)
			resp.Body.Close()
			continue
		}
		resp.Body.Close()

		log.Printf("Found %d initial stars for repo %s", len(stars), repo.FullName)

		for _, star := range stars {
			starKey := fmt.Sprintf("%s:%s", repo.FullName, star.User.Login)
			c.seenStars[starKey] = true
		}
	}
	log.Printf("Initial stars fetch complete, tracking %d stars", len(c.seenStars))
}

func (c *MyPlugin) Disable() error {
	log.Printf("Disable called")
	if c.enabled {
		c.enabled = false
		close(c.stopChannel)
	}
	return nil
}

func (c *MyPlugin) startPolling() {
	log.Printf("startPolling called with interval %v", c.pollInterval)
	ticker := time.NewTicker(c.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			log.Printf("Ticker triggered, checking for updates")
			c.checkNotifications()
			if c.watchStars {
				log.Printf("WatchStars enabled, checking stars")
				c.checkStars()
			} else {
				log.Printf("WatchStars disabled, skipping star check")
			}
		case <-c.stopChannel:
			log.Printf("Stop signal received, exiting polling routine")
			return
		}
	}
}

func (c *MyPlugin) checkNotifications() {
	log.Printf("checkNotifications called")
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

	log.Printf("Found %d notifications to process", len(notifications))

	for _, notification := range notifications {
		log.Printf("Processing notification ID: %s, Title: %s", notification.ID, notification.Subject.Title)
		if !c.seenNotifications[notification.ID] {
			log.Printf("New notification found: %s", notification.ID)
			c.seenNotifications[notification.ID] = true

			msg := &plugin.Message{
				Title:    notification.Subject.Title,
				Message:  fmt.Sprintf("New notification in %s", notification.Repository.FullName),
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
		} else {
			log.Printf("Notification %s already seen, skipping", notification.ID)
		}
	}
}

func (c *MyPlugin) checkStars() {
	log.Printf("checkStars called")
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

	repoNames := make([]string, 0, len(repos))
	for _, repo := range repos {
		repoNames = append(repoNames, repo.FullName)
	}
	log.Printf("Checking stars for repos: %v", repoNames)

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

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("error reading stargazers response for %s: %v", repo.FullName, err)
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
			log.Printf("error unmarshalling stargazers for %s: %v", repo.FullName, err)
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
	log.Printf("GetGotifyPluginInfo called")
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
	log.Printf("NewGotifyPluginInstance called with context ID: %d", ctx.ID)
	return &MyPlugin{
		ctx:          ctx,
		pollInterval: 60 * time.Second,
		enabled:      false,
		appID:        ctx.ID,
	}
}

func (c *MyPlugin) SetMessageHandler(h plugin.MessageHandler) {
	log.Printf("SetMessageHandler called")
	c.msgHandler = h
}

func (c *MyPlugin) ApplyConfig(config any) error {
	log.Printf("ApplyConfig called")
	return c.ValidateAndSetConfig(config)
}

func (c *MyPlugin) GetDisplay(location *url.URL) string {
	log.Printf("GetDisplay called with location: %s", location.String())
	return "Configure your GitHub token and polling interval below to receive notifications"
}

func main() {
	log.Printf("main function called - this should not happen")
	panic("this should be built as go plugin")
}
