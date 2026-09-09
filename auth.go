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

// syncAuthIdentity pulls /api/v2/user/info and sets auth.Blog to the primary blog UUID.
func syncAuthIdentity(client *http.Client, auth *Auth) (blogName string, err error) {
	req, err := http.NewRequest("GET", "https://www.tumblr.com/api/v2/user/info", nil)
	if err != nil {
		return "", err
	}
	setCommonHeaders(req, *auth)
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/dashboard")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", resp.Status, truncate(string(body), 120))
	}
	if csrf := resp.Header.Get("X-CSRF"); csrf != "" {
		auth.CSRF = csrf
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", err
	}
	response, _ := raw["response"].(map[string]any)
	user, _ := response["user"].(map[string]any)
	if user == nil {
		return "", fmt.Errorf("missing user in info response")
	}

	blogs, _ := user["blogs"].([]any)
	var uuid, name string
	for _, b := range blogs {
		bm, ok := b.(map[string]any)
		if !ok {
			continue
		}
		n := anyToString(bm["name"])
		u := firstNonEmpty(anyToString(bm["uuid"]), anyToString(bm["blog_uuid"]))
		primary := bm["primary"] == true
		if u == "" {
			continue
		}
		if primary || uuid == "" {
			uuid, name = u, n
		}
		if primary {
			break
		}
	}
	if uuid == "" {
		return "", fmt.Errorf("no blog uuid in user/info")
	}
	auth.Blog = uuid
	if err := saveAuth(*auth); err != nil {
		return name, err
	}
	return name, nil
}
