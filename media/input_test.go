package media

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mosaxiv/clawlet/bus"
	"github.com/mosaxiv/clawlet/config"
	"github.com/mosaxiv/clawlet/llm"
)

func TestPrepareInbound_ImagePart(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "img.png")
	if err := os.WriteFile(imgPath, []byte("\x89PNG\r\n\x1a\n"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}

	cfg := config.Default().Tools.Media
	inbound := bus.InboundMessage{
		Content: "describe",
		Attachments: []bus.Attachment{{
			Name:      "img.png",
			MIMEType:  "image/png",
			Kind:      "image",
			LocalPath: imgPath,
		}},
	}
	client := &llm.Client{Provider: "openai", Model: "gpt-4o-mini"}

	got, err := PrepareInbound(context.Background(), client, cfg, inbound)
	if err != nil {
		t.Fatalf("PrepareInbound error: %v", err)
	}
	if len(got.UserMessage.Parts) != 2 {
		t.Fatalf("parts=%d", len(got.UserMessage.Parts))
	}
	if got.UserMessage.Parts[1].Type != llm.ContentPartTypeImage {
		t.Fatalf("part type=%q", got.UserMessage.Parts[1].Type)
	}
	if !strings.Contains(got.SessionText, "[Image 1]") {
		t.Fatalf("session text=%q", got.SessionText)
	}
}

func TestPrepareInbound_AudioTranscription(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/audio/transcriptions" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"text": "transcribed voice"})
	}))
	defer srv.Close()

	dir := t.TempDir()
	audioPath := filepath.Join(dir, "voice.ogg")
	if err := os.WriteFile(audioPath, []byte("OggS"), 0o600); err != nil {
		t.Fatalf("write audio: %v", err)
	}

	cfg := config.Default().Tools.Media
	inbound := bus.InboundMessage{
		Content: "",
		Attachments: []bus.Attachment{{
			Name:      "voice.ogg",
			MIMEType:  "audio/ogg",
			Kind:      "audio",
			LocalPath: audioPath,
		}},
	}
	client := &llm.Client{
		Provider: "openai",
		BaseURL:  srv.URL,
		APIKey:   "test-key",
		Model:    "gpt-4o-mini",
		HTTP:     srv.Client(),
	}

	got, err := PrepareInbound(context.Background(), client, cfg, inbound)
	if err != nil {
		t.Fatalf("PrepareInbound error: %v", err)
	}
	if got.UserMessage.Content == "" {
		t.Fatalf("empty content")
	}
	if !strings.Contains(got.UserMessage.Content, "transcribed voice") {
		t.Fatalf("content=%q", got.UserMessage.Content)
	}
}

func TestPrepareInbound_TextAttachment(t *testing.T) {
	dir := t.TempDir()
	txtPath := filepath.Join(dir, "memo.txt")
	if err := os.WriteFile(txtPath, []byte("hello\nworld"), 0o600); err != nil {
		t.Fatalf("write txt: %v", err)
	}

	cfg := config.Default().Tools.Media
	inbound := bus.InboundMessage{
		Content: "check",
		Attachments: []bus.Attachment{{
			Name:      "memo.txt",
			MIMEType:  "text/plain",
			Kind:      "file",
			LocalPath: txtPath,
		}},
	}
	client := &llm.Client{Provider: "openai", Model: "gpt-4o-mini"}

	got, err := PrepareInbound(context.Background(), client, cfg, inbound)
	if err != nil {
		t.Fatalf("PrepareInbound error: %v", err)
	}
	if !strings.Contains(got.UserMessage.Content, "[Attachment] memo.txt") {
		t.Fatalf("content=%q", got.UserMessage.Content)
	}
	if !strings.Contains(got.UserMessage.Content, "hello") {
		t.Fatalf("content=%q", got.UserMessage.Content)
	}
}

func TestReadAttachmentBytes_BlockPrivateHost(t *testing.T) {
	_, _, err := readAttachmentBytes(context.Background(), bus.Attachment{
		URL:      "http://127.0.0.1/private.txt",
		MIMEType: "text/plain",
	}, 1024, 2)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("err=%v", err)
	}
}

func TestReadAttachmentBytes_RejectAuthHeaderForUntrustedHost(t *testing.T) {
	_, _, err := readAttachmentBytes(context.Background(), bus.Attachment{
		URL:      "https://example.com/private.txt",
		MIMEType: "text/plain",
		Headers: map[string]string{
			"Authorization": "Bearer secret",
		},
	}, 1024, 2)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "untrusted host") {
		t.Fatalf("err=%v", err)
	}
}
