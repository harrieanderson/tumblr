package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestFingerprintUAAndClientHintsAligned(t *testing.T) {
	fp := fingerprint()
	if fp.Major <= 0 {
		t.Fatalf("major=%d", fp.Major)
	}
	wantUA := fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%d.0.0.0 Safari/537.36", fp.Major)
	if fp.UA != wantUA {
		t.Fatalf("UA=%q want %q", fp.UA, wantUA)
	}
	wantCH := fmt.Sprintf(`"Chromium";v="%d", "Not?A_Brand";v="24", "Google Chrome";v="%d"`, fp.Major, fp.Major)
	if fp.SecCHUA != wantCH {
		t.Fatalf("sec-ch-ua=%q want %q", fp.SecCHUA, wantCH)
	}
	if chromeUA != fp.UA || chromeSecCHUA != fp.SecCHUA {
		t.Fatalf("package aliases drifted: chromeUA=%q chromeSecCHUA=%q", chromeUA, chromeSecCHUA)
	}
	if !strings.Contains(fp.UA, fmt.Sprintf("Chrome/%d.", fp.Major)) {
		t.Fatalf("UA major mismatch: %q vs %d", fp.UA, fp.Major)
	}
	if fp.AcceptLanguage == "" || fp.Timezone == "" || fp.Lang == "" {
		t.Fatalf("locale fields empty: %+v", fp)
	}
	if fp.ViewportW <= 0 || fp.ViewportH <= 0 {
		t.Fatalf("viewport: %dx%d", fp.ViewportW, fp.ViewportH)
	}
	if fp.SecCHUAPlatform != `"Windows"` || fp.Platform != "Windows" {
		t.Fatalf("platform: hint=%q platform=%q", fp.SecCHUAPlatform, fp.Platform)
	}
}

func TestFingerprintStableAcrossCalls(t *testing.T) {
	a := fingerprint()
	b := fingerprint()
	if a != b {
		t.Fatalf("fingerprint mutated between calls: %+v vs %+v", a, b)
	}
}

func TestBuildFingerprintFallback(t *testing.T) {
	p := buildFingerprint(0)
	if p.Major != fingerprintFallbackMajor {
		t.Fatalf("major=%d want %d", p.Major, fingerprintFallbackMajor)
	}
	if !strings.Contains(p.UA, strconv.Itoa(fingerprintFallbackMajor)) {
		t.Fatalf("UA=%q", p.UA)
	}
}

func TestDetectChromeMajorNear(t *testing.T) {
	dir := t.TempDir()
	chrome := filepath.Join(dir, "chrome.exe")
	if err := os.WriteFile(chrome, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, ver := range []string{"140.0.7339.208", "152.0.7549.97"} {
		if err := os.Mkdir(filepath.Join(dir, ver), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := detectChromeMajorNear(chrome); got != 152 {
		t.Fatalf("major=%d want 152", got)
	}
	if got := detectChromeMajorNear(""); got != 0 {
		t.Fatalf("empty path: %d", got)
	}
}

func TestVerifyLaunchedChromeMismatchFailsClosed(t *testing.T) {
	dir := t.TempDir()
	chrome := filepath.Join(dir, "chrome.exe")
	if err := os.WriteFile(chrome, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "140.0.0.0"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Simulate a disk-backed snapshot that disagrees with the launched binary.
	prevFP, prevDisk, prevPath := fp, fpSnapshotFromDisk, fpSnapshotPath
	t.Cleanup(func() {
		fp = prevFP
		fpSnapshotFromDisk = prevDisk
		fpSnapshotPath = prevPath
		chromeUA = fp.UA
		chromeSecCHUA = fp.SecCHUA
	})
	fp = buildFingerprint(152)
	fpSnapshotFromDisk = true
	fpSnapshotPath = "test-snapshot"

	err := verifyLaunchedChrome(chrome)
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("err=%v", err)
	}
}

func TestVerifyLaunchedChromeUnverifiableDoesNotFail(t *testing.T) {
	dir := t.TempDir()
	chrome := filepath.Join(dir, "chrome.exe")
	if err := os.WriteFile(chrome, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// No version dirs → unverifiable; must not fail closed.
	if err := verifyLaunchedChrome(chrome); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHeadlessArgsUseSharedFingerprint(t *testing.T) {
	fp := fingerprint()
	args := headlessChromeArgs("")
	joined := strings.Join(args, "\n")
	if !strings.Contains(joined, fp.userAgentArg()) {
		t.Fatalf("missing UA arg in %v", args)
	}
	if !strings.Contains(joined, fp.windowSizeArg()) {
		t.Fatalf("missing window-size in %v", args)
	}
	if strings.Contains(joined, "HeadlessChrome") {
		t.Fatalf("headless UA leaked: %v", args)
	}
}

func TestHeadersPullFromFingerprint(t *testing.T) {
	fp := fingerprint()
	req, err := http.NewRequest(http.MethodGet, "https://www.tumblr.com/api/v2/user/info", nil)
	if err != nil {
		t.Fatal(err)
	}
	setCommonHeaders(req, Auth{Bearer: "tok", Cookie: "sid=abc"})
	if got := req.Header.Get("User-Agent"); got != fp.UA {
		t.Fatalf("UA: %q", got)
	}
	if got := req.Header.Get("sec-ch-ua"); got != fp.SecCHUA {
		t.Fatalf("sec-ch-ua: %q", got)
	}
	if got := req.Header.Get("Accept-Language"); got != fp.AcceptLanguage {
		t.Fatalf("Accept-Language: %q", got)
	}
	if got := req.Header.Get("sec-ch-ua-platform"); got != fp.SecCHUAPlatform {
		t.Fatalf("sec-ch-ua-platform: %q", got)
	}

	html, err := http.NewRequest(http.MethodGet, "https://www.tumblr.com/dashboard", nil)
	if err != nil {
		t.Fatal(err)
	}
	setHTMLHeaders(html, Auth{Cookie: "sid=abc"})
	if got := html.Header.Get("User-Agent"); got != fp.UA {
		t.Fatalf("HTML UA: %q", got)
	}
	if got := html.Header.Get("Accept-Language"); got != fp.AcceptLanguage {
		t.Fatalf("HTML Accept-Language: %q", got)
	}
}
