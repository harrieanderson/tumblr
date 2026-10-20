package main

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"
)

// The research tuner feeds results back into reach. Each run orders its seed
// blogs and tags by a Thompson sample of their win rate: proven sources tend
// to go first and get more posts, untested ones still get explored, and
// proven duds drop to a single post until someone removes them.
const (
	tunerLookback    = 30 * 24 * time.Hour
	tunerCacheFor    = 20 * time.Minute
	tunerMinTrials   = 50 // fleet-wide fair-chance touches before tuning kicks in
	tunerPriorWeight = 20
	tunerBoostPosts  = 2

	tunerBoost   = "boost"
	tunerNormal  = "normal"
	tunerCut     = "cut"
	tunerExplore = "explore"
)

type sourceScore struct {
	Key    string  `json:"key"`
	Kind   string  `json:"kind"`
	Source string  `json:"source"`
	Trials int     `json:"trials"`
	Wins   int     `json:"wins"`
	Rate   float64 `json:"rate"`
	Low    float64 `json:"low"`
	High   float64 `json:"high"`
	Mode   string  `json:"mode"`
}

func tunerKey(kind, source string) string {
	return kind + ":" + strings.ToLower(strings.TrimSpace(source))
}

// scoreSources rates every seed blog and tag on fair-chance touches.
func scoreSources(touches []touchOutcome) []sourceScore {
	by := map[string]*sourceScore{}
	fleetW, fleetT := 0, 0
	for _, t := range touches {
		if (t.Kind != "seed" && t.Kind != "tag") || t.Source == "" || !t.trial() {
			continue
		}
		k := tunerKey(t.Kind, t.Source)
		s := by[k]
		if s == nil {
			s = &sourceScore{Key: k, Kind: t.Kind, Source: strings.ToLower(t.Source)}
			by[k] = s
		}
		s.Trials++
		fleetT++
		if t.win() {
			s.Wins++
			fleetW++
		}
	}
	base := 0.0
	if fleetT > 0 {
		base = float64(fleetW) / float64(fleetT)
	}
	out := make([]sourceScore, 0, len(by))
	for _, s := range by {
		s.Rate = float64(s.Wins) / float64(s.Trials)
		s.Low, s.High = wilson(s.Wins, s.Trials)
		switch {
		case s.Trials < researchMinSampleRate:
			s.Mode = tunerExplore
		case base > 0 && s.Low > base:
			s.Mode = tunerBoost
		case (s.Wins == 0 && s.Trials >= 2*researchMinSampleRate) || (base > 0 && s.High < base/2):
			s.Mode = tunerCut
		default:
			s.Mode = tunerNormal
		}
		out = append(out, *s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Low != out[j].Low {
			return out[i].Low > out[j].Low
		}
		return out[i].Key < out[j].Key
	})
	return out
}

var tunerCache struct {
	sync.Mutex
	at     time.Time
	scores map[string]sourceScore
	wins   int
	trials int
}

func tunerScores() (map[string]sourceScore, int, int) {
	tunerCache.Lock()
	defer tunerCache.Unlock()
	if tunerCache.scores != nil && time.Since(tunerCache.at) < tunerCacheFor {
		return tunerCache.scores, tunerCache.wins, tunerCache.trials
	}
	now := time.Now()
	touches := researchOutcomes(loadAllSeenSets("followers"), loadAllSeenSets("dm"), now.Add(-tunerLookback), now)
	m := map[string]sourceScore{}
	w, t := 0, 0
	for _, s := range scoreSources(touches) {
		m[s.Key] = s
		w += s.Wins
		t += s.Trials
	}
	tunerCache.at, tunerCache.scores, tunerCache.wins, tunerCache.trials = now, m, w, t
	return m, w, t
}

func resetTunerCache() {
	tunerCache.Lock()
	tunerCache.scores = nil
	tunerCache.Unlock()
}

// tuneSourceOrder reorders items (seed blogs or tag queries) for one run and
// says how each should be fed. Until there is enough data it keeps config
// order so behaviour only changes once results justify it.
func tuneSourceOrder(kind string, items []string, norm func(string) string) ([]string, map[string]string, string) {
	scores, wins, trials := tunerScores()
	if trials < tunerMinTrials || len(items) < 2 {
		return items, nil, ""
	}
	base := float64(wins) / float64(trials)
	a0 := math.Max(0.5, base*tunerPriorWeight)
	b0 := math.Max(1, (1-base)*tunerPriorWeight)
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	type pick struct {
		item   string
		sample float64
	}
	picks := make([]pick, 0, len(items))
	modes := map[string]string{}
	var boosted, cut []string
	explore := 0
	for _, it := range items {
		s, ok := scores[tunerKey(kind, norm(it))]
		mode := tunerExplore
		if ok {
			mode = s.Mode
		}
		modes[it] = mode
		switch mode {
		case tunerBoost:
			boosted = append(boosted, norm(it))
		case tunerCut:
			cut = append(cut, norm(it))
		case tunerExplore:
			explore++
		}
		picks = append(picks, pick{it, betaSample(r, a0+float64(s.Wins), b0+float64(s.Trials-s.Wins))})
	}
	sort.SliceStable(picks, func(i, j int) bool { return picks[i].sample > picks[j].sample })
	out := make([]string, len(picks))
	for i, p := range picks {
		out[i] = p.item
	}
	summary := fmt.Sprintf("research tuner (%s): fleet %s over %d touches", kind, pct(base), trials)
	if len(boosted) > 0 {
		summary += " · boost " + strings.Join(clipStrings(boosted, 4), ", ")
	}
	if len(cut) > 0 {
		summary += " · cut " + strings.Join(clipStrings(cut, 4), ", ")
	}
	if explore > 0 {
		summary += fmt.Sprintf(" · exploring %d", explore)
	}
	return out, modes, summary
}

// tunedPostCount scales the posts taken from one source by its mode.
func tunedPostCount(base int, mode string) int {
	switch mode {
	case tunerBoost:
		return base + tunerBoostPosts
	case tunerCut:
		return 1
	}
	return base
}
