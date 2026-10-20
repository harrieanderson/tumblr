package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNormalizeSeedBlogsFromURLs(t *testing.T) {
	got := normalizeSeedBlogs([]string{
		"https://www.tumblr.com/daddysbunnyslutt",
		"https://www.tumblr.com/makeawishonlove",
		"https://www.tumblr.com/melodyofalba",
		"https://www.tumblr.com/brunos-costa",
		"https://www.tumblr.com/theonlygoddesss",
		"daddysbunnyslutt",
	})
	if len(got) != 5 {
		t.Fatalf("dedupe: %v", got)
	}
}

func TestNormalizeFollowQueryFromSearchURL(t *testing.T) {
	got := normalizeFollowQuery("https://www.tumblr.com/search/attention%20wh0r3")
	if got != "attention wh0r3" {
		t.Fatalf("got %q", got)
	}
	got = normalizeFollowQuery("https://www.tumblr.com/tagged/girls%20with%20curves")
	if got != "girls with curves" {
		t.Fatalf("tagged: got %q", got)
	}
}

func TestNormalizeSeedBlogsBareTumblrHost(t *testing.T) {
	got := normalizeSeedBlogs([]string{
		"http://tumblr.com/nora-prg23",
		"https://www.tumblr.com/melxbxbi",
		"https://www.tumblr.com/tagged/cosplay%20girls",
	})
	if len(got) != 2 || got[0] != "nora-prg23" || got[1] != "melxbxbi" {
		t.Fatalf("got %v", got)
	}
}

func TestLikerBelongsToShardSplitsNames(t *testing.T) {
	if !likerBelongsToShard("anyone", 0, 1) {
		t.Fatal("single shard should take everyone")
	}
	found := 0
	for i := 0; i < 20; i++ {
		if likerBelongsToShard("imabitofa", i, 20) {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("wanted 1 owning shard, got %d", found)
	}
}

func TestShardIndexMatchesChosenPool(t *testing.T) {
	pool := []Account{{ID: "planbhyderabad"}, {ID: "ganesh82vijay"}}
	a := shardIndexFor("planbhyderabad", pool)
	b := shardIndexFor("ganesh82vijay", pool)
	if a != 0 || b != 1 {
		t.Fatalf("got %d %d", a, b)
	}
	if !likerBelongsToShard("imabitofa", a, len(pool)) && !likerBelongsToShard("imabitofa", b, len(pool)) {
		t.Fatal("someone in a 2-blog pool should own the liker")
	}
	if likerBelongsToShard("imabitofa", a, len(pool)) == likerBelongsToShard("imabitofa", b, len(pool)) {
		t.Fatal("both blogs should not own the same liker")
	}
}

func TestShardIndexSpreadsAccountsOutsidePool(t *testing.T) {
	var pool []Account
	for i := 0; i < 55; i++ {
		pool = append(pool, Account{ID: fmt.Sprintf("reach%d", i)})
	}
	seen := map[int]bool{}
	for _, id := range []string{"danielbuzincu31", "bobfethi1", "ahmedsalama3666", "behemont", "murillocamolesi"} {
		seen[shardIndexFor(id, pool)] = true
	}
	if len(seen) < 3 {
		t.Fatalf("mid-reply accounts should get different liker slices, got %v", seen)
	}
}

func TestRotateAccountSlice(t *testing.T) {
	in := []Account{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	got := rotateAccountSlice(in, 2)
	if got[0].ID != "c" || got[1].ID != "a" || got[2].ID != "b" {
		t.Fatalf("%+v", got)
	}
}

func TestShardStringListSplitsSeeds(t *testing.T) {
	seeds := []string{"a", "b", "c", "d", "e"}
	s0 := shardStringList(seeds, 0, 2)
	s1 := shardStringList(seeds, 1, 2)
	if len(s0) != 3 || len(s1) != 2 {
		t.Fatalf("s0=%v s1=%v", s0, s1)
	}
	if shardStringList(seeds, 0, 1)[0] != "a" || len(shardStringList(seeds, 0, 1)) != 5 {
		t.Fatal("single shard keeps all")
	}
}

func TestFilterPostsByNoteBandRespectsMax(t *testing.T) {
	posts := []ScrapedPost{
		{ID: "1", Notes: 50},
		{ID: "2", Notes: 90000},
	}
	got := filterPostsByNoteBand(posts, 20, 4000)
	if len(got) != 1 || got[0].ID != "1" {
		t.Fatalf("want only mid post, got %+v", got)
	}
	// No mid-band hits — still must not return mega-viral when max is set.
	viralOnly := []ScrapedPost{{ID: "v", Notes: 90000}}
	if got := filterPostsByNoteBand(viralOnly, 20, 4000); len(got) != 0 {
		t.Fatalf("viral fallback should be gone: %+v", got)
	}
}

func TestIsSoftFollowReject(t *testing.T) {
	if !isSoftFollowReject(filterResult{OK: false, Reason: "female signals in bio/name"}) {
		t.Fatal("female")
	}
	if !isSoftFollowReject(filterResult{OK: false, Reason: "inactive (last update 2026-08-21)"}) {
		t.Fatal("inactive")
	}
	if isSoftFollowReject(filterResult{OK: true, Reason: "gender=male"}) {
		t.Fatal("ok should not be soft reject")
	}
}

func TestFollowConfigTimeUpRespectsStopAfter(t *testing.T) {
	prev := sessionUntil
	t.Cleanup(func() { sessionUntil = prev })
	sessionUntil = time.Time{}

	cfg := FollowConfig{}
	if cfg.timeUp() {
		t.Fatal("empty stopAfter should not be time-up")
	}
	cfg.stopAfter = time.Now().Add(-time.Second)
	if !cfg.timeUp() {
		t.Fatal("past stopAfter should be time-up")
	}
	cfg.stopAfter = time.Now().Add(time.Minute)
	if cfg.timeUp() {
		t.Fatal("future stopAfter should still have time")
	}
	sessionUntil = time.Now().Add(-time.Second)
	if !cfg.timeUp() {
		t.Fatal("global session expiry should still win")
	}
}

func TestFollowDelayShortWhenStopAfterNear(t *testing.T) {
	cfg := FollowConfig{
		DelaySeconds:       72,
		DelayJitterSeconds: 50,
		stopAfter:          time.Now().Add(20 * time.Second),
	}
	d := followDelay(cfg)
	if d <= 0 || d > 18 {
		t.Fatalf("burst delay should be tiny, got %d", d)
	}
	cfg.stopAfter = time.Now().Add(2 * time.Second)
	if followDelay(cfg) != 0 {
		t.Fatal("almost out of time should skip wait")
	}
}

func TestFetchEngagersRankedPrefersReblogs(t *testing.T) {
	body := []byte(`{"response":{"notes":[
		{"type":"like","blogName":"liker1","timestamp":100},
		{"type":"reblog","blogName":"reblogger1","timestamp":99},
		{"type":"reblog","blog_name":"reblogger2","timestamp":98}
	]}}`)
	likes, _, err := parseEngagerNotes(body, "likes")
	if err != nil || len(likes) != 1 || likes[0] != "liker1" {
		t.Fatalf("likes parse: %v %v", likes, err)
	}
	rebs, _, err := parseEngagerNotes(body, "reblogs_with_tags")
	if err != nil || len(rebs) != 2 {
		t.Fatalf("reblogs parse: %v %v", rebs, err)
	}
}

func TestEngagePostsUsesRebloggersWhenConfigured(t *testing.T) {
	src, err := os.ReadFile("follow.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if !strings.Contains(body, "fetchEngagersRanked") {
		t.Fatal("engage path must use ranked engagers (reblog + like)")
	}
	if !strings.Contains(body, "PreferRebloggers") {
		t.Fatal("must honor preferRebloggers")
	}
}
