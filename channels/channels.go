package channels

import (
	"context"
	"slices"
	"strings"

	"github.com/mosaxiv/clawlet/bus"
)

type Channel interface {
	Name() string
	Start(ctx context.Context) error
	Stop() error
	Send(ctx context.Context, msg bus.OutboundMessage) error
	IsRunning() bool
}

type AllowList struct {
	AllowFrom []string
}

func (a AllowList) Allowed(senderID string) bool {
	if len(a.AllowFrom) == 0 {
		return true
	}
	senderID = strings.TrimSpace(senderID)
	if senderID == "" {
		return false
	}
	if slices.Contains(a.AllowFrom, senderID) {
		return true
	}
	// Accept compound IDs (e.g. "a|b")
	if strings.Contains(senderID, "|") {
		parts := strings.SplitSeq(senderID, "|")
		for p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if slices.Contains(a.AllowFrom, p) {
				return true
			}
		}
	}
	return false
}
