package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"
)

func guiAuth(hub *logHub) (Auth, *http.Client, error) {
	if acc, ok := currentAccount(); ok {
		hub.broadcast("Using account " + acc.ID + " (" + acc.Email + ")")
	}
	auth, err := loadAuth()
	if err != nil {
		return Auth{}, nil, err
	}
	if !authHasSID(auth) {
		if err := syncCookiesFromChrome(&auth); err != nil {
			return Auth{}, nil, fmt.Errorf("not logged in: %w (run tumblr.exe -login first)", err)
		}
	} else {
		hub.broadcast("Using saved cookies")
	}
	client := &http.Client{Timeout: 45 * time.Second}
	if err := ensureSession(client, &auth); err != nil {
		return Auth{}, nil, err
	}
	return auth, client, nil
}

func guiFollowAndLike(hub *logHub, maxFollows, likePosts int) error {
	acc, _ := currentAccount()
	return runFollowLikersOn(acc, maxFollows, likePosts, hub.broadcast)
}

func guiLikeNSFW(hub *logHub, count int) error {
	auth, client, err := guiAuth(hub)
	if err != nil {
		return err
	}
	persistMaturePref(&auth)

	queries := []string{"nsfw", "lewd", "thirst", "gay nsfw", "smut"}
	var posts []ScrapedPost
	for _, q := range queries {
		hub.broadcast("Searching: " + q)
		found, err := scrapeRecentPosts(client, auth, q, count*3)
		if err != nil || len(found) == 0 {
			found, err = scrapeTopPosts(client, auth, q, count*3)
		}
		if err != nil {
			hub.broadcast("  search fail: " + err.Error())
			continue
		}
		if len(found) > 0 {
			posts = found
			break
		}
		hub.broadcast("  empty — trying next query")
	}
	if len(posts) == 0 {
		return fmt.Errorf("no NSFW posts found — check Safe Mode / mature content")
	}

	liked := 0
	seen := map[string]bool{}
	for _, p := range posts {
		if liked >= count {
			break
		}
		if p.ID == "" || seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		if p.ReblogKey == "" {
			if enriched, e2 := fetchSinglePost(client, auth, p); e2 == nil {
				p = enriched
			}
		}
		if p.ReblogKey == "" {
			continue
		}
		hub.broadcast(fmt.Sprintf("Liking %s/%s — %s", p.Blog, p.ID, truncate(p.Summary, 40)))
		if err := likePost(client, auth, p.ID, p.ReblogKey); err != nil {
			hub.broadcast("  like fail: " + err.Error())
			continue
		}
		liked++
		time.Sleep(time.Duration(randRange(4, 12)) * time.Second)
	}
	if liked == 0 {
		return fmt.Errorf("could not like any NSFW post")
	}
	hub.broadcast(fmt.Sprintf("Liked %d NSFW posts", liked))
	return nil
}

func guiTextPost(hub *logHub, text string) error {
	auth, client, err := guiAuth(hub)
	if err != nil {
		return err
	}
	hub.broadcast("Posting text…")
	if err := createPost(client, auth, text); err != nil {
		return err
	}
	hub.broadcast("Text post published")
	return nil
}

func guiPhotoPost(hub *logHub, path, caption string) error {
	auth, client, err := guiAuth(hub)
	if err != nil {
		return err
	}
	hub.broadcast("Posting photo…")
	if caption != "" {
		hub.broadcast("Caption: " + truncate(caption, 80))
	}
	if err := createPhotoPost(client, auth, path, caption); err != nil {
		return err
	}
	hub.broadcast("Photo post published")
	return nil
}

func guiReblog(hub *logHub, category string, count int) error {
	auth, client, err := guiAuth(hub)
	if err != nil {
		return err
	}
	cfg := loadHumanizeConfigFile()

	var queries []string
	switch strings.ToLower(category) {
	case "cats":
		queries = []string{"cats", "cute cats", "kitten"}
	case "nature":
		queries = []string{"nature", "landscape", "flowers"}
	case "cute":
		queries = []string{"cute", "wholesome", "aww"}
	case "horny", "nsfw":
		queries = []string{"nsfw", "thirst", "lewd"}
	default:
		cat := pickCategory(cfg.Categories)
		queries = cat.Queries
		hub.broadcast("Mix category picked: " + cat.Name)
	}
	if len(queries) == 0 {
		queries = []string{"cute"}
	}
	q := queries[rand.Intn(len(queries))]
	hub.broadcast("Reblog search: " + q)

	posts, err := scrapeRecentPosts(client, auth, q, count*3)
	if err != nil || len(posts) == 0 {
		posts, err = scrapeTopPosts(client, auth, q, count*3)
	}
	if err != nil {
		return err
	}

	reblogged := loadStringSet(rebloggedPath())
	done := 0
	for _, p := range posts {
		if done >= count {
			break
		}
		if p.ID == "" || reblogged[p.ID] {
			continue
		}
		if p.ReblogKey == "" {
			if enriched, e2 := fetchSinglePost(client, auth, p); e2 == nil {
				p = enriched
			}
		}
		if p.ReblogKey == "" {
			continue
		}
		hub.broadcast(fmt.Sprintf("Reblogging %s/%s — %s", p.Blog, p.ID, truncate(p.Summary, 40)))
		if err := reblogPost(client, auth, p); err != nil {
			hub.broadcast("  reblog fail: " + err.Error())
			continue
		}
		reblogged[p.ID] = true
		_ = saveStringSet(rebloggedPath(), reblogged)
		done++
		time.Sleep(time.Duration(randRange(8, 20)) * time.Second)
	}
	hub.broadcast(fmt.Sprintf("Reblogged %d posts", done))
	return nil
}

func loadHumanizeConfigFile() HumanizeConfig {
	cfg := defaultHumanizeConfig()
	raw, err := os.ReadFile(humanizeConfigFile)
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(raw, &cfg)
	normalizeHumanizeConfig(&cfg)
	return cfg
}
