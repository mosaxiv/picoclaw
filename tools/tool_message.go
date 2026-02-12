package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mosaxiv/clawlet/bus"
)

func (r *Registry) message(ctx context.Context, channel, chatID, content string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", errors.New("content is empty")
	}
	if strings.TrimSpace(channel) == "" || strings.TrimSpace(chatID) == "" {
		return "", errors.New("no target channel/chat_id")
	}
	if r.Outbound == nil {
		return "", errors.New("message sending not configured")
	}
	msg := bus.OutboundMessage{Channel: channel, ChatID: chatID, Content: content}
	if err := r.Outbound(ctx, msg); err != nil {
		return "", err
	}
	return fmt.Sprintf("Message sent to %s:%s", channel, chatID), nil
}
