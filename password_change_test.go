package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestPasswordChangeRequiredError(t *testing.T) {
	err := passwordChangeRequiredError{Email: "multivacsv@example.com", ID: "multivacsv"}
	if !isPasswordChangeRequired(err) {
		t.Fatal("should detect password-change error")
	}
	if isBadLogin(err) {
		t.Fatal("password-change is not a generic bad login")
	}
}

func TestRemoveCredentialByID(t *testing.T) {
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
	raw := []byte(`{
  "keepme@example.com": "secret1",
  "multivacsv@gmail.com": "secret2",
  "aiApiKey": "not-a-login"
}
`)
	if err := os.WriteFile(credentialsFile, raw, 0600); err != nil {
		t.Fatal(err)
	}

	removed, err := removeCredentialByID("multivacsv")
	if err != nil {
		t.Fatal(err)
	}
	if removed != "multivacsv@gmail.com" {
		t.Fatalf("removed email: %q", removed)
	}
	var obj map[string]json.RawMessage
	b, err := os.ReadFile(credentialsFile)
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(b, &obj) != nil {
		t.Fatal("rewrite not valid json")
	}
	if _, ok := obj["multivacsv@gmail.com"]; ok {
		t.Fatal("password-change account still in credentials")
	}
	if _, ok := obj["keepme@example.com"]; !ok {
		t.Fatal("kept account was removed")
	}
	if _, ok := obj["aiApiKey"]; !ok {
		t.Fatal("api key must stay")
	}
}
