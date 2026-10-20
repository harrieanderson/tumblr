package main

import (
	"os"
	"testing"
	"time"
)

func TestTakeQueuedLikerRotatesSources(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	_ = os.Chdir(dir)
	_ = os.MkdirAll("data", 0o755)
	prev := likerSourceLastTaken
	likerSourceLastTaken = map[string]time.Time{}
	t.Cleanup(func() { likerSourceLastTaken = prev })

	now := time.Now()
	var q []queuedLiker
	for _, b := range []string{"a1", "a2", "a3", "a4"} {
		q = append(q, queuedLiker{Blog: b, Source: "big", Added: now})
	}
	q = append(q, queuedLiker{Blog: "b1", Source: "small", Added: now}, queuedLiker{Blog: "c1", Source: "other", Added: now})
	saveLikerQueue(q)

	var got []string
	for i := 0; i < 4; i++ {
		e, ok := takeQueuedLiker()
		if !ok {
			t.Fatal("queue ran dry early")
		}
		got = append(got, e.Source)
	}
	want := []string{"big", "small", "other", "big"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("take order %v, want %v", got, want)
		}
	}
}

func TestEnqueueLikersCapsPerPost(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	_ = os.Chdir(dir)
	_ = os.MkdirAll("data", 0o755)

	var names []string
	for i := 0; i < likerQueuePerPost+25; i++ {
		names = append(names, "liker"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	n := enqueueLikers(names, ScrapedPost{Blog: "seedblog", ID: "1", SourceKind: "seed", Source: "seedblog"})
	if n != likerQueuePerPost {
		t.Fatalf("queued %d, want %d", n, likerQueuePerPost)
	}
}
