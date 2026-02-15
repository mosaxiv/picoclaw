package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/mosaxiv/clawlet/memory"
)

func (r *Registry) memorySearch(ctx context.Context, query string, maxResults *int, minScore *float64) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return `{"results":[]}`, nil
	}
	if r.MemorySearch == nil {
		return `{"results":[],"disabled":true,"error":"memory search disabled"}`, nil
	}
	opts := memory.SearchOptions{}
	if maxResults != nil {
		opts.MaxResults = *maxResults
	}
	if minScore != nil {
		opts.MinScore = *minScore
	}
	results, err := r.MemorySearch.Search(ctx, query, opts)
	if err != nil {
		return jsonResult(map[string]any{
			"results":  []any{},
			"disabled": true,
			"error":    err.Error(),
		})
	}
	status := r.MemorySearch.Status(ctx)
	return jsonResult(map[string]any{
		"results":  results,
		"provider": status.Provider,
		"model":    status.Model,
	})
}

func (r *Registry) memoryGet(path string, from *int, lines *int) (string, error) {
	if r.MemorySearch == nil {
		return `{"path":"","text":"","disabled":true,"error":"memory search disabled"}`, nil
	}
	opts := memory.ReadFileOptions{}
	if from != nil {
		opts.From = *from
	}
	if lines != nil {
		opts.Lines = *lines
	}
	text, resolved, err := r.MemorySearch.ReadFile(path, opts)
	if err != nil {
		return jsonResult(map[string]any{
			"path":     strings.TrimSpace(path),
			"text":     "",
			"disabled": true,
			"error":    err.Error(),
		})
	}
	return jsonResult(map[string]any{
		"path": resolved,
		"text": text,
	})
}

func jsonResult(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
