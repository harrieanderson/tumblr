package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type MessageConfig struct {
	Enabled            bool     `json:"enabled"`
	MaxReplies         int      `json:"maxReplies"`
	DelaySeconds       int      `json:"delaySeconds"`
	DelayJitterSeconds int      `json:"delayJitterSeconds"`
	OnlyUnread         bool     `json:"onlyUnread"`
	ReplyTemplates     []string `json:"replyTemplates"`
	ColdOpenEnabled    bool     `json:"coldOpenEnabled"`
	ColdOpenMax        int      `json:"coldOpenMax"`
	ColdOpenTemplates  []string `json:"coldOpenTemplates"`
	AI                 AIConfig `json:"ai"`
}

const messageConfigFile = "config/messages.json"

func defaultMessageConfig() MessageConfig {
	return MessageConfig{
		Enabled:            true,
		MaxReplies:         5,
		DelaySeconds:       45,
		DelayJitterSeconds: 40,
		OnlyUnread:         true,
		ReplyTemplates: []string{
			"hey! how's your day going?",
			"hi :) saw your blog, love the vibe",
			"heyy what's up",
			"hi! hope you're having a good one",
		},
		ColdOpenEnabled: false,
		ColdOpenMax:     2,
		ColdOpenTemplates: []string{
			"hey! liked your posts — how are you?",
			"hi :) your blog's cute, what's been good lately?",
		},
		AI: defaultAIConfig(),
	}
}

func loadMessageConfig() MessageConfig {
	cfg := defaultMessageConfig()
	if raw, err := os.ReadFile(messageConfigFile); err == nil {
		_ = json.Unmarshal(raw, &cfg)
	}
	if cfg.MaxReplies <= 0 {
		cfg.MaxReplies = 5
	}
	if cfg.DelaySeconds <= 0 {
		cfg.DelaySeconds = 45
	}
	if len(cfg.ReplyTemplates) == 0 {
		cfg.ReplyTemplates = defaultMessageConfig().ReplyTemplates
	}
	cfg.AI.normalize()
	return cfg
}

type Conversation struct {
	ID                  string
	CanSend             bool
	Unread              int
	OtherName           string
	OtherUUID           string
	LastMessageText     string
	LastMessageFromSelf bool
	Messages            []ConvMessage
}

type ConvMessage struct {
	FromSelf bool
	FromName string
	Text     string
	TS       string
}

type conversationAPI struct {
	ID                  string `json:"id"`
	CanSend             bool   `json:"canSend"`
	UnreadMessagesCount int    `json:"unreadMessagesCount"`
	Participants        []struct {
		Name  string `json:"name"`
		UUID  string `json:"uuid"`
		Admin bool   `json:"admin"`
	} `json:"participants"`
	Messages struct {
		Data []struct {
			Type        string `json:"type"`
			TS          string `json:"ts"`
			Participant string `json:"participant"`
			Message     string `json:"message"`
			Content     struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"data"`
	} `json:"messages"`
}

func runMessages() {
	auth, err := loadAuth()
	if err != nil {
		fmt.Println("Error loading auth:", err)
		return
	}
	if err := syncCookiesFromChrome(&auth); err != nil {
		if !authHasSID(auth) {
			fmt.Println("Cookie sync failed — trying auto-login…")
			if err := loginWithRealChrome(&auth); err != nil {
				fmt.Println("Login failed:", err)
				return
			}
		}
	}
	client := &http.Client{Timeout: 45 * time.Second}
	if err := ensureSession(client, &auth); err != nil {
		fmt.Println("Session dead:", err)
		return
	}
	if err := printInbox(client, auth); err != nil {
		fmt.Println("Inbox error:", err)
	}
}

func runMessagesReply() {
	auth, err := loadAuth()
	if err != nil {
		fmt.Println("Error loading auth:", err)
		return
	}
	if err := syncCookiesFromChrome(&auth); err != nil {
		if !authHasSID(auth) {
			fmt.Println("Cookie sync failed — trying auto-login…")
			if err := loginWithRealChrome(&auth); err != nil {
				fmt.Println("Login failed:", err)
				return
			}
		}
	}
	client := &http.Client{Timeout: 45 * time.Second}
	if err := ensureSession(client, &auth); err != nil {
		fmt.Println("Session dead:", err)
		return
	}
	cfg := loadMessageConfig()
	if cfg.AI.Enabled {
		fmt.Printf("AI replies on (%s / %s)\n", cfg.AI.Provider, cfg.AI.Model)
	} else {
		fmt.Println("AI off — using reply templates")
	}
	n, err := messageInboxBurst(client, &auth, cfg)
	if err != nil {
		fmt.Println("Messages error:", err)
		return
	}
	fmt.Printf("Done — sent %d replies\n", n)
}

func printInbox(client *http.Client, auth Auth) error {
	convs, err := listConversations(client, auth, 50)
	if err != nil {
		return err
	}
	if len(convs) == 0 {
		fmt.Println("Inbox is empty.")
		return nil
	}

	fmt.Printf("Messages inbox (%d conversations)\n", len(convs))
	fmt.Println(strings.Repeat("=", 48))

	for i, c := range convs {
		// Prefer fuller history when available.
		msgs, err := fetchConversationMessages(client, auth, c.ID, 40)
		if err == nil && len(msgs) > 0 {
			c.Messages = msgs
		}

		unread := ""
		if c.Unread > 0 {
			unread = fmt.Sprintf("  [unread:%d]", c.Unread)
		}
		who := c.OtherName
		if who == "" {
			who = "(unknown)"
		}
		fmt.Printf("\n%d. chat with %s%s\n", i+1, who, unread)
		fmt.Println("   " + strings.Repeat("-", 40))
		if len(c.Messages) == 0 {
			if c.LastMessageText != "" {
				whoLast := who
				if c.LastMessageFromSelf {
					whoLast = "you"
				}
				fmt.Printf("   %s: %s\n", whoLast, c.LastMessageText)
			} else {
				fmt.Println("   (no messages)")
			}
			continue
		}
		// API often returns newest-first; print oldest→newest for reading.
		for a, b := 0, len(c.Messages)-1; a < b; a, b = a+1, b-1 {
			c.Messages[a], c.Messages[b] = c.Messages[b], c.Messages[a]
		}
		for _, m := range c.Messages {
			label := m.FromName
			if label == "" {
				if m.FromSelf {
					label = "you"
				} else {
					label = who
				}
			}
			text := strings.TrimSpace(m.Text)
			if text == "" {
				text = "(non-text message)"
			}
			fmt.Printf("   %s: %s\n", label, text)
		}
	}
	fmt.Println()
	return nil
}

func fetchConversationMessages(client *http.Client, auth Auth, conversationID string, limit int) ([]ConvMessage, error) {
	if limit <= 0 {
		limit = 30
	}
	u, _ := url.Parse("https://www.tumblr.com/api/v2/conversations/messages")
	q := u.Query()
	q.Set("conversation_id", conversationID)
	q.Set("participant", auth.Blog)
	q.Set("limit", fmt.Sprintf("%d", limit))
	q.Set("preserve_last_read_ts", "true")
	q.Set("fields[blogs]", "?avatar,name,?uuid,url")
	u.RawQuery = q.Encode()

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	setCommonHeaders(req, auth)
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/messaging")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", resp.Status, truncate(string(body), 160))
	}

	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	response, _ := raw["response"].(map[string]any)
	if response == nil {
		return nil, fmt.Errorf("missing response")
	}

	// Response shapes vary: messages.data, or conversation.messages.data, or top-level messages array.
	var data []any
	if msgs, ok := response["messages"].(map[string]any); ok {
		data, _ = msgs["data"].([]any)
	}
	if len(data) == 0 {
		if conv, ok := response["conversation"].(map[string]any); ok {
			if msgs, ok := conv["messages"].(map[string]any); ok {
				data, _ = msgs["data"].([]any)
			}
		}
	}
	if len(data) == 0 {
		data, _ = response["messages"].([]any)
	}

	nameByID := map[string]string{strings.ToLower(auth.Blog): "you"}
	if participants, ok := response["participants"].([]any); ok {
		for _, p := range participants {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			name := anyToString(pm["name"])
			uuid := anyToString(pm["uuid"])
			if name != "" && uuid != "" {
				nameByID[strings.ToLower(uuid)] = name
			}
		}
	}

	out := make([]ConvMessage, 0, len(data))
	for _, el := range data {
		m, ok := el.(map[string]any)
		if !ok {
			continue
		}
		part := anyToString(m["participant"])
		text := anyToString(m["message"])
		if text == "" {
			if content, ok := m["content"].(map[string]any); ok {
				text = anyToString(content["text"])
			}
		}
		fromSelf := strings.EqualFold(part, auth.Blog)
		fromName := nameByID[strings.ToLower(part)]
		if fromName == "" {
			if fromSelf {
				fromName = "you"
			} else {
				fromName = part
			}
		}
		out = append(out, ConvMessage{
			FromSelf: fromSelf,
			FromName: fromName,
			Text:     text,
			TS:       anyToString(m["ts"]),
		})
	}
	return out, nil
}

func messageInboxBurst(client *http.Client, auth *Auth, cfg MessageConfig) (int, error) {
	if !cfg.Enabled {
		return 0, nil
	}
	convs, err := listConversations(client, *auth, 30)
	if err != nil {
		return 0, err
	}
	fmt.Printf("Inbox: %d conversations\n", len(convs))

	replied := 0
	for _, c := range convs {
		if replied >= cfg.MaxReplies {
			break
		}
		if !c.CanSend {
			continue
		}
		if cfg.OnlyUnread && c.Unread <= 0 {
			continue
		}
		// Don't double-tap if we already sent the last message (unless unread somehow).
		if c.LastMessageFromSelf && c.Unread <= 0 {
			continue
		}

		history := c.Messages
		if msgs, err := fetchConversationMessages(client, *auth, c.ID, 40); err == nil && len(msgs) > 0 {
			history = msgs
			// Oldest → newest for the model.
			for a, b := 0, len(history)-1; a < b; a, b = a+1, b-1 {
				history[a], history[b] = history[b], history[a]
			}
		}
		text := pickReplyText(cfg, c.OtherName, history)
		src := "template"
		if cfg.AI.Enabled {
			src = cfg.AI.Provider
		}
		fmt.Printf("  reply [%s] → %s: %s\n", src, c.OtherName, truncate(text, 60))
		if err := sendConversationText(client, *auth, c.ID, text); err != nil {
			fmt.Println("   send fail:", err)
			if isUnauthorized(err) {
				if err := ensureSession(client, auth); err != nil {
					return replied, err
				}
				if err := sendConversationText(client, *auth, c.ID, text); err != nil {
					fmt.Println("   retry fail:", err)
					continue
				}
			} else {
				continue
			}
		}
		replied++
		wait := cfg.DelaySeconds
		if cfg.DelayJitterSeconds > 0 {
			wait += rand.Intn(cfg.DelayJitterSeconds + 1)
		}
		fmt.Printf("  waiting %ds…\n", wait)
		time.Sleep(time.Duration(wait) * time.Second)
	}

	if cfg.ColdOpenEnabled && replied < cfg.MaxReplies {
		left := cfg.ColdOpenMax
		if left > cfg.MaxReplies-replied {
			left = cfg.MaxReplies - replied
		}
		n, err := coldOpenMessages(client, auth, cfg, left)
		replied += n
		if err != nil {
			return replied, err
		}
	}
	return replied, nil
}

func coldOpenMessages(client *http.Client, auth *Auth, cfg MessageConfig, max int) (int, error) {
	if max <= 0 || len(cfg.ColdOpenTemplates) == 0 {
		return 0, nil
	}
	already := loadStringSet("data/messaged.json")
	followed, _ := loadFollowed()
	var candidates []string
	for blog := range followed {
		if already[strings.ToLower(blog)] {
			continue
		}
		candidates = append(candidates, blog)
	}
	rand.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })

	sent := 0
	for _, blog := range candidates {
		if sent >= max {
			break
		}
		text := cfg.ColdOpenTemplates[rand.Intn(len(cfg.ColdOpenTemplates))]
		fmt.Printf("  cold DM → %s: %s\n", blog, truncate(text, 60))
		if err := startConversationText(client, *auth, blog, text); err != nil {
			fmt.Println("   cold fail:", err)
			continue
		}
		already[strings.ToLower(blog)] = true
		_ = saveStringSet("data/messaged.json", already)
		sent++
		wait := cfg.DelaySeconds + rand.Intn(cfg.DelayJitterSeconds+1)
		time.Sleep(time.Duration(wait) * time.Second)
	}
	return sent, nil
}

func listConversations(client *http.Client, auth Auth, limit int) ([]Conversation, error) {
	convs, err := listConversationsOnce(client, auth, limit)
	if err == nil {
		return convs, nil
	}
	if !isForbidden(err) && !isUnauthorized(err) {
		return nil, err
	}
	// Soft 403/401 on DMs is often a stale CSRF — refresh and retry once.
	if err2 := refreshCSRF(client, &auth); err2 == nil {
		convs, err2 = listConversationsOnce(client, auth, limit)
		if err2 == nil {
			return convs, nil
		}
		err = err2
	}
	return nil, err
}

func listConversationsOnce(client *http.Client, auth Auth, limit int) ([]Conversation, error) {
	if limit <= 0 {
		limit = 20
	}
	u, _ := url.Parse("https://www.tumblr.com/api/v2/conversations")
	q := u.Query()
	q.Set("limit", fmt.Sprintf("%d", limit))
	q.Set("participant", auth.Blog)
	q.Set("fields[blogs]", "?avatar,name,?seconds_since_last_activity,url,?blog_view_url,?uuid,?theme,?description_npf,?is_adult,?primary")
	u.RawQuery = q.Encode()

	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	setCommonHeaders(req, auth)
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/messaging")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", resp.Status, truncate(string(body), 180))
	}

	var raw struct {
		Response struct {
			Conversations []conversationAPI `json:"conversations"`
		} `json:"response"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}

	out := make([]Conversation, 0, len(raw.Response.Conversations))
	for _, c := range raw.Response.Conversations {
		conv := Conversation{
			ID:      c.ID,
			CanSend: c.CanSend,
			Unread:  c.UnreadMessagesCount,
		}
		for _, p := range c.Participants {
			if strings.EqualFold(p.UUID, auth.Blog) {
				continue
			}
			if p.Name != "" {
				conv.OtherName = p.Name
				conv.OtherUUID = p.UUID
				break
			}
		}
		if len(c.Messages.Data) > 0 {
			m := c.Messages.Data[0]
			conv.LastMessageText = m.Message
			if conv.LastMessageText == "" {
				conv.LastMessageText = m.Content.Text
			}
			conv.LastMessageFromSelf = strings.EqualFold(m.Participant, auth.Blog)
			for _, msg := range c.Messages.Data {
				text := msg.Message
				if text == "" {
					text = msg.Content.Text
				}
				fromSelf := strings.EqualFold(msg.Participant, auth.Blog)
				fromName := conv.OtherName
				if fromSelf {
					fromName = "you"
				}
				conv.Messages = append(conv.Messages, ConvMessage{
					FromSelf: fromSelf,
					FromName: fromName,
					Text:     text,
					TS:       msg.TS,
				})
			}
		}
		out = append(out, conv)
	}
	return out, nil
}

func isForbidden(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "403") || strings.Contains(strings.ToLower(s), "forbidden")
}

func sendConversationText(client *http.Client, auth Auth, conversationID, text string) error {
	payload := map[string]any{
		"conversation_id": conversationID,
		"type":            "TEXT",
		"participant":     auth.Blog,
		"message":         text,
	}
	return postConversationMessage(client, auth, payload)
}

func startConversationText(client *http.Client, auth Auth, otherBlog, text string) error {
	other := strings.TrimSpace(otherBlog)
	other = strings.TrimSuffix(other, ".tumblr.com")
	payload := map[string]any{
		"participants": []string{auth.Blog, other},
		"type":         "TEXT",
		"participant":  auth.Blog,
		"message":      text,
	}
	return postConversationMessage(client, auth, payload)
}

func postConversationMessage(client *http.Client, auth Auth, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	u := "https://www.tumblr.com/api/v2/conversations/messages?fields[blogs]=?avatar,name,?uuid,url"
	req, err := http.NewRequest("POST", u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	setCommonHeaders(req, auth)
	req.Header.Set("Content-Type", "application/json; charset=utf8")
	req.Header.Set("X-CSRF", auth.CSRF)
	req.Header.Set("Referer", "https://www.tumblr.com/messaging")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", resp.Status, truncate(string(respBody), 200))
	}
	return nil
}
