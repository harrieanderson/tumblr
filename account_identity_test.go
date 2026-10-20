package main

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelectVerifiedAccountBlog(t *testing.T) {
	blogs := []tumblrBlog{{Name: "zoeyofc", UUID: "t:wrong", Primary: true}, {Name: "lillybully", UUID: "t:right"}}
	got, err := selectVerifiedAccountBlog("adrisan2009", "t:wrong", "lillybully", blogs)
	if err != nil || got.UUID != "t:right" {
		t.Fatalf("target=%+v err=%v", got, err)
	}
	if _, err := selectVerifiedAccountBlog("adrisan2009", "t:wrong", "lillybully", blogs[:1]); err == nil {
		t.Fatal("wrong primary session accepted")
	}
	if _, err := selectVerifiedAccountBlog("other", "t:missing", "", blogs); err == nil {
		t.Fatal("unowned target accepted")
	}
	if _, err := selectVerifiedAccountBlog("other", "t:wrong", "", blogs); err != nil {
		t.Fatal(err)
	}
}

type identityRoundTripper func(*http.Request) (*http.Response, error)

func (f identityRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestWrongAccountCannotCreateTextPost(t *testing.T) {
	t.Chdir(t.TempDir())
	saveNameMap(assignedUsernamesFile, map[string]string{"adrisan2009": "lillybully"})
	posts := 0
	client := &http.Client{Transport: identityRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost {
			posts++
			t.Error("a post was attempted using the wrong account")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"response":{"user":{"blogs":[{"name":"zoeyofc","uuid":"t:wrong","primary":true}]}}}`))}, nil
	})}
	auth := Auth{Blog: "t:wrong", path: filepath.Join(profilesAuthDir, "adrisan2009", "auth.json")}
	if err := createPost(client, auth, "test"); err == nil {
		t.Fatal("expected identity mismatch")
	}
	if posts != 0 {
		t.Fatal("published despite mismatch")
	}
	if err := createPhotoPostOnPinned(client, auth, "t:wrong", "missing.jpg", "test", false); err == nil || !strings.Contains(err.Error(), "expects @lillybully") {
		t.Fatalf("photo should fail identity check before upload: %v", err)
	}
}

func TestWrongSnapshotCannotRelabelAccount(t *testing.T) {
	t.Chdir(t.TempDir())
	saveNameMap(assignedUsernamesFile, map[string]string{"adrisan2009": "lillybully"})
	log := followerLog{Latest: followerSnapshot{Primary: "zoeyofc", Followers: 99, Blogs: map[string]int{"zoeyofc": 99}}}
	name, count := bestAccountBlogStats("adrisan2009", log)
	if name != "lillybully" || count != 0 {
		t.Fatalf("got @%s %d", name, count)
	}
}
