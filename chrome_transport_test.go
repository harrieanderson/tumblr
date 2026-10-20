package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

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
