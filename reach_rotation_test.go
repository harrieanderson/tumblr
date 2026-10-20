package main

import (
	"testing"
	"time"
)

func TestReachBatchSliceCoversWholePool(t *testing.T) {
	if got := reachBatchSlice(8*time.Hour, 80, 10); got != time.Hour {
		t.Fatalf("8h over 8 batches: %s", got)
	}
	if got := reachBatchSlice(3*time.Hour, 80, 10); got != 22*time.Minute+30*time.Second {
		t.Fatalf("3h over 8 batches: %s", got)
	}
	if got := reachBatchSlice(time.Hour, 80, 10); got != minReachBatchSlice {
		t.Fatalf("short windows keep a useful minimum: %s", got)
	}
	if reachBatchSlice(0, 80, 10) != 0 {
		t.Fatal("no -for window means no slicing")
	}
}

func TestLeastRecentlyReachedGivesEveryoneATurn(t *testing.T) {
	pool := []Account{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}}
	last := map[string]time.Time{}
	turns := map[string]int{}
	now := time.Now()
	for pass := 0; pass < 10; pass++ {
		picked := leastRecentlyReached(pool, last)[:2]
		for i, a := range picked {
			turns[a.ID]++
			last[a.ID] = now.Add(time.Duration(pass)*time.Minute + time.Duration(i)*time.Millisecond)
		}
	}
	for _, a := range pool {
		if turns[a.ID] != 4 {
			t.Fatalf("uneven turns: %v", turns)
		}
	}
}
