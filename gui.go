package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type logHub struct {
	mu      sync.Mutex
	clients map[chan string]struct{}
}

func newLogHub() *logHub {
	return &logHub{clients: map[chan string]struct{}{}}
}

func (h *logHub) subscribe() chan string {
	ch := make(chan string, 64)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *logHub) unsubscribe(ch chan string) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
	close(ch)
}

func (h *logHub) broadcast(msg string) {
	line := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), msg)
	fmt.Println(line)
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- line:
		default:
		}
	}
}

type guiServer struct {
	hub    *logHub
	mu     sync.Mutex
	openMu sync.Mutex
	busy   bool
}

func runGUI() {
	keepMachineAwake()
	srv := &guiServer{hub: newLogHub()}

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.handleIndex)
	mux.HandleFunc("/api/events", srv.handleEvents)
	mux.HandleFunc("/api/status", srv.handleStatus)
	mux.HandleFunc("/api/headless", srv.handleHeadless)
	mux.HandleFunc("/api/accounts", srv.handleAccounts)
	mux.HandleFunc("/api/login", srv.handleLogin)
	mux.HandleFunc("/api/follow", srv.handleFollow)
	mux.HandleFunc("/api/reach-mid-replies", srv.handleReachMidReplies)
	mux.HandleFunc("/api/research", srv.handleResearch)
	mux.HandleFunc("/api/research/report", handleResearchReport)
	mux.HandleFunc("/research", handleResearchDashboard)
	go researchAutoLoop(srv.hub.broadcast)
	mux.HandleFunc("/api/growth", srv.handleGrowth)
	mux.HandleFunc("/api/like-nsfw", srv.handleLikeNSFW)
	mux.HandleFunc("/api/text", srv.handleText)
	mux.HandleFunc("/api/photo", srv.handlePhoto)
	mux.HandleFunc("/api/photo-file", srv.handlePhotoFile)
	mux.HandleFunc("/api/posted-pics", srv.handlePostedPics)
	mux.HandleFunc("/api/reblog", srv.handleReblog)
	mux.HandleFunc("/api/session", srv.handleSession)
	mux.HandleFunc("/api/messages-reply", srv.handleMessagesReply)
	mux.HandleFunc("/api/open-chrome", srv.handleOpenChrome)
	mux.HandleFunc("/api/setup", srv.handleSetup)
	mux.HandleFunc("/api/setup-skip", srv.handleSetupSkip)

	ln, err := net.Listen("tcp", "127.0.0.1:8787")
	if err != nil {
		fmt.Println("Port 8787 is already in use by another GUI.")
		fmt.Println("That old process is what Run full setup is talking to — not this one.")
		fmt.Println("Ctrl+C the other terminal, then run: go run . -gui")
		return
	}
	addr := "http://" + ln.Addr().String()
	fmt.Println("GUI running at", addr)
	fmt.Println("Setup code", setupCodeStamp)
	fmt.Println("If Run full setup does not log that stamp, you are clicking an old GUI tab.")
	fmt.Println("Leave this terminal open. Ctrl+C to stop.")
	if guiOpenBrowser() {
		_ = openBrowser(addr)
	}

	if err := http.Serve(ln, mux); err != nil {
		fmt.Println("GUI server stopped:", err)
	}
}

func (s *guiServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(guiHTML))
}

func (s *guiServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := s.hub.subscribe()
	defer s.hub.unsubscribe(ch)

	fmt.Fprintf(w, "data: [%s] Connected. Setup code %s. Pick an action.\n\n", time.Now().Format("15:04:05"), setupCodeStamp)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", msg)
			flusher.Flush()
		}
	}
}

func (s *guiServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	busy := s.busy
	s.mu.Unlock()
	selected := ""
	if acc, ok := currentAccount(); ok {
		selected = acc.ID
	}
	accs, tally := publicAccountsWithTally()
	writeJSON(w, map[string]any{
		"busy":     busy,
		"selected": selected,
		"accounts": accs,
		"tally":    tally,
		"headless": headlessChrome,
	})
}

func (s *guiServer) handleHeadless(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var body struct {
		Headless bool `json:"headless"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := saveHeadlessPref(body.Headless); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if body.Headless {
		s.hub.broadcast("Hide Chrome on — bot Chrome windows (including logins) stay hidden.")
	} else {
		s.hub.broadcast("Hide Chrome off — bot Chrome windows are visible again.")
	}
	writeJSON(w, map[string]any{"ok": true, "headless": headlessChrome})
}

func requestedAccounts(id string) ([]Account, error) {
	id = strings.TrimSpace(id)
	if strings.EqualFold(id, "all") {
		accs := enabledAccounts()
		if len(accs) == 0 {
			return nil, fmt.Errorf("no enabled accounts in config/credentials.json")
		}
		return accs, nil
	}
	if id == "" {
		if acc, ok := currentAccount(); ok {
			return []Account{acc}, nil
		}
		return []Account{{}}, nil
	}
	acc, ok := findAccount(id)
	if !ok {
		return nil, fmt.Errorf("unknown account %q", id)
	}
	return []Account{acc}, nil
}

func guiAccounts(accountID string, accountIDs []string) ([]Account, error) {
	var ids []string
	seen := map[string]bool{}
	for _, raw := range accountIDs {
		id := accountSlug(raw)
		if id == "" || strings.EqualFold(id, "all") || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) > 0 {
		var out []Account
		for _, id := range ids {
			a, ok := findAccount(id)
			if !ok {
				return nil, fmt.Errorf("unknown account %q", id)
			}
			out = append(out, a)
		}
		return out, nil
	}
	if strings.TrimSpace(accountID) != "" {
		return requestedAccounts(accountID)
	}
	return nil, fmt.Errorf("select at least one account")
}

func parseFormAccountIDs(r *http.Request) []string {
	raw := strings.TrimSpace(r.FormValue("accountIds"))
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "[") {
		var ids []string
		if err := json.Unmarshal([]byte(raw), &ids); err == nil {
			return ids
		}
	}
	return strings.Split(raw, ",")
}

const minGUIReachBatchSlice = 5 * time.Minute

// runReachOnUntil splits a timed run into equal time slices per batch of
// `parallel` accounts, so every selected account gets a turn before the
// deadline instead of the first batch running full sessions the whole time.
func (s *guiServer) runReachOnUntil(accs []Account, parallel, maxFollows, likePosts int, until time.Time) error {
	if len(accs) == 0 {
		return fmt.Errorf("no accounts selected")
	}
	if until.IsZero() || parallel <= 1 || parallel >= len(accs) {
		return s.runReachOnAll(accs, parallel, maxFollows, likePosts, until)
	}
	batches := (len(accs) + parallel - 1) / parallel
	slice := time.Until(until) / time.Duration(batches)
	if slice < minGUIReachBatchSlice {
		// Short window: widen the batches so everyone still fits.
		fit := int(time.Until(until) / minGUIReachBatchSlice)
		if fit < 1 {
			fit = 1
		}
		parallel = (len(accs) + fit - 1) / fit
		batches = (len(accs) + parallel - 1) / parallel
		slice = time.Until(until) / time.Duration(batches)
	}
	s.hub.broadcast(fmt.Sprintf("Reach on %d accounts: %d batches of %d, about %s each", len(accs), batches, parallel, slice.Round(time.Minute)))
	var first error
	for b := 0; b < batches; b++ {
		if !time.Now().Before(until) || sessionExpired() {
			s.hub.broadcast(fmt.Sprintf("Time up after %d/%d batches — next pass starts with the rest.", b, batches))
			break
		}
		end := b*parallel + parallel
		if end > len(accs) {
			end = len(accs)
		}
		batch := accs[b*parallel : end]
		// Accounts finish their current engage after the stop time, so
		// re-split what's left or the last batch gets squeezed out.
		slice = time.Until(until) / time.Duration(batches-b)
		stop := time.Now().Add(slice)
		if stop.After(until) {
			stop = until
		}
		s.hub.broadcast(fmt.Sprintf("Batch %d/%d (%d accounts) until %s", b+1, batches, len(batch), stop.Format("15:04")))
		if err := s.runReachOnAll(batch, len(batch), maxFollows, likePosts, stop); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (s *guiServer) runReachOnAll(accs []Account, parallel, maxFollows, likePosts int, until time.Time) error {
	if parallel <= 1 || len(accs) == 1 {
		return s.forAccounts(accs, func() error {
			return guiFollowAndLike(s.hub, maxFollows, likePosts)
		})
	}
	if parallel > len(accs) {
		parallel = len(accs)
	}
	reachShardPool = accs
	reachLikerShards = len(accs)
	defer func() {
		reachShardPool = nil
		reachLikerShards = 0
	}()
	s.hub.broadcast(fmt.Sprintf("Reach on %d selected accounts, %d at a time", len(accs), parallel))
	fn := func(a Account) error {
		return runFollowLikersOnUntil(a, maxFollows, likePosts, until, s.hub.broadcast)
	}
	if parallel >= len(accs) {
		return runForAccountsParallel("follow", accs, fn)
	}
	return runForAccountsParallelLimited("follow", accs, parallel, fn)
}

func (s *guiServer) forAccounts(accs []Account, fn func() error) error {
	return s.forAccountsFiltered(accs, false, fn)
}

func (s *guiServer) forUniqueBlogs(accs []Account, fn func() error) error {
	return s.forAccountsFiltered(accs, true, fn)
}

func (s *guiServer) forAccountsFiltered(accs []Account, uniqueBlogs bool, fn func() error) error {
	var first error
	seen := map[string]string{}
	for _, a := range accs {
		if a.ID != "" {
			if len(accs) > 1 {
				s.hub.broadcast("—— " + a.ID + " (" + a.Email + ") ——")
			}
			if _, ok := findAccount(a.ID); ok {
				if err := setActiveAccount(a.ID); err != nil {
					s.hub.broadcast(err.Error())
					if first == nil {
						first = err
					}
					continue
				}
			}
		}
		if uniqueBlogs && len(accs) > 1 {
			id, err := currentTumblrIdentity()
			if err != nil {
				s.hub.broadcast("Failed on " + a.ID + ": " + err.Error())
				if first == nil {
					first = err
				}
				continue
			}
			if prev, ok := seen[id]; ok {
				s.hub.broadcast("Skip " + a.ID + " — same Tumblr as " + prev + " (@" + id + ")")
				continue
			}
			seen[id] = a.ID
		}
		if err := fn(); err != nil {
			label := a.ID
			if label == "" {
				label = "account"
			}
			s.hub.broadcast("Failed on " + label + ": " + err.Error())
			if first == nil {
				first = err
			}
		}
	}
	return first
}

func (s *guiServer) handleAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
			http.Error(w, err.Error(), 400)
			return
		}
		if strings.EqualFold(strings.TrimSpace(body.ID), "all") {
			writeJSON(w, map[string]any{"ok": true, "selected": "all", "accounts": publicAccounts()})
			return
		}
		if err := setActiveAccount(body.ID); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "selected": body.ID, "accounts": publicAccounts()})
		return
	}
	selected := ""
	if acc, ok := currentAccount(); ok {
		selected = acc.ID
	}
	accs, tally := publicAccountsWithTally()
	writeJSON(w, map[string]any{"accounts": accs, "selected": selected, "tally": tally})
}

func (s *guiServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var body struct {
		AccountID  string   `json:"accountId"`
		AccountIDs []string `json:"accountIds"`
		All        bool     `json:"all"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var accs []Account
	var err error
	if body.All || strings.EqualFold(strings.TrimSpace(body.AccountID), "all") {
		accs = enabledAccounts()
		if len(accs) == 0 {
			http.Error(w, "no enabled accounts", 400)
			return
		}
	} else {
		accs, err = guiAccounts(body.AccountID, body.AccountIDs)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	}
	label := "login"
	if len(accs) > 1 {
		label = fmt.Sprintf("login %d accounts", len(accs))
	} else if len(accs) == 1 && accs[0].ID != "" {
		label = "login " + accs[0].ID
	}
	if !s.tryStart(label) {
		http.Error(w, "busy", 409)
		return
	}
	go func() {
		s.hub.broadcast("Chrome will open once per selected profile.")
		s.done(guiLoginAccounts(s.hub, accs))
	}()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *guiServer) handleOpenChrome(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var body struct {
		AccountID  string   `json:"accountId"`
		AccountIDs []string `json:"accountIds"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	acc, err := oneAccountForChrome(body.AccountID, body.AccountIDs)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
	s.hub.broadcast("Opening Chrome for " + acc.ID + "…")
	go func() {
		s.openMu.Lock()
		defer s.openMu.Unlock()
		if err := guiOpenAccountChrome(s.hub, acc); err != nil {
			s.hub.broadcast("Error: " + err.Error())
			return
		}
	}()
}

func (s *guiServer) tryStart(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return false
	}
	s.busy = true
	clearSessionUntil()
	s.hub.broadcast("Starting: " + name)
	return true
}

func (s *guiServer) done(err error) {
	if err != nil {
		s.hub.broadcast("Error: " + err.Error())
	} else {
		s.hub.broadcast("Done.")
	}
	s.mu.Lock()
	s.busy = false
	s.mu.Unlock()
}

func (s *guiServer) handleFollow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var body struct {
		MaxFollows int      `json:"maxFollows"`
		LikePosts  int      `json:"likePosts"`
		AccountID  string   `json:"accountId"`
		AccountIDs []string `json:"accountIds"`
		Parallel   int      `json:"parallel"`
		Minutes    int      `json:"minutes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		http.Error(w, err.Error(), 400)
		return
	}
	if body.MaxFollows < 0 {
		body.MaxFollows = 0
	}
	if body.LikePosts <= 0 {
		body.LikePosts = 4
	}
	accs, err := guiAccounts(body.AccountID, body.AccountIDs)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	accs = orderAccountsForReach(accs, false)
	label := fmt.Sprintf("reach %d accounts, follow %d + like %d", len(accs), body.MaxFollows, body.LikePosts)
	if body.Minutes > 0 {
		label = fmt.Sprintf("%s for %d min", label, body.Minutes)
	}
	if !s.tryStart(label) {
		http.Error(w, "busy", 409)
		return
	}
	go func() {
		if body.Minutes > 0 {
			setSessionFor(time.Duration(body.Minutes) * time.Minute)
			s.hub.broadcast("Reach will stop at " + sessionUntil.Format("15:04"))
		}
		pass := 0
		runPass := func() error {
			run := rotateAccountSlice(accs, pass)
			pass++
			return s.runReachOnUntil(run, body.Parallel, body.MaxFollows, body.LikePosts, sessionUntil)
		}
		err := runPass()
		for body.Minutes > 0 && !sessionExpired() {
			if sessionRemaining() < 2*time.Minute {
				s.hub.broadcast("Less than 2 minutes left — not starting another pass.")
				break
			}
			s.hub.broadcast("Time left: " + sessionRemaining().Round(time.Minute).String() + " — another pass.")
			time.Sleep(time.Duration(40+rand.Intn(41)) * time.Second)
			if sessionExpired() {
				break
			}
			if err2 := runPass(); err2 != nil && err == nil {
				err = err2
			}
		}
		if body.Minutes > 0 {
			refreshResearchAfterRun(accs, s.hub.broadcast)
		}
		s.done(err)
	}()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *guiServer) handleResearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	body := struct {
		Days int   `json:"days"`
		Sync *bool `json:"sync"`
	}{}
	_ = json.NewDecoder(r.Body).Decode(&body)
	doSync := body.Sync == nil || *body.Sync
	if !researchRunning.CompareAndSwap(false, true) {
		http.Error(w, "research already running", 409)
		return
	}
	go func() {
		defer researchRunning.Store(false)
		s.hub.broadcast("Research report (runs alongside anything else)…")
		if err := runResearch(body.Days, doSync, func(string) {}); err != nil {
			s.hub.broadcast("Research error: " + err.Error())
			return
		}
		if rep, ok := loadResearchReport(); ok {
			for _, line := range researchHeadline(rep, 5) {
				s.hub.broadcast(line)
			}
		}
	}()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *guiServer) handleGrowth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	if !s.tryStart("growth report") {
		http.Error(w, "busy", 409)
		return
	}
	go func() {
		s.hub.broadcast("Refreshing follower counts…")
		refreshGrowthFollowers(enabledAccounts(), fetchGrowthFollowers, s.hub.broadcast)
		s.hub.broadcast("Scoring accounts + seed attribution…")
		err := runGrowthReport()
		if err == nil {
			if rep, ok := loadGrowthReport(); ok {
				s.hub.broadcast(rep.Summary)
				if len(rep.ReachOrder) > 0 {
					s.hub.broadcast("Reach will prefer: " + strings.Join(clipStrings(rep.ReachOrder, 8), ", "))
				}
				if len(rep.Parked) > 0 {
					s.hub.broadcast("Parked: " + strings.Join(clipStrings(rep.Parked, 8), ", "))
				}
			}
		}
		s.done(err)
	}()
	writeJSON(w, map[string]any{"ok": true})
}

// handleReachMidReplies runs reach on selected accounts while the N accounts
// with the most chats waiting on a reply watch DMs in parallel. Messaging
// accounts are pulled out of the reach pool so they don't fight over the
// same session.
func (s *guiServer) handleReachMidReplies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var body struct {
		MaxFollows int      `json:"maxFollows"`
		LikePosts  int      `json:"likePosts"`
		AccountID  string   `json:"accountId"`
		AccountIDs []string `json:"accountIds"`
		Parallel   int      `json:"parallel"`
		Minutes    int      `json:"minutes"`
		MidCount   int      `json:"midCount"`
		All        bool     `json:"all"`
		WatchHours float64  `json:"watchHours"`
		DryRun     bool     `json:"dryRun"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		http.Error(w, err.Error(), 400)
		return
	}
	if body.MaxFollows < 0 {
		body.MaxFollows = 0
	}
	if body.LikePosts <= 0 {
		body.LikePosts = 4
	}
	if body.MidCount <= 0 {
		body.MidCount = 5
	}
	if body.WatchHours <= 0 {
		body.WatchHours = 4
	}
	if body.Minutes <= 0 {
		body.Minutes = 90
	}
	accs, err := guiAccounts(body.AccountID, body.AccountIDs)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if body.All {
		label := fmt.Sprintf("reach + replies on all %d accounts for %d min", len(accs), body.Minutes)
		if !s.tryStart(label) {
			http.Error(w, "busy", 409)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
		go s.runReachAndRepliesAll(accs, body.Minutes, body.Parallel, body.MaxFollows, body.LikePosts, body.DryRun)
		return
	}
	label := fmt.Sprintf("reach + %d busiest inboxes for %d min / %.0fh watch", body.MidCount, body.Minutes, body.WatchHours)
	if !s.tryStart(label) {
		http.Error(w, "busy", 409)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
	go func() {
		s.hub.broadcast(fmt.Sprintf("Checking %d inboxes for chats waiting on a reply…", len(accs)))
		msgAccs, backlogs := busiestInboxAccounts(accs, body.MidCount)
		for _, b := range backlogs {
			if b.Waiting > 0 {
				s.hub.broadcast(fmt.Sprintf("  %s — %d waiting", b.Account.ID, b.Waiting))
			}
		}
		if len(msgAccs) < body.MidCount {
			// Quiet inboxes: fill the rest with the old middle-follower pick so
			// someone is still watching for new DMs.
			msgAccs = append(msgAccs, midFollowerAccounts(accountsExcept(accs, msgAccs), body.MidCount-len(msgAccs))...)
		}
		reachAccs := accountsExcept(accs, msgAccs)
		if len(reachAccs) == 0 {
			// Selection was tiny — let reach use everyone except leave messaging running.
			reachAccs = append([]Account{}, accs...)
		}
		reachAccs = orderAccountsForReach(reachAccs, false)
		s.hub.broadcast(fmt.Sprintf("Reach on %d accounts, replies on %d", len(reachAccs), len(msgAccs)))
		reachDur := time.Duration(body.Minutes) * time.Minute
		watchDur := time.Duration(body.WatchHours * float64(time.Hour))
		setSessionFor(maxDuration(reachDur, watchDur))
		reachDeadline := time.Now().Add(reachDur)
		s.hub.broadcast("Session until " + sessionUntil.Format("15:04") +
			fmt.Sprintf(" (reach until %s)", reachDeadline.Format("15:04")))
		for i, a := range msgAccs {
			blog, fol := bestAccountBlogStats(a.ID, loadFollowerLog(a.ID))
			tag := a.ID
			if blog != "" {
				tag = "@" + blog
			}
			s.hub.broadcast(fmt.Sprintf("  mid-reply %d/%d %s (%d followers)", i+1, len(msgAccs), tag, fol))
		}
		var wg sync.WaitGroup
		var reachErr, msgErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			pass := 0
			for !sessionExpired() && time.Now().Before(reachDeadline) {
				if time.Until(reachDeadline) < 2*time.Minute && pass > 0 {
					s.hub.broadcast("Reach: less than 2 minutes left — stopping passes.")
					break
				}
				run := rotateAccountSlice(reachAccs, pass)
				pass++
				s.hub.broadcast(fmt.Sprintf("Reach pass %d on %d accounts…", pass, len(run)))
				if err := s.runReachOnUntil(run, body.Parallel, body.MaxFollows, body.LikePosts, reachDeadline); err != nil && reachErr == nil {
					reachErr = err
				}
			}
			if !sessionExpired() {
				s.hub.broadcast("Reach minutes done — DMs keep watching.")
			}
		}()
		go func() {
			defer wg.Done()
			msgErr = guiMessagesReplyMany(s.hub, msgAccs, body.DryRun, body.WatchHours, false)
		}()
		wg.Wait()
		if msgErr != nil {
			s.done(msgErr)
			return
		}
		s.done(reachErr)
	}()
}

// runReachAndRepliesAll runs reach and DM replies on every selected account
// for the same window. Replies go through every inbox once per pass (busiest
// first) instead of parking 3 accounts on a long watch, so all accounts get
// checked repeatedly.
func (s *guiServer) runReachAndRepliesAll(accs []Account, minutes, parallel, maxFollows, likePosts int, dry bool) {
	dur := time.Duration(minutes) * time.Minute
	setSessionFor(dur)
	deadline := sessionUntil
	s.hub.broadcast(fmt.Sprintf("Reach + replies on %d accounts until %s", len(accs), deadline.Format("15:04")))
	reachAccs := orderAccountsForReach(append([]Account{}, accs...), false)

	var wg sync.WaitGroup
	var reachErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		pass := 0
		for !sessionExpired() && time.Until(deadline) > 2*time.Minute {
			run := rotateAccountSlice(reachAccs, pass)
			pass++
			s.hub.broadcast(fmt.Sprintf("Reach pass %d on %d accounts…", pass, len(run)))
			if err := s.runReachOnUntil(run, parallel, maxFollows, likePosts, deadline); err != nil && reachErr == nil {
				reachErr = err
			}
		}
		s.hub.broadcast("Reach: time is up.")
	}()
	go func() {
		defer wg.Done()
		pass := 0
		for !sessionExpired() && time.Until(deadline) > time.Minute {
			pass++
			busy, backlogs := busiestInboxAccounts(accs, len(accs))
			waiting := 0
			for _, b := range backlogs {
				waiting += b.Waiting
			}
			order := append(busy, accountsExcept(accs, busy)...)
			s.hub.broadcast(fmt.Sprintf("Replies pass %d: %d chats waiting across %d inboxes", pass, waiting, len(order)))
			_ = guiMessagesReplyMany(s.hub, order, dry, 0, true)
			if time.Until(deadline) > 2*time.Minute {
				time.Sleep(time.Duration(45+rand.Intn(30)) * time.Second)
			}
		}
		s.hub.broadcast("Replies: time is up.")
	}()
	wg.Wait()
	refreshResearchAfterRun(accs, s.hub.broadcast)
	s.done(reachErr)
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func accountIDsOf(accs []Account) []string {
	out := make([]string, 0, len(accs))
	for _, a := range accs {
		out = append(out, a.ID)
	}
	return out
}

func (s *guiServer) handleLikeNSFW(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var body struct {
		Count      int      `json:"count"`
		AccountID  string   `json:"accountId"`
		AccountIDs []string `json:"accountIds"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Count <= 0 {
		body.Count = 1
	}
	accs, err := guiAccounts(body.AccountID, body.AccountIDs)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if !s.tryStart(fmt.Sprintf("like %d NSFW posts", body.Count)) {
		http.Error(w, "busy", 409)
		return
	}
	go func() {
		err := s.forAccounts(accs, func() error {
			return guiLikeNSFW(s.hub, body.Count)
		})
		s.done(err)
	}()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *guiServer) handleText(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var body struct {
		Text       string   `json:"text"`
		AccountID  string   `json:"accountId"`
		AccountIDs []string `json:"accountIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		http.Error(w, "text required", 400)
		return
	}
	accs, err := guiAccounts(body.AccountID, body.AccountIDs)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if !s.tryStart("text post") {
		http.Error(w, "busy", 409)
		return
	}
	go func() {
		err := s.forUniqueBlogs(accs, func() error {
			return guiTextPost(s.hub, body.Text)
		})
		s.done(err)
	}()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *guiServer) handlePhoto(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	file, hdr, err := r.FormFile("image")
	if err != nil {
		http.Error(w, "image required", 400)
		return
	}
	defer file.Close()
	caption := r.FormValue("caption")
	pin := formTruthy(r.FormValue("pin"))
	accs, accErr := guiAccounts(r.FormValue("accountId"), parseFormAccountIDs(r))
	if accErr != nil {
		http.Error(w, accErr.Error(), 400)
		return
	}

	ext := filepath.Ext(hdr.Filename)
	if ext == "" {
		ctype := hdr.Header.Get("Content-Type")
		if ctype != "" {
			if exts, _ := mime.ExtensionsByType(ctype); len(exts) > 0 {
				ext = exts[0]
			}
		}
		if ext == "" {
			ext = ".jpg"
		}
	}
	tmp, err := os.CreateTemp("", "tumblr-gui-*"+ext)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	tmpPath := tmp.Name()
	if _, err := io.Copy(tmp, file); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		http.Error(w, err.Error(), 500)
		return
	}
	tmp.Close()

	if !s.tryStart("photo post") {
		_ = os.Remove(tmpPath)
		http.Error(w, "busy", 409)
		return
	}
	go func() {
		defer os.Remove(tmpPath)
		err := s.forUniqueBlogs(accs, func() error {
			return guiPhotoPost(s.hub, tmpPath, caption, pin)
		})
		s.done(err)
	}()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *guiServer) handleReblog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var body struct {
		Category   string   `json:"category"`
		Count      int      `json:"count"`
		AccountID  string   `json:"accountId"`
		AccountIDs []string `json:"accountIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		http.Error(w, err.Error(), 400)
		return
	}
	if body.Count <= 0 {
		body.Count = 3
	}
	if body.Category == "" {
		body.Category = "mix"
	}
	accs, err := guiAccounts(body.AccountID, body.AccountIDs)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if !s.tryStart(fmt.Sprintf("reblog %d (%s)", body.Count, body.Category)) {
		http.Error(w, "busy", 409)
		return
	}
	go func() {
		err := s.forAccounts(accs, func() error {
			return guiReblog(s.hub, body.Category, body.Count)
		})
		s.done(err)
	}()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *guiServer) handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var body struct {
		AccountID  string   `json:"accountId"`
		AccountIDs []string `json:"accountIds"`
		Minutes    int      `json:"minutes"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	accs, err := guiAccounts(body.AccountID, body.AccountIDs)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	label := "mixed session"
	if body.Minutes > 0 {
		label = fmt.Sprintf("mixed session %d min", body.Minutes)
	}
	if !s.tryStart(label) {
		http.Error(w, "busy", 409)
		return
	}
	go func() {
		if body.Minutes > 0 {
			setSessionFor(time.Duration(body.Minutes) * time.Minute)
		}
		s.hub.broadcast("Mixed session running — detail also prints in the terminal…")
		err := s.forAccounts(accs, func() error {
			runHumanize()
			return nil
		})
		s.done(err)
	}()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *guiServer) handlePostedPics(w http.ResponseWriter, r *http.Request) {
	pics, err := listPostedPics()
	if err != nil {
		writeJSON(w, map[string]any{"pics": []string{}})
		return
	}
	names := make([]string, 0, len(pics))
	for _, p := range pics {
		names = append(names, filepath.Base(p))
	}
	writeJSON(w, map[string]any{"pics": names})
}

func (s *guiServer) handlePhotoFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var body struct {
		File       string   `json:"file"`
		Caption    string   `json:"caption"`
		Pin        bool     `json:"pin"`
		AccountID  string   `json:"accountId"`
		AccountIDs []string `json:"accountIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		http.Error(w, err.Error(), 400)
		return
	}
	name := filepath.Base(strings.TrimSpace(body.File))
	if name == "" || name == "." || name == string(filepath.Separator) {
		http.Error(w, "pick a picture from pics/posted", 400)
		return
	}
	path := filepath.Join(picsPosted, name)
	if _, err := os.Stat(path); err != nil {
		http.Error(w, "file not found: "+name, 400)
		return
	}
	accs, err := guiAccounts(body.AccountID, body.AccountIDs)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if !s.tryStart("photo post " + name) {
		http.Error(w, "busy", 409)
		return
	}
	go func() {
		s.hub.broadcast("Posting " + name + " on " + fmt.Sprintf("%d", len(accs)) + " selected blogs")
		err := s.forUniqueBlogs(accs, func() error {
			return guiPhotoPost(s.hub, path, body.Caption, body.Pin)
		})
		s.done(err)
	}()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *guiServer) handleMessagesReply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var body struct {
		AccountID  string   `json:"accountId"`
		AccountIDs []string `json:"accountIds"`
		DryRun     bool     `json:"dryRun"`
		Watch      bool     `json:"watch"`
		WatchHours float64  `json:"watchHours"`
		Once       bool     `json:"once"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	accs, err := guiAccounts(body.AccountID, body.AccountIDs)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	label := "message replies"
	if body.DryRun {
		label = "draft message replies"
	}
	if body.Watch && !body.Once {
		label += " (watching)"
	}
	if !s.tryStart(label) {
		http.Error(w, "busy", 409)
		return
	}
	go func() {
		once := !body.Watch
		hours := body.WatchHours
		if body.Watch && hours <= 0 {
			hours = 9
		}
		if !body.Watch {
			hours = 0
		}
		var err error
		if len(accs) > 1 {
			err = guiMessagesReplyMany(s.hub, accs, body.DryRun, hours, once)
		} else {
			err = s.forAccounts(accs, func() error {
				return guiMessagesReply(s.hub, body.DryRun, hours, once)
			})
		}
		s.done(err)
	}()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *guiServer) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var body struct {
		AccountID  string   `json:"accountId"`
		AccountIDs []string `json:"accountIds"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	accs, err := guiAccounts(body.AccountID, body.AccountIDs)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if !s.tryStart(fmt.Sprintf("full setup on %d account(s)", len(accs))) {
		http.Error(w, "busy", 409)
		return
	}
	go func() {
		s.hub.broadcast("Using " + setupCodeStamp + " — if you do not see this, Ctrl+C and run: go run . -gui")
		s.hub.broadcast("Account settings first (English, birthday, AgeKey), then appearance.")
		s.hub.broadcast("If Windows Security appears, type the AgeKey passcode yourself.")
		err := s.forAccounts(accs, func() error {
			return guiFullAccountSetup(s.hub)
		})
		s.done(err)
	}()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *guiServer) handleSetupSkip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	requestSetupSkip()
	s.hub.broadcast("Skip requested — finish or close this Chrome window, then the next account starts.")
	writeJSON(w, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func openBrowser(url string) error {
	return exec.Command("cmd", "/c", "start", "", url).Start()
}

func guiOpenBrowser() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("TUMBLR_GUI_NO_BROWSER")))
	return v == "" || v == "0" || v == "false" || v == "no"
}
