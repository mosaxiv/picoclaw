package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mosaxiv/clawlet/bus"
	"github.com/mosaxiv/clawlet/llm"
	"github.com/mosaxiv/clawlet/tools"
)

type SubagentManager struct {
	loop *Loop
}

func NewSubagentManager(loop *Loop) *SubagentManager {
	return &SubagentManager{loop: loop}
}

func (m *SubagentManager) Spawn(ctx context.Context, task, label, originChannel, originChatID string) (string, error) {
	if m.loop == nil || m.loop.bus == nil {
		return "", fmt.Errorf("subagent loop not configured")
	}
	task = strings.TrimSpace(task)
	if task == "" {
		return "", fmt.Errorf("task is empty")
	}
	id := "sa_" + randID()
	go func() {
		out, err := m.runSubagent(ctx, task)
		if err != nil {
			out = "error: " + err.Error()
		}
		display := strings.TrimSpace(label)
		if display == "" {
			display = shortLabel(task)
		}
		announce := fmt.Sprintf(`[Background task '%s' completed]

Task: %s

Result:
%s

Summarize this naturally for the user. Keep it brief (1-2 sentences). Do not mention technical details like "subagent" or task IDs.`, display, task, out)

		// Announce back to origin via system channel; main loop routes and replies.
		_ = m.loop.bus.PublishInbound(context.Background(), bus.InboundMessage{
			Channel:    "system",
			SenderID:   id,
			ChatID:     originChannel + ":" + originChatID,
			Content:    announce,
			SessionKey: "",
		})
	}()
	return id, nil
}

func (m *SubagentManager) runSubagent(ctx context.Context, task string) (string, error) {
	l := m.loop
	if l == nil || l.llm == nil || l.cfg == nil {
		return "", fmt.Errorf("subagent loop not configured")
	}

	// Subagent tools: a restricted subset (no message, no spawn, no cron).
	treg := &tools.Registry{
		WorkspaceDir:        l.workspace,
		RestrictToWorkspace: l.cfg.Tools.RestrictToWorkspaceValue(),
		ExecTimeout:         l.tools.ExecTimeout,
		BraveAPIKey:         l.tools.BraveAPIKey,
		AllowTools: []string{
			"read_file",
			"write_file",
			"list_dir",
			"exec",
			"web_search",
			"web_fetch",
		},
	}

	system := buildSubagentPrompt(l.workspace, task)
	messages := []llm.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: task},
	}

	toolsDefs := treg.Definitions()

	const maxIters = 15
	var final string
	for range maxIters {
		res, err := l.llm.Chat(ctx, messages, toolsDefs)
		if err != nil {
			return "", err
		}
		if res.HasToolCalls() {
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
				out, err := treg.Execute(ctx, tools.Context{
					Channel:    "cli",
					ChatID:     "subagent",
					SessionKey: "",
				}, tc.Name, json.RawMessage(tc.Arguments))
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
	return final, nil
}

func buildSubagentPrompt(workspace string, task string) string {
	return fmt.Sprintf(`# Subagent

You are a subagent spawned by the main agent to complete a specific task.

## Your Task
%s

## Rules
1. Stay focused: complete only the assigned task
2. Do not initiate conversations or take on side tasks
3. Be concise but informative
4. Do not use tools that are not available

## Workspace
%s

When you have completed the task, provide a clear summary of your findings or actions.`, strings.TrimSpace(task), strings.TrimSpace(workspace))
}

func shortLabel(task string) string {
	task = strings.TrimSpace(task)
	if task == "" {
		return "task"
	}
	if len(task) <= 30 {
		return task
	}
	return task[:30]
}

func randID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
