package main

import (
	"encoding/json"
	"fmt"
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

type proxyRouter struct {
	mu      sync.Mutex
	cfg     ProxyConfig
	pool    []*proxyEndpoint
	byID    map[string]*proxyEndpoint
	assign  map[string]proxyAssignment // accountID -> sticky proxy
	rt      map[string]*proxyRuntime   // proxyID -> runtime
	accRT   map[string]*proxyRuntime   // accountID -> rate window
	loaded  time.Time
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
		}
		proxyShared.reload()
	})
	return proxyShared
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
	if !r.cfg.Enabled || len(r.pool) == 0 {
		return nil, nil
	}
	now := time.Now()
	accountID = accountSlug(accountID)

	if a, ok := r.assign[accountID]; ok {
		if ep := r.byID[a.ProxyID]; ep != nil && a.StickyUntil.After(now) && r.healthyLocked(ep, now) {
			return ep, nil
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
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		si, sj := r.loadScoreLocked(candidates[i], now), r.loadScoreLocked(candidates[j], now)
		if si != sj {
			return si < sj
		}
		return candidates[i].id < candidates[j].id
	})
	ep := candidates[0]
	r.assign[accountID] = proxyAssignment{
		ProxyID:     ep.id,
		StickyUntil: now.Add(time.Duration(r.cfg.StickyMinutes) * time.Minute),
	}
	r.saveStateLocked()
	return ep, nil
}

func (r *proxyRouter) proxyURLFor(accountID string) (*url.URL, error) {
	ep, err := r.pickProxy(accountID)
	if err != nil {
		return nil, err
	}
	if ep == nil {
		return nil, nil
	}
	return ep.url, nil
}

func (r *proxyRouter) waitTurn(accountID, proxyID string) error {
	for {
		wait, err := r.nextWait(accountID, proxyID)
		if err != nil {
			return err
		}
		if wait <= 0 {
			return nil
		}
		time.Sleep(wait)
	}
}

func (r *proxyRouter) nextWait(accountID, proxyID string) (time.Duration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.cfg.Enabled {
		return 0, nil
	}
	now := time.Now()
	accountID = accountSlug(accountID)

	if proxyID != "" {
		rt := r.runtime(proxyID)
		if rt.cooldownUntil.After(now) {
			return rt.cooldownUntil.Sub(now), nil
		}
		if rt.inFlight >= r.cfg.PerProxyMaxConcurrent {
			return 250 * time.Millisecond, nil
		}
		rt.recent = pruneRecent(rt.recent, now, time.Minute)
		if len(rt.recent) >= r.cfg.PerProxyRPM {
			return rt.recent[0].Add(time.Minute).Sub(now) + time.Duration(rand.Intn(400))*time.Millisecond, nil
		}
		if !rt.lastReq.IsZero() {
			gap := time.Duration(r.cfg.MinRequestGapMs) * time.Millisecond
			if ready := rt.lastReq.Add(gap); ready.After(now) {
				return ready.Sub(now), nil
			}
		}
	}

	acc := r.accountRuntime(accountID)
	acc.recent = pruneRecent(acc.recent, now, time.Minute)
	if len(acc.recent) >= r.cfg.PerAccountRPM {
		return acc.recent[0].Add(time.Minute).Sub(now) + time.Duration(rand.Intn(400))*time.Millisecond, nil
	}
	if !acc.lastReq.IsZero() {
		gap := time.Duration(r.cfg.MinRequestGapMs) * time.Millisecond
		if ready := acc.lastReq.Add(gap); ready.After(now) {
			return ready.Sub(now), nil
		}
	}
	return 0, nil
}

func (r *proxyRouter) beginRequest(accountID, proxyID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if proxyID != "" {
		rt := r.runtime(proxyID)
		rt.inFlight++
		rt.lastReq = now
		rt.recent = append(pruneRecent(rt.recent, now, time.Minute), now)
	}
	acc := r.accountRuntime(accountSlug(accountID))
	acc.lastReq = now
	acc.recent = append(pruneRecent(acc.recent, now, time.Minute), now)
}

func (r *proxyRouter) endRequest(proxyID string) {
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
	rt := r.runtime(proxyID)
	retryAfter := parseRetryAfter(hdr)

	cool := false
	switch {
	case reqErr != nil:
		rt.failures++
		cool = true
	case status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable || status == http.StatusForbidden:
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
		proxyLabel(r.byID[proxyID]), wait.Round(time.Second), accountSlug(accountID), status, rt.failures)
	return wait
}

func proxyLabel(ep *proxyEndpoint) string {
	if ep == nil {
		return "?"
	}
	return ep.label
}

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

	ep, err := r.pickProxy(t.accountID)
	if err != nil {
		return nil, err
	}
	proxyID := ""
	if ep != nil {
		proxyID = ep.id
	}
	if err := r.waitTurn(t.accountID, proxyID); err != nil {
		return nil, err
	}
	// Re-pick after wait in case cooldown reshuffled the pool.
	ep, err = r.pickProxy(t.accountID)
	if err != nil {
		return nil, err
	}
	if ep != nil {
		proxyID = ep.id
	}

	r.beginRequest(t.accountID, proxyID)
	resp, reqErr := base.RoundTrip(req)
	r.endRequest(proxyID)

	status := 0
	var hdr http.Header
	if resp != nil {
		status = resp.StatusCode
		hdr = resp.Header
	}
	wait := r.reportResult(t.accountID, proxyID, status, reqErr, hdr)
	canRetry := wait > 0 && resp != nil &&
		(status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable) &&
		(req.Body == nil || req.GetBody != nil)
	if !canRetry {
		return resp, reqErr
	}
	// One respectful retry after honoring Retry-After / backoff.
	time.Sleep(wait)
	retryReq := req.Clone(req.Context())
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return resp, reqErr
		}
		retryReq.Body = body
	}
	if err := r.waitTurn(t.accountID, ""); err != nil {
		return resp, reqErr
	}
	ep2, _ := r.pickProxy(t.accountID)
	proxyID2 := ""
	if ep2 != nil {
		proxyID2 = ep2.id
	}
	r.beginRequest(t.accountID, proxyID2)
	resp2, err2 := base.RoundTrip(retryReq)
	r.endRequest(proxyID2)
	status2 := 0
	var hdr2 http.Header
	if resp2 != nil {
		status2 = resp2.StatusCode
		hdr2 = resp2.Header
	}
	r.reportResult(t.accountID, proxyID2, status2, err2, hdr2)
	resp.Body.Close()
	return resp2, err2
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
