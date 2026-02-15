package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mosaxiv/clawlet/memory"
)

type stubMemoryManager struct{}

func (stubMemoryManager) Search(ctx context.Context, query string, opts memory.SearchOptions) ([]memory.SearchResult, error) {
	return []memory.SearchResult{}, nil
}

func (stubMemoryManager) ReadFile(relPath string, opts memory.ReadFileOptions) (string, string, error) {
	return "", relPath, nil
}

func (stubMemoryManager) Sync(ctx context.Context, force bool) error { return nil }

func (stubMemoryManager) Status(ctx context.Context) memory.SearchStatus {
	return memory.SearchStatus{Enabled: true, Provider: "openai", Model: "text-embedding-3-small"}
}

func (stubMemoryManager) Close() error { return nil }

func TestRegistryDefinitions_GatedByCapabilities(t *testing.T) {
	r := &Registry{
		WorkspaceDir:        "/tmp",
		RestrictToWorkspace: false,
		ExecTimeout:         1 * time.Second,
		BraveAPIKey:         "",
		Outbound:            nil,
		Spawn:               nil,
		Cron:                nil,
		ReadSkill:           nil,
	}

	defs := r.Definitions()
	has := map[string]bool{}
	for _, d := range defs {
		if n := d.Function.Name; n != "" {
			has[n] = true
		}
	}

	// Always present.
	for _, n := range []string{"read_file", "write_file", "edit_file", "list_dir", "exec", "web_fetch"} {
		if !has[n] {
			t.Fatalf("expected tool definition: %s", n)
		}
	}

	// Capability-gated.
	for _, n := range []string{"web_search", "message", "spawn", "cron", "read_skill", "memory_search", "memory_get"} {
		if has[n] {
			t.Fatalf("did not expect tool definition: %s", n)
		}
	}

	// Execute unknown tool should still error.
	if _, err := r.Execute(context.Background(), Context{Channel: "cli", ChatID: "direct"}, "message", json.RawMessage(`{}`)); err == nil {
		t.Fatalf("expected error executing disabled tool")
	}
}

func TestRegistryDefinitions_IncludesMemoryToolsWhenEnabled(t *testing.T) {
	r := &Registry{
		WorkspaceDir:        "/tmp",
		RestrictToWorkspace: false,
		ExecTimeout:         1 * time.Second,
		MemorySearch:        stubMemoryManager{},
	}
	defs := r.Definitions()
	has := map[string]bool{}
	for _, d := range defs {
		has[d.Function.Name] = true
	}
	for _, n := range []string{"memory_search", "memory_get"} {
		if !has[n] {
			t.Fatalf("expected memory tool definition: %s", n)
		}
	}
}
