package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
)

const (
	blazingothPostsFile  = "blazingoth-text-posts-postable.txt"
	blazingothCursorFile = "data/blazingoth-cursor.json"
)

type blazingothCursor struct {
	Next   int            `json:"next"`
	Posted map[string]int `json:"posted"`
}

var (
	reLostEmoji     = regexp.MustCompile(`\s*\?{2,}`)
	reSlashBreak    = regexp.MustCompile(`(?:\s+/)+\s+`)
	reManySpaces    = regexp.MustCompile(`[ \t]{2,}`)
	reExtraNewlines = regexp.MustCompile(`\n{3,}`)
)

// cleanBlazingothText turns the scraped line back into post text: " / " was a
// line break in the source, and runs of "??" are emoji lost in the export.
func cleanBlazingothText(s string) string {
	s = reLostEmoji.ReplaceAllString(s, "")
	s = reSlashBreak.ReplaceAllStringFunc(s, func(m string) string {
		return strings.Repeat("\n", strings.Count(m, "/"))
	})
	s = reManySpaces.ReplaceAllString(s, " ")
	s = reExtraNewlines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func loadBlazingothPosts() ([]string, error) {
	raw, err := os.ReadFile(blazingothPostsFile)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		out = append(out, cleanBlazingothText(line))
	}
	return out, nil
}

func loadBlazingothCursor() blazingothCursor {
	c := blazingothCursor{Posted: map[string]int{}}
	if raw, err := os.ReadFile(blazingothCursorFile); err == nil {
		_ = json.Unmarshal(raw, &c)
	}
	if c.Posted == nil {
		c.Posted = map[string]int{}
	}
	return c
}

func saveBlazingothCursor(c blazingothCursor) {
	raw, err := json.MarshalIndent(c, "", "  ")
	if err == nil {
		_ = os.WriteFile(blazingothCursorFile, append(raw, '\n'), 0644)
	}
}

// nextBlazingothPost returns the next line (0-based index) that hasn't been
// posted yet, in file order.
func nextBlazingothPost(posts []string, c blazingothCursor) (int, string, bool) {
	for i := c.Next; i < len(posts); i++ {
		text := posts[i]
		if text == "" {
			continue
		}
		if _, done := c.Posted[strings.ToLower(text)]; done {
			continue
		}
		return i, text, true
	}
	return 0, "", false
}

func runBlazingothNext(n int) {
	posts, err := loadBlazingothPosts()
	if err != nil {
		fmt.Println("Blazingoth:", err)
		return
	}
	c := loadBlazingothCursor()
	if len(c.Posted) == 0 {
		c.Posted[strings.ToLower(cleanBlazingothText("i love being thick and having massive thighs"))] = 36
	}
	i, text, ok := nextBlazingothPost(posts, c)
	if !ok {
		fmt.Println("Blazingoth: every post in", blazingothPostsFile, "has been used")
		return
	}
	fmt.Printf("Blazingoth post %d/%d\n", i+1, len(posts))
	if err := publishTextOnTopFollowerBlogs(text, n); err != nil {
		fmt.Println("Text:", err)
		fmt.Println("Not moving on to the next blazingoth post — run again to retry (blogs that already posted it are skipped).")
		return
	}
	c.Posted[strings.ToLower(text)] = i
	c.Next = i + 1
	saveBlazingothCursor(c)
	fmt.Printf("Done — next run posts blazingoth line %d\n", i+2)
}
