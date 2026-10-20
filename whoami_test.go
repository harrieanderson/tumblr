package main

import (
	"testing"
	"time"
)

func TestAccountDailyNewFollowersFromYesterday(t *testing.T) {
	loc := time.FixedZone("BST", 3600)
	now := time.Date(2026, 9, 19, 23, 50, 0, 0, loc)
	log := followerLog{
		Latest: followerSnapshot{
			At: "2026-09-19T23:51:43+01:00", Primary: "zoey-bunny", Followers: 55,
			Blogs: map[string]int{"zoey-bunny": 55},
		},
		History: []followerSnapshot{
			{
				At: "2026-09-18T23:35:33+01:00", Primary: "zoey-bunny", Followers: 41,
				Blogs: map[string]int{"zoey-bunny": 41},
			},
			{
				At: "2026-09-19T00:37:54+01:00", Primary: "zoey-bunny", Followers: 42,
				Blogs: map[string]int{"zoey-bunny": 42},
			},
			{
				At: "2026-09-19T23:51:43+01:00", Primary: "zoey-bunny", Followers: 55,
				Blogs: map[string]int{"zoey-bunny": 55},
			},
		},
	}
	if got := accountDailyNewFollowers(log, now); got != 14 {
		t.Fatalf("today new = %d, want 14 (55-41)", got)
	}
}

func TestAccountDailyNewFollowersIntradayOnly(t *testing.T) {
	loc := time.FixedZone("BST", 3600)
	now := time.Date(2026, 9, 19, 18, 0, 0, 0, loc)
	log := followerLog{
		Latest: followerSnapshot{
			At: "2026-09-19T17:00:00+01:00", Primary: "zoey-bunny", Followers: 55,
			Blogs: map[string]int{"zoey-bunny": 55},
		},
		History: []followerSnapshot{
			{
				At: "2026-09-19T10:00:00+01:00", Primary: "zoey-bunny", Followers: 50,
				Blogs: map[string]int{"zoey-bunny": 50},
			},
			{
				At: "2026-09-19T17:00:00+01:00", Primary: "zoey-bunny", Followers: 55,
				Blogs: map[string]int{"zoey-bunny": 55},
			},
		},
	}
	if got := accountDailyNewFollowers(log, now); got != 5 {
		t.Fatalf("intraday new = %d, want 5", got)
	}
}

func TestAccountDailyNewFollowersIgnoresRenamedBlog(t *testing.T) {
	loc := time.FixedZone("BST", 3600)
	now := time.Date(2026, 9, 19, 18, 0, 0, 0, loc)
	log := followerLog{
		Latest: followerSnapshot{
			At: "2026-09-19T17:00:00+01:00", Primary: "zoey-bunny", Followers: 55,
			Blogs: map[string]int{"zoey-bunny": 55},
		},
		History: []followerSnapshot{
			{
				At: "2026-09-18T22:00:00+01:00", Primary: "sofdk", Followers: 2,
				Blogs: map[string]int{"sofdk": 2},
			},
		},
	}
	if got := accountDailyNewFollowers(log, now); got != 0 {
		t.Fatalf("renamed blog should not count as +%d today", got)
	}
}

func TestBestAccountBlogStatsSkipsStampedZoeyofc(t *testing.T) {
	log := followerLog{
		Latest: followerSnapshot{At: "2026-09-20T16:20:39+01:00", Primary: "zoeyofc", Followers: 34, Blogs: map[string]int{"zoeyofc": 34}},
		History: []followerSnapshot{
			{At: "2026-09-19T12:00:00+01:00", Primary: "lillycosmic3", Followers: 99, Blogs: map[string]int{"lillycosmic3": 99}},
			{At: "2026-09-20T16:20:39+01:00", Primary: "zoeyofc", Followers: 34, Blogs: map[string]int{"zoeyofc": 34}},
		},
	}
	blog, n := bestAccountBlogStats("not-in-assigned-map", log)
	if blog != "lillycosmic3" || n != 99 {
		t.Fatalf("got @%s %d", blog, n)
	}
}

func TestBestAccountBlogStatsKeepsRealZoeyofc(t *testing.T) {
	log := followerLog{
		Latest: followerSnapshot{At: "2026-09-20T12:28:47+01:00", Primary: "zoeyofc", Followers: 24, Blogs: map[string]int{"zoeyofc": 24}},
		History: []followerSnapshot{
			{At: "2026-09-20T12:28:47+01:00", Primary: "zoeyofc", Followers: 24, Blogs: map[string]int{"zoeyofc": 24}},
		},
	}
	blog, n := bestAccountBlogStats("planbhyderabad", log)
	if blog != "zoeyofc" || n != 24 {
		t.Fatalf("got @%s %d", blog, n)
	}
}

func TestMidFollowerAccountsPicksMiddle(t *testing.T) {
	// Synthetic ranking via followers.json isn't easy here — exercise sort on empty logs
	// (all 0) falls back to stable ID order, still returns n accounts.
	accs := []Account{
		{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}, {ID: "f"}, {ID: "g"},
	}
	got := midFollowerAccounts(accs, 3)
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	// With equal followers, middle slice of sorted IDs a..g is c,d,e (start=(7-3)/2=2)
	want := []string{"c", "d", "e"}
	for i := range want {
		if got[i].ID != want[i] {
			t.Fatalf("mid[%d]=%s want %s (full=%v)", i, got[i].ID, want[i], accountIDsOf(got))
		}
	}
	rest := accountsExcept(accs, got)
	if len(rest) != 4 {
		t.Fatalf("except: %d", len(rest))
	}
}
