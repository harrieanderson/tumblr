package main

import (
	"fmt"
	"os/exec"
	"strings"
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

func listVisibleWindows() []winInfo {
	var out []winInfo
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		vis, _, _ := procIsWindowVisible.Call(hwnd)
		if vis == 0 {
			return 1
		}
		title := windowText(hwnd)
		class := windowClass(hwnd)
		if title == "" && class == "" {
			return 1
		}
		out = append(out, winInfo{hwnd: hwnd, title: title, class: class})
		return 1
	})
	procEnumWindows.Call(cb, 0)
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

func dismissNativePasswordNag() string {
	if !nativeChromeClicks {
		return ""
	}
	if findWindowContaining("Windows Security") != 0 {
		return ""
	}
	if got := dismissChromeLeakDialog(); nagWasDismissed(got) {
		return got
	}
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
foreach ($label in @('Never','No, thanks','Not now')) {
  $el = Find-Btn $label
  if ($el) {
    try { Invoke-El $el; Write-Output ('save-' + ($label.ToLower() -replace '[^a-z]','')); exit 0 } catch {}
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
	return strings.TrimSpace(string(out))
}

var lastLeakDismiss time.Time

func dismissChromeLeakDialog() string {
	if !nativeChromeClicks {
		return ""
	}
	if findWindowContaining("Windows Security") != 0 {
		return ""
	}
	if !lastLeakDismiss.IsZero() && time.Since(lastLeakDismiss) < 800*time.Millisecond {
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
	cmd := exec.Command("powershell", "-NoProfile", "-STA", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	got := strings.TrimSpace(string(out))
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
