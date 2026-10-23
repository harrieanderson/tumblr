package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	proxyConfigFile = "config/proxies.json"
	proxyStateFile  = "data/proxy-state.json"
)

// ProxyConfig is the resilience-oriented rotation policy for market-research
// HTTP traffic. When enabled, accounts are sticky-assigned across the pool and
// rebalanced when stickiness expires or a proxy is cooled down.
type ProxyConfig struct {
	Enabled               bool          `json:"enabled"`
	Proxies               []ProxyEntry  `json:"proxies"`
	StickyMinutes         int           `json:"stickyMinutes"`
	MinRequestGapMs       int           `json:"minRequestGapMs"`
	PerProxyMaxConcurrent int           `json:"perProxyMaxConcurrent"`
	PerAccountRPM         int           `json:"perAccountMaxPerMinute"`
	PerProxyRPM           int           `json:"perProxyMaxPerMinute"`
	RespectRetryAfter     bool          `json:"respectRetryAfter"`
	Backoff               BackoffConfig `json:"backoff"`
}

type ProxyEntry struct {
	URL   string `json:"url"`
	Label string `json:"label,omitempty"`
}

type BackoffConfig struct {
	InitialSeconds int     `json:"initialSeconds"`
	MaxSeconds     int     `json:"maxSeconds"`
	Multiplier     float64 `json:"multiplier"`
	JitterPercent  int     `json:"jitterPercent"`
}

func defaultProxyConfig() ProxyConfig {
	return ProxyConfig{
		Enabled:               false,
		StickyMinutes:         45,
		MinRequestGapMs:       900,
		PerProxyMaxConcurrent: 2,
		PerAccountRPM:         18,
		PerProxyRPM:           36,
		RespectRetryAfter:     true,
		Backoff: BackoffConfig{
			InitialSeconds: 8,
			MaxSeconds:     900,
			Multiplier:     2,
			JitterPercent:  25,
		},
	}
}

type proxyEndpoint struct {
	id    string
	label string
	raw   string
	url   *url.URL
}

type proxyAssignment struct {
	ProxyID     string    `json:"proxyId"`
	StickyUntil time.Time `json:"stickyUntil"`
}

type proxyRuntime struct {
	failures   int
	cooldownUntil time.Time
	inFlight   int
	lastReq    time.Time
	recent     []time.Time
}

// proxySleeper parks until d elapses or ctx is canceled.
// Tests inject a no-op / channel-driven sleeper via proxyRouter.sleep.
type proxySleeper func(ctx context.Context, d time.Duration) error

func contextSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type proxyRouter struct {
	mu     sync.Mutex
	cfg    ProxyConfig
	pool   []*proxyEndpoint
	byID   map[string]*proxyEndpoint
	assign map[string]proxyAssignment // accountID -> sticky proxy
	rt     map[string]*proxyRuntime   // proxyID -> runtime
	accRT  map[string]*proxyRuntime   // accountID -> rate window
	loaded time.Time
	// sleep is context-aware parking for lease waits and 429/503 retries.
	// Nil means contextSleep. Field (not package var) keeps parallel tests isolated.
	sleep proxySleeper
	// freshBindCount increments only when beginRequestLocked commits a previously
	// unbound (or expired/rebound) account sticky. Test-only; reset per test.
	freshBindCount int
}

var (
	proxyOnce   sync.Once
	proxyShared *proxyRouter
)

func newProxyRouter(cfg ProxyConfig) *proxyRouter {
	r := &proxyRouter{
		cfg:    defaultProxyConfig(),
		byID:   map[string]*proxyEndpoint{},
		assign: map[string]proxyAssignment{},
		rt:     map[string]*proxyRuntime{},
		accRT:  map[string]*proxyRuntime{},
		sleep:  contextSleep,
	}
	r.applyConfig(cfg)
	return r
}

func proxyPool() *proxyRouter {
	proxyOnce.Do(func() {
		proxyShared = &proxyRouter{
			cfg:    defaultProxyConfig(),
			byID:   map[string]*proxyEndpoint{},
			assign: map[string]proxyAssignment{},
			rt:     map[string]*proxyRuntime{},
			accRT:  map[string]*proxyRuntime{},
			sleep:  contextSleep,
		}
		proxyShared.reload()
	})
	return proxyShared
}

func (r *proxyRouter) doSleep(ctx context.Context, d time.Duration) error {
	fn := r.sleep
	if fn == nil {
		fn = contextSleep
	}
	return fn(ctx, d)
}

// persistedHasAccountLocked mirrors saveStateLocked's assignment filter.
// Must be called with r.mu held. Test-only helper for deferred-sticky asserts.
func (r *proxyRouter) persistedHasAccountLocked(accountID string) bool {
	now := time.Now()
	a, ok := r.assign[accountSlug(accountID)]
	return ok && a.StickyUntil.After(now)
}

// inflightFor is a test-only helper to sample capacity without racing on maps.
func (r *proxyRouter) inflightFor(proxyID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if proxyID == "" {
		return 0
	}
	rt := r.rt[proxyID]
	if rt == nil {
		return 0
	}
	return rt.inFlight
}

// boostProxyPaceForWipe slightly raises wipe throughput without touching
// config/proxies.json (reach/DM keep the normal saved pace on next process).
func boostProxyPaceForWipe() {
	r := proxyPool()
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.cfg.Enabled || len(r.pool) == 0 {
		return
	}
	if r.cfg.MinRequestGapMs > 400 {
		r.cfg.MinRequestGapMs = 400
	}
	if r.cfg.PerAccountRPM < 36 {
		r.cfg.PerAccountRPM = 36
	}
	if r.cfg.PerProxyRPM < 72 {
		r.cfg.PerProxyRPM = 72
	}
	// Clear recent windows so the new caps apply immediately.
	for _, rt := range r.accRT {
		rt.recent = nil
		rt.lastReq = time.Time{}
	}
	for _, rt := range r.rt {
		rt.recent = nil
		rt.lastReq = time.Time{}
	}
	fmt.Printf("proxy: wipe boost — gap %dms, account %d/min, proxy %d/min\n",
		r.cfg.MinRequestGapMs, r.cfg.PerAccountRPM, r.cfg.PerProxyRPM)
}

func (r *proxyRouter) reload() {
	cfg := defaultProxyConfig()
	if raw, err := os.ReadFile(proxyConfigFile); err == nil {
		_ = json.Unmarshal(raw, &cfg)
	}
	r.applyConfig(cfg)
	r.loadState()
}

func (r *proxyRouter) applyConfig(cfg ProxyConfig) {
	if cfg.StickyMinutes <= 0 {
		cfg.StickyMinutes = 45
	}
	if cfg.MinRequestGapMs < 0 {
		cfg.MinRequestGapMs = 0
	}
	if cfg.PerProxyMaxConcurrent <= 0 {
		cfg.PerProxyMaxConcurrent = 2
	}
	if cfg.PerAccountRPM <= 0 {
		cfg.PerAccountRPM = 18
	}
	if cfg.PerProxyRPM <= 0 {
		cfg.PerProxyRPM = 36
	}
	if cfg.Backoff.InitialSeconds <= 0 {
		cfg.Backoff.InitialSeconds = 8
	}
	if cfg.Backoff.MaxSeconds <= 0 {
		cfg.Backoff.MaxSeconds = 900
	}
	if cfg.Backoff.Multiplier < 1.1 {
		cfg.Backoff.Multiplier = 2
	}
	if cfg.Backoff.JitterPercent < 0 {
		cfg.Backoff.JitterPercent = 0
	}
	if cfg.Backoff.JitterPercent > 80 {
		cfg.Backoff.JitterPercent = 80
	}

	pool := make([]*proxyEndpoint, 0, len(cfg.Proxies))
	byID := map[string]*proxyEndpoint{}
	for i, e := range cfg.Proxies {
		raw := strings.TrimSpace(e.URL)
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.Scheme == "" || u.Host == "" {
			fmt.Printf("proxy: skipping invalid entry %q\n", redactProxyURL(raw))
			continue
		}
		label := strings.TrimSpace(e.Label)
		if label == "" {
			label = fmt.Sprintf("p%d", i+1)
		}
		id := proxyIDFor(label, raw)
		ep := &proxyEndpoint{id: id, label: label, raw: raw, url: u}
		pool = append(pool, ep)
		byID[id] = ep
	}
	r.cfg = cfg
	r.pool = pool
	r.byID = byID
	r.loaded = time.Now()
	if !cfg.Enabled {
		return
	}
	if len(pool) == 0 {
		fmt.Println("proxy: enabled but no valid proxies in config/proxies.json")
		return
	}
	fmt.Printf("proxy: enabled — %d endpoints, sticky %dm, gap %dms, account %d/min, proxy %d/min\n",
		len(pool), cfg.StickyMinutes, cfg.MinRequestGapMs, cfg.PerAccountRPM, cfg.PerProxyRPM)
}

func proxyIDFor(label, raw string) string {
	// Stable id without credentials so state files stay safe to skim.
	u, err := url.Parse(raw)
	if err != nil {
		return label
	}
	host := u.Host
	if u.User != nil {
		host = u.Hostname()
		if p := u.Port(); p != "" {
			host += ":" + p
		}
	}
	return label + "@" + u.Scheme + "://" + host
}

func redactProxyURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(invalid)"
	}
	if u.User != nil {
		u.User = url.UserPassword("***", "***")
	}
	return u.String()
}

type proxyStateFileData struct {
	Assignments map[string]proxyAssignment `json:"assignments"`
	Cooldowns   map[string]time.Time       `json:"cooldowns"`
}

func (r *proxyRouter) loadState() {
	raw, err := os.ReadFile(proxyStateFile)
	if err != nil {
		return
	}
	var st proxyStateFileData
	if json.Unmarshal(raw, &st) != nil {
		return
	}
	now := time.Now()
	for acc, a := range st.Assignments {
		if a.ProxyID == "" || r.byID[a.ProxyID] == nil {
			continue
		}
		if !a.StickyUntil.IsZero() && a.StickyUntil.Before(now) {
			continue
		}
		r.assign[acc] = a
	}
	for id, until := range st.Cooldowns {
		if until.After(now) && r.byID[id] != nil {
			rt := r.runtime(id)
			rt.cooldownUntil = until
		}
	}
}

func (r *proxyRouter) saveStateLocked() {
	st := proxyStateFileData{
		Assignments: map[string]proxyAssignment{},
		Cooldowns:   map[string]time.Time{},
	}
	now := time.Now()
	for acc, a := range r.assign {
		if a.StickyUntil.After(now) {
			st.Assignments[acc] = a
		}
	}
	for id, rt := range r.rt {
		if rt.cooldownUntil.After(now) {
			st.Cooldowns[id] = rt.cooldownUntil
		}
	}
	_ = os.MkdirAll(filepath.Dir(proxyStateFile), 0o755)
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(proxyStateFile, append(raw, '\n'), 0o644)
}

func (r *proxyRouter) enabled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cfg.Enabled && len(r.pool) > 0
}

func (r *proxyRouter) runtime(id string) *proxyRuntime {
	rt := r.rt[id]
	if rt == nil {
		rt = &proxyRuntime{}
		r.rt[id] = rt
	}
	return rt
}

func (r *proxyRouter) accountRuntime(id string) *proxyRuntime {
	if id == "" {
		id = "_"
	}
	rt := r.accRT[id]
	if rt == nil {
		rt = &proxyRuntime{}
		r.accRT[id] = rt
	}
	return rt
}

func (r *proxyRouter) healthyLocked(ep *proxyEndpoint, now time.Time) bool {
	if ep == nil {
		return false
	}
	rt := r.runtime(ep.id)
	return rt.cooldownUntil.Before(now) || rt.cooldownUntil.Equal(now)
}

func (r *proxyRouter) loadScoreLocked(ep *proxyEndpoint, now time.Time) int {
	score := 0
	for _, a := range r.assign {
		if a.ProxyID == ep.id && a.StickyUntil.After(now) {
			score++
		}
	}
	rt := r.runtime(ep.id)
	score += rt.inFlight * 3
	score += len(pruneRecent(rt.recent, now, time.Minute))
	return score
}

func pruneRecent(times []time.Time, now time.Time, window time.Duration) []time.Time {
	cut := now.Add(-window)
	out := times[:0]
	for _, t := range times {
		if t.After(cut) {
			out = append(out, t)
		}
	}
	return out
}

// pickProxy returns the sticky proxy for accountID, rebalancing when needed.
func (r *proxyRouter) pickProxy(accountID string) (*proxyEndpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pickProxyLocked(accountID), nil
}

// pickProxyLocked must be called with r.mu held.
// Sticky accounts stay on their binding even at capacity (wait-on-sticky).
// Fresh assigns hard-filter peers with inFlight >= cap when any free healthy
// peer exists; if every healthy peer is at cap, soft-pick lowest loadScore and wait.
// Unbound accounts return a candidate only — sticky is committed in beginRequestLocked
// so saveStateLocked never observes a half-binding while waiting.
func (r *proxyRouter) pickProxyLocked(accountID string) *proxyEndpoint {
	if !r.cfg.Enabled || len(r.pool) == 0 {
		return nil
	}
	now := time.Now()
	accountID = accountSlug(accountID)

	if a, ok := r.assign[accountID]; ok {
		if ep := r.byID[a.ProxyID]; ep != nil && a.StickyUntil.After(now) && r.healthyLocked(ep, now) {
			return ep
		}
	}

	var candidates []*proxyEndpoint
	for _, ep := range r.pool {
		if r.healthyLocked(ep, now) {
			candidates = append(candidates, ep)
		}
	}
	if len(candidates) == 0 {
		// All cooling down — pick the one that frees soonest so we don't stall forever.
		best := r.pool[0]
		bestUntil := r.runtime(best.id).cooldownUntil
		for _, ep := range r.pool[1:] {
			until := r.runtime(ep.id).cooldownUntil
			if until.Before(bestUntil) {
				best = ep
				bestUntil = until
			}
		}
		candidates = []*proxyEndpoint{best}
	} else {
		// Fresh assign: prefer peers under concurrency cap when any have room.
		capN := r.cfg.PerProxyMaxConcurrent
		var underCap []*proxyEndpoint
		for _, ep := range candidates {
			if r.runtime(ep.id).inFlight < capN {
				underCap = append(underCap, ep)
			}
		}
		if len(underCap) > 0 {
			candidates = underCap
		}
		// else: all healthy peers at cap — soft-pick lowest loadScore and wait (same as sticky path).
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		si, sj := r.loadScoreLocked(candidates[i], now), r.loadScoreLocked(candidates[j], now)
		if si != sj {
			return si < sj
		}
		return candidates[i].id < candidates[j].id
	})
	return candidates[0]
}

// proxyURLFor is sticky-read-only: returns the bound proxy URL when a healthy
// sticky assignment exists. Unbound accounts get (nil, nil) — no soft-pick and
// no assign write. Fresh sticky is committed only in beginRequestLocked.
func (r *proxyRouter) proxyURLFor(accountID string) (*url.URL, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.cfg.Enabled || len(r.pool) == 0 {
		return nil, nil
	}
	now := time.Now()
	accountID = accountSlug(accountID)
	a, ok := r.assign[accountID]
	if !ok || !a.StickyUntil.After(now) {
		return nil, nil
	}
	ep := r.byID[a.ProxyID]
	if ep == nil || !r.healthyLocked(ep, now) {
		return nil, nil
	}
	return ep.url, nil
}

// proxyLeaseCtxKey marks a request whose paced RoundTrip already holds a lease,
// so dialChromeTLS must not acquire a nested lease.
type proxyLeaseCtxKey struct{}

func contextWithProxyLease(ctx context.Context, proxyID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if proxyID == "" {
		return ctx
	}
	return context.WithValue(ctx, proxyLeaseCtxKey{}, proxyID)
}

func proxyLeaseFromContext(ctx context.Context) (proxyID string, ok bool) {
	if ctx == nil {
		return "", false
	}
	id, ok := ctx.Value(proxyLeaseCtxKey{}).(string)
	return id, ok && id != ""
}

// acquireLease picks a sticky proxy and reserves capacity under one lock so
// concurrent RoundTrips cannot oversubscribe between wait and beginRequest.
// Each loop re-calls pickProxyLocked after wait/wake so fresh soft-picks recompute.
// Blocks (capped) when the endpoint is at RPM/concurrency/gap limits.
// Honors ctx cancellation on every wait loop; never leaves inFlight reserved on cancel.
func (r *proxyRouter) acquireLease(ctx context.Context, accountID string) (proxyID string, err error) {
	const maxWait = 2 * time.Second
	if ctx == nil {
		ctx = context.Background()
	}
	accountID = accountSlug(accountID)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		r.mu.Lock()
		if !r.cfg.Enabled || len(r.pool) == 0 {
			r.mu.Unlock()
			return "", nil
		}
		ep := r.pickProxyLocked(accountID)
		proxyID = ""
		if ep != nil {
			proxyID = ep.id
		}
		wait := r.nextWaitLocked(accountID, proxyID)
		if wait <= 0 {
			r.beginRequestLocked(accountID, proxyID)
			r.mu.Unlock()
			return proxyID, nil
		}
		r.mu.Unlock()
		// Never park a request for minutes on cooldown — cap and re-check
		// (reportResult will rotate/cooldown as needed).
		if wait > maxWait {
			wait = maxWait
		}
		if err := r.doSleep(ctx, wait); err != nil {
			return "", err
		}
	}
}

func (r *proxyRouter) releaseLease(proxyID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if proxyID == "" {
		return
	}
	rt := r.runtime(proxyID)
	if rt.inFlight > 0 {
		rt.inFlight--
	}
}

// nextWaitLocked must be called with r.mu held.
func (r *proxyRouter) nextWaitLocked(accountID, proxyID string) time.Duration {
	if !r.cfg.Enabled {
		return 0
	}
	now := time.Now()
	accountID = accountSlug(accountID)

	if proxyID != "" {
		rt := r.runtime(proxyID)
		if rt.cooldownUntil.After(now) {
			return rt.cooldownUntil.Sub(now)
		}
		if rt.inFlight >= r.cfg.PerProxyMaxConcurrent {
			return 250 * time.Millisecond
		}
		rt.recent = pruneRecent(rt.recent, now, time.Minute)
		if len(rt.recent) >= r.cfg.PerProxyRPM {
			return rt.recent[0].Add(time.Minute).Sub(now) + time.Duration(rand.Intn(400))*time.Millisecond
		}
		if !rt.lastReq.IsZero() {
			gap := time.Duration(r.cfg.MinRequestGapMs) * time.Millisecond
			if ready := rt.lastReq.Add(gap); ready.After(now) {
				return ready.Sub(now)
			}
		}
	}

	acc := r.accountRuntime(accountID)
	acc.recent = pruneRecent(acc.recent, now, time.Minute)
	if len(acc.recent) >= r.cfg.PerAccountRPM {
		return acc.recent[0].Add(time.Minute).Sub(now) + time.Duration(rand.Intn(400))*time.Millisecond
	}
	if !acc.lastReq.IsZero() {
		gap := time.Duration(r.cfg.MinRequestGapMs) * time.Millisecond
		if ready := acc.lastReq.Add(gap); ready.After(now) {
			return ready.Sub(now)
		}
	}
	return 0
}

// beginRequestLocked must be called with r.mu held.
// Sole commit point for fresh sticky state: writes r.assign under the same lock
// hold as inFlight++ so cancel-before-lease never leaves a half-binding.
func (r *proxyRouter) beginRequestLocked(accountID, proxyID string) {
	now := time.Now()
	accountID = accountSlug(accountID)
	if proxyID != "" {
		rt := r.runtime(proxyID)
		rt.inFlight++
		rt.lastReq = now
		rt.recent = append(pruneRecent(rt.recent, now, time.Minute), now)
		if a, ok := r.assign[accountID]; !ok || a.ProxyID != proxyID || !a.StickyUntil.After(now) {
			r.assign[accountID] = proxyAssignment{
				ProxyID:     proxyID,
				StickyUntil: now.Add(time.Duration(r.cfg.StickyMinutes) * time.Minute),
			}
			r.freshBindCount++
			r.saveStateLocked()
		}
	}
	acc := r.accountRuntime(accountID)
	acc.lastReq = now
	acc.recent = append(pruneRecent(acc.recent, now, time.Minute), now)
}

// resetFreshBindCount clears the test-only fresh-bind counter.
func (r *proxyRouter) resetFreshBindCount() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.freshBindCount = 0
}

// freshBinds returns how many true sticky commits beginRequestLocked made.
func (r *proxyRouter) freshBinds() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.freshBindCount
}

func (r *proxyRouter) backoffDuration(failures int, retryAfter time.Duration) time.Duration {
	cfg := r.cfg.Backoff
	base := time.Duration(cfg.InitialSeconds) * time.Second
	if failures > 1 {
		base = time.Duration(float64(base) * math.Pow(cfg.Multiplier, float64(failures-1)))
	}
	max := time.Duration(cfg.MaxSeconds) * time.Second
	if base > max {
		base = max
	}
	if r.cfg.RespectRetryAfter && retryAfter > base {
		base = retryAfter
		if base > max {
			base = max
		}
	}
	if cfg.JitterPercent > 0 {
		j := float64(cfg.JitterPercent) / 100
		delta := 1 + (rand.Float64()*2-1)*j
		base = time.Duration(float64(base) * delta)
	}
	if base < time.Second {
		base = time.Second
	}
	return base
}

func parseRetryAfter(h http.Header) time.Duration {
	if h == nil {
		return 0
	}
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if sec, err := strconv.Atoi(v); err == nil && sec > 0 {
		return time.Duration(sec) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}

func (r *proxyRouter) reportResult(accountID, proxyID string, status int, reqErr error, hdr http.Header) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if proxyID == "" || !r.cfg.Enabled {
		return 0
	}
	accountID = accountSlug(accountID)
	rt := r.runtime(proxyID)
	retryAfter := parseRetryAfter(hdr)

	// 403 is usually account/session scoped (CSRF soft-fail, email verify, etc).
	// Break this account's sticky binding only — do not cool the shared proxy.
	if reqErr == nil && status == http.StatusForbidden {
		if a, ok := r.assign[accountID]; ok && a.ProxyID == proxyID {
			delete(r.assign, accountID)
			r.saveStateLocked()
			fmt.Printf("proxy: account sticky cleared for %s after 403 (proxy %s stays warm)\n",
				accountID, proxyLabel(r.byID[proxyID]))
		}
		return 0
	}

	cool := false
	switch {
	case reqErr != nil:
		rt.failures++
		cool = true
	case status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable:
		rt.failures++
		cool = true
	case status >= 500:
		rt.failures++
		cool = rt.failures >= 2
	default:
		rt.failures = 0
	}
	if !cool {
		r.saveStateLocked()
		return 0
	}

	wait := r.backoffDuration(rt.failures, retryAfter)
	until := time.Now().Add(wait)
	if until.After(rt.cooldownUntil) {
		rt.cooldownUntil = until
	}
	// Drop sticky bindings to this proxy so accounts reshuffle elsewhere.
	for acc, a := range r.assign {
		if a.ProxyID == proxyID {
			delete(r.assign, acc)
		}
	}
	r.saveStateLocked()
	fmt.Printf("proxy: cooldown %s for %s (account=%s status=%d failures=%d)\n",
		proxyLabel(r.byID[proxyID]), wait.Round(time.Second), accountID, status, rt.failures)
	return wait
}

func proxyLabel(ep *proxyEndpoint) string {
	if ep == nil {
		return "?"
	}
	return ep.label
}

// maxProxyRetries is the bounded retry budget for 429/503 after cooldown wait.
// 403 is not retried here — callers handle CSRF refresh if needed.
const maxProxyRetries = 2

// pacedProxyTransport enforces respectful gaps / RPM caps and feeds results
// back into the pool cooldown rules.
type pacedProxyTransport struct {
	base      http.RoundTripper
	accountID string
}

func (t *pacedProxyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	r := proxyPool()
	if !r.enabled() {
		return base.RoundTrip(req)
	}

	ctx := req.Context()
	cur := req
	var lastResp *http.Response
	var lastErr error
	for attempt := 0; attempt <= maxProxyRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		proxyID, err := r.acquireLease(ctx, t.accountID)
		if err != nil {
			return nil, err
		}

		// Panic-safe release around RoundTrip only; each retry re-acquires.
		// Tag ctx so dialChromeTLS sees the outer lease and skips a nested one.
		resp, reqErr := func() (resp *http.Response, err error) {
			defer r.releaseLease(proxyID)
			req := cur.WithContext(contextWithProxyLease(cur.Context(), proxyID))
			return base.RoundTrip(req)
		}()

		status := 0
		var hdr http.Header
		if resp != nil {
			status = resp.StatusCode
			hdr = resp.Header
		}
		wait := r.reportResult(t.accountID, proxyID, status, reqErr, hdr)
		canRetry := attempt < maxProxyRetries && wait > 0 &&
			(status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable) &&
			(cur.Body == nil || cur.GetBody != nil)
		if !canRetry {
			return resp, reqErr
		}

		// Always drain/close before sleep/retry (nil/partial responses included).
		closeProxyResponseBody(resp)
		lastResp, lastErr = resp, reqErr
		if err := r.doSleep(ctx, wait); err != nil {
			return lastResp, err
		}

		retryReq := cur.Clone(ctx)
		if cur.GetBody != nil {
			body, err := cur.GetBody()
			if err != nil {
				return nil, fmt.Errorf("proxy retry: recreate body: %w", err)
			}
			retryReq.Body = body
		}
		cur = retryReq
	}
	return lastResp, lastErr
}

// closeProxyResponseBody drains and closes a response body when present.
// Safe for nil resp / nil Body so retries never leak connections.
func closeProxyResponseBody(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
}

func wrapProxyPace(base http.RoundTripper, accountID string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &pacedProxyTransport{base: base, accountID: accountSlug(accountID)}
}

// proxyFuncFor returns an http.Transport.Proxy closure that sticky-routes
// this account through the shared pool (or nil when disabled).
func proxyFuncFor(accountID string) func(*http.Request) (*url.URL, error) {
	id := accountSlug(accountID)
	return func(req *http.Request) (*url.URL, error) {
		r := proxyPool()
		if !r.enabled() {
			return http.ProxyFromEnvironment(req)
		}
		return r.proxyURLFor(id)
	}
}

// proxyStatusSummary is a short operator-facing snapshot for logs / GUI later.
func proxyStatusSummary() string {
	r := proxyPool()
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.cfg.Enabled {
		return "proxy: disabled"
	}
	now := time.Now()
	parts := make([]string, 0, len(r.pool))
	for _, ep := range r.pool {
		rt := r.runtime(ep.id)
		stickies := 0
		for _, a := range r.assign {
			if a.ProxyID == ep.id && a.StickyUntil.After(now) {
				stickies++
			}
		}
		state := "ok"
		if rt.cooldownUntil.After(now) {
			state = fmt.Sprintf("cool %s", rt.cooldownUntil.Sub(now).Round(time.Second))
		}
		parts = append(parts, fmt.Sprintf("%s[%s stickies=%d inflight=%d]", ep.label, state, stickies, rt.inFlight))
	}
	return "proxy: " + strings.Join(parts, ", ")
}
