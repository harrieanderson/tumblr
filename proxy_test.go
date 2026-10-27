package main

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testProxyCfg(urls ...string) ProxyConfig {
	cfg := defaultProxyConfig()
	cfg.Enabled = true
	cfg.StickyMinutes = 30
	cfg.MinRequestGapMs = 0
	cfg.PerProxyMaxConcurrent = 4
	cfg.PerAccountRPM = 100
	cfg.PerProxyRPM = 100
	cfg.Backoff.JitterPercent = 0
	for i, u := range urls {
		cfg.Proxies = append(cfg.Proxies, ProxyEntry{
			Label: string(rune('a' + i)),
			URL:   u,
		})
	}
	return cfg
}

func withSharedProxy(t *testing.T, r *proxyRouter) {
	t.Helper()
	_ = proxyPool()
	prev := proxyShared
	proxyShared = r
	t.Cleanup(func() { proxyShared = prev })
}

func bindStickyViaLease(t *testing.T, r *proxyRouter, accountID string) string {
	t.Helper()
	id, err := r.acquireLease(context.Background(), accountID)
	if err != nil || id == "" {
		t.Fatalf("lease %s: %v %q", accountID, err, id)
	}
	r.releaseLease(id)
	return id
}

func TestPickProxyJugglesAccountsAcrossPool(t *testing.T) {
	r := newProxyRouter(testProxyCfg(
		"http://user:pass@p1.example:8080",
		"http://user:pass@p2.example:8080",
		"http://user:pass@p3.example:8080",
	))

	seen := map[string]string{}
	for _, acc := range []string{"acc1", "acc2", "acc3", "acc4", "acc5", "acc6"} {
		seen[acc] = bindStickyViaLease(t, r, acc)
	}

	used := map[string]int{}
	for _, id := range seen {
		used[id]++
	}
	if len(used) < 2 {
		t.Fatalf("expected accounts spread across proxies, got %#v", seen)
	}
	for _, n := range used {
		if n > 3 {
			t.Fatalf("load uneven: %#v", used)
		}
	}
}

func TestPickProxyStickyThenRebalanceAfterCooldown(t *testing.T) {
	r := newProxyRouter(testProxyCfg(
		"http://p1.example:8080",
		"http://p2.example:8080",
	))

	ep1ID := bindStickyViaLease(t, r, "zoey1")
	ep1b, _ := r.pickProxy("zoey1")
	if ep1b == nil || ep1ID != ep1b.id {
		t.Fatalf("sticky broken: %s vs %v", ep1ID, ep1b)
	}

	r.reportResult("zoey1", ep1ID, http.StatusTooManyRequests, nil, http.Header{
		"Retry-After": []string{"30"},
	})

	ep2, _ := r.pickProxy("zoey1")
	if ep2 == nil || ep2.id == ep1ID {
		t.Fatalf("expected reassignment after cooldown, still on %s", ep1ID)
	}
}

func TestReportResult403ClearsAccountStickyOnly(t *testing.T) {
	r := newProxyRouter(testProxyCfg(
		"http://p1.example:8080",
		"http://p2.example:8080",
	))
	ep1ID := bindStickyViaLease(t, r, "zoey1")
	_ = bindStickyViaLease(t, r, "zoey2")

	wait := r.reportResult("zoey1", ep1ID, http.StatusForbidden, nil, nil)
	if wait != 0 {
		t.Fatalf("403 should not return proxy cooldown wait, got %s", wait)
	}

	r.mu.Lock()
	rt := r.runtime(ep1ID)
	cooled := rt.cooldownUntil.After(time.Now())
	_, zoey1Assigned := r.assign["zoey1"]
	_, zoey2Assigned := r.assign["zoey2"]
	r.mu.Unlock()
	if cooled {
		t.Fatal("403 must not cool shared proxy")
	}
	if zoey1Assigned {
		t.Fatal("expected zoey1 sticky cleared")
	}
	if !zoey2Assigned {
		t.Fatal("zoey2 sticky should remain")
	}
}

func TestForbiddenDoesNotCoolSharedProxy(t *testing.T) {
	// Contrast: 403 is account-scoped; 429 cools the shared endpoint.
	r := newProxyRouter(testProxyCfg(
		"http://p1.example:8080",
		"http://p2.example:8080",
	))
	epID := bindStickyViaLease(t, r, "accA")
	// Force both onto the same proxy for the contrast.
	r.mu.Lock()
	r.assign["accB"] = proxyAssignment{ProxyID: epID, StickyUntil: time.Now().Add(time.Hour)}
	r.mu.Unlock()

	_ = r.reportResult("accA", epID, http.StatusForbidden, nil, nil)
	if r.inflightFor(epID) != 0 {
		t.Fatalf("unexpected inflight")
	}
	r.mu.Lock()
	cooledAfter403 := r.runtime(epID).cooldownUntil.After(time.Now())
	_, bStill := r.assign["accB"]
	r.mu.Unlock()
	if cooledAfter403 {
		t.Fatal("403 cooled proxy")
	}
	if !bStill {
		t.Fatal("accB sticky cleared by peer 403")
	}

	_ = r.reportResult("accB", epID, http.StatusTooManyRequests, nil, http.Header{"Retry-After": []string{"5"}})
	r.mu.Lock()
	cooledAfter429 := r.runtime(epID).cooldownUntil.After(time.Now())
	_, aSticky := r.assign["accA"]
	_, bSticky := r.assign["accB"]
	r.mu.Unlock()
	if !cooledAfter429 {
		t.Fatal("429 should cool shared proxy")
	}
	if aSticky || bSticky {
		t.Fatal("429 should clear all stickies to that proxy")
	}
}

func TestAcquireLeaseDoesNotOversubscribe(t *testing.T) {
	r := newProxyRouter(testProxyCfg("http://p1.example:8080"))
	r.cfg.PerProxyMaxConcurrent = 1
	r.cfg.MinRequestGapMs = 0
	r.cfg.PerAccountRPM = 1000
	r.cfg.PerProxyRPM = 1000
	// Instant sleep so waiters re-check quickly without wall-clock delays.
	r.sleep = func(ctx context.Context, d time.Duration) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}

	var peak atomic.Int32
	var wg sync.WaitGroup
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id, err := r.acquireLease(ctx, "acc"+string(rune('a'+n)))
			if err != nil {
				t.Errorf("lease: %v", err)
				return
			}
			cur := int32(r.inflightFor(id))
			for {
				prev := peak.Load()
				if cur <= prev || peak.CompareAndSwap(prev, cur) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			r.releaseLease(id)
		}(i)
	}
	wg.Wait()
	if peak.Load() > 1 {
		t.Fatalf("inFlight peaked at %d, want <= 1", peak.Load())
	}
	if r.inflightFor(r.pool[0].id) != 0 {
		t.Fatal("leaked inFlight after all releases")
	}
}

func TestAcquireLeaseRespectsContext(t *testing.T) {
	r := newProxyRouter(testProxyCfg("http://p1.example:8080"))
	r.cfg.PerProxyMaxConcurrent = 1
	r.cfg.MinRequestGapMs = 0
	r.cfg.PerAccountRPM = 1000
	r.cfg.PerProxyRPM = 1000

	held, err := r.acquireLease(context.Background(), "holder")
	if err != nil || held == "" {
		t.Fatalf("hold lease: %v %q", err, held)
	}
	defer r.releaseLease(held)

	parked := make(chan struct{})
	r.sleep = func(ctx context.Context, d time.Duration) error {
		close(parked)
		<-ctx.Done()
		return ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := r.acquireLease(ctx, "waiter")
		errCh <- err
	}()

	select {
	case <-parked:
	case <-time.After(2 * time.Second):
		t.Fatal("waiter never entered sleep")
	}
	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected ctx error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("acquireLease did not return on cancel")
	}
	if r.inflightFor(held) != 1 {
		t.Fatalf("cancel must not touch held lease, inflight=%d", r.inflightFor(held))
	}
	r.mu.Lock()
	_, waiterSticky := r.assign["waiter"]
	r.mu.Unlock()
	if waiterSticky {
		t.Fatal("cancel before lease must leave assign empty for waiter")
	}
}

func TestConcurrentLeasesNeverExceedCap(t *testing.T) {
	r := newProxyRouter(testProxyCfg("http://p1.example:8080"))
	r.cfg.PerProxyMaxConcurrent = 2
	r.cfg.MinRequestGapMs = 0
	r.cfg.PerAccountRPM = 1000
	r.cfg.PerProxyRPM = 1000
	r.sleep = func(ctx context.Context, d time.Duration) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			time.Sleep(time.Millisecond)
			return nil
		}
	}

	gate := make(chan struct{})
	var peak atomic.Int32
	var wg sync.WaitGroup
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id, err := r.acquireLease(ctx, "c"+string(rune('a'+n)))
			if err != nil {
				t.Errorf("lease: %v", err)
				return
			}
			cur := int32(r.inflightFor(id))
			for {
				prev := peak.Load()
				if cur <= prev || peak.CompareAndSwap(prev, cur) {
					break
				}
			}
			<-gate
			r.releaseLease(id)
		}(i)
	}
	// Let leases settle at cap, then sample peak under load.
	time.Sleep(50 * time.Millisecond)
	if got := r.inflightFor(r.pool[0].id); got > 2 {
		t.Fatalf("inflight=%d want <= 2 before release", got)
	}
	close(gate)
	wg.Wait()
	if peak.Load() > 2 {
		t.Fatalf("peak=%d want <= 2", peak.Load())
	}
	if r.inflightFor(r.pool[0].id) != 0 {
		t.Fatal("inFlight not zero after cancel/release")
	}
}

func TestFreshAssignSkipsAtCapProxy(t *testing.T) {
	r := newProxyRouter(testProxyCfg(
		"http://p1.example:8080",
		"http://p2.example:8080",
	))
	r.cfg.PerProxyMaxConcurrent = 1

	p1 := r.pool[0]
	p2 := r.pool[1]
	r.mu.Lock()
	r.runtime(p1.id).inFlight = 1 // at cap
	r.mu.Unlock()

	ep, err := r.pickProxy("fresh-acc")
	if err != nil || ep == nil {
		t.Fatalf("pick: %v %v", ep, err)
	}
	if ep.id != p2.id {
		t.Fatalf("fresh assign landed on at-cap %s, want free %s", ep.id, p2.id)
	}
	r.mu.Lock()
	_, has := r.assign["fresh-acc"]
	r.mu.Unlock()
	if has {
		t.Fatal("pickProxy must not commit sticky for unbound accounts")
	}
}

func TestStickyAtCapDoesNotHop(t *testing.T) {
	// Documented tradeoff: wait-on-sticky may starve idle peers under sticky load.
	// No timed sticky-break in this pass — StickyUntil / caller ctx bound the wait.
	r := newProxyRouter(testProxyCfg(
		"http://p1.example:8080",
		"http://p2.example:8080",
	))
	r.cfg.PerProxyMaxConcurrent = 1

	stickyID := bindStickyViaLease(t, r, "sticky-acc")
	free := r.pool[0]
	if free.id == stickyID {
		free = r.pool[1]
	}

	r.mu.Lock()
	r.runtime(stickyID).inFlight = 1 // saturated
	r.runtime(free.id).inFlight = 0  // idle peer available
	r.mu.Unlock()

	ep, err := r.pickProxy("sticky-acc")
	if err != nil || ep == nil {
		t.Fatalf("pick: %v %v", ep, err)
	}
	if ep.id != stickyID {
		t.Fatalf("sticky hopped to %s (idle peer existed); want wait-on-sticky on %s", ep.id, stickyID)
	}
}

func TestFreshSoftPickRebindsAfterCapFrees(t *testing.T) {
	// Both peers at cap → soft-pick + wait → free one peer → fresh account
	// recomputes and binds to the freed peer (not the initially soft-picked one).
	r := newProxyRouter(testProxyCfg(
		"http://p1.example:8080",
		"http://p2.example:8080",
	))
	r.cfg.PerProxyMaxConcurrent = 1
	r.cfg.MinRequestGapMs = 0
	r.cfg.PerAccountRPM = 1000
	r.cfg.PerProxyRPM = 1000

	p1 := r.pool[0]
	p2 := r.pool[1]
	r.mu.Lock()
	r.runtime(p1.id).inFlight = 1
	r.runtime(p2.id).inFlight = 1
	soft := r.pickProxyLocked("fresh-rebind")
	if soft == nil {
		r.mu.Unlock()
		t.Fatal("expected soft-pick candidate while all at cap")
	}
	softID := soft.id
	freeID := p2.id
	if freeID == softID {
		freeID = p1.id
	}
	r.mu.Unlock()

	enteredWait := make(chan struct{})
	releaseGate := make(chan struct{})
	var sawAssignDuringWait atomic.Bool
	r.sleep = func(ctx context.Context, d time.Duration) error {
		select {
		case <-enteredWait:
		default:
			close(enteredWait)
		}
		r.mu.Lock()
		_, has := r.assign[accountSlug("fresh-rebind")]
		persisted := r.persistedHasAccountLocked("fresh-rebind")
		r.mu.Unlock()
		if has || persisted {
			sawAssignDuringWait.Store(true)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-releaseGate:
			return nil
		}
	}

	errCh := make(chan error, 1)
	var gotID string
	go func() {
		id, err := r.acquireLease(context.Background(), "fresh-rebind")
		gotID = id
		errCh <- err
	}()

	select {
	case <-enteredWait:
	case <-time.After(2 * time.Second):
		t.Fatal("fresh account never entered wait")
	}

	r.mu.Lock()
	_, has := r.assign["fresh-rebind"]
	persisted := r.persistedHasAccountLocked("fresh-rebind")
	r.mu.Unlock()
	if has || persisted {
		t.Fatal("saveState/assign must not contain fresh account while waiting")
	}

	// Free the peer that was not soft-picked; acquireLease must recompute.
	r.mu.Lock()
	r.runtime(freeID).inFlight = 0
	r.mu.Unlock()
	close(releaseGate)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("acquireLease: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("acquireLease timed out after peer freed")
	}
	if sawAssignDuringWait.Load() {
		t.Fatal("fresh account appeared in assign/persisted state during wait")
	}
	if gotID != freeID {
		t.Fatalf("bound to %s (soft-pick was %s), want freed peer %s", gotID, softID, freeID)
	}
	r.mu.Lock()
	a, ok := r.assign[accountSlug("fresh-rebind")]
	r.mu.Unlock()
	if !ok || a.ProxyID != freeID {
		t.Fatalf("sticky after lease = %#v ok=%v, want %s", a, ok, freeID)
	}
	r.releaseLease(gotID)

	// Cancel before lease acquisition leaves assign empty.
	r.mu.Lock()
	r.runtime(p1.id).inFlight = 1
	r.runtime(p2.id).inFlight = 1
	delete(r.assign, accountSlug("fresh-cancel"))
	r.mu.Unlock()

	enteredWait2 := make(chan struct{})
	r.sleep = func(ctx context.Context, d time.Duration) error {
		close(enteredWait2)
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh2 := make(chan error, 1)
	go func() {
		_, err := r.acquireLease(ctx, "fresh-cancel")
		errCh2 <- err
	}()
	select {
	case <-enteredWait2:
	case <-time.After(2 * time.Second):
		t.Fatal("cancel path never entered wait")
	}
	r.mu.Lock()
		_, has = r.assign[accountSlug("fresh-cancel")]
	r.mu.Unlock()
	if has {
		t.Fatal("assign written while waiting before cancel")
	}
	cancel()
	select {
	case err := <-errCh2:
		if err == nil {
			t.Fatal("expected ctx error on cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("acquireLease did not return on cancel")
	}
	r.mu.Lock()
	_, has = r.assign[accountSlug("fresh-cancel")]
	persisted = r.persistedHasAccountLocked("fresh-cancel")
	r.mu.Unlock()
	if has || persisted {
		t.Fatal("cancel before lease left sticky in assign/persisted state")
	}
}

func TestPacedTransportClosesBodyBeforeRetry(t *testing.T) {
	r := newProxyRouter(testProxyCfg("http://p1.example:8080"))
	r.cfg.Backoff.InitialSeconds = 1
	r.cfg.Backoff.JitterPercent = 0
	r.cfg.MinRequestGapMs = 0
	r.cfg.PerAccountRPM = 1000
	r.cfg.PerProxyRPM = 1000
	var slept atomic.Int32
	r.sleep = func(ctx context.Context, d time.Duration) error {
		slept.Add(1)
		return nil
	}
	withSharedProxy(t, r)

	var bodiesClosed atomic.Int32
	var calls atomic.Int32
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		n := calls.Add(1)
		body := &closeTracker{Reader: strings.NewReader("rate limited"), closed: &bodiesClosed}
		if n == 1 {
			return &http.Response{
				StatusCode: http.StatusTooManyRequests,
				Header:     http.Header{"Retry-After": []string{"1"}},
				Body:       body,
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Request:    req,
		}, nil
	})

	rt := &pacedProxyTransport{base: base, accountID: "retryacc"}
	req, err := http.NewRequest(http.MethodGet, "https://www.tumblr.com/api/v2/user/info", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if calls.Load() < 2 {
		t.Fatalf("expected retry, calls=%d", calls.Load())
	}
	if bodiesClosed.Load() < 1 {
		t.Fatal("first response body was not closed before retry")
	}
	if slept.Load() < 1 {
		t.Fatal("expected injectable sleep before retry")
	}
}

func TestRoundTripPanicReleasesLease(t *testing.T) {
	r := newProxyRouter(testProxyCfg("http://p1.example:8080"))
	r.cfg.PerProxyMaxConcurrent = 1
	r.cfg.MinRequestGapMs = 0
	r.cfg.PerAccountRPM = 1000
	r.cfg.PerProxyRPM = 1000
	withSharedProxy(t, r)

	proxyID := r.pool[0].id
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		panic("boom")
	})
	rt := &pacedProxyTransport{base: base, accountID: "panic-acc"}
	req, err := http.NewRequest(http.MethodGet, "https://www.tumblr.com/api/v2/user/info", nil)
	if err != nil {
		t.Fatal(err)
	}

	func() {
		defer func() {
			if rec := recover(); rec == nil {
				t.Fatal("expected panic to propagate")
			}
		}()
		_, _ = rt.RoundTrip(req)
		t.Fatal("RoundTrip returned after panic")
	}()

	if got := r.inflightFor(proxyID); got != 0 {
		t.Fatalf("lease leaked after panic: inflight=%d", got)
	}
}

func TestBackoffHonorsRetryAfterCap(t *testing.T) {
	r := newProxyRouter(testProxyCfg("http://p1.example:8080"))
	r.cfg.RespectRetryAfter = true
	r.cfg.Backoff.InitialSeconds = 5
	r.cfg.Backoff.MaxSeconds = 60
	r.cfg.Backoff.JitterPercent = 0

	d := r.backoffDuration(1, 45*time.Second)
	if d < 45*time.Second || d > 60*time.Second {
		t.Fatalf("expected Retry-After-driven wait ~45s, got %s", d)
	}

	d2 := r.backoffDuration(1, 5*time.Minute)
	if d2 != 60*time.Second {
		t.Fatalf("expected cap at maxSeconds, got %s", d2)
	}
}

func TestParseRetryAfter(t *testing.T) {
	if d := parseRetryAfter(http.Header{"Retry-After": []string{"12"}}); d != 12*time.Second {
		t.Fatalf("seconds: got %s", d)
	}
	if d := parseRetryAfter(nil); d != 0 {
		t.Fatalf("nil: got %s", d)
	}
}

func TestRedactProxyURL(t *testing.T) {
	got := redactProxyURL("http://alice:secret@host:8080")
	if got == "" || containsAny(got, "secret", "alice") {
		t.Fatalf("credentials leaked: %q", got)
	}
}

func TestProxyFuncDisabledFallsBackToEnv(t *testing.T) {
	r := newProxyRouter(defaultProxyConfig())
	if r.enabled() {
		t.Fatal("default should be disabled")
	}
}

func TestProxyIDOmitsCredentials(t *testing.T) {
	id := proxyIDFor("us-1", "http://alice:secret@host.example:8080")
	if containsAny(id, "alice", "secret") {
		t.Fatalf("id leaked creds: %q", id)
	}
	u, _ := url.Parse("http://alice:secret@host.example:8080")
	if u.User == nil {
		t.Fatal("parse sanity")
	}
}

func TestProxyURLForStickyReadOnly(t *testing.T) {
	r := newProxyRouter(testProxyCfg(
		"http://p1.example:8080",
		"http://p2.example:8080",
	))
	u, err := r.proxyURLFor("fresh-readonly")
	if err != nil || u != nil {
		t.Fatalf("unbound proxyURLFor want nil, got %v err=%v", u, err)
	}
	r.mu.Lock()
	_, has := r.assign["fresh-readonly"]
	r.mu.Unlock()
	if has {
		t.Fatal("proxyURLFor must not commit sticky for unbound accounts")
	}

	boundID := bindStickyViaLease(t, r, "fresh-readonly")
	u, err = r.proxyURLFor("fresh-readonly")
	if err != nil || u == nil {
		t.Fatalf("bound proxyURLFor: %v %v", u, err)
	}
	ep := r.byID[boundID]
	if ep == nil || u.String() != ep.url.String() {
		t.Fatalf("URL %v want %v", u, ep)
	}
}

func TestEnsureStickyProxyBindsAndReleasesInFlight(t *testing.T) {
	r := newProxyRouter(testProxyCfg(
		"http://user:pass@p1.example:8080",
		"http://user:pass@p2.example:8080",
	))
	r.cfg.MinRequestGapMs = 0
	r.cfg.PerAccountRPM = 1000
	r.cfg.PerProxyRPM = 1000
	r.cfg.PerProxyMaxConcurrent = 1
	r.resetFreshBindCount()

	u, id, err := r.ensureStickyProxy(context.Background(), "watch-acc")
	if err != nil || u == nil || id == "" {
		t.Fatalf("ensureStickyProxy: u=%v id=%q err=%v", u, id, err)
	}
	if got := r.inflightFor(id); got != 0 {
		t.Fatalf("browser ensure must release inFlight, got %d", got)
	}
	if r.freshBinds() != 1 {
		t.Fatalf("freshBind=%d want 1", r.freshBinds())
	}
	a, ok := r.assign[accountSlug("watch-acc")]
	if !ok || a.ProxyID != id {
		t.Fatalf("sticky assign=%#v ok=%v", a, ok)
	}

	// Second call refreshes sticky, no new bind, still zero inFlight.
	u2, id2, err := r.ensureStickyProxy(context.Background(), "watch-acc")
	if err != nil || u2 == nil || id2 != id {
		t.Fatalf("refresh: u=%v id=%q err=%v want %s", u2, id2, err, id)
	}
	if r.freshBinds() != 1 {
		t.Fatalf("refresh must not re-bind, freshBind=%d", r.freshBinds())
	}
	if got := r.inflightFor(id); got != 0 {
		t.Fatalf("inFlight after refresh=%d", got)
	}
}

func TestEnsureStickyProxyCancel(t *testing.T) {
	r := newProxyRouter(testProxyCfg("http://p1.example:8080"))
	r.cfg.PerProxyMaxConcurrent = 1
	r.cfg.MinRequestGapMs = 0
	r.cfg.PerAccountRPM = 1000
	r.cfg.PerProxyRPM = 1000
	held, err := r.acquireLease(context.Background(), "holder")
	if err != nil || held == "" {
		t.Fatalf("hold: %v %q", err, held)
	}
	defer r.releaseLease(held)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = r.ensureStickyProxy(ctx, "watch-cancel")
	if err == nil {
		t.Fatal("expected cancel error while sticky peer saturated")
	}
	if _, ok := r.assign[accountSlug("watch-cancel")]; ok {
		t.Fatal("cancel must not leave sticky for watch-cancel")
	}
}

func TestCrossPathNakedProxyURLForDoesNotBind(t *testing.T) {
	// Paced lease is the only commit path; naked proxyURLFor must not bind
	// under concurrent pressure with acquireLease.
	r := newProxyRouter(testProxyCfg(
		"http://p1.example:8080",
		"http://p2.example:8080",
	))
	r.cfg.MinRequestGapMs = 0
	r.cfg.PerAccountRPM = 1000
	r.cfg.PerProxyRPM = 1000
	withSharedProxy(t, r)

	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = r.proxyURLFor("cross-acc")
		}()
	}
	wg.Wait()
	r.mu.Lock()
	_, has := r.assign["cross-acc"]
	r.mu.Unlock()
	if has {
		t.Fatal("naked proxyURLFor wrote sticky")
	}

	for i := 0; i < 24; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			id, err := r.acquireLease(context.Background(), "cross-acc")
			if err == nil && id != "" {
				r.releaseLease(id)
			}
		}()
		go func() {
			defer wg.Done()
			_, _ = r.proxyURLFor("cross-acc")
		}()
	}
	wg.Wait()

	r.mu.Lock()
	a, ok := r.assign[accountSlug("cross-acc")]
	n := 0
	for acc := range r.assign {
		if acc == accountSlug("cross-acc") {
			n++
		}
	}
	r.mu.Unlock()
	if !ok || a.ProxyID == "" {
		t.Fatal("lease path should commit exactly one sticky")
	}
	if n != 1 {
		t.Fatalf("expected one assign entry for cross-acc, got %d", n)
	}
	if got := r.freshBinds(); got < 1 {
		t.Fatalf("freshBind=%d want >=1 from lease path", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type closeTracker struct {
	io.Reader
	closed *atomic.Int32
	once   sync.Once
}

func (c *closeTracker) Close() error {
	c.once.Do(func() { c.closed.Add(1) })
	return nil
}

func containsAny(s string, parts ...string) bool {
	for _, p := range parts {
		if p != "" && strings.Contains(s, p) {
			return true
		}
	}
	return false
}
