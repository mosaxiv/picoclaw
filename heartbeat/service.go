package heartbeat

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

const (
	DefaultIntervalSec = 30 * 60

	DefaultPrompt = "Read HEARTBEAT.md in your workspace (if it exists).\n" +
		"Follow any instructions or tasks listed there.\n" +
		"If nothing needs attention, reply with just: HEARTBEAT_OK"

	OKToken = "HEARTBEAT_OK"
)

type Service struct {
	workspace string
	onBeat    func(ctx context.Context, prompt string) (string, error)

	enabled   bool
	interval  time.Duration
	running   atomic.Bool
	inFlight  atomic.Bool
	stopCh    chan struct{}
	stoppedCh chan struct{}
}

type Options struct {
	Enabled     bool
	IntervalSec int
	OnHeartbeat func(ctx context.Context, prompt string) (string, error)
}

func New(workspace string, opts Options) *Service {
	sec := opts.IntervalSec
	if sec <= 0 {
		sec = DefaultIntervalSec
	}
	return &Service{
		workspace: workspace,
		onBeat:    opts.OnHeartbeat,
		enabled:   opts.Enabled,
		interval:  time.Duration(sec) * time.Second,
		stopCh:    make(chan struct{}),
		stoppedCh: make(chan struct{}),
	}
}

func (s *Service) Start(ctx context.Context) {
	if !s.enabled || s.onBeat == nil {
		return
	}
	if s.running.Swap(true) {
		return
	}
	go s.loop(ctx)
}

func (s *Service) Stop() {
	if !s.running.Swap(false) {
		return
	}
	close(s.stopCh)
	<-s.stoppedCh
}

func (s *Service) TriggerNow(ctx context.Context) (string, error) {
	if s.onBeat == nil {
		return "", nil
	}
	return s.onBeat(ctx, DefaultPrompt)
}

func (s *Service) loop(ctx context.Context) {
	defer close(s.stoppedCh)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

func (s *Service) tick(ctx context.Context) {
	// Ensure only one tick runs at a time.
	if !s.inFlight.CompareAndSwap(false, true) {
		return
	}
	defer s.inFlight.Store(false)

	content := s.readHeartbeatFile()
	if isEmpty(content) {
		return
	}
	resp, err := s.onBeat(ctx, DefaultPrompt)
	if err != nil {
		log.Printf("heartbeat: error: %v", err)
		return
	}
	if isHeartbeatOK(resp) {
		return
	}
	if strings.TrimSpace(resp) != "" {
		log.Printf("heartbeat: response: %s", truncateForLog(resp, 400))
	}
}

func (s *Service) readHeartbeatFile() string {
	p := filepath.Join(s.workspace, "HEARTBEAT.md")
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func isEmpty(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return true
	}
	skip := map[string]bool{
		"- [ ]": true,
		"* [ ]": true,
		"- [x]": true,
		"* [x]": true,
	}
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "<!--") {
			continue
		}
		if skip[line] {
			continue
		}
		return false
	}
	return true
}

func isHeartbeatOK(resp string) bool {
	// Tolerate case/underscore/whitespace variations.
	return strings.Contains(normalizeToken(resp), normalizeToken(OKToken))
}

func truncateForLog(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 {
		max = 200
	}
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

func normalizeToken(s string) string {
	s = strings.ToUpper(s)
	// Keep only A-Z and 0-9 to ignore punctuation/whitespace/underscores.
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(r)
			continue
		}
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
