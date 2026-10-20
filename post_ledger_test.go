package main

import (
	"path/filepath"
	"testing"
)

func TestPostLedgerTracksPerAccount(t *testing.T) {
	old := postLedgerFile
	postLedgerFile = filepath.Join(t.TempDir(), "post-ledger.jsonl")
	defer func() { postLedgerFile = old }()

	recordPost(ledgerEntry{Account: "acc1", Blog: "@zoeyone", Kind: "text", Text: "i love being thick and having massive thighs"})
	recordPost(ledgerEntry{Account: "acc2", Blog: "zoeytwo", Kind: "video", Text: "hi", Media: "pics/posted/new_pinned.mp4", Pinned: true})

	if _, ok := accountAlreadyPosted("acc1", "I love being thick, and having massive thighs!", ""); !ok {
		t.Fatal("same text with different punctuation/case should count as already posted")
	}
	if _, ok := accountAlreadyPosted("acc2", "i love being thick and having massive thighs", ""); ok {
		t.Fatal("another account's post must not block this account")
	}
	if e, ok := accountAlreadyPosted("acc2", "", "new_pinned.mp4"); !ok || !e.Pinned {
		t.Fatal("media post should be found by file name")
	}
	got := loadPostLedger()
	if len(got) != 2 || got[0].Blog != "zoeyone" || got[0].Time == "" {
		t.Fatalf("unexpected ledger contents: %+v", got)
	}
}
