package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
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
)

const (
	picsDir    = "pics"
	picsPosted = "pics/posted"
)

func createPhotoPost(client *http.Client, auth Auth, imagePath, caption string) error {
	return createPhotoPostOn(client, auth, auth.Blog, imagePath, caption)
}

func createPhotoPostOn(client *http.Client, auth Auth, blogID, imagePath, caption string) error {
	auth.Blog = blogID
	var body []byte
	var status string
	var err error
	if isVideoPath(imagePath) {
		body, status, err = createVideoPostLegacy(client, auth, imagePath, caption, "published")
	} else {
		body, status, err = createPhotoPostState(client, auth, imagePath, caption, "published")
	}
	fmt.Println("Status:", status)
	if err != nil {
		fmt.Println(truncate(string(body), 200))
	}
	return err
}

func createVideoPostLegacy(client *http.Client, auth Auth, imagePath, caption, state string) ([]byte, string, error) {
	file, err := os.Open(imagePath)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("type", "video")
	_ = w.WriteField("state", state)
	if strings.TrimSpace(caption) != "" {
		_ = w.WriteField("caption", caption)
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
	req.Header.Set("Referer", "https://www.tumblr.com/new/video")

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
		return respBody, resp.Status, fmt.Errorf("video post failed: %s — %s", resp.Status, string(respBody))
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
	if strings.TrimSpace(caption) != "" {
		content = append(content, map[string]any{"type": "text", "text": caption})
		layoutDisplay = append(layoutDisplay, map[string]any{"blocks": []int{block}})
		block++
	}
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

	payload := map[string]any{
		"state":   state,
		"content": content,
	}
	if !video {
		payload["hide_trail"] = false
		payload["tags"] = ""
		payload["layout"] = []map[string]any{
			{"type": "rows", "display": layoutDisplay},
		}
		payload["has_community_label"] = false
		payload["community_label_categories"] = []any{}
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, "", err
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if !video {
		_ = w.SetBoundary("TumblrBoundary")
	}

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

	postURL := "https://www.tumblr.com/api/v2/blog/" + auth.Blog + "/posts"
	if video {
		postURL = "https://www.tumblr.com/api/v2/blog/" + auth.Blog + "/post"
	}
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
	fmt.Println("Posting", filepath.ToSlash(path), "on every distinct Tumblr blog…")
	if caption != "" {
		fmt.Println("Caption:", truncate(caption, 120))
	}

	seenBlog := map[string]string{}
	return runForEachUniqueBlog("photo post", func() error {
		return postPhotoOnAllBlogs(path, caption, seenBlog)
	})
}

func postPhotoOnAllBlogs(imagePath, caption string, seenBlog map[string]string) error {
	auth, err := loadAuth()
	if err != nil {
		return err
	}
	if !authHasSID(auth) {
		return fmt.Errorf("not logged in")
	}
	acc, _ := currentAccount()
	client := newHumanClient()
	if isVideoPath(imagePath) {
		client.Timeout = 3 * time.Minute
	}
	if err := ensureSession(client, &auth); err != nil {
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
		fmt.Println("Posting to", label)
		blogID := b.Name
		if blogID == "" {
			blogID = b.UUID
		}
		err := createPhotoPostOn(client, auth, blogID, imagePath, caption)
		if isCookieProblem(err) {
			err = retryAfterCookie(client, &auth, nil, acc.ID, err, func() error {
				return createPhotoPostOn(client, auth, blogID, imagePath, caption)
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
