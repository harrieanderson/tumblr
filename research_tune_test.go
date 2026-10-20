package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWilsonIsHonestOnSmallSamples(t *testing.T) {
	lo, hi := wilson(1, 1)
	if lo > 0.25 || hi < 0.99 {
		t.Fatalf("1/1 should be a wide range, got %.2f–%.2f", lo, hi)
	}
	lo, hi = wilson(30, 1000)
	if lo < 0.02 || hi > 0.045 {
		t.Fatalf("30/1000 should be tight around 3%%, got %.3f–%.3f", lo, hi)
	}
	if lo, hi = wilson(0, 0); lo != 0 || hi != 1 {
		t.Fatalf("no data should be 0–1, got %.2f–%.2f", lo, hi)
	}
}

func TestBetaSampleMean(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	sum := 0.0
	for i := 0; i < 20000; i++ {
		sum += betaSample(r, 3, 27)
	}
	if mean := sum / 20000; math.Abs(mean-0.1) > 0.01 {
		t.Fatalf("Beta(3,27) mean should be ~0.10, got %.3f", mean)
	}
	sum = 0
	for i := 0; i < 20000; i++ {
		sum += betaSample(r, 0.5, 1)
	}
	if mean := sum / 20000; math.Abs(mean-1.0/3) > 0.02 {
		t.Fatalf("Beta(0.5,1) mean should be ~0.33, got %.3f", mean)
	}
}

// fakeOutcomes builds n matured touches from source with wins of them winning.
func fakeOutcomes(kind, source string, n, wins int) []touchOutcome {
	var out []touchOutcome
	for i := 0; i < n; i++ {
		out = append(out, touchOutcome{
			researchTouch: researchTouch{Account: "a", Target: fmt.Sprintf("%s-%d", source, i), Kind: kind, Source: source},
			matured:       true,
			followback:    i < wins,
		})
	}
	return out
}

func TestScoreSourcesModes(t *testing.T) {
	var all []touchOutcome
	all = append(all, fakeOutcomes("seed", "hot", 100, 25)...)
	all = append(all, fakeOutcomes("seed", "meh", 200, 10)...)
	all = append(all, fakeOutcomes("seed", "dead", 40, 0)...)
	all = append(all, fakeOutcomes("seed", "new", 5, 1)...)
	all = append(all, fakeOutcomes("tag", "goth", 60, 4)...)
	young := fakeOutcomes("seed", "meh", 50, 0)
	for i := range young {
		young[i].matured = false
		young[i].Target += "-young"
	}
	all = append(all, young...)

	got := map[string]sourceScore{}
	for _, s := range scoreSources(all) {
		got[s.Key] = s
	}
	want := map[string]string{"seed:hot": tunerBoost, "seed:meh": tunerNormal, "seed:dead": tunerCut, "seed:new": tunerExplore, "tag:goth": tunerNormal}
	for k, mode := range want {
		if got[k].Mode != mode {
			t.Errorf("%s mode=%s want %s (%+v)", k, got[k].Mode, mode, got[k])
		}
	}
	if got["seed:meh"].Trials != 200 {
		t.Fatalf("young touches with no win must not count as trials: %+v", got["seed:meh"])
	}
}

func TestJudgeVerdicts(t *testing.T) {
	mk := func(key string, wins, trials int) researchStat {
		return finishStat(researchStat{Key: key, Wins: wins, Trials: trials, Touches: trials, accs: map[string]bool{}})
	}
	fleet := mk("fleet", 60, 1000)
	rows := []researchStat{mk("tiny", 3, 5), mk("great", 30, 150), mk("zero", 0, 40), mk("fine", 6, 100), mk("weak", 3, 400)}
	judge(rows, fleet)
	got := map[string]string{}
	for _, r := range rows {
		got[r.Key] = r.Verdict
	}
	want := map[string]string{"tiny": verdictTesting, "great": verdictWinner, "zero": verdictDud, "fine": verdictAverage, "weak": verdictDud}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s verdict=%q want %q", k, got[k], v)
		}
	}
	if rows[0].Key != "great" {
		t.Fatalf("proven winner should rank first, got %s", rows[0].Key)
	}
	for _, r := range rows[len(rows)-1:] {
		if r.Key != "tiny" {
			t.Fatalf("small samples rank last, got %s", r.Key)
		}
	}
}

func TestTuneSourceOrderFavoursWinnersAndCutsDuds(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd); resetTunerCache() })
	_ = os.Chdir(dir)
	resetTunerCache()

	now := time.Now()
	touchAt := now.Add(-72 * time.Hour)
	names := map[string]string{}
	add := func(source string, n, wins int) {
		for i := 0; i < n; i++ {
			target := fmt.Sprintf("%s-%d", source, i)
			recordResearchTouchAt(touchAt, "acc1", target, "seed", source)
			if i < wins {
				names[target] = touchAt.Add(5 * time.Hour).Format(time.RFC3339)
			}
		}
	}
	add("hot", 80, 24)
	add("cold", 80, 0)
	add("mid", 80, 5)
	_ = saveSeenSet("followers", "acc1", seenSet{BaselineAt: now.Add(-96 * time.Hour).Format(time.RFC3339), Names: names})

	firstHot := 0
	for i := 0; i < 200; i++ {
		order, modes, summary := tuneSourceOrder("seed", []string{"cold", "mid", "hot", "fresh"}, normalizeBlogName)
		if modes["hot"] != tunerBoost || modes["cold"] != tunerCut || modes["fresh"] != tunerExplore {
			t.Fatalf("modes %+v", modes)
		}
		if !strings.Contains(summary, "boost hot") || !strings.Contains(summary, "cut cold") {
			t.Fatalf("summary %q", summary)
		}
		if order[0] == "hot" {
			firstHot++
		}
		if order[0] == "cold" {
			t.Fatalf("a proven dud should never lead: %v", order)
		}
	}
	if firstHot < 120 {
		t.Fatalf("hot seed led only %d/200 runs", firstHot)
	}
	if tunedPostCount(4, tunerBoost) != 6 || tunedPostCount(4, tunerCut) != 1 || tunedPostCount(4, tunerExplore) != 4 {
		t.Fatal("post counts per mode")
	}
}

func TestTuneSourceOrderKeepsConfigOrderWithoutData(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd); resetTunerCache() })
	_ = os.Chdir(dir)
	resetTunerCache()
	in := []string{"a", "b", "c"}
	out, modes, summary := tuneSourceOrder("seed", in, normalizeBlogName)
	if strings.Join(out, ",") != "a,b,c" || modes != nil || summary != "" {
		t.Fatalf("expected untouched order, got %v %v %q", out, modes, summary)
	}
}

func TestResearchActionsFlagDudsAndProfiles(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	_ = os.Chdir(dir)
	rep := researchReport{
		Fleet: finishStat(researchStat{Wins: 60, Trials: 1000, accs: map[string]bool{}}),
		Sources: []researchStat{
			{Key: "seed:great", Verdict: verdictWinner, Lift: 3.1},
			{Key: "seed:dead", Verdict: verdictDud, Wins: 0, Trials: 40},
		},
		Accounts: []researchAccount{
			{ID: "nobanner", Verdict: verdictBelow, WinRate: 0.01, Setup: "essential (no banner)", TrackedSince: "x"},
			{ID: "leaky", Lost: 5, NewFollowers: 1, Verdict: verdictAverage, TrackedSince: "x"},
		},
	}
	var text []string
	for _, a := range buildResearchActions(rep, time.Now()) {
		text = append(text, a.Text)
	}
	all := strings.Join(text, "\n")
	for _, want := range []string{"seed:great 3.1x", "seed:dead (0/40)", "nobanner converts", "no banner", "leaky lost 5"} {
		if !strings.Contains(all, want) {
			t.Errorf("actions missing %q:\n%s", want, all)
		}
	}
}

func recordResearchTouchAt(at time.Time, account, target, kind, source string) {
	recordResearchTouch(account, target, ScrapedPost{SourceKind: kind, Source: source}, true, false, true)
	raw, _ := os.ReadFile(researchTouchesFile)
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	last := lines[len(lines)-1]
	i := strings.Index(last, `"at":"`)
	j := strings.Index(last[i+6:], `"`)
	lines[len(lines)-1] = last[:i+6] + at.Format(time.RFC3339) + last[i+6+j:]
	_ = os.WriteFile(researchTouchesFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}
