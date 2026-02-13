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
		Messages    []Message        `json:"messages"`
		MaxTokens   int              `json:"max_tokens,omitempty"`
		Temperature *float64         `json:"temperature,omitempty"`
		Tools       []ToolDefinition `json:"tools,omitempty"`
		ToolChoice  string           `json:"tool_choice,omitempty"`
	}
	reqBody := chatRequest{
		Model:       c.Model,
		Messages:    messages,
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
