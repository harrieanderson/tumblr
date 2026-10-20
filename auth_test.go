package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBlogConfigured(t *testing.T) {
	if blogConfigured("") || blogConfigured("YOUR_BLOG_NAME") || blogConfigured(" your_blog_name ") {
		t.Fatal("placeholder blog should not count as configured")
	}
	if !blogConfigured("harrie") {
		t.Fatal("real blog name should count as configured")
	}
}

func TestPrepareWorkspace(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll("config", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(authExampleFile, []byte(`{"blog":"YOUR_BLOG_NAME"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := prepareWorkspace(); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{authFile, "posts/queue.txt", "pics/posted", "data"} {
		if _, err := os.Stat(filepath.Clean(p)); err != nil {
			t.Fatalf("missing %s: %v", p, err)
		}
	}
}

func TestRejectForeignBlogSession(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("data", 0755); err != nil {
		t.Fatal(err)
	}
	saveNameMap(assignedUsernamesFile, map[string]string{
		"adrisan2009":  "lillybully",
		"yasemin1983":  "lillycosmic3",
		"583943680":    "cosmiczoeyy",
	})

	auth := Auth{path: filepath.Join(profilesAuthDir, "yasemin1983", "auth.json")}
	if err := rejectForeignBlogSession(&auth, "lillybully"); err == nil {
		t.Fatal("yasemin must not keep adrisan's @lillybully session")
	} else if !isForeignBlogSession(err) {
		t.Fatalf("want foreignBlogSessionError, got %T %v", err, err)
	}
	owner := Auth{path: filepath.Join(profilesAuthDir, "adrisan2009", "auth.json")}
	if err := rejectForeignBlogSession(&owner, "lillybully"); err != nil {
		t.Fatalf("owner must keep own blog: %v", err)
	}
	if err := rejectForeignBlogSession(&auth, "lillycosmic3"); err != nil {
		t.Fatalf("matching assigned blog should pass: %v", err)
	}
}

func TestChromePortForCookieSyncRequiresLiveAccountMatch(t *testing.T) {
	prevPort, prevAcc := liveDebugPort, liveChromeAccount
	t.Cleanup(func() {
		liveDebugPort = prevPort
		liveChromeAccount = prevAcc
	})

	liveDebugPort = "9333"
	liveChromeAccount = "adrisan2009"
	if _, ok := chromePortForCookieSync("yasemin1983"); ok {
		t.Fatal("must not attach yasemin to adrisan's Chrome")
	}
	if _, ok := chromePortForCookieSync(""); ok {
		t.Fatal("empty account must not attach")
	}
	liveChromeAccount = ""
	if _, ok := chromePortForCookieSync("adrisan2009"); ok {
		t.Fatal("must not attach when liveChromeAccount is unset")
	}
}
