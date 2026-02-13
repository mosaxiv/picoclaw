package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mdp/qrterminal/v3"
	"github.com/mosaxiv/clawlet/bus"
	"github.com/mosaxiv/clawlet/channels"
	"github.com/mosaxiv/clawlet/config"
	"go.mau.fi/whatsmeow"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	_ "modernc.org/sqlite"
)

type Channel struct {
	cfg   config.WhatsAppConfig
	bus   *bus.Bus
	allow channels.AllowList

	running atomic.Bool

	mu     sync.Mutex
	cancel context.CancelFunc
	wa     *whatsmeow.Client
	db     *sqlstore.Container
}

func New(cfg config.WhatsAppConfig, b *bus.Bus) *Channel {
	return &Channel{
		cfg:   cfg,
		bus:   b,
		allow: channels.AllowList{AllowFrom: cfg.AllowFrom},
	}
}

func (c *Channel) Name() string    { return "whatsapp" }
func (c *Channel) IsRunning() bool { return c.running.Load() }

func (c *Channel) Start(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	db, wa, err := newRuntimeClient(runCtx)
	if err != nil {
		return err
	}
	wa.EnableAutoReconnect = true
	wa.AddEventHandler(c.handleEvent)

	c.mu.Lock()
	c.cancel = cancel
	c.db = db
	c.wa = wa
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.wa == wa {
			c.wa = nil
		}
		if c.db == db {
			c.db = nil
		}
		c.cancel = nil
		c.mu.Unlock()
		wa.Disconnect()
		_ = db.Close()
	}()

	var qrChan <-chan whatsmeow.QRChannelItem
	if wa.Store.ID == nil {
		qrChan, err = wa.GetQRChannel(runCtx)
		if err != nil {
			return err
		}
		go consumeWhatsAppQR(runCtx, qrChan)
	}

	if err := wa.Connect(); err != nil {
		return err
	}

	c.running.Store(true)
	defer c.running.Store(false)

	<-runCtx.Done()
	return runCtx.Err()
}

func (c *Channel) Stop() error {
	c.mu.Lock()
	cancel := c.cancel
	wa := c.wa
	db := c.db
	c.cancel = nil
	c.wa = nil
	c.db = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if wa != nil {
		wa.Disconnect()
	}
	if db != nil {
		return db.Close()
	}
	return nil
}

func (c *Channel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	to, err := parseWhatsAppChatID(msg.ChatID)
	if err != nil {
		return err
	}
	text := strings.TrimSpace(msg.Content)
	if text == "" {
		return nil
	}

	c.mu.Lock()
	wa := c.wa
	c.mu.Unlock()
	if wa == nil {
		return fmt.Errorf("whatsapp not connected")
	}

	payload := buildOutboundMessage(text, resolveWhatsAppReplyTarget(msg))

	const maxAttempts = 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		_, err = wa.SendMessage(ctx, to, payload)
		if err == nil {
			return nil
		}
		retry, wait := shouldRetryWhatsAppSend(err, attempt)
		if !retry || attempt == maxAttempts {
			return err
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

func (c *Channel) handleEvent(raw any) {
	switch evt := raw.(type) {
	case *events.Message:
		c.handleIncomingMessage(evt)
	case *events.LoggedOut:
		log.Printf("whatsapp: logged out")
	case *events.Connected:
		log.Printf("whatsapp: connected")
	case *events.Disconnected:
		log.Printf("whatsapp: disconnected")
	}
}

func (c *Channel) handleIncomingMessage(evt *events.Message) {
	if evt == nil || evt.Message == nil {
		return
	}
	if evt.Info.IsFromMe {
		return
	}

	senderID := whatsappSenderID(evt.Info)
	if !c.allow.Allowed(senderID) {
		return
	}

	content := whatsappMessageContent(evt.Message)
	if content == "" {
		return
	}

	chatID := evt.Info.Chat.String()
	delivery := bus.Delivery{
		MessageID: strings.TrimSpace(evt.Info.ID),
		IsDirect:  !evt.Info.IsGroup,
	}
	if replyToID := whatsappReplyToID(evt.Message); replyToID != "" {
		delivery.ReplyToID = replyToID
	}

	publishCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = c.bus.PublishInbound(publishCtx, bus.InboundMessage{
		Channel:    "whatsapp",
		SenderID:   senderID,
		ChatID:     chatID,
		Content:    content,
		SessionKey: "whatsapp:" + chatID,
		Delivery:   delivery,
	})
	cancel()
}

func newRuntimeClient(ctx context.Context) (*sqlstore.Container, *whatsmeow.Client, error) {
	// Non-persistent runtime DB (session is not persisted by design).
	dsn := "file:clawlet-whatsapp-runtime?mode=memory&cache=shared&_pragma=foreign_keys(1)"
	db, err := sqlstore.New(ctx, "sqlite", dsn, waLog.Noop)
	if err != nil {
		return nil, nil, err
	}
	store, err := db.GetFirstDevice(ctx)
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	wa := whatsmeow.NewClient(store, waLog.Noop)
	return db, wa, nil
}

func consumeWhatsAppQR(ctx context.Context, ch <-chan whatsmeow.QRChannelItem) {
	for {
		select {
		case <-ctx.Done():
			return
		case item, ok := <-ch:
			if !ok {
				return
			}
			if item.Event == whatsmeow.QRChannelEventCode {
				log.Printf("whatsapp: scan QR code with Linked Devices")
				qrterminal.GenerateHalfBlock(item.Code, qrterminal.L, os.Stdout)
				continue
			}
			if item.Event == whatsmeow.QRChannelEventError {
				log.Printf("whatsapp: qr error: %v", item.Error)
				continue
			}
			log.Printf("whatsapp: qr event: %s", item.Event)
		}
	}
}

func parseWhatsAppChatID(v string) (types.JID, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return types.EmptyJID, fmt.Errorf("chat_id is empty")
	}
	if strings.Contains(v, "@") {
		jid, err := types.ParseJID(v)
		if err != nil {
			return types.EmptyJID, err
		}
		return jid, nil
	}
	phone := normalizePhone(v)
	if phone == "" {
		return types.EmptyJID, fmt.Errorf("chat_id is invalid: %q", v)
	}
	return types.NewJID(phone, types.DefaultUserServer), nil
}

func normalizePhone(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "+")
	var b strings.Builder
	for _, r := range v {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func buildOutboundMessage(text, replyToID string) *waE2E.Message {
	if strings.TrimSpace(replyToID) != "" {
		return &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text: proto.String(text),
				ContextInfo: &waE2E.ContextInfo{
					StanzaID: proto.String(strings.TrimSpace(replyToID)),
				},
			},
		}
	}
	return &waE2E.Message{Conversation: proto.String(text)}
}

func resolveWhatsAppReplyTarget(msg bus.OutboundMessage) string {
	candidates := []string{
		strings.TrimSpace(msg.Delivery.ReplyToID),
		strings.TrimSpace(msg.ReplyTo),
	}
	for _, v := range candidates {
		if v != "" {
			return v
		}
	}
	return ""
}

func shouldRetryWhatsAppSend(err error, attempt int) (bool, time.Duration) {
	if err == nil {
		return false, 0
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false, 0
	}
	if errors.Is(err, whatsmeow.ErrIQRateOverLimit) ||
		errors.Is(err, whatsmeow.ErrIQInternalServerError) ||
		errors.Is(err, whatsmeow.ErrIQServiceUnavailable) ||
		errors.Is(err, whatsmeow.ErrIQPartialServerError) ||
		errors.Is(err, whatsmeow.ErrMessageTimedOut) ||
		errors.Is(err, whatsmeow.ErrNotConnected) {
		return true, whatsappSendBackoff(attempt)
	}

	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary()) {
		return true, whatsappSendBackoff(attempt)
	}
	return false, 0
}

func whatsappSendBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	shift := min(attempt-1, 4)
	return 300 * time.Millisecond * time.Duration(1<<shift)
}

func whatsappSenderID(info types.MessageInfo) string {
	parts := make([]string, 0, 3)
	if v := strings.TrimSpace(info.Sender.User); v != "" {
		parts = append(parts, v)
	}
	if v := strings.TrimSpace(info.Sender.ToNonAD().String()); v != "" && !contains(parts, v) {
		parts = append(parts, v)
	}
	if v := strings.TrimSpace(info.SenderAlt.User); v != "" && !contains(parts, v) {
		parts = append(parts, v)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "|")
}

func whatsappMessageContent(msg *waE2E.Message) string {
	if msg == nil {
		return ""
	}
	if v := strings.TrimSpace(msg.GetConversation()); v != "" {
		return v
	}
	if ext := msg.GetExtendedTextMessage(); ext != nil {
		if v := strings.TrimSpace(ext.GetText()); v != "" {
			return v
		}
	}
	if image := msg.GetImageMessage(); image != nil {
		if caption := strings.TrimSpace(image.GetCaption()); caption != "" {
			return "[Image] " + caption
		}
		return "[Image]"
	}
	if video := msg.GetVideoMessage(); video != nil {
		if caption := strings.TrimSpace(video.GetCaption()); caption != "" {
			return "[Video] " + caption
		}
		return "[Video]"
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		if caption := strings.TrimSpace(doc.GetCaption()); caption != "" {
			return "[Document] " + caption
		}
		if name := strings.TrimSpace(doc.GetFileName()); name != "" {
			return "[Document] " + name
		}
		return "[Document]"
	}
	if msg.GetAudioMessage() != nil {
		return "[Voice Message]"
	}
	if react := msg.GetReactionMessage(); react != nil {
		if emoji := strings.TrimSpace(react.GetText()); emoji != "" {
			return "[Reaction] " + emoji
		}
		return "[Reaction]"
	}
	return ""
}

func whatsappReplyToID(msg *waE2E.Message) string {
	if msg == nil {
		return ""
	}
	if ext := msg.GetExtendedTextMessage(); ext != nil {
		if ctx := ext.GetContextInfo(); ctx != nil {
			return strings.TrimSpace(ctx.GetStanzaID())
		}
	}
	return ""
}

func contains(vals []string, v string) bool {
	for _, existing := range vals {
		if existing == v {
			return true
		}
	}
	return false
}
