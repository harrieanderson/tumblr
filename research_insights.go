package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// ---- daily trend ----

type researchDay struct {
	Day          string `json:"day"`
	Touches      int    `json:"touches"`
	Follows      int    `json:"follows"`
	Trials       int    `json:"trials"`
	Wins         int    `json:"wins"`
	NewFollowers int    `json:"newFollowers"`
	Organic      int    `json:"organic"`
	Lost         int    `json:"lost"`
	DMChats      int    `json:"dmChats"`
	Replies      int    `json:"replies"`
	Nudges       int    `json:"nudges"`
}

func buildResearchDaily(touches []touchOutcome, fol map[string]seenSet, since, now time.Time) []researchDay {
	by := map[string]*researchDay{}
	day := func(t time.Time) *researchDay {
		k := t.Local().Format("2006-01-02")
		d := by[k]
		if d == nil {
			d = &researchDay{Day: k}
			by[k] = d
		}
		return d
	}
	credited := map[string]bool{}
	for _, t := range touches {
		d := day(t.at)
		d.Touches++
		if t.Followed {
			d.Follows++
		}
		if t.trial() {
			d.Trials++
			if t.win() {
				d.Wins++
			}
		}
		if t.followback {
			credited[t.Account+"|"+t.Target] = true
		}
	}
	for acc, s := range fol {
		for name, stamp := range s.Names {
			at := parseFollowerAt(stamp)
			if stamp == "" || at.Before(since) {
				continue
			}
			d := day(at)
			d.NewFollowers++
			if !credited[acc+"|"+name] {
				d.Organic++
			}
		}
		for _, stamp := range s.Lost {
			if at := parseFollowerAt(stamp); !at.Before(since) {
				day(at).Lost++
			}
		}
	}
	chatStarted := map[string]bool{}
	for _, ev := range loadDMEvents(since) {
		if ev.Event != "reply" {
			continue
		}
		at := parseFollowerAt(ev.At)
		if at.IsZero() {
			continue
		}
		d := day(at)
		d.Replies++
		if k := ev.Account + "|" + ev.Partner; !chatStarted[k] {
			chatStarted[k] = true
			d.DMChats++
		}
		if ev.Stage == "pin_nudge" {
			d.Nudges++
		}
	}
	out := make([]researchDay, 0, len(by))
	for _, d := range by {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day > out[j].Day })
	return out
}

// ---- profile setup ----

func loadSetupStatus() map[string]string {
	raw, err := os.ReadFile("config/zoey-setup-status.json")
	if err != nil {
		return nil
	}
	var doc struct {
		Accounts []struct {
			ID         string `json:"id"`
			Status     string `json:"status"`
			UsernameOK bool   `json:"usernameOK"`
			TitleOK    bool   `json:"titleOK"`
			BioOK      bool   `json:"bioOK"`
			AvatarOK   bool   `json:"avatarOK"`
			BannerOK   bool   `json:"bannerOK"`
		} `json:"accounts"`
	}
	if json.Unmarshal(raw, &doc) != nil {
		return nil
	}
	out := map[string]string{}
	for _, a := range doc.Accounts {
		var missing []string
		for _, f := range []struct {
			ok   bool
			name string
		}{{a.AvatarOK, "pic"}, {a.BannerOK, "banner"}, {a.BioOK, "bio"}, {a.UsernameOK, "username"}, {a.TitleOK, "title"}} {
			if !f.ok {
				missing = append(missing, f.name)
			}
		}
		s := a.Status
		if s == "" {
			s = "unknown"
		}
		if len(missing) > 0 {
			s += " (no " + strings.Join(missing, ", ") + ")"
		}
		out[accountSlug(a.ID)] = s
	}
	return out
}

// ---- recommended actions ----

type researchAction struct {
	Area string `json:"area"`
	Text string `json:"text"`
}

func rangeText(s researchStat) string {
	return fmt.Sprintf("%s (likely %s–%s, %d/%d)", pct(s.WinRate), pct(s.WinLow), pct(s.WinHigh), s.Wins, s.Trials)
}

// buildResearchActions turns the report into the handful of changes worth
// making now. Nothing is suggested off a sample too small to trust.
func buildResearchActions(rep researchReport, now time.Time) []researchAction {
	var out []researchAction
	add := func(area, format string, args ...any) {
		out = append(out, researchAction{Area: area, Text: fmt.Sprintf(format, args...)})
	}
	f := rep.Fleet
	if f.Trials < 4*researchMinSampleRate {
		add("data", "Only %d touches have had their %.0fh to work so far. Verdicts start at %d per source; until then everything reads as testing.",
			f.Trials, researchMatureAfter.Hours(), researchMinSampleRate)
	}

	var winners, duds []string
	for _, s := range rep.Sources {
		switch s.Verdict {
		case verdictWinner:
			winners = append(winners, fmt.Sprintf("%s %.1fx", s.Key, s.Lift))
		case verdictDud:
			duds = append(duds, fmt.Sprintf("%s (%d/%d)", s.Key, s.Wins, s.Trials))
		}
	}
	if len(winners) > 0 {
		add("seeds", "Proven above the fleet rate: %s. The tuner already gives these more posts and runs them first.", strings.Join(clipStrings(winners, 8), ", "))
	}
	if len(duds) > 0 {
		add("seeds", "Proven duds — consider removing from config/follow.json: %s. The tuner has cut them to 1 post meanwhile.", strings.Join(clipStrings(duds, 10), ", "))
	}

	for _, a := range rep.Accounts {
		if a.Verdict != verdictDud && a.Verdict != verdictBelow {
			continue
		}
		if a.Setup != "" && !strings.HasPrefix(a.Setup, "full") {
			add("accounts", "%s converts at %s vs fleet %s and its profile is %s — finish the setup first.", a.ID, pct(a.WinRate), pct(f.WinRate), a.Setup)
		} else {
			add("accounts", "%s converts at %s vs fleet %s with a complete profile — check its pinned post, bio and recent posts.", a.ID, pct(a.WinRate), pct(f.WinRate))
		}
	}
	for _, a := range rep.Accounts {
		if a.Lost >= 3 && a.Lost > a.NewFollowers {
			add("accounts", "%s lost %d followers vs %d new — look for a content flag or a post that put people off.", a.ID, a.Lost, a.NewFollowers)
		}
	}

	if cmp := compareRows(rep.TouchTypes); cmp != "" {
		add("engage", "%s", cmp)
	}
	var hours []researchStat
	for _, h := range rep.Hours {
		if !h.LowSample {
			hours = append(hours, h)
		}
	}
	if len(hours) >= 3 {
		sort.SliceStable(hours, func(i, j int) bool { return statBetter(hours[i], hours[j]) })
		best, worst := hours[0], hours[len(hours)-1]
		if best.WinLow > worst.WinHigh {
			add("timing", "Touches at %s convert at %s vs %s at %s — schedule long runs around the better hours.", best.Key, pct(best.WinRate), pct(worst.WinRate), worst.Key)
		}
	}

	m := rep.Messaging
	if m.Fleet.Chats >= 10 {
		if m.DeadChats*2 > m.Fleet.Chats {
			add("messaging", "%d of %d chats went quiet before the pin nudge — nudging a set earlier would reach more of them.", m.DeadChats, m.Fleet.Chats)
		}
		if len(m.QuietAfter) > 0 && m.QuietAfter[0].N >= 3 {
			add("messaging", "Most quiet chats stop after %s (%d chats) — that reply is the one to rework.", m.QuietAfter[0].Key, m.QuietAfter[0].N)
		}
		if m.Fleet.Nudged >= 10 {
			add("messaging", "Pin nudge: %d sent, %s replied after, %s of chatters follow the account.", m.Fleet.Nudged, pct(m.Fleet.NudgeReplyRate), pct(m.Fleet.FollowRate))
		}
		for _, s := range m.Accounts {
			if s.Chats >= 5 && s.Nudged == 0 {
				add("messaging", "%s has %d chats but none reached the pin nudge.", s.Key, s.Chats)
			}
		}
	}
	if m.Fleet.SendFails >= 5 {
		add("messaging", "%d DM sends failed — check the send_fail reasons in %s.", m.Fleet.SendFails, researchDMEventsFile)
	}

	var stale []string
	sets := loadAllSeenSets("followers")
	for _, a := range rep.Accounts {
		at := parseFollowerAt(sets[a.ID].UpdatedAt)
		switch {
		case at.IsZero():
			stale = append(stale, a.ID+" (never)")
		case now.Sub(at) > 12*time.Hour:
			stale = append(stale, a.ID)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		add("data", "%d accounts have no follower sync in 12h+, so their followbacks are late or missing: %s", len(stale), strings.Join(clipStrings(stale, 10), ", "))
	}
	return out
}

// compareRows names the best and worst confident rows when their ranges do
// not overlap; otherwise it says there is no clear difference yet.
func compareRows(rows []researchStat) string {
	var ok []researchStat
	for _, r := range rows {
		if !r.LowSample {
			ok = append(ok, r)
		}
	}
	if len(ok) < 2 {
		return ""
	}
	sort.SliceStable(ok, func(i, j int) bool { return statBetter(ok[i], ok[j]) })
	best, worst := ok[0], ok[len(ok)-1]
	if best.WinLow > worst.WinHigh {
		return fmt.Sprintf("\"%s\" gets people back at %s vs \"%s\" at %s — a real difference.", best.Key, rangeText(best), worst.Key, rangeText(worst))
	}
	return fmt.Sprintf("No clear winner between engage styles yet (best \"%s\" %s, worst \"%s\" %s).", best.Key, pct(best.WinRate), worst.Key, pct(worst.WinRate))
}
