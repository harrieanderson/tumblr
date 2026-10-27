package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// lookAppliedState records when THIS bot successfully uploaded Zoey media.
// Page/API "has some custom avatar/banner" is not enough — accounts often
// already have unrelated pics, and we used to skip uploading ours.
type lookAppliedState struct {
	AvatarAt string `json:"avatarAt,omitempty"`
	BannerAt string `json:"bannerAt,omitempty"`
	Blog     string `json:"blog,omitempty"`
}

func lookAppliedPath(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		if acc, ok := currentAccount(); ok {
			id = acc.ID
		}
	}
	if id == "" {
		return filepath.Join("data", "look-applied.json")
	}
	return filepath.Join(profilesDataDir, id, "look-applied.json")
}

func loadLookApplied(id string) lookAppliedState {
	var st lookAppliedState
	b, err := os.ReadFile(lookAppliedPath(id))
	if err != nil {
		return st
	}
	_ = json.Unmarshal(b, &st)
	return st
}

func saveLookApplied(id string, st lookAppliedState) {
	path := lookAppliedPath(id)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, append(b, '\n'), 0o644)
}

func markLookAvatarApplied(blog string) {
	id := ""
	if acc, ok := currentAccount(); ok {
		id = acc.ID
	}
	st := loadLookApplied(id)
	st.AvatarAt = time.Now().UTC().Format(time.RFC3339)
	if b := normalizeBlogName(blog); usableBlogName(b) {
		st.Blog = b
	}
	saveLookApplied(id, st)
}

func markLookBannerApplied(blog string) {
	id := ""
	if acc, ok := currentAccount(); ok {
		id = acc.ID
	}
	st := loadLookApplied(id)
	st.BannerAt = time.Now().UTC().Format(time.RFC3339)
	if b := normalizeBlogName(blog); usableBlogName(b) {
		st.Blog = b
	}
	saveLookApplied(id, st)
}

func lookAvatarAppliedByUs() bool {
	id := ""
	if acc, ok := currentAccount(); ok {
		id = acc.ID
	}
	return strings.TrimSpace(loadLookApplied(id).AvatarAt) != ""
}

func lookBannerAppliedByUs() bool {
	id := ""
	if acc, ok := currentAccount(); ok {
		id = acc.ID
	}
	return strings.TrimSpace(loadLookApplied(id).BannerAt) != ""
}

func bannerURLPersisted(header string) bool {
	u := strings.ToLower(strings.TrimSpace(header))
	return strings.Contains(u, "media.tumblr.com") && !strings.Contains(u, "default_header")
}
