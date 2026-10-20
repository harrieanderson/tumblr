package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

func runningUnderGoTest() bool {
	return strings.HasSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".test.exe") || strings.HasSuffix(os.Args[0], ".test")
}

// Blog UUIDs never change, even when a blog is renamed, so they are the one
// reliable way to tell whose session a cookie really is. The first account a
// blog was seen on owns it; a session owning only other accounts' blogs is
// never written into this account's auth.json.
const blogOwnersFile = "data/blog-owners.json"

var blogOwnerMu sync.Mutex

type foreignSessionError struct {
	AccountID string
	OwnerID   string
	Blog      string
}

func (e foreignSessionError) Error() string {
	return fmt.Sprintf("refusing to save %s's session (@%s) into account %s — this profile's Chrome is signed in to the wrong Tumblr", e.OwnerID, e.Blog, e.AccountID)
}

func loadBlogOwners() map[string]string {
	out := map[string]string{}
	if raw, err := os.ReadFile(blogOwnersFile); err == nil && json.Unmarshal(raw, &out) == nil && len(out) > 0 {
		return out
	}
	return seedBlogOwners()
}

// seedBlogOwners builds the map from the saved sessions; when two accounts
// hold the same blog, the older auth.json keeps it.
func seedBlogOwners() map[string]string {
	type entry struct {
		id, uuid string
		mod      time.Time
	}
	var all []entry
	dirs, _ := os.ReadDir(profilesAuthDir)
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		path := filepath.Join(profilesAuthDir, d.Name(), "auth.json")
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		var a Auth
		if raw, err := os.ReadFile(path); err != nil || json.Unmarshal(raw, &a) != nil {
			continue
		}
		if u := strings.TrimSpace(a.Blog); strings.HasPrefix(u, "t:") {
			all = append(all, entry{accountSlug(d.Name()), u, st.ModTime()})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mod.Before(all[j].mod) })
	out := map[string]string{}
	for _, e := range all {
		if _, taken := out[e.uuid]; !taken && e.id != "" {
			out[e.uuid] = e.id
		}
	}
	return out
}

func saveBlogOwners(m map[string]string) {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(blogOwnersFile), 0o755)
	_ = os.WriteFile(blogOwnersFile, append(raw, '\n'), 0o644)
}

func sidFromCookieHeader(h string) string {
	for _, part := range strings.Split(h, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && k == "sid" {
			return v
		}
	}
	return ""
}

// guardSessionOwner checks a new session before it is written for id.
func guardSessionOwner(id string, auth Auth) error {
	probe := auth
	client := &http.Client{Timeout: 15 * time.Second}
	if err := refreshCSRF(client, &probe); err != nil {
		return nil // dead or offline session: nothing to misattribute
	}
	blogs, err := listAccountBlogsDetailed(client, probe)
	if err != nil || len(blogs) == 0 {
		return nil
	}
	blogOwnerMu.Lock()
	defer blogOwnerMu.Unlock()
	owners := loadBlogOwners()
	var foreignOwner, foreignBlog string
	mine := false
	for _, b := range blogs {
		if b.UUID == "" {
			continue
		}
		switch owner := owners[b.UUID]; {
		case owner == "" || owner == id:
			mine = true
		case foreignOwner == "":
			foreignOwner, foreignBlog = owner, b.Name
		}
	}
	if !mine && foreignOwner != "" {
		return foreignSessionError{AccountID: id, OwnerID: foreignOwner, Blog: foreignBlog}
	}
	for _, b := range blogs {
		if b.UUID != "" && owners[b.UUID] == "" {
			owners[b.UUID] = id
		}
	}
	saveBlogOwners(owners)
	return nil
}

// checkSessionOwnerBeforeSave runs the guard only when the sid is changing,
// so routine saves (CSRF refresh, prefs) cost nothing.
func checkSessionOwnerBeforeSave(path string, auth Auth) error {
	if runningUnderGoTest() {
		return nil
	}
	newSID := sidFromCookieHeader(auth.Cookie)
	if newSID == "" {
		return nil
	}
	id := accountSlug(filepath.Base(filepath.Dir(filepath.Clean(path))))
	if id == "" || id == "config" || id == "profiles" {
		return nil
	}
	var old Auth
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &old)
	}
	if sidFromCookieHeader(old.Cookie) == newSID {
		return nil
	}
	return guardSessionOwner(id, auth)
}
