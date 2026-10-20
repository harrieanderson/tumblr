package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Seed blogs' newest posts get walked by every pass, so their likers run out
// within days. We page further back into each seed and remember which posts
// have nobody fresh left, so later passes skip straight past them.
const (
	seedPostsFile     = "data/seed-posts.json"
	seedMaxDepth      = 200 // posts back per seed blog
	seedPageSize      = 20  // Tumblr's max per request
	seedFreshMin      = 3   // fewer fresh engagers than this = used up
	seedUsedUpForDays = 30
)

type seedPostRecord struct {
	At    time.Time `json:"at"`
	Fresh int       `json:"fresh"`
}

var seedPostsMu sync.Mutex

func seedPostKey(blog, id string) string {
	return strings.ToLower(strings.TrimSpace(blog)) + "/" + strings.TrimSpace(id)
}

func loadSeedPosts() map[string]seedPostRecord {
	m := map[string]seedPostRecord{}
	if raw, err := os.ReadFile(seedPostsFile); err == nil {
		_ = json.Unmarshal(raw, &m)
	}
	return m
}

func seedPostUsedUp(blog, id string) bool {
	seedPostsMu.Lock()
	defer seedPostsMu.Unlock()
	r, ok := loadSeedPosts()[seedPostKey(blog, id)]
	return ok && r.Fresh < seedFreshMin && time.Since(r.At) < seedUsedUpForDays*24*time.Hour
}

func recordSeedPostFreshness(blog, id string, fresh int) {
	seedPostsMu.Lock()
	defer seedPostsMu.Unlock()
	m := loadSeedPosts()
	for k, r := range m {
		if time.Since(r.At) > seedUsedUpForDays*24*time.Hour {
			delete(m, k)
		}
	}
	m[seedPostKey(blog, id)] = seedPostRecord{At: time.Now(), Fresh: fresh}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(seedPostsFile), 0o755)
	_ = os.WriteFile(seedPostsFile, append(raw, '\n'), 0o644)
}

// freshEngagers counts engagers no account has touched yet (ignoring this
// account's liker slice, so the number means the same for every account).
func freshEngagers(names []string, postBlog string) int {
	touched := interactedSnapshot()
	n := 0
	for _, name := range names {
		blog := strings.TrimSpace(name)
		if blog == "" || strings.EqualFold(blog, postBlog) || touched[strings.ToLower(blog)] {
			continue
		}
		if _, ours := managedByUs(blog); ours {
			continue
		}
		n++
	}
	return n
}
