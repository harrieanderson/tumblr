package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRefreshGrowthFollowersUpdatesReportAndDailyTally(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := saveFollowerSnapshot("default", []tumblrBlog{{Name: "example", Primary: true, Followers: 10}}); err != nil {
		t.Fatal(err)
	}
	var messages []string
	logFn := func(s string) { messages = append(messages, s) }
	accs := []Account{{ID: "default"}}
	refreshGrowthFollowers(accs, func(Account) ([]tumblrBlog, error) {
		return []tumblrBlog{{Name: "example", Primary: true, Followers: 14}}, nil
	}, logFn)
	rep := buildGrowthReport(accs, time.Now())
	if row := rep.Accounts[0]; row.Followers != 14 || row.Gain1d != 4 {
		t.Fatalf("fresh report row = %+v", row)
	}
	if got := accountDailyNewFollowers(loadFollowerLog("default"), time.Now()); got != 4 {
		t.Fatalf("daily tally = %d, want 4", got)
	}
	for _, empty := range []bool{false, true} {
		refreshGrowthFollowers(accs, func(Account) ([]tumblrBlog, error) {
			if empty {
				return nil, nil
			}
			return nil, fmt.Errorf("session expired")
		}, logFn)
		if got := loadFollowerLog("default"); got.Latest.Followers != 14 || len(got.History) != 2 {
			t.Fatalf("failed refresh changed saved counts: %+v", got)
		}
	}
	if !strings.Contains(strings.Join(messages, "\n"), "keeping saved count") {
		t.Fatal("missing refresh failure warning")
	}
}

func TestFollowerGainSince(t *testing.T) {
	log := followerLog{
		History: []followerSnapshot{
			{At: "2026-09-17T12:00:00+01:00", Primary: "zoey", Followers: 100, Blogs: map[string]int{"zoey": 100}},
			{At: "2026-09-23T12:00:00+01:00", Primary: "zoey", Followers: 110, Blogs: map[string]int{"zoey": 110}},
			{At: "2026-09-24T18:00:00+01:00", Primary: "zoey", Followers: 115, Blogs: map[string]int{"zoey": 115}},
		},
		Latest: followerSnapshot{At: "2026-09-24T18:00:00+01:00", Primary: "zoey", Followers: 115, Blogs: map[string]int{"zoey": 115}},
	}
	now := time.Date(2026, 9, 24, 19, 0, 0, 0, time.FixedZone("BST", 3600))
	g1 := followerGainSince(log, "zoey", now.Add(-24*time.Hour), now)
	if g1 != 5 {
		t.Fatalf("1d gain want 5 got %d", g1)
	}
	g7 := followerGainSince(log, "zoey", now.Add(-7*24*time.Hour), now)
	if g7 != 15 {
		t.Fatalf("7d gain want 15 got %d", g7)
	}
}

func TestOrderAccountsForReachUsesReport(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	_ = os.Chdir(dir)
	_ = os.MkdirAll("config", 0755)

	rep := growthReport{
		At:         time.Now().Format(time.RFC3339),
		ReachOrder: []string{"mid", "big"},
		Parked:     []string{"dead1"},
		Accounts: []growthAccountRow{
			{ID: "big", Followers: 200},
			{ID: "mid", Followers: 50},
			{ID: "dead1", Followers: 10, Tier: growthPark},
		},
	}
	if err := saveGrowthReport(rep); err != nil {
		t.Fatal(err)
	}
	accs := []Account{
		{ID: "dead1"},
		{ID: "mid"},
		{ID: "big"},
	}
	ordered := orderAccountsForReach(accs, true)
	if len(ordered) != 2 {
		t.Fatalf("drop parked: got %d want 2", len(ordered))
	}
	if ordered[0].ID != "big" || ordered[1].ID != "mid" {
		t.Fatalf("want followers desc, got %s %s", ordered[0].ID, ordered[1].ID)
	}
	withPark := orderAccountsForReach(accs, false)
	if withPark[len(withPark)-1].ID != "dead1" {
		t.Fatalf("parked should be last, got %v", withPark[len(withPark)-1].ID)
	}
}

func TestOrderAccountsForReachTopFollowersFirst(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	_ = os.Chdir(dir)
	_ = os.MkdirAll("config", 0755)

	rep := growthReport{
		At:         time.Now().Format(time.RFC3339),
		ReachOrder: []string{"b", "c", "a"},
		Accounts: []growthAccountRow{
			{ID: "a", Followers: 10},
			{ID: "b", Followers: 100},
			{ID: "c", Followers: 40},
		},
	}
	if err := saveGrowthReport(rep); err != nil {
		t.Fatal(err)
	}
	ordered := orderAccountsForReach([]Account{{ID: "a"}, {ID: "b"}, {ID: "c"}}, true)
	if len(ordered) != 3 || ordered[0].ID != "b" || ordered[1].ID != "c" || ordered[2].ID != "a" {
		ids := make([]string, len(ordered))
		for i := range ordered {
			ids[i] = ordered[i].ID
		}
		t.Fatalf("want b,c,a got %v", ids)
	}
}

func TestRecordFollowAttribution(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	_ = os.Chdir(dir)
	_ = os.MkdirAll("data", 0755)
	recordFollowAttribution("acc1", "SomeBlog", "seed", "seedblog1")
	recordFollowAttribution("acc1", "Other", "tag", "daddy")
	ev := loadFollowAttributionSince(time.Now().Add(-time.Hour))
	if len(ev) != 2 {
		t.Fatalf("want 2 events got %d", len(ev))
	}
	stats := seedStatsFromAttribution(time.Now().Add(-time.Hour), time.Now())
	if len(stats) < 1 {
		t.Fatal("expected seed stats")
	}
}

func TestPickSeedPromoteDemote(t *testing.T) {
	promoted, demoted := pickSeedPromoteDemote([]growthSeedStat{
		{Source: "hotseed", Kind: "seed", Follows: 10},
		{Source: "warm", Kind: "seed", Follows: 3},
		{Source: "taggy", Kind: "tag", Follows: 99},
	})
	if len(promoted) == 0 || promoted[0] != "hotseed" {
		t.Fatalf("promoted=%v", promoted)
	}
	_ = demoted // depends on follow.json seeds
}
