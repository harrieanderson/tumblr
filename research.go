package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Research tracking joins what reach did (touches) with what came back
// (followers and DM partners first seen after the touch), so seeds, accounts
// and engage styles can be compared on results instead of on effort.
const (
	researchDir            = "data/research"
	researchTouchesFile    = "data/research/touches.jsonl"
	researchReportFile     = "data/research/report.json"
	researchReportText     = "research-report.txt"
	researchOutcomeWindow  = 14 * 24 * time.Hour
	researchMinSampleRate  = 15
	researchTouchClockSkew = 10 * time.Minute
	researchMatureAfter    = 48 * time.Hour
)

type researchTouch struct {
	At        string `json:"at"`
	Account   string `json:"account"`
	Target    string `json:"target"`
	Kind      string `json:"kind"`
	Source    string `json:"source"`
	PostBlog  string `json:"postBlog,omitempty"`
	PostID    string `json:"postId,omitempty"`
	Liked     bool   `json:"liked"`
	Reblogged bool   `json:"reblogged"`
	Followed  bool   `json:"followed"`
	Legacy    bool   `json:"legacy,omitempty"`
}

func recordResearchTouch(accountID, target string, post ScrapedPost, liked, reblogged, followed bool) {
	accountID = accountSlug(accountID)
	target = normalizeBlogName(target)
	if accountID == "" || target == "" {
		return
	}
	kind := strings.ToLower(strings.TrimSpace(post.SourceKind))
	if kind == "" {
		kind = "unknown"
	}
	t := researchTouch{
		At:        time.Now().Format(time.RFC3339),
		Account:   accountID,
		Target:    target,
		Kind:      kind,
		Source:    strings.ToLower(strings.TrimSpace(post.Source)),
		PostBlog:  normalizeBlogName(post.Blog),
		PostID:    post.ID,
		Liked:     liked,
		Reblogged: reblogged,
		Followed:  followed,
	}
	raw, err := json.Marshal(t)
	if err != nil {
		return
	}
	_ = os.MkdirAll(researchDir, 0o755)
	f, err := os.OpenFile(researchTouchesFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(raw, '\n'))
}

func loadResearchTouches() []researchTouch {
	raw, err := os.ReadFile(researchTouchesFile)
	if err != nil {
		return nil
	}
	var out []researchTouch
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var t researchTouch
		if json.Unmarshal([]byte(line), &t) == nil && t.Account != "" && t.Target != "" {
			out = append(out, t)
		}
	}
	return out
}

// seenSet records when each name was first seen. Names present at the first
// snapshot are baseline ("") so pre-tracking followers never get credited.
type seenSet struct {
	Blog       string            `json:"blog,omitempty"`
	BaselineAt string            `json:"baselineAt"`
	UpdatedAt  string            `json:"updatedAt"`
	Names      map[string]string `json:"names"`
	Lost       map[string]string `json:"lost,omitempty"`
}

func researchSetPath(kind, id string) string {
	return filepath.Join(researchDir, kind, accountSlug(id)+".json")
}

func loadSeenSet(kind, id string) seenSet {
	var s seenSet
	if raw, err := os.ReadFile(researchSetPath(kind, id)); err == nil {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

func saveSeenSet(kind, id string, s seenSet) error {
	path := researchSetPath(kind, id)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644)
}

// mergeSeen folds a fresh full listing into s. trackLost marks names that
// vanished; skip that when the listing may be partial.
func mergeSeen(s seenSet, current []string, now time.Time, trackLost bool) (seenSet, int, int) {
	stamp := now.Format(time.RFC3339)
	first := s.Names == nil
	if first {
		s.Names = map[string]string{}
		s.BaselineAt = stamp
	}
	if s.Lost == nil {
		s.Lost = map[string]string{}
	}
	cur := map[string]bool{}
	added := 0
	for _, n := range current {
		k := normalizeBlogName(n)
		if k == "" {
			continue
		}
		cur[k] = true
		delete(s.Lost, k)
		if _, ok := s.Names[k]; ok {
			continue
		}
		if first {
			s.Names[k] = ""
		} else {
			s.Names[k] = stamp
			added++
		}
	}
	lost := 0
	if trackLost && !first {
		for k := range s.Names {
			if !cur[k] {
				if _, already := s.Lost[k]; !already {
					s.Lost[k] = stamp
					lost++
				}
			}
		}
	}
	s.UpdatedAt = stamp
	return s, added, lost
}

func syncResearchFollowers(a Account, now time.Time) (added, lost int, err error) {
	if accountIsTerminated(a.ID) {
		return 0, 0, fmt.Errorf("terminated")
	}
	// loadAuthFrom keeps proxy/CSRF/save on this account (peek leaves path empty).
	auth, err := loadAuthFrom(accountAuthPath(a.ID))
	if err != nil || !authHasSID(auth) {
		auth = Auth{path: accountAuthPath(a.ID)}
		if !headlessChrome {
			client := newAuthClient(&auth)
			if rec := recoverSession(client, &auth, true); rec != nil {
				return 0, 0, fmt.Errorf("no session: %w", rec)
			}
		} else {
			return 0, 0, fmt.Errorf("no session")
		}
	}
	client := newAuthClient(&auth)
	if err := refreshCSRF(client, &auth); err != nil {
		// Dead cookie — open Chrome, mint a new sid, then retry once.
		if isCookieProblem(err) && !headlessChrome {
			if rec := recoverSession(client, &auth, true); rec != nil {
				return 0, 0, err
			}
			if err2 := refreshCSRF(client, &auth); err2 != nil {
				return 0, 0, err2
			}
		} else {
			return 0, 0, err
		}
	}
	blogs, err := listAccountBlogsDetailed(client, auth)
	if err != nil {
		if isCookieProblem(err) && !headlessChrome {
			if rec := recoverSession(client, &auth, true); rec == nil {
				blogs, err = listAccountBlogsDetailed(client, auth)
			}
		}
		if err != nil {
			return 0, 0, err
		}
	}
	var primary tumblrBlog
	for _, b := range blogs {
		if b.Primary {
			primary = b
			break
		}
	}
	if primary.Name == "" && len(blogs) > 0 {
		primary = blogs[0]
	}
	if primary.Name == "" {
		return 0, 0, fmt.Errorf("no blog")
	}
	id := primary.UUID
	if id == "" {
		id = primary.Name
	}
	names, err := fetchBlogFollowerNames(client, auth, id)
	if err != nil {
		return 0, 0, err
	}
	s := loadSeenSet("followers", a.ID)
	active := len(s.Names) - len(s.Lost)
	// A listing far short of the known count is more likely a cut-off page
	// than half the followers leaving at once.
	trackLost := primary.Followers <= 0 || len(names) >= primary.Followers*9/10
	if active > 20 && len(names) < active/2 {
		trackLost = false
	}
	s, added, lost = mergeSeen(s, names, now, trackLost)
	s.Blog = primary.Name
	return added, lost, saveSeenSet("followers", a.ID, s)
}

func syncResearchDMPartners(a Account, now time.Time) (int, error) {
	raw, err := os.ReadFile(dmReplySetsPathFor(a.ID))
	if err != nil {
		if os.IsNotExist(err) {
			raw = []byte("{}")
		} else {
			return 0, err
		}
	}
	var sets map[string]int
	if err := json.Unmarshal(raw, &sets); err != nil {
		return 0, err
	}
	names := make([]string, 0, len(sets))
	for n := range sets {
		names = append(names, n)
	}
	s, added, _ := mergeSeen(loadSeenSet("dm", a.ID), names, now, false)
	return added, saveSeenSet("dm", a.ID, s)
}

func syncResearch(accs []Account, logFn func(string)) {
	now := time.Now()
	okN, newF, lostF, newDM := 0, 0, 0, 0
	for i, a := range accs {
		dm, _ := syncResearchDMPartners(a, now)
		newDM += dm
		added, lost, err := syncResearchFollowers(a, now)
		for try := 1; err != nil && try < 3 && (isTransientNet(err) || strings.Contains(err.Error(), "deadline exceeded")); try++ {
			time.Sleep(time.Duration(2*try) * time.Second)
			added, lost, err = syncResearchFollowers(a, now)
		}
		if err != nil {
			logFn(fmt.Sprintf("Research %d/%d %s: followers not synced (%v)", i+1, len(accs), a.ID, err))
			continue
		}
		okN++
		newF += added
		lostF += lost
		time.Sleep(300 * time.Millisecond)
	}
	logFn(fmt.Sprintf("Research sync: %d/%d accounts, +%d new followers, -%d lost, +%d new DM partners", okN, len(accs), newF, lostF, newDM))
}

// ---- report ----

type researchStat struct {
	Key            string  `json:"key"`
	Touches        int     `json:"touches"`
	Liked          int     `json:"liked"`
	Reblogged      int     `json:"reblogged"`
	Followed       int     `json:"followed"`
	Followbacks    int     `json:"followbacks"`
	DMs            int     `json:"dms"`
	Accounts       int     `json:"accounts,omitempty"`
	Matured        int     `json:"matured"`
	FollowbackRate float64 `json:"followbackRate"`
	DMRate         float64 `json:"dmRate"`
	MedianLagHours float64 `json:"medianLagHours,omitempty"`
	LowSample      bool    `json:"lowSample,omitempty"`
	Trials         int     `json:"trials"`
	Wins           int     `json:"wins"`
	WinRate        float64 `json:"winRate"`
	WinLow         float64 `json:"winLow"`
	WinHigh        float64 `json:"winHigh"`
	Lift           float64 `json:"lift,omitempty"`
	Verdict        string  `json:"verdict,omitempty"`
	accs           map[string]bool
	lags           []float64
}

type researchAccount struct {
	ID           string  `json:"id"`
	Blog         string  `json:"blog"`
	Followers    int     `json:"followers"`
	Gain1d       int     `json:"gain1d"`
	Gain7d       int     `json:"gain7d"`
	NewFollowers int     `json:"newFollowers"`
	Attributed   int     `json:"attributed"`
	Organic      int     `json:"organic"`
	Lost         int     `json:"lost"`
	NewDMs       int     `json:"newDMs"`
	Touches      int     `json:"touches"`
	Followbacks  int     `json:"followbacks"`
	TouchDMs     int     `json:"touchDMs"`
	Rate         float64 `json:"followbackRate"`
	BestSource   string  `json:"bestSource,omitempty"`
	TrackedSince string  `json:"trackedSince,omitempty"`
	Trials       int     `json:"trials"`
	Wins         int     `json:"wins"`
	WinRate      float64 `json:"winRate"`
	WinLow       float64 `json:"winLow"`
	WinHigh      float64 `json:"winHigh"`
	Verdict      string  `json:"verdict,omitempty"`
	DMChats      int     `json:"dmChats"`
	DMNudged     int     `json:"dmNudged"`
	Setup        string  `json:"setup,omitempty"`
}

type researchReport struct {
	At           string            `json:"at"`
	WindowDays   int               `json:"windowDays"`
	TrackedSince string            `json:"trackedSince"`
	Fleet        researchStat      `json:"fleet"`
	NewFollowers int               `json:"newFollowers"`
	Organic      int               `json:"organic"`
	Lost         int               `json:"lost"`
	Sources      []researchStat    `json:"sources"`
	Accounts     []researchAccount `json:"accounts"`
	Pairs        []researchStat    `json:"pairs"`
	TouchTypes   []researchStat    `json:"touchTypes"`
	Hours        []researchStat    `json:"hours"`
	Messaging    dmReport          `json:"messaging"`
	Daily        []researchDay     `json:"daily"`
	Actions      []researchAction  `json:"actions"`
	Tuner        []sourceScore     `json:"tuner"`
}

type touchOutcome struct {
	researchTouch
	at         time.Time
	followback bool
	dm         bool
	matured    bool
	lagHours   float64
}

// collapsedTouches merges repeat touches on the same person by the same
// account (earliest time, all actions), and folds in follows logged before
// the touch log existed.
func collapsedTouches(since time.Time) []touchOutcome {
	byKey := map[string]*touchOutcome{}
	add := func(t researchTouch) {
		at := parseFollowerAt(t.At)
		if at.IsZero() || at.Before(since) {
			return
		}
		k := t.Account + "|" + t.Target
		cur := byKey[k]
		if cur == nil {
			byKey[k] = &touchOutcome{researchTouch: t, at: at}
			return
		}
		if at.Before(cur.at) {
			cur.at, cur.At, cur.Kind, cur.Source = at, t.At, t.Kind, t.Source
		}
		cur.Liked = cur.Liked || t.Liked
		cur.Reblogged = cur.Reblogged || t.Reblogged
		cur.Followed = cur.Followed || t.Followed
	}
	for _, t := range loadResearchTouches() {
		add(t)
	}
	for _, ev := range loadFollowAttributionSince(since) {
		k := ev.Account + "|" + normalizeBlogName(ev.Target)
		if _, ok := byKey[k]; ok {
			continue
		}
		add(researchTouch{At: ev.At, Account: ev.Account, Target: normalizeBlogName(ev.Target), Kind: ev.Kind, Source: ev.Source, Followed: true, Legacy: true})
	}
	out := make([]touchOutcome, 0, len(byKey))
	for _, t := range byKey {
		out = append(out, *t)
	}
	return out
}

// researchOutcomes joins touches with followers / DM partners first seen
// after them. Only touches after the account's baseline count: anyone touched
// earlier who already followed back sits in the baseline and could never be
// credited.
func researchOutcomes(fol, dms map[string]seenSet, since, now time.Time) []touchOutcome {
	var touches []touchOutcome
	for _, t := range collapsedTouches(since) {
		base := parseFollowerAt(fol[t.Account].BaselineAt)
		if base.IsZero() || t.at.Before(base) {
			continue
		}
		t.matured = now.Sub(t.at) >= researchMatureAfter
		if at, ok := firstSeenAfter(fol[t.Account], t.Target, t.at); ok {
			t.followback = true
			t.lagHours = at.Sub(t.at).Hours()
		}
		if _, ok := firstSeenAfter(dms[t.Account], t.Target, t.at); ok {
			t.dm = true
		}
		touches = append(touches, t)
	}
	return touches
}

// win is the outcome every ranking uses: they followed back or opened a DM.
func (t touchOutcome) win() bool { return t.followback || t.dm }

// trial is whether the touch has had its full chance yet. Rates only use
// matured touches: counting young winners alone would read as 100% early on.
func (t touchOutcome) trial() bool { return t.matured }

func loadAllSeenSets(kind string) map[string]seenSet {
	out := map[string]seenSet{}
	entries, err := os.ReadDir(filepath.Join(researchDir, kind))
	if err != nil {
		return out
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		out[id] = loadSeenSet(kind, id)
	}
	return out
}

func firstSeenAfter(s seenSet, name string, touch time.Time) (time.Time, bool) {
	stamp, ok := s.Names[name]
	if !ok || stamp == "" {
		return time.Time{}, false
	}
	at := parseFollowerAt(stamp)
	if at.IsZero() || at.Before(touch.Add(-researchTouchClockSkew)) || at.After(touch.Add(researchOutcomeWindow)) {
		return time.Time{}, false
	}
	return at, true
}

func touchTypeLabel(t researchTouch) string {
	var parts []string
	if t.Liked {
		parts = append(parts, "like")
	}
	if t.Reblogged {
		parts = append(parts, "reblog")
	}
	if t.Followed {
		parts = append(parts, "follow")
	}
	if t.Legacy {
		return "follow (before touch log)"
	}
	if len(parts) == 0 {
		return "viewed only"
	}
	return strings.Join(parts, " + ")
}

func buildResearchReport(accs []Account, days int, now time.Time) researchReport {
	if days <= 0 {
		days = 14
	}
	since := now.Add(-time.Duration(days) * 24 * time.Hour)
	rep := researchReport{At: now.Format(time.RFC3339), WindowDays: days}

	fol := map[string]seenSet{}
	dms := map[string]seenSet{}
	tracked := ""
	for _, a := range accs {
		f := loadSeenSet("followers", a.ID)
		fol[a.ID] = f
		dms[a.ID] = loadSeenSet("dm", a.ID)
		if f.BaselineAt != "" && (tracked == "" || f.BaselineAt < tracked) {
			tracked = f.BaselineAt
		}
	}
	rep.TrackedSince = tracked
	touches := researchOutcomes(fol, dms, since, now)

	group := func(key func(touchOutcome) string) []researchStat {
		by := map[string]*researchStat{}
		for _, t := range touches {
			k := key(t)
			if k == "" {
				continue
			}
			s := by[k]
			if s == nil {
				s = &researchStat{Key: k, accs: map[string]bool{}}
				by[k] = s
			}
			addOutcome(s, t)
		}
		out := make([]researchStat, 0, len(by))
		for _, s := range by {
			out = append(out, finishStat(*s))
		}
		sort.SliceStable(out, func(i, j int) bool { return statBetter(out[i], out[j]) })
		return out
	}

	fleet := researchStat{Key: "fleet", accs: map[string]bool{}}
	for _, t := range touches {
		addOutcome(&fleet, t)
	}
	rep.Fleet = finishStat(fleet)
	rep.Sources = group(func(t touchOutcome) string {
		if t.Source == "" {
			return t.Kind
		}
		return t.Kind + ":" + t.Source
	})
	rep.Pairs = group(func(t touchOutcome) string {
		src := t.Source
		if src == "" {
			src = t.Kind
		}
		return t.Account + " <- " + src
	})
	rep.TouchTypes = group(func(t touchOutcome) string { return touchTypeLabel(t.researchTouch) })
	rep.Hours = group(func(t touchOutcome) string { return fmt.Sprintf("%02d:00", t.at.Hour()) })
	for _, rows := range [][]researchStat{rep.Sources, rep.Pairs, rep.TouchTypes, rep.Hours} {
		judge(rows, rep.Fleet)
	}
	sort.SliceStable(rep.Hours, func(i, j int) bool { return rep.Hours[i].Key < rep.Hours[j].Key })

	perAcc := map[string][]touchOutcome{}
	for _, t := range touches {
		perAcc[t.Account] = append(perAcc[t.Account], t)
	}
	for _, a := range accs {
		g := scoreGrowthAccount(a, now)
		row := researchAccount{ID: a.ID, Blog: g.Blog, Followers: g.Followers, Gain1d: g.Gain1d, Gain7d: g.Gain7d, TrackedSince: fol[a.ID].BaselineAt}
		if fol[a.ID].Blog != "" {
			row.Blog = fol[a.ID].Blog
		}
		credited := map[string]bool{}
		bySrc := map[string]*researchStat{}
		mine := researchStat{Key: a.ID, accs: map[string]bool{}}
		for _, t := range perAcc[a.ID] {
			addOutcome(&mine, t)
			row.Touches++
			if t.followback {
				row.Followbacks++
				credited[t.Target] = true
			}
			if t.dm {
				row.TouchDMs++
			}
			src := t.Source
			if src == "" {
				src = t.Kind
			}
			s := bySrc[src]
			if s == nil {
				s = &researchStat{Key: src, accs: map[string]bool{}}
				bySrc[src] = s
			}
			addOutcome(s, t)
		}
		for name, stamp := range fol[a.ID].Names {
			if stamp == "" || parseFollowerAt(stamp).Before(since) {
				continue
			}
			row.NewFollowers++
			if !credited[name] {
				row.Organic++
			}
		}
		for _, stamp := range fol[a.ID].Lost {
			if !parseFollowerAt(stamp).Before(since) {
				row.Lost++
			}
		}
		for _, stamp := range dms[a.ID].Names {
			if stamp != "" && !parseFollowerAt(stamp).Before(since) {
				row.NewDMs++
			}
		}
		row.Attributed = row.NewFollowers - row.Organic
		if row.Touches > 0 {
			row.Rate = float64(row.Followbacks) / float64(row.Touches)
		}
		judged := []researchStat{finishStat(mine)}
		judge(judged, rep.Fleet)
		row.Trials, row.Wins, row.WinRate, row.WinLow, row.WinHigh, row.Verdict = judged[0].Trials, judged[0].Wins, judged[0].WinRate, judged[0].WinLow, judged[0].WinHigh, judged[0].Verdict
		var best *researchStat
		for _, s := range bySrc {
			if s.Followbacks == 0 {
				continue
			}
			if best == nil || statBetter(finishStat(*s), finishStat(*best)) {
				best = s
			}
		}
		if best != nil {
			row.BestSource = best.Key
		}
		if row.Touches == 0 && row.NewFollowers == 0 && row.Followers == 0 {
			continue
		}
		rep.NewFollowers += row.NewFollowers
		rep.Organic += row.Organic
		rep.Lost += row.Lost
		rep.Accounts = append(rep.Accounts, row)
	}
	rep.Messaging = buildDMReport(since, now)
	dmBy := map[string]dmStat{}
	for _, s := range rep.Messaging.Accounts {
		dmBy[s.Key] = s
	}
	setup := loadSetupStatus()
	for i := range rep.Accounts {
		a := &rep.Accounts[i]
		a.DMChats, a.DMNudged = dmBy[a.ID].Chats, dmBy[a.ID].Nudged
		a.Setup = setup[a.ID]
	}
	sort.SliceStable(rep.Accounts, func(i, j int) bool {
		if rep.Accounts[i].NewFollowers != rep.Accounts[j].NewFollowers {
			return rep.Accounts[i].NewFollowers > rep.Accounts[j].NewFollowers
		}
		return rep.Accounts[i].Gain7d > rep.Accounts[j].Gain7d
	})
	rep.Daily = buildResearchDaily(touches, fol, since, now)
	rep.Tuner = scoreSources(touches)
	rep.Actions = buildResearchActions(rep, now)
	return rep
}

func addOutcome(s *researchStat, t touchOutcome) {
	s.Touches++
	if t.Liked {
		s.Liked++
	}
	if t.Reblogged {
		s.Reblogged++
	}
	if t.Followed {
		s.Followed++
	}
	if t.followback {
		s.Followbacks++
		s.lags = append(s.lags, t.lagHours)
	}
	if t.dm {
		s.DMs++
	}
	if t.matured {
		s.Matured++
	}
	if t.trial() {
		s.Trials++
		if t.win() {
			s.Wins++
		}
	}
	s.accs[t.Account] = true
}

const (
	verdictWinner  = "winner"
	verdictAverage = "average"
	verdictBelow   = "below average"
	verdictDud     = "dud"
	verdictTesting = "testing"
)

// judge compares each row with the fleet. A verdict is only given when the
// 95% range clears the fleet rate, so noise never reads as a result.
func judge(rows []researchStat, fleet researchStat) {
	base := fleet.WinRate
	for i := range rows {
		s := &rows[i]
		if base > 0 {
			s.Lift = s.WinRate / base
		}
		switch {
		case s.Trials < researchMinSampleRate:
			s.Verdict = verdictTesting
		case base > 0 && s.WinLow > base:
			s.Verdict = verdictWinner
		case (s.Wins == 0 && s.Trials >= 2*researchMinSampleRate) || (base > 0 && s.WinHigh < base/2):
			s.Verdict = verdictDud
		case base > 0 && s.WinHigh < base:
			s.Verdict = verdictBelow
		default:
			s.Verdict = verdictAverage
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return statBetter(rows[i], rows[j]) })
}

func finishStat(s researchStat) researchStat {
	s.Accounts = len(s.accs)
	if s.Touches > 0 {
		s.FollowbackRate = float64(s.Followbacks) / float64(s.Touches)
		s.DMRate = float64(s.DMs) / float64(s.Touches)
	}
	if len(s.lags) > 0 {
		l := append([]float64(nil), s.lags...)
		sort.Float64s(l)
		s.MedianLagHours = l[len(l)/2]
	}
	if s.Trials > 0 {
		s.WinRate = float64(s.Wins) / float64(s.Trials)
	}
	s.WinLow, s.WinHigh = wilson(s.Wins, s.Trials)
	s.LowSample = s.Trials < researchMinSampleRate
	return s
}

// statBetter ranks by the pessimistic end of the win range, so a source has
// to prove itself on volume before it can top the table.
func statBetter(a, b researchStat) bool {
	if a.LowSample != b.LowSample {
		return !a.LowSample
	}
	if a.WinLow != b.WinLow {
		return a.WinLow > b.WinLow
	}
	if a.FollowbackRate != b.FollowbackRate {
		return a.FollowbackRate > b.FollowbackRate
	}
	if a.Followbacks != b.Followbacks {
		return a.Followbacks > b.Followbacks
	}
	if a.DMs != b.DMs {
		return a.DMs > b.DMs
	}
	return a.Key < b.Key
}

func pct(f float64) string { return fmt.Sprintf("%.1f%%", f*100) }

// col fits s into n characters without spilling into the next column.
func col(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "~"
}

func researchReportLines(rep researchReport) []string {
	var out []string
	p := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	since := rep.TrackedSince
	if since == "" {
		since = "not yet — run a sync first"
	}
	p("RESEARCH REPORT — last %d days (made %s)", rep.WindowDays, rep.At[:16])
	p("Tracking since %s. Only touches after each account's first follower snapshot count; people get %.0f days to follow back or DM.", since, researchOutcomeWindow.Hours()/24)
	p("A WIN is a followback or a DM. A touch counts toward rates once it is %.0fh old; younger results show as early followbacks.", researchMatureAfter.Hours())
	p("\"likely\" is the 95%% range — a verdict is only given when that range clears the fleet rate.")
	p("")
	p("DO THIS NEXT")
	if len(rep.Actions) == 0 {
		p("  Nothing stands out yet.")
	}
	for _, a := range rep.Actions {
		p("  [%s] %s", a.Area, a.Text)
	}
	p("")
	f := rep.Fleet
	p("FLEET  touches %d (liked %d, reblogged %d, followed %d)  followbacks %d  DMs %d",
		f.Touches, f.Liked, f.Reblogged, f.Followed, f.Followbacks, f.DMs)
	p("       so far %s of touches got a followback or DM (%d of %d, includes touches under %.0fh old)", pct(earlyRate(f)), f.Followbacks+f.DMs, f.Touches, researchMatureAfter.Hours())
	if f.Trials > 0 {
		p("       win rate %s (likely %s–%s over %d touches %.0fh+ old)", pct(f.WinRate), pct(f.WinLow), pct(f.WinHigh), f.Trials, researchMatureAfter.Hours())
	}
	p("       new followers %d (from touches %d, organic %d)  lost %d", rep.NewFollowers, rep.NewFollowers-rep.Organic, rep.Organic, rep.Lost)
	p("")
	p("DAILY TREND")
	p("  %-10s %7s %7s %6s %5s %6s %5s %5s %5s %5s %7s %6s", "day", "touches", "follows", "trials", "wins", "win%", "new", "organ", "lost", "chats", "replies", "nudges")
	for i, d := range rep.Daily {
		if i >= 14 {
			break
		}
		wr := "-"
		if d.Trials > 0 {
			wr = pct(float64(d.Wins) / float64(d.Trials))
		}
		p("  %-10s %7d %7d %6d %5d %6s %5d %5d %5d %5d %7d %6d", d.Day, d.Touches, d.Follows, d.Trials, d.Wins, wr, d.NewFollowers, d.Organic, d.Lost, d.DMChats, d.Replies, d.Nudges)
	}
	p("")
	statHeader := fmt.Sprintf("  %-32s %7s %6s %6s %5s %6s %-13s %5s %4s  %s", "", "touches", "trials", "f-back", "DMs", "win%", "likely", "lift", "accs", "verdict")
	statRow := func(s researchStat) string {
		lift := "-"
		if s.Lift > 0 {
			lift = fmt.Sprintf("%.1fx", s.Lift)
		}
		return fmt.Sprintf("  %-32s %7d %6d %6d %5d %6s %-13s %5s %4d  %s", col(s.Key, 32), s.Touches, s.Trials, s.Followbacks, s.DMs, pct(s.WinRate),
			fmt.Sprintf("%.0f–%.0f%%", s.WinLow*100, s.WinHigh*100), lift, s.Accounts, s.Verdict)
	}
	p("SEED BLOGS / TAGS — best proven results first")
	p("%s", statHeader)
	for i, s := range rep.Sources {
		if i >= 30 {
			p("  … %d more in %s", len(rep.Sources)-30, researchReportFile)
			break
		}
		p("%s", statRow(s))
	}
	p("")
	if len(rep.Tuner) > 0 {
		counts := map[string][]string{}
		for _, s := range rep.Tuner {
			counts[s.Mode] = append(counts[s.Mode], s.Key)
		}
		p("REACH TUNER — how the next runs feed each source")
		for _, m := range []string{tunerBoost, tunerNormal, tunerExplore, tunerCut} {
			if len(counts[m]) == 0 {
				continue
			}
			p("  %-8s %3d  %s", m, len(counts[m]), strings.Join(clipStrings(counts[m], 12), ", "))
		}
		p("")
	}
	p("ACCOUNTS — most new followers first")
	p("  %-20s %-18s %5s %4s %4s %4s %4s %5s %4s %6s %6s %-13s %5s %6s  %-14s %s", "account", "blog", "fol", "+1d", "+7d", "new", "ours", "organ", "lost", "trials", "win%", "verdict", "chats", "nudged", "best source", "setup")
	for _, a := range rep.Accounts {
		p("  %-20s %-18s %5d %+4d %+4d %4d %4d %5d %4d %6d %6s %-13s %5d %6d  %-14s %s", col(a.ID, 20), col("@"+a.Blog, 18), a.Followers, a.Gain1d, a.Gain7d,
			a.NewFollowers, a.Attributed, a.Organic, a.Lost, a.Trials, pct(a.WinRate), a.Verdict, a.DMChats, a.DMNudged, col(a.BestSource, 14), a.Setup)
	}
	p("")
	p("ENGAGE STYLE — which kind of touch gets people back")
	p("%s", statHeader)
	for _, s := range rep.TouchTypes {
		p("%s", statRow(s))
	}
	p("")
	p("BEST ACCOUNT + SOURCE PAIRS")
	p("%s", statHeader)
	shown := 0
	for _, s := range rep.Pairs {
		if s.Wins == 0 {
			continue
		}
		p("%s", statRow(s))
		shown++
		if shown >= 20 {
			break
		}
	}
	if shown == 0 {
		p("  (no wins attributed yet)")
	}
	p("")
	p("HOUR OF DAY (when the touch happened)")
	p("%s", statHeader)
	for _, s := range rep.Hours {
		p("%s", statRow(s))
	}
	p("")
	out = append(out, dmReportLines(rep.Messaging)...)
	return out
}

func saveResearchReport(rep researchReport) error {
	_ = os.MkdirAll(researchDir, 0o755)
	raw, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(researchReportFile, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	text := strings.Join(researchReportLines(rep), "\n") + "\n"
	return os.WriteFile(researchReportText, []byte(text), 0o644)
}

// researchAccounts is every enabled, non-terminated account with a session.
func researchAccounts() []Account {
	var out []Account
	for _, a := range enabledAccounts() {
		if accountIsTerminated(a.ID) || !authHasSID(peekAccountAuth(a.ID)) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// refreshResearchAfterRun syncs the accounts a run just worked and rebuilds
// the fleet report, so the dashboard and tuner never lag a run behind.
func refreshResearchAfterRun(accs []Account, logFn func(string)) {
	researchMu.Lock()
	defer researchMu.Unlock()
	syncResearch(accs, logFn)
	resetTunerCache()
	rep := buildResearchReport(researchAccounts(), 14, time.Now())
	if err := saveResearchReport(rep); err != nil {
		logFn("Research report not saved: " + err.Error())
		return
	}
	for _, line := range researchHeadline(rep, 3) {
		logFn(line)
	}
}

// earlyRate is the raw share of touches that have won so far, young or not.
func earlyRate(s researchStat) float64 {
	if s.Touches == 0 {
		return 0
	}
	return float64(s.Followbacks+s.DMs) / float64(s.Touches)
}

func researchHeadline(rep researchReport, maxActions int) []string {
	f := rep.Fleet
	rate := fmt.Sprintf("win rate %s over %d matured touches", pct(f.WinRate), f.Trials)
	if f.Trials < researchMinSampleRate {
		rate = fmt.Sprintf("%d followbacks/DMs from %d touches so far (%s)", f.Followbacks+f.DMs, f.Touches, pct(earlyRate(f)))
	}
	out := []string{fmt.Sprintf("Research: %s · %d new followers (%d organic) · %d chats, %d pin nudges · dashboard at /research",
		rate, rep.NewFollowers, rep.Organic, rep.Messaging.Fleet.Chats, rep.Messaging.Fleet.Nudged)}
	for i, a := range rep.Actions {
		if i >= maxActions {
			break
		}
		out = append(out, "  ["+a.Area+"] "+a.Text)
	}
	return out
}

// researchMu serialises syncs and report writes. Research only reads Tumblr
// over HTTP with saved sessions, so it runs alongside reach and replies.
var researchMu sync.Mutex

func runResearch(days int, sync bool, logFn func(string)) error {
	researchMu.Lock()
	defer researchMu.Unlock()
	accs := researchAccounts()
	if len(accs) == 0 {
		return fmt.Errorf("no accounts with a session")
	}
	if sync {
		logFn(fmt.Sprintf("Syncing followers + DM partners on %d accounts…", len(accs)))
		syncResearch(accs, logFn)
		resetTunerCache()
	}
	rep := buildResearchReport(accs, days, time.Now())
	if err := saveResearchReport(rep); err != nil {
		return err
	}
	for _, line := range researchReportLines(rep) {
		logFn(line)
	}
	logFn(fmt.Sprintf("Saved %s and %s", researchReportText, researchReportFile))
	return nil
}
