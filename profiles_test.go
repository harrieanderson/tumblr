package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAccountSlug(t *testing.T) {
	if got := accountSlug("Harrieand1@outlook.com"); got != "harrieand1" {
		t.Fatalf("slug = %q", got)
	}
	if got := accountSlug("Harrie And 10"); got != "harrieand10" {
		t.Fatalf("slug = %q", got)
	}
}

func TestAccountsFromCredsList(t *testing.T) {
	enabled := true
	c := credsFile{Accounts: []Account{
		{Email: "Harrieand1@outlook.com", Password: "x", Enabled: &enabled},
		{ID: "custom", Email: "two@example.com", Password: "y"},
	}}
	got := accountsFromCreds(c)
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].ID != "harrieand1" || got[1].ID != "custom" {
		t.Fatalf("ids=%q %q", got[0].ID, got[1].ID)
	}
}

func TestParseLoginDict(t *testing.T) {
	raw := []byte(`{
		"Harrieand1@outlook.com": "pw1",
		"aiApiKey": "skip-me",
		"Harrieand2@outlook.com": "pw2"
	}`)
	got := parseLoginDict(raw)
	if len(got) != 2 {
		t.Fatalf("len=%d", len(got))
	}
	if got[0].Email != "Harrieand1@outlook.com" || got[0].Password != "pw1" {
		t.Fatalf("first=%+v", got[0])
	}
	if got[1].ID != "harrieand2" || got[1].Password != "pw2" {
		t.Fatalf("second=%+v", got[1])
	}
}

func TestParseNestedLogins(t *testing.T) {
	raw := []byte(`{"logins":{"a@x.com":"one","b@x.com":"two"},"aiApiKey":"k"}`)
	got := parseLoginDict(raw)
	if len(got) != 2 || got[0].Email != "a@x.com" || got[1].Email != "b@x.com" {
		t.Fatalf("got=%+v", got)
	}
}

func TestAuthFileIsolated(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		accountMu.Lock()
		activeAccountID = ""
		accountMu.Unlock()
		_ = os.Chdir(old)
	})
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TUMBLR_CHROME_DATA", filepath.Join(dir, "chrome-data"))
	if err := os.MkdirAll("config", 0755); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"accounts":[{"id":"harrieand1","email":"a@example.com","password":"pw","enabled":true}]}`)
	if err := os.WriteFile(credentialsFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := initAccounts(); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(profilesAuthDir, "harrieand1", "auth.json")
	if authFilePath() != want {
		t.Fatalf("auth path %s", authFilePath())
	}
	if chromeProfilePath() != filepath.Join(chromeProfilesDir(), "harrieand1") {
		t.Fatalf("chrome path %s", chromeProfilePath())
	}
}

func TestAuthFileFromDict(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		accountMu.Lock()
		activeAccountID = ""
		accountMu.Unlock()
		_ = os.Chdir(old)
	})
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("config", 0755); err != nil {
		t.Fatal(err)
	}
	raw := []byte("{\n  \"Harrieand1@outlook.com\": \"pw\"\n}\n")
	if err := os.WriteFile(credentialsFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := initAccounts(); err != nil {
		t.Fatal(err)
	}
	if authFilePath() != filepath.Join(profilesAuthDir, "harrieand1", "auth.json") {
		t.Fatalf("auth path %s", authFilePath())
	}
}

func TestSortAccountsByFollowers(t *testing.T) {
	accs := []accountPublic{
		{ID: "low", Followers: 3},
		{ID: "high", Followers: 50},
		{ID: "mid-b", Followers: 10},
		{ID: "mid-a", Followers: 10, ZoeySetup: true},
		{ID: "none", Followers: 0},
		{ID: "zoey-low", Followers: 2, ZoeySetup: true},
		{ID: "zoey-high", Followers: 20, ZoeySetup: true},
	}
	sortAccountsByFollowers(accs)
	// Followers desc; same followers → Zoey-setup before not-setup.
	want := []string{"high", "zoey-high", "mid-a", "mid-b", "low", "zoey-low", "none"}
	for i, id := range want {
		if accs[i].ID != id {
			t.Fatalf("pos %d: got %q want %q", i, accs[i].ID, id)
		}
	}
}
