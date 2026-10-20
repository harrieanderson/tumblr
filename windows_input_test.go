package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestAgeKeyNewestClickHitsSecondRowNotCancel(t *testing.T) {
	sizes := [][2]int{
		{360, 480},
		{400, 520},
		{440, 560},
		{480, 620},
		{520, 680},
		{560, 720},
	}
	for _, sz := range sizes {
		w, h := sz[0], sz[1]
		x, y := ageKeyNewestPasskeyPoint(w, h)
		if z := passkeyClickZone(w, h, x, y); z != "newest-agekey" {
			t.Fatalf("%dx%d primary click is %s at %d,%d", w, h, z, x, y)
		}
		pts := ageKeyNewestPasskeyPoints(w, h)
		if len(pts) == 0 {
			t.Fatalf("%dx%d produced no safe AgeKey clicks", w, h)
		}
		for _, p := range pts {
			z := passkeyClickZone(w, h, p[0], p[1])
			if z != "newest-agekey" {
				t.Fatalf("%dx%d candidate %v is %s", w, h, p, z)
			}
			if p[1] > h*3/4 {
				t.Fatalf("%dx%d candidate %v is in the Cancel band", w, h, p)
			}
		}
	}
}

func TestPasskeyClickZonesMatchScreenshot(t *testing.T) {
	w, h := 440, 560
	x := int(float64(w) * 0.30)
	cases := []struct {
		yFrac float64
		want  string
	}{
		{0.10, "title"},
		{0.32, "old-agekey"},
		{0.34, "old-agekey"},
		{0.42, "newest-agekey"},
		{0.445, "newest-agekey"},
		{0.52, "other-passkey"},
		{0.60, "other-passkey"},
		{0.78, "cancel"},
		{0.88, "cancel"},
	}
	for _, tc := range cases {
		y := int(float64(h) * tc.yFrac)
		got := passkeyClickZone(w, h, x, y)
		if got != tc.want {
			t.Fatalf("y=%.3f -> %s, want %s", tc.yFrac, got, tc.want)
		}
	}
	if passkeyClickHitsNewestAgeKey(w, h, int(float64(w)*0.30), int(float64(h)*0.34)) {
		t.Fatal("0.34 height is the 2026-09-11 row and must not be clicked")
	}
	if passkeyClickHitsNewestAgeKey(w, h, int(float64(w)*0.80), int(float64(h)*0.85)) {
		t.Fatal("Cancel must never count as the newest AgeKey")
	}
}

func TestPreferredAgeKeyPicks20260919(t *testing.T) {
	now := time.Date(2026, 9, 20, 18, 0, 0, 0, time.Local)
	names := []string{
		"AgeKey - 2026-09-11",
		"AgeKey - 2026-09-19",
		"iPhone, iPad, or Android device",
		"Security key",
		"Cancel",
	}
	got := preferredAgeKeyLabel(names, now)
	if got != "AgeKey - 2026-09-19" {
		t.Fatalf("got %q", got)
	}
	if preferredAgeKeyLabel(names, time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)) != "AgeKey - 2026-09-19" {
		t.Fatal("on the create day, still pick 2026-09-19 over 2026-09-11")
	}
}

func TestPasskeyHitRejectsOldKeyIPhoneAndCancel(t *testing.T) {
	now := time.Date(2026, 9, 20, 18, 0, 0, 0, time.Local)
	if passkeyHitIsToday("AgeKey - 2026-09-11", now) {
		t.Fatal("must not accept the 2026-09-11 AgeKey")
	}
	if passkeyHitIsToday("iPhone, iPad, or Android device", now) {
		t.Fatal("must not accept iPhone")
	}
	if passkeyHitIsToday("Security key", now) {
		t.Fatal("must not accept security key")
	}
	if passkeyHitIsToday("Cancel", now) {
		t.Fatal("must not accept Cancel")
	}
	if passkeyHitIsToday("pin-prompt", now) {
		t.Fatal("PIN prompt is not a passkey hit")
	}
	if !passkeyHitIsToday("AgeKey - 2026-09-19", now) {
		t.Fatal("2026-09-19 is yesterday and must count")
	}
	if !passkeyHitIsToday(reportedNewestPasskeyLabel(now), now) {
		t.Fatal("reported newest label must count as a hit")
	}
	if !strings.Contains(reportedNewestPasskeyLabel(now), "2026-09-19") {
		t.Fatalf("reported label should name 2026-09-19, got %q", reportedNewestPasskeyLabel(now))
	}
}

func TestSelectTodayAgeKeyPasskeyDoesNotMoveMouse(t *testing.T) {
	raw, err := os.ReadFile("windows_input.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	bodyOf := func(name string) string {
		start := strings.Index(src, "func "+name)
		if start < 0 {
			t.Fatalf("missing %s", name)
		}
		body := src[start:]
		if next := strings.Index(body[len("func "+name):], "\nfunc "); next >= 0 {
			body = body[:len("func "+name)+next]
		}
		return body
	}
	for _, name := range []string{"selectTodayAgeKeyPasskey()", "clickNewestListedAgeKey("} {
		body := bodyOf(name)
		for _, forbidden := range []string{"clickScreen(", "procSetCursorPos", "procMouseEvent", "mouseClick"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s must not move or click the physical mouse: found %s", name, forbidden)
			}
		}
	}
}

func TestClickNewestListedAgeKeyNilHWND(t *testing.T) {
	if hit := clickNewestListedAgeKey(0, time.Now()); hit != "" {
		t.Fatalf("nil hwnd should not click, got %q", hit)
	}
}
