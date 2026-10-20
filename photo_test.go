package main

import (
	"strings"
	"testing"
)

func TestNpfCaptionBlocksKeepLines(t *testing.T) {
	got := npfCaptionBlocks("bored\r\nhe's cute")
	if len(got) != 2 {
		t.Fatalf("len=%d want 2 %#v", len(got), got)
	}
	if got[0]["text"] != "bored" || got[1]["text"] != "he's cute" {
		t.Fatalf("lines %#v", got)
	}
	if npfCaptionBlocks("  \n  ") != nil {
		t.Fatal("whitespace-only caption should be omitted")
	}
	blank := npfCaptionBlocks("hi\n\nthere")
	if len(blank) != 3 || blank[1]["text"] != " " {
		t.Fatalf("blank line %#v", blank)
	}
}

func TestNpfCaptionBlocksLinkifyURL(t *testing.T) {
	url := "https://beacons.ai/lil.cosmic.bunny.o7"
	line := "it's easier to talk to me here " + url + " 😭😭"
	got := npfCaptionBlocks(line)
	if len(got) != 1 {
		t.Fatalf("len=%d %#v", len(got), got)
	}
	fmts, ok := got[0]["formatting"].([]map[string]any)
	if !ok || len(fmts) != 1 {
		t.Fatalf("formatting %#v", got[0]["formatting"])
	}
	if fmts[0]["type"] != "link" || fmts[0]["url"] != url {
		t.Fatalf("link %#v", fmts[0])
	}
	prefix := "it's easier to talk to me here "
	wantStart := utf16Len(prefix)
	wantEnd := utf16Len(prefix + url)
	if fmts[0]["start"] != wantStart || fmts[0]["end"] != wantEnd {
		t.Fatalf("span %#v want %d-%d", fmts[0], wantStart, wantEnd)
	}
}

func TestNpfCaptionBlocksFancyClickyLink(t *testing.T) {
	clicky := "𝓒𝓵𝓲𝓬𝓴𝔂"
	url := "https://beacons.ai/lil.cosmic.bunny.o7"
	got := npfCaptionBlocks("[" + clicky + "](" + url + ")")
	if len(got) != 1 {
		t.Fatalf("len=%d %#v", len(got), got)
	}
	if got[0]["text"] != clicky {
		t.Fatalf("text %#v", got[0]["text"])
	}
	fmts, ok := got[0]["formatting"].([]map[string]any)
	if !ok || len(fmts) != 1 {
		t.Fatalf("formatting %#v", got[0]["formatting"])
	}
	if fmts[0]["type"] != "link" || fmts[0]["url"] != url {
		t.Fatalf("link %#v", fmts[0])
	}
	if fmts[0]["start"] != 0 || fmts[0]["end"] != utf16Len(clicky) {
		t.Fatalf("span %#v want 0-%d", fmts[0], utf16Len(clicky))
	}
	cap := zoeyNewPinnedCaption()
	if !strings.Contains(cap, clicky) || !strings.Contains(cap, "skyrim") {
		t.Fatalf("pinned caption missing bits: %q", cap)
	}
	blocks := npfCaptionBlocks(cap)
	var found bool
	for _, b := range blocks {
		if b["text"] == clicky {
			found = true
			fm, _ := b["formatting"].([]map[string]any)
			if len(fm) != 1 || fm[0]["url"] != beaconsLink {
				t.Fatalf("clicky formatting %#v", b)
			}
		}
	}
	if !found {
		t.Fatal("pinned caption missing Clicky block")
	}
	htmlCap := captionToLegacyHTML(cap)
	if !strings.Contains(htmlCap, `<a href="`+beaconsLink+`">`) || !strings.Contains(htmlCap, clicky) {
		t.Fatalf("legacy html caption missing clicky link: %q", htmlCap)
	}
}

func TestParseCreatedPostID(t *testing.T) {
	if got := parseCreatedPostID([]byte(`{"response":{"id":827651365124882432,"id_string":"827651365124882432"}}`)); got != "827651365124882432" {
		t.Fatalf("id_string got %q", got)
	}
	if got := parseCreatedPostID([]byte(`{"response":{"id":"123"}}`)); got != "123" {
		t.Fatalf("string id got %q", got)
	}
}

func TestFindNewPinnedCaptionMarkers(t *testing.T) {
	m := map[string]any{
		"id_string": "123",
		"is_pinned": false,
		"content": []any{
			map[string]any{"type": "text", "text": "Hiii (redoing pinned)"},
			map[string]any{"type": "text", "text": "skyrim"},
			map[string]any{"type": "text", "text": "https://beacons.ai/lil.cosmic.bunny.o7"},
		},
	}
	if postIDFromMap(m) != "123" {
		t.Fatal(postIDFromMap(m))
	}
	blob := strings.ToLower(postTextBlob(m))
	if !strings.Contains(blob, "redoing pinned") {
		t.Fatalf("blob %q", blob)
	}
	if isPinnedPost(m) {
		t.Fatal("should not be pinned")
	}
	m["is_pinned"] = true
	if !isPinnedPost(m) {
		t.Fatal("should be pinned")
	}
}
