package main

import (
	"bufio"
	"compress/zlib"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/gzip"
	"github.com/klauspost/compress/zstd"
	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

// chromeTransport speaks HTTPS with a Chrome TLS fingerprint (uTLS) and
// HTTP/2, optionally tunneling via the sticky HTTP proxy for accountID.
type chromeTransport struct {
	accountID string
	dialer    *net.Dialer
	// rawDial optionally replaces CONNECT/TCP dial (tests only). Nil keeps
	// production dialHTTPProxyCONNECT / dialer.DialContext. Receives ctx so
	// callers can inspect proxyLeaseFromContext without opening sockets.
	rawDial func(ctx context.Context, proxyURL *url.URL, addr string) (net.Conn, error)

	initOnce sync.Once
	h2       *http2.Transport
	h1       *http.Transport
}

// newChromeTransport builds the uTLS RoundTripper. Production callers must wrap
// with wrapProxyPace so acquireLease commits sticky before CONNECT. Bare use
// is allowed only via the guarded dial path (dialChromeTLS acquires+releases
// when the request ctx has no outer paced lease).
func newChromeTransport(accountID string) http.RoundTripper {
	return &chromeTransport{
		accountID: accountSlug(accountID),
		dialer: &net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		},
	}
}

func (t *chromeTransport) ensure() {
	t.initOnce.Do(func() {
		t.h2 = &http2.Transport{
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				return t.dialChromeTLS(ctx, addr)
			},
		}
		t.h1 = &http.Transport{
			Proxy:                 nil, // dialChromeTLS handles proxy CONNECT
			DialTLSContext:        t.dialTLSContextH1,
			ForceAttemptHTTP2:     false,
			MaxIdleConns:          32,
			MaxIdleConnsPerHost:   8,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   15 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			DisableCompression:    true, // we advertise encodings and decode ourselves
		}
	})
}

// dialTLSContextH1 matches net/http.Transport.DialTLSContext.
func (t *chromeTransport) dialTLSContextH1(ctx context.Context, network, addr string) (net.Conn, error) {
	return t.dialChromeTLS(ctx, addr)
}

func (t *chromeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.ensure()
	if req.URL == nil || (req.URL.Scheme != "https" && req.URL.Scheme != "http") {
		return nil, fmt.Errorf("chrome transport: unsupported URL")
	}
	// Plain http (rare for Tumblr API) — fall back to std dial without uTLS.
	if req.URL.Scheme == "http" {
		plain := &http.Transport{
			Proxy:               t.proxyFunc(),
			DialContext:         t.dialer.DialContext,
			DisableCompression:  true,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 15 * time.Second,
		}
		resp, err := plain.RoundTrip(req)
		return decodeContentEncoding(resp), err
	}

	resp, err := t.h2.RoundTrip(req)
	if err != nil && isHTTP2NegotiationError(err) {
		resp, err = t.h1.RoundTrip(req)
	}
	return decodeContentEncoding(resp), err
}

func isHTTP2NegotiationError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "http2") &&
		(strings.Contains(s, "alpn") ||
			strings.Contains(s, "protocol") ||
			strings.Contains(s, "no cached conn") ||
			strings.Contains(s, "server pref"))
}

func (t *chromeTransport) proxyFunc() func(*http.Request) (*url.URL, error) {
	id := t.accountID
	return func(req *http.Request) (*url.URL, error) {
		r := proxyPool()
		if r.enabled() {
			return r.proxyURLFor(id)
		}
		return http.ProxyFromEnvironment(req)
	}
}

func (t *chromeTransport) resolveProxy(ctx context.Context) (*url.URL, error) {
	r := proxyPool()
	if r.enabled() {
		// Sticky-read-only: never soft-bind here.
		return r.proxyURLFor(t.accountID)
	}
	// Match prior behaviour when pool is off.
	return http.ProxyFromEnvironment(&http.Request{URL: &url.URL{Scheme: "https", Host: "www.tumblr.com"}})
}

func (t *chromeTransport) dialChromeTLS(ctx context.Context, addr string) (net.Conn, error) {
	r := proxyPool()
	ownLease := ""
	if r.enabled() {
		if _, ok := proxyLeaseFromContext(ctx); !ok {
			// Bare transport / no outer paced lease — acquire for this dial only.
			id, err := r.acquireLease(ctx, t.accountID)
			if err != nil {
				return nil, err
			}
			ownLease = id
			defer r.releaseLease(ownLease)
		}
	}

	proxyURL, err := t.resolveProxy(ctx)
	if err != nil {
		return nil, err
	}
	if r.enabled() && proxyURL == nil {
		return nil, fmt.Errorf("chrome transport: sticky proxy required for %s — refusing direct/home IP to %s", t.accountID, addr)
	}

	var raw net.Conn
	if t.rawDial != nil {
		raw, err = t.rawDial(ctx, proxyURL, addr)
	} else if proxyURL != nil {
		switch strings.ToLower(proxyURL.Scheme) {
		case "http", "https":
			raw, err = dialHTTPProxyCONNECT(ctx, t.dialer, proxyURL, addr)
		default:
			return nil, fmt.Errorf("chrome transport: unsupported proxy scheme %q", proxyURL.Scheme)
		}
	} else {
		raw, err = t.dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, err
	}

	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	cfg := &utls.Config{
		ServerName:         host,
		NextProtos:         []string{"h2", "http/1.1"},
		InsecureSkipVerify: false,
		MinVersion:         utls.VersionTLS12,
	}
	// HelloChrome_Auto tracks the latest uTLS Chrome ClientHello. Keep the
	// claimed HTTP major (fingerprint()) near the installed browser so Auto
	// and UA/sec-ch-ua stay aligned after Chrome updates. Full H2 SETTINGS /
	// WINDOW_UPDATE / priority mimicry is intentionally not done here.
	uconn := utls.UClient(raw, cfg, utls.HelloChrome_Auto)
	if err := uconn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return uconn, nil
}

// dialHTTPProxyCONNECT opens a TCP tunnel through an HTTP proxy to targetAddr (host:port).
func dialHTTPProxyCONNECT(ctx context.Context, d *net.Dialer, proxyURL *url.URL, targetAddr string) (net.Conn, error) {
	if d == nil {
		d = &net.Dialer{Timeout: 30 * time.Second}
	}
	proxyAddr := proxyURL.Host
	if !strings.Contains(proxyAddr, ":") {
		if proxyURL.Scheme == "https" {
			proxyAddr += ":443"
		} else {
			proxyAddr += ":80"
		}
	}
	conn, err := d.DialContext(ctx, "tcp", proxyAddr)
	if err != nil {
		return nil, err
	}

	var b strings.Builder
	b.WriteString("CONNECT ")
	b.WriteString(targetAddr)
	b.WriteString(" HTTP/1.1\r\nHost: ")
	b.WriteString(targetAddr)
	b.WriteString("\r\n")
	if proxyURL.User != nil {
		user := proxyURL.User.Username()
		pass, _ := proxyURL.User.Password()
		token := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
		b.WriteString("Proxy-Authorization: Basic ")
		b.WriteString(token)
		b.WriteString("\r\n")
	}
	b.WriteString("User-Agent: ")
	b.WriteString(fingerprint().UA)
	b.WriteString("\r\nProxy-Connection: Keep-Alive\r\n\r\n")

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if _, err := io.WriteString(conn, b.String()); err != nil {
		_ = conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_ = conn.Close()
		return nil, fmt.Errorf("proxy CONNECT %s: %s", redactProxyURL(proxyURL.String()), resp.Status)
	}
	// Buffered bytes (if any) must stay with the connection.
	if br.Buffered() > 0 {
		conn = &bufferedConn{Conn: conn, r: br}
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.r.Read(p)
}

func proxyConnectRequestLine(proxyURL *url.URL, targetAddr string) string {
	var b strings.Builder
	b.WriteString("CONNECT ")
	b.WriteString(targetAddr)
	b.WriteString(" HTTP/1.1\r\nHost: ")
	b.WriteString(targetAddr)
	b.WriteString("\r\n")
	if proxyURL != nil && proxyURL.User != nil {
		user := proxyURL.User.Username()
		pass, _ := proxyURL.User.Password()
		token := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
		b.WriteString("Proxy-Authorization: Basic ")
		b.WriteString(token)
		b.WriteString("\r\n")
	}
	b.WriteString("\r\n")
	return b.String()
}

func decodeContentEncoding(resp *http.Response) *http.Response {
	if resp == nil || resp.Body == nil {
		return resp
	}
	ce := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	if ce == "" || ce == "identity" {
		return resp
	}
	var (
		rc  io.ReadCloser
		err error
	)
	switch ce {
	case "gzip":
		rc, err = gzip.NewReader(resp.Body)
	case "deflate":
		rc, err = zlib.NewReader(resp.Body)
	case "br":
		rc = io.NopCloser(brotli.NewReader(resp.Body))
	case "zstd":
		var zr *zstd.Decoder
		zr, err = zstd.NewReader(resp.Body)
		if err == nil {
			rc = zr.IOReadCloser()
		}
	default:
		return resp
	}
	if err != nil {
		return resp
	}
	orig := resp.Body
	resp.Body = &chainCloser{ReadCloser: rc, also: orig}
	resp.Header.Del("Content-Encoding")
	resp.Header.Del("Content-Length")
	resp.ContentLength = -1
	resp.Uncompressed = true
	return resp
}

type chainCloser struct {
	io.ReadCloser
	also io.Closer
}

func (c *chainCloser) Close() error {
	err1 := c.ReadCloser.Close()
	err2 := c.also.Close()
	if err1 != nil {
		return err1
	}
	return err2
}
