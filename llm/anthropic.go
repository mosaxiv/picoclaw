package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const anthropicVersion = "2023-06-01"

func (c *Client) chatAnthropic(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResult, error) {
	endpoint := anthropicMessagesEndpoint(c.BaseURL)

	anthropicMessages, systemText := toAnthropicMessages(messages)
	reqBody := struct {
		Model       string          `json:"model"`
		Messages    []anthropicMsg  `json:"messages"`
		System      string          `json:"system,omitempty"`
		Tools       []anthropicTool `json:"tools,omitempty"`
		MaxTokens   int             `json:"max_tokens"`
		Temperature *float64        `json:"temperature,omitempty"`
	}{
		Model:       c.Model,
		Messages:    anthropicMessages,
		System:      systemText,
		MaxTokens:   c.maxTokensValue(),
		Temperature: c.temperatureValue(),
	}
	if len(tools) > 0 {
		converted, err := toAnthropicTools(tools)
		if err != nil {
			return nil, err
		}
		reqBody.Tools = converted
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
		req.Header.Set("x-api-key", c.APIKey)
	}
	req.Header.Set("anthropic-version", anthropicVersion)
	for k, v := range c.Headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		req.Header.Set(k, v)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("llm http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text,omitempty"`
			ID    string          `json:"id,omitempty"`
			Name  string          `json:"name,omitempty"`
			Input json.RawMessage `json:"input,omitempty"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse anthropic response: %w", err)
	}
	if len(parsed.Content) == 0 {
		return nil, fmt.Errorf("anthropic response: empty content")
	}

	out := &ChatResult{}
	var textParts []string
	for i, part := range parsed.Content {
		switch part.Type {
		case "text":
			if strings.TrimSpace(part.Text) != "" {
				textParts = append(textParts, part.Text)
			}
		case "tool_use":
			toolID := strings.TrimSpace(part.ID)
			if toolID == "" {
				toolID = fmt.Sprintf("toolu_%d", i+1)
			}
			args := part.Input
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:        toolID,
				Name:      part.Name,
				Arguments: args,
			})
		}
	}
	out.Content = strings.Join(textParts, "\n")
	return out, nil
}

type anthropicMsg struct {
	Role    string                 `json:"role"`
	Content []anthropicContentPart `json:"content"`
}

type anthropicContentPart struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type anthropicTool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"input_schema,omitempty"`
}

func toAnthropicTools(tools []ToolDefinition) ([]anthropicTool, error) {
	out := make([]anthropicTool, 0, len(tools))
	for _, t := range tools {
		params, err := schemaToAny(t.Function.Parameters)
		if err != nil {
			return nil, fmt.Errorf("anthropic tool schema %s: %w", t.Function.Name, err)
		}
		out = append(out, anthropicTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: params,
		})
	}
	return out, nil
}

func toAnthropicMessages(messages []Message) ([]anthropicMsg, string) {
	out := make([]anthropicMsg, 0, len(messages))
	systemParts := make([]string, 0, 1)
	pendingToolResults := make([]anthropicContentPart, 0)

	flushToolResults := func() {
		if len(pendingToolResults) == 0 {
			return
		}
		out = append(out, anthropicMsg{
			Role:    "user",
			Content: pendingToolResults,
		})
		pendingToolResults = pendingToolResults[:0]
	}

	for _, m := range messages {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		switch role {
		case "system":
			if strings.TrimSpace(m.Content) != "" {
				systemParts = append(systemParts, m.Content)
			}
		case "tool":
			pendingToolResults = append(pendingToolResults, anthropicContentPart{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   m.Content,
			})
		case "user", "assistant":
			flushToolResults()

			parts := make([]anthropicContentPart, 0, 1+len(m.ToolCalls))
			if strings.TrimSpace(m.Content) != "" {
				parts = append(parts, anthropicContentPart{
					Type: "text",
					Text: m.Content,
				})
			}
			if role == "assistant" {
				for i, tc := range m.ToolCalls {
					toolID := strings.TrimSpace(tc.ID)
					if toolID == "" {
						toolID = fmt.Sprintf("toolu_%d", i+1)
					}
					parts = append(parts, anthropicContentPart{
						Type:  "tool_use",
						ID:    toolID,
						Name:  tc.Function.Name,
						Input: parseArgsToRawJSON(tc.Function.Arguments),
					})
				}
			}
			if len(parts) > 0 {
				out = append(out, anthropicMsg{
					Role:    role,
					Content: parts,
				})
			}
		}
	}
	flushToolResults()
	return out, strings.Join(systemParts, "\n\n")
}

func anthropicMessagesEndpoint(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/messages"
	}
	return base + "/v1/messages"
}

func parseArgsToRawJSON(s string) json.RawMessage {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return json.RawMessage(`{}`)
	}
	b := []byte(trimmed)
	if json.Valid(b) {
		return b
	}
	quoted, _ := json.Marshal(trimmed)
	return quoted
}

func schemaToAny(s JSONSchema) (any, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return map[string]any{"type": "object"}, nil
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	if out == nil {
		return map[string]any{"type": "object"}, nil
	}
	return out, nil
}
