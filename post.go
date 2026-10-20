package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func createPost(client *http.Client, auth Auth, text string) error {
	name, err := verifyAccountBlog(client, &auth)
	if err != nil {
		return err
	}
	fmt.Printf("Verified text post target: @%s (account %s)\n", name, accountIDFromAuth(auth))
	url := "https://www.tumblr.com/api/v2/blog/" + auth.Blog + "/posts"

	payload := map[string]any{
		"layout": []map[string]any{
			{"type": "rows", "display": []map[string]any{{"blocks": []int{0}}}},
		},
		"state":      "published",
		"hide_trail": false,
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
		"tags":                       "",
		"has_community_label":        false,
		"community_label_categories": []any{},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	setCommonHeaders(req, auth)
	req.Header.Set("Content-Type", "application/json; charset=utf8")
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/new/text")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	fmt.Println("Status:", resp.Status)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fmt.Println(truncate(string(respBody), 240))
		return fmt.Errorf("post failed: %s — %s", resp.Status, truncate(string(respBody), 180))
	}
	recordPost(ledgerEntry{
		Account: accountIDFromAuth(auth),
		Blog:    name,
		Kind:    "text",
		Text:    text,
		PostID:  parseCreatedPostID(respBody),
	})
	return nil
}

// publishTextOnTopFollowerBlogs posts plain text on the N logged-in accounts
// with the most followers (unique blogs), targeting each account's assigned Zoey blog.
func publishTextOnTopFollowerBlogs(text string, n int) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("empty post text")
	}
	if n <= 0 {
		n = 21
	}
	accs := loggedInTopFollowerAccounts(n)
	fmt.Printf("Text post on top %d follower blogs (%d found)\n", n, len(accs))
	fmt.Println("Text:", truncate(text, 120))
	for i, a := range accs {
		blog, fol := bestAccountBlogStats(a.ID, loadFollowerLog(a.ID))
		fmt.Printf("  %d) %s  @%s  %d followers\n", i+1, a.ID, blog, fol)
	}
	return runForEachAccountList("text post", accs, func() error {
		return postTextOnAssignedBlog(text)
	})
}

func postTextOnAssignedBlog(text string) error {
	auth, err := loadAuth()
	if err != nil {
		return err
	}
	if !authHasSID(auth) {
		return fmt.Errorf("not logged in")
	}
	acc, _ := currentAccount()
	if prev, done := accountAlreadyPosted(acc.ID, text, ""); done {
		fmt.Printf("Already posted this on @%s (%s) — skipping\n", prev.Blog, prev.Time[:10])
		return nil
	}
	client := newAuthClient(&auth)
	if err := ensureSession(client, &auth); err != nil {
		return err
	}
	name, _ := syncAuthIdentity(client, &auth)
	want := preferredMessagingBlog(acc.ID, name)
	if usableBlogName(want) {
		if n, err := pinMessagingBlog(client, &auth, want); err == nil && n != "" {
			name = n
		}
	}
	label := name
	if label == "" {
		label = auth.Blog
	}
	fmt.Printf("Posting on @%s…\n", label)
	if err := createPost(client, auth, text); err != nil {
		if isUnauthorized(err) || isForbidden(err) {
			if err2 := ensureSession(client, &auth); err2 == nil {
				if usableBlogName(want) {
					_, _ = pinMessagingBlog(client, &auth, want)
				}
				err = createPost(client, auth, text)
			}
		}
		return err
	}
	fmt.Printf("OK @%s\n", label)
	return nil
}
