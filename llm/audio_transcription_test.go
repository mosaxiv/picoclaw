package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTranscribeAudio_OpenAICompatible(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/audio/transcriptions" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method=%q", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("authorization=%q", got)
		}
		if !strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
			t.Fatalf("content-type=%q", r.Header.Get("Content-Type"))
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_ = json.NewEncoder(w).Encode(map[string]string{"text": "hello world"})
	}))
	defer srv.Close()

	c := &Client{
		Provider: "openai",
		BaseURL:  srv.URL,
		APIKey:   "test-key",
		Model:    "gpt-4o-mini",
		HTTP:     srv.Client(),
	}
	got, err := c.TranscribeAudio(context.Background(), []byte("fake audio"), "audio/ogg", "voice.ogg")
	if err != nil {
		t.Fatalf("TranscribeAudio error: %v", err)
	}
	if !called {
		t.Fatalf("server was not called")
	}
	if got != "hello world" {
		t.Fatalf("text=%q", got)
	}
}

func TestTranscribeAudio_Gemini(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/models/gemini-2.5-flash:generateContent") {
			t.Fatalf("path=%q", r.URL.Path)
		}
		if got := r.Header.Get("x-goog-api-key"); got != "g-key" {
			t.Fatalf("api-key=%q", got)
		}
		type part struct {
			Text string `json:"text,omitempty"`
		}
		type content struct {
			Parts []part `json:"parts"`
		}
		type candidate struct {
			Content content `json:"content"`
		}
		type response struct {
			Candidates []candidate `json:"candidates"`
		}
		_ = json.NewEncoder(w).Encode(response{
			Candidates: []candidate{
				{
					Content: content{
						Parts: []part{{Text: "line1"}, {Text: "line2"}},
					},
				},
			},
		})
	}))
	defer srv.Close()

	c := &Client{
		Provider: "gemini",
		BaseURL:  srv.URL,
		APIKey:   "g-key",
		Model:    "gemini-2.5-flash",
		HTTP:     srv.Client(),
	}
	got, err := c.TranscribeAudio(context.Background(), []byte("fake audio"), "audio/ogg", "voice.ogg")
	if err != nil {
		t.Fatalf("TranscribeAudio error: %v", err)
	}
	if got != "line1\nline2" {
		t.Fatalf("text=%q", got)
	}
}

func TestSupportsImageInput(t *testing.T) {
	cases := []struct {
		provider string
		model    string
		want     bool
	}{
		{provider: "gemini", model: "gemini-2.5-flash", want: true},
		{provider: "openai", model: "gpt-4o-mini", want: true},
		{provider: "openai", model: "gpt-3.5-turbo", want: false},
		{provider: "anthropic", model: "claude-sonnet", want: true},
		{provider: "openai-codex", model: "gpt-5-codex", want: false},
	}
	for _, tc := range cases {
		c := &Client{Provider: tc.provider, Model: tc.model}
		if got := c.SupportsImageInput(); got != tc.want {
			t.Fatalf("provider=%s model=%s got=%v want=%v", tc.provider, tc.model, got, tc.want)
		}
	}
}
