package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNeedsInteractiveLoginError(t *testing.T) {
	err := needsInteractiveLoginError{ID: "miromfs", Reason: "captcha"}
	if !isNeedsInteractiveLogin(err) {
		t.Fatal("typed error should match")
	}
	if !isNeedsInteractiveLogin(fmt.Errorf("miromfs: needs interactive login (captcha)")) {
		t.Fatal("string form should match")
	}
	if isNeedsInteractiveLogin(fmt.Errorf("csrf refresh failed: 401")) {
		t.Fatal("plain 401 is not interactive-login")
	}
	if !wipeSessionSoftSkip(err) {
		t.Fatal("wipe must soft-skip needs interactive login")
	}
}

func TestMarkSessionValidatedClearsFlags(t *testing.T) {
	auth := &Auth{
		Cookie:                "sid=abc; tz=Europe/London",
		NeedsInteractiveLogin: true,
		RecoveryAttempts:      2,
		LastRecoveryAt:        "old",
		path:                  filepath.Join(t.TempDir(), "auth.json"),
	}
	markSessionValidated(auth)
	if auth.NeedsInteractiveLogin || auth.RecoveryAttempts != 0 || auth.LastRecoveryAt != "" {
		t.Fatalf("flags not cleared: %+v", auth)
	}
	if auth.ValidatedAt == "" {
		t.Fatal("ValidatedAt unset")
	}
	if _, err := time.Parse(time.RFC3339, auth.ValidatedAt); err != nil {
		t.Fatalf("ValidatedAt: %v", err)
	}
}

func TestMarkNeedsInteractiveLoginPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	auth := &Auth{
		Cookie: "sid=dead",
		path:   path,
	}
	err := markNeedsInteractiveLogin(auth, "captcha")
	if !isNeedsInteractiveLogin(err) {
		t.Fatalf("want needsInteractiveLoginError, got %v", err)
	}
	if !auth.NeedsInteractiveLogin {
		t.Fatal("flag not set in memory")
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var got Auth
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !got.NeedsInteractiveLogin {
		t.Fatalf("flag not on disk: %s", data)
	}
}

func TestSessionBlocksChromeRecovery(t *testing.T) {
	if sessionBlocksChromeRecovery(nil) {
		t.Fatal("nil")
	}
	a := &Auth{NeedsInteractiveLogin: true}
	if !sessionBlocksChromeRecovery(a) {
		t.Fatal("should block")
	}
	a.NeedsInteractiveLogin = false
	if sessionBlocksChromeRecovery(a) {
		t.Fatal("should allow")
	}
}

func TestRecoveryAttemptsExhausted(t *testing.T) {
	a := &Auth{RecoveryAttempts: sessionMaxRecoveryAttempts - 1}
	if recoveryAttemptsExhausted(a) {
		t.Fatal("not yet")
	}
	noteRecoveryAttempt(a)
	if !recoveryAttemptsExhausted(a) {
		t.Fatal("exhausted after note")
	}
}

func TestPreflightNoSIDRespectsInteractiveFlag(t *testing.T) {
	auth := &Auth{
		NeedsInteractiveLogin: true,
		path:                  filepath.Join(t.TempDir(), "auth.json"),
	}
	err := preflightSession(http.DefaultClient, auth)
	if !isNeedsInteractiveLogin(err) {
		t.Fatalf("flagged auth without sid must fail preflight: %v", err)
	}
}

func TestSaveAuthAtomicRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	auth := Auth{
		Cookie:      "sid=xyz; cl_pref=show",
		CSRF:        "tok",
		Blog:        "t:test",
		ValidatedAt: time.Now().UTC().Format(time.RFC3339),
		ProxyID:     "ash-05",
		path:        path,
	}
	if err := saveAuth(auth); err != nil {
		t.Fatal(err)
	}
	got, err := loadAuthFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProxyID != "ash-05" || got.ValidatedAt == "" || !authHasSID(got) {
		t.Fatalf("round-trip: %+v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0077 != 0 {
		// On Windows ACLs differ; only assert file exists + parses.
		t.Logf("perm=%v (Windows may ignore 0600)", info.Mode())
	}
}

func TestRecoverSessionRespectsInteractiveFlag(t *testing.T) {
	auth := &Auth{
		Cookie:                "sid=stale",
		NeedsInteractiveLogin: true,
		path:                  filepath.Join(t.TempDir(), "auth.json"),
	}
	client := &http.Client{Timeout: time.Second}
	err := recoverSession(client, auth, false)
	if !isNeedsInteractiveLogin(err) {
		t.Fatalf("want interactive block, got %v", err)
	}
}
