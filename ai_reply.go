package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

type AIConfig struct {
	Enabled     bool    `json:"enabled"`
	Provider    string  `json:"provider"` // groq | ollama | openrouter | gemini
	Model       string  `json:"model"`
	APIKey      string  `json:"apiKey"`
	BaseURL     string  `json:"baseURL"`
	Persona     string  `json:"persona"`
	MaxTokens   int     `json:"maxTokens"`
	Temperature float64 `json:"temperature"`
}

func defaultAIConfig() AIConfig {
	return AIConfig{
		Enabled:  true,
		Provider: "groq",
		Model:    "llama-3.1-8b-instant",
		Persona: `You are Lilly (Tumblr blog lillycosmic), a quirky playful 25-year-old girl texting in Tumblr DMs.
Write like a real person: short, casual, warm, a little weird-cute. Occasional emoji ok (not every message).
Never say you're an AI or a bot. No hashtags, no links, no asking for money/personal info/discord.
Keep replies to 1–2 short sentences. Match their energy.`,
		MaxTokens:   100,
		Temperature: 0.95,
	}
}

func (a *AIConfig) normalize() {
	d := defaultAIConfig()
	a.Provider = strings.ToLower(strings.TrimSpace(a.Provider))
	if a.Provider == "" {
		a.Provider = d.Provider
	}
	if a.Model == "" {
		a.Model = d.Model
		if a.Provider == "ollama" {
			a.Model = "llama3.2"
		}
		if a.Provider == "gemini" {
			a.Model = "gemini-2.0-flash"
		}
	}
	if strings.TrimSpace(a.Persona) == "" {
		a.Persona = d.Persona
	}
	if a.MaxTokens <= 0 {
		a.MaxTokens = d.MaxTokens
	}
	if a.Temperature <= 0 {
		a.Temperature = d.Temperature
	}
}

func resolveAIAPIKey(cfg AIConfig) string {
	if k := strings.TrimSpace(cfg.APIKey); k != "" {
		return k
	}
	switch cfg.Provider {
	case "groq":
		if k := strings.TrimSpace(os.Getenv("GROQ_API_KEY")); k != "" {
			return k
		}
	case "openrouter":
		if k := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")); k != "" {
			return k
		}
	case "gemini":
		if k := strings.TrimSpace(os.Getenv("GEMINI_API_KEY")); k != "" {
			return k
		}
		if k := strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")); k != "" {
			return k
		}
	}
	if k := strings.TrimSpace(os.Getenv("AI_API_KEY")); k != "" {
		return k
	}
	raw, err := os.ReadFile(credentialsFile)
	if err != nil {
		return ""
	}
	var file struct {
		AIAPIKey       string `json:"aiApiKey"`
		GroqAPIKey     string `json:"groqApiKey"`
		GeminiAPIKey   string `json:"geminiApiKey"`
		OpenRouterKey  string `json:"openrouterApiKey"`
	}
	if json.Unmarshal(raw, &file) != nil {
		return ""
	}
	switch cfg.Provider {
	case "groq":
		if k := strings.TrimSpace(file.GroqAPIKey); k != "" {
			return k
		}
	case "gemini":
		if k := strings.TrimSpace(file.GeminiAPIKey); k != "" {
			return k
		}
	case "openrouter":
		if k := strings.TrimSpace(file.OpenRouterKey); k != "" {
			return k
		}
	}
	return strings.TrimSpace(file.AIAPIKey)
}

func openaiCompatibleURL(cfg AIConfig) string {
	if u := strings.TrimSpace(cfg.BaseURL); u != "" {
		return strings.TrimRight(u, "/") + "/chat/completions"
	}
	switch cfg.Provider {
	case "ollama":
		return "http://127.0.0.1:11434/v1/chat/completions"
	case "openrouter":
		return "https://openrouter.ai/api/v1/chat/completions"
	default: // groq
		return "https://api.groq.com/openai/v1/chat/completions"
	}
}

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func buildChatMessages(cfg AIConfig, otherName string, history []ConvMessage) []chatMsg {
	msgs := []chatMsg{{Role: "system", Content: cfg.Persona}}
	who := otherName
	if who == "" {
		who = "them"
	}
	// Keep last ~12 turns for context.
	start := 0
	if len(history) > 12 {
		start = len(history) - 12
	}
	for _, m := range history[start:] {
		text := strings.TrimSpace(m.Text)
		if text == "" {
			continue
		}
		if m.FromSelf {
			msgs = append(msgs, chatMsg{Role: "assistant", Content: text})
		} else {
			msgs = append(msgs, chatMsg{Role: "user", Content: text})
		}
	}
	// If history empty / ends on us, nudge a reply.
	if len(msgs) == 1 {
		msgs = append(msgs, chatMsg{
			Role:    "user",
			Content: fmt.Sprintf("(new chat with %s — say hi briefly)", who),
		})
	} else if msgs[len(msgs)-1].Role == "assistant" {
		msgs = append(msgs, chatMsg{
			Role:    "user",
			Content: "(they haven't said anything new — send a soft check-in, still in character)",
		})
	}
	return msgs
}

func generateAIReply(cfg AIConfig, otherName string, history []ConvMessage) (string, error) {
	cfg.normalize()
	if !cfg.Enabled {
		return "", fmt.Errorf("ai disabled")
	}
	switch cfg.Provider {
	case "gemini":
		return generateGeminiReply(cfg, otherName, history)
	case "groq", "ollama", "openrouter":
		return generateOpenAICompatReply(cfg, otherName, history)
	default:
		return "", fmt.Errorf("unknown ai provider %q (use groq, ollama, openrouter, gemini)", cfg.Provider)
	}
}

func generateOpenAICompatReply(cfg AIConfig, otherName string, history []ConvMessage) (string, error) {
	key := resolveAIAPIKey(cfg)
	if cfg.Provider != "ollama" && key == "" {
		return "", fmt.Errorf("%s api key missing — set credentials aiApiKey or env", cfg.Provider)
	}

	payload := map[string]any{
		"model":       cfg.Model,
		"messages":    buildChatMessages(cfg, otherName, history),
		"temperature": cfg.Temperature,
		"max_tokens":  cfg.MaxTokens,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", openaiCompatibleURL(cfg), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	if cfg.Provider == "openrouter" {
		req.Header.Set("HTTP-Referer", "https://www.tumblr.com")
		req.Header.Set("X-Title", "tumblr-dm-bot")
	}

	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%s: %s", resp.Status, truncate(string(raw), 200))
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("empty ai response")
	}
	return sanitizeAIReply(out.Choices[0].Message.Content), nil
}

func generateGeminiReply(cfg AIConfig, otherName string, history []ConvMessage) (string, error) {
	key := resolveAIAPIKey(cfg)
	if key == "" {
		return "", fmt.Errorf("gemini api key missing — set credentials geminiApiKey or GEMINI_API_KEY")
	}
	model := cfg.Model
	endpoint := fmt.Sprintf(
		"https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		urlPathEscape(model), key,
	)

	chat := buildChatMessages(cfg, otherName, history)
	var systemText string
	var contents []map[string]any
	for _, m := range chat {
		if m.Role == "system" {
			systemText = m.Content
			continue
		}
		role := "user"
		if m.Role == "assistant" {
			role = "model"
		}
		contents = append(contents, map[string]any{
			"role": role,
			"parts": []map[string]string{
				{"text": m.Content},
			},
		})
	}
	payload := map[string]any{
		"contents": contents,
		"generationConfig": map[string]any{
			"temperature":     cfg.Temperature,
			"maxOutputTokens": cfg.MaxTokens,
		},
	}
	if systemText != "" {
		payload["systemInstruction"] = map[string]any{
			"parts": []map[string]string{{"text": systemText}},
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%s: %s", resp.Status, truncate(string(raw), 200))
	}
	var out struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if len(out.Candidates) == 0 || len(out.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("empty gemini response")
	}
	return sanitizeAIReply(out.Candidates[0].Content.Parts[0].Text), nil
}

func urlPathEscape(s string) string {
	return strings.ReplaceAll(s, " ", "%20")
}

func sanitizeAIReply(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"'`)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	// Flatten multi-line into soft spaces for Tumblr DM.
	parts := strings.Split(s, "\n")
	var cleaned []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.TrimLeft(p, "-•* ")
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}
	s = strings.Join(cleaned, " ")
	lower := strings.ToLower(s)
	for _, bad := range []string{
		"as an ai", "as a language model", "i'm an ai", "i am an ai", "i'm a bot",
	} {
		if strings.Contains(lower, bad) {
			s = "heyy what's up"
			break
		}
	}
	// Hard cap length for DMs.
	if utf8.RuneCountInString(s) > 280 {
		r := []rune(s)
		s = string(r[:277]) + "…"
	}
	return strings.TrimSpace(s)
}

func pickReplyText(cfg MessageConfig, otherName string, history []ConvMessage) string {
	if cfg.AI.Enabled {
		text, err := generateAIReply(cfg.AI, otherName, history)
		if err == nil && text != "" {
			return text
		}
		if err != nil {
			fmt.Println("   ai fail:", err, "— using template")
		}
	}
	if len(cfg.ReplyTemplates) == 0 {
		return "hey! how's it going?"
	}
	return cfg.ReplyTemplates[rand.Intn(len(cfg.ReplyTemplates))]
}
