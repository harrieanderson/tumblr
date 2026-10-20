package main

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Reach hands mid-run replies to the accounts with the most chats waiting on
// Zoey, so the limited AI budget goes where people are actually talking.

type dmBacklog struct {
	Account Account
	Waiting int
	Err     error
}

// countWaitingChats counts inbox threads whose last message is theirs. It only
// reads: no CSRF refresh or login, because those save auth for the *active*
// account and would copy this account's session into another profile.
func countWaitingChats(a Account) (int, error) {
	auth := peekAccountAuth(a.ID)
	if !authHasSID(auth) {
		return 0, fmt.Errorf("not logged in")
	}
	if b := preferredMessagingBlog(a.ID, auth.Blog); b != "" {
		auth.Blog = b
	}
	client := &http.Client{Timeout: 20 * time.Second}
	waiting := 0
	before := ""
	for page := 0; page < 10; page++ {
		batch, next, err := listConversationsOnceWithCursor(client, auth, 20, before)
		if err != nil {
			if page == 0 {
				return 0, err
			}
			break
		}
		for _, c := range batch {
			if c.CanSend && !c.LastMessageFromSelf && strings.TrimSpace(c.OtherName) != "" &&
				!looksLikeChatBot(c.OtherName, nil) && !looksLikeMoneyAsk(c.LastMessageText) {
				waiting++
			}
		}
		if next == "" || next == before || len(batch) == 0 {
			break
		}
		before = next
	}
	return waiting, nil
}

func dmBacklogs(accs []Account) []dmBacklog {
	out := make([]dmBacklog, len(accs))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for i, a := range accs {
		wg.Add(1)
		go func(i int, a Account) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			n, err := countWaitingChats(a)
			out[i] = dmBacklog{Account: a, Waiting: n, Err: err}
		}(i, a)
	}
	wg.Wait()
	sort.SliceStable(out, func(i, j int) bool { return out[i].Waiting > out[j].Waiting })
	return out
}

func printDMBacklog() {
	var accs []Account
	for _, a := range enabledAccounts() {
		if !accountIsTerminated(a.ID) {
			accs = append(accs, a)
		}
	}
	total := 0
	for _, r := range dmBacklogs(accs) {
		switch {
		case r.Err != nil:
			continue
		case r.Waiting > 0:
			total += r.Waiting
			fmt.Printf("  %-24s %3d waiting\n", r.Account.ID, r.Waiting)
		}
	}
	fmt.Printf("%d chats waiting on a reply\n", total)
}

// busiestInboxAccounts picks up to n accounts with the most chats waiting.
// Accounts with nothing waiting are left for reach.
func busiestInboxAccounts(accs []Account, n int) ([]Account, []dmBacklog) {
	rows := dmBacklogs(accs)
	var out []Account
	for _, r := range rows {
		if len(out) >= n {
			break
		}
		if r.Err == nil && r.Waiting > 0 {
			out = append(out, r.Account)
		}
	}
	return out, rows
}
