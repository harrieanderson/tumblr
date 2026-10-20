//go:build windows

package main

import "syscall"

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
)

// OpenProcess alone also succeeds for exited processes whose handles are
// still held, so a killed run would keep its locks; check the exit code.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
