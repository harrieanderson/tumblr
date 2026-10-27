package main

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Bounded Chrome recovery: after this many failed recoveries in a row, mark
// needsInteractiveLogin and stop launching Chrome until a manual -login.
const sessionMaxRecoveryAttempts = 2

// needsInteractiveLoginError means automated recovery stopped; run
// `tumblr.exe -login -account ID` (visible window) to clear captcha/2FA.
type needsInteractiveLoginError struct {
	ID     string
	Reason string
}

func (e needsInteractiveLoginError) Error() string {
	id := strings.TrimSpace(e.ID)
	if id == "" {
		id = "account"
	}
	reason := strings.TrimSpace(e.Reason)
	if reason == "" {
		reason = "needs interactive login"
	}
	return fmt.Sprintf("%s: needs interactive login (%s)", id, reason)
}

func isNeedsInteractiveLogin(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(needsInteractiveLoginError); ok {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "needs interactive login")
}

// stickyProxyIDForAccount returns the currently bound sticky proxy id, or "".
// Read-only — does not lease or rebalance.
func stickyProxyIDForAccount(accountID string) string {
	accountID = accountSlug(accountID)
	if accountID == "" {
		return ""
	}
	r := proxyPool()
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.assign[accountID]
	if !ok {
		return ""
	}
	return a.ProxyID
}

// markSessionValidated records a successful authenticated preflight and clears
// interactive-login / recovery counters. Does not write disk — caller saves.
func markSessionValidated(auth *Auth) {
	if auth == nil {
		return
	}
	auth.ValidatedAt = time.Now().UTC().Format(time.RFC3339)
	if id := accountIDFromAuth(*auth); id != "" {
		if pid := stickyProxyIDForAccount(id); pid != "" {
			auth.ProxyID = pid
		}
	}
	auth.NeedsInteractiveLogin = false
	auth.RecoveryAttempts = 0
	auth.LastRecoveryAt = ""
}

// markNeedsInteractiveLogin persists the flag so later wipe/reach runs soft-skip
// instead of re-opening Chrome. Returns the typed error for callers.
func markNeedsInteractiveLogin(auth *Auth, reason string) error {
	id := ""
	if auth != nil {
		id = accountIDFromAuth(*auth)
		auth.NeedsInteractiveLogin = true
		if strings.TrimSpace(reason) != "" {
			auth.LastRecoveryAt = time.Now().UTC().Format(time.RFC3339)
		}
		if auth.path != "" {
			_ = saveAuth(*auth)
		}
		if id != "" {
			sessionDead.Store(id, true)
		}
	}
	return needsInteractiveLoginError{ID: id, Reason: reason}
}

func clearNeedsInteractiveLogin(auth *Auth) {
	if auth == nil {
		return
	}
	auth.NeedsInteractiveLogin = false
	auth.RecoveryAttempts = 0
	if id := accountIDFromAuth(*auth); id != "" {
		sessionDead.Delete(id)
	}
}

func sessionBlocksChromeRecovery(auth *Auth) bool {
	return auth != nil && auth.NeedsInteractiveLogin
}

func noteRecoveryAttempt(auth *Auth) {
	if auth == nil {
		return
	}
	auth.RecoveryAttempts++
	auth.LastRecoveryAt = time.Now().UTC().Format(time.RFC3339)
}

func recoveryAttemptsExhausted(auth *Auth) bool {
	return auth != nil && auth.RecoveryAttempts >= sessionMaxRecoveryAttempts
}

// preflightSession is a cheap authenticated check (CSRF/counts). On success it
// refreshes CSRF, clears needsInteractiveLogin, marks ValidatedAt + sticky
// ProxyID, and persists auth.json. A flagged account may still pass if the
// cookie works — only Chrome recovery stays blocked.
func preflightSession(client *http.Client, auth *Auth) error {
	if client == nil || auth == nil {
		return fmt.Errorf("preflight: missing client/auth")
	}
	if !authHasSID(*auth) {
		if sessionBlocksChromeRecovery(auth) {
			return needsInteractiveLoginError{ID: accountIDFromAuth(*auth), Reason: "flagged in auth.json"}
		}
		return fmt.Errorf("preflight: no sid")
	}
	attachAuthJar(client, auth)
	if err := refreshCSRF(client, auth); err != nil {
		return err
	}
	maybeFillBlog(client, auth)
	return nil
}

func interactiveReasonFrom(err error) string {
	if err == nil {
		return "recovery failed"
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "captcha"):
		return "captcha"
	case strings.Contains(s, "2fa"), strings.Contains(s, "two-factor"):
		return "2fa"
	case strings.Contains(s, "login did not complete"):
		return "login incomplete"
	case strings.Contains(s, "session expired"):
		return "session expired"
	case strings.Contains(s, "csrf"):
		return "csrf/session rejected"
	default:
		return truncate(err.Error(), 80)
	}
}
