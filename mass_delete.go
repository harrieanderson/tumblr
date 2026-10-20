package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// wipePublishedPosts deletes published posts on the account's primary blog.
// When keepPinned is true, posts flagged is_pinned/pinned are left alone.
func wipePublishedPosts(client *http.Client, auth *Auth, accountID string, keepPinned, dryRun bool) (deleted, skipped, failed int, err error) {
	if auth == nil {
		return 0, 0, 0, fmt.Errorf("missing auth")
	}
	tag := accountSlug(accountID)
	if tag == "" {
		tag = "?"
	}
	logf := func(format string, args ...any) {
		fmt.Printf("["+tag+"] "+format+"\n", args...)
	}
	if err := ensureSession(client, auth); err != nil {
		return 0, 0, 0, err
	}
	name, err := syncAuthIdentity(client, auth)
	if err != nil || !usableBlogName(name) {
		if usableBlogName(auth.Blog) {
			name = auth.Blog
		} else {
			return 0, 0, 0, fmt.Errorf("no blog name: %v", err)
		}
	}
	blog := normalizeBlogName(name)
	logf("wiping @%s (keepPinned=%v dryRun=%v)", blog, keepPinned, dryRun)

	const page = 20
	seen := map[string]bool{}
	emptyPages := 0
	for offset := 0; emptyPages < 3; {
		raw, err := fetchBlogPostsPage(client, auth, blog, page, offset)
		if err != nil {
			return deleted, skipped, failed, err
		}
		if len(raw) == 0 {
			emptyPages++
			offset += page
			continue
		}
		emptyPages = 0
		progress := false
		for _, p := range raw {
			id := postIDFromMap(p)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			progress = true
			if keepPinned && postIsPinned(p) {
				logf("keep pinned %s", id)
				skipped++
				continue
			}
			if dryRun {
				logf("would delete %s", id)
				deleted++
				continue
			}
			if err := deleteBlogPost(client, auth, blog, id); err != nil {
				logf("delete %s: %v", id, err)
				failed++
				time.Sleep(250 * time.Millisecond)
				continue
			}
			logf("deleted %s", id)
			deleted++
			// Proxy pacing already gaps requests; no extra sleep on success.
		}
		if !progress {
			offset += page
			continue
		}
		if dryRun {
			offset += page
		} else {
			offset = 0
		}
		if deleted+skipped+failed > 5000 {
			return deleted, skipped, failed, fmt.Errorf("safety stop after 5000 actions")
		}
	}
	return deleted, skipped, failed, nil
}

func postIsPinned(m map[string]any) bool {
	for _, k := range []string{"is_pinned", "pinned", "isPinned"} {
		switch v := m[k].(type) {
		case bool:
			if v {
				return true
			}
		case string:
			if strings.EqualFold(v, "true") || v == "1" {
				return true
			}
		case float64:
			if v != 0 {
				return true
			}
		}
	}
	return false
}

func deleteBlogPost(client *http.Client, auth *Auth, blog, postID string) error {
	blogID := blog
	if !strings.Contains(blogID, ".") && !strings.HasPrefix(blogID, "t:") {
		blogID = blogID + ".tumblr.com"
	}
	// Redpop / API v2 delete (form body).
	form := url.Values{}
	form.Set("id", postID)
	u := "https://www.tumblr.com/api/v2/blog/" + url.PathEscape(blogID) + "/post/delete"
	req, err := http.NewRequest(http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	setCommonHeaders(req, *auth)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/mega-editor/published/"+normalizeBlogName(blog))

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	takeCSRF(resp.Header, auth)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	// Fallback: JSON DELETE-style path some redpop builds use.
	payload, _ := json.Marshal(map[string]any{"id": postID})
	u2 := "https://www.tumblr.com/api/v2/blog/" + url.PathEscape(blogID) + "/posts/" + url.PathEscape(postID)
	req2, err := http.NewRequest(http.MethodDelete, u2, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("%s: %s", resp.Status, truncate(string(body), 160))
	}
	setCommonHeaders(req2, *auth)
	req2.Header.Set("Content-Type", "application/json; charset=utf8")
	req2.Header.Set("X-CSRF", auth.CSRF)
	req2.Header.Set("Referer", "https://www.tumblr.com/mega-editor/published/"+normalizeBlogName(blog))
	resp2, err := client.Do(req2)
	if err != nil {
		return fmt.Errorf("delete %s then fallback: %v (%s)", resp.Status, err, truncate(string(body), 120))
	}
	defer resp2.Body.Close()
	takeCSRF(resp2.Header, auth)
	body2, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode >= 200 && resp2.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("%s / %s: %s | %s", resp.Status, resp2.Status, truncate(string(body), 100), truncate(string(body2), 100))
}

func wipeOneAccount(a Account, keepPinned, dryRun bool) error {
	// loadAuthFrom sets auth.path so proxy/CSRF/saveAuth stay on this account
	// (peekAccountAuth leaves path empty and falls back to the active account).
	auth, err := loadAuthFrom(accountAuthPath(a.ID))
	if err != nil || !authHasSID(auth) {
		fmt.Printf("[%s] skip: no session\n", a.ID)
		return nil
	}
	client := newAuthClient(&auth)
	del, skip, fail, err := wipePublishedPosts(client, &auth, a.ID, keepPinned, dryRun)
	fmt.Printf("[%s] done deleted=%d kept=%d failed=%d\n", a.ID, del, skip, fail)
	return err
}

func runWipePosts(keepPinned, dryRun, fullySetup bool, onlyAccount string, parallel int) error {
	// Dead sessions auto-relogin via recoverSession (serialized); keep native
	// clicks off so the login window can run without stealing the mouse focus fight.
	nativeChromeClicks = false
	boostProxyPaceForWipe()
	var accs []Account
	if fullySetup {
		accs = loggedInFullySetupAccounts()
	} else if only := strings.TrimSpace(onlyAccount); only != "" {
		a, ok := findAccount(only)
		if !ok {
			return fmt.Errorf("unknown account %s", only)
		}
		accs = []Account{a}
	} else {
		accs = enabledAccounts()
	}
	// Prefer accounts that still have a session on disk.
	var ready []Account
	for _, a := range accs {
		auth, err := loadAuthFrom(accountAuthPath(a.ID))
		if err == nil && authHasSID(auth) {
			ready = append(ready, a)
		} else {
			fmt.Printf("[%s] skip: no session\n", a.ID)
		}
	}
	accs = ready
	if len(accs) == 0 {
		return fmt.Errorf("no logged-in accounts to wipe")
	}
	if parallel <= 0 {
		parallel = 1
	}
	if parallel > len(accs) {
		parallel = len(accs)
	}
	fmt.Printf("Mass-delete published posts on %d account(s) parallel=%d (keepPinned=%v dryRun=%v)\n",
		len(accs), parallel, keepPinned, dryRun)
	if parallel <= 1 {
		var first error
		for i, a := range accs {
			fmt.Printf("\n======== wipe posts: %s (%d/%d) ========\n", a.ID, i+1, len(accs))
			if err := wipeOneAccount(a, keepPinned, dryRun); err != nil && first == nil {
				first = err
			}
		}
		return first
	}
	// No enqueue stagger — fill all workers immediately.
	return runForAccountsParallelLimitedStagger("wipe posts", accs, parallel, 0, func(a Account) error {
		return wipeOneAccount(a, keepPinned, dryRun)
	})
}
