package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	picsDir    = "pics"
	picsPosted = "pics/posted"
)

func createPhotoPost(client *http.Client, auth Auth, imagePath, caption string) error {
	return createPhotoPostPinned(client, auth, imagePath, caption, false)
}

func createPhotoPostPinned(client *http.Client, auth Auth, imagePath, caption string, pin bool) error {
	return createPhotoPostOnPinned(client, auth, auth.Blog, imagePath, caption, pin)
}

func createPhotoPostOn(client *http.Client, auth Auth, blogID, imagePath, caption string) error {
	return createPhotoPostOnPinned(client, auth, blogID, imagePath, caption, false)
}

func createPhotoPostOnPinned(client *http.Client, auth Auth, blogID, imagePath, caption string, pin bool) error {
	auth.Blog = blogID
	name, err := verifyAccountBlog(client, &auth)
	if err != nil {
		return err
	}
	fmt.Printf("Verified photo post target: @%s (account %s)\n", name, accountIDFromAuth(auth))
	body, status, err := createPhotoPostState(client, auth, imagePath, caption, "published")
	// NPF rejects link formatting on mathematical-script glyphs (fancy Clicky).
	// Legacy /post with HTML caption keeps the look + working beacons link.
	if err != nil && isVideoPath(imagePath) {
		fmt.Println("NPF video rejected — retrying legacy video upload…")
		body, status, err = createVideoPostLegacy(client, auth, imagePath, captionToLegacyHTML(caption), "published")
	} else if err != nil && !isVideoPath(imagePath) {
		fmt.Println("NPF photo rejected — retrying legacy photo upload (HTML Clicky link)…")
		body, status, err = createPhotoPostLegacy(client, auth, imagePath, captionToLegacyHTML(caption), "published")
	}
	fmt.Println("Status:", status)
	if err != nil {
		fmt.Println(truncate(string(body), 200))
		return err
	}
	id := parseCreatedPostID(body)
	kind := "photo"
	if isVideoPath(imagePath) {
		kind = "video"
	}
	record := func(pinned bool) {
		recordPost(ledgerEntry{
			Account: accountIDFromAuth(auth),
			Blog:    name,
			Kind:    kind,
			Text:    caption,
			Media:   filepath.Base(imagePath),
			PostID:  id,
			Pinned:  pinned,
		})
	}
	if !pin {
		record(false)
		return nil
	}
	if id == "" {
		record(false)
		return fmt.Errorf("posted, but Tumblr did not return an id to pin")
	}
	// Videos need a moment before Tumblr accepts pin (same API the GUI uses).
	if isVideoPath(imagePath) {
		time.Sleep(3 * time.Second)
	}
	if err := pinBlogPost(client, &auth, id); err != nil {
		record(false)
		return fmt.Errorf("posted, but pin failed: %w", err)
	}
	record(true)
	fmt.Println("Pinned post", id)
	return nil
}

func parseCreatedPostID(body []byte) string {
	var wrap struct {
		Response struct {
			ID       json.RawMessage `json:"id"`
			IDString string          `json:"id_string"`
		} `json:"response"`
	}
	if json.Unmarshal(body, &wrap) != nil {
		return ""
	}
	if s := strings.TrimSpace(wrap.Response.IDString); s != "" {
		return s
	}
	raw := strings.TrimSpace(string(wrap.Response.ID))
	raw = strings.Trim(raw, `"`)
	if raw == "" || raw == "null" {
		return ""
	}
	return raw
}

// pinBlogPost uses the same pin endpoints as the GUI photo-pin checkbox.
func pinBlogPost(client *http.Client, auth *Auth, postID string) error {
	if auth == nil {
		return fmt.Errorf("missing auth")
	}
	postID = strings.TrimSpace(postID)
	blog := strings.TrimSpace(auth.Blog)
	if postID == "" || blog == "" {
		return fmt.Errorf("missing blog or post id")
	}
	var last error
	for attempt := 0; attempt < 5; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt*2) * time.Second)
		}
		if err := pinBlogPostOnce(client, auth, blog, postID); err == nil {
			return nil
		} else {
			last = err
		}
	}
	if last == nil {
		return fmt.Errorf("pin failed")
	}
	return last
}

func pinBlogPostOnce(client *http.Client, auth *Auth, blog, postID string) error {
	tries := []struct {
		method, path string
		body         map[string]any
	}{
		{"PUT", "/posts/" + postID, map[string]any{"pin": true}},
		{"PUT", "/posts/" + postID, map[string]any{"is_pinned": true}},
		{"POST", "/posts/" + postID + "/pin", map[string]any{"pin": true}},
	}
	var last error
	for _, t := range tries {
		raw, err := json.Marshal(t.body)
		if err != nil {
			return err
		}
		u := "https://www.tumblr.com/api/v2/blog/" + blog + t.path
		req, err := http.NewRequest(t.method, u, bytes.NewReader(raw))
		if err != nil {
			return err
		}
		setCommonHeaders(req, *auth)
		req.Header.Set("Content-Type", "application/json; charset=utf8")
		req.Header.Set("X-CSRF", auth.CSRF)
		req.Header.Set("Referer", "https://www.tumblr.com/blog/"+blog)
		resp, err := client.Do(req)
		if err != nil {
			last = err
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		takeCSRF(resp.Header, auth)
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		last = fmt.Errorf("%s %s: %s %s", t.method, t.path, resp.Status, truncate(string(b), 160))
	}
	if last == nil {
		return fmt.Errorf("pin failed")
	}
	return last
}

// newPinnedScanPosts is how deep we look for an existing redo intro: reach
// reblogs bury the pinned post 100-200 posts down, and missing it means a
// duplicate video post.
const newPinnedScanPosts = 600

// findNewPinnedCaptionPost looks for the redo pinned intro already on the blog.
func findNewPinnedCaptionPost(client *http.Client, auth Auth, blog string) (id string, alreadyPinned bool, err error) {
	for offset := 0; offset < newPinnedScanPosts; offset += 20 {
		posts, err := fetchBlogPostsPage(client, &auth, blog, 20, offset)
		if err != nil {
			if offset > 0 {
				return "", false, fmt.Errorf("scan for existing pinned post stopped at %d: %w", offset, err)
			}
			return "", false, err
		}
		if len(posts) == 0 {
			break
		}
		for _, p := range posts {
			if !isNewPinnedIntro(p) {
				continue
			}
			if pid := postIDFromMap(p); pid != "" {
				return pid, isPinnedPost(p), nil
			}
		}
	}
	return "", false, nil
}

func isNewPinnedIntro(p map[string]any) bool {
	blob := strings.ToLower(postTextBlob(p))
	return strings.Contains(blob, "redoing pinned") ||
		(strings.Contains(blob, "skyrim") && strings.Contains(blob, "beacons.ai/lil.cosmic.bunny"))
}

func postIDFromMap(m map[string]any) string {
	if s, ok := m["id_string"].(string); ok && strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	switch v := m["id"].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case json.Number:
		return v.String()
	}
	return ""
}

func postTextBlob(m map[string]any) string {
	var parts []string
	for _, k := range []string{"summary", "caption", "body", "title"} {
		if s, ok := m[k].(string); ok && s != "" {
			parts = append(parts, s)
		}
	}
	if content, ok := m["content"].([]any); ok {
		for _, el := range content {
			blk, ok := el.(map[string]any)
			if !ok {
				continue
			}
			if s, ok := blk["text"].(string); ok && s != "" {
				parts = append(parts, s)
			}
		}
	}
	if trail, ok := m["trail"].([]any); ok {
		for _, el := range trail {
			t, ok := el.(map[string]any)
			if !ok {
				continue
			}
			if c, ok := t["content_raw"].(string); ok {
				parts = append(parts, c)
			}
			if c, ok := t["content"].(string); ok {
				parts = append(parts, c)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// ensureMediaPinnedOnBlog posts only if needed, then pins (GUI pin API).
func ensureMediaPinnedOnBlog(client *http.Client, auth *Auth, blogID, imagePath, caption string, pin bool) error {
	if auth == nil {
		return fmt.Errorf("missing auth")
	}
	auth.Blog = blogID
	if pin && isNewPinnedIntro(map[string]any{"caption": caption}) {
		id, pinned, err := findNewPinnedCaptionPost(client, *auth, blogID)
		if err != nil {
			return fmt.Errorf("could not check for an existing pinned post (not posting a duplicate): %w", err)
		}
		if id != "" {
			if pinned {
				fmt.Println("  already posted + pinned", id, "— skipping")
				return nil
			}
			fmt.Println("  already posted", id, "— pinning only (no duplicate)")
			if err := pinBlogPost(client, auth, id); err != nil {
				return fmt.Errorf("pin existing post: %w", err)
			}
			fmt.Println("Pinned post", id)
			return nil
		}
	}
	return createPhotoPostOnPinned(client, *auth, blogID, imagePath, caption, pin)
}

func formTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func createVideoPostLegacy(client *http.Client, auth Auth, imagePath, caption, state string) ([]byte, string, error) {
	return createLegacyMediaPost(client, auth, imagePath, caption, state, "video", "https://www.tumblr.com/new/video")
}

func createPhotoPostLegacy(client *http.Client, auth Auth, imagePath, captionHTML, state string) ([]byte, string, error) {
	return createLegacyMediaPost(client, auth, imagePath, captionHTML, state, "photo", "https://www.tumblr.com/new/photo")
}

func createLegacyMediaPost(client *http.Client, auth Auth, imagePath, caption, state, typ, referer string) ([]byte, string, error) {
	file, err := os.Open(imagePath)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("type", typ)
	_ = w.WriteField("state", state)
	if strings.TrimSpace(caption) != "" {
		_ = w.WriteField("caption", normalizeCaption(caption))
	}
	part, err := w.CreateFormFile("data", filepath.Base(imagePath))
	if err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}

	postURL := "https://www.tumblr.com/api/v2/blog/" + auth.Blog + "/post"
	req, err := http.NewRequest("POST", postURL, &body)
	if err != nil {
		return nil, "", err
	}
	setCommonHeaders(req, auth)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", referer)

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.Status, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return respBody, resp.Status, fmt.Errorf("%s post failed: %s — %s", typ, resp.Status, string(respBody))
	}
	return respBody, resp.Status, nil
}

func isVideoPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".m4v", ".webm", ".mov":
		return true
	default:
		return false
	}
}

// videoDimensions is best-effort (tkhd atom). Zero means omit from the NPF body.
func videoDimensions(path string) (int, int) {
	b, err := os.ReadFile(path)
	if err != nil || len(b) < 16 {
		return 0, 0
	}
	for i := 0; i+84 < len(b); i++ {
		if string(b[i:i+4]) != "tkhd" {
			continue
		}
		version := b[i+4]
		off := i + 5
		if version == 1 {
			off += 3 + 8 + 8 + 4 + 8 + 8 + 4 + 4
		} else {
			off += 3 + 4 + 4 + 4 + 4 + 4 + 4 + 4
		}
		if off+20+4 > len(b) {
			continue
		}
		off += 4 + 2 + 2 + 8*4 // reserved, layer, alt group, volume, reserved, matrix skip starts
		// After flags/times/trackID/duration/reserved, matrix is 36 bytes, then width/height as 16.16
		// Use a second scan: last 8 bytes of tkhd are width/height 16.16
	}
	// Walk boxes properly.
	return mp4TKHDSize(b)
}

func mp4TKHDSize(b []byte) (int, int) {
	var w, h int
	var walk func(int, int)
	walk = func(start, end int) {
		i := start
		for i+8 <= end && i+8 <= len(b) {
			size := int(uint32(b[i])<<24 | uint32(b[i+1])<<16 | uint32(b[i+2])<<8 | uint32(b[i+3]))
			typ := string(b[i+4 : i+8])
			if size < 8 {
				return
			}
			boxEnd := i + size
			if size == 1 && i+16 <= end {
				// 64-bit largesize
				boxEnd = i + int(uint64(b[i+8])<<56|uint64(b[i+9])<<48|uint64(b[i+10])<<40|uint64(b[i+11])<<32|uint64(b[i+12])<<24|uint64(b[i+13])<<16|uint64(b[i+14])<<8|uint64(b[i+15]))
			}
			if boxEnd > end || boxEnd > len(b) {
				return
			}
			payload := i + 8
			if size == 1 {
				payload = i + 16
			}
			switch typ {
			case "moov", "trak", "mdia", "minf", "stbl":
				walk(payload, boxEnd)
			case "tkhd":
				if ww, hh, ok := parseTKHD(b[payload:boxEnd]); ok && ww > 0 && hh > 0 && ww <= 7680 && hh <= 7680 {
					if ww*hh >= w*h {
						w, h = ww, hh
					}
				}
			}
			if size == 0 {
				return
			}
			i = boxEnd
		}
	}
	walk(0, len(b))
	return w, h
}

func parseTKHD(payload []byte) (int, int, bool) {
	if len(payload) < 4 {
		return 0, 0, false
	}
	version := payload[0]
	// width/height are the last two 16.16 fixed values
	if len(payload) < 8 {
		return 0, 0, false
	}
	wRaw := payload[len(payload)-8 : len(payload)-4]
	hRaw := payload[len(payload)-4:]
	w := int(uint32(wRaw[0])<<24 | uint32(wRaw[1])<<16 | uint32(wRaw[2])<<8 | uint32(wRaw[3]))
	h := int(uint32(hRaw[0])<<24 | uint32(hRaw[1])<<16 | uint32(hRaw[2])<<8 | uint32(hRaw[3]))
	w, h = w>>16, h>>16
	_ = version
	if w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

func normalizeCaption(caption string) string {
	caption = strings.ReplaceAll(caption, "\r\n", "\n")
	return strings.ReplaceAll(caption, "\r", "\n")
}

func npfCaptionBlocks(caption string) []map[string]any {
	caption = normalizeCaption(caption)
	if strings.TrimSpace(caption) == "" {
		return nil
	}
	lines := strings.Split(caption, "\n")
	out := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			line = " "
		}
		out = append(out, npfTextBlock(line))
	}
	return out
}

// npfTextBlock builds a text block. Supports markdown [label](https://…) links
// (label can be fancy unicode like 𝓒𝓵𝓲𝓬𝓴𝔂) and auto-linkifies bare http(s) URLs.
// Formatting offsets are UTF-16 code units (Tumblr NPF).
func npfTextBlock(line string) map[string]any {
	text, formatting := npfParseLine(line)
	blk := map[string]any{"type": "text", "text": text}
	if len(formatting) > 0 {
		blk["formatting"] = formatting
	}
	return blk
}

func npfParseLine(line string) (string, []map[string]any) {
	var out strings.Builder
	var fmts []map[string]any
	i := 0
	for i < len(line) {
		if line[i] == '[' {
			if n, label, url, ok := parseMarkdownLink(line[i:]); ok {
				start := utf16Len(out.String())
				out.WriteString(label)
				end := utf16Len(out.String())
				fmts = append(fmts, map[string]any{
					"start": start,
					"end":   end,
					"type":  "link",
					"url":   url,
				})
				i += n
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(line[i:])
		out.WriteRune(r)
		i += size
	}
	text := out.String()
	covered := make([]bool, utf16Len(text))
	for _, f := range fmts {
		start, _ := f["start"].(int)
		end, _ := f["end"].(int)
		for j := start; j < end && j < len(covered); j++ {
			covered[j] = true
		}
	}
	for _, span := range npfURLSpansUTF16(text) {
		overlap := false
		for j := span.start; j < span.end && j < len(covered); j++ {
			if covered[j] {
				overlap = true
				break
			}
		}
		if overlap {
			continue
		}
		fmts = append(fmts, map[string]any{
			"start": span.start,
			"end":   span.end,
			"type":  "link",
			"url":   span.url,
		})
	}
	return text, fmts
}

func parseMarkdownLink(s string) (consumed int, label, url string, ok bool) {
	if !strings.HasPrefix(s, "[") {
		return 0, "", "", false
	}
	closeBracket := strings.IndexByte(s, ']')
	if closeBracket < 1 {
		return 0, "", "", false
	}
	if closeBracket+1 >= len(s) || s[closeBracket+1] != '(' {
		return 0, "", "", false
	}
	closeParen := strings.IndexByte(s[closeBracket+2:], ')')
	if closeParen < 0 {
		return 0, "", "", false
	}
	label = s[1:closeBracket]
	url = strings.TrimSpace(s[closeBracket+2 : closeBracket+2+closeParen])
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return 0, "", "", false
	}
	if strings.TrimSpace(label) == "" {
		return 0, "", "", false
	}
	consumed = closeBracket + 2 + closeParen + 1
	return consumed, label, url, true
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

type npfURLSpan struct {
	start, end int
	url        string
}

func npfURLSpans(s string) []npfURLSpan {
	// Kept for older tests; prefer UTF-16 spans for NPF.
	return npfURLSpansUTF16(s)
}

func npfURLSpansUTF16(s string) []npfURLSpan {
	var out []npfURLSpan
	runes := []rune(s)
	i := 0
	for i < len(runes) {
		rest := string(runes[i:])
		idx := strings.Index(rest, "https://")
		scheme := "https://"
		if idx < 0 {
			idx = strings.Index(rest, "http://")
			scheme = "http://"
		}
		if idx < 0 {
			break
		}
		// idx is byte offset into rest; convert to rune index from i.
		runeIdx := i + len([]rune(rest[:idx]))
		schemeRunes := []rune(scheme)
		j := runeIdx + len(schemeRunes)
		for j < len(runes) {
			c := runes[j]
			if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '<' || c == '>' || c == '"' {
				break
			}
			j++
		}
		end := j
		for end > runeIdx+len(schemeRunes) {
			c := runes[end-1]
			if c == '.' || c == ',' || c == ';' || c == ':' || c == ')' || c == ']' || c == '!' || c == '?' {
				end--
				continue
			}
			break
		}
		url := string(runes[runeIdx:end])
		if len(url) > len(scheme) {
			startU16 := utf16Len(string(runes[:runeIdx]))
			endU16 := utf16Len(string(runes[:end]))
			out = append(out, npfURLSpan{start: startU16, end: endU16, url: url})
		}
		i = end
	}
	return out
}

// zoeyNewPinnedCaption is the pinned intro with fancy Clicky → beacons.
func zoeyNewPinnedCaption() string {
	clicky := "𝓒𝓵𝓲𝓬𝓴𝔂"
	return strings.Join([]string{
		"Hiii 🥰",
		"",
		"i'm zoey, i'm 25 and 5'4, and im obsessed with the gym, skyrim and art 🥰",
		"",
		"i'm new here so figuring it out, feel free to say hello 🥰🥰",
		"",
		"[" + clicky + "](" + beaconsLink + ")",
		"#d4ddys slvt",
		"#anime and manga",
		"#art",
		"#attractive",
		"#attractive blonde woman",
		"#attractive body types women",
		"#attractive girls",
		"#attractive woman",
		"#attractive young lady",
		"#beautiful model",
		"#dd lg babygirl",
		"#dd lg blog",
		"#dd lg brat",
		"#ddlglittle",
		"#dd/lg princess",
		"#dd/lg brat",
		"#dd/lg breeding",
		"#curvy model",
		"#bikini model",
		"#follow my onlyfans",
		"#only f@nz",
		"#onlyfans sale",
		"#onlyfans subscription",
		"#girl",
		"#good girl thoughts",
		"#good girl training",
		"#good girls don't think",
		"#good girls get wet",
		"#good girls obey",
		"#good girls rub their minds away",
	}, "\n")
}

// captionToLegacyHTML turns markdown [label](url) lines into HTML anchors for
// the legacy video /post endpoint (NPF formatting is not available there).
func captionToLegacyHTML(caption string) string {
	caption = normalizeCaption(caption)
	if strings.TrimSpace(caption) == "" {
		return ""
	}
	var parts []string
	for _, line := range strings.Split(caption, "\n") {
		if strings.TrimSpace(line) == "" {
			parts = append(parts, "<br/>")
			continue
		}
		var b strings.Builder
		i := 0
		for i < len(line) {
			if line[i] == '[' {
				if n, label, url, ok := parseMarkdownLink(line[i:]); ok {
					b.WriteString(`<a href="`)
					b.WriteString(html.EscapeString(url))
					b.WriteString(`">`)
					b.WriteString(html.EscapeString(label))
					b.WriteString(`</a>`)
					i += n
					continue
				}
			}
			r, size := utf8.DecodeRuneInString(line[i:])
			b.WriteString(html.EscapeString(string(r)))
			i += size
		}
		parts = append(parts, "<p>"+b.String()+"</p>")
	}
	return strings.Join(parts, "")
}

func createPhotoPostState(client *http.Client, auth Auth, imagePath, caption, state string) ([]byte, string, error) {
	file, err := os.Open(imagePath)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()

	video := isVideoPath(imagePath)
	mimeType := "image/jpeg"
	ident := "img0"
	width, height := 0, 0
	if video {
		ident = "media-0"
		mimeType = "video/mp4"
		width, height = videoDimensions(imagePath)
		if width <= 0 || height <= 0 {
			width, height = 720, 1280
		}
	} else {
		cfg, format, err := image.DecodeConfig(file)
		if err != nil {
			return nil, "", fmt.Errorf("decode image: %w", err)
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return nil, "", err
		}
		mimeType = mimeFromFormat(format, imagePath)
		width, height = cfg.Width, cfg.Height
	}

	var content []map[string]any
	var layoutDisplay []map[string]any
	block := 0
	// Image/video first so the post reads like the tumblr UI (media on top).
	if video {
		media := map[string]any{
			"type":       mimeType,
			"identifier": ident,
		}
		if width > 0 {
			media["width"] = width
		}
		if height > 0 {
			media["height"] = height
		}
		content = append(content, map[string]any{
			"type":     "video",
			"provider": "tumblr",
			"media":    media,
		})
	} else {
		content = append(content, map[string]any{
			"type": "image",
			"media": []map[string]any{
				{
					"type":       mimeType,
					"identifier": ident,
					"width":      width,
					"height":     height,
				},
			},
		})
	}
	layoutDisplay = append(layoutDisplay, map[string]any{"blocks": []int{block}})
	block++
	for _, blk := range npfCaptionBlocks(caption) {
		content = append(content, blk)
		layoutDisplay = append(layoutDisplay, map[string]any{"blocks": []int{block}})
		block++
	}

	payload := map[string]any{
		"state":                      state,
		"content":                    content,
		"hide_trail":                 false,
		"tags":                       "",
		"layout":                     []map[string]any{{"type": "rows", "display": layoutDisplay}},
		"has_community_label":        false,
		"community_label_categories": []any{},
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.SetBoundary("TumblrBoundary")

	jsonHeader := textproto.MIMEHeader{}
	jsonHeader.Set("Content-Disposition", `form-data; name="json"`)
	jsonHeader.Set("Content-Type", "application/json")
	jp, err := w.CreatePart(jsonHeader)
	if err != nil {
		return nil, "", err
	}
	if _, err := jp.Write(jsonBytes); err != nil {
		return nil, "", err
	}

	imgHeader := textproto.MIMEHeader{}
	imgHeader.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name="%s"; filename="%s"`, ident, filepath.Base(imagePath)))
	imgHeader.Set("Content-Type", mimeType)
	ip, err := w.CreatePart(imgHeader)
	if err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(ip, file); err != nil {
		return nil, "", err
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}

	// NPF always uses /posts — /post is legacy and treats NPF video as empty.
	postURL := "https://www.tumblr.com/api/v2/blog/" + auth.Blog + "/posts"
	req, err := http.NewRequest("POST", postURL, bytes.NewReader(body.Bytes()))
	if err != nil {
		return nil, "", err
	}
	setCommonHeaders(req, auth)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-CSRF", auth.CSRF)
	if video {
		req.Header.Set("Referer", "https://www.tumblr.com/new/video")
	} else {
		req.Header.Set("Referer", "https://www.tumblr.com/new/photo")
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.Status, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return respBody, resp.Status, fmt.Errorf("photo post failed: %s — %s", resp.Status, string(respBody))
	}
	return respBody, resp.Status, nil
}

func mimeFromFormat(format, path string) string {
	switch strings.ToLower(format) {
	case "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

func nextPic() (string, error) {
	entries, err := os.ReadDir(picsDir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		switch ext {
		case ".jpg", ".jpeg", ".png", ".gif", ".webp":
			return filepath.Join(picsDir, name), nil
		}
	}
	return "", fmt.Errorf("no images left in %s/", picsDir)
}

func markPicPosted(path string) error {
	if err := os.MkdirAll(picsPosted, 0755); err != nil {
		return err
	}
	dest := filepath.Join(picsPosted, filepath.Base(path))
	return os.Rename(path, dest)
}

func picsRemaining() bool {
	_, err := nextPic()
	return err == nil
}

func captionForPic(imagePath string) string {
	base := strings.TrimSuffix(imagePath, filepath.Ext(imagePath))
	for _, ext := range []string{".txt", ".caption"} {
		data, err := os.ReadFile(base + ext)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return ""
}

var stdinReader = bufio.NewReader(os.Stdin)

func readStdinLine() (string, error) {
	line, err := stdinReader.ReadString('\n')
	line = strings.TrimRight(line, "\r\n")
	if err == io.EOF && line != "" {
		return line, nil
	}
	if err == io.EOF {
		return line, nil
	}
	return line, err
}

func listPostedPics() ([]string, error) {
	entries, err := os.ReadDir(picsPosted)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", picsPosted, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		switch ext {
		case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".mp4", ".m4v", ".webm", ".mov":
			out = append(out, filepath.Join(picsPosted, e.Name()))
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no images/videos in %s/ — put a jpg/png/gif/webp/mp4 there first", picsPosted)
	}
	return out, nil
}

func pickPostedPic() (string, error) {
	pics, err := listPostedPics()
	if err != nil {
		return "", err
	}
	fmt.Println("Pictures in", picsPosted+"/")
	for i, p := range pics {
		fmt.Printf("  %d) %s\n", i+1, filepath.Base(p))
	}
	for {
		fmt.Print("Pick a picture number: ")
		line, err := readStdinLine()
		if err != nil {
			return "", err
		}
		n, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil || n < 1 || n > len(pics) {
			fmt.Printf("Enter a number from 1 to %d.\n", len(pics))
			continue
		}
		return pics[n-1], nil
	}
}

func readCaptionFromConsole() (string, error) {
	fmt.Print("Caption (Enter for none): ")
	line, err := readStdinLine()
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(line, `\n`, "\n"), nil
}

func runInteractivePhotoPost() error {
	path, err := pickPostedPic()
	if err != nil {
		return err
	}
	caption, err := readCaptionFromConsole()
	if err != nil {
		return err
	}
	return publishMediaOnUniqueBlogs(path, caption)
}

func publishMediaOnUniqueBlogs(path, caption string) error {
	return publishMediaOnUniqueBlogsPinned(path, caption, false)
}

func publishMediaOnUniqueBlogsPinned(path, caption string, pin bool) error {
	fmt.Println("Posting", filepath.ToSlash(path), "on every distinct Tumblr blog…")
	if pin {
		fmt.Println("Pinning each post after publish")
	}
	if caption != "" {
		fmt.Println("Caption:", truncate(caption, 120))
	}

	seenBlog := map[string]string{}
	return runForEachUniqueBlog("photo post", func() error {
		return postPhotoOnAllBlogsPinned(path, caption, seenBlog, pin)
	})
}

func publishMediaOnFirstLoggedInBlogs(path, caption string, n int) error {
	return publishMediaOnFirstLoggedInBlogsPinned(path, caption, n, false)
}

func publishMediaOnFirstLoggedInBlogsPinned(path, caption string, n int, pin bool) error {
	accs := loggedInUniqueAccounts(n)
	fmt.Printf("Posting %s on the first %d logged-in blogs (%d found)\n", filepath.ToSlash(path), n, len(accs))
	if pin {
		fmt.Println("Pinning each post after publish")
	}
	if caption != "" {
		fmt.Println("Caption:", truncate(caption, 120))
	}
	for i, a := range accs {
		fmt.Printf("  %d) %s\n", i+1, a.ID)
	}
	seenBlog := map[string]string{}
	return runForEachAccountList("photo post", accs, func() error {
		return postPhotoOnAllBlogsPinned(path, caption, seenBlog, pin)
	})
}

// publishMediaOnTopFollowerBlogs posts (and optionally pins) on the N logged-in
// accounts with the most followers (unique blogs).
func publishMediaOnTopFollowerBlogs(path, caption string, n int, pin bool) error {
	accs := loggedInTopFollowerAccounts(n)
	fmt.Printf("Posting %s on top %d follower blogs (%d found)\n", filepath.ToSlash(path), n, len(accs))
	if pin {
		fmt.Println("Pinning each post after publish")
	}
	if caption != "" {
		fmt.Println("Caption:", truncate(caption, 160))
	}
	for i, a := range accs {
		auth := peekAccountAuth(a.ID)
		blog, fol := bestAccountBlogStats(a.ID, loadFollowerLog(a.ID))
		if blog == "" {
			blog = auth.Blog
		}
		fmt.Printf("  %d) %s  @%s  %d followers\n", i+1, a.ID, blog, fol)
	}
	seenBlog := map[string]string{}
	return runForEachAccountList("photo post", accs, func() error {
		return postPhotoOnAllBlogsPinned(path, caption, seenBlog, pin)
	})
}

// publishMediaOnFullySetupBlogs posts (and optionally pins) on every account in
// config/zoey-fully-setup.json that is still logged in.
func publishMediaOnFullySetupBlogs(path, caption string, pin bool) error {
	accs := loggedInFullySetupAccounts()
	fmt.Printf("Posting %s on %d fully-setup blogs\n", filepath.ToSlash(path), len(accs))
	if pin {
		fmt.Println("Pinning each post after publish")
	}
	if caption != "" {
		fmt.Println("Caption:", truncate(caption, 160))
	}
	for i, a := range accs {
		auth := peekAccountAuth(a.ID)
		blog, fol := bestAccountBlogStats(a.ID, loadFollowerLog(a.ID))
		if blog == "" {
			blog = auth.Blog
		}
		fmt.Printf("  %d) %s  @%s  %d followers\n", i+1, a.ID, blog, fol)
	}
	if len(accs) == 0 {
		return fmt.Errorf("no fully-setup logged-in accounts")
	}
	seenBlog := map[string]string{}
	return runForEachAccountList("photo post", accs, func() error {
		return postPhotoOnAllBlogsPinned(path, caption, seenBlog, pin)
	})
}

func postPhotoOnAllBlogs(imagePath, caption string, seenBlog map[string]string) error {
	return postPhotoOnAllBlogsPinned(imagePath, caption, seenBlog, false)
}

func postPhotoOnAllBlogsPinned(imagePath, caption string, seenBlog map[string]string, pin bool) error {
	auth, err := loadAuth()
	if err != nil {
		return err
	}
	if !authHasSID(auth) {
		return fmt.Errorf("not logged in")
	}
	acc, _ := currentAccount()
	client := newAuthClient(&auth)
	if isVideoPath(imagePath) {
		client.Timeout = 3 * time.Minute
	}
	if err := ensureSession(client, &auth); err != nil {
		return err
	}
	if _, err := syncAuthIdentity(client, &auth); isForeignBlogSession(err) {
		return err
	}

	blogs, err := listAccountBlogsDetailed(client, auth)
	if err != nil {
		return err
	}

	var last error
	posted := 0
	primaries := 0
	for _, b := range blogs {
		if b.Primary {
			primaries++
		}
	}
	for _, b := range blogs {
		if primaries > 0 && !b.Primary {
			continue
		}
		key := strings.ToLower(b.Name)
		if key == "" {
			key = strings.ToLower(b.UUID)
		}
		if prev, ok := seenBlog[key]; ok {
			fmt.Printf("Skip @%s — already posted via %s\n", b.Name, prev)
			continue
		}
		label := "@" + b.Name
		if b.Primary {
			label += " (primary)"
		}
		fmt.Println("Ensuring pinned post on", label)
		blogID := b.Name
		if blogID == "" {
			blogID = b.UUID
		}
		err := ensureMediaPinnedOnBlog(client, &auth, blogID, imagePath, caption, pin)
		if isCookieProblem(err) {
			err = retryAfterCookie(client, &auth, nil, acc.ID, err, func() error {
				return ensureMediaPinnedOnBlog(client, &auth, blogID, imagePath, caption, pin)
			})
		}
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			fmt.Println("  already posted — skipping")
			seenBlog[key] = acc.ID
			continue
		}
		if err != nil {
			fmt.Println("  fail:", err)
			last = err
			continue
		}
		seenBlog[key] = acc.ID
		posted++
		time.Sleep(8 * time.Second)
	}
	if posted == 0 && last != nil {
		return last
	}
	return nil
}
