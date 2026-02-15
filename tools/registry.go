package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mosaxiv/clawlet/bus"
	"github.com/mosaxiv/clawlet/cron"
	"github.com/mosaxiv/clawlet/llm"
	"github.com/mosaxiv/clawlet/memory"
)

type Context struct {
	Channel    string
	ChatID     string
	SessionKey string
}

type Registry struct {
	WorkspaceDir        string
	RestrictToWorkspace bool
	ExecTimeout         time.Duration

	// If non-empty, only these tools are exposed and executable.
	// Unknown tool names are ignored.
	AllowTools []string

	BraveAPIKey  string
	Outbound     func(ctx context.Context, msg bus.OutboundMessage) error
	Spawn        func(ctx context.Context, task, label, originChannel, originChatID string) (string, error)
	Cron         *cron.Service
	ReadSkill    func(name string) (string, bool)
	MemorySearch memory.SearchManager
}

func (r *Registry) Definitions() []llm.ToolDefinition {
	defs := []llm.ToolDefinition{
		defReadFile(),
		defWriteFile(),
		defEditFile(),
		defListDir(),
		defExec(),
		defWebFetch(),
	}
	if r.ReadSkill != nil {
		defs = append(defs, defReadSkill())
	}
	if strings.TrimSpace(r.BraveAPIKey) != "" {
		defs = append(defs, defWebSearch())
	}
	if r.Outbound != nil {
		defs = append(defs, defMessage())
	}
	if r.Spawn != nil {
		defs = append(defs, defSpawn())
	}
	if r.Cron != nil {
		defs = append(defs, defCron())
	}
	if r.MemorySearch != nil {
		defs = append(defs, defMemorySearch(), defMemoryGet())
	}
	if len(r.AllowTools) == 0 {
		return defs
	}
	allow := r.allowSet()
	out := make([]llm.ToolDefinition, 0, len(defs))
	for _, d := range defs {
		name := strings.TrimSpace(d.Function.Name)
		if name != "" && allow[name] {
			out = append(out, d)
		}
	}
	return out
}

func (r *Registry) Execute(ctx context.Context, tctx Context, name string, args json.RawMessage) (string, error) {
	if !r.allowed(name) {
		return "", fmt.Errorf("tool disabled: %s", name)
	}
	switch name {
	case "read_file":
		var a struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return "", err
		}
		return r.readFile(a.Path)
	case "write_file":
		var a struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return "", err
		}
		return r.writeFile(a.Path, a.Content)
	case "edit_file":
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(args, &raw); err != nil {
			return "", err
		}
		_, hasOld := raw["old_text"]
		_, hasNew := raw["new_text"]
		if !hasOld && !hasNew {
			// Back-compat: older line-range edit.
			var a struct {
				Path      string `json:"path"`
				StartLine int    `json:"startLine"`
				EndLine   int    `json:"endLine"`
				NewText   string `json:"newText"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "", err
			}
			return r.editFile(a.Path, a.StartLine, a.EndLine, a.NewText)
		}
		var a struct {
			Path    string `json:"path"`
			OldText string `json:"old_text"`
			NewText string `json:"new_text"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return "", err
		}
		return r.editFileReplace(a.Path, a.OldText, a.NewText)
	case "list_dir":
		var a struct {
			Path       string `json:"path"`
			Recursive  bool   `json:"recursive"`
			MaxEntries int    `json:"maxEntries"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return "", err
		}
		return r.listDir(a.Path, a.Recursive, a.MaxEntries)
	case "exec":
		var a struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return "", err
		}
		return r.exec(ctx, a.Command)
	case "read_skill":
		var a struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return "", err
		}
		return r.readSkill(a.Name)
	case "web_fetch":
		var a struct {
			URL         string `json:"url"`
			ExtractMode string `json:"extractMode"`
			MaxChars    int    `json:"maxChars"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return "", err
		}
		return r.webFetch(ctx, a.URL, a.ExtractMode, a.MaxChars)
	case "web_search":
		var a struct {
			Query string `json:"query"`
			Count int    `json:"count"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return "", err
		}
		return r.webSearch(ctx, a.Query, a.Count)
	case "message":
		var a struct {
			Content string `json:"content"`
			Channel string `json:"channel"`
			ChatID  string `json:"chat_id"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return "", err
		}
		ch := strings.TrimSpace(a.Channel)
		cid := strings.TrimSpace(a.ChatID)
		if ch == "" || cid == "" {
			return "", errors.New("message requires explicit channel and chat_id")
		}
		// Avoid duplicate sends to the active conversation; reply with normal assistant text instead.
		if strings.TrimSpace(tctx.Channel) != "" && strings.TrimSpace(tctx.ChatID) != "" {
			if ch == strings.TrimSpace(tctx.Channel) && cid == strings.TrimSpace(tctx.ChatID) {
				return "", errors.New("message to current session is not allowed; respond with assistant text instead")
			}
		}
		return r.message(ctx, ch, cid, a.Content)
	case "spawn":
		var a struct {
			Task  string `json:"task"`
			Label string `json:"label"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return "", err
		}
		return r.spawn(ctx, a.Task, a.Label, tctx.Channel, tctx.ChatID)
	case "cron":
		var a struct {
			Action       string `json:"action"`
			Message      string `json:"message"`
			EverySeconds int    `json:"every_seconds"`
			CronExpr     string `json:"cron_expr"`
			JobID        string `json:"job_id"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return "", err
		}
		return r.cronTool(ctx, tctx, a.Action, a.Message, a.EverySeconds, a.CronExpr, a.JobID)
	case "memory_search":
		var a struct {
			Query      string   `json:"query"`
			MaxResults *int     `json:"maxResults"`
			MinScore   *float64 `json:"minScore"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return "", err
		}
		return r.memorySearch(ctx, a.Query, a.MaxResults, a.MinScore)
	case "memory_get":
		var a struct {
			Path  string `json:"path"`
			From  *int   `json:"from"`
			Lines *int   `json:"lines"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return "", err
		}
		return r.memoryGet(a.Path, a.From, a.Lines)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func (r *Registry) allowed(name string) bool {
	if len(r.AllowTools) == 0 {
		return true
	}
	return r.allowSet()[name]
}

func (r *Registry) allowSet() map[string]bool {
	m := map[string]bool{}
	for _, n := range r.AllowTools {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		m[n] = true
	}
	return m
}
