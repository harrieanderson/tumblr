package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

const (
	chromeDataDir   = "chrome-data"
	debugPort       = "9333"
	credentialsFile = "config/credentials.json"
)

// liveDebugPort is the port of the Chrome we just launched. Empty means do not
// attach to whatever happens to be listening on 9333.
var liveDebugPort string

var fileChooserMu sync.Mutex
var lastFileChooserBackend int

// liveChromeCtx stays attached for the whole Chrome process. Cancelling a
// chromedp WithTargetID context closes that tab; if it is the last tab, Chrome
// exits. Cookie polls must not do that.
var (
	liveChromeCtx      context.Context
	liveChromeCancel   context.CancelFunc
	liveChromePort     string
	liveTargetID       target.ID
	liveChromeAccount  string
	liveChromePID      int
	liveChromeProxyID  string // sticky proxy id used for this Chrome process (Watch/login)
	// forceProxySessionBind requests one fresh Tumblr sign-in while Chrome is on
	// the sticky proxy so Active Sessions shows the proxy IP. Watch/Login set this;
	// batch -setup-profile does not (avoids captcha on every account).
	forceProxySessionBind bool
	chromeLoginMu      sync.Mutex
	skipChromeRecovery bool
	// headlessCookieOnly: recover sessions by scraping the Chrome profile
	// headlessly only — never open a visible login window (safe while gaming).
	headlessCookieOnly bool
	// quietChromeLogin: reach may open Chrome to mint a new cookie, but must
	// not steal the mouse or wait for you to finish captcha by hand.
	quietChromeLogin bool
	sessionDead      sync.Map
)

type Credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func loadCredentials() (Credentials, error) {
	if acc, ok := currentAccount(); ok {
		email := strings.TrimSpace(acc.Email)
		pass := strings.TrimSpace(acc.Password)
		if email != "" && pass != "" {
			return Credentials{Email: email, Password: pass}, nil
		}
	}
	raw, err := os.ReadFile(credentialsFile)
	if err != nil {
		return Credentials{}, err
	}
	var c Credentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return Credentials{}, err
	}
	c.Email = strings.TrimSpace(c.Email)
	c.Password = strings.TrimSpace(c.Password)
	if c.Email == "" || c.Password == "" {
		return Credentials{}, fmt.Errorf("email/password empty in %s", credentialsFile)
	}
	return c, nil
}

func loadCredentialsFor(auth *Auth) (Credentials, error) {
	if auth != nil {
		if id := accountIDFromAuth(*auth); id != "" {
			if acc, ok := findAccount(id); ok {
				email := strings.TrimSpace(acc.Email)
				pass := strings.TrimSpace(acc.Password)
				if email != "" && pass != "" {
					return Credentials{Email: email, Password: pass}, nil
				}
			}
		}
	}
	return loadCredentials()
}

func chromePath() (string, error) {
	var candidates []string
	if p, err := exec.LookPath("chrome.exe"); err == nil {
		candidates = append(candidates, p)
	}
	if p, err := exec.LookPath("chrome"); err == nil {
		candidates = append(candidates, p)
	}
	candidates = append(candidates,
		os.Getenv("PROGRAMFILES")+`\Google\Chrome\Application\chrome.exe`,
		os.Getenv("ProgramFiles(x86)")+`\Google\Chrome\Application\chrome.exe`,
		os.Getenv("LOCALAPPDATA")+`\Google\Chrome\Application\chrome.exe`,
		os.Getenv("PROGRAMFILES")+`\Google\Chrome Beta\Application\chrome.exe`,
		os.Getenv("LOCALAPPDATA")+`\Google\Chrome Beta\Application\chrome.exe`,
	)
	seen := map[string]bool{}
	for _, p := range candidates {
		if p == "" || strings.HasPrefix(p, `\`) || seen[p] {
			continue
		}
		seen[p] = true
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("chrome.exe not found — install Google Chrome from https://www.google.com/chrome/")
}

func profileDir() (string, error) {
	dir, err := filepath.Abs(chromeProfilePath())
	if err != nil {
		return "", err
	}
	return dir, os.MkdirAll(dir, 0755)
}

func debugEndpoint() string {
	port := strings.TrimSpace(liveDebugPort)
	if port == "" {
		port = currentDebugPort()
	}
	return "http://127.0.0.1:" + port
}

// portInUse is the quick probe used when picking a free port. A closed local
// port refuses straight away, so a short deadline is enough here.
func portInUse(port string) bool {
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// portAlive is the patient probe used to decide whether Chrome is still there.
// A Chrome showing a captcha can take over a second to accept a connection,
// and treating that as "closed" killed real login windows mid-captcha.
func portAlive(port string) bool {
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 3*time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func attachAccountDebugPort() bool {
	if strings.TrimSpace(liveDebugPort) != "" && portAlive(liveDebugPort) {
		return true
	}
	p := currentDebugPort()
	if p != "" && portAlive(p) {
		liveDebugPort = p
		return true
	}
	return false
}

func pickFreeDebugPort(preferred string) string {
	if preferred != "" && !portInUse(preferred) {
		return preferred
	}
	start := 9400
	if n, err := strconv.Atoi(preferred); err == nil && n > 0 {
		start = n + 80
	}
	for p := start; p < start+200; p++ {
		s := strconv.Itoa(p)
		if !portInUse(s) {
			return s
		}
	}
	return preferred
}

func stopChrome(cmd *exec.Cmd) {
	stopChromeOpts(cmd, true)
}

// stopChromeOpts closes bot Chrome. When polite is false (batch setup), skip the
// CDP browser.Close wait — that is what left Tumblr sitting on the finished page.
func stopChromeOpts(cmd *exec.Cmd, polite bool) {
	port := liveDebugPort
	pid := liveChromePID
	if cmd != nil && cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	if polite {
		closeBotChrome()
	} else {
		detachLiveChrome()
	}
	liveDebugPort = ""
	liveChromeAccount = ""
	liveChromePID = 0
	liveChromeProxyID = ""
	if runtime.GOOS == "windows" {
		if pid > 0 {
			_ = exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
		}
		killListenersOnPort(port)
		return
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
}

// ensureFreshChromeForAccount closes any leftover bot Chrome for this profile
// (stale debug port / prior recovery) so the next login starts clean — same as reach.
func ensureFreshChromeForAccount(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	if liveChromeAccount == id || liveChromeAccount == "" {
		closeBotChrome()
		liveDebugPort = ""
		liveChromeAccount = ""
		liveChromePID = 0
		liveChromeProxyID = ""
	}
	preferred := debugPortForAccount(id)
	killListenersOnPort(preferred)
	if dir, err := chromeDirForAccount(id); err == nil {
		killChromeWithUserDataDir(dir)
	}
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if preferred == "" || !portInUse(preferred) {
			return
		}
		killListenersOnPort(preferred)
		time.Sleep(250 * time.Millisecond)
	}
}

func killListenersOnPort(port string) {
	port = strings.TrimSpace(port)
	if port == "" {
		return
	}
	pids := pidsListeningOnPort(port)
	for _, pid := range pids {
		if pid <= 0 {
			continue
		}
		if runtime.GOOS == "windows" {
			_ = exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
		} else {
			_ = exec.Command("kill", "-9", strconv.Itoa(pid)).Run()
		}
	}
}

func pidsListeningOnPort(port string) []int {
	if runtime.GOOS != "windows" {
		out, err := exec.Command("lsof", "-ti", "tcp:"+port).Output()
		if err != nil {
			return nil
		}
		var pids []int
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if n, err := strconv.Atoi(line); err == nil {
				pids = append(pids, n)
			}
		}
		return pids
	}
	out, err := exec.Command("netstat", "-ano").Output()
	if err != nil {
		return nil
	}
	return parseNetstatListeningPIDs(string(out), port)
}

func parseNetstatListeningPIDs(out, port string) []int {
	port = strings.TrimSpace(port)
	if port == "" {
		return nil
	}
	needle := ":" + port
	seen := map[int]bool{}
	var pids []int
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, needle) {
			continue
		}
		upper := strings.ToUpper(line)
		if !strings.Contains(upper, "LISTENING") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		local := fields[1]
		if !strings.HasSuffix(local, needle) {
			continue
		}
		pidStr := fields[len(fields)-1]
		n, err := strconv.Atoi(pidStr)
		if err != nil || n <= 0 || seen[n] {
			continue
		}
		seen[n] = true
		pids = append(pids, n)
	}
	return pids
}

// killBotDebugChromes force-kills Chrome for THIS account's debug port only.
// Never wipe the whole 93xx range — parallel setup lanes share that band.
func killBotDebugChromes() {
	port := strings.TrimSpace(liveDebugPort)
	if port == "" {
		port = strings.TrimSpace(currentDebugPort())
	}
	if port != "" {
		killListenersOnPort(port)
	}
	if id := strings.TrimSpace(liveChromeAccount); id != "" {
		if dir, err := chromeDirForAccount(id); err == nil {
			killChromeWithUserDataDir(dir)
		}
	}
}

func killChromeWithUserDataDir(dir string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return
	}
	abs, err := filepath.Abs(dir)
	if err == nil {
		dir = abs
	}
	if runtime.GOOS == "windows" {
		// Match by full path and by profiles\<id> fragment (path separators vary).
		needle := strings.ToLower(filepath.ToSlash(dir))
		base := strings.ToLower(filepath.Base(dir))
		frag := strings.ToLower(filepath.ToSlash(filepath.Join("profiles", base)))
		ps := fmt.Sprintf(
			`$needles = @(%s, %s); Get-CimInstance Win32_Process -Filter "Name='chrome.exe'" | ForEach-Object { $cl = $_.CommandLine; if (-not $cl) { return }; $n = $cl.ToLower().Replace('\','/'); foreach ($d in $needles) { if ($d -and $n.Contains($d)) { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue; break } } }`,
			strconv.Quote(needle),
			strconv.Quote(frag),
		)
		cmd := exec.Command("powershell", "-NoProfile", "-Command", ps)
		_ = cmd.Start()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		}
		return
	}
	out, err := exec.Command("ps", "aux").Output()
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "chrome") || !strings.Contains(line, dir) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if pid, err := strconv.Atoi(fields[1]); err == nil && pid > 0 {
			_ = exec.Command("kill", "-9", strconv.Itoa(pid)).Run()
		}
	}
}

// loginWithRealChrome opens the bot Chrome profile and logs into Tumblr.
// If credentials exist, it fills email/password automatically.
// Skips the form when the profile already has a sid, unless recovery
// asked for a forced login (API already rejected that session).
func loginWithRealChrome(auth *Auth) error {
	prevQuiet := quietChromeLogin
	quietChromeLogin = false
	defer func() { quietChromeLogin = prevQuiet }()

	chromeLoginMu.Lock()
	defer chromeLoginMu.Unlock()
	return loginWithRealChromeLocked(auth)
}

func loginWithRealChromeLocked(auth *Auth) error {
	return loginChromeSession(auth, false)
}

func loginChromeSession(auth *Auth, forceForm bool) error {
	if skipChromeRecovery {
		return fmt.Errorf("Chrome login blocked during reach")
	}
	prevClicks := nativeChromeClicks
	if !quietChromeLogin {
		nativeChromeClicks = false
	}
	defer func() { nativeChromeClicks = prevClicks }()

	restore, err := activateAccountForAuth(auth)
	if err != nil {
		return err
	}
	if restore != nil {
		defer restore()
	}

	id := accountIDFromAuth(*auth)
	if at, bad := recentBadLogin(id); bad && !explicitLogin {
		return fmt.Errorf("Tumblr rejected %s's login at %s — not retrying automatically for 24h (run: tumblr.exe -login -account %s)", id, at.Format("15:04"), id)
	}

	// Hide Chrome / gaming: never force a real window. Headless also skips
	// forceProxySessionBind — that path logs out and re-fills the form, which
	// always hits captcha we can't solve. Prefer syncing an existing Chrome sid.
	prevBind := forceProxySessionBind
	if !headlessChrome {
		forceProxySessionBind = true
	}
	defer func() { forceProxySessionBind = prevBind }()
	cmd, err := startChromeLoggedInOpts(auth, forceForm, !headlessChrome)
	if err != nil {
		if cmd != nil {
			stopChrome(cmd)
			time.Sleep(2 * time.Second)
		}
		return err
	}
	clearBadLogin(id)
	fmt.Println("Synced cookies. Closing bot Chrome for this profile.")
	stopChrome(cmd)
	time.Sleep(2 * time.Second)
	// Make sure this account's debug port is free for the next recovery/reach pass.
	if id := accountIDFromAuth(*auth); id != "" {
		ensureFreshChromeForAccount(id)
	}
	return nil
}

// loginWithRealChromeKeepOpen logs in the same way as -login, but leaves
// that account's Chrome open so you can look around until stop is called.
func loginWithRealChromeKeepOpen(auth *Auth) (stop func(), err error) {
	prevQuiet := quietChromeLogin
	quietChromeLogin = false
	defer func() { quietChromeLogin = prevQuiet }()

	chromeLoginMu.Lock()
	defer chromeLoginMu.Unlock()

	if skipChromeRecovery {
		return nil, fmt.Errorf("Chrome login blocked during reach")
	}
	prevClicks := nativeChromeClicks
	nativeChromeClicks = false
	defer func() { nativeChromeClicks = prevClicks }()

	prevBind := forceProxySessionBind
	forceProxySessionBind = true
	defer func() { forceProxySessionBind = prevBind }()
	// Respect Hide Chrome — keep-open is for Watch; headless still hides the window.
	cmd, err := startChromeLoggedInOpts(auth, false, !headlessChrome)
	if err != nil {
		if cmd != nil {
			stopChrome(cmd)
			time.Sleep(2 * time.Second)
		}
		return nil, err
	}
	fmt.Println("Synced cookies. Chrome stays open until you close the window.")
	return func() {
		if cmd == nil {
			return
		}
		fmt.Println("Closing this Chrome.")
		stopChrome(cmd)
		time.Sleep(2 * time.Second)
	}, nil
}

func startChromeLoggedIn(auth *Auth) (*exec.Cmd, error) {
	return startChromeLoggedInMode(auth, false)
}

func startChromeLoggedInMode(auth *Auth, forceForm bool) (*exec.Cmd, error) {
	return startChromeLoggedInOpts(auth, forceForm, false)
}

// visible forces a real window even with headless on (Watch, age-verify).
func startChromeLoggedInOpts(auth *Auth, forceForm, visible bool) (*exec.Cmd, error) {
	if skipChromeRecovery {
		return nil, fmt.Errorf("Chrome login blocked during reach")
	}
	id := accountIDFromAuth(*auth)
	if id == "" {
		if acc, ok := currentAccount(); ok {
			id = acc.ID
		}
	}
	if accountIsTerminated(id) {
		return nil, terminatedAccountError{ID: id}
	}
	dir, err := chromeDirForAccount(id)
	if err != nil {
		return nil, err
	}
	chrome, err := chromePath()
	if err != nil {
		return nil, err
	}
	if err := verifyLaunchedChrome(chrome); err != nil {
		return nil, err
	}

	detachLiveChrome()

	creds, credErr := loadCredentialsFor(auth)
	auto := credErr == nil
	ensureFreshChromeForAccount(id)

	// Sticky proxy for this account's Chrome (Watch, login, setup, reach recovery).
	// Same exit IP as paced API traffic; local auth bridge injects credentials so
	// Chrome never sees a proxy Sign-in dialog (creds stay off argv).
	proxyLaunch, proxyErr := prepareChromeProxyLaunch(context.Background(), id, dir)
	if proxyErr != nil {
		return nil, fmt.Errorf("sticky proxy for Chrome: %w", proxyErr)
	}
	if err := requireChromeProxyLaunch(proxyLaunch); err != nil {
		return nil, err
	}
	// Tumblr "Active Sessions" records the IP at login time. Watch/Login may force
	// one fresh sign-in through the sticky proxy; setup batches keep the sid and
	// still route Chrome via the proxy (no captcha storm).
	forcedProxyLogin := forceProxySessionBind && proxyLaunch != nil && !chromeProxySessionBound(id)
	if forcedProxyLogin {
		forceForm = true
		fmt.Println("Forcing fresh Tumblr login via sticky proxy so Active Sessions shows the proxy IP (not a prior home-UK session)…")
	}
	proxyOwned := false
	loginOK := false
	defer func() {
		if !proxyOwned && proxyLaunch != nil && proxyLaunch.stop != nil {
			proxyLaunch.stop()
		}
		if loginOK && proxyLaunch != nil {
			markChromeProxySessionBound(id, proxyLaunch.ProxyID)
		}
	}()

	preferred := debugPortForAccount(id)
	port := pickFreeDebugPort(preferred)
	liveDebugPort = port
	liveChromeAccount = id
	liveChromeProxyID = ""

	if creds.Email != "" {
		fmt.Println("Account:", id, "("+creds.Email+")")
	} else if acc, ok := currentAccount(); ok {
		fmt.Println("Account:", acc.ID, "("+acc.Email+")")
	}
	fmt.Println("Profile folder:", dir)
	fmt.Println("Debug port:", port)
	if port != preferred {
		fmt.Println("Port", preferred, "was already in use — using", port, "so we do not attach to another Chrome.")
	}
	if proxyLaunch != nil {
		fmt.Println("Chrome proxy:", proxyLaunch.LogLabel, "("+proxyLaunch.ProxyID+") via local auth bridge")
	}
	fmt.Println()
	if auto {
		fmt.Println("Auto-login enabled for this account.")
	} else {
		fmt.Println("No credentials for this account — will reuse an existing Chrome session if present.")
		fmt.Println("Add email:password pairs in config/credentials.json")
	}
	fmt.Println()

	markChromeCleanExit(dir)

	// EA / games routinely leave <2GB free; Tumblr then Aw-Snaps with Out of Memory.
	waitForChromeMemoryHeadroom(2500, 90*time.Second)

	startURL := "https://www.tumblr.com/login"
	if auto {
		fmt.Println("Opening Tumblr login…")
	}

	hidden := headlessChrome && !visible
	if hidden {
		fmt.Println("Headless — Chrome stays hidden (captchas can't be solved in this mode).")
	}

	args := []string{
		"--user-data-dir=" + dir,
		"--remote-debugging-port=" + port,
		"--remote-debugging-address=127.0.0.1",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-session-crashed-bubble",
		"--hide-crash-restore-bubble",
		"--disable-infobars",
		"--noerrdialogs",
		"--disable-save-password-bubble",
		"--password-store=basic",
		"--disable-features=PasswordManager,PasswordLeakDetection,PasswordCheck,PasswordManagerOnboarding,SafetyCheck,SafetyHub,SafetyTip,IsolateOrigins,site-per-process,TranslateUI,MediaRouter,CalculateNativeWinOcclusion",
		// Keep bot Chrome lean — EA/games often leave <2GB free and Tumblr OOMs.
		"--disable-extensions",
		"--disable-component-extensions-with-background-pages",
		"--disable-background-networking",
		"--disable-background-timer-throttling",
		"--disable-renderer-backgrounding",
		"--disable-ipc-flooding-protection",
		"--disable-sync",
		"--disable-default-apps",
		"--disable-hang-monitor",
		"--metrics-recording-only",
		"--no-pings",
		"--renderer-process-limit=3",
		"--process-per-site",
		"--disk-cache-size=33554432",
		"--media-cache-size=8388608",
		"--js-flags=--max-old-space-size=384",
	}
	args = mergeChromeProxyArgs(args, proxyLaunch)
	if proxyLaunch != nil {
		args = append(args, chromeWebRTCProxyArgs()...)
	}
	fp := fingerprint()
	args = append(args, fp.langArg())
	if hidden {
		args = append(args, headlessChromeArgs(chrome)...)
	}
	if !hidden {
		// Visible but don't maximize — Harrie often has other work on-screen.
		// Window size matches the shared fingerprint (same as headless/API claims).
		args = append(args, fp.windowSizeArg(), "--window-position=80,40")
	}
	cmd := exec.Command(chrome, append(args, startURL)...)
	if err := cmd.Start(); err != nil {
		liveDebugPort = ""
		liveChromeAccount = ""
		liveChromePID = 0
		liveChromeProxyID = ""
		return nil, err
	}
	if proxyLaunch != nil {
		liveChromeProxyID = proxyLaunch.ProxyID
		setLiveChromeProxyStop(proxyLaunch.stop)
		proxyOwned = true
	} else {
		liveChromeProxyID = ""
		stopLiveChromeProxy()
	}
	if cmd.Process != nil {
		liveChromePID = cmd.Process.Pid
	}
	ensurePasswordNagWatcher()

	if err := waitForDebugPort(12 * time.Second); err != nil {
		if p := readDevToolsPort(dir); p != "" {
			liveDebugPort = p
			fmt.Println("Using Chrome debug port from profile:", p)
		} else {
			stopChrome(cmd)
			return nil, fmt.Errorf("%w (is another Chrome already using debug port %s?)", err, port)
		}
	} else if p := readDevToolsPort(dir); p != "" && p != liveDebugPort {
		liveDebugPort = p
	}

	if err := waitForPageTarget(8 * time.Second); err != nil {
		stopChrome(cmd)
		return nil, err
	}
	if id, err := existingPageTargetID(); err == nil {
		closeExtraChromeTabs(id)
	}
	if err := errIfChromeTerminated(); err != nil {
		stopChrome(cmd)
		return nil, err
	}
	if !hidden {
		raiseLiveChromeWindow(id)
	}

	if setupUILog != nil {
		setupSay("Waiting for the Tumblr browser session...")
	}
	hasSid := waitForTumblrSession(8*time.Second) == nil
	if !hasSid && onTumblrConsentPage() {
		fmt.Println("Already logged in — dismissing Privacy & Cookies…")
		hasSid = waitForTumblrSession(45*time.Second) == nil
	}
	switch {
	case hasSid && !forceForm:
		fmt.Println("Already logged in — syncing cookies…")
		_ = os.Stdout.Sync()
	case auto && forceForm:
		fmt.Println("API rejected this session — logging out of Chrome and signing in again…")
		_ = os.Stdout.Sync()
		if err := finishChromeLoginWait(cmd, reloginTumblrInChrome(creds)); err != nil {
			return nil, err
		}
	case auto:
		fmt.Println("Not logged in — auto-filling Tumblr login…")
		_ = os.Stdout.Sync()
		if err := finishChromeLoginWait(cmd, autoFillTumblrLogin(creds)); err != nil {
			return nil, err
		}
	default:
		if forceForm && hasSid {
			fmt.Println("Need a fresh login — logging out. Finish sign-in in Chrome…")
			_ = chromeLogoutOnly()
		}
		fmt.Println("Waiting up to 3 minutes for login in Chrome…")
		if err := waitForTumblrSession(3 * time.Minute); err != nil {
			stopChrome(cmd)
			return nil, fmt.Errorf("login did not complete: %w", err)
		}
	}

	// Always scrape sid into auth.json when Chrome is logged in. The old
	// "dashboard up → return" fast path skipped CDP sync, so recoverSession
	// (wipe/reach) printed "Synced cookies" then immediately 401'd on CSRF.
	looksIn := chromeLooksLoggedIn()
	if looksIn {
		fmt.Println("Chrome session ready — continuing.")
		_ = os.Stdout.Sync()
	}

	// Unattended setup: Chrome dashboard is enough. Do not block on CDP cookie
	// scrape / OneDrive auth.json / proxy CSRF — those hung the lane for minutes
	// after "session ready" with no further log lines.
	if skipSetupAgeKey && looksIn {
		if name := strings.TrimSpace(auth.Blog); usableBlogName(name) {
			fmt.Println("Active blog:", name, "(from saved auth — setup continues)")
		} else {
			fmt.Println("Chrome logged in — setup continues (skipping slow cookie/CSRF sync).")
		}
		_ = os.Stdout.Sync()
		go func() {
			cookies, err := cookiesFromDebugPortTimed(3 * time.Second)
			if err != nil || cookieValue(cookies, "sid") == "" {
				return
			}
			auth.Cookie = buildTumblrCookieHeader(cookies)
			persistMaturePref(auth)
			_ = saveAuth(*auth)
		}()
		loginOK = true
		return cmd, nil
	}

	cookies, err := cookiesFromDebugPortTimed(5 * time.Second)
	sid := ""
	if err == nil {
		sid = cookieValue(cookies, "sid")
		if sid != "" {
			auth.Cookie = buildTumblrCookieHeader(cookies)
			persistMaturePref(auth)
			clearNeedsInteractiveLogin(auth)
			if liveChromeProxyID != "" {
				auth.ProxyID = liveChromeProxyID
			}
			if err := saveAuth(*auth); err != nil {
				stopChrome(cmd)
				return nil, err
			}
		}
	} else {
		fmt.Println("Cookie sync:", err)
		_ = os.Stdout.Sync()
	}
	if sid == "" {
		if !looksIn && !chromeLooksLoggedIn() {
			stopChrome(cmd)
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("still no sid cookie — login did not complete")
		}
		fmt.Println("Could not read sid yet, but Chrome is logged in — continuing.")
		_ = os.Stdout.Sync()
		loginOK = true
		return cmd, nil
	}

	// Setup-only fast path: cookies are on disk; skip proxy CSRF/identity so
	// profile setup is not blocked by API. recoverSession re-checks CSRF next.
	if looksIn && forceForm == false && !auto {
		loginOK = true
		return cmd, nil
	}

	client := newAuthClient(auth)
	if client != nil && client.Timeout > 8*time.Second {
		client.Timeout = 8 * time.Second
	}
	if err := refreshCSRF(client, auth); err != nil {
		fmt.Println("CSRF refresh warning:", err)
		if !forceForm && auto {
			fmt.Println("Saved session is dead — logging out of Chrome and signing in again…")
			if err := finishChromeLoginWait(cmd, reloginTumblrInChrome(creds)); err != nil {
				stopChrome(cmd)
				return nil, err
			}
			cookies, err = cookiesFromDebugPort()
			if err != nil {
				stopChrome(cmd)
				return nil, err
			}
			if cookieValue(cookies, "sid") == "" {
				stopChrome(cmd)
				return nil, fmt.Errorf("still no sid cookie — login did not complete")
			}
			auth.Cookie = buildTumblrCookieHeader(cookies)
			persistMaturePref(auth)
			if err := saveAuth(*auth); err != nil {
				stopChrome(cmd)
				return nil, err
			}
		} else if forceForm {
			// recoverSession / -login: cookies were just minted in Chrome; a
			// transient 401 here must not discard them — caller retries CSRF.
			fmt.Println("CSRF not ready yet — cookies saved; caller will retry.")
		} else {
			stopChrome(cmd)
			return nil, fmt.Errorf("Tumblr still unauthorized after login: %w", err)
		}
	} else if name, err := syncAuthIdentity(client, auth); err != nil {
		maybeFillBlog(client, auth)
		fmt.Println("Could not detect blog identity yet:", err)
	} else {
		fmt.Println("Active blog:", name, "("+auth.Blog+")")
	}
	// Password/leak dismiss runs in ensurePasswordNagWatcher with a hard
	// timeout. Doing it inline here used to freeze setup on the dashboard
	// when UIA hung walking Chrome's tree.
	loginOK = true
	return cmd, nil
}

func loginWaitBudget() time.Duration {
	if headlessChrome {
		return 20 * time.Second // cannot solve captcha — don't park the batch
	}
	if quietChromeLogin {
		return 90 * time.Second
	}
	if skipSetupAgeKey {
		return 45 * time.Second // unattended: don't babysit captcha for minutes
	}
	return 8 * time.Minute
}

func finishChromeLoginWait(cmd *exec.Cmd, fillErr error) error {
	if isTerminated(fillErr) {
		stopChrome(cmd)
		time.Sleep(1 * time.Second)
		return fillErr
	}
	if isPasswordChangeRequired(fillErr) {
		var p passwordChangeRequiredError
		_ = errors.As(fillErr, &p)
		fmt.Println("Auto-login failed:", fillErr)
		stopChrome(cmd)
		time.Sleep(1 * time.Second)
		_ = discardPasswordChangeLogin(p.Email)
		return fillErr
	}
	if isBadLogin(fillErr) {
		var b badLoginError
		_ = errors.As(fillErr, &b)
		fmt.Println("Auto-login failed:", fillErr)
		stopChrome(cmd)
		time.Sleep(1 * time.Second)
		// Setup lanes must not wipe credentials on a flaky "wrong password"
		// flash (empty React fields / captcha). Keep the login for a retry.
		if skipSetupAgeKey {
			fmt.Println("Keeping credentials (setup mode) — not discarding.")
			return fillErr
		}
		_ = discardDeadLogin(b.Email)
		return fillErr
	}
	// Headless / quiet / unattended setup cannot babysit captcha — close and move on.
	if fillErr != nil && strings.Contains(strings.ToLower(fillErr.Error()), "captcha") {
		if headlessChrome || quietChromeLogin || skipSetupAgeKey {
			fmt.Println("Login blocked by captcha — closing Chrome, next account.")
			stopChromeOpts(cmd, false)
			return fillErr
		}
	}
	if fillErr != nil {
		fmt.Println("Login not finished yet:", fillErr)
		if !quietChromeLogin && !headlessChrome && !skipSetupAgeKey {
			fmt.Println("Chrome stays open. Finish captcha/2FA in the window if it appears — I will not close it early.")
		}
	} else {
		fmt.Println("Login submitted — waiting for session…")
	}
	waitBudget := loginWaitBudget()
	if skipSetupAgeKey {
		waitBudget = 45 * time.Second
	}
	if err := waitForTumblrSession(waitBudget); err != nil {
		if isPasswordChangeRequired(err) {
			var p passwordChangeRequiredError
			_ = errors.As(err, &p)
			if p.Email == "" {
				if acc, ok := currentAccount(); ok {
					p.Email = acc.Email
					p.ID = acc.ID
				}
			}
			fmt.Println("Tumblr requires a password change — discarding this login.")
			stopChrome(cmd)
			time.Sleep(1 * time.Second)
			_ = discardPasswordChangeLogin(p.Email)
			return p
		}
		if pageShowsBadLogin() {
			email := ""
			if acc, ok := currentAccount(); ok {
				email = acc.Email
			}
			// After a missed/abandoned captcha Tumblr often flashes the same
			// "incorrect email or password" banner — do NOT discard; retry fill.
			if fillErr != nil && strings.Contains(strings.ToLower(fillErr.Error()), "captcha") {
				fmt.Println("Login error banner after captcha — retrying fill (not discarding)…")
				if retryErr := retryBadLoginFillInLiveChrome(); retryErr == nil {
					if err := waitForTumblrSession(loginWaitBudget()); err == nil {
						return nil
					}
				} else {
					fmt.Println("Login retry after captcha:", retryErr)
				}
				stopChromeOpts(cmd, false)
				return fmt.Errorf("captcha/login blocked — skipped")
			}
			fmt.Println("Tumblr says this email/password is wrong — retrying before discard…")
			if retryErr := retryBadLoginFillInLiveChrome(); retryErr == nil {
				if err := waitForTumblrSession(loginWaitBudget()); err == nil {
					return nil
				}
			} else {
				fmt.Println("Login retry:", retryErr)
			}
			if !pageShowsBadLogin() {
				// Banner cleared / moved on — give session wait one more shot.
				if err := waitForTumblrSession(loginWaitBudget()); err == nil {
					return nil
				}
			}
			fmt.Println("Tumblr still says this email/password is wrong.")
			stopChrome(cmd)
			time.Sleep(1 * time.Second)
			if skipSetupAgeKey {
				fmt.Println("Keeping credentials (setup mode) — not discarding.")
				return badLoginError{Email: email, ID: accountSlug(email)}
			}
			_ = discardDeadLogin(email)
			return badLoginError{Email: email, ID: accountSlug(email)}
		}
		if recoverLiveDebugPort() && !quietChromeLogin && !skipSetupAgeKey {
			fmt.Println("Still no sid — Chrome is open. Waiting longer for captcha/2FA…")
			if err := waitForTumblrSession(loginWaitBudget()); err == nil {
				return nil
			}
		}
		stopChrome(cmd)
		return fmt.Errorf("login did not complete: %w", err)
	}
	return nil
}

// retryBadLoginFillInLiveChrome reloads /login and runs the filled-form retry
// loop while Chrome is still open (finishChromeLoginWait path).
func retryBadLoginFillInLiveChrome() error {
	creds, err := loadCredentials()
	if err != nil {
		return err
	}
	ctx, cancel, err := withBotChrome(90 * time.Second)
	if err != nil {
		return err
	}
	defer cancel()
	return chromedp.Run(ctx,
		chromedp.Navigate("https://www.tumblr.com/login"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(1500*time.Millisecond),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return fillTumblrLoginFormJS(ctx, creds)
		}),
	)
}

func pageShowsBadLogin() bool {
	ctx, cancel, err := withBotChrome(8 * time.Second)
	if err != nil {
		return false
	}
	defer cancel()
	var bad bool
	_ = chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		// Captcha / verify UI can leave copy that looks like a login error.
		if evalBoolJS(ctx, tumblrCaptchaVisibleJS) {
			bad = false
			return nil
		}
		bad = evalBoolJS(ctx, tumblrLoginErrorJS)
		return nil
	}))
	return bad
}

func pageShowsPasswordChangeRequired() bool {
	list, err := listChromeTargets()
	if err != nil {
		return false
	}
	for _, t := range list {
		if t.Type != "page" || !tumblrLoginURL(t.URL) {
			continue
		}
		var hit bool
		if err := evalJSONOnChromeTarget(t, tumblrPasswordChangeRequiredJS, &hit); err == nil && hit {
			return true
		}
	}
	ctx, cancel, err := withBotChrome(8 * time.Second)
	if err != nil {
		return false
	}
	defer cancel()
	var hit bool
	_ = chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		hit = evalBoolJS(ctx, tumblrPasswordChangeRequiredJS)
		return nil
	}))
	return hit
}

func clearTumblrSessionCookies(ctx context.Context) error {
	cookies, err := network.GetCookies().WithURLs([]string{
		"https://www.tumblr.com",
		"https://tumblr.com",
	}).Do(ctx)
	if err != nil {
		return err
	}
	for _, c := range cookies {
		switch strings.ToLower(c.Name) {
		case "sid", "pfg", "logged_in":
		default:
			continue
		}
		del := network.DeleteCookies(c.Name)
		if c.Domain != "" {
			del = del.WithDomain(c.Domain)
		}
		if c.Path != "" {
			del = del.WithPath(c.Path)
		}
		_ = del.Do(ctx)
	}
	return nil
}

func chromeLogoutOnly() error {
	ctx, cancel, err := withBotChrome(30 * time.Second)
	if err != nil {
		return err
	}
	defer cancel()
	return chromedp.Run(ctx,
		chromedp.Navigate("https://www.tumblr.com/logout"),
		chromedp.Sleep(2*time.Second),
		chromedp.ActionFunc(clearTumblrSessionCookies),
		chromedp.Navigate("https://www.tumblr.com/login"),
		chromedp.WaitReady("body", chromedp.ByQuery),
	)
}

func reloginTumblrInChrome(creds Credentials) error {
	ctx, cancel, err := withBotChrome(90 * time.Second)
	if err != nil {
		return err
	}
	defer cancel()

	return chromedp.Run(ctx,
		chromedp.Navigate("https://www.tumblr.com/logout"),
		chromedp.Sleep(2*time.Second),
		chromedp.ActionFunc(clearTumblrSessionCookies),
		chromedp.Navigate("https://www.tumblr.com/login"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(1500*time.Millisecond),
		chromedp.ActionFunc(errIfTerminatedPage),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return fillTumblrLoginFormJS(ctx, creds)
		}),
	)
}

func autoFillTumblrLogin(creds Credentials) error {
	ctx, cancel, err := withBotChrome(90 * time.Second)
	if err != nil {
		return err
	}
	defer cancel()

	return chromedp.Run(ctx,
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(1500*time.Millisecond),
		chromedp.ActionFunc(errIfTerminatedPage),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var href string
			_ = chromedp.Location(&href).Do(ctx)
			if err := errIfTerminatedHref(href); err != nil {
				return err
			}
			if strings.Contains(href, "/login") {
				fmt.Println("Chrome is on the login screen — filling the form even if a sid cookie exists.")
				return fillTumblrLoginFormJS(ctx, creds)
			}
			if loginFillDone(href, cookieOK(ctx)) {
				return nil
			}
			if err := chromedp.Navigate("https://www.tumblr.com/login").Do(ctx); err != nil {
				return err
			}
			_ = chromedp.WaitReady("body", chromedp.ByQuery).Do(ctx)
			_ = chromedp.Sleep(1500 * time.Millisecond).Do(ctx)
			if err := errIfTerminatedPage(ctx); err != nil {
				return err
			}
			return fillTumblrLoginFormJS(ctx, creds)
		}),
	)
}

func cookieOK(ctx context.Context) bool {
	cookies, err := network.GetCookies().WithURLs([]string{"https://www.tumblr.com"}).Do(ctx)
	if err != nil {
		return false
	}
	return cookieValue(cookies, "sid") != ""
}

const tumblrVisibleEmailJS = `(() => {
	const vis = (el) => {
		if (!el) return false;
		const s = getComputedStyle(el);
		if (s.display === 'none' || s.visibility === 'hidden' || Number(s.opacity) === 0) return false;
		const r = el.getBoundingClientRect();
		return r.width > 2 && r.height > 2;
	};
	const nodes = [...document.querySelectorAll('input[name="email"], input[type="email"], input[placeholder="Email"], input[aria-label="email"], input[autocomplete="username"]')];
	return nodes.some(vis);
})()`

const tumblrVisiblePasswordJS = `(() => {
	const vis = (el) => {
		if (!el) return false;
		const s = getComputedStyle(el);
		if (s.display === 'none' || s.visibility === 'hidden' || Number(s.opacity) === 0) return false;
		const r = el.getBoundingClientRect();
		return r.width > 2 && r.height > 2;
	};
	const nodes = [...document.querySelectorAll('input[name="password"], input[type="password"], input[aria-label="password"], input[autocomplete="current-password"]')];
	return nodes.some(vis);
})()`

const tumblrLoginErrorJS = `(() => {
	const t = ((document.body && document.body.innerText) || '').toLowerCase();
	return /email or password (you entered )?(is|was|were) incorrect|incorrect email or password|that password is incorrect/.test(t);
})()`

// tumblrRecaptchaFailedJS is Google's "Verification failed / try again" after a bad click.
const tumblrRecaptchaFailedJS = `(() => {
	const t = ((document.body && document.body.innerText) || '').replace(/\s+/g, ' ');
	return /verification failed|could not verify|please try again|try again later/i.test(t) &&
		/recaptcha|robot|captcha|check the box/i.test(t);
})()`

// Tumblr's red login banner when the account must reset before signing in.
// Not Chrome's breach nag — that is dismissed separately and never blocks sid.
const tumblrPasswordChangeRequiredJS = `(() => {
	const t = ((document.body && document.body.innerText) || '').toLowerCase();
	if (!/please\s+change your password/.test(t)) return false;
	// Still on the login form (email/password visible) — forced reset, not a post-login prompt.
	const vis = (el) => {
		if (!el) return false;
		const s = getComputedStyle(el);
		if (s.display === 'none' || s.visibility === 'hidden' || Number(s.opacity) === 0) return false;
		const r = el.getBoundingClientRect();
		return r.width > 2 && r.height > 2;
	};
	const email = [...document.querySelectorAll('input[type="email"], input[name="email"], input[autocomplete="username"]')].some(vis);
	const pass = [...document.querySelectorAll('input[type="password"]')].some(vis);
	return email && pass;
})()`

const tumblrCannotPostLoginJS = `(() => {
	const t = ((document.body && document.body.innerText) || '') + ' ' + (document.title || '');
	return /cannot post\s*\/login/i.test(t);
})()`

const tumblrLoginHydratedJS = `(() => {
	const vis = (el) => {
		if (!el) return false;
		const s = getComputedStyle(el);
		if (s.display === 'none' || s.visibility === 'hidden' || Number(s.opacity) === 0) return false;
		const r = el.getBoundingClientRect();
		return r.width > 2 && r.height > 2;
	};
	const label = (el) => (el.innerText || el.getAttribute('aria-label') || el.value || '').replace(/\s+/g, ' ').trim();
	const btn = [...document.querySelectorAll('button, [role="button"], input[type="submit"]')].filter(vis).find(b => /^(log in|sign in|next|continue)$/i.test(label(b)));
	if (!btn) return false;
	return Object.keys(btn).some((k) => k.startsWith('__react'));
})()`

const tumblrCaptchaVisibleJS = `(() => {
	const vis = (el, minW, minH) => {
		if (!el) return false;
		const s = getComputedStyle(el);
		if (s.display === 'none' || s.visibility === 'hidden' || Number(s.opacity) === 0) return false;
		const r = el.getBoundingClientRect();
		return r.width > minW && r.height > minH;
	};
	const body = ((document.body && document.body.innerText) || '').replace(/\s+/g, ' ');
	// Visible "I'm not a robot" / "Check the box above to continue" on Tumblr login.
	if (/i('| a)?m not a robot|check the box above to continue/i.test(body)) return true;
	// Anchor widget is ~300x74; ignore tiny invisible recaptcha iframes.
	const challengeFrame = [...document.querySelectorAll('iframe')].find(f => {
		const meta = ((f.src || '') + ' ' + (f.title || '') + ' ' + (f.name || '')).toLowerCase();
		if (!/recaptcha|hcaptcha|arkoselabs|funcaptcha|challenge/i.test(meta)) return false;
		if (/api2\/anchor|recaptcha\/enterprise\/anchor|title=\"recaptcha\"/i.test(meta) || /recaptcha/i.test(f.title || '')) {
			return vis(f, 100, 40);
		}
		return vis(f, 120, 60);
	});
	if (challengeFrame) return true;
	const explicit = [...document.querySelectorAll('[role="dialog"], main, section, h1, h2, p, div')]
		.filter(el => vis(el, 40, 10))
		.map(el => (el.innerText || '').replace(/\s+/g, ' ').trim())
		.find(t => t && t.length < 240 && /verify you are (a )?human|complete the captcha|unusual traffic|security challenge|i('| a)?m not a robot|check the box above/i.test(t));
	return !!explicit;
})()`

// tumblrRecaptchaAnchorBoxJS returns hit points for the checkbox box and/or the
// "I'm not a robot" / "I am not a robot" text. Either is a valid click target.
const tumblrRecaptchaAnchorBoxJS = `(() => {
	const vis = (el, minW, minH) => {
		if (!el) return false;
		const s = getComputedStyle(el);
		if (s.display === 'none' || s.visibility === 'hidden' || Number(s.opacity) === 0) return false;
		const r = el.getBoundingClientRect();
		return r.width >= minW && r.height >= minH;
	};
	const hits = [];
	const add = (cx, cy, kind) => {
		if (!(cx > 0 && cy > 0)) return;
		hits.push({ cx, cy, kind });
	};
	const robotRe = /i('|’| a)?m not a robot/i;
	// Google anchor iframe: left strip = checkbox, right = robot text.
	const frames = [...document.querySelectorAll('iframe')].filter(el => {
		const meta = ((el.src || '') + ' ' + (el.title || '') + ' ' + (el.name || '') + ' ' + (el.id || '')).toLowerCase();
		if (!/recaptcha|hcaptcha|anchor/.test(meta)) return false;
		return vis(el, 40, 20);
	});
	let f = frames.find(el => /anchor/i.test((el.src || '') + (el.name || '') + (el.title || '')));
	if (!f) f = frames.find(el => /recaptcha/i.test(el.title || ''));
	if (!f) f = frames[0];
	if (f) {
		const r = f.getBoundingClientRect();
		// Checkbox sits in the left square of the anchor (~28×28). Aim dead-center.
		const box = Math.min(28, Math.max(18, r.height * 0.72));
		add(r.x + box / 2, r.y + r.height / 2, 'box');
		add(r.x + r.width * 0.42, r.y + r.height / 2, 'text');
	}
	// Page copy: "I am not a robot" / "I'm not a robot" (outside iframe).
	for (const el of document.querySelectorAll('div,span,label,p,b,strong')) {
		if (!vis(el, 20, 8)) continue;
		const t = (el.innerText || el.textContent || '').replace(/\s+/g, ' ').trim();
		if (!robotRe.test(t) || t.length > 48) continue;
		const r = el.getBoundingClientRect();
		add(r.x + r.width / 2, r.y + r.height / 2, 'label');
		add(Math.max(8, r.x - 32), r.y + r.height / 2, 'box-near-label');
		break;
	}
	// Tumblr prompt sits under the widget — click just above it for the box.
	for (const el of document.querySelectorAll('div,span,label,p,b,strong')) {
		if (!vis(el, 40, 8)) continue;
		const t = (el.innerText || el.textContent || '').replace(/\s+/g, ' ').trim();
		if (!/check the box above to continue/i.test(t) || t.length > 60) continue;
		const r = el.getBoundingClientRect();
		add(r.x + 24, r.y - 28, 'above-prompt-box');
		add(r.x + 90, r.y - 28, 'above-prompt-text');
		break;
	}
	if (!hits.length) return null;
	const first = hits[0];
	return {
		x: first.cx, y: first.cy, w: 40, h: 40,
		cx: first.cx, cy: first.cy,
		vw: window.innerWidth || 0,
		vh: window.innerHeight || 0,
		dpr: window.devicePixelRatio || 1,
		hits,
	};
})()`

const tumblrLoginNeedsRefillJS = `(() => {
	const vis = (el) => {
		if (!el) return false;
		const s = getComputedStyle(el);
		if (s.display === 'none' || s.visibility === 'hidden' || Number(s.opacity) === 0) return false;
		const r = el.getBoundingClientRect();
		return r.width > 2 && r.height > 2;
	};
	const email = [...document.querySelectorAll('input[name="email"], input[type="email"]')].find(vis);
	if (!email) return false;
	return !String(email.value || '').includes('@');
})()`

func isCannotPostLoginBody(body string) bool {
	return strings.Contains(strings.ToLower(body), "cannot post /login")
}

func blockNativeTumblrLoginSubmit(ctx context.Context) {
	var ok bool
	_ = chromedp.Evaluate(`(() => {
		document.querySelectorAll('form').forEach((f) => {
			if (f.dataset.zoeyNoNav === '1') return;
			f.dataset.zoeyNoNav = '1';
			f.addEventListener('submit', (e) => { e.preventDefault(); }, true);
		});
		return true;
	})()`, &ok).Do(ctx)
}

func evalBoolJS(ctx context.Context, script string) bool {
	var ok bool
	_ = chromedp.Evaluate(script, &ok).Do(ctx)
	return ok
}

func waitJSTrue(ctx context.Context, timeout time.Duration, script string) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if evalBoolJS(ctx, script) {
			return nil
		}
		time.Sleep(400 * time.Millisecond)
	}
	return fmt.Errorf("timed out")
}

// clearTumblrLoginFieldsJS empties email+password so accidental keystrokes
// (or a half-typed OS click) cannot ride along into the next submit.
const clearTumblrLoginFieldsJS = `(() => {
	const vis = (el) => {
		if (!el) return false;
		const s = getComputedStyle(el);
		if (s.display === 'none' || s.visibility === 'hidden' || Number(s.opacity) === 0) return false;
		const r = el.getBoundingClientRect();
		return r.width > 2 && r.height > 2;
	};
	const wipe = (el) => {
		if (!el) return;
		el.focus();
		try { el.select(); } catch (e) {}
		const desc = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value');
		if (desc && desc.set) {
			desc.set.call(el, '');
		} else {
			el.value = '';
		}
		try { el.setAttribute('value', ''); } catch (e) {}
		el.dispatchEvent(new Event('input', { bubbles: true }));
		try { el.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'deleteContentBackward', data: null })); } catch (e) {}
		el.dispatchEvent(new Event('change', { bubbles: true }));
		el.dispatchEvent(new KeyboardEvent('keyup', { bubbles: true, key: 'Backspace' }));
	};
	const emailSel = 'input[name="email"], input[type="email"], input[placeholder="Email"], input[aria-label="email"], input[autocomplete="username"]';
	const passSel = 'input[name="password"], input[type="password"], input[aria-label="password"], input[autocomplete="current-password"]';
	[...document.querySelectorAll(emailSel)].filter(vis).forEach(wipe);
	[...document.querySelectorAll(passSel)].filter(vis).forEach(wipe);
	return true;
})()`

func clearTumblrLoginFields(ctx context.Context) {
	var ok bool
	_ = chromedp.Evaluate(clearTumblrLoginFieldsJS, &ok).Do(ctx)
}

func fillVisibleInputJS(kind, value string) string {
	val, _ := json.Marshal(value)
	sel := `input[name="email"], input[type="email"], input[placeholder="Email"], input[aria-label="email"], input[autocomplete="username"]`
	if kind == "password" {
		sel = `input[name="password"], input[type="password"], input[aria-label="password"], input[autocomplete="current-password"]`
	}
	selJSON, _ := json.Marshal(sel)
	// Prefer document.execCommand('insertText') — Tumblr's React inputs often
	// ignore native value setters, so the field looks empty even when we "filled".
	return fmt.Sprintf(`(() => {
		const vis = (el) => {
			if (!el) return false;
			const s = getComputedStyle(el);
			if (s.display === 'none' || s.visibility === 'hidden' || Number(s.opacity) === 0) return false;
			const r = el.getBoundingClientRect();
			return r.width > 2 && r.height > 2;
		};
		const want = %s;
		const nodes = [...document.querySelectorAll(%s)].filter(vis);
		const el = nodes[0];
		if (!el) return 'missing';
		el.scrollIntoView({ block: 'center', inline: 'nearest' });
		el.focus();
		try { el.click(); } catch (e) {}
		try { el.select(); } catch (e) {}
		const desc = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value');
		desc.set.call(el, '');
		try { el.setAttribute('value', ''); } catch (e) {}
		el.dispatchEvent(new Event('input', { bubbles: true }));
		let inserted = false;
		try { inserted = document.execCommand('insertText', false, want); } catch (e) { inserted = false; }
		if (!inserted || el.value !== want) {
			desc.set.call(el, want);
			try { el.setAttribute('value', want); } catch (e) {}
			el.dispatchEvent(new Event('input', { bubbles: true }));
			try { el.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: want })); } catch (e) {}
			el.dispatchEvent(new Event('change', { bubbles: true }));
		}
		if (el.value !== want) return 'stuck:' + String(el.value || '').length + '/' + want.length;
		return 'ok';
	})()`, string(val), string(selJSON))
}

func clickTumblrLoginButton(ctx context.Context) error {
	script := `(() => {
		const vis = (el) => {
			if (!el) return false;
			const s = getComputedStyle(el);
			if (s.display === 'none' || s.visibility === 'hidden' || Number(s.opacity) === 0) return false;
			const r = el.getBoundingClientRect();
			return r.width > 2 && r.height > 2;
		};
		const label = (el) => (el.innerText || el.getAttribute('aria-label') || el.value || '').replace(/\s+/g, ' ').trim();
		const btns = [...document.querySelectorAll('button, [role="button"], input[type="submit"]')].filter(vis);
		const match = btns.find(b => /^(log in|sign in|next|continue|done)$/i.test(label(b)));
		if (!match) return 'no-btn';
		match.focus();
		for (const type of ['pointerdown', 'mousedown', 'pointerup', 'mouseup', 'click']) {
			match.dispatchEvent(new MouseEvent(type, { bubbles: true, cancelable: true, view: window }));
		}
		match.click();
		return 'ok';
	})()`
	var result string
	if err := chromedp.Evaluate(script, &result).Do(ctx); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("login button: %s", result)
	}
	return nil
}

// badLoginFillRetries: Tumblr sometimes flashes "incorrect email or password"
// after a flaky captcha / rate blip even when the password is right. Re-fill
// and resubmit a few times before treating it as a real dead login.
const badLoginFillRetries = 3

func fillTumblrLoginFormJS(ctx context.Context, creds Credentials) error {
	var last error
	for attempt := 1; attempt <= badLoginFillRetries; attempt++ {
		err := fillTumblrLoginFormAttempt(ctx, creds, true)
		if err == nil {
			return nil
		}
		last = err
		if !isBadLogin(err) {
			return err
		}
		if attempt >= badLoginFillRetries {
			break
		}
		fmt.Printf("Tumblr says email/password wrong — clearing fields and re-entering (%d/%d)…\n", attempt, badLoginFillRetries-1)
		_ = os.Stdout.Sync()
		clearTumblrLoginFields(ctx)
		time.Sleep(400 * time.Millisecond)
		// Stay on the form when possible; only reload if fields vanished.
		if !evalBoolJS(ctx, tumblrVisibleEmailJS) {
			if navErr := chromedp.Navigate("https://www.tumblr.com/login").Do(ctx); navErr != nil {
				fmt.Println("login retry navigate:", navErr)
			} else {
				_ = chromedp.WaitReady("body", chromedp.ByQuery).Do(ctx)
				time.Sleep(1500 * time.Millisecond)
			}
		}
	}
	return last
}

func fillTumblrLoginFormAttempt(ctx context.Context, creds Credentials, retry bool) error {
	email := strings.TrimSpace(creds.Email)
	pass := creds.Password
	if email == "" || strings.TrimSpace(pass) == "" {
		return fmt.Errorf("missing email/password for this account")
	}
	fmt.Println("Filling Tumblr login for", email)

	if err := waitJSTrue(ctx, 25*time.Second, tumblrVisibleEmailJS); err != nil {
		return fmt.Errorf("email field never appeared (already logged in, or page blocked)")
	}
	if err := waitJSTrue(ctx, 20*time.Second, tumblrLoginHydratedJS); err != nil {
		fmt.Println("Login page still hydrating — clicking Log in anyway.")
	}
	if !retry {
		blockNativeTumblrLoginSubmit(ctx)
	}

	// Always wipe first — accidental keystrokes / leftover text must not stick.
	clearTumblrLoginFields(ctx)
	time.Sleep(200 * time.Millisecond)

	var filled string
	if err := chromedp.Evaluate(fillVisibleInputJS("email", email), &filled).Do(ctx); err != nil {
		return err
	}
	if filled != "ok" {
		return fmt.Errorf("login form: email fill failed (%s)", filled)
	}
	fmt.Printf("Email/username typed (%d chars)\n", len(email))
	_ = os.Stdout.Sync()

	if !evalBoolJS(ctx, tumblrVisiblePasswordJS) {
		_ = clickTumblrLoginButton(ctx)
		if evalBoolJS(ctx, tumblrCannotPostLoginJS) {
			return recoverCannotPostLogin(ctx, creds, retry)
		}
		if err := waitJSTrue(ctx, 20*time.Second, tumblrVisiblePasswordJS); err != nil {
			return fmt.Errorf("password field never appeared")
		}
		if !retry {
			blockNativeTumblrLoginSubmit(ctx)
		}
	}

	if err := chromedp.Evaluate(fillVisibleInputJS("password", pass), &filled).Do(ctx); err != nil {
		return err
	}
	if filled != "ok" {
		return fmt.Errorf("login form: password fill failed (%s)", filled)
	}
	fmt.Printf("Password typed (%d chars)\n", len(pass))
	_ = os.Stdout.Sync()
	if evalBoolJS(ctx, tumblrCaptchaVisibleJS) {
		return waitOutLoginCaptcha(ctx, email)
	}
	if err := clickTumblrLoginButton(ctx); err != nil {
		return err
	}
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if evalBoolJS(ctx, tumblrCannotPostLoginJS) {
			return recoverCannotPostLogin(ctx, creds, retry)
		}
		if evalBoolJS(ctx, tumblrPasswordChangeRequiredJS) {
			return passwordChangeRequiredError{Email: email, ID: accountSlug(email)}
		}
		if evalBoolJS(ctx, tumblrLoginErrorJS) && !evalBoolJS(ctx, tumblrCaptchaVisibleJS) {
			return badLoginError{Email: email, ID: accountSlug(email)}
		}
		if evalBoolJS(ctx, tumblrCaptchaVisibleJS) {
			return waitOutLoginCaptcha(ctx, email)
		}
		var href string
		_ = chromedp.Location(&href).Do(ctx)
		if loginFillDone(href, cookieOK(ctx)) {
			return nil
		}
		time.Sleep(400 * time.Millisecond)
	}
	if evalBoolJS(ctx, tumblrCannotPostLoginJS) {
		return recoverCannotPostLogin(ctx, creds, retry)
	}
	if evalBoolJS(ctx, tumblrPasswordChangeRequiredJS) {
		return passwordChangeRequiredError{Email: email, ID: accountSlug(email)}
	}
	if evalBoolJS(ctx, tumblrLoginErrorJS) && !evalBoolJS(ctx, tumblrCaptchaVisibleJS) {
		return badLoginError{Email: email, ID: accountSlug(email)}
	}
	if evalBoolJS(ctx, tumblrCaptchaVisibleJS) {
		return waitOutLoginCaptcha(ctx, email)
	}
	var href string
	_ = chromedp.Location(&href).Do(ctx)
	if loginFillDone(href, cookieOK(ctx)) {
		return nil
	}
	return fmt.Errorf("still on login page")
}

// waitOutLoginCaptcha clicks I'm-not-a-robot, waits for Google to verify, and
// re-aims/retries if the box is still up (missed click, cursor moved, etc.).
// Gaps stay long — spam re-clicks cause "Verification failed".
func waitOutLoginCaptcha(ctx context.Context, email string) error {
	fmt.Println("reCAPTCHA on the login form — OS click on I'm-not-a-robot (will re-aim if we miss)…")
	_ = os.Stdout.Sync()
	waitFor := 90 * time.Second
	clickEvery := 8 * time.Second
	maxClicks := 5
	if headlessChrome {
		waitFor = 10 * time.Second
		maxClicks = 1
	} else if quietChromeLogin {
		waitFor = 40 * time.Second
		maxClicks = 3
	}
	deadline := time.Now().Add(waitFor)
	lastClick := time.Time{}
	clicks := 0
	tryClick := func(n int, why string) {
		raiseLiveChromeWindow(liveChromeAccount)
		time.Sleep(350 * time.Millisecond) // let the window settle after raise / mouse jostle
		lastRecaptchaOSClick = time.Time{}  // allow intentional re-aim after a miss
		if err := clickRecaptchaCheckboxIfPresent(ctx); err != nil {
			fmt.Printf("reCAPTCHA click #%d (%s): %v\n", n, why, err)
		} else {
			fmt.Printf("reCAPTCHA click #%d (%s) — waiting for Google…\n", n, why)
		}
		_ = os.Stdout.Sync()
	}
	// First click immediately.
	if !headlessChrome {
		clicks++
		lastClick = time.Now()
		tryClick(clicks, "first")
	}
	for time.Now().Before(deadline) {
		var href string
		_ = chromedp.Location(&href).Do(ctx)
		if loginFillDone(href, cookieOK(ctx)) {
			fmt.Println("Captcha cleared — logged in.")
			return nil
		}
		// Tumblr often shows "incorrect email or password" after a failed /
		// abandoned captcha. Bubble out so finishChromeLoginWait can retry fill
		// (do not nest another captcha wait / discard here).
		if evalBoolJS(ctx, tumblrLoginErrorJS) && !evalBoolJS(ctx, tumblrCaptchaVisibleJS) {
			fmt.Println("Login error banner after captcha — will retry fill (not discarding).")
			_ = os.Stdout.Sync()
			return fmt.Errorf("captcha/login blocked — skipped")
		}
		if evalBoolJS(ctx, tumblrPasswordChangeRequiredJS) {
			return passwordChangeRequiredError{Email: email, ID: accountSlug(email)}
		}
		failed := evalBoolJS(ctx, tumblrRecaptchaFailedJS)
		stillUp := evalBoolJS(ctx, tumblrCaptchaVisibleJS)
		if !stillUp {
			// Checkbox accepted / challenge gone — try Log in once.
			_ = clickTumblrLoginButton(ctx)
			time.Sleep(900 * time.Millisecond)
			_ = chromedp.Location(&href).Do(ctx)
			if loginFillDone(href, cookieOK(ctx)) {
				fmt.Println("Captcha cleared — logged in.")
				return nil
			}
		} else if !headlessChrome && clicks < maxClicks && time.Since(lastClick) >= clickEvery {
			why := "still up — re-aim"
			if failed {
				why = "verification failed — retry"
				fmt.Println("reCAPTCHA said verification failed — slow re-aim…")
				_ = os.Stdout.Sync()
			} else {
				fmt.Println("reCAPTCHA still up (missed or cursor moved) — raising window and clicking again…")
				_ = os.Stdout.Sync()
			}
			clicks++
			lastClick = time.Now()
			tryClick(clicks, why)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if headlessChrome {
		return fmt.Errorf("headless captcha — skipped")
	}
	fmt.Printf("Captcha still up after %d careful click(s) / %s — skipping this account.\n", clicks, waitFor)
	_ = os.Stdout.Sync()
	return fmt.Errorf("captcha not cleared — skipped")
}

var lastRecaptchaOSClick time.Time

func clickRecaptchaCheckboxIfPresent(ctx context.Context) error {
	// Physical mouse is allowed ONLY while the I'm-not-a-robot UI is on screen.
	if !evalBoolJS(ctx, tumblrCaptchaVisibleJS) {
		return fmt.Errorf("no recaptcha on screen")
	}
	var box map[string]any
	if err := chromedp.Evaluate(tumblrRecaptchaAnchorBoxJS, &box).Do(ctx); err != nil {
		return err
	}
	return clickRecaptchaBox(ctx, box)
}

// clickRecaptchaBox does ONE physical click on the checkbox only.
// Clicking the text too / re-clicking while Google verifies → "Verification failed".
func clickRecaptchaBox(ctx context.Context, box map[string]any) error {
	// Global cool-down so session-wait nudge + login wait don't double-click.
	// Intentional re-aims clear lastRecaptchaOSClick before calling us.
	if !lastRecaptchaOSClick.IsZero() && time.Since(lastRecaptchaOSClick) < 7*time.Second {
		return fmt.Errorf("reCAPTCHA cool-down (avoid verification failed)")
	}
	points := recaptchaHitPoints(box)
	if len(points) == 0 {
		return fmt.Errorf("no recaptcha checkbox")
	}
	x, y := points[0][0], points[0][1]
	vw, _ := box["vw"].(float64)
	vh, _ := box["vh"].(float64)
	fmt.Println("Clicking reCAPTCHA checkbox (single OS click)…")
	_ = os.Stdout.Sync()
	raiseLiveChromeWindow(liveChromeAccount)
	time.Sleep(200 * time.Millisecond)
	withOSRecaptchaClick(func() {
		// Calibrate CSS→screen via render-widget client size vs page viewport.
		clickChromePageCSSForcedScaled(x, y, vw, vh)
	})
	lastRecaptchaOSClick = time.Now()
	return nil
}

func recaptchaHitPoints(box map[string]any) [][2]float64 {
	if box == nil {
		return nil
	}
	var boxHits, other [][2]float64
	if raw, ok := box["hits"].([]any); ok {
		for _, item := range raw {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			x, _ := m["cx"].(float64)
			y, _ := m["cy"].(float64)
			if x <= 1 || y <= 1 {
				continue
			}
			kind, _ := m["kind"].(string)
			pt := [2]float64{x, y}
			// Prefer the actual checkbox, never spray text/label hits.
			if kind == "box" || kind == "box-near-label" || kind == "above-prompt-box" {
				boxHits = append(boxHits, pt)
			} else {
				other = append(other, pt)
			}
		}
	}
	if len(boxHits) > 0 {
		return boxHits[:1]
	}
	if len(other) > 0 {
		return other[:1]
	}
	x, _ := box["cx"].(float64)
	y, _ := box["cy"].(float64)
	if x < 1 {
		x, _ = box["x"].(float64)
	}
	if y < 1 {
		y, _ = box["y"].(float64)
	}
	if x > 1 && y > 1 {
		return [][2]float64{{x, y}}
	}
	return nil
}

// clickRecaptchaOnTarget clicks the checkbox via a page target (session wait path).
// Does NOT click Log in here — wait for Google to verify first (see waitOutLoginCaptcha).
func clickRecaptchaOnTarget(t chromeJSONTarget) error {
	var captcha bool
	_ = evalJSONOnChromeTarget(t, tumblrCaptchaVisibleJS, &captcha)
	if !captcha {
		return fmt.Errorf("no recaptcha on screen")
	}
	var box map[string]any
	if err := evalJSONOnChromeTarget(t, tumblrRecaptchaAnchorBoxJS, &box); err != nil {
		return err
	}
	return clickRecaptchaBox(nil, box)
}

func recoverCannotPostLogin(ctx context.Context, creds Credentials, retry bool) error {
	if !retry {
		return fmt.Errorf("Tumblr login page rejected a raw POST /login")
	}
	fmt.Println("Tumblr blocked a raw POST /login — opening the login page again…")
	if err := chromedp.Navigate("https://www.tumblr.com/login").Do(ctx); err != nil {
		return err
	}
	if err := chromedp.WaitReady("body", chromedp.ByQuery).Do(ctx); err != nil {
		return err
	}
	time.Sleep(1500 * time.Millisecond)
	return fillTumblrLoginFormAttempt(ctx, creds, false)
}

func recoverLiveDebugPort() bool {
	if strings.TrimSpace(liveDebugPort) != "" && portAlive(liveDebugPort) {
		return true
	}
	if liveChromeAccount != "" {
		if dir, err := chromeDirForAccount(liveChromeAccount); err == nil {
			if p := readDevToolsPort(dir); p != "" {
				if liveDebugPort != p {
					fmt.Println("Chrome debug port moved to", p)
				}
				liveDebugPort = p
				return true
			}
		}
	}
	return attachAccountDebugPort()
}

var notedLoginCaptcha bool

func waitForTumblrSession(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	misses := 0
	notedLoginCaptcha = false
	var lastFillNudge time.Time
	var firstMiss time.Time
	// Chrome must look gone for a while, not just for a few polls. A captcha
	// can stall the debug port long enough to fake a shutdown, and giving up
	// here kills a window the user is part-way through.
	chromeGone := func() bool {
		return misses >= 5 && !firstMiss.IsZero() && time.Since(firstMiss) > 30*time.Second
	}
	for time.Now().Before(deadline) {
		if !recoverLiveDebugPort() {
			misses++
			if firstMiss.IsZero() {
				firstMiss = time.Now()
			}
			if chromeGone() {
				return fmt.Errorf("chrome closed")
			}
			time.Sleep(1 * time.Second)
			continue
		}
		misses = 0
		firstMiss = time.Time{}
		if err := errIfChromeTerminated(); err != nil {
			return err
		}
		if pageShowsPasswordChangeRequired() {
			email, id := "", liveChromeAccount
			if acc, ok := currentAccount(); ok {
				email, id = acc.Email, acc.ID
			}
			return passwordChangeRequiredError{Email: email, ID: id}
		}
		if hit := clickPrivacyConsentAgree(); hit != "" {
			fmt.Println("Dismissed cookies:", hit)
		}
		nudgeEvery := 25 * time.Second
		if skipSetupAgeKey {
			// Setup: nudge often for empty forms, but captcha path self-throttles clicks.
			nudgeEvery = 8 * time.Second
		}
		if lastFillNudge.IsZero() || time.Since(lastFillNudge) > nudgeEvery {
			lastFillNudge = time.Now()
			nudgeTumblrLoginIfStuck()
		}
		if chromeLooksLoggedIn() {
			if list, err := listChromeTargets(); err == nil {
				if t := preferredChromePageTarget(list); strings.TrimSpace(t.URL) != "" {
					fmt.Println("Chrome is already signed in at", t.URL, "— continuing.")
					return nil
				}
			}
			fmt.Println("Chrome is already on the dashboard — continuing.")
			return nil
		}
		cookies, err := cookiesFromDebugPort()
		if err == nil && cookieValue(cookies, "sid") != "" && tumblrSessionPageOK() {
			return nil
		}
		if err == nil && cookieValue(cookies, "sid") != "" && onTumblrConsentPage() {
			time.Sleep(400 * time.Millisecond)
			continue
		}
		if err != nil && !recoverLiveDebugPort() {
			misses++
			if firstMiss.IsZero() {
				firstMiss = time.Now()
			}
			if chromeGone() {
				return fmt.Errorf("chrome closed: %w", err)
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timed out waiting for sid cookie")
}

func nudgeTumblrLoginIfStuck() {
	if quietChromeLogin || !onTumblrLoginPage() {
		return
	}
	list, err := listChromeTargets()
	if err != nil {
		return
	}
	t := preferredChromePageTarget(list)
	if t.ID == "" || !strings.Contains(strings.ToLower(t.URL), "/login") {
		return
	}
	var captcha, needFill bool
	_ = evalJSONOnChromeTarget(t, tumblrCaptchaVisibleJS, &captcha)
	if captcha {
		if !notedLoginCaptcha {
			notedLoginCaptcha = true
			fmt.Println("Captcha is showing — auto-clicking I'm-not-a-robot (OS mouse)…")
		}
		_ = clickRecaptchaOnTarget(t)
		return
	}
	_ = evalJSONOnChromeTarget(t, tumblrLoginNeedsRefillJS, &needFill)
	if !needFill {
		return
	}
	creds, err := loadCredentials()
	if err != nil || strings.TrimSpace(creds.Email) == "" {
		return
	}
	fmt.Println("Still on login with an empty form — filling it again…")
	_ = autoFillTumblrLogin(creds)
}

var chromeDebugHTTPClient = &http.Client{Timeout: 3 * time.Second}

func waitForDebugPort(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	url := debugEndpoint() + "/json/version"
	for time.Now().Before(deadline) {
		resp, err := chromeDebugHTTPClient.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("chrome debug port %s not ready", strings.TrimPrefix(debugEndpoint(), "http://127.0.0.1:"))
}

func readDevToolsPort(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "DevToolsActivePort"))
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
	if line == "" || !portInUse(line) {
		return ""
	}
	return line
}

// cookiesFromDebugPortTimed caps CDP/chromedp hangs so login/setup cannot stall forever.
func cookiesFromDebugPortTimed(d time.Duration) ([]*network.Cookie, error) {
	if d < 500*time.Millisecond {
		d = 500 * time.Millisecond
	}
	type res struct {
		c   []*network.Cookie
		err error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := cookiesFromDebugPort()
		ch <- res{c, err}
	}()
	select {
	case r := <-ch:
		return r.c, r.err
	case <-time.After(d):
		return nil, fmt.Errorf("timed out after %s", d)
	}
}

func cookiesFromDebugPort() ([]*network.Cookie, error) {
	list, err := listChromeTargets()
	if err != nil {
		return nil, err
	}
	t := preferredChromePageTarget(list)
	if t.ID == "" {
		return nil, fmt.Errorf("no chrome page")
	}
	var cookies []*network.Cookie
	cdpErr := withChromeTargetCDPFor(t, 8*time.Second, func(call cdpCall) error {
		_, _ = call("Network.enable", nil)
		raw, err := call("Network.getCookies", map[string]any{
			"urls": []string{"https://www.tumblr.com/", "https://tumblr.com/"},
		})
		if err != nil {
			raw, err = call("Storage.getCookies", map[string]any{})
			if err != nil {
				return err
			}
		}
		var wrap struct {
			Cookies []*network.Cookie `json:"cookies"`
		}
		if err := json.Unmarshal(raw, &wrap); err != nil {
			return err
		}
		if len(wrap.Cookies) == 0 {
			if extra, err2 := call("Storage.getCookies", map[string]any{}); err2 == nil {
				_ = json.Unmarshal(extra, &wrap)
			}
		}
		cookies = wrap.Cookies
		return nil
	})
	if cdpErr == nil {
		return cookies, nil
	}
	ctx, cancel, err := withBotChrome(20 * time.Second)
	if err != nil {
		return nil, cdpErr
	}
	defer cancel()
	err = chromedp.Run(ctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			_ = network.Enable().Do(ctx)
			var err error
			cookies, err = network.GetCookies().WithURLs([]string{
				"https://www.tumblr.com",
				"https://tumblr.com",
			}).Do(ctx)
			return err
		}),
	)
	if err != nil {
		return nil, err
	}
	return cookies, nil
}

type chromeJSONTarget struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	Title                string `json:"title"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func chromeTargetWSURL(t chromeJSONTarget) string {
	if u := strings.TrimSpace(t.WebSocketDebuggerURL); u != "" {
		return u
	}
	if t.ID == "" {
		return ""
	}
	return strings.Replace(debugEndpoint(), "http://", "ws://", 1) + "/devtools/page/" + t.ID
}

// evalJSONOnChromeTarget runs JS in a Chrome target and unmarshals the value.
// It only closes the websocket, so Chrome and the tab stay open.
func evalJSONOnChromeTarget(t chromeJSONTarget, expression string, out any) error {
	return evalJSONOnChromeTargetOpts(t, 12*time.Second, false, expression, out)
}

func evalPromiseOnChromeTarget(t chromeJSONTarget, expression string, out any) error {
	return evalJSONOnChromeTargetOpts(t, 25*time.Second, true, expression, out)
}

func evalJSONOnChromeTargetOpts(t chromeJSONTarget, d time.Duration, await bool, expression string, out any) error {
	return withChromeTargetCDPFor(t, d, func(call cdpCall) error {
		raw, err := call("Runtime.evaluate", map[string]any{
			"expression":    expression,
			"returnByValue": true,
			"awaitPromise":  await,
			"userGesture":   true,
		})
		if err != nil {
			return err
		}
		var wrap struct {
			Result struct {
				Value json.RawMessage `json:"value"`
			} `json:"result"`
			ExceptionDetails json.RawMessage `json:"exceptionDetails"`
		}
		if err := json.Unmarshal(raw, &wrap); err != nil {
			return err
		}
		if len(wrap.ExceptionDetails) > 0 && string(wrap.ExceptionDetails) != "null" {
			return fmt.Errorf("js: %s", string(wrap.ExceptionDetails))
		}
		if out == nil || len(wrap.Result.Value) == 0 || string(wrap.Result.Value) == "null" {
			return nil
		}
		return json.Unmarshal(wrap.Result.Value, out)
	})
}

type cdpCall func(method string, params any) (json.RawMessage, error)

func withChromeTargetCDP(t chromeJSONTarget, fn func(cdpCall) error) error {
	return withChromeTargetCDPFor(t, 12*time.Second, fn)
}

func withChromeTargetCDPFor(t chromeJSONTarget, d time.Duration, fn func(cdpCall) error) error {
	wsURL := chromeTargetWSURL(t)
	if wsURL == "" {
		return fmt.Errorf("no debugger url")
	}
	if d < 12*time.Second {
		d = 12 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	conn, br, _, err := ws.Dial(ctx, wsURL)
	if err != nil {
		return err
	}
	defer conn.Close()
	var rw io.ReadWriter = conn
	if br != nil && br.Buffered() > 0 {
		rw = struct {
			io.Reader
			io.Writer
		}{io.MultiReader(br, conn), conn}
	}
	next := 0
	call := func(method string, params any) (json.RawMessage, error) {
		next++
		id := next
		payload := map[string]any{"id": id, "method": method}
		if params != nil {
			payload["params"] = params
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		if err := wsutil.WriteClientText(rw, raw); err != nil {
			return nil, err
		}
		deadline := time.Now().Add(d)
		for time.Now().Before(deadline) {
			remain := time.Until(deadline)
			if remain > 8*time.Second {
				remain = 8 * time.Second
			}
			if remain < 500*time.Millisecond {
				remain = 500 * time.Millisecond
			}
			_ = conn.SetReadDeadline(time.Now().Add(remain))
			data, err := wsutil.ReadServerText(rw)
			if err != nil {
				if time.Now().After(deadline) {
					return nil, err
				}
				continue
			}
			var msg struct {
				ID     int             `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			if msg.Method == "Page.fileChooserOpened" {
				var p struct {
					BackendNodeID int `json:"backendNodeId"`
				}
				if json.Unmarshal(msg.Params, &p) == nil && p.BackendNodeID != 0 {
					fileChooserMu.Lock()
					lastFileChooserBackend = p.BackendNodeID
					fileChooserMu.Unlock()
				}
				continue
			}
			if msg.ID != id {
				continue
			}
			if len(msg.Error) > 0 && string(msg.Error) != "null" {
				return nil, fmt.Errorf("cdp %s: %s", method, string(msg.Error))
			}
			return msg.Result, nil
		}
		return nil, fmt.Errorf("cdp %s timeout", method)
	}
	return fn(call)
}

func mouseClickOnChromeTarget(t chromeJSONTarget, x, y float64) error {
	return withChromeTargetCDP(t, func(call cdpCall) error {
		_, _ = call("Page.bringToFront", nil)
		ev := func(typ string, buttons int) error {
			_, err := call("Input.dispatchMouseEvent", map[string]any{
				"type":        typ,
				"x":           x,
				"y":           y,
				"button":      "left",
				"buttons":     buttons,
				"clickCount":  1,
				"pointerType": "mouse",
			})
			return err
		}
		if err := ev("mouseMoved", 0); err != nil {
			return err
		}
		if err := ev("mousePressed", 1); err != nil {
			return err
		}
		return ev("mouseReleased", 0)
	})
}

func listChromeTargets() ([]chromeJSONTarget, error) {
	resp, err := chromeDebugHTTPClient.Get(debugEndpoint() + "/json/list")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var list []chromeJSONTarget
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	return list, nil
}

func existingPageTargetID() (target.ID, error) {
	list, err := listChromeTargets()
	if err != nil {
		return "", err
	}
	t := preferredChromePageTarget(list)
	if t.ID == "" {
		return "", fmt.Errorf("no chrome page target")
	}
	return target.ID(t.ID), nil
}

func preferredChromePageTarget(list []chromeJSONTarget) chromeJSONTarget {
	var login, settings, tumblr, fallback chromeJSONTarget
	for _, t := range list {
		if t.Type != "page" || t.ID == "" {
			continue
		}
		u := strings.ToLower(t.URL)
		if strings.HasPrefix(u, "chrome://") || strings.HasPrefix(u, "devtools://") {
			continue
		}
		if fallback.ID == "" {
			fallback = t
		}
		if !strings.Contains(u, "tumblr.com") {
			continue
		}
		if strings.Contains(u, "/login") {
			if login.ID == "" {
				login = t
			}
			continue
		}
		if strings.Contains(u, "/privacy/consent") {
			continue
		}
		if strings.Contains(u, "/settings") && settings.ID == "" {
			settings = t
		}
		if tumblr.ID == "" {
			tumblr = t
		}
	}
	switch {
	case settings.ID != "":
		return settings
	case tumblr.ID != "":
		return tumblr
	case login.ID != "":
		return login
	default:
		return fallback
	}
}

func waitForPageTarget(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := existingPageTargetID(); err == nil {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("chrome opened but no page tab appeared")
}

func closeExtraChromeTabs(keep target.ID) {
	list, err := listChromeTargets()
	if err != nil {
		return
	}
	keepID := string(keep)
	var pages int
	for _, t := range list {
		if t.Type == "page" && t.ID != "" {
			pages++
		}
	}
	if pages <= 1 {
		return
	}
	for _, t := range list {
		if t.Type != "page" || t.ID == "" || t.ID == keepID || t.ID == string(liveTargetID) {
			continue
		}
		if strings.Contains(strings.ToLower(t.URL), "k-id.com") {
			continue
		}
		if pages <= 1 {
			return
		}
		u := debugEndpoint() + "/json/close/" + t.ID
		resp, err := http.Get(u)
		if err == nil {
			resp.Body.Close()
		}
		pages--
	}
}

func clickUseAgeKeyOnKidTarget() string {
	list, err := listChromeTargets()
	if err != nil {
		return ""
	}
	for _, t := range kidOrAgeKeyTargets(list) {
		if strings.Contains(strings.ToLower(t.URL), "agekey.org") {
			continue
		}
		fmt.Println("AgeKey frame:", chromeTargetHost(t.URL))
		var box struct {
			Found bool    `json:"found"`
			Text  string  `json:"text"`
			X     float64 `json:"x"`
			Y     float64 `json:"y"`
		}
		if err := evalJSONOnChromeTarget(t, chromeUseAgeKeyHitJS, &box); err != nil {
			continue
		}
		if box.X > 1 && box.Y > 1 {
			_ = mouseClickOnChromeTarget(t, box.X, box.Y)
		}
		if box.Found && strings.TrimSpace(box.Text) != "" {
			return box.Text
		}
	}
	return ""
}

func chromeTargetHost(raw string) string {
	u := raw
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if i := strings.IndexAny(u, "/?"); i >= 0 {
		u = u[:i]
	}
	if u == "" {
		return "(unknown)"
	}
	return u
}

func ageKeyFrameRank(raw string) int {
	u := strings.ToLower(raw)
	switch {
	case strings.Contains(u, "auth.agekey.org"):
		return 0
	case strings.Contains(u, "agekey.org"), strings.Contains(u, "agekey.com"):
		return 1
	case strings.Contains(u, "k-id.com"):
		return 2
	default:
		return 3
	}
}

func kidOrAgeKeyTargets(list []chromeJSONTarget) []chromeJSONTarget {
	var out []chromeJSONTarget
	for _, t := range list {
		if t.Type != "page" && t.Type != "iframe" {
			continue
		}
		u := strings.ToLower(t.URL)
		if strings.Contains(u, "k-id.com") || strings.Contains(u, "agekey") {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return ageKeyFrameRank(out[i].URL) < ageKeyFrameRank(out[j].URL)
	})
	return out
}

func ageKeyOrgTargets(list []chromeJSONTarget) []chromeJSONTarget {
	var out []chromeJSONTarget
	for _, t := range kidOrAgeKeyTargets(list) {
		u := strings.ToLower(t.URL)
		if strings.Contains(u, "agekey.org") || strings.Contains(u, "agekey.com") {
			out = append(out, t)
		}
	}
	return out
}

func chromeHasTargetHost(substr string) bool {
	list, err := listChromeTargets()
	if err != nil {
		return false
	}
	want := strings.ToLower(substr)
	for _, t := range list {
		if strings.Contains(strings.ToLower(t.URL), want) {
			return true
		}
	}
	return false
}

func isConsentTargetURL(raw string) bool {
	u := strings.ToLower(raw)
	if u == "" {
		return false
	}
	if strings.Contains(u, "agekey") || strings.Contains(u, "k-id.com") || strings.Contains(u, "chrome-extension") {
		return false
	}
	needles := []string{
		"consent", "quantcast", "onetrust", "didomi", "sourcepoint",
		"privacy-mgmt", "privacymanager", "cookiebot", "gdpr",
		"euconsent", "/privacy", "cmp.", "qc-cmp",
	}
	for _, n := range needles {
		if strings.Contains(u, n) {
			return true
		}
	}
	return false
}

func consentTargets(list []chromeJSONTarget) []chromeJSONTarget {
	var out []chromeJSONTarget
	for _, t := range list {
		if t.Type != "page" && t.Type != "iframe" {
			continue
		}
		if isConsentTargetURL(t.URL) {
			out = append(out, t)
		}
	}
	return out
}

func clickCookieAcceptOnConsentTargets() string {
	if ageKeyHandsOff() {
		return ""
	}
	if hit := clickPrivacyConsentAgree(); hit != "" {
		return hit
	}
	list, err := listChromeTargets()
	if err != nil {
		return ""
	}
	for _, t := range consentTargets(list) {
		var hit string
		if err := evalJSONOnChromeTarget(t, chromeClickCookieJS, &hit); err != nil {
			continue
		}
		if hit = strings.TrimSpace(hit); hit != "" {
			return hit
		}
	}
	return ""
}

func onTumblrConsentPage() bool {
	return chromeHasTargetHost("/privacy/consent")
}

func terminatedErrForCurrent() error {
	id := liveChromeAccount
	if id == "" {
		if acc, ok := currentAccount(); ok {
			id = acc.ID
		}
	}
	markAccountTerminated(id)
	return terminatedAccountError{ID: id}
}

func onTumblrTerminatedPage() bool {
	list, err := listChromeTargets()
	if err != nil {
		return false
	}
	for _, t := range list {
		if t.Type != "page" {
			continue
		}
		if isTumblrTerminatedURL(t.URL) {
			fmt.Printf("Terminated page open: %s\n", t.URL)
			return true
		}
		if !deadTextCountsOn(t.URL) {
			continue
		}
		title := strings.ToLower(t.Title)
		if strings.Contains(title, "account terminated") || isTumblrDeadAccountText(t.Title) {
			fmt.Printf("Dead-account title on %s: %q\n", t.URL, t.Title)
			return true
		}
		var hit string
		if err := evalJSONOnChromeTarget(t, chromeDeadAccountJS, &hit); err == nil && hit != "" {
			fmt.Printf("Dead-account text on %s: %q\n", t.URL, hit)
			return true
		}
	}
	return false
}

const chromeDeadAccountJS = `(() => {
	const t = ((document.body && document.body.innerText) || '') + ' ' + (document.title || '');
	const m = /account has been terminated|you have deleted your account|this account (has been|was) deleted/i.exec(t);
	if (!m) return '';
	return t.slice(Math.max(0, m.index - 80), m.index + m[0].length + 80).replace(/\s+/g, ' ');
})()`

func freePhysicalMemoryMB() int {
	if runtime.GOOS != "windows" {
		return 99999
	}
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		"(Get-CimInstance Win32_OperatingSystem).FreePhysicalMemory").Output()
	if err != nil {
		return 99999
	}
	kb, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || kb <= 0 {
		return 99999
	}
	return kb / 1024
}

// waitForChromeMemoryHeadroom pauses before launch when free RAM is too low for
// a Tumblr tab (otherwise Chrome Aw-Snaps with Out of Memory / 0xe0000008).
func waitForChromeMemoryHeadroom(minFreeMB int, maxWait time.Duration) {
	if minFreeMB <= 0 {
		return
	}
	if maxWait < 5*time.Second {
		maxWait = 5 * time.Second
	}
	deadline := time.Now().Add(maxWait)
	warned := false
	for {
		free := freePhysicalMemoryMB()
		if free >= minFreeMB {
			if warned {
				fmt.Printf("Memory OK — %d MB free, launching Chrome.\n", free)
			}
			return
		}
		if !warned {
			fmt.Printf("Low RAM (%d MB free, need ~%d MB) — waiting so Chrome does not OOM. Close EA/game tabs if you can.\n", free, minFreeMB)
			warned = true
		}
		if time.Now().After(deadline) {
			fmt.Printf("Still only %d MB free after waiting — launching anyway.\n", free)
			return
		}
		time.Sleep(3 * time.Second)
	}
}

func chromeOutOfMemoryPage() bool {
	t, err := firstTumblrPageTarget()
	if err != nil || t.ID == "" {
		return false
	}
	var hit bool
	_ = evalJSONOnChromeTarget(t, `(() => {
		const b = ((document.body && document.body.innerText) || '') + ' ' + (document.title || '');
		return /aw,? snap|out of memory|STATUS_BREAKPOINT|something went wrong while displaying/i.test(b);
	})()`, &hit)
	return hit
}

func errIfChromeTerminated() error {
	if !onTumblrTerminatedPage() {
		return nil
	}
	return terminatedErrForCurrent()
}

func errIfTerminatedHref(href string) error {
	if !isTumblrTerminatedURL(href) {
		return nil
	}
	return terminatedErrForCurrent()
}

func errIfTerminatedPage(ctx context.Context) error {
	var href string
	_ = chromedp.Location(&href).Do(ctx)
	if err := errIfTerminatedHref(href); err != nil {
		return err
	}
	if !deadTextCountsOn(href) {
		return nil
	}
	var hit string
	_ = chromedp.Evaluate(chromeDeadAccountJS, &hit).Do(ctx)
	if hit != "" {
		fmt.Printf("Dead-account text on %s: %q\n", href, hit)
		return terminatedErrForCurrent()
	}
	return nil
}

func onTumblrLoginPage() bool {
	list, err := listChromeTargets()
	if err != nil {
		return false
	}
	for _, t := range list {
		if t.Type != "page" {
			continue
		}
		u := strings.ToLower(t.URL)
		if strings.Contains(u, "tumblr.com") && strings.Contains(u, "/login") {
			return true
		}
	}
	return false
}

func chromeURLLooksLoggedIn(raw string) bool {
	u := strings.ToLower(raw)
	if !strings.Contains(u, "tumblr.com") {
		return false
	}
	if strings.Contains(u, "/login") || strings.Contains(u, "/privacy/consent") {
		return false
	}
	return strings.Contains(u, "/dashboard") || strings.Contains(u, "/settings") ||
		strings.Contains(u, "/blog/") || strings.Contains(u, "/explore") ||
		strings.Contains(u, "/inbox") || strings.Contains(u, "/activity")
}

func chromeLooksLoggedIn() bool {
	list, err := listChromeTargets()
	if err != nil {
		return false
	}
	for _, t := range list {
		if t.Type != "page" {
			continue
		}
		if chromeURLLooksLoggedIn(t.URL) {
			return true
		}
	}
	return false
}

func tumblrSessionPageOK() bool {
	return !onTumblrConsentPage() && !onTumblrLoginPage()
}

func clickPrivacyConsentAgree() string {
	if ageKeyHandsOff() {
		return ""
	}
	if findWindowsSecurityHWND() != 0 {
		return ""
	}
	if hit := dismissConsentIAgree(); nagWasDismissed(hit) {
		return hit
	}
	list, err := listChromeTargets()
	if err != nil {
		return ""
	}
	type box struct {
		Found bool    `json:"found"`
		Text  string  `json:"text"`
		X     float64 `json:"x"`
		Y     float64 `json:"y"`
	}
	for _, t := range list {
		if t.Type != "page" && t.Type != "iframe" {
			continue
		}
		if !strings.Contains(strings.ToLower(t.URL), "/privacy/consent") {
			continue
		}
		var b box
		if err := evalJSONOnChromeTarget(t, chromeClickPrivacyConsentJS, &b); err != nil || !b.Found {
			continue
		}
		_ = mouseClickOnChromeTarget(t, b.X, b.Y)
		if txt := strings.TrimSpace(b.Text); txt != "" {
			return txt
		}
		return "I Agree!"
	}
	return ""
}

func cookieBannerOpen() bool {
	list, err := listChromeTargets()
	if err == nil && len(consentTargets(list)) > 0 {
		return true
	}
	var raw string
	if evalOnTumblrPage(chromeAgeStatusJS, &raw) != nil {
		return false
	}
	var st setupPageStatus
	if json.Unmarshal([]byte(raw), &st) != nil {
		return false
	}
	return st.CookieBanner
}

func waitForChromeTargetHost(substr string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if chromeHasTargetHost(substr) {
			return true
		}
		time.Sleep(350 * time.Millisecond)
	}
	return false
}

func tumblrPageTarget() (chromeJSONTarget, bool) {
	list, err := listChromeTargets()
	if err != nil {
		return chromeJSONTarget{}, false
	}
	t := preferredChromePageTarget(list)
	if t.ID == "" {
		return chromeJSONTarget{}, false
	}
	return t, true
}

func tumblrSettingsAccountTarget() (chromeJSONTarget, bool) {
	list, err := listChromeTargets()
	if err != nil {
		return chromeJSONTarget{}, false
	}
	for _, t := range list {
		if t.Type == "page" && strings.Contains(t.URL, "/settings/account") {
			return t, true
		}
	}
	return tumblrPageTarget()
}

func focusChromeTarget(t chromeJSONTarget) {
	if t.ID == "" {
		return
	}
	_ = withChromeTargetCDP(t, func(call cdpCall) error {
		_, err := call("Page.bringToFront", nil)
		return err
	})
}

func evalOnTumblrPage(expr string, out any) error {
	t, ok := tumblrPageTarget()
	if !ok {
		return fmt.Errorf("no tumblr page")
	}
	return evalJSONOnChromeTarget(t, expr, out)
}

func saveTargetShot(t chromeJSONTarget, name string) {
	var b64 string
	err := withChromeTargetCDP(t, func(call cdpCall) error {
		raw, err := call("Page.captureScreenshot", map[string]any{"format": "png"})
		if err != nil {
			return err
		}
		var wrap struct {
			Data string `json:"data"`
		}
		if err := json.Unmarshal(raw, &wrap); err != nil {
			return err
		}
		b64 = wrap.Data
		return nil
	})
	if err != nil || b64 == "" {
		return
	}
	buf, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return
	}
	_ = os.MkdirAll("scraped", 0755)
	path := filepath.Join("scraped", name)
	if err := os.WriteFile(path, buf, 0644); err != nil {
		return
	}
	fmt.Println("Screenshot:", path)
}

const chromeUseAgeKeyHitJS = `(() => {
	const vis = (el) => {
		if (!el || !el.getBoundingClientRect) return false;
		const s = window.getComputedStyle ? getComputedStyle(el) : {};
		const r = el.getBoundingClientRect();
		return s.display !== 'none' && s.visibility !== 'hidden' && r.width > 8 && r.height > 8;
	};
	const textOf = (el) => (el.innerText || el.textContent || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
	const isDialog = (t) => /verify your age/i.test(t) && /use your selfie/i.test(t) && /use your agekey/i.test(t);
	const isAgeKeyOnly = (t) => /use your agekey/i.test(t) && !/use your selfie/i.test(t) && !/use your id/i.test(t);
	const fire = (el) => {
		if (!el) return;
		el.scrollIntoView({block:'center', inline:'nearest'});
		try { el.focus(); } catch (e) {}
		const opts = {bubbles:true, cancelable:true, view:window, composed:true};
		try { el.dispatchEvent(new PointerEvent('pointerdown', Object.assign({pointerId:1, pointerType:'mouse', isPrimary:true}, opts))); } catch (e) {}
		try { el.dispatchEvent(new MouseEvent('mousedown', opts)); } catch (e) {}
		try { el.dispatchEvent(new PointerEvent('pointerup', Object.assign({pointerId:1, pointerType:'mouse', isPrimary:true}, opts))); } catch (e) {}
		try { el.dispatchEvent(new MouseEvent('mouseup', opts)); } catch (e) {}
		try { el.dispatchEvent(new MouseEvent('click', opts)); } catch (e) {}
		try { el.click(); } catch (e) {}
	};
	const cardOf = (el) => {
		let n = el;
		for (let i = 0; i < 10 && n && n !== document.body; i++, n = n.parentElement) {
			const t = textOf(n);
			if (isDialog(t)) continue;
			const r = n.getBoundingClientRect();
			if (r.width >= 200 && r.height >= 48 && r.height <= 260 && isAgeKeyOnly(t)) return n;
			if (n.matches && n.matches('button, a, [role="button"], [role="option"], [role="listitem"], li') && isAgeKeyOnly(t)) return n;
		}
		return el;
	};
	const acc = [];
	const visit = (root) => {
		if (!root || !root.querySelectorAll) return;
		for (const el of root.querySelectorAll('button, a, [role="button"], [role="option"], [role="listitem"], [tabindex], li, div, span, p, h1, h2, h3, h4, label')) {
			if (!vis(el)) continue;
			const t = textOf(el);
			if (!t || t.length > 240) continue;
			if (!isAgeKeyOnly(t)) continue;
			acc.push(el);
		}
		for (const el of root.querySelectorAll('*')) {
			if (el.shadowRoot) visit(el.shadowRoot);
		}
	};
	visit(document);
	acc.sort((a,b) => {
		const ra = a.getBoundingClientRect(), rb = b.getBoundingClientRect();
		const score = (el, r) => {
			let s = 0;
			const t = textOf(el);
			if (/^use your agekey/i.test(t)) s += 80;
			if (r.width >= 220 && r.height >= 56 && r.height <= 220) s += 60;
			if (el.matches && el.matches('button, a, [role="button"]')) s += 30;
			return s;
		};
		return score(b, rb) - score(a, ra) || (rb.width * rb.height) - (ra.width * ra.height);
	});
	if (acc[0]) {
		const el = cardOf(acc[0]);
		const r = el.getBoundingClientRect();
		fire(el);
		return {found: true, text: 'Use your AgeKey', x: r.left + r.width / 2, y: r.top + r.height / 2};
	}
	const dlg = [...document.querySelectorAll('[role="dialog"], [aria-modal="true"], div')]
		.filter(vis)
		.find(el => isDialog(textOf(el)) && el.getBoundingClientRect().height > 200);
	if (dlg) {
		const r = dlg.getBoundingClientRect();
		const x = r.left + r.width / 2;
		const y = r.top + r.height * 0.72;
		const topEl = document.elementFromPoint(x, y);
		if (topEl) fire(cardOf(topEl) || topEl);
		return {found: true, text: 'Use your AgeKey', x, y};
	}
	return {found: false, text: '', x: 0, y: 0};
})()`

func clickAgeKeyCardOnTumblrPage() string {
	list, err := listChromeTargets()
	if err != nil {
		return ""
	}
	for _, t := range list {
		if (t.Type != "page" && t.Type != "iframe") || t.ID == "" {
			continue
		}
		u := strings.ToLower(t.URL)
		if strings.Contains(u, "agekey.org") || strings.Contains(u, "agekey.com") {
			continue
		}
		if !strings.Contains(u, "tumblr.com") && !strings.Contains(u, "k-id.com") {
			continue
		}
		var b struct {
			Found bool    `json:"found"`
			Text  string  `json:"text"`
			X     float64 `json:"x"`
			Y     float64 `json:"y"`
		}
		if err := evalJSONOnChromeTarget(t, chromeUseAgeKeyHitJS, &b); err != nil || !b.Found || b.X < 1 || b.Y < 1 {
			continue
		}
		fmt.Println("Clicked:", b.Text)
		_ = mouseClickOnChromeTarget(t, b.X, b.Y)
		return b.Text
	}
	return ""
}

func clickHaveAgeKeyOnKidTarget() string {
	list, err := listChromeTargets()
	if err != nil {
		return ""
	}
	clickJS := `(() => {
		const vis = (el) => {
			if (!el || !el.getBoundingClientRect) return false;
			const r = el.getBoundingClientRect();
			return r.width > 2 && r.height > 2;
		};
		const textOf = (el) => (el.innerText || el.textContent || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
		const fire = (el) => {
			if (!el) return;
			el.scrollIntoView({block:'center', inline:'nearest'});
			try { el.focus(); } catch (e) {}
			const opts = {bubbles:true, cancelable:true, view:window, composed:true};
			try { el.click(); } catch (e) {}
			try { el.dispatchEvent(new MouseEvent('click', opts)); } catch (e) {}
		};
		const acc = [];
		const visit = (root) => {
			if (!root || !root.querySelectorAll) return;
			for (const el of root.querySelectorAll('a, button, [role="button"], span, div, p, label')) {
				if (!vis(el)) continue;
				const t = textOf(el);
				if (/already have an agekey/i.test(t) && t.length < 80 && !/create my agekey/i.test(t)) {
					acc.push({el, t});
				}
			}
			for (const el of root.querySelectorAll('*')) {
				if (el.shadowRoot) visit(el.shadowRoot);
			}
		};
		visit(document);
		acc.sort((a,b) => a.t.length - b.t.length);
		if (!acc[0]) {
			return {t:'', x:0, y:0, body: ((document.body && document.body.innerText) || '').replace(/\s+/g,' ').slice(0, 160)};
		}
		const el = acc[0].el.closest('a, button, [role="button"]') || acc[0].el;
		const r = el.getBoundingClientRect();
		fire(el);
		return {t: acc[0].t, x: r.left + r.width/2, y: r.top + r.height/2, body:''};
	})()`
	for _, t := range ageKeyOrgTargets(list) {
		fmt.Println("AgeKey frame:", chromeTargetHost(t.URL))
		var box struct {
			T    string  `json:"t"`
			X    float64 `json:"x"`
			Y    float64 `json:"y"`
			Body string  `json:"body"`
		}
		if err := evalJSONOnChromeTarget(t, clickJS, &box); err != nil {
			continue
		}
		if box.T != "" {
			if box.X > 1 && box.Y > 1 {
				_ = mouseClickOnChromeTarget(t, box.X, box.Y)
			}
			time.Sleep(700 * time.Millisecond)
			if stillOnAgeKeyCreatePage(t) {
				clickAgeKeyViewport(t, 0.78, 0.86, 0.92)
			}
			return box.T
		}
		if box.Body != "" && strings.Contains(strings.ToLower(box.Body), "already have") {
			fmt.Println("AgeKey page has the link but click missed.")
			clickAgeKeyViewport(t, 0.78, 0.86, 0.92)
			return "Already have an AgeKey?"
		}
	}
	return ""
}

func stillOnAgeKeyCreatePage(t chromeJSONTarget) bool {
	var body string
	_ = evalJSONOnChromeTarget(t, `((document.body && document.body.innerText) || '')`, &body)
	b := strings.ToLower(body)
	return strings.Contains(b, "create my agekey") && !regexp.MustCompile(`\d{1,2}[\/\-.]\d{1,2}[\/\-.]\d{2,4}`).MatchString(b)
}

func clickAgeKeyViewport(t chromeJSONTarget, fracs ...float64) {
	var size struct {
		W float64 `json:"w"`
		H float64 `json:"h"`
	}
	_ = evalJSONOnChromeTarget(t, `({w: window.innerWidth, h: window.innerHeight})`, &size)
	if size.W < 80 || size.H < 80 {
		return
	}
	x := size.W * 0.5
	for _, frac := range fracs {
		_ = mouseClickOnChromeTarget(t, x, size.H*frac)
		time.Sleep(350 * time.Millisecond)
	}
}

func ageKeyDateNeedles(now time.Time) []string {
	seen := map[string]bool{}
	var out []string
	for i := 0; i <= 14; i++ {
		for _, n := range ageKeyDateNeedlesOne(now.AddDate(0, 0, -i)) {
			if n == "" || seen[n] {
				continue
			}
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

var listedAgeKeyDateRe = regexp.MustCompile(`(?i)(?:(\d{1,2})[/.\-](\d{1,2})[/.\-](\d{4})|(\d{4})-(\d{2})-(\d{2}))`)

func parseAgeKeyDates(s string) []time.Time {
	matches := listedAgeKeyDateRe.FindAllStringSubmatch(s, -1)
	out := make([]time.Time, 0, len(matches))
	for _, m := range matches {
		var day time.Time
		if m[4] != "" {
			d, err := time.Parse("2006-01-02", m[4]+"-"+m[5]+"-"+m[6])
			if err != nil {
				continue
			}
			day = d
		} else {
			a, _ := strconv.Atoi(m[1])
			b, _ := strconv.Atoi(m[2])
			y, _ := strconv.Atoi(m[3])
			dom, month := a, b
			if a > 12 {
				dom, month = a, b
			} else if b > 12 {
				dom, month = b, a
			}
			if month < 1 || month > 12 || dom < 1 || dom > 31 || y < 2000 {
				continue
			}
			day = time.Date(y, time.Month(month), dom, 0, 0, 0, 0, time.UTC)
		}
		out = append(out, day)
	}
	return out
}

func preferredListedAgeKeyText(labels []string) string {
	best := ""
	var bestDay time.Time
	for _, label := range labels {
		t := strings.TrimSpace(label)
		low := strings.ToLower(t)
		if t == "" || strings.Contains(low, "create my agekey") || strings.Contains(low, "already have") ||
			strings.Contains(low, "iphone") || strings.Contains(low, "security key") || strings.Contains(low, "cancel") {
			continue
		}
		days := parseAgeKeyDates(t)
		if len(days) != 1 {
			continue
		}
		if best == "" || days[0].After(bestDay) {
			best = t
			bestDay = days[0]
		}
	}
	return best
}

func ageKeyDateNeedlesOne(now time.Time) []string {
	d, m, y := now.Day(), int(now.Month()), now.Year()
	return []string{
		fmt.Sprintf("%02d/%02d/%04d", d, m, y),
		fmt.Sprintf("%d/%02d/%04d", d, m, y),
		fmt.Sprintf("%02d/%d/%04d", d, m, y),
		fmt.Sprintf("%d/%d/%04d", d, m, y),
		fmt.Sprintf("%02d-%02d-%04d", d, m, y),
		fmt.Sprintf("%04d-%02d-%02d", y, m, d),
		now.Format("2 January 2006"),
		now.Format("02 January 2006"),
		now.Format("2 Jan 2006"),
		now.Format("02 Jan 2006"),
		now.Format("Jan 2, 2006"),
		now.Format("January 2, 2006"),
		now.Format("2 January, 2006"),
	}
}

func chromePickListedAgeKeyJS(now time.Time) string {
	needles, _ := json.Marshal(ageKeyDateNeedles(now))
	return fmt.Sprintf(`(() => {
		const vis = (el) => {
			if (!el || !el.getBoundingClientRect) return false;
			const r = el.getBoundingClientRect();
			return r.width > 2 && r.height > 2;
		};
		const textOf = (el) => (el.innerText || el.textContent || el.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim();
		const needles = %s;
		const fire = (el) => {
			if (!el) return;
			el.scrollIntoView({block:'center', inline:'nearest'});
			try { el.focus(); } catch (e) {}
			const opts = {bubbles:true, cancelable:true, view:window, composed:true};
			try { el.dispatchEvent(new PointerEvent('pointerdown', Object.assign({pointerId:1, pointerType:'mouse', isPrimary:true}, opts))); } catch (e) {}
			try { el.dispatchEvent(new MouseEvent('mousedown', opts)); } catch (e) {}
			try { el.dispatchEvent(new PointerEvent('pointerup', Object.assign({pointerId:1, pointerType:'mouse', isPrimary:true}, opts))); } catch (e) {}
			try { el.dispatchEvent(new MouseEvent('mouseup', opts)); } catch (e) {}
			try { el.dispatchEvent(new MouseEvent('click', opts)); } catch (e) {}
			try { el.click(); } catch (e) {}
		};
		const parseAllDates = (t) => {
			const out = [];
			const re = /(\d{1,2})[\/\-.](\d{1,2})[\/\-.](\d{4})|(\d{4})-(\d{2})-(\d{2})/g;
			let m;
			while ((m = re.exec(t || ''))) {
				if (m[4]) {
					out.push({ts: new Date(Number(m[4]), Number(m[5]) - 1, Number(m[6])).getTime(), raw: m[0]});
					continue;
				}
				let a = Number(m[1]), b = Number(m[2]), y = Number(m[3]);
				let d = a, mo = b;
				if (a > 12) { d = a; mo = b; }
				else if (b > 12) { d = b; mo = a; }
				out.push({ts: new Date(y, mo - 1, d).getTime(), raw: m[0]});
			}
			return out;
		};
		const body = textOf(document.body);
		const present = needles.filter(n => n && body.toLowerCase().includes(String(n).toLowerCase()));
		present.sort((a,b) => {
			const ta = (parseAllDates(a)[0] || {}).ts || 0;
			const tb = (parseAllDates(b)[0] || {}).ts || 0;
			return tb - ta || b.length - a.length;
		});
		const want = present[0] || '';
		const rowOf = (el) => {
			let best = el;
			let n = el;
			for (let i = 0; i < 10 && n && n !== document.body; i++, n = n.parentElement) {
				const t = textOf(n);
				if (parseAllDates(t).length > 1) break;
				if (/create my agekey|already have an agekey|learn more|privacy|terms|go back/i.test(t)) break;
				const r = n.getBoundingClientRect();
				if (r.height > 140 || r.width > 800) break;
				if (n.matches && n.matches('button, a, label, [role="button"], [role="option"], [role="listitem"], [role="radio"], li, [tabindex]')) {
					return n;
				}
				if (r.width >= 48 && r.height >= 24 && r.height <= 110) best = n;
			}
			return best;
		};
		const findWant = (needle) => {
			if (!needle) return null;
			const low = String(needle).toLowerCase();
			const acc = [];
			const visit = (root) => {
				if (!root || !root.querySelectorAll) return;
				for (const el of root.querySelectorAll('button, a, [role="button"], [role="option"], [role="listitem"], [role="radio"], li, label, div, span, p, td')) {
					if (!vis(el)) continue;
					const t = textOf(el);
					if (!t || t.length > 160) continue;
					if (!t.toLowerCase().includes(low)) continue;
					if (parseAllDates(t).length !== 1) continue;
					const r = el.getBoundingClientRect();
					acc.push({el, t, area: r.width * r.height});
				}
				for (const el of root.querySelectorAll('*')) {
					if (el.shadowRoot) visit(el.shadowRoot);
				}
			};
			visit(document);
			acc.sort((a,b) => a.area - b.area || a.t.length - b.t.length);
			return acc[0] || null;
		};
		let hit = findWant(want);
		if (!hit) {
			const dates = parseAllDates(body);
			dates.sort((a,b) => b.ts - a.ts);
			if (dates[0]) hit = findWant(dates[0].raw);
		}
		if (!hit) {
			return {t:'', x:0, y:0, dates: present.slice(0, 8), body: body.slice(0, 280)};
		}
		const el = rowOf(hit.el);
		const r = el.getBoundingClientRect();
		fire(el);
		return {t: hit.t.slice(0, 80), x: r.left + r.width/2, y: r.top + r.height/2, dates: present.slice(0, 8), body:''};
	})()`, string(needles))
}

var lastListedAgeKeyAt time.Time
var lastAgeKeyListDump string

func clickListedAgeKeyOnKidTarget() string {
	if !lastListedAgeKeyAt.IsZero() && time.Since(lastListedAgeKeyAt) < 2*time.Second {
		return ""
	}
	list, err := listChromeTargets()
	if err != nil {
		return ""
	}
	js := chromePickListedAgeKeyJS(time.Now())
	targets := kidOrAgeKeyTargets(list)
	for _, t := range targets {
		var box struct {
			T     string   `json:"t"`
			X     float64  `json:"x"`
			Y     float64  `json:"y"`
			Dates []string `json:"dates"`
			Body  string   `json:"body"`
		}
		if err := evalJSONOnChromeTarget(t, js, &box); err != nil {
			continue
		}
		if strings.TrimSpace(box.T) == "" {
			if b := strings.TrimSpace(box.Body); b != "" && b != lastAgeKeyListDump {
				lastAgeKeyListDump = b
				fmt.Println("AgeKey list page:", chromeTargetHost(t.URL), b)
			}
			continue
		}
		want := preferredListedAgeKeyText(append(append([]string{}, box.Dates...), box.T))
		if want != "" {
			gotDays := parseAgeKeyDates(box.T)
			wantDays := parseAgeKeyDates(want)
			if len(gotDays) == 1 && len(wantDays) == 1 && gotDays[0].Before(wantDays[0]) {
				continue
			}
		}
		lastListedAgeKeyAt = time.Now()
		fmt.Println("AgeKey listed:", box.T)
		if box.X > 1 && box.Y > 1 {
			_ = mouseClickOnChromeTarget(t, box.X, box.Y)
		}
		return box.T
	}
	return ""
}

func recoverSetupLogin() {
	if nag := dismissChromeLeakDialog(); nagWasDismissed(nag) {
		fmt.Println("Dismissed:", nag)
	}
	if nag := dismissSavePasswordBubble(); nagWasDismissed(nag) {
		fmt.Println("Dismissed:", nag)
	}
	dismissOverlaysOnPort()
	if chromeLooksLoggedIn() {
		closeLeftoverLoginTabs()
		return
	}
	if onTumblrLoginPage() {
		fmt.Println("Still on Tumblr login — signing in again instead of opening a gated page.")
		_ = reloginIfOnLoginPage()
		return
	}
	closeLeftoverLoginTabs()
}

func closeLeftoverLoginTabs() {
	list, err := listChromeTargets()
	if err != nil {
		return
	}
	pref := preferredChromePageTarget(list)
	if pref.ID == "" || tumblrLoginURL(pref.URL) || !chromeURLLooksLoggedIn(pref.URL) {
		return
	}
	for _, t := range list {
		if t.Type != "page" || t.ID == "" || t.ID == pref.ID {
			continue
		}
		if !tumblrLoginURL(t.URL) {
			continue
		}
		fmt.Println("Closing leftover login tab.")
		closeChromeTarget(t)
	}
}

var lastReloginAt time.Time

func reloginIfOnLoginPage() bool {
	if chromeLooksLoggedIn() {
		closeLeftoverLoginTabs()
		return true
	}
	if !onTumblrLoginPage() {
		return false
	}
	if !lastReloginAt.IsZero() && time.Since(lastReloginAt) < 8*time.Second {
		return false
	}
	lastReloginAt = time.Now()
	creds, err := loadCredentials()
	if err != nil || strings.TrimSpace(creds.Email) == "" {
		return false
	}
	fmt.Println("Signing in again on the login screen.")
	if err := autoFillTumblrLogin(creds); err != nil {
		if isBadLogin(err) || isTerminated(err) {
			fmt.Println("Login:", err)
			return false
		}
		fmt.Println("Login form:", err)
	}
	if err := waitForTumblrSession(2 * time.Minute); err != nil {
		fmt.Println("Login wait:", err)
	}
	if chromeLooksLoggedIn() {
		closeLeftoverLoginTabs()
		return true
	}
	return !onTumblrLoginPage()
}

func loginFillDone(href string, hasSID bool) bool {
	if strings.TrimSpace(href) == "" || tumblrLoginURL(href) {
		return false
	}
	return chromeURLLooksLoggedIn(href) || hasSID
}

func tumblrLoginURL(raw string) bool {
	u := strings.ToLower(raw)
	return strings.Contains(u, "tumblr.com") && strings.Contains(u, "/login")
}

func looksLikeTumblrLoginPage(url, body string) bool {
	if tumblrLoginURL(url) {
		return true
	}
	b := strings.ToLower(body)
	return strings.Contains(b, "continue with google") && (strings.Contains(b, "log in") || strings.Contains(b, "sign up"))
}

func withBotChrome(timeout time.Duration) (context.Context, context.CancelFunc, error) {
	if err := attachLiveChrome(); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(liveChromeCtx, timeout)
	return ctx, cancel, nil
}

// liveChromeHold keeps chromedp WithTargetID contexts alive. Cancelling those
// contexts sends Target.closeTarget, which closes the tab the user is watching
// and exits Chrome when it was the last one.
var liveChromeHold []context.Context

func holdLiveChromeContext(ctx context.Context) {
	if ctx == nil {
		return
	}
	liveChromeHold = append(liveChromeHold, ctx)
	if len(liveChromeHold) > 32 {
		liveChromeHold = liveChromeHold[len(liveChromeHold)-16:]
	}
}

func attachLiveChrome() error {
	id, err := existingPageTargetID()
	if err != nil {
		if liveChromeCtx != nil && liveChromePort == liveDebugPort {
			return nil
		}
		return err
	}
	if liveChromeCtx != nil && liveChromePort == liveDebugPort && liveTargetID == id {
		return nil
	}
	allocCtx, cancelAlloc := chromedp.NewRemoteAllocator(context.Background(), debugEndpoint())
	ctx, cancelCtx := chromedp.NewContext(allocCtx, chromedp.WithTargetID(id))
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(context.Context) error { return nil })); err != nil {
		// Do not cancelAlloc or cancelCtx. Either one closes this tab.
		holdLiveChromeContext(ctx)
		_ = cancelAlloc
		_ = cancelCtx
		if liveChromeCtx != nil && liveChromePort == liveDebugPort {
			return nil
		}
		return err
	}
	holdLiveChromeContext(liveChromeCtx)
	liveChromeCtx = ctx
	liveChromePort = liveDebugPort
	liveTargetID = id
	liveChromeCancel = func() {
		// Leaving the chromedp context uncancelled keeps the tab open.
	}
	_ = cancelAlloc
	_ = cancelCtx
	return nil
}

func accountIDFromAuth(auth Auth) string {
	path := strings.TrimSpace(auth.path)
	if path != "" {
		dir := filepath.Base(filepath.Dir(filepath.Clean(path)))
		if dir != "" && dir != "." && dir != "config" && dir != "profiles" {
			return dir
		}
	}
	if acc, ok := currentAccount(); ok {
		return acc.ID
	}
	return ""
}

func activateAccountForAuth(auth *Auth) (restore func(), err error) {
	id := accountIDFromAuth(*auth)
	if id == "" {
		return nil, nil
	}
	accountMu.Lock()
	prev := activeAccountID
	accountMu.Unlock()
	if prev == id {
		return nil, nil
	}
	if err := setActiveAccount(id); err != nil {
		return nil, err
	}
	return func() {
		if prev != "" {
			_ = setActiveAccount(prev)
		}
	}, nil
}

func chromeDirForAccount(id string) (string, error) {
	dir := chromeDataRoot()
	if id != "" && usingIsolatedProfiles() {
		dir = filepath.Join(chromeProfilesDir(), id)
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return abs, os.MkdirAll(abs, 0755)
}

func detachLiveChrome() {
	stopLiveChromeProxy()
	holdLiveChromeContext(liveChromeCtx)
	liveChromeCtx = nil
	liveChromeCancel = nil
	liveChromePort = ""
	liveTargetID = ""
}

func closeBotChrome() {
	if liveChromeCtx != nil {
		// Short timeout — a dead CDP must not park the account batch.
		ctx, cancel := context.WithTimeout(liveChromeCtx, 800*time.Millisecond)
		_ = chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			return browser.Close().Do(ctx)
		}))
		cancel()
	}
	detachLiveChrome()
}

func markChromeCleanExit(dir string) {
	prefs := filepath.Join(dir, "Default", "Preferences")
	if data, err := os.ReadFile(prefs); err == nil {
		var m map[string]any
		if json.Unmarshal(data, &m) == nil {
			prof, _ := m["profile"].(map[string]any)
			if prof == nil {
				prof = map[string]any{}
				m["profile"] = prof
			}
			prof["exit_type"] = "Normal"
			prof["exited_cleanly"] = true
			prof["password_manager_leak_detection"] = false
			prof["password_manager_enabled"] = false
			m["credentials_enable_service"] = false
			m["credentials_enable_autosignin"] = false
			pm, _ := m["password_manager"].(map[string]any)
			if pm == nil {
				pm = map[string]any{}
				m["password_manager"] = pm
			}
			leak, _ := pm["leak_detection"].(map[string]any)
			if leak == nil {
				leak = map[string]any{}
				pm["leak_detection"] = leak
			}
			leak["enabled"] = false
			sess, _ := m["session"].(map[string]any)
			if sess == nil {
				sess = map[string]any{}
				m["session"] = sess
			}
			sess["restore_on_startup"] = 5
			if out, err := json.Marshal(m); err == nil {
				_ = os.WriteFile(prefs, out, 0600)
			}
		}
	}
	for _, rel := range []string{
		filepath.Join("Default", "Last Session"),
		filepath.Join("Default", "Last Tabs"),
		filepath.Join("Default", "Current Session"),
		filepath.Join("Default", "Current Tabs"),
	} {
		_ = os.Remove(filepath.Join(dir, rel))
	}
	sessDir := filepath.Join(dir, "Default", "Sessions")
	if entries, err := os.ReadDir(sessDir); err == nil {
		for _, e := range entries {
			_ = os.Remove(filepath.Join(sessDir, e.Name()))
		}
	}
}

var passwordNagWatchOnce sync.Once

func ensurePasswordNagWatcher() {
	passwordNagWatchOnce.Do(func() {
		go func() {
			var busy sync.Mutex
			for {
				time.Sleep(2 * time.Second)
				if liveDebugPort == "" || !portInUse(liveDebugPort) {
					continue
				}
				if findWindowsSecurityHWND() != 0 {
					continue
				}
				if !busy.TryLock() {
					continue
				}
				go func() {
					defer busy.Unlock()
					if nag := dismissChromeLeakDialog(); nagWasDismissed(nag) {
						fmt.Println("Dismissed:", nag)
						return
					}
					if nag := dismissNativePasswordNag(); nagWasDismissed(nag) {
						fmt.Println("Dismissed:", nag)
					}
				}()
			}
		}()
	})
}

// syncCookiesFromChrome reuses the chrome-data profile headlessly and
// refreshes auth.json from whatever session is stored there.
func syncCookiesFromChrome(auth *Auth) error {
	chromeLoginMu.Lock()
	defer chromeLoginMu.Unlock()
	return syncCookiesFromChromeLocked(auth)
}

// chromePortForCookieSync picks a debug port only when it belongs to this
// account. Never use another account's live Chrome (that clones sid across
// auth.json files and makes whoami report the same blog everywhere).
func chromePortForCookieSync(accountID string) (port string, ok bool) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return "", false
	}
	liveAcc := strings.TrimSpace(liveChromeAccount)
	if liveAcc != accountID {
		return "", false
	}
	port = strings.TrimSpace(liveDebugPort)
	if port == "" {
		port = debugPortForAccount(accountID)
	}
	if port == "" || !portInUse(port) {
		return "", false
	}
	// The live window must be this account's dedicated port (or the port
	// Chrome actually moved to for this same liveChromeAccount).
	want := debugPortForAccount(accountID)
	if want != "" && port != want {
		if dir, err := chromeDirForAccount(accountID); err == nil {
			if p := readDevToolsPort(dir); p == "" || p != port {
				return "", false
			}
		} else {
			return "", false
		}
	}
	return port, true
}

func syncCookiesFromChromeLocked(auth *Auth) error {
	id := accountIDFromAuth(*auth)
	if port, ok := chromePortForCookieSync(id); ok {
		prev := liveDebugPort
		liveDebugPort = port
		if err := waitForDebugPort(500 * time.Millisecond); err == nil {
			cookies, err := cookiesFromDebugPort()
			if err == nil && cookieValue(cookies, "sid") != "" {
				auth.Cookie = buildTumblrCookieHeader(cookies)
				persistMaturePref(auth)
				if err := saveAuth(*auth); err != nil {
					liveDebugPort = prev
					return err
				}
				fmt.Println("Synced cookies from this profile’s Chrome.")
				return nil
			}
		}
		liveDebugPort = prev
		if authHasSID(*auth) {
			return nil
		}
		return fmt.Errorf("chrome already open on port %s — close it or use that window", port)
	}

	dir, err := chromeDirForAccount(id)
	if err != nil {
		return err
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.UserDataDir(dir),
		chromedp.Flag("headless", "new"),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("mute-audio", true),
	)

	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, cancel = context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	var cookies []*network.Cookie
	err = chromedp.Run(ctx,
		chromedp.Navigate("https://www.tumblr.com/dashboard"),
		chromedp.Sleep(2*time.Second),
		chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			cookies, err = network.GetCookies().WithURLs([]string{
				"https://www.tumblr.com",
				"https://tumblr.com",
				"https://www.tumblr.com/dashboard",
			}).Do(ctx)
			return err
		}),
	)
	if err != nil {
		return fmt.Errorf("%w (close the bot Chrome window from -login, or just rely on saved cookies)", err)
	}

	cookieHeader := buildTumblrCookieHeader(cookies)
	if cookieHeader == "" || cookieValue(cookies, "sid") == "" {
		return fmt.Errorf("no Tumblr sid cookie found — need a Chrome login")
	}

	auth.Cookie = cookieHeader
	persistMaturePref(auth)
	if err := saveAuth(*auth); err != nil {
		return err
	}
	fmt.Println("Synced cookies from Chrome profile.")
	return nil
}

func authHasSID(auth Auth) bool {
	return strings.Contains(auth.Cookie, "sid=")
}

func cookieValue(cookies []*network.Cookie, name string) string {
	for _, c := range cookies {
		if c.Name == name && strings.Contains(c.Domain, "tumblr.com") {
			return c.Value
		}
	}
	for _, c := range cookies {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func buildTumblrCookieHeader(cookies []*network.Cookie) string {
	var parts []string
	seen := map[string]bool{}
	for _, c := range cookies {
		if c.Domain != "" && !strings.Contains(c.Domain, "tumblr.com") {
			continue
		}
		if c.Domain == "" && !likelyTumblrCookie(c.Name) {
			continue
		}
		if seen[c.Name] {
			continue
		}
		seen[c.Name] = true
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

func likelyTumblrCookie(name string) bool {
	switch name {
	case "sid", "logged_in", "pfu", "tmgioct", "tz", "cl_pref", "likes-displayMode", "redpop-editor-beta-toggled":
		return true
	default:
		return strings.HasPrefix(name, "euconsent")
	}
}
