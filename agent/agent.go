package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
	cfg          *config.Config
	workspace    string
	maxIters     int
	memoryWindow int
	verbose      bool

	llm   *llm.Client
	tools *tools.Registry

	sessionDir string
	sess       *session.Session

	consolidationMu      sync.Mutex
	consolidationRunning bool
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
	memMgr, err := memory.NewIndexManager(opts.Config, wsAbs)
	if err != nil {
		return nil, err
	}
	treg.MemorySearch = memMgr

	return &Agent{
		cfg:          opts.Config,
		workspace:    wsAbs,
		maxIters:     opts.MaxIters,
		memoryWindow: opts.Config.Agents.Defaults.MemoryWindowValue(),
		verbose:      opts.Verbose,
		llm:          c,
		tools:        treg,
		sessionDir:   sdir,
		sess:         sess,
	}, nil
}

func (a *Agent) Process(ctx context.Context, input string) (string, error) {
	a.scheduleConsolidation()

	sys := a.systemPrompt()
	history := a.sess.History(a.memoryWindow)
	messages := make([]llm.Message, 0, 1+len(history)+1)
	messages = append(messages, llm.Message{Role: "system", Content: sys})
	for _, m := range history {
		messages = append(messages, llm.Message{Role: m.Role, Content: m.Content})
	}
	messages = append(messages, llm.Message{Role: "user", Content: input})

	toolsDefs := a.tools.Definitions()

	var final string
	toolsUsed := make([]string, 0, 8)
	for iter := 0; iter < a.maxIters; iter++ {
		res, err := a.llm.Chat(ctx, messages, toolsDefs)
		if err != nil {
			return "", err
		}

		if res.HasToolCalls() {
			for _, tc := range res.ToolCalls {
				toolsUsed = append(toolsUsed, tc.Name)
			}
			messages = appendToolRound(messages, res.Content, res.ToolCalls, func(tc llm.ToolCall) string {
				if a.verbose {
					fmt.Fprintf(os.Stderr, "tool: %s %s\n", tc.Name, previewJSON(tc.Arguments, 200))
				}
				out, err := a.tools.Execute(ctx, tools.Context{
					Channel:    "cli",
					ChatID:     "direct",
					SessionKey: a.sess.Key,
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

	a.sess.Add("user", input)
	a.sess.AddWithTools("assistant", final, toolsUsed)
	_ = session.Save(a.sessionDir, a.sess)
	return final, nil
}

func (a *Agent) scheduleConsolidation() {
	if a == nil || a.sess == nil {
		return
	}
	if !a.sess.NeedsConsolidation(a.memoryWindow) {
		return
	}

	a.consolidationMu.Lock()
	if a.consolidationRunning {
		a.consolidationMu.Unlock()
		return
	}
	a.consolidationRunning = true
	a.consolidationMu.Unlock()

	go func() {
		defer func() {
			a.consolidationMu.Lock()
			a.consolidationRunning = false
			a.consolidationMu.Unlock()
		}()

		cctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		done, err := maybeConsolidateSession(cctx, a.workspace, a.sess, a.memoryWindow, func(ctx context.Context, currentMemory, conversation string) (string, string, error) {
			return summarizeConsolidationWithLLM(ctx, a.llm, currentMemory, conversation)
		})
		if err != nil {
			if a.verbose {
				fmt.Fprintf(os.Stderr, "consolidation error: %v\n", err)
			}
			return
		}
		if !done {
			return
		}
		if err := session.Save(a.sessionDir, a.sess); err != nil && a.verbose {
			fmt.Fprintf(os.Stderr, "consolidation save error: %v\n", err)
		}
	}()
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
