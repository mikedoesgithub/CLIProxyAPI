// Package balance implements bounded local classification and registry-aware model selection.
package balance

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

type Input struct {
	Request          string   `json:"request"`
	Context          string   `json:"context"`
	Vision           bool     `json:"-"`
	Tools            bool     `json:"-"`
	NativeTools      []string `json:"-"`
	LongContext      bool     `json:"-"`
	ExplicitThinking bool     `json:"-"`
}

// Extract reads only user text, never system instructions, tool output or image data.
// Opaque server-side continuations cannot be safely moved across providers.
func Extract(raw []byte) (Input, error) {
	var body map[string]json.RawMessage
	if err := json.Unmarshal(raw, &body); err != nil || body == nil {
		return Input{}, errors.New("balance requires a JSON chat request")
	}
	for _, key := range []string{"previous_response_id", "previous_interaction_id", "interaction_id"} {
		if v := body[key]; len(v) > 0 && string(v) != "null" && string(v) != "\"\"" {
			return Input{}, errors.New("balance requires full conversation history; pin the actual model for server-side continuations")
		}
	}
	var users []string
	in := Input{}
	parsed := gjson.ParseBytes(raw)
	for _, path := range []string{"thinking", "reasoning", "reasoning_effort", "generationConfig.thinkingConfig", "generation_config.thinking_config"} {
		if parsed.Get(path).Exists() {
			in.ExplicitThinking = true
		}
	}
	for _, item := range parsed.Get("input").Array() {
		if item.Get("type").String() == "item_reference" || item.Get("type").String() == "compaction" || item.Get("encrypted_content").Exists() {
			return Input{}, errors.New("balance cannot route opaque reasoning, compaction or item references; pin a concrete model")
		}
	}

	for _, key := range []string{"messages", "input", "contents"} {
		var text string
		if key == "input" && json.Unmarshal(body[key], &text) == nil {
			users = append(users, text)
			continue
		}
		var messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
			Parts   json.RawMessage `json:"parts"`
			Type    string          `json:"type"`
		}
		_ = json.Unmarshal(body[key], &messages)
		for _, m := range messages {
			if m.Role != "user" && !(key == "input" && m.Role == "" && (m.Type == "message" || m.Type == "")) {
				continue
			}
			content := m.Content
			if key == "contents" {
				content = m.Parts
			}
			for _, part := range gjson.ParseBytes(content).Array() {
				kind := part.Get("type").String()
				if kind == "input_audio" || kind == "audio" || kind == "input_file" || kind == "file" || kind == "video" || kind == "input_video" {
					return Input{}, errors.New("balance currently supports text and image chat inputs; pin a concrete model for other modalities")
				}
				for _, key := range []string{"inlineData", "inline_data", "fileData", "file_data"} {
					if media := part.Get(key); media.Exists() {
						mime := media.Get("mimeType").String()
						if mime == "" {
							mime = media.Get("mime_type").String()
						}
						if !strings.HasPrefix(mime, "image/") {
							return Input{}, errors.New("balance requires an image MIME type for inline or file media")
						}
					}
				}
			}
			text, vision := userText(content)
			in.Vision = in.Vision || vision
			if text != "" {
				users = append(users, text)
			}
		}
	}
	var tools []map[string]json.RawMessage
	_ = json.Unmarshal(body["tools"], &tools)
	for _, tool := range tools {
		in.Tools = true
		var kind string
		_ = json.Unmarshal(tool["type"], &kind)
		if kind != "" && kind != "function" {
			in.NativeTools = append(in.NativeTools, kind)
		}
		for k := range tool {
			if k != "type" && k != "function" && k != "functionDeclarations" && k != "function_declarations" && k != "name" && k != "description" && k != "input_schema" && k != "parameters" && k != "strict" && kind == "" {
				in.NativeTools = append(in.NativeTools, k)
			}
		}
	}
	total := 0
	for _, text := range users {
		total += utf8.RuneCountInString(text)
	}
	in.LongContext = total > 32000
	in.Context = "(new conversation)"
	if len(users) > 0 {
		in.Request = users[len(users)-1]
		runes := []rune(in.Request)
		if len(runes) > 4000 {
			in.Request = string(runes[:3000]) + string(runes[len(runes)-1000:])
		}
	}
	if len(users) > 1 {
		in.Context = tail(users[len(users)-2], 600)
	}
	return in, nil
}

func userText(raw json.RawMessage) (string, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, false
	}
	var parts []struct {
		Type        string          `json:"type"`
		Text        string          `json:"text"`
		Image       json.RawMessage `json:"image_url"`
		Inline      json.RawMessage `json:"inlineData"`
		File        json.RawMessage `json:"fileData"`
		InlineSnake json.RawMessage `json:"inline_data"`
		FileSnake   json.RawMessage `json:"file_data"`
		Source      json.RawMessage `json:"source"`
	}
	_ = json.Unmarshal(raw, &parts)
	var texts []string
	vision := false
	for _, p := range parts {
		if p.Type == "image" || p.Type == "image_url" || p.Type == "input_image" || len(p.Image) > 0 || len(p.Inline) > 0 || len(p.File) > 0 || len(p.InlineSnake) > 0 || len(p.FileSnake) > 0 {
			vision = true
			continue
		}
		if p.Type == "" || p.Type == "text" || p.Type == "input_text" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n"), vision
}

func tail(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[len(r)-n:]
	}
	return string(r)
}
