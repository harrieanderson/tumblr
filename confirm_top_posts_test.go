package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const confirmTopText = "im so bored today idk what to do"

type topPostRow struct {
	ID        string
	Blog      string
	Followers int
	Status    string
	URL       string
	Error     string
}

// Look through the recent published posts, preserving exact IDs. An unreadable
// response is an error, never evidence that the requested post is missing.
func findConfirmedText(client *http.Client, auth *Auth, blogName string) (string, error) {
	cutoff := time.Now().Add(-48 * time.Hour).Unix()
	for offset := 0; offset < 2000; offset += 20 {
		u := fmt.Sprintf("https://www.tumblr.com/api/v2/blog/%s/posts?npf=true&limit=20&offset=%d", url.PathEscape(auth.Blog), offset)
		req, _ := http.NewRequest("GET", u, nil)
		setCommonHeaders(req, *auth)
		req.Header.Set("X-CSRF", auth.CSRF)
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		var raw map[string]any
		dec := json.NewDecoder(resp.Body)
		dec.UseNumber()
		err = dec.Decode(&raw)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("read posts: HTTP %d", resp.StatusCode)
		}
		if err != nil {
			return "", err
		}
		response, _ := raw["response"].(map[string]any)
		posts, ok := response["posts"].([]any)
		if !ok {
			return "", fmt.Errorf("missing posts array")
		}
		if len(posts) == 0 {
			return "", nil
		}
		older := false
		for _, v := range posts {
			p, ok := v.(map[string]any)
			if !ok {
				return "", fmt.Errorf("invalid post")
			}
			if ts, ok := p["timestamp"].(json.Number); ok {
				n, _ := ts.Int64()
				if n > 0 && n < cutoff && !isPinnedPost(p) {
					older = true
				}
			}
			var texts []string
			if blocks, ok := p["content"].([]any); ok {
				for _, b := range blocks {
					if m, ok := b.(map[string]any); ok {
						if s, ok := m["text"].(string); ok {
							texts = append(texts, s)
						}
					}
				}
			}
			for _, key := range []string{"summary", "body"} {
				if s, ok := p[key].(string); ok {
					texts = append(texts, s)
				}
			}
			for _, s := range texts {
				s = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(s, "<p>"), "</p>"))
				if strings.Join(strings.Fields(s), " ") == confirmTopText {
					if u, ok := p["post_url"].(string); ok && u != "" {
						return u, nil
					}
					if id := postIDFromMap(p); id != "" {
						return "https://www.tumblr.com/" + blogName + "/" + id, nil
					}
					return "", fmt.Errorf("matching post has no ID")
				}
			}
		}
		if older {
			return "", nil
		}
	}
	return "", fmt.Errorf("recent post scan limit reached")
}

func TestConfirmTopPosts(t *testing.T) {
	mode := os.Getenv("TUMBLR_CONFIRM_TOP_POSTS")
	if mode == "" {
		t.Skip("manual authorized post verification")
	}
	if err := initAccounts(); err != nil {
		t.Fatal(err)
	}
	nativeChromeClicks = false
	quietChromeLogin = true
	var rows []topPostRow
	if raw, err := os.ReadFile("top60-post-confirmation.json"); err == nil {
		if err = json.Unmarshal(raw, &rows); err != nil {
			t.Fatal(err)
		}
	} else {
		for _, a := range loggedInTopFollowerAccounts(60) {
			blog, n := bestAccountBlogStats(a.ID, loadFollowerLog(a.ID))
			rows = append(rows, topPostRow{ID: a.ID, Blog: blog, Followers: n})
		}
	}
	if len(rows) != 60 {
		t.Fatalf("expected 60 frozen targets, got %d", len(rows))
	}
	if os.Getenv("TUMBLR_CONFIRM_REFRESH_TARGETS") == "1" {
		previous := map[string]topPostRow{}
		for _, r := range rows {
			previous[r.ID] = r
		}
		rows = nil
		for _, a := range loggedInTopFollowerAccounts(60) {
			if r, ok := previous[a.ID]; ok {
				rows = append(rows, r)
			} else {
				blog, n := bestAccountBlogStats(a.ID, loadFollowerLog(a.ID))
				rows = append(rows, topPostRow{ID: a.ID, Blog: blog, Followers: n})
			}
		}
		if len(rows) != 60 {
			t.Fatalf("only %d remaining eligible accounts", len(rows))
		}
	}
	save := func() {
		raw, _ := json.MarshalIndent(rows, "", "  ")
		if err := os.WriteFile("top60-post-confirmation.json", raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	save()
	check := func(i int, repair bool) {
		row := &rows[i]
		row.Error = ""
		auth, err := loadAuthFrom(accountAuthPath(row.ID))
		client := &http.Client{Timeout: 35 * time.Second}
		if err == nil {
			err = refreshCSRF(client, &auth)
		}
		if err != nil && repair {
			err = loginChromeSession(&auth, true)
		}
		if err == nil {
			row.Blog, err = verifyAccountBlog(client, &auth)
		}
		if err != nil {
			row.Status = "blocked"
			row.Error = err.Error()
			return
		}
		found, err := findConfirmedText(client, &auth, row.Blog)
		if err != nil {
			row.Status = "blocked"
			row.Error = err.Error()
			return
		}
		if found != "" {
			row.Status = "confirmed"
			row.URL = found
			return
		}
		row.Status = "missing"
		if !repair {
			return
		}
		// Never retry an ambiguous write automatically. Check whether it landed.
		postErr := createPost(client, auth, confirmTopText)
		for attempt := 0; attempt < 3; attempt++ {
			time.Sleep(2 * time.Second)
			found, err = findConfirmedText(client, &auth, row.Blog)
			if err == nil && found != "" {
				row.Status = "posted and confirmed"
				row.URL = found
				return
			}
		}
		row.Status = "unconfirmed after attempt"
		row.Error = fmt.Sprintf("publish=%v; verification=%v", postErr, err)
	}
	if mode == "audit" {
		var wg sync.WaitGroup
		sem := make(chan struct{}, 4)
		for i := range rows {
			wg.Add(1)
			go func(i int) { defer wg.Done(); sem <- struct{}{}; defer func() { <-sem }(); check(i, false) }(i)
		}
		wg.Wait()
		save()
	} else if mode == "repair" {
		for i := range rows {
			if rows[i].Status == "confirmed" || rows[i].Status == "posted and confirmed" || rows[i].Status == "unconfirmed after attempt" {
				continue
			}
			check(i, true)
			save()
			t.Logf("%d/60 %s @%s: %s %s", i+1, rows[i].ID, rows[i].Blog, rows[i].Status, rows[i].Error)
		}
	} else {
		t.Fatal("invalid mode")
	}
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.Status]++
		if r.Status != "confirmed" && r.Status != "posted and confirmed" {
			t.Logf("%s @%s: %s %s", r.ID, r.Blog, r.Status, r.Error)
		}
	}
	t.Logf("RESULTS: %v", counts)
}
