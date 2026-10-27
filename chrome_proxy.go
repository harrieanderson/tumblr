package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const chromeProxySessionMarker = "chrome-proxy-session.json"

// Loopback must bypass so CDP / remote-debugging stay local. The auth bridge
// itself listens on 127.0.0.1, so Chrome talks to loopback without credentials.
const chromeProxyBypassList = "<-loopback>;localhost;127.0.0.1"

// chromeProxyLaunch is the sticky proxy wiring for one Chrome process.
type chromeProxyLaunch struct {
	ProxyID  string
	Server   string // what Chrome gets: http://127.0.0.1:<bridge> (never upstream creds)
	ExtDir   string // unused when local bridge is active; kept for tests/compat
	LogLabel string // redacted upstream label for logs
	stop     func() // closes the local auth bridge
}

var (
	liveChromeProxyStopMu sync.Mutex
	liveChromeProxyStop   func()
)

func setLiveChromeProxyStop(stop func()) {
	liveChromeProxyStopMu.Lock()
	defer liveChromeProxyStopMu.Unlock()
	if liveChromeProxyStop != nil {
		liveChromeProxyStop()
	}
	liveChromeProxyStop = stop
}

func stopLiveChromeProxy() {
	setLiveChromeProxyStop(nil)
}

// prepareChromeProxyLaunch binds the account's sticky proxy and starts a local
// auth bridge so Watch/login Chrome uses the same exit IP as API traffic without
// Chrome's username/password dialog (creds never go on argv).
func prepareChromeProxyLaunch(ctx context.Context, accountID, profileDir string) (*chromeProxyLaunch, error) {
	accountID = accountSlug(accountID)
	if accountID == "" || strings.TrimSpace(profileDir) == "" {
		return nil, nil
	}
	r := proxyPool()
	if !r.enabled() {
		return nil, nil
	}
	u, proxyID, err := r.ensureStickyProxy(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if u == nil || proxyID == "" {
		return nil, fmt.Errorf("sticky proxy required for Chrome (%s) — refusing home IP", accountID)
	}

	bridgeAddr, stop, err := startChromeAuthBridge(u)
	if err != nil {
		return nil, fmt.Errorf("chrome auth bridge: %w", err)
	}
	out := &chromeProxyLaunch{
		ProxyID:  proxyID,
		Server:   "http://" + bridgeAddr,
		LogLabel: redactProxyURL(u.String()),
		stop:     stop,
	}

	// Optional legacy extension material (not loaded when bridge is used).
	user := ""
	pass := ""
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}
	if user != "" || pass != "" {
		extDir := filepath.Join(profileDir, "proxy-auth-ext")
		if err := writeChromeProxyAuthExtension(extDir, user, pass); err == nil {
			out.ExtDir = extDir
		}
	}
	return out, nil
}

// chromeProxyServerArg returns scheme://host:port with credentials stripped so
// secrets never appear on the Chrome command line.
func chromeProxyServerArg(u *url.URL) string {
	if u == nil {
		return ""
	}
	scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
	if scheme == "" {
		scheme = "http"
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		switch scheme {
		case "https":
			port = "443"
		default:
			port = "80"
		}
	}
	if host == "" {
		return ""
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}

func writeChromeProxyAuthExtension(dir, username, password string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	manifest := `{
  "manifest_version": 3,
  "name": "Tumblr Bot Proxy Auth",
  "version": "1.0.0",
  "permissions": ["webRequest", "webRequestAuthProvider"],
  "host_permissions": ["<all_urls>"],
  "background": { "service_worker": "background.js" }
}
`
	userJSON, err := json.Marshal(username)
	if err != nil {
		return err
	}
	passJSON, err := json.Marshal(password)
	if err != nil {
		return err
	}
	background := fmt.Sprintf(`chrome.webRequest.onAuthRequired.addListener(
  (details, callback) => {
    callback({ authCredentials: { username: %s, password: %s } });
  },
  { urls: ["<all_urls>"] },
  ["asyncBlocking"]
);
`, userJSON, passJSON)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "background.js"), []byte(background), 0o600)
}

// mergeChromeProxyArgs points Chrome at the local auth bridge. Extensions are
// not required (and stay disabled) — the bridge injects Proxy-Authorization.
func mergeChromeProxyArgs(args []string, launch *chromeProxyLaunch) []string {
	if launch == nil || launch.Server == "" {
		return args
	}
	return append(args,
		"--proxy-server="+launch.Server,
		"--proxy-bypass-list="+chromeProxyBypassList,
	)
}

// chromeLaunchUsesProxy reports whether the live bot Chrome for accountID was
// started with a sticky proxy. Used by Watch to decide whether to relaunch.
func chromeLaunchUsesProxy(accountID, liveProxyID string) bool {
	if !proxyPool().enabled() {
		return true // nothing to enforce
	}
	accountID = accountSlug(accountID)
	if accountID == "" {
		return false
	}
	return accountSlug(liveChromeAccount) == accountID && strings.TrimSpace(liveProxyID) != ""
}

// requireChromeProxyLaunch fails closed when the pool is on but Chrome would
// otherwise open on the machine's real IP.
func requireChromeProxyLaunch(launch *chromeProxyLaunch) error {
	if !proxyPool().enabled() {
		return nil
	}
	if launch == nil || strings.TrimSpace(launch.Server) == "" || strings.TrimSpace(launch.ProxyID) == "" {
		return fmt.Errorf("proxies enabled — refusing to launch Chrome without sticky proxy (home IP risk)")
	}
	return nil
}

func chromeProxySessionPath(accountID string) string {
	accountID = accountSlug(accountID)
	if accountID == "" {
		return ""
	}
	return filepath.Join(profilesDataDir, accountID, chromeProxySessionMarker)
}

// chromeProxySessionBound is true after this account completed a Tumblr login
// while Chrome was on the sticky proxy. Until then Active Sessions may still
// show a prior home-IP login.
func chromeProxySessionBound(accountID string) bool {
	p := chromeProxySessionPath(accountID)
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}

func markChromeProxySessionBound(accountID, proxyID string) {
	p := chromeProxySessionPath(accountID)
	if p == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	payload, _ := json.Marshal(map[string]string{
		"proxyId":   strings.TrimSpace(proxyID),
		"boundAt":   time.Now().Format(time.RFC3339),
		"accountId": accountSlug(accountID),
	})
	_ = os.WriteFile(p, payload, 0o600)
}

// chromeWebRTCProxyArgs stops WebRTC/STUN from leaking the real home IP while
// HTTP(S) already goes through the sticky proxy.
func chromeWebRTCProxyArgs() []string {
	return []string{
		"--force-webrtc-ip-handling-policy=disable_non_proxied_udp",
		"--webrtc-ip-handling-policy=disable_non_proxied_udp",
	}
}

// startChromeAuthBridge listens on 127.0.0.1 and forwards CONNECT/HTTP through
// upstream, adding Proxy-Authorization so Chrome never sees a credential prompt.
func startChromeAuthBridge(upstream *url.URL) (addr string, stop func(), err error) {
	if upstream == nil || upstream.Host == "" {
		return "", nil, fmt.Errorf("missing upstream proxy")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					return
				}
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer c.Close()
				_ = handleChromeAuthBridgeConn(c, upstream)
			}(conn)
		}
	}()
	stop = func() {
		cancel()
		_ = ln.Close()
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
	return ln.Addr().String(), stop, nil
}

func proxyAuthorizationHeader(u *url.URL) string {
	if u == nil || u.User == nil {
		return ""
	}
	user := u.User.Username()
	pass, _ := u.User.Password()
	if user == "" && pass == "" {
		return ""
	}
	token := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
	return "Basic " + token
}

func handleChromeAuthBridgeConn(client net.Conn, upstream *url.URL) error {
	_ = client.SetDeadline(time.Now().Add(60 * time.Second))
	br := bufio.NewReader(client)
	req, err := http.ReadRequest(br)
	if err != nil {
		return err
	}

	upHost := upstream.Host
	if !strings.Contains(upHost, ":") {
		upHost = net.JoinHostPort(upHost, "80")
	}
	upConn, err := net.DialTimeout("tcp", upHost, 20*time.Second)
	if err != nil {
		_, _ = io.WriteString(client, "HTTP/1.1 502 Bad Gateway\r\n\r\n")
		return err
	}
	defer upConn.Close()
	_ = upConn.SetDeadline(time.Now().Add(60 * time.Second))

	auth := proxyAuthorizationHeader(upstream)

	if req.Method == http.MethodConnect {
		// Client -> bridge CONNECT host:443 -> upstream proxy CONNECT with auth.
		var b strings.Builder
		fmt.Fprintf(&b, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n", req.RequestURI, req.RequestURI)
		if auth != "" {
			fmt.Fprintf(&b, "Proxy-Authorization: %s\r\n", auth)
		}
		b.WriteString("Proxy-Connection: Keep-Alive\r\n\r\n")
		if _, err := io.WriteString(upConn, b.String()); err != nil {
			return err
		}
		upBr := bufio.NewReader(upConn)
		resp, err := http.ReadResponse(upBr, req)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Write(client)
			return fmt.Errorf("upstream CONNECT %s", resp.Status)
		}
		if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return err
		}
		_ = client.SetDeadline(time.Time{})
		_ = upConn.SetDeadline(time.Time{})
		return tunnel(client, upConn, br, upBr)
	}

	// Absolute-form HTTP request via upstream.
	req.RequestURI = ""
	if auth != "" {
		req.Header.Set("Proxy-Authorization", auth)
	}
	if err := req.WriteProxy(upConn); err != nil {
		return err
	}
	upBr := bufio.NewReader(upConn)
	resp, err := http.ReadResponse(upBr, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return resp.Write(client)
}

func tunnel(a, b net.Conn, aBuf, bBuf *bufio.Reader) error {
	errc := make(chan error, 2)
	go func() {
		_, err := io.Copy(b, aBuf)
		errc <- err
	}()
	go func() {
		_, err := io.Copy(a, bBuf)
		errc <- err
	}()
	<-errc
	return nil
}
