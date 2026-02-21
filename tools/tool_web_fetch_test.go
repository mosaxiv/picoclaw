package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestRegistry() *Registry {
	return &Registry{
		WorkspaceDir: "/tmp",
		ExecTimeout:  5 * time.Second,
	}
}

func TestWebFetch_BasicGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("hello world"))
	}))
	defer srv.Close()

	r := newTestRegistry()
	out, err := r.webFetch(context.Background(), srv.URL, "text", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if status := result["status"].(float64); status != 200 {
		t.Fatalf("expected status 200, got %v", status)
	}
	if text := result["text"].(string); text != "hello world" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestWebFetch_HeadersForwarded(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	r := newTestRegistry()
	headers := map[string]string{"Authorization": "Bearer secret"}
	_, err := r.webFetch(context.Background(), srv.URL, "text", 0, headers)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer secret" {
		t.Fatalf("expected Authorization header to be forwarded, got %q", gotAuth)
	}
}

func TestWebFetch_NilHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	r := newTestRegistry()
	// nil headers must not panic
	_, err := r.webFetch(context.Background(), srv.URL, "text", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestWebFetch_InvalidURL(t *testing.T) {
	r := newTestRegistry()
	_, err := r.webFetch(context.Background(), "", "text", 0, nil)
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
	_, err = r.webFetch(context.Background(), "ftp://example.com", "text", 0, nil)
	if err == nil {
		t.Fatal("expected error for non-http scheme")
	}
}

func TestWebFetch_ExecuteDispatch(t *testing.T) {
	var gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	r := newTestRegistry()
	args, _ := json.Marshal(map[string]any{
		"url":     srv.URL,
		"headers": map[string]string{"Accept": "application/json"},
	})
	out, err := r.Execute(context.Background(), Context{}, "web_fetch", args)
	if err != nil {
		t.Fatal(err)
	}
	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if gotAccept != "application/json" {
		t.Fatalf("expected Accept header forwarded, got %q", gotAccept)
	}
}
