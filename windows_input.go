package main

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// nativeChromeClicks lets login/setup dismiss Chrome UI. Reach must never
// move the mouse or send Enter to whatever window is in front.
var nativeChromeClicks bool

var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetClassNameW            = user32.NewProc("GetClassNameW")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procShowWindow               = user32.NewProc("ShowWindow")
	procBringWindowToTop         = user32.NewProc("BringWindowToTop")
	procSetFocus                 = user32.NewProc("SetFocus")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetCurrentThreadId       = syscall.NewLazyDLL("kernel32.dll").NewProc("GetCurrentThreadId")
	procAttachThreadInput        = user32.NewProc("AttachThreadInput")
	procKeybdEvent               = user32.NewProc("keybd_event")
	procGetWindowRect            = user32.NewProc("GetWindowRect")
	procGetCursorPos             = user32.NewProc("GetCursorPos")
	procClientToScreen           = user32.NewProc("ClientToScreen")
	procGetDpiForWindow          = user32.NewProc("GetDpiForWindow")
	procSetCursorPos             = user32.NewProc("SetCursorPos")
	procMouseEvent               = user32.NewProc("mouse_event")
	procGetGUIThreadInfo         = user32.NewProc("GetGUIThreadInfo")
	procSendMessageW             = user32.NewProc("SendMessageW")
	procPostMessageW             = user32.NewProc("PostMessageW")
)

type winInfo struct {
	hwnd  uintptr
	title string
	class string
}

var (
	enumWindowsMu  sync.Mutex
	enumWindowsCB  uintptr
	enumWindowsOut []winInfo
)

func enumWindowsProc(hwnd uintptr, _ uintptr) uintptr {
	vis, _, _ := procIsWindowVisible.Call(hwnd)
	if vis == 0 {
		return 1
	}
	title := windowText(hwnd)
	class := windowClass(hwnd)
	if title == "" && class == "" {
		return 1
	}
	enumWindowsOut = append(enumWindowsOut, winInfo{hwnd: hwnd, title: title, class: class})
	return 1
}

func listVisibleWindows() []winInfo {
	enumWindowsMu.Lock()
	defer enumWindowsMu.Unlock()
	if enumWindowsCB == 0 {
		enumWindowsCB = syscall.NewCallback(enumWindowsProc)
	}
	enumWindowsOut = enumWindowsOut[:0]
	procEnumWindows.Call(enumWindowsCB, 0)
	out := make([]winInfo, len(enumWindowsOut))
	copy(out, enumWindowsOut)
	return out
}

func windowText(hwnd uintptr) string {
	buf := make([]uint16, 512)
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), 512)
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

func windowClass(hwnd uintptr) string {
	buf := make([]uint16, 256)
	n, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), 256)
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

func findWindowContaining(titleSubstr string) uintptr {
	want := strings.ToLower(titleSubstr)
	for _, w := range listVisibleWindows() {
		if strings.Contains(strings.ToLower(w.title), want) {
			return w.hwnd
		}
	}
	return 0
}

// findMainWindowForPID finds the main Chrome_WidgetWin window for a browser pid.
func findMainWindowForPID(pid uint32) uintptr {
	if pid == 0 {
		return 0
	}
	bestHWND, bestTitled := uintptr(0), uintptr(0)
	bestArea, bestTitledArea := -1, -1
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var got uint32
		procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&got)))
		if got != pid {
			return 1
		}
		title := windowText(hwnd)
		class := windowClass(hwnd)
		if !strings.Contains(class, "Chrome_WidgetWin") && title == "" {
			return 1
		}
		var r struct{ left, top, right, bottom int32 }
		procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
		area := int(r.right-r.left) * int(r.bottom-r.top)
		if area < 200 {
			return 1
		}
		if area > bestArea {
			bestArea = area
			bestHWND = hwnd
		}
		if title != "" && area > bestTitledArea {
			bestTitledArea = area
			bestTitled = hwnd
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	if bestTitled != 0 {
		return bestTitled
	}
	return bestHWND
}

func findChromeWindowForAccount(accountID string) uintptr {
	accountID = accountSlug(accountID)
	if accountID != "" && liveChromeAccount == accountID && liveChromePID > 0 {
		return findMainWindowForPID(uint32(liveChromePID))
	}
	if liveChromePID > 0 {
		return findMainWindowForPID(uint32(liveChromePID))
	}
	return 0
}

func findWindowsSecurityHWND() uintptr {
	if hwnd := findWindowContaining("Windows Security"); hwnd != 0 {
		return hwnd
	}
	if hwnd := findWindowContaining("Choose a passkey"); hwnd != 0 {
		return hwnd
	}
	for _, w := range listVisibleWindows() {
		c := strings.ToLower(w.class)
		t := strings.ToLower(w.title)
		if strings.Contains(c, "credential") && (strings.Contains(t, "security") || strings.Contains(t, "passkey") || strings.Contains(c, "xaml")) {
			return w.hwnd
		}
	}
	return 0
}

func focusWindow(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	procShowWindow.Call(hwnd, 9) // SW_RESTORE
	fg, _, _ := procGetForegroundWindow.Call()
	fgTid, _, _ := procGetWindowThreadProcessId.Call(fg, 0)
	cur, _, _ := procGetCurrentThreadId.Call()
	if fg != 0 && fgTid != cur {
		procAttachThreadInput.Call(fgTid, cur, 1)
		defer procAttachThreadInput.Call(fgTid, cur, 0)
	}
	procBringWindowToTop.Call(hwnd)
	ok, _, _ := procSetForegroundWindow.Call(hwnd)
	procSetFocus.Call(hwnd)
	return ok != 0
}

const (
	keyeventfKeyup      = 0x0002
	vkReturn            = 0x0D
	vkEscape            = 0x1B
	vkSpace             = 0x20
	vkHome              = 0x24
	vkUp                = 0x26
	vkDown              = 0x28
	mouseeventfLeftDown = 0x0002
	mouseeventfLeftUp   = 0x0004
	wmChar              = 0x0102
	wmKeydown           = 0x0100
	wmKeyup             = 0x0101
	wmSetfocus          = 0x0007
)

type guiThreadInfo struct {
	Size          uint32
	Flags         uint32
	HwndActive    uintptr
	HwndFocus     uintptr
	HwndCapture   uintptr
	HwndMenuOwner uintptr
	HwndMoveSize  uintptr
	HwndCaret     uintptr
	CaretLeft     int32
	CaretTop      int32
	CaretRight    int32
	CaretBottom   int32
}

func focusedControl(hwnd uintptr) uintptr {
	tid, _, _ := procGetWindowThreadProcessId.Call(hwnd, 0)
	info := guiThreadInfo{Size: uint32(unsafe.Sizeof(guiThreadInfo{}))}
	ok, _, _ := procGetGUIThreadInfo.Call(tid, uintptr(unsafe.Pointer(&info)))
	if ok != 0 && info.HwndFocus != 0 {
		return info.HwndFocus
	}
	return hwnd
}

func attachToWindowThread(hwnd uintptr) func() {
	tid, _, _ := procGetWindowThreadProcessId.Call(hwnd, 0)
	cur, _, _ := procGetCurrentThreadId.Call()
	if tid == 0 || tid == cur {
		return func() {}
	}
	procAttachThreadInput.Call(cur, tid, 1)
	return func() { procAttachThreadInput.Call(cur, tid, 0) }
}

func clickScreen(x, y int) {
	procSetCursorPos.Call(uintptr(x), uintptr(y))
	time.Sleep(30 * time.Millisecond)
	procMouseEvent.Call(mouseeventfLeftDown, 0, 0, 0, 0)
	procMouseEvent.Call(mouseeventfLeftUp, 0, 0, 0, 0)
}

func clickScreenRestore(x, y int) {
	var pt struct{ X, Y int32 }
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	clickScreen(x, y)
	time.Sleep(40 * time.Millisecond)
	procSetCursorPos.Call(uintptr(pt.X), uintptr(pt.Y))
}

func clickChromePageCSS(cssX, cssY float64) {
	if !nativeChromeClicks || cssX < 1 || cssY < 1 {
		return
	}
	hwnd := findWindowContaining("Tumblr")
	if hwnd == 0 {
		return
	}
	var origin struct{ X, Y int32 }
	procClientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&origin)))
	dpi := uintptr(96)
	if r, _, _ := procGetDpiForWindow.Call(hwnd); r != 0 {
		dpi = r
	}
	scale := float64(dpi) / 96
	chromeUI := int(88 * scale)
	clickScreen(int(origin.X)+int(cssX*scale), int(origin.Y)+chromeUI+int(cssY*scale))
}

func clickPasskeyPINField(hwnd uintptr) {
	var r struct{ Left, Top, Right, Bottom int32 }
	ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	if ok == 0 {
		return
	}
	w := int(r.Right - r.Left)
	h := int(r.Bottom - r.Top)
	if w < 200 || h < 200 {
		return
	}
	// PIN box sits under "Enter your PIN". Stay above "I forgot my PIN".
	x := int(r.Left) + w/2
	y := int(r.Top) + int(float64(h)*0.56)
	clickScreen(x, y)
}

func sendCharTo(hwnd uintptr, ch rune) {
	procSendMessageW.Call(hwnd, wmChar, uintptr(ch), 0)
}

func sendPINToPasskey(hwnd uintptr, pin string) {
	detach := attachToWindowThread(hwnd)
	defer detach()
	procBringWindowToTop.Call(hwnd)
	procSetForegroundWindow.Call(hwnd)
	time.Sleep(150 * time.Millisecond)
	clickPasskeyPINField(hwnd)
	time.Sleep(120 * time.Millisecond)
	target := focusedControl(hwnd)
	procSetFocus.Call(target)
	procSendMessageW.Call(target, wmSetfocus, 0, 0)
	time.Sleep(80 * time.Millisecond)
	for _, r := range pin {
		if r < '0' || r > '9' {
			continue
		}
		sendCharTo(target, r)
		sendVirtualKey(byte(r))
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond)
	procSendMessageW.Call(target, wmKeydown, vkReturn, 0)
	procSendMessageW.Call(target, wmKeyup, vkReturn, 0)
	procPostMessageW.Call(target, wmKeydown, vkReturn, 0)
	sendVirtualKey(vkReturn)
}

func sendVirtualKey(vk byte) {
	procKeybdEvent.Call(uintptr(vk), 0, 0, 0)
	procKeybdEvent.Call(uintptr(vk), 0, keyeventfKeyup, 0)
}

func sendDigitString(s string) {
	for _, r := range s {
		if r < '0' || r > '9' {
			continue
		}
		sendVirtualKey(byte(r))
		time.Sleep(40 * time.Millisecond)
	}
}

func typeIntoPasskeyDialog(pin string) error {
	return typeIntoPasskeyDialogWait(pin, 12*time.Second)
}

func typeIntoPasskeyDialogWait(pin string, wait time.Duration) error {
	pin = strings.TrimSpace(pin)
	if pin == "" {
		return fmt.Errorf("empty PIN")
	}
	deadline := time.Now().Add(wait)
	var hwnd uintptr
	for time.Now().Before(deadline) {
		hwnd = findWindowContaining("Windows Security")
		if hwnd == 0 {
			hwnd = findWindowContaining("passkey")
		}
		if hwnd == 0 {
			hwnd = findWindowContaining("AgeKey")
		}
		if hwnd != 0 {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if hwnd == 0 {
		var titles []string
		for _, w := range listVisibleWindows() {
			if w.title != "" {
				titles = append(titles, w.title)
			}
		}
		return fmt.Errorf("passkey dialog not found; windows: %s", strings.Join(titles, " | "))
	}
	fmt.Println("Focused:", windowText(hwnd))
	sendPINToPasskey(hwnd, pin)
	return nil
}

func nagWasDismissed(got string) bool {
	switch strings.TrimSpace(got) {
	case "", "none", "no-ok", "no-btn", "action-required", "no-chrome", "no-action", "no-hwnd":
		return false
	default:
		return true
	}
}

func leakWindowPresent() bool {
	for _, t := range []string{
		"Change your password",
		"Check your passwords",
		"Check passwords",
		"Password leaked",
		"compromised password",
	} {
		if findWindowContaining(t) != 0 {
			return true
		}
	}
	return false
}

var lastConsentUIA time.Time
var lastSavePasswordDismiss time.Time

// runUIAPowerShell runs a short STA UIAutomation script with a hard timeout so
// a stuck UIA tree walk cannot freeze login/setup on the dashboard.
func runUIAPowerShell(script string, timeout time.Duration) string {
	if timeout <= 0 {
		timeout = 2500 * time.Millisecond
	}
	cmd := exec.Command("powershell", "-NoProfile", "-STA", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return ""
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return ""
	}
	type result struct {
		out string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		buf := make([]byte, 0, 256)
		tmp := make([]byte, 256)
		for {
			n, rerr := stdout.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
			}
			if rerr != nil {
				waitErr := cmd.Wait()
				ch <- result{out: strings.TrimSpace(string(buf)), err: waitErr}
				return
			}
		}
	}()
	select {
	case r := <-ch:
		if r.err != nil && r.out == "" {
			return ""
		}
		return r.out
	case <-time.After(timeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		// Don't block forever waiting for the reader after Kill — UIA can
		// leave pipes wedged and that used to freeze setup on the dashboard.
		select {
		case <-ch:
		case <-time.After(500 * time.Millisecond):
		}
		return ""
	}
}

func dismissConsentIAgree() string {
	if !nativeChromeClicks {
		return ""
	}
	if findWindowContaining("Windows Security") != 0 {
		return ""
	}
	if !lastConsentUIA.IsZero() && time.Since(lastConsentUIA) < 1500*time.Millisecond {
		return ""
	}
	lastConsentUIA = time.Now()
	script := `
Add-Type -AssemblyName UIAutomationClient | Out-Null
$root = [System.Windows.Automation.AutomationElement]::RootElement
$nameProp = [System.Windows.Automation.AutomationElement]::NameProperty
$typeProp = [System.Windows.Automation.AutomationElement]::ControlTypeProperty
$scope = [System.Windows.Automation.TreeScope]::Descendants
$btnType = [System.Windows.Automation.ControlType]::Button
function Find-Btn([string]$label) {
  $cond = New-Object System.Windows.Automation.AndCondition (
    (New-Object System.Windows.Automation.PropertyCondition $nameProp, $label),
    (New-Object System.Windows.Automation.PropertyCondition $typeProp, $btnType)
  )
  return $root.FindFirst($scope, $cond)
}
function Invoke-El($el) {
  $pat = $el.GetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern)
  $pat.Invoke()
}
foreach ($label in @('I Agree!','I Agree')) {
  $el = Find-Btn $label
  if ($el) {
    try { Invoke-El $el; Write-Output ('consent-' + ($label.ToLower() -replace '[^a-z]','')); exit 0 } catch {}
  }
}
Write-Output 'none'
`
	cmd := exec.Command("powershell", "-NoProfile", "-STA", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	got := strings.TrimSpace(string(out))
	if nagWasDismissed(got) {
		lastConsentUIA = time.Now()
	}
	return got
}

func dismissNativePasswordNag() string {
	// Always dismiss Save-password / leak bubbles — they block setup even when
	// nativeChromeClicks is off (setup/login currently disable mouse steals).
	if findWindowContaining("Windows Security") != 0 {
		return ""
	}
	if !lastSavePasswordDismiss.IsZero() && time.Since(lastSavePasswordDismiss) < 1500*time.Millisecond {
		return ""
	}
	if got := dismissChromeLeakDialog(); nagWasDismissed(got) {
		return got
	}
	script := `
Add-Type -AssemblyName UIAutomationClient | Out-Null
$root = [System.Windows.Automation.AutomationElement]::RootElement
$nameProp = [System.Windows.Automation.AutomationElement]::NameProperty
$classProp = [System.Windows.Automation.AutomationElement]::ClassNameProperty
$typeProp = [System.Windows.Automation.AutomationElement]::ControlTypeProperty
$scope = [System.Windows.Automation.TreeScope]::Descendants
$child = [System.Windows.Automation.TreeScope]::Children
$btnType = [System.Windows.Automation.ControlType]::Button
function Invoke-El($el) {
  $pat = $el.GetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern)
  $pat.Invoke()
}
$wins = $root.FindAll($child, (New-Object System.Windows.Automation.PropertyCondition $classProp, 'Chrome_WidgetWin_1'))
foreach ($w in $wins) {
  foreach ($label in @('Never','No, thanks','No thanks','Not now')) {
    $cond = New-Object System.Windows.Automation.AndCondition (
      (New-Object System.Windows.Automation.PropertyCondition $nameProp, $label),
      (New-Object System.Windows.Automation.PropertyCondition $typeProp, $btnType)
    )
    $el = $w.FindFirst($scope, $cond)
    if (-not $el) { continue }
    try { Invoke-El $el; Write-Output ('save-' + ($label.ToLower() -replace '[^a-z]','')); exit 0 } catch {}
  }
}
Write-Output 'none'
`
	got := runUIAPowerShell(script, 2500*time.Millisecond)
	if nagWasDismissed(got) {
		lastSavePasswordDismiss = time.Now()
	}
	return got
}

var lastLeakDismiss time.Time

func dismissChromeLeakDialog() string {
	if findWindowContaining("Windows Security") != 0 {
		return ""
	}
	if !lastLeakDismiss.IsZero() && time.Since(lastLeakDismiss) < 1500*time.Millisecond {
		return ""
	}
	script := `
Add-Type -AssemblyName UIAutomationClient | Out-Null
$root = [System.Windows.Automation.AutomationElement]::RootElement
$nameProp = [System.Windows.Automation.AutomationElement]::NameProperty
$classProp = [System.Windows.Automation.AutomationElement]::ClassNameProperty
$typeProp = [System.Windows.Automation.AutomationElement]::ControlTypeProperty
$btnType = [System.Windows.Automation.ControlType]::Button
$child = [System.Windows.Automation.TreeScope]::Children
$desc = [System.Windows.Automation.TreeScope]::Descendants
$walker = [System.Windows.Automation.TreeWalker]::ControlViewWalker
function Is-Leak([string]$t) {
  return $t -match '(?i)change your password|check your passwords|check passwords|data breach|compromised password|found in a data|password leak|leaked password|unsafe password|password was found|passwords were found'
}
function Invoke-El($el) {
  $pat = $el.GetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern)
  $pat.Invoke()
}
function Ctx($el) {
  $t = ''
  $n = $el
  for ($i = 0; $i -lt 10 -and $n; $i++) {
    try { $t += ' ' + $n.Current.Name } catch {}
    $n = $walker.GetParent($n)
  }
  return $t
}
$wins = $root.FindAll($child, (New-Object System.Windows.Automation.PropertyCondition $classProp, 'Chrome_WidgetWin_1'))
$targets = @($root)
foreach ($w in $wins) { $targets += $w }
foreach ($w in $targets) {
  foreach ($label in @('OK','Okay','Got it','Dismiss','Not now','Never','Close')) {
    $cond = New-Object System.Windows.Automation.AndCondition (
      (New-Object System.Windows.Automation.PropertyCondition $nameProp, $label),
      (New-Object System.Windows.Automation.PropertyCondition $typeProp, $btnType)
    )
    $el = $w.FindFirst($desc, $cond)
    if (-not $el) { continue }
    $ctx = Ctx $el
    if (-not (Is-Leak $ctx)) { continue }
    try { Invoke-El $el; Write-Output ('leak-' + ($label.ToLower() -replace '[^a-z]','')); exit 0 } catch {}
  }
}
Write-Output 'none'
`
	got := runUIAPowerShell(script, 2500*time.Millisecond)
	if nagWasDismissed(got) {
		lastLeakDismiss = time.Now()
	}
	return got
}

func dismissSavePasswordBubble() string {
	if nag := dismissNativePasswordNag(); nagWasDismissed(nag) {
		return nag
	}
	return ""
}

func passkeyPickerNeedles(now time.Time) []string {
	out := passkeyDateNeedles(now)
	out = append(out, passkeyDateNeedles(now.AddDate(0, 0, -1))...)
	return out
}

func passkeyDateNeedles(now time.Time) []string {
	iso, uk := passkeyTodayTokens(now)
	out := []string{iso, "AgeKey - " + iso, uk}
	out = append(out, ageKeyDateNeedlesOne(now)...)
	return out
}

func passkeyTodayTokens(now time.Time) (iso, uk string) {
	y, m, d := now.Date()
	iso = fmt.Sprintf("%04d-%02d-%02d", y, int(m), d)
	uk = fmt.Sprintf("%02d/%02d/%04d", d, int(m), y)
	return iso, uk
}

func passkeyHitIsToday(got string, now time.Time) bool {
	got = strings.ToLower(strings.TrimSpace(got))
	if got == "" || got == "none" || got == "pin-prompt" {
		return false
	}
	if strings.Contains(got, "iphone") || strings.Contains(got, "android") || strings.Contains(got, "security key") || strings.Contains(got, "cancel") {
		return false
	}
	if strings.Contains(got, "newest") {
		return true
	}
	for _, day := range []time.Time{now, now.AddDate(0, 0, -1)} {
		iso, uk := passkeyTodayTokens(day)
		if strings.Contains(got, strings.ToLower(iso)) || strings.Contains(got, strings.ToLower(uk)) {
			return true
		}
	}
	return false
}

var ageKeyISORe = regexp.MustCompile(`(?i)agekey[^\d]{0,12}(\d{4}-\d{2}-\d{2})`)

func preferredAgeKeyLabel(names []string, now time.Time) string {
	today, _ := passkeyTodayTokens(now)
	yday, _ := passkeyTodayTokens(now.AddDate(0, 0, -1))
	best := ""
	bestScore := 0
	for _, name := range names {
		n := strings.ToLower(strings.TrimSpace(name))
		if n == "" || strings.Contains(n, "iphone") || strings.Contains(n, "android") || strings.Contains(n, "security key") {
			continue
		}
		m := ageKeyISORe.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		iso := m[1]
		score := 1
		day, err := time.Parse("2006-01-02", iso)
		if err == nil {
			score = int(day.Unix() / 86400)
		}
		if iso == today {
			score += 1_000_000
		} else if iso == yday {
			score += 500_000
		}
		if score > bestScore || (score == bestScore && len(name) > len(best)) {
			bestScore = score
			best = strings.TrimSpace(name)
		}
	}
	return best
}

var lastPasskeySelect time.Time

func pickAgeKeyPasskeySoon() string {
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if findWindowsSecurityHWND() == 0 {
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if securityDialogIsPIN() {
			return "pin-prompt"
		}
		time.Sleep(300 * time.Millisecond)
	}
	if securityDialogIsPIN() {
		return "pin-prompt"
	}
	return ""
}

func selectTodayAgeKeyPasskey() string {
	if findWindowsSecurityHWND() == 0 {
		return ""
	}
	if securityDialogIsPIN() {
		return "pin-prompt"
	}
	return ""
}

// Windows Security "Choose a passkey" layout, measured from the 2026-09-20
// dialog (oldest AgeKey, newest AgeKey, iPhone, Security key, Cancel).
// Fractions are of the Credential Dialog Xaml Host, not Chrome.
const (
	passkeyNewestXFrac   = 0.30
	passkeyNewestYFrac   = 0.42
	passkeyNewestYFrac2  = 0.445
	passkeyTitleMaxY     = 0.26
	passkeyNewestMinY    = 0.38
	passkeyNewestMaxY    = 0.47
	passkeyCancelMinY    = 0.72
	passkeyCancelMinX    = 0.62
	passkeyCancelCornerY = 0.68
)

func ageKeyNewestPasskeyPoint(w, h int) (x, y int) {
	if w < 1 {
		w = 440
	}
	if h < 1 {
		h = 560
	}
	return int(float64(w) * passkeyNewestXFrac), int(float64(h) * passkeyNewestYFrac)
}

func ageKeyNewestPasskeyPoints(w, h int) [][2]int {
	if w < 1 {
		w = 440
	}
	if h < 1 {
		h = 560
	}
	raw := [][2]int{
		{int(float64(w) * passkeyNewestXFrac), int(float64(h) * passkeyNewestYFrac)},
		{int(float64(w) * 0.28), int(float64(h) * passkeyNewestYFrac2)},
	}
	out := make([][2]int, 0, len(raw))
	for _, p := range raw {
		if passkeyClickHitsNewestAgeKey(w, h, p[0], p[1]) {
			out = append(out, p)
		}
	}
	return out
}

func passkeyClickZone(w, h, x, y int) string {
	if w < 1 || h < 1 || x < 0 || y < 0 || x > w || y > h {
		return "invalid"
	}
	xf := float64(x) / float64(w)
	yf := float64(y) / float64(h)
	if yf >= passkeyCancelMinY || (xf >= passkeyCancelMinX && yf >= passkeyCancelCornerY) {
		return "cancel"
	}
	if yf < passkeyTitleMaxY {
		return "title"
	}
	if yf < passkeyNewestMinY {
		return "old-agekey"
	}
	if yf <= passkeyNewestMaxY && xf >= 0.12 && xf <= 0.62 {
		return "newest-agekey"
	}
	if yf < passkeyCancelMinY {
		return "other-passkey"
	}
	return "cancel"
}

func passkeyClickHitsNewestAgeKey(w, h, x, y int) bool {
	return passkeyClickZone(w, h, x, y) == "newest-agekey"
}

func reportedNewestPasskeyLabel(now time.Time) string {
	yday, _ := passkeyTodayTokens(now.AddDate(0, 0, -1))
	return "AgeKey - " + yday + " newest"
}

func windowRect(hwnd uintptr) (left, top, width, height int, ok bool) {
	var r struct{ Left, Top, Right, Bottom int32 }
	pass, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	if pass == 0 {
		return 0, 0, 0, 0, false
	}
	w := int(r.Right - r.Left)
	h := int(r.Bottom - r.Top)
	if w < 160 || h < 160 {
		return 0, 0, 0, 0, false
	}
	return int(r.Left), int(r.Top), w, h, true
}

func clickNewestListedAgeKey(hwnd uintptr, now time.Time) string {
	_ = hwnd
	_ = now
	return ""
}

func securityDialogIsPIN() bool {
	hwnd := findWindowsSecurityHWND()
	if hwnd == 0 {
		return false
	}
	t := strings.ToLower(windowText(hwnd) + " " + windowClass(hwnd))
	if strings.Contains(t, "choose a passkey") {
		return false
	}
	return strings.Contains(t, "pin")
}
