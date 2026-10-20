package main

import (
	"os"
	"testing"
	"time"
)

func TestDMReportFunnelAndSkipDedup(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	_ = os.Chdir(dir)
	_ = os.MkdirAll("data", 0o755)

	now := time.Now()
	at := func(h int) string { return now.Add(time.Duration(-h) * time.Hour).Format(time.RFC3339) }
	recordResearchTouch("acc1", "alice", ScrapedPost{SourceKind: "seed", Source: "goodseed"}, true, false, true)
	for _, ev := range []dmEvent{
		{At: at(10), Account: "acc1", Partner: "alice", Event: "reply", Stage: "chat", Set: 1, Source: "groq", TheyLast: true, LatencySec: 600},
		{At: at(9), Account: "acc1", Partner: "alice", Event: "reply", Stage: "pin_nudge", Set: 2, Source: "groq", TheyLast: true, LatencySec: 1200},
		{At: at(8), Account: "acc1", Partner: "alice", Event: "reply", Stage: "post_pin", Set: 3, Source: "openrouter", TheyLast: true, LatencySec: 300},
		{At: at(72), Account: "acc1", Partner: "bob", Event: "reply", Stage: "chat", Set: 1, Source: "groq", TheyLast: true},
		{At: at(5), Account: "acc1", Partner: "kid", Event: "skip", Reason: "under 18 (already blocked)"},
		{At: at(4), Account: "acc1", Partner: "kid", Event: "skip", Reason: "under 18 (already blocked)"},
		{At: at(3), Account: "acc1", Partner: "carl", Event: "send_fail", Reason: "403"},
	} {
		recordDMEvent(ev)
	}

	rep := buildDMReport(now.Add(-14*24*time.Hour), now)
	f := rep.Fleet
	if f.Chats != 2 || f.Replies != 4 || f.Nudged != 1 || f.NudgeReplied != 1 || f.MinorsSkipped != 1 || f.SendFails != 1 {
		t.Fatalf("fleet %+v", f)
	}
	if rep.DeadChats != 1 {
		t.Fatalf("bob went quiet 72h ago before any nudge; dead=%d", rep.DeadChats)
	}
	if len(rep.Skips) != 1 || rep.Skips[0].N != 1 {
		t.Fatalf("repeat skips should count once: %+v", rep.Skips)
	}
	var fromSeed dmStat
	for _, s := range rep.Sources {
		if s.Key == "goodseed" {
			fromSeed = s
		}
	}
	if fromSeed.Chats != 1 || fromSeed.Nudged != 1 {
		t.Fatalf("alice should be credited to goodseed: %+v", rep.Sources)
	}
	if rep.Funnel[2].Step != "reached set 3" || rep.Funnel[2].Chats != 1 {
		t.Fatalf("funnel %+v", rep.Funnel)
	}
}
