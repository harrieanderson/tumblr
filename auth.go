package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

const authFile = "config/auth.json"

type Auth struct {
	Cookie string `json:"cookie"`
	Bearer string `json:"bearer"`
	Blog   string `json:"blog"`
	CSRF   string `json:"csrf"`
}

func loadAuth() (Auth, error) {
	data, err := os.ReadFile(authFile)
	if err != nil {
		return Auth{}, err
	}
	var auth Auth
	if err := json.Unmarshal(data, &auth); err != nil {
		return Auth{}, err
	}
	return auth, nil
}

func saveAuth(auth Auth) error {
	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(authFile, data, 0600)
}

func refreshCSRF(client *http.Client, auth *Auth) error {
	url := "https://www.tumblr.com/api/v2/user/counts?unread=true&inbox=true&unread_messages=true&blog_notification_counts=true"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	setCommonHeaders(req, *auth)
	req.Header.Set("Referer", "https://www.tumblr.com/dashboard")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("csrf refresh failed: %s", resp.Status)
	}

	csrf := resp.Header.Get("X-CSRF")
	if csrf == "" {
		csrf = resp.Header.Get("X-Csrf")
	}
	if csrf == "" {
		return fmt.Errorf("no X-CSRF header in counts response (cookie may be expired)")
	}

	auth.CSRF = csrf
	return saveAuth(*auth)
}
