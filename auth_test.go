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
