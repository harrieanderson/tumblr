package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Daily AI budget shared by every run (CLI, GUI, reach). Groq's free tier is a
// per-model daily token cap; once a model hits it, calling it again only burns
// time, so we remember when it resets and skip it until then.
const aiQuotaFile = "data/ai-quota.json"

var errAIExhausted = errors.New("ai daily quota used up")

type aiQuotaState struct {
	BlockedUntil map[string]time.Time      `json:"blockedUntil"`
	Usage        map[string]map[string]int `json:"usage"` // day → model → tokens
}

var aiQuotaMu sync.Mutex

func loadAIQuota() aiQuotaState {
	s := aiQuotaState{}
	if raw, err := os.ReadFile(aiQuotaFile); err == nil {
		_ = json.Unmarshal(raw, &s)
	}
	if s.BlockedUntil == nil {
		s.BlockedUntil = map[string]time.Time{}
	}
	if s.Usage == nil {
		s.Usage = map[string]map[string]int{}
	}
	return s
}

func saveAIQuota(s aiQuotaState) {
	today := time.Now().Format("2006-01-02")
	for day := range s.Usage {
		if day < time.Now().AddDate(0, 0, -7).Format("2006-01-02") && day != today {
			delete(s.Usage, day)
		}
	}
	for m, t := range s.BlockedUntil {
		if time.Now().After(t) {
			delete(s.BlockedUntil, m)
		}
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(aiQuotaFile), 0o755)
	_ = os.WriteFile(aiQuotaFile, append(raw, '\n'), 0o644)
}

func aiQuotaKey(provider, model string) string {
	return provider + ":" + model
}

func recordAIUsage(provider, model string, tokens int) {
	if tokens <= 0 {
		return
	}
	aiQuotaMu.Lock()
	defer aiQuotaMu.Unlock()
	s := loadAIQuota()
	day := time.Now().Format("2006-01-02")
	if s.Usage[day] == nil {
		s.Usage[day] = map[string]int{}
	}
	s.Usage[day][aiQuotaKey(provider, model)] += tokens
	saveAIQuota(s)
}

func aiModelBlockedUntil(provider, model string) (time.Time, bool) {
	aiQuotaMu.Lock()
	defer aiQuotaMu.Unlock()
	t, ok := loadAIQuota().BlockedUntil[aiQuotaKey(provider, model)]
	if !ok || time.Now().After(t) {
		return time.Time{}, false
	}
	return t, true
}

func blockAIModel(provider, model string, until time.Time) {
	aiQuotaMu.Lock()
	defer aiQuotaMu.Unlock()
	s := loadAIQuota()
	s.BlockedUntil[aiQuotaKey(provider, model)] = until
	saveAIQuota(s)
}

// aiOutUntil reports whether every model a reply could use is blocked, and
// when the first of them frees up.
func aiOutUntil(cfg AIConfig) (time.Time, bool) {
	if !cfg.Enabled || cfg.Provider == "ollama" {
		return time.Time{}, false
	}
	var soonest time.Time
	check := func(provider, model string) bool {
		t, blocked := aiModelBlockedUntil(provider, model)
		if blocked && (soonest.IsZero() || t.Before(soonest)) {
			soonest = t
		}
		return blocked
	}
	models := []string{cfg.Model}
	if cfg.Provider == "groq" {
		models = append(models, groqFallbackModels(cfg.Model)...)
	}
	for _, m := range models {
		if !check(cfg.Provider, m) {
			return time.Time{}, false
		}
	}
	if cfg.Provider == "groq" {
		or := cfg
		or.Provider, or.APIKey = "openrouter", ""
		if resolveAIAPIKey(or) != "" && os.Getenv("AI_DISABLE_OPENROUTER") != "1" && !check("openrouter", "*") {
			return time.Time{}, false
		}
	}
	return soonest, true
}

var groqRetryInRe = regexp.MustCompile(`try again in ((?:\d+h)?(?:\d+m)?(?:[\d.]+s)?)`)

// dailyLimitReset reads a Groq 429 body. Only per-day limits (TPD/RPD) are
// worth remembering; per-minute ones clear on their own.
func dailyLimitReset(errText string) (time.Time, bool) {
	l := strings.ToLower(errText)
	if !strings.Contains(l, "per day") && !strings.Contains(l, "(tpd)") && !strings.Contains(l, "(rpd)") {
		return time.Time{}, false
	}
	wait := time.Hour
	if m := groqRetryInRe.FindStringSubmatch(l); m != nil && m[1] != "" {
		if d, err := time.ParseDuration(m[1]); err == nil && d > 0 {
			wait = d
		}
	}
	return time.Now().Add(wait + time.Minute), true
}

// OpenRouter's free daily allowance resets at 00:00 UTC.
func nextUTCMidnight() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
}

// aiBudgetSummary is printed at the start of a messaging run.
func aiBudgetSummary() string {
	aiQuotaMu.Lock()
	s := loadAIQuota()
	aiQuotaMu.Unlock()
	day := time.Now().Format("2006-01-02")
	var parts []string
	keys := make([]string, 0, len(s.Usage[day]))
	for k := range s.Usage[day] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %sk", k, strconv.Itoa(s.Usage[day][k]/1000)))
	}
	for k, t := range s.BlockedUntil {
		if time.Now().Before(t) {
			parts = append(parts, fmt.Sprintf("%s out until %s", k, t.Format("15:04")))
		}
	}
	if len(parts) == 0 {
		return "AI today: nothing used yet"
	}
	return "AI today: " + strings.Join(parts, " · ")
}
