package llm

import (
	"testing"
)

func TestAnthropicMessagesEndpoint(t *testing.T) {
	if got := anthropicMessagesEndpoint("https://api.anthropic.com"); got != "https://api.anthropic.com/v1/messages" {
		t.Fatalf("endpoint=%q", got)
	}
	if got := anthropicMessagesEndpoint("https://api.anthropic.com/v1"); got != "https://api.anthropic.com/v1/messages" {
		t.Fatalf("endpoint=%q", got)
	}
}

func TestGeminiGenerateContentEndpoint(t *testing.T) {
	if got := geminiGenerateContentEndpoint("https://generativelanguage.googleapis.com", "gemini-2.5-flash"); got != "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent" {
		t.Fatalf("endpoint=%q", got)
	}
	if got := geminiGenerateContentEndpoint("https://generativelanguage.googleapis.com/v1beta", "models/gemini-2.5-flash"); got != "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent" {
		t.Fatalf("endpoint=%q", got)
	}
}

func TestToAnthropicMessages_ToolMapping(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
		{
			Role:    "assistant",
			Content: "calling tool",
			ToolCalls: []ToolCallPayload{
				{
					ID:   "call_1",
					Type: "function",
					Function: ToolCallPayloadFunc{
						Name:      "read_file",
						Arguments: `{"path":"README.md"}`,
					},
				},
			},
		},
		{Role: "tool", ToolCallID: "call_1", Content: `{"ok":true}`},
	}

	converted, system := toAnthropicMessages(msgs)
	if system != "sys" {
		t.Fatalf("system=%q", system)
	}
	if len(converted) != 3 {
		t.Fatalf("messages=%d", len(converted))
	}
	if converted[1].Role != "assistant" {
		t.Fatalf("role=%q", converted[1].Role)
	}
	if len(converted[1].Content) != 2 {
		t.Fatalf("assistant parts=%d", len(converted[1].Content))
	}
	if converted[1].Content[1].Type != "tool_use" {
		t.Fatalf("assistant part type=%q", converted[1].Content[1].Type)
	}
	if converted[2].Content[0].Type != "tool_result" {
		t.Fatalf("tool part type=%q", converted[2].Content[0].Type)
	}
}

func TestToGeminiMessages_ToolMapping(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
		{
			Role:    "assistant",
			Content: "calling tool",
			ToolCalls: []ToolCallPayload{
				{
					ID:   "call_1",
					Type: "function",
					Function: ToolCallPayloadFunc{
						Name:      "read_file",
						Arguments: `{"path":"README.md"}`,
					},
				},
			},
		},
		{Role: "tool", Name: "read_file", ToolCallID: "call_1", Content: `{"ok":true}`},
	}

	converted, system := toGeminiMessages(msgs)
	if system != "sys" {
		t.Fatalf("system=%q", system)
	}
	if len(converted) != 3 {
		t.Fatalf("messages=%d", len(converted))
	}
	if converted[1].Role != "model" {
		t.Fatalf("role=%q", converted[1].Role)
	}
	if len(converted[1].Parts) != 2 {
		t.Fatalf("model parts=%d", len(converted[1].Parts))
	}
	if converted[1].Parts[1].FunctionCall == nil {
		t.Fatalf("functionCall=nil")
	}
	if converted[2].Parts[0].FunctionResponse == nil {
		t.Fatalf("functionResponse=nil")
	}
}

func TestToOpenAIMessages_ImagePart(t *testing.T) {
	msgs := []Message{
		{
			Role: "user",
			Parts: []ContentPart{
				{Type: ContentPartTypeText, Text: "what is in this image?"},
				{Type: ContentPartTypeImage, MIMEType: "image/png", Data: "ZmFrZQ=="},
			},
		},
	}

	converted := toOpenAIMessages(msgs)
	if len(converted) != 1 {
		t.Fatalf("messages=%d", len(converted))
	}
	if converted[0].Content == nil {
		t.Fatalf("content is nil")
	}
	parts := converted[0].Content.Parts
	if len(parts) == 0 {
		t.Fatalf("parts are empty")
	}
	if len(parts) != 2 {
		t.Fatalf("parts=%d", len(parts))
	}
	if parts[0].Type != "text" || parts[1].Type != "image_url" {
		t.Fatalf("unexpected parts: %+v", parts)
	}
	if parts[1].ImageURL["url"] != "data:image/png;base64,ZmFrZQ==" {
		t.Fatalf("image_url=%q", parts[1].ImageURL["url"])
	}
}

func TestToAnthropicMessages_ImagePart(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{
			Role: "user",
			Parts: []ContentPart{
				{Type: ContentPartTypeText, Text: "describe"},
				{Type: ContentPartTypeImage, MIMEType: "image/jpeg", Data: "ZmFrZQ=="},
			},
		},
	}

	converted, _ := toAnthropicMessages(msgs)
	if len(converted) != 1 {
		t.Fatalf("messages=%d", len(converted))
	}
	if len(converted[0].Content) != 2 {
		t.Fatalf("parts=%d", len(converted[0].Content))
	}
	if converted[0].Content[1].Type != "image" {
		t.Fatalf("part type=%q", converted[0].Content[1].Type)
	}
	if converted[0].Content[1].Source == nil || converted[0].Content[1].Source.Data != "ZmFrZQ==" {
		t.Fatalf("source=%+v", converted[0].Content[1].Source)
	}
}

func TestToGeminiMessages_ImagePart(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{
			Role: "user",
			Parts: []ContentPart{
				{Type: ContentPartTypeText, Text: "describe"},
				{Type: ContentPartTypeImage, MIMEType: "image/jpeg", Data: "ZmFrZQ=="},
			},
		},
	}

	converted, _ := toGeminiMessages(msgs)
	if len(converted) != 1 {
		t.Fatalf("messages=%d", len(converted))
	}
	if len(converted[0].Parts) != 2 {
		t.Fatalf("parts=%d", len(converted[0].Parts))
	}
	if converted[0].Parts[1].InlineData == nil {
		t.Fatalf("inlineData=nil")
	}
	if converted[0].Parts[1].InlineData.Data != "ZmFrZQ==" {
		t.Fatalf("inline data=%q", converted[0].Parts[1].InlineData.Data)
	}
}
