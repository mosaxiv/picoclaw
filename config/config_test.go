package config

import "testing"

func TestAgentDefaults_MaxTokensTemperature(t *testing.T) {
	cfg := Default()
	if cfg.Agents.Defaults.MaxTokensValue() != DefaultAgentMaxTokens {
		t.Fatalf("maxTokens=%d", cfg.Agents.Defaults.MaxTokensValue())
	}
	if cfg.Agents.Defaults.TemperatureValue() != DefaultAgentTemperature {
		t.Fatalf("temperature=%f", cfg.Agents.Defaults.TemperatureValue())
	}
	if cfg.Agents.Defaults.MemoryWindowValue() != DefaultAgentMemoryWindow {
		t.Fatalf("memoryWindow=%d", cfg.Agents.Defaults.MemoryWindowValue())
	}
	if cfg.Agents.Defaults.MemorySearch.EnabledValue() {
		t.Fatalf("memorySearch.enabled should be false by default")
	}
	if cfg.Agents.Defaults.MemorySearch.Chunking.Tokens != DefaultMemorySearchChunkTokens {
		t.Fatalf("memorySearch.chunking.tokens=%d", cfg.Agents.Defaults.MemorySearch.Chunking.Tokens)
	}
	if cfg.Agents.Defaults.MemorySearch.Query.MaxResults != DefaultMemorySearchMaxResults {
		t.Fatalf("memorySearch.query.maxResults=%d", cfg.Agents.Defaults.MemorySearch.Query.MaxResults)
	}

	cfg.Agents.Defaults.MaxTokens = 2048
	cfg.Agents.Defaults.MemoryWindow = 80
	temp := 0.0
	cfg.Agents.Defaults.Temperature = &temp
	if cfg.Agents.Defaults.MaxTokensValue() != 2048 {
		t.Fatalf("maxTokens=%d", cfg.Agents.Defaults.MaxTokensValue())
	}
	if cfg.Agents.Defaults.TemperatureValue() != 0.0 {
		t.Fatalf("temperature=%f", cfg.Agents.Defaults.TemperatureValue())
	}
	if cfg.Agents.Defaults.MemoryWindowValue() != 80 {
		t.Fatalf("memoryWindow=%d", cfg.Agents.Defaults.MemoryWindowValue())
	}
}

func TestLoad_MemorySearchDefaultsAndClamp(t *testing.T) {
	cfg := Default()
	enabled := true
	cfg.Agents.Defaults.MemorySearch.Enabled = &enabled
	cfg.Agents.Defaults.MemorySearch.Provider = ""
	cfg.Agents.Defaults.MemorySearch.Chunking.Tokens = 10
	cfg.Agents.Defaults.MemorySearch.Chunking.Overlap = 99
	cfg.Agents.Defaults.MemorySearch.Query.MaxResults = 0
	minScore := 2.0
	cfg.Agents.Defaults.MemorySearch.Query.MinScore = &minScore

	tmp := t.TempDir() + "/cfg.json"
	if err := Save(tmp, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := Load(tmp)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Agents.Defaults.MemorySearch.Provider != "openai" {
		t.Fatalf("provider=%q", loaded.Agents.Defaults.MemorySearch.Provider)
	}
	if loaded.Agents.Defaults.MemorySearch.Chunking.Overlap != 9 {
		t.Fatalf("overlap=%d", loaded.Agents.Defaults.MemorySearch.Chunking.Overlap)
	}
	if loaded.Agents.Defaults.MemorySearch.Query.MaxResults != DefaultMemorySearchMaxResults {
		t.Fatalf("maxResults=%d", loaded.Agents.Defaults.MemorySearch.Query.MaxResults)
	}
	if loaded.Agents.Defaults.MemorySearch.Query.MinScore == nil {
		t.Fatalf("minScore is nil")
	}
	if *loaded.Agents.Defaults.MemorySearch.Query.MinScore != 1.0 {
		t.Fatalf("minScore=%f", *loaded.Agents.Defaults.MemorySearch.Query.MinScore)
	}
}

func TestApplyLLMRouting_OpenRouter(t *testing.T) {
	cfg := Default()
	cfg.Env["OPENROUTER_API_KEY"] = "sk-or-123"
	cfg.Agents.Defaults.Model = "openrouter/anthropic/claude-sonnet-4-5"
	cfg.LLM.BaseURL = ""
	cfg.LLM.APIKey = ""

	provider, configured := cfg.ApplyLLMRouting()
	if provider != "openrouter" {
		t.Fatalf("provider=%q", provider)
	}
	if configured != "openrouter/anthropic/claude-sonnet-4-5" {
		t.Fatalf("configured=%q", configured)
	}
	if cfg.LLM.BaseURL != DefaultOpenRouterBaseURL {
		t.Fatalf("baseURL=%q", cfg.LLM.BaseURL)
	}
	if cfg.LLM.APIKey != "sk-or-123" {
		t.Fatalf("apiKey=%q", cfg.LLM.APIKey)
	}
	if cfg.LLM.Model != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("model=%q", cfg.LLM.Model)
	}
}

func TestApplyLLMRouting_OpenAI(t *testing.T) {
	cfg := Default()
	cfg.Env["OPENAI_API_KEY"] = "sk-123"
	cfg.Agents.Defaults.Model = "openai/gpt-4o-mini"
	cfg.LLM.BaseURL = ""
	cfg.LLM.APIKey = ""

	provider, _ := cfg.ApplyLLMRouting()
	if provider != "openai" {
		t.Fatalf("provider=%q", provider)
	}
	if cfg.LLM.BaseURL != DefaultOpenAIBaseURL {
		t.Fatalf("baseURL=%q", cfg.LLM.BaseURL)
	}
	if cfg.LLM.APIKey != "sk-123" {
		t.Fatalf("apiKey=%q", cfg.LLM.APIKey)
	}
	if cfg.LLM.Model != "gpt-4o-mini" {
		t.Fatalf("model=%q", cfg.LLM.Model)
	}
}

func TestApplyLLMRouting_Anthropic(t *testing.T) {
	cfg := Default()
	cfg.Env["ANTHROPIC_API_KEY"] = "sk-ant-123"
	cfg.Agents.Defaults.Model = "anthropic/claude-sonnet-4-5"
	cfg.LLM.BaseURL = ""
	cfg.LLM.APIKey = ""

	provider, _ := cfg.ApplyLLMRouting()
	if provider != "anthropic" {
		t.Fatalf("provider=%q", provider)
	}
	if cfg.LLM.BaseURL != DefaultAnthropicBaseURL {
		t.Fatalf("baseURL=%q", cfg.LLM.BaseURL)
	}
	if cfg.LLM.APIKey != "sk-ant-123" {
		t.Fatalf("apiKey=%q", cfg.LLM.APIKey)
	}
	if cfg.LLM.Model != "claude-sonnet-4-5" {
		t.Fatalf("model=%q", cfg.LLM.Model)
	}
}

func TestApplyLLMRouting_Gemini(t *testing.T) {
	cfg := Default()
	cfg.Env["GOOGLE_API_KEY"] = "g-123"
	cfg.Agents.Defaults.Model = "gemini/gemini-2.5-flash"
	cfg.LLM.BaseURL = ""
	cfg.LLM.APIKey = ""

	provider, _ := cfg.ApplyLLMRouting()
	if provider != "gemini" {
		t.Fatalf("provider=%q", provider)
	}
	if cfg.LLM.BaseURL != DefaultGeminiBaseURL {
		t.Fatalf("baseURL=%q", cfg.LLM.BaseURL)
	}
	if cfg.LLM.APIKey != "g-123" {
		t.Fatalf("apiKey=%q", cfg.LLM.APIKey)
	}
	if cfg.LLM.Model != "gemini-2.5-flash" {
		t.Fatalf("model=%q", cfg.LLM.Model)
	}
}

func TestApplyLLMRouting_OllamaLocal(t *testing.T) {
	cfg := Default()
	cfg.Agents.Defaults.Model = "ollama/qwen2.5:14b"
	cfg.LLM.BaseURL = ""
	cfg.LLM.APIKey = ""

	provider, _ := cfg.ApplyLLMRouting()
	if provider != "ollama" {
		t.Fatalf("provider=%q", provider)
	}
	if cfg.LLM.BaseURL != DefaultOllamaBaseURL {
		t.Fatalf("baseURL=%q", cfg.LLM.BaseURL)
	}
	if cfg.LLM.APIKey != "" {
		t.Fatalf("apiKey=%q", cfg.LLM.APIKey)
	}
	if cfg.LLM.Model != "qwen2.5:14b" {
		t.Fatalf("model=%q", cfg.LLM.Model)
	}
}

func TestApplyLLMRouting_OpenAICodex(t *testing.T) {
	cfg := Default()
	cfg.Agents.Defaults.Model = "openai-codex/gpt-5.1-codex"
	cfg.LLM.BaseURL = ""
	cfg.LLM.APIKey = ""

	provider, _ := cfg.ApplyLLMRouting()
	if provider != "openai-codex" {
		t.Fatalf("provider=%q", provider)
	}
	if cfg.LLM.BaseURL != DefaultOpenAICodexBaseURL {
		t.Fatalf("baseURL=%q", cfg.LLM.BaseURL)
	}
	if cfg.LLM.APIKey != "" {
		t.Fatalf("apiKey=%q", cfg.LLM.APIKey)
	}
	if cfg.LLM.Model != "gpt-5.1-codex" {
		t.Fatalf("model=%q", cfg.LLM.Model)
	}
}

func TestApplyLLMRouting_LocalAlias(t *testing.T) {
	cfg := Default()
	cfg.Agents.Defaults.Model = "local/qwen2.5:14b"
	cfg.LLM.BaseURL = ""
	cfg.LLM.APIKey = ""

	provider, _ := cfg.ApplyLLMRouting()
	if provider != "ollama" {
		t.Fatalf("provider=%q", provider)
	}
	if cfg.LLM.BaseURL != DefaultOllamaBaseURL {
		t.Fatalf("baseURL=%q", cfg.LLM.BaseURL)
	}
	if cfg.LLM.Model != "qwen2.5:14b" {
		t.Fatalf("model=%q", cfg.LLM.Model)
	}
}

func TestLoad_MediaDefaults(t *testing.T) {
	cfg := Default()
	cfg.Tools.Media = MediaToolsConfig{}

	tmp := t.TempDir() + "/cfg.json"
	if err := Save(tmp, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := Load(tmp)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !loaded.Tools.Media.EnabledValue() {
		t.Fatalf("media.enabled should default to true")
	}
	if !loaded.Tools.Media.AudioEnabledValue() {
		t.Fatalf("media.audioEnabled should default to true")
	}
	if !loaded.Tools.Media.ImageEnabledValue() {
		t.Fatalf("media.imageEnabled should default to true")
	}
	if !loaded.Tools.Media.AttachmentEnabledValue() {
		t.Fatalf("media.attachmentEnabled should default to true")
	}
	if loaded.Tools.Media.MaxAttachments != DefaultMediaMaxAttachments {
		t.Fatalf("maxAttachments=%d", loaded.Tools.Media.MaxAttachments)
	}
	if loaded.Tools.Media.MaxFileBytes != DefaultMediaMaxFileBytes {
		t.Fatalf("maxFileBytes=%d", loaded.Tools.Media.MaxFileBytes)
	}
	if loaded.Tools.Media.MaxInlineImageBytes != DefaultMediaMaxInlineImageBytes {
		t.Fatalf("maxInlineImageBytes=%d", loaded.Tools.Media.MaxInlineImageBytes)
	}
	if loaded.Tools.Media.MaxTextChars != DefaultMediaMaxTextChars {
		t.Fatalf("maxTextChars=%d", loaded.Tools.Media.MaxTextChars)
	}
	if loaded.Tools.Media.DownloadTimeoutSec != DefaultMediaDownloadTimeoutSec {
		t.Fatalf("downloadTimeoutSec=%d", loaded.Tools.Media.DownloadTimeoutSec)
	}
}
