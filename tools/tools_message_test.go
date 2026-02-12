package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mosaxiv/clawlet/bus"
)

func TestMessageRequiresExplicitTarget(t *testing.T) {
	r := &Registry{
		Outbound: func(ctx context.Context, msg bus.OutboundMessage) error { return nil },
	}
	_, err := r.Execute(context.Background(), Context{Channel: "discord", ChatID: "123"}, "message", json.RawMessage(`{"content":"hi"}`))
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestMessageRejectsCurrentSessionTarget(t *testing.T) {
	r := &Registry{
		Outbound: func(ctx context.Context, msg bus.OutboundMessage) error { return nil },
	}
	_, err := r.Execute(
		context.Background(),
		Context{Channel: "discord", ChatID: "123"},
		"message",
		json.RawMessage(`{"content":"hi","channel":"discord","chat_id":"123"}`),
	)
	if err == nil {
		t.Fatalf("expected error")
	}
}
