package main

import (
	"encoding/json"
	"fmt"
	"io"
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
	hub  *logHub
	mu   sync.Mutex
	busy bool
}

func runGUI() {
	srv := &guiServer{hub: newLogHub()}

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.handleIndex)
	mux.HandleFunc("/api/events", srv.handleEvents)
	mux.HandleFunc("/api/status", srv.handleStatus)
	mux.HandleFunc("/api/follow", srv.handleFollow)
	mux.HandleFunc("/api/like-nsfw", srv.handleLikeNSFW)
	mux.HandleFunc("/api/text", srv.handleText)
	mux.HandleFunc("/api/photo", srv.handlePhoto)
	mux.HandleFunc("/api/reblog", srv.handleReblog)
	mux.HandleFunc("/api/session", srv.handleSession)

	ln, err := net.Listen("tcp", "127.0.0.1:8787")
	if err != nil {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fmt.Println("Could not start GUI server:", err)
			return
		}
	}
	addr := "http://" + ln.Addr().String()
	fmt.Println("GUI running at", addr)
	fmt.Println("Leave this terminal open. Ctrl+C to stop.")
	_ = openBrowser(addr)

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

	fmt.Fprintf(w, "data: [%s] Connected. Pick an action.\n\n", time.Now().Format("15:04:05"))
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
	writeJSON(w, map[string]any{"busy": busy})
}

func (s *guiServer) tryStart(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return false
	}
	s.busy = true
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
		MaxFollows int `json:"maxFollows"`
		LikePosts  int `json:"likePosts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && err != io.EOF {
		http.Error(w, err.Error(), 400)
		return
	}
	if body.MaxFollows <= 0 {
		body.MaxFollows = 5
	}
	if body.LikePosts <= 0 {
		body.LikePosts = 2
	}
	if !s.tryStart(fmt.Sprintf("follow %d + like %d posts", body.MaxFollows, body.LikePosts)) {
		http.Error(w, "busy", 409)
		return
	}
	go func() {
		err := guiFollowAndLike(s.hub, body.MaxFollows, body.LikePosts)
		s.done(err)
	}()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *guiServer) handleLikeNSFW(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var body struct {
		Count int `json:"count"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Count <= 0 {
		body.Count = 8
	}
	if !s.tryStart(fmt.Sprintf("like %d NSFW posts", body.Count)) {
		http.Error(w, "busy", 409)
		return
	}
	go func() {
		err := guiLikeNSFW(s.hub, body.Count)
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
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		http.Error(w, "text required", 400)
		return
	}
	if !s.tryStart("text post") {
		http.Error(w, "busy", 409)
		return
	}
	go func() {
		err := guiTextPost(s.hub, body.Text)
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
		err := guiPhotoPost(s.hub, tmpPath, caption)
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
		Category string `json:"category"`
		Count    int    `json:"count"`
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
	if !s.tryStart(fmt.Sprintf("reblog %d (%s)", body.Count, body.Category)) {
		http.Error(w, "busy", 409)
		return
	}
	go func() {
		err := guiReblog(s.hub, body.Category, body.Count)
		s.done(err)
	}()
	writeJSON(w, map[string]any{"ok": true})
}

func (s *guiServer) handleSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	if !s.tryStart("mixed session") {
		http.Error(w, "busy", 409)
		return
	}
	go func() {
		s.hub.broadcast("Mixed session running — detail also prints in the terminal…")
		runHumanize()
		s.done(nil)
	}()
	writeJSON(w, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func openBrowser(url string) error {
	return exec.Command("cmd", "/c", "start", "", url).Start()
}
