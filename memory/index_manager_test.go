package memory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mosaxiv/clawlet/config"
)

func TestNewIndexManager_Disabled(t *testing.T) {
	cfg := config.Default()
	enabled := false
	cfg.Agents.Defaults.MemorySearch.Enabled = &enabled
	mgr, err := NewIndexManager(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("NewIndexManager error: %v", err)
	}
	if mgr != nil {
		t.Fatalf("expected nil manager when disabled")
	}
}

func TestIndexManager_SearchAndRead(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "MEMORY.md"), []byte("# Long-term Memory\n\n- project codename is Nebula\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "memory", "2026-02-14.md"), []byte("We decided to use sqlite vector search for memory recall.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := newEmbeddingTestServer(t)
	defer server.Close()

	cfg := config.Default()
	enabled := true
	cfg.Agents.Defaults.MemorySearch.Enabled = &enabled
	cfg.Agents.Defaults.MemorySearch.Provider = "openai"
	cfg.Agents.Defaults.MemorySearch.Model = "text-embedding-3-small"
	cfg.Agents.Defaults.MemorySearch.Remote.BaseURL = server.URL + "/v1"
	cfg.Agents.Defaults.MemorySearch.Remote.APIKey = "test-key"
	cfg.Agents.Defaults.MemorySearch.Store.Path = filepath.Join(ws, ".memory", "index.sqlite")

	mgr, err := NewIndexManager(cfg, ws)
	if err != nil {
		t.Fatalf("NewIndexManager error: %v", err)
	}
	if mgr == nil {
		t.Fatalf("manager is nil")
	}
	t.Cleanup(func() { _ = mgr.Close() })

	results, err := mgr.Search(context.Background(), "sqlite vector memory", SearchOptions{MaxResults: 5})
	if err != nil {
		t.Fatalf("Search error: %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected search results")
	}
	found := false
	for _, r := range results {
		if strings.Contains(r.Path, "memory/2026-02-14.md") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected daily memory file in results, got: %+v", results)
	}

	text, rp, err := mgr.ReadFile("memory/2026-02-14.md", ReadFileOptions{From: 1, Lines: 1})
	if err != nil {
		t.Fatalf("ReadFile error: %v", err)
	}
	if rp != "memory/2026-02-14.md" {
		t.Fatalf("resolved path=%q", rp)
	}
	if !strings.Contains(text, "sqlite vector search") {
		t.Fatalf("unexpected text: %q", text)
	}
	if _, _, err := mgr.ReadFile("../secret.md", ReadFileOptions{}); err == nil {
		t.Fatalf("expected path validation error")
	}
}

func newEmbeddingTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/embeddings" {
			http.NotFound(w, r)
			return
		}
		var req struct {
			Model string `json:"model"`
			Input any    `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode req: %v", err)
		}
		var texts []string
		switch v := req.Input.(type) {
		case string:
			texts = []string{v}
		case []any:
			for _, item := range v {
				texts = append(texts, toString(item))
			}
		default:
			t.Fatalf("unexpected input type: %T", req.Input)
		}
		data := make([]map[string]any, 0, len(texts))
		for i, txt := range texts {
			data = append(data, map[string]any{
				"index":     i,
				"embedding": fakeEmbedding(txt),
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func fakeEmbedding(text string) []float64 {
	sum := sha256.Sum256([]byte(text))
	out := make([]float64, 8)
	var norm float64
	for i := range out {
		// map [0,255] -> [-1,1]
		v := (float64(sum[i]) / 127.5) - 1
		out[i] = v
		norm += v * v
	}
	if norm <= 1e-10 {
		return out
	}
	scale := 1 / math.Sqrt(norm)
	for i := range out {
		out[i] *= scale
	}
	return out
}
