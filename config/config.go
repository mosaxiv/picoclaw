package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Env map[string]string `json:"env"`
	// Agent configuration (model, iterations, etc.). Kept small on purpose.
	Agents AgentsConfig `json:"agents"`

	LLM       LLMConfig       `json:"llm"`
	Tools     ToolsConfig     `json:"tools"`
	Cron      CronConfig      `json:"cron"`
	Heartbeat HeartbeatConfig `json:"heartbeat"`
	Gateway   GatewayConfig   `json:"gateway"`
	// Channels are optional; enable what you need.
	Channels ChannelsConfig `json:"channels"`
}

type LLMConfig struct {
	Provider string            `json:"provider,omitempty"`
	APIKey   string            `json:"apiKey"`
	BaseURL  string            `json:"baseURL"`
	Model    string            `json:"model"`
	Headers  map[string]string `json:"headers,omitempty"`
}

type AgentsConfig struct {
	Defaults AgentDefaultsConfig `json:"defaults"`
}

type AgentDefaultsConfig struct {
	Model       string   `json:"model"`
	MaxTokens   int      `json:"maxTokens,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
}

func (c AgentDefaultsConfig) MaxTokensValue() int {
	if c.MaxTokens <= 0 {
		return DefaultAgentMaxTokens
	}
	return c.MaxTokens
}

func (c AgentDefaultsConfig) TemperatureValue() float64 {
	if c.Temperature == nil {
		return DefaultAgentTemperature
	}
	return *c.Temperature
}

type ToolsConfig struct {
	RestrictToWorkspace *bool          `json:"restrictToWorkspace"`
	Exec                ExecToolConfig `json:"exec"`
	Web                 WebToolsConfig `json:"web"`
}

func (c ToolsConfig) RestrictToWorkspaceValue() bool {
	if c.RestrictToWorkspace == nil {
		return true
	}
	return *c.RestrictToWorkspace
}

type ExecToolConfig struct {
	TimeoutSec int `json:"timeoutSec"`
}

type WebToolsConfig struct {
	BraveAPIKey string `json:"braveApiKey"`
}

type CronConfig struct {
	Enabled *bool `json:"enabled"`
}

func (c CronConfig) EnabledValue() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

type HeartbeatConfig struct {
	Enabled     *bool `json:"enabled"`
	IntervalSec int   `json:"intervalSec"`
}

func (c HeartbeatConfig) EnabledValue() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

type GatewayConfig struct {
	// Listen address for HTTP endpoints needed by channels (reserved for future use).
	// Example: "0.0.0.0:18790"
	Listen string `json:"listen"`
}

type ChannelsConfig struct {
	Discord  DiscordConfig  `json:"discord"`
	Slack    SlackConfig    `json:"slack"`
	Telegram TelegramConfig `json:"telegram"`
	WhatsApp WhatsAppConfig `json:"whatsapp"`
}

type DiscordConfig struct {
	Enabled    bool     `json:"enabled"`
	Token      string   `json:"token"`
	AllowFrom  []string `json:"allowFrom"`
	GatewayURL string   `json:"gatewayURL,omitempty"`
	Intents    int      `json:"intents,omitempty"`
}

// Slack (Socket Mode).
// Inbound via Socket Mode, outbound via Web API (chat.postMessage).
type SlackConfig struct {
	Enabled   bool     `json:"enabled"`
	AllowFrom []string `json:"allowFrom"`
	BotToken  string   `json:"botToken"` // xoxb-...
	AppToken  string   `json:"appToken"` // xapp-... (Socket Mode)
	// GroupPolicy controls whether the bot responds to non-DM messages.
	// Supported: "mention" (default), "open", "allowlist".
	GroupPolicy    string         `json:"groupPolicy,omitempty"`
	GroupAllowFrom []string       `json:"groupAllowFrom,omitempty"` // channel IDs allowed when groupPolicy="allowlist"
	DM             *SlackDMConfig `json:"dm,omitempty"`
}

type SlackDMConfig struct {
	Enabled bool `json:"enabled"`
}

// Telegram (Bot API via long polling).
type TelegramConfig struct {
	Enabled        bool     `json:"enabled"`
	Token          string   `json:"token"`
	AllowFrom      []string `json:"allowFrom"`
	BaseURL        string   `json:"baseURL,omitempty"` // optional: custom Bot API server URL
	PollTimeoutSec int      `json:"pollTimeoutSec,omitempty"`
	Workers        int      `json:"workers,omitempty"`
}

// WhatsApp (whatsmeow / WhatsApp Web Multi-Device).
type WhatsAppConfig struct {
	Enabled          bool     `json:"enabled"`
	AllowFrom        []string `json:"allowFrom"`
	SessionStorePath string   `json:"sessionStorePath,omitempty"` // optional: sqlite store path for persistent login
}

const (
	DefaultAgentMaxTokens    = 8192
	DefaultAgentTemperature  = 0.7
	DefaultOpenAIBaseURL     = "https://api.openai.com/v1"
	DefaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"
	DefaultAnthropicBaseURL  = "https://api.anthropic.com"
	DefaultGeminiBaseURL     = "https://generativelanguage.googleapis.com/v1beta"
	DefaultOllamaBaseURL     = "http://localhost:11434/v1"
)

func Default() *Config {
	restrict := true
	cronEnabled := true
	hbEnabled := true
	return &Config{
		Env:    map[string]string{},
		Agents: AgentsConfig{Defaults: AgentDefaultsConfig{Model: "openrouter/openai/gpt-4o-mini"}},
		LLM: LLMConfig{
			Provider: "",
			APIKey:   "",
			BaseURL:  "",
			Model:    "",
			Headers:  map[string]string{},
		},
		Tools: ToolsConfig{
			RestrictToWorkspace: &restrict,
			Exec: ExecToolConfig{
				TimeoutSec: 60,
			},
			Web: WebToolsConfig{
				BraveAPIKey: "",
			},
		},
		Cron: CronConfig{
			Enabled: &cronEnabled,
		},
		Heartbeat: HeartbeatConfig{
			Enabled:     &hbEnabled,
			IntervalSec: 30 * 60,
		},
		Gateway: GatewayConfig{
			Listen: "0.0.0.0:18790",
		},
		Channels: ChannelsConfig{
			Discord: DiscordConfig{
				Enabled:    false,
				Token:      "",
				AllowFrom:  nil,
				GatewayURL: "wss://gateway.discord.gg/?v=10&encoding=json",
				Intents:    37377, // GUILDS (1<<0) + GUILD_MESSAGES (1<<9) + DIRECT_MESSAGES (1<<12) + MESSAGE_CONTENT (1<<15)
			},
			Slack: SlackConfig{
				Enabled:        false,
				AllowFrom:      nil,
				BotToken:       "",
				AppToken:       "",
				GroupPolicy:    "mention",
				GroupAllowFrom: nil,
				DM:             &SlackDMConfig{Enabled: true},
			},
			Telegram: TelegramConfig{
				Enabled:        false,
				Token:          "",
				AllowFrom:      nil,
				BaseURL:        "https://api.telegram.org",
				PollTimeoutSec: 25,
				Workers:        2,
			},
			WhatsApp: WhatsAppConfig{
				Enabled:   false,
				AllowFrom: nil,
			},
		},
	}
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.LLM.Headers == nil {
		cfg.LLM.Headers = map[string]string{}
	}
	if cfg.Env == nil {
		cfg.Env = map[string]string{}
	}
	if cfg.Tools.Exec.TimeoutSec <= 0 {
		cfg.Tools.Exec.TimeoutSec = 60
	}
	if cfg.Tools.RestrictToWorkspace == nil {
		v := true
		cfg.Tools.RestrictToWorkspace = &v
	}
	if cfg.Cron.Enabled == nil {
		v := true
		cfg.Cron.Enabled = &v
	}
	if cfg.Heartbeat.IntervalSec <= 0 {
		cfg.Heartbeat.IntervalSec = 30 * 60
	}
	if cfg.Heartbeat.Enabled == nil {
		// Default to enabled when missing from config.
		v := true
		cfg.Heartbeat.Enabled = &v
	}
	if cfg.Gateway.Listen == "" {
		cfg.Gateway.Listen = "0.0.0.0:18790"
	}
	if cfg.Channels.Discord.GatewayURL == "" {
		cfg.Channels.Discord.GatewayURL = "wss://gateway.discord.gg/?v=10&encoding=json"
	}
	if cfg.Channels.Discord.Intents == 0 {
		cfg.Channels.Discord.Intents = 37377
	}
	if strings.TrimSpace(cfg.Channels.Slack.GroupPolicy) == "" {
		cfg.Channels.Slack.GroupPolicy = "mention"
	}
	// Default DM policy is open (enabled).
	if cfg.Channels.Slack.DM == nil {
		cfg.Channels.Slack.DM = &SlackDMConfig{Enabled: true}
	}
	if strings.TrimSpace(cfg.Channels.Telegram.BaseURL) == "" {
		cfg.Channels.Telegram.BaseURL = "https://api.telegram.org"
	}
	if cfg.Channels.Telegram.PollTimeoutSec <= 0 {
		cfg.Channels.Telegram.PollTimeoutSec = 25
	}
	if cfg.Channels.Telegram.Workers <= 0 {
		cfg.Channels.Telegram.Workers = 2
	}
	cfg.Channels.WhatsApp.SessionStorePath = strings.TrimSpace(cfg.Channels.WhatsApp.SessionStorePath)

	// Apply model routing to populate cfg.LLM for runtime use.
	cfg.ApplyLLMRouting()
	return &cfg, nil
}

func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return err
	}
	return nil
}

// ApplyLLMRouting resolves the effective LLM endpoint and API key from:
// - agents.defaults.model (preferred) or llm.model
// - env keys OPENAI_API_KEY / OPENROUTER_API_KEY / ANTHROPIC_API_KEY / GEMINI_API_KEY / GOOGLE_API_KEY
// It mutates cfg.LLM to the effective values used at runtime.
func (cfg *Config) ApplyLLMRouting() (provider string, configuredModel string) {
	providerHint := canonicalProvider(cfg.LLM.Provider)
	cfg.LLM.Provider = ""

	configuredModel = strings.TrimSpace(cfg.Agents.Defaults.Model)
	if configuredModel == "" {
		configuredModel = strings.TrimSpace(cfg.LLM.Model)
	}
	if configuredModel == "" {
		configuredModel = "openai/gpt-4o-mini"
	}

	p, model := parseRoutedModel(configuredModel)
	if p == "" {
		provider = providerHint
		cfg.LLM.Provider = provider

		// No routing prefix; treat cfg.LLM as already effective.
		if strings.TrimSpace(cfg.LLM.BaseURL) == "" {
			switch provider {
			case "anthropic":
				cfg.LLM.BaseURL = DefaultAnthropicBaseURL
			case "gemini":
				cfg.LLM.BaseURL = DefaultGeminiBaseURL
			case "ollama":
				cfg.LLM.BaseURL = DefaultOllamaBaseURL
			default:
				cfg.LLM.BaseURL = DefaultOpenAIBaseURL
			}
		}
		if strings.TrimSpace(cfg.LLM.Model) == "" {
			cfg.LLM.Model = configuredModel
		}
		if strings.TrimSpace(cfg.LLM.APIKey) == "" {
			switch provider {
			case "anthropic":
				cfg.LLM.APIKey = strings.TrimSpace(cfg.Env["ANTHROPIC_API_KEY"])
			case "gemini":
				cfg.LLM.APIKey = strings.TrimSpace(cfg.Env["GEMINI_API_KEY"])
				if cfg.LLM.APIKey == "" {
					cfg.LLM.APIKey = strings.TrimSpace(cfg.Env["GOOGLE_API_KEY"])
				}
			}
		}
		return provider, configuredModel
	}

	provider = p
	cfg.LLM.Provider = provider
	cfg.LLM.Model = model

	if strings.TrimSpace(cfg.LLM.BaseURL) == "" {
		switch provider {
		case "openai":
			cfg.LLM.BaseURL = DefaultOpenAIBaseURL
		case "openrouter":
			cfg.LLM.BaseURL = DefaultOpenRouterBaseURL
		case "anthropic":
			cfg.LLM.BaseURL = DefaultAnthropicBaseURL
		case "gemini":
			cfg.LLM.BaseURL = DefaultGeminiBaseURL
		case "ollama":
			cfg.LLM.BaseURL = DefaultOllamaBaseURL
		}
	}

	if strings.TrimSpace(cfg.LLM.APIKey) == "" {
		switch provider {
		case "openai":
			cfg.LLM.APIKey = strings.TrimSpace(cfg.Env["OPENAI_API_KEY"])
		case "openrouter":
			cfg.LLM.APIKey = strings.TrimSpace(cfg.Env["OPENROUTER_API_KEY"])
		case "anthropic":
			cfg.LLM.APIKey = strings.TrimSpace(cfg.Env["ANTHROPIC_API_KEY"])
		case "gemini":
			cfg.LLM.APIKey = strings.TrimSpace(cfg.Env["GEMINI_API_KEY"])
			if cfg.LLM.APIKey == "" {
				cfg.LLM.APIKey = strings.TrimSpace(cfg.Env["GOOGLE_API_KEY"])
			}
		}
	}

	return provider, configuredModel
}

func parseRoutedModel(s string) (provider string, model string) {
	s = strings.TrimSpace(s)
	if after, ok := strings.CutPrefix(s, "openai/"); ok {
		return "openai", after
	}
	if after, ok := strings.CutPrefix(s, "openrouter/"); ok {
		return "openrouter", after
	}
	if after, ok := strings.CutPrefix(s, "anthropic/"); ok {
		return "anthropic", after
	}
	if after, ok := strings.CutPrefix(s, "gemini/"); ok {
		return "gemini", after
	}
	if after, ok := strings.CutPrefix(s, "ollama/"); ok {
		return "ollama", after
	}
	if after, ok := strings.CutPrefix(s, "local/"); ok {
		return "ollama", after
	}
	return "", s
}

func canonicalProvider(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "local":
		return "ollama"
	default:
		return strings.ToLower(strings.TrimSpace(s))
	}
}
