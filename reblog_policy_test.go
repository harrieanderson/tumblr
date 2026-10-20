package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNSFWReblogQuotaWindow(t *testing.T) {
	id := "zzreblogquota"
	path := reblogLedgerPath(id)
	_ = os.Remove(path)
	t.Cleanup(func() { _ = os.Remove(path); _ = os.Remove(filepath.Dir(path)) })

	appendReblogLedger(id, reblogLedgerEntry{
		At:       time.Now().Add(-2 * time.Hour).Format(time.RFC3339),
		PostID:   "1",
		Category: "nsfw_rare",
	})
	cfg := nsfwReblogConfig{MaxCount: 1, WindowHours: 72, Chance: 1}
	if canNSFWReblog(id, cfg) {
		t.Fatal("quota should be used")
	}
	appendReblogLedger(id, reblogLedgerEntry{
		At:       time.Now().Add(-80 * time.Hour).Format(time.RFC3339),
		PostID:   "old",
		Category: "nsfw_rare",
	})
	if canNSFWReblog(id, cfg) {
		t.Fatal("still blocked by recent NSFW reblog")
	}
}

func TestLooksNSFWTrailText(t *testing.T) {
	if !looksNSFWTrailText("cute nsfw dump") {
		t.Fatal("nsfw")
	}
	if looksNSFWTrailText("cottage garden flowers") {
		t.Fatal("soft text should pass")
	}
}

func TestPreferRebloggersStillDefault(t *testing.T) {
	cfg := loadFollowConfig()
	if !cfg.PreferRebloggers || !cfg.IncludeRebloggers {
		t.Fatal("reach must still prefer NSFW post rebloggers for audience")
	}
	if !cfg.TrailReblog.Enabled {
		t.Fatal("trail reblog should be on")
	}
	if cfg.NSFWReblog.WindowHours != 72 || cfg.NSFWReblog.MaxCount != 1 {
		t.Fatalf("nsfw quota %#v", cfg.NSFWReblog)
	}
}
