package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestMergeSeenBaselineThenNewAndLost(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	s, added, lost := mergeSeen(seenSet{}, []string{"Alice", "bob"}, t0, true)
	if added != 0 || lost != 0 || s.Names["alice"] != "" || s.Names["bob"] != "" {
		t.Fatalf("first snapshot should be baseline: %+v added=%d lost=%d", s, added, lost)
	}
	t1 := t0.Add(time.Hour)
	s, added, lost = mergeSeen(s, []string{"alice", "carol"}, t1, true)
	if added != 1 || lost != 1 {
		t.Fatalf("added=%d lost=%d", added, lost)
	}
	if s.Names["carol"] != t1.Format(time.RFC3339) || s.Lost["bob"] == "" {
		t.Fatalf("carol/bob not tracked: %+v", s)
	}
	s, _, _ = mergeSeen(s, []string{"alice", "carol", "bob"}, t1.Add(time.Hour), true)
	if _, still := s.Lost["bob"]; still {
		t.Fatal("bob came back but is still marked lost")
	}
}

func TestResearchReportAttributesFollowbacksAndDMs(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	_ = os.Chdir(dir)
	_ = os.MkdirAll("data", 0o755)

	now := time.Now()
	baseline := now.Add(-48 * time.Hour)
	touchAt := now.Add(-24 * time.Hour)
	acc := Account{ID: "acc1"}

	_ = saveSeenSet("followers", "acc1", seenSet{
		BaselineAt: baseline.Format(time.RFC3339),
		Names: map[string]string{
			"oldfan":  "",
			"fan1":    now.Add(-20 * time.Hour).Format(time.RFC3339),
			"early":   now.Add(-30 * time.Hour).Format(time.RFC3339),
			"organic": now.Add(-2 * time.Hour).Format(time.RFC3339),
		},
	})
	_ = saveSeenSet("dm", "acc1", seenSet{
		BaselineAt: baseline.Format(time.RFC3339),
		Names:      map[string]string{"chatty": now.Add(-10 * time.Hour).Format(time.RFC3339)},
	})

	write := func(target, source string, followed bool) {
		raw := researchTouch{At: touchAt.Format(time.RFC3339), Account: "acc1", Target: target, Kind: "seed", Source: source, Liked: true, Followed: followed}
		_ = os.MkdirAll(researchDir, 0o755)
		f, _ := os.OpenFile(researchTouchesFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		b, _ := json.Marshal(raw)
		_, _ = f.Write(append(b, '\n'))
		f.Close()
	}
	write("fan1", "goodseed", true)
	write("early", "goodseed", false)
	write("chatty", "goodseed", false)
	write("nobody", "badseed", true)

	rep := buildResearchReport([]Account{acc}, 14, now)
	if rep.Fleet.Touches != 4 || rep.Fleet.Followbacks != 1 || rep.Fleet.DMs != 1 {
		t.Fatalf("fleet %+v", rep.Fleet)
	}
	var good researchStat
	for _, s := range rep.Sources {
		if s.Key == "seed:goodseed" {
			good = s
		}
	}
	if good.Touches != 3 || good.Followbacks != 1 || good.DMs != 1 {
		t.Fatalf("goodseed %+v", good)
	}
	if len(rep.Accounts) != 1 {
		t.Fatalf("accounts %+v", rep.Accounts)
	}
	a := rep.Accounts[0]
	// "early" followed before the touch, so it counts as organic, not ours.
	if a.NewFollowers != 3 || a.Organic != 2 || a.Attributed != 1 || a.BestSource != "goodseed" {
		t.Fatalf("account row %+v", a)
	}
}
