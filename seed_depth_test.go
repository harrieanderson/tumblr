package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestSeedPostUsedUp(t *testing.T) {
	t.Chdir(t.TempDir())
	if seedPostUsedUp("maria818noctis", "1") {
		t.Fatal("unknown post should be usable")
	}
	recordSeedPostFreshness("Maria818noctis", "1", 0)
	recordSeedPostFreshness("maria818noctis", "2", 40)
	if !seedPostUsedUp("maria818noctis", "1") {
		t.Fatal("post with nobody fresh should be skipped")
	}
	if seedPostUsedUp("maria818noctis", "2") {
		t.Fatal("post with 40 fresh engagers should still be walked")
	}

	m := loadSeedPosts()
	m[seedPostKey("maria818noctis", "1")] = seedPostRecord{At: time.Now().AddDate(0, 0, -seedUsedUpForDays-1), Fresh: 0}
	raw, _ := json.Marshal(m)
	if err := os.WriteFile(seedPostsFile, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if seedPostUsedUp("maria818noctis", "1") {
		t.Fatal("used-up mark should expire after the cooldown")
	}
}
