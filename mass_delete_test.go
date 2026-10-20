package main

import "testing"

func TestPostIsPinned(t *testing.T) {
	if !postIsPinned(map[string]any{"is_pinned": true}) {
		t.Fatal("is_pinned true")
	}
	if postIsPinned(map[string]any{"is_pinned": false, "id": "1"}) {
		t.Fatal("is_pinned false")
	}
	if !postIsPinned(map[string]any{"pinned": "true"}) {
		t.Fatal("pinned string")
	}
}

func TestPostIDFromMapPrefersString(t *testing.T) {
	id := postIDFromMap(map[string]any{"id": float64(1.234e18), "id_string": "1234567890123456789"})
	if id != "1234567890123456789" {
		t.Fatalf("got %q", id)
	}
}
