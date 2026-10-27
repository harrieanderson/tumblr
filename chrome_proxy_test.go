package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChromeProxyServerArgOmitsCredentials(t *testing.T) {
	u, err := url.Parse("http://alice:s3cret@proxy.example:5262")
	if err != nil {
		t.Fatal(err)
	}
	got := chromeProxyServerArg(u)
	if got != "http://proxy.example:5262" {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "alice") || strings.Contains(got, "s3cret") {
		t.Fatal("credentials leaked into --proxy-server value")
	}
}

func TestChromeProxyServerArgBracketsIPv6(t *testing.T) {
	u := &url.URL{Scheme: "http", Host: "[2001:db8::10]:5262"}
	if got := chromeProxyServerArg(u); got != "http://[2001:db8::10]:5262" {
		t.Fatalf("got %q", got)
	}
}

func TestWriteChromeProxyAuthExtension(t *testing.T) {
	dir := t.TempDir()
	if err := writeChromeProxyAuthExtension(dir, `user"\`, "páss"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "background.js"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, "asyncBlocking") {
		t.Fatal("expected asyncBlocking auth listener")
	}
	if !strings.Contains(body, "authCredentials") || !strings.Contains(body, "user") {
		t.Fatalf("username/credentials missing from extension: %s", body)
	}
	man, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(man), "webRequestAuthProvider") {
		t.Fatal("manifest missing webRequestAuthProvider")
	}
}

func TestMergeChromeProxyArgsKeepsDisableExtensions(t *testing.T) {
	base := []string{"--disable-extensions", "--no-pings"}
	launch := &chromeProxyLaunch{
		Server: "http://127.0.0.1:12345",
		ExtDir: `C:\tmp\proxy-auth-ext`,
	}
	got := mergeChromeProxyArgs(base, launch)
	if !hasExactArg(got, "--disable-extensions") {
		t.Fatalf("bridge mode should keep --disable-extensions: %#v", got)
	}
	if !hasExactArg(got, "--proxy-server=http://127.0.0.1:12345") {
		t.Fatalf("missing proxy-server: %#v", got)
	}
	if !hasExactArg(got, "--proxy-bypass-list="+chromeProxyBypassList) {
		t.Fatalf("missing bypass list: %#v", got)
	}
	if hasExactArg(got, "--load-extension="+launch.ExtDir) {
		t.Fatalf("must not load extension when using local bridge: %#v", got)
	}
}

func TestChromeLaunchUsesProxy(t *testing.T) {
	oldAccount := liveChromeAccount
	defer func() { liveChromeAccount = oldAccount }()
	r := newProxyRouter(defaultProxyConfig())
	withSharedProxy(t, r)
	if !chromeLaunchUsesProxy("any", "") {
		t.Fatal("proxies disabled should not force relaunch")
	}

	r2 := newProxyRouter(testProxyCfg("http://user:pass@p.example:8080"))
	withSharedProxy(t, r2)
	liveChromeAccount = "acc"
	if chromeLaunchUsesProxy("acc", "") {
		t.Fatal("enabled pool + empty live proxy id should force relaunch")
	}
	if !chromeLaunchUsesProxy("acc", "p.example:8080") {
		t.Fatal("live proxy id should count as using proxy")
	}
	if chromeLaunchUsesProxy("other", "p.example:8080") {
		t.Fatal("proxy state for another account must force relaunch")
	}
}

func TestChromeProxySessionBoundMarker(t *testing.T) {
	id := "proxy-bound-test"
	path := chromeProxySessionPath(id)
	_ = os.Remove(path)
	if chromeProxySessionBound(id) {
		t.Fatal("expected unbound")
	}
	markChromeProxySessionBound(id, "ash-01@example")
	defer os.Remove(path)
	if !chromeProxySessionBound(id) {
		t.Fatal("expected bound after mark")
	}
}

func TestRequireChromeProxyLaunchFailClosed(t *testing.T) {
	withSharedProxy(t, newProxyRouter(defaultProxyConfig()))
	if err := requireChromeProxyLaunch(nil); err != nil {
		t.Fatalf("disabled pool should allow nil launch: %v", err)
	}

	r := newProxyRouter(testProxyCfg("http://user:pass@p.example:8080"))
	withSharedProxy(t, r)
	if err := requireChromeProxyLaunch(nil); err == nil {
		t.Fatal("enabled pool must refuse Chrome without proxy")
	}
	if err := requireChromeProxyLaunch(&chromeProxyLaunch{ProxyID: "p", Server: "http://127.0.0.1:9"}); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareChromeProxyLaunchStartsLocalBridge(t *testing.T) {
	r := newProxyRouter(testProxyCfg("http://alice:secret@proxy.example:5262"))
	r.cfg.MinRequestGapMs = 0
	r.cfg.PerAccountRPM = 1000
	r.cfg.PerProxyRPM = 1000
	withSharedProxy(t, r)

	profile := t.TempDir()
	launch, err := prepareChromeProxyLaunch(context.Background(), "watch-prep", profile)
	if err != nil {
		t.Fatal(err)
	}
	if launch == nil {
		t.Fatal("expected launch")
	}
	defer launch.stop()
	if !strings.HasPrefix(launch.Server, "http://127.0.0.1:") {
		t.Fatalf("expected loopback bridge, got %#v", launch)
	}
	if strings.Contains(launch.Server, "alice") || strings.Contains(launch.LogLabel, "secret") {
		t.Fatalf("credential leak: %#v", launch)
	}
	if launch.ExtDir == "" {
		t.Fatal("expected auth extension dir written as fallback material")
	}
	if _, err := os.Stat(filepath.Join(launch.ExtDir, "background.js")); err != nil {
		t.Fatal(err)
	}
	if got := r.inflightFor(launch.ProxyID); got != 0 {
		t.Fatalf("inFlight=%d after prepare", got)
	}
}

func TestChromeAuthBridgeInjectsProxyAuthorization(t *testing.T) {
	var sawAuth string
	var sawConnectHost string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Proxy-Authorization")
		if r.Method == http.MethodConnect {
			sawConnectHost = r.Host
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("no hijack")
			}
			conn, bufrw, err := hj.Hijack()
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_, _ = io.WriteString(bufrw, "HTTP/1.1 200 Connection Established\r\n\r\n")
			_ = bufrw.Flush()
			return
		}
		http.Error(w, "unexpected", 500)
	}))
	defer up.Close()

	u, err := url.Parse(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("alice", "secret")

	addr, stop, err := startChromeAuthBridge(u)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = fmt.Fprintf(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%s", resp.Status)
	}
	if sawAuth == "" || !strings.HasPrefix(sawAuth, "Basic ") {
		t.Fatalf("upstream missing Proxy-Authorization: %q", sawAuth)
	}
	if sawConnectHost != "example.com:443" {
		t.Fatalf("CONNECT host=%q", sawConnectHost)
	}
}

func hasExactArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}
