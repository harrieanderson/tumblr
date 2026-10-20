package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// DM events feed the messaging half of the research report: one line per
// reply, skip or failure, with enough context (stage, model, latency, who
// spoke last) to rebuild every chat's funnel afterwards.
const researchDMEventsFile = "data/research/dm-events.jsonl"

type dmEvent struct {
	At         string `json:"at"`
	Account    string `json:"account"`
	Partner    string `json:"partner"`
	ConvID     string `json:"convId,omitempty"`
	Event      string `json:"event"`            // reply, skip, send_fail, ai_exhausted
	Stage      string `json:"stage,omitempty"`  // chat, pin_nudge, beacons, post_pin, post_beacons
	Reason     string `json:"reason,omitempty"` // skip / failure reason
	Set        int    `json:"set,omitempty"`
	Source     string `json:"source,omitempty"` // model / template that wrote the reply
	Bubbles    int    `json:"bubbles,omitempty"`
	TheyLast   bool   `json:"theyLast,omitempty"`
	LatencySec int64  `json:"latencySec,omitempty"`
	TheirMsgs  int    `json:"theirMsgs,omitempty"`
	OurMsgs    int    `json:"ourMsgs,omitempty"`
	Age        int    `json:"age,omitempty"`
}

func recordDMEvent(ev dmEvent) {
	ev.Account = accountSlug(ev.Account)
	ev.Partner = normalizeBlogName(ev.Partner)
	if ev.Account == "" || ev.Partner == "" || ev.Event == "" {
		return
	}
	if ev.At == "" {
		ev.At = time.Now().Format(time.RFC3339)
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		return
	}
	_ = os.MkdirAll(researchDir, 0o755)
	f, err := os.OpenFile(researchDMEventsFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(raw, '\n'))
}

// dmReplyEvent describes a reply that was just sent, from the history it
// answered and the text that went out.
func dmReplyEvent(accountID string, c Conversation, history []ConvMessage, text, src string, set int) dmEvent {
	ev := dmEvent{Account: accountID, Partner: c.OtherName, ConvID: c.ID, Event: "reply", Set: set, Source: src}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			ev.Bubbles++
		}
	}
	for _, m := range history {
		if m.FromSelf {
			ev.OurMsgs++
		} else {
			ev.TheirMsgs++
		}
	}
	if latest, ok := latestMessage(history); ok && !latest.FromSelf {
		ev.TheyLast = true
		if ts := messageUnix(latest); ts > 0 {
			if ts > 1e12 {
				ts /= 1000
			}
			if lag := time.Now().Unix() - ts; lag >= 0 {
				ev.LatencySec = lag
			}
		}
	}
	switch {
	case weAlreadySentBeacons(history):
		ev.Stage = "post_beacons"
	case strings.Contains(foldDM(text), "beacons.ai"):
		ev.Stage = "beacons"
	case weAlreadySentPinNudge(history):
		ev.Stage = "post_pin"
	case looksLikePinNudge(text):
		ev.Stage = "pin_nudge"
	default:
		ev.Stage = "chat"
	}
	if age, ok := theirStatedAge(history); ok {
		ev.Age = age
	}
	return ev
}

func loadDMEvents(since time.Time) []dmEvent {
	raw, err := os.ReadFile(researchDMEventsFile)
	if err != nil {
		return nil
	}
	var out []dmEvent
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev dmEvent
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if at := parseFollowerAt(ev.At); !since.IsZero() && at.Before(since) {
			continue
		}
		out = append(out, ev)
	}
	return out
}

// ---- report ----

type dmChat struct {
	Account, Partner string
	First, Last      time.Time
	Replies          int
	MaxSet           int
	Nudged           bool
	NudgeAt          time.Time
	ReplyAfterNudge  bool
	Beacons          bool
	ReplyAfterBeacon bool
	Source           string
	LastStage        string
	Followed         bool
}

type dmStat struct {
	Key             string  `json:"key"`
	Chats           int     `json:"chats"`
	Replies         int     `json:"replies"`
	AvgSets         float64 `json:"avgSets"`
	Nudged          int     `json:"nudged"`
	NudgeReplied    int     `json:"nudgeReplied"`
	NudgeReplyRate  float64 `json:"nudgeReplyRate"`
	Beacons         int     `json:"beacons"`
	BeaconReplied   int     `json:"beaconReplied"`
	MedianLatencyM  float64 `json:"medianLatencyMin"`
	MinorsSkipped   int     `json:"minorsSkipped"`
	SendFails       int     `json:"sendFails"`
	ReachedNudgePct float64 `json:"reachedNudgePct"`
	Followed        int     `json:"followed"`
	FollowRate      float64 `json:"followRate"`
	latencies       []float64
	sets            int
}

type dmReport struct {
	Fleet     dmStat         `json:"fleet"`
	Funnel    []dmFunnelStep `json:"funnel"`
	Accounts  []dmStat       `json:"accounts"`
	Sources   []dmStat       `json:"sources"`
	Models    []dmCount      `json:"models"`
	Skips     []dmCount      `json:"skips"`
	Hours     []dmCount      `json:"hours"`
	Stages    []dmCount      `json:"stages"`
	DeadChats int            `json:"deadChats"`
	// QuietAfter counts chats silent for 48h+ by the last reply we sent, so
	// the reply that loses people stands out.
	QuietAfter []dmCount `json:"quietAfter"`
}

type dmFunnelStep struct {
	Step  string  `json:"step"`
	Chats int     `json:"chats"`
	Pct   float64 `json:"pct"`
}

type dmCount struct {
	Key string `json:"key"`
	N   int    `json:"n"`
}

const (
	dmChatDeadAfter = 48 * time.Hour
	dmLatencyCap    = 7 * 24 * time.Hour
)

func isMinorSkip(reason string) bool {
	r := strings.ToLower(reason)
	return strings.Contains(r, "under 18") || strings.Contains(r, "minor")
}

func buildDMReport(since, now time.Time) dmReport {
	events := loadDMEvents(since)
	sort.SliceStable(events, func(i, j int) bool { return events[i].At < events[j].At })

	touchSource := map[string]string{}
	for _, t := range loadResearchTouches() {
		k := t.Account + "|" + t.Target
		if _, ok := touchSource[k]; !ok {
			src := t.Source
			if src == "" {
				src = t.Kind
			}
			touchSource[k] = src
		}
	}

	chats := map[string]*dmChat{}
	fleet := dmStat{Key: "fleet"}
	accStats := map[string]*dmStat{}
	models, skips, hours, stages := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	accStat := func(id string) *dmStat {
		s := accStats[id]
		if s == nil {
			s = &dmStat{Key: id}
			accStats[id] = s
		}
		return s
	}

	skipSeen := map[string]bool{}
	minorSeen := map[string]bool{}
	for _, ev := range events {
		at := parseFollowerAt(ev.At)
		as := accStat(ev.Account)
		switch ev.Event {
		case "skip":
			// Skips repeat every pass while a chat sits in the inbox; count
			// each chat once per reason.
			sk := ev.Account + "|" + ev.Partner + "|" + ev.Reason
			if !skipSeen[sk] {
				skipSeen[sk] = true
				skips[ev.Reason]++
			}
			if isMinorSkip(ev.Reason) && !minorSeen[ev.Account+"|"+ev.Partner] {
				minorSeen[ev.Account+"|"+ev.Partner] = true
				fleet.MinorsSkipped++
				as.MinorsSkipped++
			}
			continue
		case "send_fail":
			fleet.SendFails++
			as.SendFails++
			continue
		case "reply":
		default:
			continue
		}
		k := ev.Account + "|" + ev.Partner
		c := chats[k]
		if c == nil {
			c = &dmChat{Account: ev.Account, Partner: ev.Partner, First: at, Source: touchSource[k]}
			if c.Source == "" {
				c.Source = "not from reach"
			}
			chats[k] = c
		}
		if ev.TheyLast {
			if c.Nudged && at.After(c.NudgeAt) {
				c.ReplyAfterNudge = true
			}
			if c.Beacons && ev.Stage == "post_beacons" {
				c.ReplyAfterBeacon = true
			}
		}
		c.Last = at
		c.LastStage = ev.Stage
		c.Replies++
		if ev.Set > c.MaxSet {
			c.MaxSet = ev.Set
		}
		switch ev.Stage {
		case "pin_nudge":
			if !c.Nudged {
				c.Nudged, c.NudgeAt = true, at
			}
		case "beacons":
			c.Beacons = true
		}
		stages[ev.Stage]++
		src := ev.Source
		if src == "" {
			src = "unknown"
		}
		models[src]++
		hours[fmt.Sprintf("%02d:00", at.Hour())]++
		fleet.Replies++
		as.Replies++
		// Waits over dmLatencyCap are old chats being picked up, not reply speed.
		if ev.LatencySec > 0 && ev.TheyLast && time.Duration(ev.LatencySec)*time.Second <= dmLatencyCap {
			m := float64(ev.LatencySec) / 60
			fleet.latencies = append(fleet.latencies, m)
			as.latencies = append(as.latencies, m)
		}
	}

	fol := loadAllSeenSets("followers")
	srcStats := map[string]*dmStat{}
	setReached := map[int]int{}
	quiet := map[string]int{}
	dead := 0
	for _, c := range chats {
		c.Followed = fol[c.Account].Names[c.Partner] != ""
		for _, s := range []*dmStat{&fleet, accStat(c.Account), srcStat(srcStats, c.Source)} {
			s.Chats++
			s.sets += c.MaxSet
			if c.Followed {
				s.Followed++
			}
			if c.Nudged {
				s.Nudged++
				if c.ReplyAfterNudge {
					s.NudgeReplied++
				}
			}
			if c.Beacons {
				s.Beacons++
				if c.ReplyAfterBeacon {
					s.BeaconReplied++
				}
			}
		}
		if c != nil && srcStats[c.Source] != nil {
			srcStats[c.Source].Replies += c.Replies
		}
		for i := 1; i <= c.MaxSet && i <= 8; i++ {
			setReached[i]++
		}
		if now.Sub(c.Last) > dmChatDeadAfter {
			if !c.Nudged && !c.Beacons {
				dead++
			}
			label := fmt.Sprintf("set %d", c.MaxSet)
			if c.MaxSet >= 8 {
				label = "set 8+"
			}
			if c.LastStage != "" && c.LastStage != "chat" {
				label += " (" + strings.ReplaceAll(c.LastStage, "_", " ") + ")"
			}
			quiet[label]++
		}
	}

	rep := dmReport{Fleet: finishDMStat(fleet), DeadChats: dead, QuietAfter: sortedCounts(quiet)}
	total := len(chats)
	addStep := func(name string, n int) {
		pct := 0.0
		if total > 0 {
			pct = float64(n) / float64(total)
		}
		rep.Funnel = append(rep.Funnel, dmFunnelStep{Step: name, Chats: n, Pct: pct})
	}
	for i := 1; i <= 8; i++ {
		label := fmt.Sprintf("reached set %d", i)
		if i == 8 {
			label = "reached set 8+"
		}
		addStep(label, setReached[i])
	}
	addStep("got the pin nudge", fleet.Nudged)
	addStep("replied after pin nudge", fleet.NudgeReplied)
	addStep("got the beacons link", fleet.Beacons)
	addStep("replied after beacons", fleet.BeaconReplied)
	addStep("became a follower", fleet.Followed)

	for _, s := range accStats {
		rep.Accounts = append(rep.Accounts, finishDMStat(*s))
	}
	sort.SliceStable(rep.Accounts, func(i, j int) bool {
		if rep.Accounts[i].Nudged != rep.Accounts[j].Nudged {
			return rep.Accounts[i].Nudged > rep.Accounts[j].Nudged
		}
		return rep.Accounts[i].Chats > rep.Accounts[j].Chats
	})
	for _, s := range srcStats {
		rep.Sources = append(rep.Sources, finishDMStat(*s))
	}
	sort.SliceStable(rep.Sources, func(i, j int) bool {
		if rep.Sources[i].Nudged != rep.Sources[j].Nudged {
			return rep.Sources[i].Nudged > rep.Sources[j].Nudged
		}
		return rep.Sources[i].Chats > rep.Sources[j].Chats
	})
	rep.Models = sortedCounts(models)
	rep.Skips = sortedCounts(skips)
	rep.Stages = sortedCounts(stages)
	rep.Hours = sortedCounts(hours)
	sort.SliceStable(rep.Hours, func(i, j int) bool { return rep.Hours[i].Key < rep.Hours[j].Key })
	return rep
}

func srcStat(m map[string]*dmStat, key string) *dmStat {
	s := m[key]
	if s == nil {
		s = &dmStat{Key: key}
		m[key] = s
	}
	return s
}

func finishDMStat(s dmStat) dmStat {
	if s.Chats > 0 {
		s.AvgSets = float64(s.sets) / float64(s.Chats)
		s.ReachedNudgePct = float64(s.Nudged) / float64(s.Chats)
		s.FollowRate = float64(s.Followed) / float64(s.Chats)
	}
	if s.Nudged > 0 {
		s.NudgeReplyRate = float64(s.NudgeReplied) / float64(s.Nudged)
	}
	if len(s.latencies) > 0 {
		l := append([]float64(nil), s.latencies...)
		sort.Float64s(l)
		s.MedianLatencyM = l[len(l)/2]
	}
	return s
}

func sortedCounts(m map[string]int) []dmCount {
	out := make([]dmCount, 0, len(m))
	for k, n := range m {
		out = append(out, dmCount{Key: k, N: n})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func fmtMinutes(m float64) string {
	if m <= 0 {
		return "-"
	}
	if m < 90 {
		return fmt.Sprintf("%.0fm", m)
	}
	return fmt.Sprintf("%.1fh", m/60)
}

func dmReportLines(rep dmReport) []string {
	var out []string
	p := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	f := rep.Fleet
	p("MESSAGING")
	p("  chats %d  replies sent %d  avg sets per chat %.1f  median reply time %s", f.Chats, f.Replies, f.AvgSets, fmtMinutes(f.MedianLatencyM))
	p("  pin nudges %d (%s of chats)  replied after nudge %d (%s)  beacons sent %d",
		f.Nudged, pct(f.ReachedNudgePct), f.NudgeReplied, pct(f.NudgeReplyRate), f.Beacons)
	p("  under-18 skipped %d  send failures %d  chats gone quiet before the nudge (48h+) %d", f.MinorsSkipped, f.SendFails, rep.DeadChats)
	p("")
	p("CHAT FUNNEL — where chats drop off")
	for _, s := range rep.Funnel {
		p("  %-26s %5d  %6s", s.Step, s.Chats, pct(s.Pct))
	}
	p("")
	dmHeader := fmt.Sprintf("  %-26s %5s %7s %5s %6s %7s %7s %7s %6s %6s", "", "chats", "replies", "sets", "nudged", "nudge%", "n-reply", "beacons", "follow", "reply")
	dmRow := func(s dmStat) string {
		return fmt.Sprintf("  %-26s %5d %7d %5.1f %6d %7s %7s %7d %6s %6s", truncate(s.Key, 26), s.Chats, s.Replies, s.AvgSets, s.Nudged, pct(s.ReachedNudgePct), pct(s.NudgeReplyRate), s.Beacons, pct(s.FollowRate), fmtMinutes(s.MedianLatencyM))
	}
	p("MESSAGING BY ACCOUNT — most pin nudges first (reply = median time to answer)")
	p("%s", dmHeader)
	for _, s := range rep.Accounts {
		if s.Chats == 0 && s.MinorsSkipped == 0 {
			continue
		}
		p("%s", dmRow(s))
	}
	p("")
	p("MESSAGING BY WHERE THE CHATTER CAME FROM")
	p("%s", dmHeader)
	for i, s := range rep.Sources {
		if i >= 20 {
			break
		}
		p("%s", dmRow(s))
	}
	p("")
	p("CHATS GONE QUIET 48h+ — by the last reply we sent")
	if len(rep.QuietAfter) == 0 {
		p("  (none)")
	}
	for _, c := range rep.QuietAfter {
		p("  %-26s %5d", c.Key, c.N)
	}
	p("")
	p("REPLY STAGES SENT")
	for _, c := range rep.Stages {
		p("  %-26s %5d", c.Key, c.N)
	}
	p("")
	p("WHO WROTE THE REPLIES")
	for _, c := range rep.Models {
		p("  %-40s %5d", truncate(c.Key, 40), c.N)
	}
	p("")
	p("SKIPPED CHATS")
	if len(rep.Skips) == 0 {
		p("  (none)")
	}
	for _, c := range rep.Skips {
		p("  %-40s %5d", truncate(c.Key, 40), c.N)
	}
	p("")
	p("REPLIES BY HOUR")
	for _, c := range rep.Hours {
		p("  %-8s %5d", c.Key, c.N)
	}
	return out
}
