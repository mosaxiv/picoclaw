package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mosaxiv/clawlet/config"
	"github.com/mosaxiv/clawlet/llm"
	"github.com/mosaxiv/clawlet/memory"
	"github.com/mosaxiv/clawlet/paths"
	"github.com/mosaxiv/clawlet/session"
	"github.com/mosaxiv/clawlet/skills"
	"github.com/mosaxiv/clawlet/tools"
)

type Options struct {
	Config       *config.Config
	WorkspaceDir string
	SessionKey   string
	MaxIters     int
	Verbose      bool
}

type Agent struct {
	cfg       *config.Config
	workspace string
	maxIters  int
	verbose   bool

	llm   *llm.Client
	tools *tools.Registry

	sessionDir string
	sess       *session.Session
}

func New(opts Options) (*Agent, error) {
	if opts.Config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if strings.TrimSpace(opts.WorkspaceDir) == "" {
		return nil, fmt.Errorf("workspace is empty")
	}
	wsAbs, err := filepath.Abs(opts.WorkspaceDir)
	if err != nil {
		return nil, err
	}
	if opts.MaxIters <= 0 {
		opts.MaxIters = 20
	}
	if err := paths.EnsureStateDirs(); err != nil {
		return nil, err
	}
	sdir := paths.SessionsDir()

	sess, err := session.Load(sdir, opts.SessionKey)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		sess = session.New(opts.SessionKey)
	}

	c := &llm.Client{
		Provider:    opts.Config.LLM.Provider,
		BaseURL:     opts.Config.LLM.BaseURL,
		APIKey:      opts.Config.LLM.APIKey,
		Model:       opts.Config.LLM.Model,
		MaxTokens:   opts.Config.Agents.Defaults.MaxTokensValue(),
		Temperature: opts.Config.Agents.Defaults.Temperature,
		Headers:     opts.Config.LLM.Headers,
	}

	treg := &tools.Registry{
		WorkspaceDir:        wsAbs,
		RestrictToWorkspace: opts.Config.Tools.RestrictToWorkspaceValue(),
		ExecTimeout:         time.Duration(opts.Config.Tools.Exec.TimeoutSec) * time.Second,
		BraveAPIKey:         opts.Config.Tools.Web.BraveAPIKey,
		ReadSkill: func(name string) (string, bool) {
			// CLI agent doesn't have a skills loader; use the embedded loader via workspace.
			l := skills.New(wsAbs)
			return l.Load(name)
		},
	}

	return &Agent{
		cfg:        opts.Config,
		workspace:  wsAbs,
		maxIters:   opts.MaxIters,
		verbose:    opts.Verbose,
		llm:        c,
		tools:      treg,
		sessionDir: sdir,
		sess:       sess,
	}, nil
}

func (a *Agent) Process(ctx context.Context, input string) (string, error) {
	sys := a.systemPrompt()
	messages := make([]llm.Message, 0, 1+len(a.sess.Messages)+1)
	messages = append(messages, llm.Message{Role: "system", Content: sys})
	for _, m := range a.sess.History(50) {
		messages = append(messages, llm.Message{Role: m.Role, Content: m.Content})
	}
	messages = append(messages, llm.Message{Role: "user", Content: input})

	toolsDefs := a.tools.Definitions()

	var final string
	for iter := 0; iter < a.maxIters; iter++ {
		res, err := a.llm.Chat(ctx, messages, toolsDefs)
		if err != nil {
			return "", err
		}

		if res.HasToolCalls() {
			// Add assistant message with tool calls (OpenAI expects arguments as JSON string, but
			// many servers accept object; we keep it string for compatibility).
			tcs := make([]llm.ToolCallPayload, 0, len(res.ToolCalls))
			for _, tc := range res.ToolCalls {
				tcs = append(tcs, llm.ToolCallPayload{
					ID:   tc.ID,
					Type: "function",
					Function: llm.ToolCallPayloadFunc{
						Name:      tc.Name,
						Arguments: string(tc.Arguments),
					},
				})
			}
			messages = append(messages, llm.Message{Role: "assistant", Content: res.Content, ToolCalls: tcs})

			for _, tc := range res.ToolCalls {
				if a.verbose {
					fmt.Fprintf(os.Stderr, "tool: %s %s\n", tc.Name, previewJSON(tc.Arguments, 200))
				}
				out, err := a.tools.Execute(ctx, tools.Context{
					Channel:    "cli",
					ChatID:     "direct",
					SessionKey: a.sess.Key,
				}, tc.Name, tc.Arguments)
				if err != nil {
					out = "error: " + err.Error()
				}
				messages = append(messages, llm.Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Name, Content: out})
			}
			continue
		}

		final = res.Content
		break
	}
	if strings.TrimSpace(final) == "" {
		final = "(no response)"
	}

	a.sess.Add("user", input)
	a.sess.Add("assistant", final)
	_ = session.Save(a.sessionDir, a.sess)
	return final, nil
}

func (a *Agent) systemPrompt() string {
	now := time.Now().Format("2006-01-02 15:04 (Mon)")
	ws := a.workspace
	rt := fmt.Sprintf("%s/%s Go %s", runtime.GOOS, runtime.GOARCH, runtime.Version())

	var b strings.Builder
	b.WriteString("# clawlet\n\n")
	b.WriteString("You are clawlet, a helpful AI assistant.\n")
	b.WriteString("You can use tools to read/write/edit files, list directories, execute shell commands, and fetch/search the web.\n\n")
	b.WriteString("IMPORTANT: Reply with plain text. Do not call the message tool.\n\n")
	b.WriteString("## Current Time\n")
	b.WriteString(now + "\n\n")
	b.WriteString("## Runtime\n")
	b.WriteString(rt + "\n\n")
	b.WriteString("## Workspace\n")
	b.WriteString(ws + "\n\n")
	if a.cfg.Tools.RestrictToWorkspaceValue() {
		b.WriteString("## Safety\nTools are restricted to the workspace directory.\n\n")
	}

	// Bootstrap files from workspace (optional).
	for _, fn := range []string{"AGENTS.md", "SOUL.md", "USER.md", "TOOLS.md", "IDENTITY.md"} {
		p := filepath.Join(ws, fn)
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
	mem := memory.New(ws).GetContext()
	if strings.TrimSpace(mem) != "" {
		b.WriteString("# Memory\n\n")
		b.WriteString(mem)
		b.WriteString("\n\n")
	}
	return b.String()
}

func previewJSON(b json.RawMessage, max int) string {
	s := strings.TrimSpace(string(b))
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
