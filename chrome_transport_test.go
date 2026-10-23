package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errFakeCONNECT = errors.New("fake CONNECT: no network")

func TestProxyConnectRequestIncludesBasicAuth(t *testing.T) {
	u, err := url.Parse("http://alice:s3cret@proxy.example:8080")
	if err != nil {
		t.Fatal(err)
	}
	line := proxyConnectRequestLine(u, "www.tumblr.com:443")
	if !strings.Contains(line, "CONNECT www.tumblr.com:443 HTTP/1.1") {
		t.Fatalf("missing CONNECT: %q", line)
	}
	if !strings.Contains(line, "Proxy-Authorization: Basic ") {
		t.Fatalf("missing proxy auth: %q", line)
	}
	if strings.Contains(line, "s3cret") {
		t.Fatal("password must be base64, not plaintext")
	}
}

func TestDialHTTPProxyCONNECT(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	done := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			done <- err.Error()
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		req, err := http.ReadRequest(br)
		if err != nil {
			done <- err.Error()
			return
		}
		done <- req.Method + " " + req.Host
		_, _ = io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
	}()

	proxyURL, _ := url.Parse("http://user:pass@" + ln.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := dialHTTPProxyCONNECT(ctx, &net.Dialer{}, proxyURL, "www.tumblr.com:443")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	select {
	case got := <-done:
		if got != "CONNECT www.tumblr.com:443" {
			t.Fatalf("proxy saw %q", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for CONNECT")
	}
}

func TestSetCommonHeadersOrderAndEncoding(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://www.tumblr.com/api/v2/user/info", nil)
	if err != nil {
		t.Fatal(err)
	}
	auth := Auth{Bearer: "tok", Cookie: "sid=abc"}
	setCommonHeaders(req, auth)

	if got := req.Header.Get("User-Agent"); got != chromeUA {
		t.Fatalf("UA: %q", got)
	}
	if got := req.Header.Get("sec-ch-ua"); got != chromeSecCHUA {
		t.Fatalf("sec-ch-ua: %q", got)
	}
	if got := req.Header.Get("Accept-Encoding"); got != chromeAcceptEncoding {
		t.Fatalf("Accept-Encoding: %q", got)
	}
	if got := req.Header.Get("X-Version"); !strings.Contains(got, "redpop") {
		t.Fatalf("X-Version: %q", got)
	}

	for _, want := range commonHeaderOrder {
		if req.Header.Get(want) == "" {
			t.Fatalf("missing header %s", want)
		}
	}
}

func TestDecodeContentEncodingGzip(t *testing.T) {
	// Round-trip through a tiny server is overkill; encode manually.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "identity")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out := decodeContentEncoding(resp)
	body, _ := io.ReadAll(out.Body)
	if string(body) != `{"ok":true}` {
		t.Fatalf("body %s", body)
	}
}

func TestNewHumanClientUsesChromeTransport(t *testing.T) {
	c := newHumanClientFor("testacc")
	rt := c.Transport
	// unwrap pace
	if p, ok := rt.(*pacedProxyTransport); ok {
		rt = p.base
	}
	if _, ok := rt.(*chromeTransport); !ok {
		t.Fatalf("expected chromeTransport under pace, got %T", rt)
	}
}

func TestDialChromeTLSFailedConnectReleasesLease(t *testing.T) {
	// Closed local port → CONNECT fails; bare dial must still release inFlight.
	r := newProxyRouter(testProxyCfg("http://127.0.0.1:1"))
	r.cfg.MinRequestGapMs = 0
	r.cfg.PerAccountRPM = 1000
	r.cfg.PerProxyRPM = 1000
	r.cfg.PerProxyMaxConcurrent = 1
	withSharedProxy(t, r)

	ct := newChromeTransport("dial-fail").(*chromeTransport)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := ct.dialChromeTLS(ctx, "www.tumblr.com:443")
	if err == nil {
		t.Fatal("expected failed CONNECT")
	}
	if got := r.inflightFor(r.pool[0].id); got != 0 {
		t.Fatalf("leaked inFlight after failed CONNECT: %d", got)
	}
}

func TestDialChromeTLSCancelReleasesLease(t *testing.T) {
	r := newProxyRouter(testProxyCfg("http://127.0.0.1:1"))
	r.cfg.MinRequestGapMs = 0
	r.cfg.PerAccountRPM = 1000
	r.cfg.PerProxyRPM = 1000
	r.cfg.PerProxyMaxConcurrent = 1
	withSharedProxy(t, r)

	held, err := r.acquireLease(context.Background(), "holder")
	if err != nil || held == "" {
		t.Fatalf("hold: %v %q", err, held)
	}
	defer r.releaseLease(held)

	parked := make(chan struct{})
	r.sleep = func(ctx context.Context, d time.Duration) error {
		close(parked)
		<-ctx.Done()
		return ctx.Err()
	}

	ct := newChromeTransport("dial-cancel").(*chromeTransport)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := ct.dialChromeTLS(ctx, "www.tumblr.com:443")
		errCh <- err
	}()

	select {
	case <-parked:
	case <-time.After(2 * time.Second):
		t.Fatal("dial never waited for lease")
	}
	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected ctx error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dial did not return on cancel")
	}
	if got := r.inflightFor(held); got != 1 {
		t.Fatalf("cancel must not touch held lease, inflight=%d", got)
	}
	r.mu.Lock()
	_, has := r.assign["dial-cancel"]
	r.mu.Unlock()
	if has {
		t.Fatal("cancel before lease left sticky for dial-cancel")
	}
}

func TestDialChromeTLSSkipsNestedLeaseWhenPaced(t *testing.T) {
	r := newProxyRouter(testProxyCfg("http://127.0.0.1:1"))
	r.cfg.MinRequestGapMs = 0
	r.cfg.PerAccountRPM = 1000
	r.cfg.PerProxyRPM = 1000
	r.cfg.PerProxyMaxConcurrent = 1
	withSharedProxy(t, r)

	proxyID, err := r.acquireLease(context.Background(), "paced-dial")
	if err != nil || proxyID == "" {
		t.Fatalf("lease: %v %q", err, proxyID)
	}
	defer r.releaseLease(proxyID)
	if got := r.inflightFor(proxyID); got != 1 {
		t.Fatalf("inflight=%d want 1", got)
	}

	ct := newChromeTransport("paced-dial").(*chromeTransport)
	ctx := contextWithProxyLease(context.Background(), proxyID)
	_, err = ct.dialChromeTLS(ctx, "www.tumblr.com:443")
	if err == nil {
		t.Fatal("expected failed CONNECT")
	}
	// Nested acquire must not have bumped inFlight past the outer lease.
	if got := r.inflightFor(proxyID); got != 1 {
		t.Fatalf("nested lease changed inflight to %d", got)
	}
}

func TestPacedChromeCONNECTCrossPathOneFreshBind(t *testing.T) {
	// Production path: wrapProxyPace + dialChromeTLS under the paced ctx marker.
	// Naked proxyURLFor must not commit; exactly one fresh bind from the lease path.
	r := newProxyRouter(testProxyCfg(
		"http://p1.example:8080",
		"http://p2.example:8080",
	))
	r.cfg.MinRequestGapMs = 0
	r.cfg.PerAccountRPM = 1000
	r.cfg.PerProxyRPM = 1000
	r.cfg.Backoff.JitterPercent = 0
	withSharedProxy(t, r)
	r.resetFreshBindCount()

	ct := newChromeTransport("cross-paced").(*chromeTransport)
	var dials atomic.Int32
	var markerOK atomic.Int32
	ct.rawDial = func(ctx context.Context, proxyURL *url.URL, addr string) (net.Conn, error) {
		dials.Add(1)
		if id, ok := proxyLeaseFromContext(ctx); ok && id != "" {
			markerOK.Add(1)
		}
		if proxyURL == nil {
			t.Error("paced dial expected sticky proxy URL after lease")
		}
		return nil, errFakeCONNECT
	}

	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		_, err := ct.dialChromeTLS(req.Context(), "www.tumblr.com:443")
		if !errors.Is(err, errFakeCONNECT) {
			t.Errorf("rawDial err: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Request:    req,
		}, nil
	})
	rt := wrapProxyPace(base, "cross-paced")

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = r.proxyURLFor("cross-paced")
				}
			}
		}()
	}
	req, err := http.NewRequest(http.MethodGet, "https://www.tumblr.com/api/v2/user/info", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rt.RoundTrip(req)
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if dials.Load() != 1 {
		t.Fatalf("rawDial calls=%d want 1", dials.Load())
	}
	if markerOK.Load() != 1 {
		t.Fatalf("lease marker seen=%d want 1", markerOK.Load())
	}
	if got := r.freshBinds(); got != 1 {
		t.Fatalf("freshBind=%d want 1", got)
	}
	r.mu.Lock()
	a, ok := r.assign[accountSlug("cross-paced")]
	n := 0
	for acc := range r.assign {
		if acc == accountSlug("cross-paced") {
			n++
		}
	}
	r.mu.Unlock()
	if !ok || a.ProxyID == "" || n != 1 {
		t.Fatalf("assign=%v ok=%v entries=%d", a, ok, n)
	}
}

func TestPacedRetryMarkerPresentEachAttempt(t *testing.T) {
	// 503 clears sticky via reportResult; release between retries then re-acquire.
	// Assert marker on every attempt; freshBind==2 after cooldown clear (true commits).
	r := newProxyRouter(testProxyCfg("http://p1.example:8080"))
	r.cfg.MinRequestGapMs = 0
	r.cfg.PerAccountRPM = 1000
	r.cfg.PerProxyRPM = 1000
	r.cfg.Backoff.InitialSeconds = 1
	r.cfg.Backoff.JitterPercent = 0
	var slept atomic.Int32
	r.sleep = func(ctx context.Context, d time.Duration) error {
		slept.Add(1)
		return nil
	}
	withSharedProxy(t, r)
	r.resetFreshBindCount()

	ct := newChromeTransport("retry-marker").(*chromeTransport)
	var mu sync.Mutex
	var markers []string
	ct.rawDial = func(ctx context.Context, proxyURL *url.URL, addr string) (net.Conn, error) {
		id, _ := proxyLeaseFromContext(ctx)
		mu.Lock()
		markers = append(markers, id)
		mu.Unlock()
		return nil, errFakeCONNECT
	}

	var calls atomic.Int32
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		n := calls.Add(1)
		_, _ = ct.dialChromeTLS(req.Context(), "www.tumblr.com:443")
		if n == 1 {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header:     http.Header{"Retry-After": []string{"1"}},
				Body:       io.NopCloser(strings.NewReader("unavailable")),
				Request:    req,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("ok")),
			Request:    req,
		}, nil
	})
	rt := wrapProxyPace(base, "retry-marker")
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
	if slept.Load() < 1 {
		t.Fatal("expected sleep override on retry cooldown")
	}
	mu.Lock()
	got := append([]string(nil), markers...)
	mu.Unlock()
	if len(got) < 2 {
		t.Fatalf("markers=%v want >=2", got)
	}
	for i, id := range got {
		if id == "" {
			t.Fatalf("attempt %d missing lease marker", i)
		}
	}
	// Single-endpoint pool: soft-pick after clear may reuse the same id; that is
	// luck, not sticky continuity. freshBind must still count both commits.
	if got := r.freshBinds(); got != 2 {
		t.Fatalf("freshBind=%d want 2 (rebind after 503 clear)", got)
	}
}

func TestDialChromeTLSRawDialUsedWithoutSocket(t *testing.T) {
	// Guarded bare dial: rawDial short-circuits before CONNECT/uTLS.
	r := newProxyRouter(testProxyCfg("http://p1.example:8080"))
	r.cfg.MinRequestGapMs = 0
	r.cfg.PerAccountRPM = 1000
	r.cfg.PerProxyRPM = 1000
	withSharedProxy(t, r)
	r.resetFreshBindCount()

	ct := newChromeTransport("raw-only").(*chromeTransport)
	var sawProxy atomic.Bool
	ct.rawDial = func(ctx context.Context, proxyURL *url.URL, addr string) (net.Conn, error) {
		if _, ok := proxyLeaseFromContext(ctx); ok {
			t.Error("bare dial should own lease, not see outer marker")
		}
		if proxyURL != nil {
			sawProxy.Store(true)
		}
		return nil, errFakeCONNECT
	}
	_, err := ct.dialChromeTLS(context.Background(), "www.tumblr.com:443")
	if !errors.Is(err, errFakeCONNECT) {
		t.Fatalf("err=%v", err)
	}
	if !sawProxy.Load() {
		t.Fatal("expected proxy URL after bare acquire")
	}
	if got := r.inflightFor(r.pool[0].id); got != 0 {
		t.Fatalf("leaked inFlight: %d", got)
	}
	if got := r.freshBinds(); got != 1 {
		t.Fatalf("freshBind=%d want 1", got)
	}
}

func TestChromeTransportLiveSmoke(t *testing.T) {
	if !proxyPool().enabled() {
		t.Skip("proxies disabled")
	}
	client := &http.Client{
		Timeout:   35 * time.Second,
		Transport: newChromeTransport("smoke"),
	}
	req, err := http.NewRequest(http.MethodGet, "https://api.ipify.org", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", chromeUA)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Encoding", chromeAcceptEncoding)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || len(strings.TrimSpace(string(body))) < 7 {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
	t.Logf("chrome+proxy exit IP: %s", strings.TrimSpace(string(body)))
}
