package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Two runs replying from the same account double-text people and log in
// twice at once, which Tumblr treats as suspicious. A lock file per account
// and job keeps that to one run, across the CLI, GUI and reach.
type accountLock struct {
	PID   int       `json:"pid"`
	Since time.Time `json:"since"`
}

const staleAccountLock = 6 * time.Hour

func accountLockPath(accountID, job string) string {
	return filepath.Join(profilesDataDir, accountID, job+".lock")
}

// tryLockAccount returns a release func, or ok=false when another live
// process holds the lock.
func tryLockAccount(accountID, job string) (release func(), holder int, ok bool) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return func() {}, 0, true
	}
	path := accountLockPath(accountID, job)
	if raw, err := os.ReadFile(path); err == nil {
		var l accountLock
		if json.Unmarshal(raw, &l) == nil && l.PID != os.Getpid() &&
			time.Since(l.Since) < staleAccountLock && processAlive(l.PID) {
			return nil, l.PID, false
		}
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	raw, _ := json.Marshal(accountLock{PID: os.Getpid(), Since: time.Now()})
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return func() {}, 0, true
	}
	return func() {
		if raw, err := os.ReadFile(path); err == nil {
			var l accountLock
			if json.Unmarshal(raw, &l) == nil && l.PID == os.Getpid() {
				_ = os.Remove(path)
			}
		}
	}, 0, true
}
