package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// go run . -probe-msg
func runProbeMsg() {
	auth, err := loadAuth()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("syncing cookies from chrome…")
	if err := syncCookiesFromChrome(&auth); err != nil {
		fmt.Println("cookie sync:", err)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	if err := ensureSession(client, &auth); err != nil {
		fmt.Println("session:", err)
		fmt.Println("trying login…")
		if err := loginWithRealChrome(&auth); err != nil {
			fmt.Println("login fail:", err)
			return
		}
		if err := ensureSession(client, &auth); err != nil {
			fmt.Println("session still dead:", err)
			return
		}
	}

	// Warm messaging-related counts (same as browser).
	warm := "https://www.tumblr.com/api/v2/user/counts?unread=true&inbox=true&unread_messages=true&blog_notification_counts=true"
	status, body := doGET(client, auth, warm, "https://www.tumblr.com/dashboard")
	fmt.Printf("counts: %s (%d bytes)\n", status, len(body))

	status, body = doGET(client, auth, "https://www.tumblr.com/messaging", "https://www.tumblr.com/")
	fmt.Printf("messaging html: %s (%d bytes)\n", status, len(body))

	base := "https://www.tumblr.com/api/v2/conversations"
	variants := []struct {
		name string
		url  string
	}{
		{"with_participant_fields", withQuery(base, map[string]string{
			"limit": "20", "participant": auth.Blog,
			"fields[blogs]": "?avatar,name,?seconds_since_last_activity,url,?blog_view_url,?uuid,?theme,?description_npf,?is_adult,?primary",
		})},
		{"participant_only", withQuery(base, map[string]string{
			"limit": "20", "participant": auth.Blog,
		})},
		{"no_participant", withQuery(base, map[string]string{"limit": "20"})},
		{"participant_name", withQuery(base, map[string]string{
			"limit": "20", "participant": "lillycosmic",
		})},
	}
	for _, v := range variants {
		status, body = doGET(client, auth, v.url, "https://www.tumblr.com/messaging")
		fmt.Printf("%s: %s %s\n", v.name, status, truncate(string(body), 140))
		time.Sleep(800 * time.Millisecond)
	}
}

func withQuery(raw string, kv map[string]string) string {
	u, _ := url.Parse(raw)
	q := u.Query()
	for k, v := range kv {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func doGET(client *http.Client, auth Auth, rawURL, referer string) (string, []byte) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return err.Error(), nil
	}
	setCommonHeaders(req, auth)
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", referer)
	if strings.Contains(rawURL, "/api/") {
		req.Header.Set("Accept", "application/json;format=camelcase")
	} else {
		req.Header.Set("Accept", "text/html,application/xhtml+xml")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err.Error(), nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if csrf := resp.Header.Get("X-CSRF"); csrf != "" {
		auth.CSRF = csrf
		_ = saveAuth(auth)
	}
	return resp.Status, body
}
