package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestWipeIgnorablePostID(t *testing.T) {
	if !wipeIgnorablePostID("google_native_6adcd94cb2a55") {
		t.Fatal("google_native should be ignored")
	}
	if wipeIgnorablePostID("830349961426288640") {
		t.Fatal("numeric post id should wipe")
	}
}

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

func TestWipeSessionSoftSkip(t *testing.T) {
	if !wipeSessionSoftSkip(fmt.Errorf("csrf refresh failed: 401 Unauthorized")) {
		t.Fatal("csrf 401 should soft-skip")
	}
	if !wipeSessionSoftSkip(fmt.Errorf("waiting for captcha")) {
		t.Fatal("captcha should soft-skip")
	}
	if !wipeSessionSoftSkip(badLoginError{Email: "a@b.com", ID: "a"}) {
		t.Fatal("bad login should soft-skip")
	}
	if !wipeSessionSoftSkip(fmt.Errorf("no blog name: missing")) {
		t.Fatal("no blog should soft-skip")
	}
	if !wipeSessionSoftSkip(needsInteractiveLoginError{ID: "miromfs", Reason: "captcha"}) {
		t.Fatal("needs interactive login should soft-skip")
	}
	if wipeSessionSoftSkip(fmt.Errorf("safety stop after 5000 actions")) {
		t.Fatal("safety stop must not soft-skip")
	}
}

func TestWipeParallelCap(t *testing.T) {
	if got := wipeParallelCap(0, 80); got != wipeDefaultParallel {
		t.Fatalf("default: got %d want %d", got, wipeDefaultParallel)
	}
	if got := wipeParallelCap(99, 80); got != wipeMaxParallel {
		t.Fatalf("max: got %d want %d", got, wipeMaxParallel)
	}
	if got := wipeParallelCap(10, 3); got != 3 {
		t.Fatalf("pool cap: got %d want 3", got)
	}
	if wipeDefaultParallel < 8 || wipeDefaultParallel > 12 {
		t.Fatalf("default parallel %d outside agreed 8–12", wipeDefaultParallel)
	}
	if wipeDeleteWorkers < 3 || wipeDeleteWorkers > 4 {
		t.Fatalf("workers %d outside agreed 3–4", wipeDeleteWorkers)
	}
	if wipePageSize != 100 {
		t.Fatalf("page size %d want 100", wipePageSize)
	}
	if wipeBulkLimit != 100 {
		t.Fatalf("bulk limit %d want 100", wipeBulkLimit)
	}
	if wipeSafetyLimit != 12000 {
		t.Fatalf("safety %d want 12000", wipeSafetyLimit)
	}
}

func TestWipeDeleteBatchDryRun(t *testing.T) {
	var delN, failN atomic.Int64
	var csrfMu sync.Mutex
	var logs []string
	wipeDeleteBatch(nil, nil, "blog", []string{"1", "2", "3"}, "", true, &csrfMu, &delN, &failN, func(f string, a ...any) {
		logs = append(logs, fmt.Sprintf(f, a...))
	})
	if delN.Load() != 3 || failN.Load() != 0 {
		t.Fatalf("deleted=%d failed=%d", delN.Load(), failN.Load())
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "would bulk-delete 3") {
		t.Fatalf("logs=%v", logs)
	}
}

func TestDeleteBlogPostsBulkChunksViaMock(t *testing.T) {
	var hits atomic.Int64
	var lastIDs string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/delete_posts" {
			hits.Add(1)
			_ = r.ParseForm()
			lastIDs = r.Form.Get("post_ids")
			if r.Form.Get("form_key") == "" {
				t.Error("missing form_key")
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"meta":{"status":200,"msg":"OK"},"response":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	auth := &Auth{Cookie: "sid=test", Bearer: "b", Blog: "testblog", CSRF: "csrf"}
	client := &http.Client{Transport: rewriteHostTransport{target: srv.URL}}
	ids := make([]string, 0, 105)
	for i := 0; i < 105; i++ {
		ids = append(ids, fmt.Sprintf("%d", 1000+i))
	}
	var csrfMu sync.Mutex
	var delN, failN atomic.Int64
	wipeDeleteBatch(client, auth, "testblog", ids, "fk-test", false, &csrfMu, &delN, &failN, func(string, ...any) {})
	if hits.Load() != 2 {
		t.Fatalf("bulk hits=%d want 2 (100+5), last=%q", hits.Load(), lastIDs)
	}
	if delN.Load() != 105 || failN.Load() != 0 {
		t.Fatalf("deleted=%d failed=%d", delN.Load(), failN.Load())
	}
}

func TestDeleteBlogPostGuardedConcurrentCSRF(t *testing.T) {
	var hits atomic.Int64
	var sawCSRF sync.Map
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.Contains(r.URL.Path, "/post/delete") {
			// form_key scrape / other probes may hit this host — ignore.
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		csrf := r.Header.Get("X-CSRF")
		sawCSRF.Store(csrf, true)
		body, _ := io.ReadAll(r.Body)
		_ = body
		w.Header().Set("X-CSRF", "rotated-"+csrf)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Point delete at the test server by overriding via a custom RoundTripper
	// that rewrites tumblr hosts — keep deleteBlogPostGuarded URL logic intact
	// by using a transport that forwards all requests to srv.
	auth := &Auth{Cookie: "sid=test", Bearer: "b", Blog: "testblog", CSRF: "token-a"}
	client := &http.Client{Transport: rewriteHostTransport{target: srv.URL}}

	ids := []string{"10", "11", "12", "13", "14", "15", "16", "17"}
	var csrfMu sync.Mutex
	var delN, failN atomic.Int64
	// Empty formKey forces per-post /post/delete path (CSRF rotation coverage).
	wipeDeleteBatch(client, auth, "testblog", ids, "", false, &csrfMu, &delN, &failN, func(string, ...any) {})
	if delN.Load() != int64(len(ids)) {
		t.Fatalf("deleted=%d want %d (failed=%d hits=%d)", delN.Load(), len(ids), failN.Load(), hits.Load())
	}
	if failN.Load() != 0 {
		t.Fatalf("failed=%d", failN.Load())
	}
	if hits.Load() < int64(len(ids)) {
		t.Fatalf("hits=%d", hits.Load())
	}
	if auth.CSRF == "" || auth.CSRF == "token-a" {
		t.Fatalf("csrf not rotated: %q", auth.CSRF)
	}
}

type rewriteHostTransport struct {
	target string
}

func (t rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := strings.TrimRight(t.target, "/")
	tu, err := http.NewRequest(req.Method, base+req.URL.RequestURI(), req.Body)
	if err != nil {
		return nil, err
	}
	tu.Header = req.Header.Clone()
	return http.DefaultTransport.RoundTrip(tu)
}
