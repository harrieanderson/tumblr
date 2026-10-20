package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	authFile        = "config/auth.json"
	authExampleFile = "config/auth.example.json"
)

type Auth struct {
	Cookie string `json:"cookie"`
	Bearer string `json:"bearer"`
	Blog   string `json:"blog"`
	CSRF   string `json:"csrf"`
	path   string `json:"-"`
}

func prepareWorkspace() error {
	for _, dir := range []string{"config", "data", "pics/posted", "posts", "scraped", chromeDataRoot(), chromeProfilesDir(), profilesAuthDir, profilesDataDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}
	if _, err := os.Stat("posts/queue.txt"); os.IsNotExist(err) {
		if err := os.WriteFile("posts/queue.txt", []byte(""), 0644); err != nil {
			return err
		}
	}
	if err := initAccounts(); err != nil {
		return err
	}
	if _, err := os.Stat(authFile); os.IsNotExist(err) && !usingIsolatedProfiles() {
		example, err := os.ReadFile(authExampleFile)
		if err != nil {
			example = []byte("{\n  \"cookie\": \"\",\n  \"bearer\": \"\",\n  \"blog\": \"YOUR_BLOG_NAME\",\n  \"csrf\": \"\"\n}\n")
		}
		if err := os.WriteFile(authFile, example, 0600); err != nil {
			return err
		}
		fmt.Println("Created", authFile, "— run: go run . -login")
	}
	return nil
}

func blogConfigured(name string) bool {
	n := strings.TrimSpace(name)
	return n != "" && !strings.EqualFold(n, "YOUR_BLOG_NAME")
}

func loadAuth() (Auth, error) {
	return loadAuthFrom(authFilePath())
}

func loadAuthFrom(path string) (Auth, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Auth{path: path}, nil
		}
		return Auth{}, err
	}
	var auth Auth
	if err := json.Unmarshal(data, &auth); err != nil {
		return Auth{}, err
	}
	auth.path = path
	return auth, nil
}

func saveAuth(auth Auth) error {
	path := auth.path
	if path == "" {
		path = authFilePath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := checkSessionOwnerBeforeSave(path, auth); err != nil {
		fmt.Println(err)
		return err
	}
	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func maybeFillBlog(client *http.Client, auth *Auth) {
	if blogConfigured(auth.Blog) {
		return
	}
	if err := fillBlogFromAPI(client, auth); err != nil {
		fmt.Println("Could not auto-detect blog name:", err)
		fmt.Println("Set \"blog\" in config/auth.json to your Tumblr blog name (the part before .tumblr.com).")
		return
	}
	fmt.Println("Using blog:", auth.Blog)
}

func fillBlogFromAPI(client *http.Client, auth *Auth) error {
	req, err := http.NewRequest("GET", "https://www.tumblr.com/api/v2/user/info", nil)
	if err != nil {
		return err
	}
	setCommonHeaders(req, *auth)
	req.Header.Set("Referer", "https://www.tumblr.com/dashboard")
	if auth.CSRF != "" {
		req.Header.Set("X-CSRF", auth.CSRF)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("user/info failed: %s", resp.Status)
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return err
	}
	respObj, _ := raw["response"].(map[string]any)
	if respObj == nil {
		respObj = raw
	}
	user, _ := respObj["user"].(map[string]any)
	blogs, _ := user["blogs"].([]any)
	name := ""
	for _, b := range blogs {
		m, _ := b.(map[string]any)
		if m == nil {
			continue
		}
		n := anyToString(m["name"])
		if n == "" {
			continue
		}
		if primary, ok := m["primary"].(bool); ok && primary {
			name = n
			break
		}
		if name == "" {
			name = n
		}
	}
	if name == "" {
		name = anyToString(user["name"])
	}
	if name == "" {
		return fmt.Errorf("no blog name in user/info")
	}
	auth.Blog = name
	return saveAuth(*auth)
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
	takeCSRF(resp.Header, auth)
	if auth.CSRF == "" {
		if csrf != "" {
			auth.CSRF = csrf
		} else {
			return fmt.Errorf("no X-CSRF header in counts response (cookie may be expired)")
		}
	}
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
	if err := rejectForeignBlogSession(auth, name); err != nil {
		clearAuthSession(auth)
		_ = saveAuth(*auth)
		return "", err
	}
	auth.Blog = uuid
	if err := saveAuth(*auth); err != nil {
		return name, err
	}
	return name, nil
}

// rejectForeignBlogSession blocks saving a Tumblr session whose primary blog
// is another account's assigned username (cookie mix across profiles).
func rejectForeignBlogSession(auth *Auth, primaryName string) error {
	id := accountIDFromAuth(*auth)
	primaryName = normalizeBlogName(primaryName)
	if id == "" || primaryName == "" {
		return nil
	}
	want := assignedBlogName(id)
	if want != "" && want == primaryName {
		return nil
	}
	for otherID, otherName := range loadNameMap(assignedUsernamesFile) {
		otherID = accountSlug(otherID)
		otherName = normalizeBlogName(otherName)
		if otherID == "" || otherName == "" || otherID == id {
			continue
		}
		if otherName == primaryName {
			label := "its own blog"
			if want != "" {
				label = "@" + want
			}
			return foreignBlogSessionError{
				AccountID: id,
				GotBlog:   primaryName,
				OwnerID:   otherID,
				WantBlog:  label,
			}
		}
	}
	return nil
}

type foreignBlogSessionError struct {
	AccountID string
	GotBlog   string
	OwnerID   string
	WantBlog  string
}

func (e foreignBlogSessionError) Error() string {
	return fmt.Sprintf("session is @%s (account %s) — not %s; run -login for this account", e.GotBlog, e.OwnerID, e.WantBlog)
}

func isForeignBlogSession(err error) bool {
	_, ok := err.(foreignBlogSessionError)
	return ok
}

func clearAuthSession(auth *Auth) {
	if auth == nil {
		return
	}
	auth.Cookie = ""
	auth.Blog = ""
	auth.CSRF = ""
	auth.Bearer = ""
}
