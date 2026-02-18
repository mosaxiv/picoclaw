package media

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mosaxiv/clawlet/bus"
	"github.com/mosaxiv/clawlet/config"
	"github.com/mosaxiv/clawlet/llm"
)

type PreparedInbound struct {
	UserMessage llm.Message
	SessionText string
}

func PrepareInbound(ctx context.Context, client *llm.Client, cfg config.MediaToolsConfig, inbound bus.InboundMessage) (PreparedInbound, error) {
	baseText := strings.TrimSpace(inbound.Content)
	prepared := PreparedInbound{
		UserMessage: llm.Message{Role: "user", Content: baseText},
		SessionText: baseText,
	}
	if client == nil || !cfg.EnabledValue() || len(inbound.Attachments) == 0 {
		return prepared, nil
	}

	maxAttachments := cfg.MaxAttachments
	if maxAttachments <= 0 {
		maxAttachments = config.DefaultMediaMaxAttachments
	}
	if maxAttachments > len(inbound.Attachments) {
		maxAttachments = len(inbound.Attachments)
	}
	attachments := inbound.Attachments[:maxAttachments]
	omitted := len(inbound.Attachments) - maxAttachments

	textSections := make([]string, 0, 1+len(attachments))
	if baseText != "" {
		textSections = append(textSections, "User text:\n"+baseText)
	}

	imageParts := make([]llm.ContentPart, 0, len(attachments))
	imageNotes := make([]string, 0, len(attachments))

	for i, raw := range attachments {
		if err := ctx.Err(); err != nil {
			return prepared, err
		}
		att := normalizeAttachment(raw, i+1)
		name := att.Name
		if name == "" {
			name = fmt.Sprintf("attachment-%d", i+1)
		}

		switch att.Kind {
		case "image":
			handledImage := false
			if cfg.ImageEnabledValue() && client.SupportsImageInput() {
				data, mimeType, err := readAttachmentBytes(ctx, att, cfg.MaxInlineImageBytes, cfg.DownloadTimeoutSec)
				if err == nil && len(data) > 0 {
					if mimeType == "" {
						mimeType = "image/jpeg"
					}
					imageParts = append(imageParts, llm.ContentPart{
						Type:     llm.ContentPartTypeImage,
						MIMEType: mimeType,
						Data:     base64.StdEncoding.EncodeToString(data),
						Name:     name,
					})
					imageNotes = append(imageNotes, fmt.Sprintf("[Image %d] %s (%s)", len(imageParts), name, mimeType))
					handledImage = true
				}
			}
			if handledImage {
				continue
			}
			if cfg.AttachmentEnabledValue() {
				textSections = append(textSections, fmt.Sprintf("[Image attachment] %s", name))
			}
		case "audio":
			handledAudio := false
			if cfg.AudioEnabledValue() && client.SupportsAudioTranscription() {
				data, mimeType, err := readAttachmentBytes(ctx, att, cfg.MaxFileBytes, cfg.DownloadTimeoutSec)
				if err == nil && len(data) > 0 {
					transcript, txErr := client.TranscribeAudio(ctx, data, mimeType, name)
					if txErr == nil && strings.TrimSpace(transcript) != "" {
						textSections = append(textSections, fmt.Sprintf("[Audio transcript: %s]\n%s", name, strings.TrimSpace(transcript)))
						handledAudio = true
					}
				}
			}
			if handledAudio {
				continue
			}
			if cfg.AttachmentEnabledValue() {
				textSections = append(textSections, fmt.Sprintf("[Audio attachment] %s", name))
			}
		default:
			if !cfg.AttachmentEnabledValue() {
				continue
			}
			section := buildAttachmentSection(ctx, att, cfg)
			if section != "" {
				textSections = append(textSections, section)
			}
		}
	}

	if omitted > 0 {
		textSections = append(textSections, fmt.Sprintf("[%d additional attachments omitted]", omitted))
	}

	text := strings.TrimSpace(strings.Join(textSections, "\n\n"))
	if len(imageParts) == 0 {
		prepared.UserMessage = llm.Message{Role: "user", Content: text}
		prepared.SessionText = text
		return prepared, nil
	}

	if text == "" {
		text = "Please analyze the attached image(s)."
	}
	parts := make([]llm.ContentPart, 0, 1+len(imageParts))
	parts = append(parts, llm.ContentPart{Type: llm.ContentPartTypeText, Text: text})
	parts = append(parts, imageParts...)
	prepared.UserMessage = llm.Message{Role: "user", Parts: parts}

	sessionText := text
	if len(imageNotes) > 0 {
		sessionText = strings.TrimSpace(sessionText + "\n\n" + strings.Join(imageNotes, "\n"))
	}
	prepared.SessionText = sessionText
	return prepared, nil
}

func normalizeAttachment(att bus.Attachment, index int) bus.Attachment {
	att.Name = strings.TrimSpace(att.Name)
	att.MIMEType = strings.TrimSpace(att.MIMEType)
	att.URL = strings.TrimSpace(att.URL)
	att.LocalPath = strings.TrimSpace(att.LocalPath)
	att.Kind = strings.TrimSpace(att.Kind)
	if att.Kind == "" {
		att.Kind = bus.InferAttachmentKind(att.MIMEType)
	}
	if att.Name == "" {
		if att.LocalPath != "" {
			att.Name = filepath.Base(att.LocalPath)
		} else if att.URL != "" {
			if u, err := url.Parse(att.URL); err == nil {
				if base := filepath.Base(strings.TrimSpace(u.Path)); base != "" && base != "/" && base != "." {
					att.Name = base
				}
			}
		}
	}
	if att.Name == "" {
		att.Name = fmt.Sprintf("attachment-%d", index)
	}
	return att
}

func buildAttachmentSection(ctx context.Context, att bus.Attachment, cfg config.MediaToolsConfig) string {
	header := fmt.Sprintf("[Attachment] %s", att.Name)
	if strings.TrimSpace(att.MIMEType) != "" {
		header += fmt.Sprintf(" (%s)", strings.TrimSpace(att.MIMEType))
	}

	if !isTextCandidate(att) {
		return header
	}

	data, _, err := readAttachmentBytes(ctx, att, cfg.MaxFileBytes, cfg.DownloadTimeoutSec)
	if err != nil || len(data) == 0 {
		return header
	}
	text, ok := extractText(data, cfg.MaxTextChars)
	if !ok || strings.TrimSpace(text) == "" {
		return header
	}
	return header + "\n" + text
}

func isTextCandidate(att bus.Attachment) bool {
	mimeType := strings.ToLower(strings.TrimSpace(att.MIMEType))
	if strings.HasPrefix(mimeType, "text/") {
		return true
	}
	switch mimeType {
	case "application/json", "application/xml", "text/xml", "application/yaml", "text/yaml", "application/x-yaml", "application/javascript", "text/javascript", "application/csv", "text/csv":
		return true
	}
	ext := strings.ToLower(filepath.Ext(att.Name))
	switch ext {
	case ".txt", ".md", ".markdown", ".json", ".yaml", ".yml", ".xml", ".csv", ".tsv", ".log", ".ini", ".cfg", ".conf", ".go", ".js", ".ts", ".py", ".java", ".rs", ".sh":
		return true
	default:
		return false
	}
}

func extractText(data []byte, maxChars int) (string, bool) {
	if len(data) == 0 {
		return "", false
	}
	if maxChars <= 0 {
		maxChars = config.DefaultMediaMaxTextChars
	}
	text := string(data)
	if !utf8.ValidString(text) {
		return "", false
	}
	if len(text) > maxChars {
		text = text[:maxChars] + "\n(truncated)"
	}
	return strings.TrimSpace(text), true
}

func readAttachmentBytes(ctx context.Context, att bus.Attachment, maxBytes int64, timeoutSec int) ([]byte, string, error) {
	if maxBytes <= 0 {
		maxBytes = config.DefaultMediaMaxFileBytes
	}
	if timeoutSec <= 0 {
		timeoutSec = config.DefaultMediaDownloadTimeoutSec
	}
	if att.LocalPath != "" {
		st, err := os.Stat(att.LocalPath)
		if err != nil {
			return nil, "", err
		}
		if st.Size() > maxBytes {
			return nil, "", fmt.Errorf("attachment too large: %d > %d", st.Size(), maxBytes)
		}
		b, err := os.ReadFile(att.LocalPath)
		if err != nil {
			return nil, "", err
		}
		mimeType := strings.TrimSpace(att.MIMEType)
		if mimeType == "" {
			mimeType = http.DetectContentType(b)
		}
		return b, mimeType, nil
	}
	if att.URL == "" {
		return nil, "", fmt.Errorf("attachment source is empty")
	}

	u, err := url.Parse(att.URL)
	if err != nil {
		return nil, "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, "", fmt.Errorf("unsupported attachment scheme: %s", u.Scheme)
	}

	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, att.URL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "clawlet/0.1")
	for k, v := range att.Headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: time.Duration(timeoutSec) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("attachment http %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > maxBytes {
		return nil, "", fmt.Errorf("attachment too large: > %d", maxBytes)
	}
	mimeType := strings.TrimSpace(att.MIMEType)
	if mimeType == "" {
		mimeType = strings.TrimSpace(resp.Header.Get("Content-Type"))
		if before, _, ok := strings.Cut(mimeType, ";"); ok {
			mimeType = strings.TrimSpace(before)
		}
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(body)
	}
	if clHeader := strings.TrimSpace(resp.Header.Get("Content-Length")); clHeader != "" {
		if n, err := strconv.ParseInt(clHeader, 10, 64); err == nil && n > maxBytes {
			return nil, "", fmt.Errorf("attachment too large: %d > %d", n, maxBytes)
		}
	}
	return body, mimeType, nil
}
