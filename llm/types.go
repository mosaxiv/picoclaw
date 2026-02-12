package llm

import "encoding/json"

// Message is an OpenAI-compatible chat message.
// We only model the fields clawlet needs.
type Message struct {
	Role       string            `json:"role"`
	Content    string            `json:"content,omitempty"`
	ToolCalls  []ToolCallPayload `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
	Name       string            `json:"name,omitempty"`
}

// ToolCallPayload is used inside assistant messages to request tool execution.
type ToolCallPayload struct {
	ID       string              `json:"id"`
	Type     string              `json:"type"`
	Function ToolCallPayloadFunc `json:"function"`
}

type ToolCallPayloadFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// JSONSchema is a small subset of JSON Schema used for tool parameters.
type JSONSchema struct {
	Type        string                `json:"type,omitempty"`
	Description string                `json:"description,omitempty"`
	Properties  map[string]JSONSchema `json:"properties,omitempty"`
	Required    []string              `json:"required,omitempty"`
	Enum        []string              `json:"enum,omitempty"`
	Items       *JSONSchema           `json:"items,omitempty"`

	// Raw can be used when the schema cannot be expressed with this subset.
	// If set, other fields should be left empty.
	Raw json.RawMessage `json:"-"`
}

func (s JSONSchema) MarshalJSON() ([]byte, error) {
	if len(s.Raw) > 0 {
		return s.Raw, nil
	}
	type alias JSONSchema
	a := alias(s)
	a.Raw = nil
	return json.Marshal(a)
}

type FunctionDefinition struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Parameters  JSONSchema `json:"parameters"`
}

type ToolDefinition struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}
