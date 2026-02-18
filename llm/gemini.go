package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func (c *Client) chatGemini(ctx context.Context, messages []Message, tools []ToolDefinition) (*ChatResult, error) {
	endpoint := geminiGenerateContentEndpoint(c.BaseURL, c.Model)

	contents, systemText := toGeminiMessages(messages)
	reqBody := struct {
		Contents          []geminiContent `json:"contents,omitempty"`
		SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
		Tools             []geminiTool    `json:"tools,omitempty"`
		GenerationConfig  struct {
			MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
			Temperature     *float64 `json:"temperature,omitempty"`
		} `json:"generationConfig"`
	}{
		Contents: contents,
	}
	if strings.TrimSpace(systemText) != "" {
		reqBody.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: systemText}},
		}
	}
	if len(tools) > 0 {
		converted, err := toGeminiTools(tools)
		if err != nil {
			return nil, err
		}
		reqBody.Tools = converted
	}
	reqBody.GenerationConfig.MaxOutputTokens = c.maxTokensValue()
	reqBody.GenerationConfig.Temperature = c.temperatureValue()

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
		req.Header.Set("x-goog-api-key", c.APIKey)
	}
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
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text         string `json:"text,omitempty"`
					FunctionCall *struct {
						Name string          `json:"name"`
						Args json.RawMessage `json:"args"`
					} `json:"functionCall,omitempty"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason,omitempty"`
		} `json:"promptFeedback"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse gemini response: %w", err)
	}
	if len(parsed.Candidates) == 0 {
		if strings.TrimSpace(parsed.PromptFeedback.BlockReason) != "" {
			return nil, fmt.Errorf("gemini blocked: %s", parsed.PromptFeedback.BlockReason)
		}
		return nil, fmt.Errorf("gemini response: no candidates")
	}

	out := &ChatResult{}
	var textParts []string
	callCount := 0
	for _, part := range parsed.Candidates[0].Content.Parts {
		if strings.TrimSpace(part.Text) != "" {
			textParts = append(textParts, part.Text)
		}
		if part.FunctionCall != nil {
			callCount++
			args := part.FunctionCall.Args
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			out.ToolCalls = append(out.ToolCalls, ToolCall{
				ID:        fmt.Sprintf("call_%d", callCount),
				Name:      part.FunctionCall.Name,
				Arguments: args,
			})
		}
	}
	out.Content = strings.Join(textParts, "\n")
	return out, nil
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	InlineData       *geminiInlineData       `json:"inlineData,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
}

type geminiFunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

func toGeminiTools(tools []ToolDefinition) ([]geminiTool, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	decls := make([]geminiFunctionDeclaration, 0, len(tools))
	for _, t := range tools {
		params, err := schemaToRawJSON(t.Function.Parameters)
		if err != nil {
			return nil, fmt.Errorf("gemini tool schema %s: %w", t.Function.Name, err)
		}
		decls = append(decls, geminiFunctionDeclaration{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  params,
		})
	}
	return []geminiTool{{FunctionDeclarations: decls}}, nil
}

func toGeminiMessages(messages []Message) ([]geminiContent, string) {
	contents := make([]geminiContent, 0, len(messages))
	systemParts := make([]string, 0, 1)

	for _, m := range messages {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		switch role {
		case "system":
			if strings.TrimSpace(m.Content) != "" {
				systemParts = append(systemParts, m.Content)
			}
		case "user":
			parts := toGeminiInputParts(m)
			if len(parts) == 0 {
				continue
			}
			contents = append(contents, geminiContent{
				Role:  "user",
				Parts: parts,
			})
		case "assistant":
			parts := toGeminiInputParts(m)
			if len(parts) == 0 {
				parts = make([]geminiPart, 0, len(m.ToolCalls))
			}
			for _, tc := range m.ToolCalls {
				parts = append(parts, geminiPart{
					FunctionCall: &geminiFunctionCall{
						Name: tc.Function.Name,
						Args: parseArgsToRawJSON(tc.Function.Arguments),
					},
				})
			}
			if len(parts) > 0 {
				contents = append(contents, geminiContent{
					Role:  "model",
					Parts: parts,
				})
			}
		case "tool":
			name := strings.TrimSpace(m.Name)
			if name == "" {
				name = "tool"
			}
			contents = append(contents, geminiContent{
				Role: "user",
				Parts: []geminiPart{{
					FunctionResponse: &geminiFunctionResponse{
						Name:     name,
						Response: parseToolResponseValue(m.Content),
					},
				}},
			})
		}
	}

	return contents, strings.Join(systemParts, "\n\n")
}

func toGeminiInputParts(m Message) []geminiPart {
	if len(m.Parts) == 0 {
		if strings.TrimSpace(m.Content) == "" {
			return nil
		}
		return []geminiPart{{Text: m.Content}}
	}

	out := make([]geminiPart, 0, len(m.Parts)+1)
	if strings.TrimSpace(m.Content) != "" {
		out = append(out, geminiPart{Text: m.Content})
	}
	for _, p := range m.Parts {
		switch p.Type {
		case ContentPartTypeText:
			if strings.TrimSpace(p.Text) == "" {
				continue
			}
			out = append(out, geminiPart{Text: p.Text})
		case ContentPartTypeImage:
			mimeType := strings.TrimSpace(p.MIMEType)
			if mimeType == "" {
				mimeType = "image/jpeg"
			}
			data := strings.TrimSpace(p.Data)
			if data == "" {
				continue
			}
			out = append(out, geminiPart{
				InlineData: &geminiInlineData{
					MimeType: mimeType,
					Data:     data,
				},
			})
		}
	}
	if len(out) == 0 && strings.TrimSpace(m.Content) != "" {
		return []geminiPart{{Text: m.Content}}
	}
	return out
}

func parseToolResponseValue(s string) json.RawMessage {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return json.RawMessage(`{}`)
	}
	b := []byte(trimmed)
	if json.Valid(b) {
		return json.RawMessage(b)
	}
	fallback, _ := json.Marshal(map[string]string{"content": s})
	return json.RawMessage(fallback)
}

func geminiGenerateContentEndpoint(baseURL, model string) string {
	base := strings.TrimRight(baseURL, "/")
	m := strings.TrimPrefix(strings.TrimSpace(model), "models/")
	escaped := url.PathEscape(m)

	if strings.Contains(base, "/v1beta") || strings.HasSuffix(base, "/v1") || strings.Contains(base, "/v1/") {
		return base + "/models/" + escaped + ":generateContent"
	}
	return base + "/v1beta/models/" + escaped + ":generateContent"
}
