package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func (c *Client) chatOpenAICompatible(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResult, error) {
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/chat/completions"

	type chatRequest struct {
		Model       string           `json:"model"`
		Messages    []openAIMessage  `json:"messages"`
		MaxTokens   int              `json:"max_tokens,omitempty"`
		Temperature *float64         `json:"temperature,omitempty"`
		Tools       []ToolDefinition `json:"tools,omitempty"`
		ToolChoice  string           `json:"tool_choice,omitempty"`
	}
	reqBody := chatRequest{
		Model:       c.Model,
		Messages:    toOpenAIMessages(messages),
		MaxTokens:   c.maxTokensValue(),
		Temperature: c.temperatureValue(),
	}
	if len(tools) > 0 {
		reqBody.Tools = tools
		reqBody.ToolChoice = "auto"
	}
	b, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(c.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	for k, v := range c.Headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		req.Header.Set(k, v)
	}

	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 120 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("llm http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse llm response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("llm response: no choices")
	}
	m := parsed.Choices[0].Message
	out := &ChatResult{Content: m.Content}
	for _, tc := range m.ToolCalls {
		args := tc.Function.Arguments
		// OpenAI-compatible servers typically return arguments as a JSON string.
		// Convert it to raw JSON bytes so downstream tools can unmarshal into structs.
		if len(args) > 0 && args[0] == '"' {
			var s string
			if err := json.Unmarshal(args, &s); err == nil {
				args = []byte(s)
			}
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}
	return out, nil
}

type openAIMessage struct {
	Role       string            `json:"role"`
	Content    *openAIContent    `json:"content,omitempty"`
	ToolCalls  []ToolCallPayload `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	Name       string            `json:"name,omitempty"`
}

type openAIContent struct {
	Text  string
	Parts []openAIContentPart
}

func (c openAIContent) MarshalJSON() ([]byte, error) {
	if len(c.Parts) > 0 {
		return json.Marshal(c.Parts)
	}
	return json.Marshal(c.Text)
}

type openAIContentPart struct {
	Type     string            `json:"type"`
	Text     string            `json:"text,omitempty"`
	ImageURL map[string]string `json:"image_url,omitempty"`
}

func toOpenAIMessages(messages []Message) []openAIMessage {
	out := make([]openAIMessage, 0, len(messages))
	for _, m := range messages {
		item := openAIMessage{
			Role:       m.Role,
			ToolCalls:  m.ToolCalls,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
		if len(m.Parts) > 0 {
			parts := toOpenAIContentParts(m.Parts)
			if len(parts) > 0 {
				item.Content = &openAIContent{Parts: parts}
			} else if strings.TrimSpace(m.Content) != "" {
				item.Content = &openAIContent{Text: m.Content}
			}
		} else if strings.TrimSpace(m.Content) != "" {
			item.Content = &openAIContent{Text: m.Content}
		}
		out = append(out, item)
	}
	return out
}

func toOpenAIContentParts(parts []ContentPart) []openAIContentPart {
	out := make([]openAIContentPart, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case ContentPartTypeText:
			if strings.TrimSpace(part.Text) == "" {
				continue
			}
			out = append(out, openAIContentPart{
				Type: "text",
				Text: part.Text,
			})
		case ContentPartTypeImage:
			mimeType := strings.TrimSpace(part.MIMEType)
			if mimeType == "" {
				mimeType = "image/jpeg"
			}
			data := strings.TrimSpace(part.Data)
			if data == "" {
				continue
			}
			out = append(out, openAIContentPart{
				Type: "image_url",
				ImageURL: map[string]string{
					"url": "data:" + mimeType + ";base64," + data,
				},
			})
		}
	}
	return out
}
