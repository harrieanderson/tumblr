package main

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	esContinuous       = 0x80000000
	esSystemRequired   = 0x00000001
	esDisplayRequired  = 0x00000002
	esAwaymodeRequired = 0x00000040
)

var (
	sessionUntil  time.Time
	kernel32      = syscall.NewLazyDLL("kernel32.dll")
	procExecState = kernel32.NewProc("SetThreadExecutionState")
)

func parseForArg(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("empty -for value")
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n <= 0 {
			return 0, fmt.Errorf("-for needs a positive number of minutes")
		}
		return time.Duration(n) * time.Minute, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("try -for 60 or -for 1h")
	}
	return d, nil
}

func setSessionFor(d time.Duration) {
	if d <= 0 {
		return
	}
	sessionUntil = time.Now().Add(d)
	fmt.Printf("This run will stop at %s (~%s).\n", sessionUntil.Format("15:04"), d.Round(time.Minute))
}

func sessionExpired() bool {
	return !sessionUntil.IsZero() && !time.Now().Before(sessionUntil)
}

func sessionRemaining() time.Duration {
	if sessionUntil.IsZero() {
		return 0
	}
	d := time.Until(sessionUntil)
	if d < 0 {
		return 0
	}
	return d
}

func keepMachineAwake() {
	assertAwake()
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for range t.C {
			assertAwake()
		}
	}()
	fmt.Println("Windows will not sleep or turn the screen off while this window is running.")
}

func assertAwake() {
	flags := uintptr(esContinuous | esSystemRequired | esDisplayRequired | esAwaymodeRequired)
	_, _, _ = procExecState.Call(flags)
}
