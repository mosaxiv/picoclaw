package llm

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexResponsesEndpoint(t *testing.T) {
	if got := codexResponsesEndpoint(""); got != "https://chatgpt.com/backend-api/codex/responses" {
		t.Fatalf("endpoint=%q", got)
	}
	if got := codexResponsesEndpoint("https://chatgpt.com/backend-api"); got != "https://chatgpt.com/backend-api/codex/responses" {
		t.Fatalf("endpoint=%q", got)
	}
	if got := codexResponsesEndpoint("https://chatgpt.com/backend-api/codex/responses"); got != "https://chatgpt.com/backend-api/codex/responses" {
		t.Fatalf("endpoint=%q", got)
	}
	if got := codexResponsesEndpoint("https://chatgpt.com/backend-api/codex"); got != "https://chatgpt.com/backend-api/codex/responses" {
		t.Fatalf("endpoint=%q", got)
	}
}

func TestResolveCodexModel(t *testing.T) {
	if got := resolveCodexModel(""); got != defaultCodexModel {
		t.Fatalf("model=%q", got)
	}
	if got := resolveCodexModel("openai-codex/gpt-5.1-codex"); got != "gpt-5.1-codex" {
		t.Fatalf("model=%q", got)
	}
	if got := resolveCodexModel("anthropic/claude-sonnet"); got != defaultCodexModel {
		t.Fatalf("model=%q", got)
	}
}

func TestToCodexInput_ToolMapping(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
		{
			Role:    "assistant",
			Content: "calling tool",
			ToolCalls: []ToolCallPayload{
				{
					ID:   "call_1|fc_1",
					Type: "function",
					Function: ToolCallPayloadFunc{
						Name:      "read_file",
						Arguments: `{"path":"README.md"}`,
					},
				},
			},
		},
		{Role: "tool", ToolCallID: "call_1|fc_1", Name: "read_file", Content: `{"ok":true}`},
	}

	system, input := toCodexInput(msgs)
	if system != "sys" {
		t.Fatalf("system=%q", system)
	}
	if len(input) != 4 {
		t.Fatalf("input=%d", len(input))
	}
	if input[2].Type != "function_call" {
		t.Fatalf("type=%q", input[2].Type)
	}
	if input[2].CallID != "call_1" {
		t.Fatalf("call_id=%q", input[2].CallID)
	}
}

func TestConsumeCodexSSE_ToolCall(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read_file","arguments":""}}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"Hello"}`,
		"",
		`data: {"type":"response.function_call_arguments.delta","call_id":"call_1","delta":"{\"path\":\"README.md\"}"}`,
		"",
		`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read_file","arguments":"{\"path\":\"README.md\"}"}}`,
		"",
		`data: {"type":"response.completed","response":{"status":"completed"}}`,
		"",
	}, "\n")

	out, err := consumeCodexSSE(strings.NewReader(stream))
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if out.Content != "Hello" {
		t.Fatalf("content=%q", out.Content)
	}
	if len(out.ToolCalls) != 1 {
		t.Fatalf("tool_calls=%d", len(out.ToolCalls))
	}
	if out.ToolCalls[0].ID != "call_1|fc_1" {
		t.Fatalf("tool_id=%q", out.ToolCalls[0].ID)
	}
	var args map[string]string
	if err := json.Unmarshal(out.ToolCalls[0].Arguments, &args); err != nil {
		t.Fatalf("args json: %v", err)
	}
	if args["path"] != "README.md" {
		t.Fatalf("path=%q", args["path"])
	}
}

func TestParseAuthorizationInput(t *testing.T) {
	code, state := parseAuthorizationInput("http://localhost:1455/auth/callback?code=abc&state=xyz")
	if code != "abc" || state != "xyz" {
		t.Fatalf("code=%q state=%q", code, state)
	}
	code, state = parseAuthorizationInput("abc#xyz")
	if code != "abc" || state != "xyz" {
		t.Fatalf("code=%q state=%q", code, state)
	}
	code, state = parseAuthorizationInput("just-code")
	if code != "just-code" || state != "" {
		t.Fatalf("code=%q state=%q", code, state)
	}
}

func TestLoadCodexOAuthToken_FromStoredToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	path := filepath.Join(dir, ".clawlet", "auth", "codex.json")

	stored := codexStoredToken{
		Access:    "access-token",
		Refresh:   "refresh-token",
		Expires:   time.Now().Add(10 * time.Minute).UnixMilli(),
		AccountID: "acct_123",
	}
	b, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}

	tok, err := LoadCodexOAuthToken()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if tok.AccessToken != "access-token" {
		t.Fatalf("access=%q", tok.AccessToken)
	}
	if tok.AccountID != "acct_123" {
		t.Fatalf("account=%q", tok.AccountID)
	}
}

func TestDecodeCodexAccountID_FromNestedClaim(t *testing.T) {
	payload := map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct_nested",
		},
	}
	token := "x." + base64.RawURLEncoding.EncodeToString(mustJSON(payload)) + ".y"
	if got := decodeCodexAccountID(token); got != "acct_nested" {
		t.Fatalf("account_id=%q", got)
	}
}

func TestParseDeviceCodeResponse(t *testing.T) {
	body := []byte(`{"device_auth_id":"dev-1","user_code":"ABC-DEF","interval":"7","expires_in":"1800"}`)
	parsed, err := parseDeviceCodeResponse(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.DeviceAuthID != "dev-1" {
		t.Fatalf("device_auth_id=%q", parsed.DeviceAuthID)
	}
	if parsed.UserCode != "ABC-DEF" {
		t.Fatalf("user_code=%q", parsed.UserCode)
	}
	if parsed.IntervalSec != 7 {
		t.Fatalf("interval=%d", parsed.IntervalSec)
	}
	if parsed.ExpiresInSec != 1800 {
		t.Fatalf("expires_in=%d", parsed.ExpiresInSec)
	}
}

func TestParseTokenPayload_RefreshTokenOptionalOnRefreshFlow(t *testing.T) {
	body := []byte(`{"access_token":"acc","expires_in":3600}`)
	tok, err := parseTokenPayload(body, "missing", false)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if tok.Access != "acc" {
		t.Fatalf("access=%q", tok.Access)
	}
	if tok.Refresh != "" {
		t.Fatalf("refresh=%q", tok.Refresh)
	}
}

func TestParseTokenPayload_RequiresRefreshTokenOnAuthCodeFlow(t *testing.T) {
	body := []byte(`{"access_token":"acc","expires_in":3600}`)
	if _, err := parseTokenPayload(body, "missing", true); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCodexDeviceAuthIsPending(t *testing.T) {
	if !codexDeviceAuthIsPending([]byte(`{"error":"authorization_pending"}`)) {
		t.Fatal("expected pending=true")
	}
	if !codexDeviceAuthIsPending([]byte(`{"error":{"message":"Device authorization is unknown. Please try again.","type":"invalid_request_error","code":"deviceauth_authorization_unknown"}}`)) {
		t.Fatal("expected pending=true for nested deviceauth_authorization_unknown")
	}
	if !codexDeviceAuthIsPending([]byte(`{"error":{"message":"Device authorization is unknown. Please try again."}}`)) {
		t.Fatal("expected pending=true for unknown message only")
	}
	if codexDeviceAuthIsPending([]byte(`{"error":"access_denied"}`)) {
		t.Fatal("expected pending=false")
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
