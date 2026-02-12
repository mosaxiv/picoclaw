package discord

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/mosaxiv/clawlet/bus"
	"github.com/mosaxiv/clawlet/channels"
	"github.com/mosaxiv/clawlet/config"
)

type Channel struct {
	cfg   config.DiscordConfig
	bus   *bus.Bus
	allow channels.AllowList

	running atomic.Bool

	mu  sync.Mutex
	dg  *discordgo.Session
	hc  *http.Client
	ctx context.Context
}

func New(cfg config.DiscordConfig, b *bus.Bus) *Channel {
	return &Channel{
		cfg:   cfg,
		bus:   b,
		allow: channels.AllowList{AllowFrom: cfg.AllowFrom},
		hc: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (c *Channel) Name() string    { return "discord" }
func (c *Channel) IsRunning() bool { return c.running.Load() }

func (c *Channel) Start(ctx context.Context) error {
	if strings.TrimSpace(c.cfg.Token) == "" {
		return fmt.Errorf("discord token is empty")
	}

	dg, err := discordgo.New("Bot " + strings.TrimSpace(c.cfg.Token))
	if err != nil {
		return err
	}
	// Keep operations bounded; discordgo doesn't take context in most calls.
	dg.Client = c.hc

	if c.cfg.Intents != 0 {
		dg.Identify.Intents = discordgo.Intent(c.cfg.Intents)
	}
	dg.AddHandler(c.onMessageCreate)

	c.mu.Lock()
	c.dg = dg
	c.ctx = ctx
	c.mu.Unlock()

	c.running.Store(true)
	defer c.running.Store(false)
	defer func() {
		_ = dg.Close()
		c.mu.Lock()
		if c.dg == dg {
			c.dg = nil
		}
		c.mu.Unlock()
	}()

	if err := dg.Open(); err != nil {
		return err
	}

	<-ctx.Done()
	return ctx.Err()
}

func (c *Channel) Stop() error {
	c.mu.Lock()
	dg := c.dg
	c.dg = nil
	c.mu.Unlock()
	if dg != nil {
		return dg.Close()
	}
	return nil
}

func (c *Channel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	chID := strings.TrimSpace(msg.ChatID)
	if chID == "" {
		return fmt.Errorf("chat_id is empty")
	}
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		return nil
	}

	c.mu.Lock()
	dg := c.dg
	c.mu.Unlock()
	if dg == nil {
		return fmt.Errorf("discord not connected")
	}

	// Best-effort cancellation: discordgo doesn't propagate ctx. We at least
	// fail fast if ctx is already cancelled.
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	_, err := dg.ChannelMessageSend(chID, content)
	return err
}

func (c *Channel) onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m == nil || m.Author == nil {
		return
	}
	if m.Author.Bot {
		return
	}
	if !c.allow.Allowed(m.Author.ID) {
		return
	}
	chID := strings.TrimSpace(m.ChannelID)
	content := strings.TrimSpace(m.Content)
	if chID == "" || content == "" {
		return
	}

	ctx := context.Background()
	c.mu.Lock()
	if c.ctx != nil {
		ctx = c.ctx
	}
	c.mu.Unlock()

	_ = c.bus.PublishInbound(ctx, bus.InboundMessage{
		Channel:    "discord",
		SenderID:   m.Author.ID,
		ChatID:     chID,
		Content:    content,
		SessionKey: "discord:" + chID,
	})
}
