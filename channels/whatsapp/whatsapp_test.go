package whatsapp

import (
	"context"
	"errors"
	"testing"

	"github.com/mosaxiv/clawlet/bus"
	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

func TestResolveWhatsAppReplyTarget(t *testing.T) {
	t.Run("prefer delivery reply id", func(t *testing.T) {
		got := resolveWhatsAppReplyTarget(bus.OutboundMessage{
			ReplyTo: "legacy",
			Delivery: bus.Delivery{
				ReplyToID: "typed",
			},
		})
		if got != "typed" {
			t.Fatalf("expected typed, got %q", got)
		}
	})

	t.Run("fallback legacy reply_to", func(t *testing.T) {
		got := resolveWhatsAppReplyTarget(bus.OutboundMessage{
			ReplyTo: "legacy",
		})
		if got != "legacy" {
			t.Fatalf("expected legacy, got %q", got)
		}
	})
}

func TestParseWhatsAppChatID(t *testing.T) {
	t.Run("full jid", func(t *testing.T) {
		jid, err := parseWhatsAppChatID("12345@s.whatsapp.net")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := jid.String(); got != "12345@s.whatsapp.net" {
			t.Fatalf("unexpected jid: %s", got)
		}
	})

	t.Run("phone number", func(t *testing.T) {
		jid, err := parseWhatsAppChatID("+1 555-123-4567")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := jid.String(); got != "15551234567@s.whatsapp.net" {
			t.Fatalf("unexpected jid: %s", got)
		}
	})
}

func TestBuildOutboundMessage(t *testing.T) {
	t.Run("normal text", func(t *testing.T) {
		msg := buildOutboundMessage("hello", "")
		if msg.GetConversation() != "hello" {
			t.Fatalf("unexpected conversation: %q", msg.GetConversation())
		}
	})

	t.Run("reply text", func(t *testing.T) {
		msg := buildOutboundMessage("hello", "wamid.1")
		if msg.GetExtendedTextMessage() == nil {
			t.Fatal("expected extended text message")
		}
		if msg.GetExtendedTextMessage().GetContextInfo().GetStanzaID() != "wamid.1" {
			t.Fatalf("unexpected stanza id: %q", msg.GetExtendedTextMessage().GetContextInfo().GetStanzaID())
		}
	})
}

func TestWhatsAppMessageContent(t *testing.T) {
	t.Run("conversation", func(t *testing.T) {
		msg := &waE2E.Message{Conversation: proto.String("hi")}
		if got := whatsappMessageContent(msg); got != "hi" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("document fallback", func(t *testing.T) {
		msg := &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("a.pdf")}}
		if got := whatsappMessageContent(msg); got != "[Document] a.pdf" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("reaction", func(t *testing.T) {
		msg := &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Text: proto.String("👍")}}
		if got := whatsappMessageContent(msg); got != "[Reaction] 👍" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestWhatsAppReplyToID(t *testing.T) {
	msg := &waE2E.Message{
		ExtendedTextMessage: &waE2E.ExtendedTextMessage{
			Text: proto.String("hello"),
			ContextInfo: &waE2E.ContextInfo{
				StanzaID: proto.String("wamid.prev"),
			},
		},
	}
	if got := whatsappReplyToID(msg); got != "wamid.prev" {
		t.Fatalf("unexpected reply id: %q", got)
	}
}

func TestShouldRetryWhatsAppSend(t *testing.T) {
	t.Run("retry on rate limit", func(t *testing.T) {
		retry, wait := shouldRetryWhatsAppSend(whatsmeow.ErrIQRateOverLimit, 1)
		if !retry || wait <= 0 {
			t.Fatalf("expected retry, got retry=%v wait=%v", retry, wait)
		}
	})

	t.Run("no retry on context cancel", func(t *testing.T) {
		retry, wait := shouldRetryWhatsAppSend(context.Canceled, 1)
		if retry || wait != 0 {
			t.Fatalf("expected no retry, got retry=%v wait=%v", retry, wait)
		}
	})

	t.Run("no retry on generic error", func(t *testing.T) {
		retry, wait := shouldRetryWhatsAppSend(errors.New("bad request"), 1)
		if retry || wait != 0 {
			t.Fatalf("expected no retry, got retry=%v wait=%v", retry, wait)
		}
	})
}

func TestWhatsAppSenderID(t *testing.T) {
	info := types.MessageInfo{}
	info.Sender = types.NewJID("15551234567", types.DefaultUserServer)
	info.SenderAlt = types.NewJID("15559876543", types.DefaultUserServer)

	got := whatsappSenderID(info)
	if got != "15551234567|15551234567@s.whatsapp.net|15559876543" {
		t.Fatalf("unexpected sender id: %q", got)
	}
}
