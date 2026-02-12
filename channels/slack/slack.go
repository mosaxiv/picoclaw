package slack

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mosaxiv/clawlet/bus"
	"github.com/mosaxiv/clawlet/channels"
	"github.com/mosaxiv/clawlet/config"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

type Channel struct {
	cfg   config.SlackConfig
	bus   *bus.Bus
	allow channels.AllowList

	running atomic.Bool

	mu  sync.Mutex
	api *slack.Client
	sm  *socketmode.Client
	hc  *http.Client

	botUserID string
	cancel    context.CancelFunc
}

func New(cfg config.SlackConfig, b *bus.Bus) *Channel {
	hc := &http.Client{Timeout: 20 * time.Second}
	return &Channel{
		cfg:   cfg,
		bus:   b,
		allow: channels.AllowList{AllowFrom: cfg.AllowFrom},
		hc:    hc,
	}
}

func (c *Channel) Name() string    { return "slack" }
func (c *Channel) IsRunning() bool { return c.running.Load() }

func (c *Channel) Start(ctx context.Context) error {
	if strings.TrimSpace(c.cfg.BotToken) == "" {
		return fmt.Errorf("slack botToken is empty")
	}
	if strings.TrimSpace(c.cfg.AppToken) == "" {
		return fmt.Errorf("slack appToken is empty")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Socket Mode: inbound via WebSocket, no public HTTP endpoint required.
	api := slack.New(
		strings.TrimSpace(c.cfg.BotToken),
		slack.OptionHTTPClient(c.hc),
		slack.OptionAppLevelToken(strings.TrimSpace(c.cfg.AppToken)),
	)
	sm := socketmode.New(api)

	c.mu.Lock()
	c.api = api
	c.sm = sm
	c.cancel = cancel
	c.mu.Unlock()

	// Resolve bot user ID for mention stripping/dedup (best-effort).
	if auth, err := api.AuthTestContext(runCtx); err == nil {
		c.mu.Lock()
		c.botUserID = strings.TrimSpace(auth.UserID)
		c.mu.Unlock()
	}

	c.running.Store(true)
	defer c.running.Store(false)

	go c.runSocketEventLoop(runCtx, sm)
	return sm.RunContext(runCtx)
}

func (c *Channel) Stop() error {
	c.running.Store(false)
	c.mu.Lock()
	cancel := c.cancel
	c.cancel = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

func (c *Channel) handleEvent(ctx context.Context, ev slackevents.EventsAPIEvent) {
	if ev.Type != slackevents.CallbackEvent {
		return
	}
	if ev.InnerEvent.Type != "message" && ev.InnerEvent.Type != "app_mention" {
		return
	}

	switch inner := ev.InnerEvent.Data.(type) {
	case *slackevents.MessageEvent:
		if inner == nil {
			return
		}
		// Ignore bot messages / message_changed etc.
		if strings.TrimSpace(inner.BotID) != "" || strings.TrimSpace(inner.SubType) != "" {
			return
		}
		c.publishInbound(ctx, "message", inner.User, inner.Channel, inner.ChannelType, inner.TimeStamp, inner.Text)
	case *slackevents.AppMentionEvent:
		if inner == nil {
			return
		}
		// Ignore bot-triggered app mentions.
		if strings.TrimSpace(inner.BotID) != "" {
			return
		}
		c.publishInbound(ctx, "app_mention", inner.User, inner.Channel, "", inner.TimeStamp, inner.Text)
	default:
		return
	}
}

func (c *Channel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if strings.TrimSpace(c.cfg.BotToken) == "" {
		return fmt.Errorf("slack botToken is empty")
	}
	if strings.TrimSpace(c.cfg.AppToken) == "" {
		return fmt.Errorf("slack appToken is empty")
	}
	ch := strings.TrimSpace(msg.ChatID)
	if ch == "" {
		return fmt.Errorf("chat_id is empty")
	}
	text := strings.TrimSpace(msg.Content)
	if text == "" {
		return nil
	}
	c.mu.Lock()
	api := c.api
	hc := c.hc
	appTok := c.cfg.AppToken
	botTok := c.cfg.BotToken
	c.mu.Unlock()
	if api == nil {
		api = slack.New(
			strings.TrimSpace(botTok),
			slack.OptionHTTPClient(hc),
			slack.OptionAppLevelToken(strings.TrimSpace(appTok)),
		)
		c.mu.Lock()
		if c.api == nil {
			c.api = api
		} else {
			api = c.api
		}
		c.mu.Unlock()
	}

	_, _, err := api.PostMessageContext(ctx, ch, slack.MsgOptionText(text, false))
	return err
}

func (c *Channel) runSocketEventLoop(ctx context.Context, sm *socketmode.Client) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-sm.Events:
			if !ok {
				return
			}
			if evt.Type != socketmode.EventTypeEventsAPI {
				continue
			}
			eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
			if !ok {
				continue
			}
			// Ack quickly; process later.
			if evt.Request != nil {
				sm.Ack(*evt.Request)
			}
			go c.handleEvent(ctx, eventsAPIEvent)
		}
	}
}

func (c *Channel) publishInbound(ctx context.Context, eventType, user, ch, channelType, ts, text string) {
	user = strings.TrimSpace(user)
	ch = strings.TrimSpace(ch)
	channelType = strings.TrimSpace(channelType)
	ts = strings.TrimSpace(ts)
	text = strings.TrimSpace(text)
	if user == "" || ch == "" || text == "" {
		return
	}
	if !c.allow.Allowed(user) {
		return
	}
	if !c.allowedByPolicy(eventType, ch, channelType, text) {
		return
	}
	text = c.stripBotMention(text)
	if strings.TrimSpace(text) == "" {
		return
	}

	// Best-effort :eyes: reaction (matches nanobot behavior).
	if ts != "" {
		c.mu.Lock()
		api := c.api
		c.mu.Unlock()
		if api != nil {
			_ = api.AddReactionContext(ctx, "eyes", slack.ItemRef{Channel: ch, Timestamp: ts})
		}
	}

	_ = c.bus.PublishInbound(ctx, bus.InboundMessage{
		Channel:    "slack",
		SenderID:   user,
		ChatID:     ch,
		Content:    text,
		SessionKey: "slack:" + ch,
	})
}

func (c *Channel) allowedByPolicy(eventType, chatID, channelType, text string) bool {
	// DM-like channels: always allow (subject to allowFrom).
	if channelType == "im" || channelType == "mpim" {
		if c.cfg.DM != nil && !c.cfg.DM.Enabled {
			return false
		}
		return true
	}

	policy := strings.ToLower(strings.TrimSpace(c.cfg.GroupPolicy))
	if policy == "" {
		policy = "mention"
	}

	// Avoid double-processing: for mentions in channels Slack often sends both `message` and `app_mention`.
	c.mu.Lock()
	botID := strings.TrimSpace(c.botUserID)
	c.mu.Unlock()
	if eventType == "message" && botID != "" && strings.Contains(text, "<@"+botID+">") {
		return false
	}

	switch policy {
	case "open":
		return true
	case "allowlist":
		for _, v := range c.cfg.GroupAllowFrom {
			if strings.TrimSpace(v) == chatID {
				return true
			}
		}
		return false
	case "mention":
		// Respond only to explicit app mentions.
		return eventType == "app_mention"
	default:
		// Fail closed on unknown policy.
		return false
	}
}

func (c *Channel) stripBotMention(text string) string {
	c.mu.Lock()
	botID := c.botUserID
	c.mu.Unlock()
	if botID == "" {
		return text
	}
	text = strings.TrimSpace(text)
	pfx := "<@" + botID + ">"
	if after, ok := strings.CutPrefix(text, pfx); ok {
		text = strings.TrimSpace(after)
		// Common forms: "<@U..>: hi" or "<@U..>, hi"
		text = strings.TrimSpace(strings.TrimPrefix(text, ":"))
		text = strings.TrimSpace(strings.TrimPrefix(text, ","))
	}
	return strings.TrimSpace(text)
}
