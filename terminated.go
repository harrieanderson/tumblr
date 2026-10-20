package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

const terminatedAccountsFile = "config/terminated-accounts.json"

type terminatedAccountError struct {
	ID    string
	Email string
}

func (e terminatedAccountError) Error() string {
	id := strings.TrimSpace(e.ID)
	if id == "" {
		id = accountSlug(e.Email)
	}
	if id == "" {
		return "Tumblr terminated this account"
	}
	return "Tumblr terminated account " + id
}

func isTerminated(err error) bool {
	var t terminatedAccountError
	return errors.As(err, &t)
}

func isTumblrTerminatedURL(raw string) bool {
	u := strings.ToLower(strings.TrimSpace(raw))
	i := strings.Index(u, "tumblr.com")
	if u == "" || i < 0 {
		return false
	}
	return strings.HasPrefix(u[i+len("tumblr.com"):], "/terminated")
}

// deadTextCountsOn says whether "account terminated/deleted" text on this
// page is about us. Messages, posts and blogs show it for other people (a DM
// with someone who deleted their account), so only account-level pages count.
func deadTextCountsOn(rawURL string) bool {
	u := strings.ToLower(strings.TrimSpace(rawURL))
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	var path string
	switch {
	case strings.HasPrefix(u, "www.tumblr.com"):
		path = u[len("www.tumblr.com"):]
	case strings.HasPrefix(u, "tumblr.com"):
		path = u[len("tumblr.com"):]
	default:
		return false
	}
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	path = strings.TrimSuffix(path, "/")
	switch {
	case path == "", path == "/dashboard", path == "/login", path == "/register", path == "/account",
		strings.HasPrefix(path, "/settings/account"), strings.HasPrefix(path, "/login"), strings.HasPrefix(path, "/terminated"):
		return true
	}
	return false
}

func isTumblrDeadAccountText(s string) bool {
	t := strings.ToLower(s)
	return strings.Contains(t, "account has been terminated") ||
		strings.Contains(t, "you have deleted your account") ||
		strings.Contains(t, "this account has been deleted") ||
		strings.Contains(t, "this account was deleted")
}

var (
	terminatedMu   sync.Mutex
	terminatedMemo map[string]bool
)

func readTerminatedFile() map[string]bool {
	out := map[string]bool{}
	raw, err := os.ReadFile(terminatedAccountsFile)
	if err != nil {
		return out
	}
	var ids []string
	if json.Unmarshal(raw, &ids) != nil {
		return out
	}
	for _, id := range ids {
		id = accountSlug(id)
		if id != "" {
			out[id] = true
		}
	}
	return out
}

func accountIsTerminated(id string) bool {
	id = accountSlug(id)
	if id == "" {
		return false
	}
	terminatedMu.Lock()
	defer terminatedMu.Unlock()
	if terminatedMemo == nil {
		terminatedMemo = readTerminatedFile()
	}
	return terminatedMemo[id]
}

func markAccountTerminated(id string) {
	id = accountSlug(id)
	if id == "" {
		return
	}
	terminatedMu.Lock()
	defer terminatedMu.Unlock()
	if terminatedMemo == nil {
		terminatedMemo = readTerminatedFile()
	}
	if terminatedMemo[id] {
		return
	}
	terminatedMemo[id] = true
	sessionDead.Store(id, true)
	var ids []string
	for k := range terminatedMemo {
		ids = append(ids, k)
	}
	sort.Strings(ids)
	raw, err := json.MarshalIndent(ids, "", "  ")
	if err == nil {
		_ = os.WriteFile(terminatedAccountsFile, append(raw, '\n'), 0644)
	}
	fmt.Println("Tumblr closed", id, "— skipping. I will not try to log this account in again.")
}
