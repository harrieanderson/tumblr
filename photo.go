package main

import (
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
	"strings"
)

const (
	picsDir    = "pics"
	picsPosted = "pics/posted"
)

func createPhotoPost(client *http.Client, auth Auth, imagePath, caption string) error {
	file, err := os.Open(imagePath)
	if err != nil {
		return err
	}
	defer file.Close()

	cfg, format, err := image.DecodeConfig(file)
	if err != nil {
		return fmt.Errorf("decode image: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}

	mimeType := mimeFromFormat(format, imagePath)
	ident := "img0"

	var content []map[string]any
	var layoutDisplay []map[string]any
	block := 0
	if strings.TrimSpace(caption) != "" {
		content = append(content, map[string]any{"type": "text", "text": caption})
		layoutDisplay = append(layoutDisplay, map[string]any{"blocks": []int{block}})
		block++
	}
	content = append(content, map[string]any{
		"type": "image",
		"media": []map[string]any{
			{
				"type":       mimeType,
				"identifier": ident,
				"width":      cfg.Width,
				"height":     cfg.Height,
			},
		},
	})
	layoutDisplay = append(layoutDisplay, map[string]any{"blocks": []int{block}})

	payload := map[string]any{
		"state":      "published",
		"hide_trail": false,
		"tags":       "",
		"content":    content,
		"layout": []map[string]any{
			{"type": "rows", "display": layoutDisplay},
		},
		"has_community_label":        false,
		"community_label_categories": []any{},
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.SetBoundary("TumblrBoundary")

	jsonHeader := textproto.MIMEHeader{}
	jsonHeader.Set("Content-Disposition", `form-data; name="json"`)
	jsonHeader.Set("Content-Type", "application/json")
	jp, err := w.CreatePart(jsonHeader)
	if err != nil {
		return err
	}
	if _, err := jp.Write(jsonBytes); err != nil {
		return err
	}

	imgHeader := textproto.MIMEHeader{}
	imgHeader.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name="%s"; filename="%s"`, ident, filepath.Base(imagePath)))
	imgHeader.Set("Content-Type", mimeType)
	ip, err := w.CreatePart(imgHeader)
	if err != nil {
		return err
	}
	if _, err := io.Copy(ip, file); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	url := "https://www.tumblr.com/api/v2/blog/" + auth.Blog + "/posts"
	req, err := http.NewRequest("POST", url, &body)
	if err != nil {
		return err
	}
	setCommonHeaders(req, auth)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/new/photo")
	req.Header.Set("sec-ch-ua", `"Chromium";v="152", "Not?A_Brand";v="24", "Google Chrome";v="152"`)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"Windows"`)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	fmt.Println("Status:", resp.Status)
	fmt.Println(string(respBody))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("photo post failed: %s — %s", resp.Status, string(respBody))
	}
	return nil
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
