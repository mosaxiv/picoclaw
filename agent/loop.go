package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mosaxiv/clawlet/bus"
	"github.com/mosaxiv/clawlet/config"
	"github.com/mosaxiv/clawlet/cron"
	"github.com/mosaxiv/clawlet/llm"
	"github.com/mosaxiv/clawlet/memory"
	"github.com/mosaxiv/clawlet/session"
	"github.com/mosaxiv/clawlet/skills"
	"github.com/mosaxiv/clawlet/tools"
)

type Loop struct {
	cfg          *config.Config
	workspace    string
	model        string
	maxIters     int
	memoryWindow int

	bus      *bus.Bus
	sessions *session.Manager
	skills   *skills.Loader

	llm   *llm.Client
	tools *tools.Registry

	cron *cron.Service

	verbose bool
}

type LoopOptions struct {
	Config       *config.Config
	WorkspaceDir string
	Model        string
	MaxIters     int
	Bus          *bus.Bus
	Sessions     *session.Manager
	Skills       *skills.Loader
	Cron         *cron.Service
	Spawn        func(ctx context.Context, task, label, originChannel, originChatID string) (string, error)
	Verbose      bool
}

func NewLoop(opts LoopOptions) (*Loop, error) {
	if opts.Config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if opts.Bus == nil {
		return nil, fmt.Errorf("bus is nil")
	}
	if strings.TrimSpace(opts.WorkspaceDir) == "" {
		return nil, fmt.Errorf("workspace is empty")
	}
	ws, err := filepath.Abs(opts.WorkspaceDir)
	if err != nil {
		return nil, err
	}
	if opts.MaxIters <= 0 {
		opts.MaxIters = 20
	}
	memoryWindow := opts.Config.Agents.Defaults.MemoryWindowValue()
	model := opts.Model
	if strings.TrimSpace(model) == "" {
		model = opts.Config.LLM.Model
	}

	smgr := opts.Sessions
	if smgr == nil {
		return nil, fmt.Errorf("sessions manager is nil")
	}
	sloader := opts.Skills
	if sloader == nil {
		sloader = skills.New(ws)
	}

	client := &llm.Client{
		Provider:    opts.Config.LLM.Provider,
		BaseURL:     opts.Config.LLM.BaseURL,
		APIKey:      opts.Config.LLM.APIKey,
		Model:       model,
		MaxTokens:   opts.Config.Agents.Defaults.MaxTokensValue(),
		Temperature: opts.Config.Agents.Defaults.Temperature,
		Headers:     opts.Config.LLM.Headers,
	}

	treg := &tools.Registry{
		WorkspaceDir:        ws,
		RestrictToWorkspace: opts.Config.Tools.RestrictToWorkspaceValue(),
		ExecTimeout:         time.Duration(opts.Config.Tools.Exec.TimeoutSec) * time.Second,
		BraveAPIKey:         opts.Config.Tools.Web.BraveAPIKey,
		Outbound: func(ctx context.Context, msg bus.OutboundMessage) error {
			return opts.Bus.PublishOutbound(ctx, msg)
		},
		Spawn: opts.Spawn,
		Cron:  opts.Cron,
		ReadSkill: func(name string) (string, bool) {
			if sloader == nil {
				return "", false
			}
			return sloader.Load(name)
		},
	}

	return &Loop{
		cfg:          opts.Config,
		workspace:    ws,
		model:        model,
		maxIters:     opts.MaxIters,
		memoryWindow: memoryWindow,
		bus:          opts.Bus,
		sessions:     smgr,
		skills:       sloader,
		llm:          client,
		tools:        treg,
		cron:         opts.Cron,
		verbose:      opts.Verbose,
	}, nil
}

func (l *Loop) SetSpawn(fn func(ctx context.Context, task, label, originChannel, originChatID string) (string, error)) {
	if l == nil || l.tools == nil {
		return
	}
	l.tools.Spawn = fn
}

func (l *Loop) Run(ctx context.Context) error {
	for {
		msg, err := l.bus.ConsumeInbound(ctx)
		if err != nil {
			return err
		}
		out, omsg, err := l.processInbound(ctx, msg)
		_ = out
		if err != nil {
			// Best-effort error reply
			if omsg.Channel != "" && omsg.ChatID != "" {
				omsg.Content = "error: " + err.Error()
				_ = l.bus.PublishOutbound(ctx, omsg)
			}
			continue
		}
		if omsg.Channel != "" && omsg.ChatID != "" && strings.TrimSpace(omsg.Content) != "" {
			_ = l.bus.PublishOutbound(ctx, omsg)
		}
	}
}

func (l *Loop) ProcessDirect(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	return l.processDirect(ctx, content, sessionKey, channel, chatID)
}

func (l *Loop) processInbound(ctx context.Context, msg bus.InboundMessage) (string, bus.OutboundMessage, error) {
	// System message is used by subagents to announce back to origin.
	if msg.Channel == "system" {
		originCh, originChat := parseOrigin(msg.ChatID)
		if originCh == "" || originChat == "" {
			originCh = "cli"
			originChat = msg.ChatID
		}
		// Route response back to origin session.
		sk := originCh + ":" + originChat
		res, err := l.processDirect(ctx, msg.Content, sk, originCh, originChat)
		return res, bus.OutboundMessage{Channel: originCh, ChatID: originChat, Content: res}, err
	}

	sessionKey := msg.SessionKey
	if strings.TrimSpace(sessionKey) == "" {
		sessionKey = msg.Channel + ":" + msg.ChatID
	}
	res, err := l.processDirect(ctx, msg.Content, sessionKey, msg.Channel, msg.ChatID)
	return res, bus.OutboundMessage{
		Channel:  msg.Channel,
		ChatID:   msg.ChatID,
		Content:  res,
		Delivery: msg.Delivery,
	}, err
}

func (l *Loop) processDirect(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	sess, err := l.sessions.GetOrCreate(sessionKey)
	if err != nil {
		return "", err
	}
	if done, err := maybeConsolidateSession(ctx, l.workspace, sess, l.memoryWindow, func(ctx context.Context, currentMemory, conversation string) (string, string, error) {
		return summarizeConsolidationWithLLM(ctx, l.llm, currentMemory, conversation)
	}); err == nil && done {
		_ = l.sessions.Save(sess)
	}

	history := sess.History(l.memoryWindow)
	messages := make([]llm.Message, 0, 1+len(history)+1)
	system := l.buildSystemPrompt(channel, chatID)
	messages = append(messages, llm.Message{Role: "system", Content: system})
	for _, m := range history {
		messages = append(messages, llm.Message{Role: m.Role, Content: m.Content})
	}
	messages = append(messages, llm.Message{Role: "user", Content: content})

	toolsDefs := l.tools.Definitions()

	var final string
	for iter := 0; iter < l.maxIters; iter++ {
		res, err := l.llm.Chat(ctx, messages, toolsDefs)
		if err != nil {
			return "", err
		}
		if res.HasToolCalls() {
			messages = appendToolRound(messages, res.Content, res.ToolCalls, func(tc llm.ToolCall) string {
				out, err := l.tools.Execute(ctx, tools.Context{
					Channel:    channel,
					ChatID:     chatID,
					SessionKey: sessionKey,
				}, tc.Name, tc.Arguments)
				if err != nil {
					return "error: " + err.Error()
				}
				return out
			})
			continue
		}
		final = res.Content
		break
	}
	if strings.TrimSpace(final) == "" {
		final = "(no response)"
	}

	sess.Add("user", content)
	sess.Add("assistant", final)
	_ = l.sessions.Save(sess)
	return final, nil
}

func (l *Loop) buildSystemPrompt(channel, chatID string) string {
	// Keep it simple and deterministic. Add progressive skill summary.
	var b strings.Builder
	b.WriteString("# clawlet\n\n")
	b.WriteString("You are clawlet, a helpful AI assistant.\n")
	b.WriteString("You can use tools to read/write/edit files, list directories, execute shell commands, fetch/search the web, schedule tasks, and spawn background subagents.\n\n")
	b.WriteString("IMPORTANT: When replying to the current conversation, respond with plain text. Do not call the message tool.\n")
	b.WriteString("Only use the message tool when you must send to a different channel/chat_id.\n\n")
	b.WriteString("## Current Time\n")
	b.WriteString(time.Now().Format("2006-01-02 15:04 (Mon)") + "\n\n")
	b.WriteString("## Workspace\n")
	b.WriteString(l.workspace + "\n\n")
	if l.cfg.Tools.RestrictToWorkspaceValue() {
		b.WriteString("## Safety\nTools are restricted to the workspace directory.\n\n")
	}
	if channel != "" && chatID != "" {
		b.WriteString("## Current Session\n")
		b.WriteString("Channel: " + channel + "\nChat ID: " + chatID + "\n\n")
	}

	// Bootstrap files from workspace (optional).
	for _, fn := range []string{"AGENTS.md", "SOUL.md", "USER.md", "TOOLS.md", "IDENTITY.md"} {
		p := filepath.Join(l.workspace, fn)
		if bb, err := os.ReadFile(p); err == nil && len(bb) > 0 {
			b.WriteString("## " + fn + "\n\n")
			b.Write(bb)
			if bb[len(bb)-1] != '\n' {
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}

	// Memory (long-term + today's notes)
	mem := memory.New(l.workspace).GetContext()
	if strings.TrimSpace(mem) != "" {
		b.WriteString("# Memory\n\n")
		b.WriteString(mem)
		b.WriteString("\n\n")
	}

	// Skills summary (progressive loading).
	if l.skills != nil {
		sum := l.skills.SummaryXML()
		if sum != "" {
			b.WriteString("# Skills\n\n")
			b.WriteString("To use a skill:\n- workspace skills: read_file(path)\n- bundled skills: read_skill(name)\n\n")
			b.WriteString(sum + "\n\n")
		}
	}

	return b.String()
}

func parseOrigin(chatID string) (string, string) {
	if before, after, ok := strings.Cut(chatID, ":"); ok {
		return before, after
	}
	return "", ""
}
