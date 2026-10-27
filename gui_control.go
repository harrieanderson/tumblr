package main

import (
	"fmt"
	"sync"
	"time"
)

// Localhost control-panel concurrency. Chrome + Tumblr rate limits dominate;
// keep the worker ceiling small so profiles/proxies stay stable.
const (
	guiDefaultWorkers = 4
	guiMaxWorkers     = 8
)

// profileLocks prevents two GUI jobs from using the same Chrome profile at once.
type profileLocks struct {
	mu   sync.Mutex
	held map[string]string // accountID -> job name
}

func newProfileLocks() *profileLocks {
	return &profileLocks{held: map[string]string{}}
}

func (p *profileLocks) tryLock(id, job string) error {
	id = accountSlug(id)
	if id == "" {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if holder, ok := p.held[id]; ok {
		return fmt.Errorf("profile %s is locked by %q", id, holder)
	}
	p.held[id] = job
	return nil
}

func (p *profileLocks) unlock(id string) {
	id = accountSlug(id)
	if id == "" {
		return
	}
	p.mu.Lock()
	delete(p.held, id)
	p.mu.Unlock()
}

func (p *profileLocks) unlockAll() {
	p.mu.Lock()
	p.held = map[string]string{}
	p.mu.Unlock()
}

func (p *profileLocks) snapshot() []map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]map[string]string, 0, len(p.held))
	for id, job := range p.held {
		out = append(out, map[string]string{"id": id, "job": job})
	}
	return out
}

func (p *profileLocks) lockedIDs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.held))
	for id := range p.held {
		out = append(out, id)
	}
	return out
}

// clampGUIWorkers bounds a requested parallel worker count.
func clampGUIWorkers(n int) int {
	if n <= 0 {
		return guiDefaultWorkers
	}
	if n > guiMaxWorkers {
		return guiMaxWorkers
	}
	return n
}

func (s *guiServer) requestStop() {
	s.stopMu.Lock()
	s.stop = true
	s.stopMu.Unlock()
	clearSessionUntil()
	s.hub.broadcast("Emergency stop requested — finishing in-flight work, then idle.")
}

func (s *guiServer) clearStop() {
	s.stopMu.Lock()
	s.stop = false
	s.stopMu.Unlock()
}

func (s *guiServer) stopRequested() bool {
	s.stopMu.Lock()
	defer s.stopMu.Unlock()
	return s.stop
}

func (s *guiServer) controlSnapshot() map[string]any {
	s.mu.Lock()
	busy := s.busy
	job := s.job
	workers := s.workers
	s.mu.Unlock()

	until := ""
	remainSec := 0
	if !sessionUntil.IsZero() {
		until = sessionUntil.Format("15:04")
		remainSec = int(sessionRemaining() / time.Second)
	}
	return map[string]any{
		"busy":          busy,
		"job":           job,
		"workers":       workers,
		"maxWorkers":    guiMaxWorkers,
		"defaultWorkers": guiDefaultWorkers,
		"stop":          s.stopRequested(),
		"locked":        s.locks.snapshot(),
		"lockedIds":     s.locks.lockedIDs(),
		"sessionUntil":  until,
		"remainSec":     remainSec,
	}
}
