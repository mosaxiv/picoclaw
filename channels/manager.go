package channels

import (
	"context"
	"fmt"
	"sync"

	"github.com/mosaxiv/clawlet/bus"
)

type Manager struct {
	bus      *bus.Bus
	channels map[string]Channel

	mu       sync.RWMutex
	running  bool
	stopOnce sync.Once
}

func NewManager(b *bus.Bus) *Manager {
	return &Manager{
		bus:      b,
		channels: map[string]Channel{},
	}
}

func (m *Manager) Add(ch Channel) {
	if ch == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[ch.Name()] = ch
}

func (m *Manager) StartAll(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return nil
	}
	m.running = true

	chs := make([]Channel, 0, len(m.channels))
	for _, ch := range m.channels {
		chs = append(chs, ch)
	}
	m.mu.Unlock()

	// Start outbound dispatcher
	go m.dispatchOutbound(ctx)

	// Start channels
	for _, ch := range chs {
		go func() {
			_ = ch.Start(ctx)
		}()
	}
	return nil
}

func (m *Manager) StopAll() error {
	m.stopOnce.Do(func() {
		m.mu.Lock()
		m.running = false
		chs := make([]Channel, 0, len(m.channels))
		for _, ch := range m.channels {
			chs = append(chs, ch)
		}
		m.mu.Unlock()

		for _, ch := range chs {
			_ = ch.Stop()
		}
	})
	return nil
}

func (m *Manager) Status() map[string]map[string]any {
	out := map[string]map[string]any{}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for name, ch := range m.channels {
		out[name] = map[string]any{
			"running": ch.IsRunning(),
		}
	}
	return out
}

func (m *Manager) dispatchOutbound(ctx context.Context) {
	for {
		msg, err := m.bus.ConsumeOutbound(ctx)
		if err != nil {
			return
		}
		m.mu.RLock()
		ch := m.channels[msg.Channel]
		m.mu.RUnlock()
		if ch == nil {
			// Unknown channel; drop.
			continue
		}
		_ = ch.Send(ctx, msg)
	}
}

func (m *Manager) Require(name string) (Channel, error) {
	m.mu.RLock()
	ch := m.channels[name]
	m.mu.RUnlock()
	if ch == nil {
		return nil, fmt.Errorf("channel not found: %s", name)
	}
	return ch, nil
}
