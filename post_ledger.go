package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// postLedgerFile is the append-only record of every post the bot has made,
// shared across accounts so we can see who posted what, and where.
var postLedgerFile = filepath.Join("data", "post-ledger.jsonl")

type ledgerEntry struct {
	Time    string `json:"time"`
	Account string `json:"account"`
	Blog    string `json:"blog"`
	Kind    string `json:"kind"`
	Text    string `json:"text,omitempty"`
	Media   string `json:"media,omitempty"`
	PostID  string `json:"postId,omitempty"`
	Pinned  bool   `json:"pinned,omitempty"`
}

var ledgerMu sync.Mutex

var ledgerNormRe = regexp.MustCompile(`[^a-z0-9]+`)

func ledgerKey(text string) string {
	t := strings.ToLower(strings.ReplaceAll(text, "\u2019", "'"))
	return strings.TrimSpace(ledgerNormRe.ReplaceAllString(t, " "))
}

func recordPost(e ledgerEntry) {
	if e.Time == "" {
		e.Time = time.Now().Format(time.RFC3339)
	}
	e.Blog = strings.TrimPrefix(strings.TrimSpace(e.Blog), "@")
	raw, err := json.Marshal(e)
	if err != nil {
		return
	}
	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(postLedgerFile), 0755); err != nil {
		fmt.Println("post ledger:", err)
		return
	}
	f, err := os.OpenFile(postLedgerFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("post ledger:", err)
		return
	}
	defer f.Close()
	_, _ = f.Write(append(raw, '\n'))
}

func loadPostLedger() []ledgerEntry {
	ledgerMu.Lock()
	defer ledgerMu.Unlock()
	f, err := os.Open(postLedgerFile)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []ledgerEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		var e ledgerEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Account != "" {
			out = append(out, e)
		}
	}
	return out
}

// accountAlreadyPosted reports whether this account has already posted this text
// (or this media file), so reruns never double-post on the same blog.
func accountAlreadyPosted(accountID, text, media string) (ledgerEntry, bool) {
	k := ledgerKey(text)
	m := strings.ToLower(filepath.Base(media))
	for _, e := range loadPostLedger() {
		if !strings.EqualFold(e.Account, accountID) {
			continue
		}
		if k != "" && ledgerKey(e.Text) == k && (m == "" || strings.EqualFold(filepath.Base(e.Media), m)) {
			return e, true
		}
		if k == "" && m != "" && strings.EqualFold(filepath.Base(e.Media), m) {
			return e, true
		}
	}
	return ledgerEntry{}, false
}

// printPostLedger shows every post grouped by what was posted, or one account's history.
func printPostLedger(accountID string) {
	entries := loadPostLedger()
	if len(entries) == 0 {
		fmt.Println("No posts recorded yet in", postLedgerFile)
		return
	}
	if accountID != "" {
		fmt.Printf("Posts by %s:\n", accountID)
		n := 0
		for _, e := range entries {
			if !strings.EqualFold(e.Account, accountID) {
				continue
			}
			n++
			what := e.Text
			if e.Media != "" {
				what = "[" + filepath.Base(e.Media) + "] " + what
			}
			pin := ""
			if e.Pinned {
				pin = " (pinned)"
			}
			fmt.Printf("  %s  @%s  %s%s\n", e.Time[:16], e.Blog, truncate(what, 90), pin)
		}
		if n == 0 {
			fmt.Println("  nothing yet")
		}
		return
	}
	type group struct {
		label string
		kind  string
		first string
		blogs []string
	}
	groups := map[string]*group{}
	var order []string
	for _, e := range entries {
		key := e.Kind + "|" + ledgerKey(e.Text) + "|" + strings.ToLower(filepath.Base(e.Media))
		g := groups[key]
		if g == nil {
			label := e.Text
			if e.Media != "" {
				label = "[" + filepath.Base(e.Media) + "] " + label
			}
			g = &group{label: label, kind: e.Kind, first: e.Time}
			groups[key] = g
			order = append(order, key)
		}
		g.blogs = append(g.blogs, "@"+e.Blog)
	}
	sort.SliceStable(order, func(i, j int) bool { return groups[order[i]].first > groups[order[j]].first })
	accounts := map[string]bool{}
	for _, e := range entries {
		accounts[strings.ToLower(e.Account)] = true
	}
	fmt.Printf("%d posts across %d accounts (%s)\n\n", len(entries), len(accounts), postLedgerFile)
	for _, k := range order {
		g := groups[k]
		fmt.Printf("%s  %-5s  %d blogs  %q\n", g.first[:10], g.kind, len(g.blogs), truncate(g.label, 80))
		fmt.Printf("      %s\n", strings.Join(g.blogs, " "))
	}
}
