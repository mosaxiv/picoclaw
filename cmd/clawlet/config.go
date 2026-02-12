package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mosaxiv/clawlet/config"
	"github.com/mosaxiv/clawlet/paths"
)

func loadConfig() (*config.Config, string, error) {
	cfgPath, err := paths.ConfigPath()
	if err != nil {
		return nil, "", err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, cfgPath, fmt.Errorf("failed to load config: %s\nhint: run `clawlet onboard`\n%w", cfgPath, err)
	}

	applyEnvOverrides(cfg)
	cfg.ApplyLLMRouting()

	if strings.TrimSpace(cfg.LLM.APIKey) == "" && providerNeedsAPIKey(cfg.LLM.Provider) {
		fmt.Fprintln(os.Stderr, "warning: llm.apiKey is empty (set in config.env or env vars)")
	}

	return cfg, cfgPath, nil
}

func applyEnvOverrides(cfg *config.Config) {
	if v := os.Getenv("CLAWLET_API_KEY"); v != "" {
		cfg.LLM.APIKey = v
	}
	if v := os.Getenv("CLAWLET_BASE_URL"); v != "" {
		cfg.LLM.BaseURL = v
	}
	if v := os.Getenv("CLAWLET_MODEL"); v != "" {
		cfg.Agents.Defaults.Model = v
	}
	if v := os.Getenv("CLAWLET_OPENAI_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["OPENAI_API_KEY"] = v
	}
	if v := os.Getenv("CLAWLET_OPENROUTER_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["OPENROUTER_API_KEY"] = v
	}
	if v := os.Getenv("CLAWLET_ANTHROPIC_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["ANTHROPIC_API_KEY"] = v
	}
	if v := os.Getenv("CLAWLET_GEMINI_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["GEMINI_API_KEY"] = v
	}
	if v := os.Getenv("CLAWLET_GOOGLE_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["GOOGLE_API_KEY"] = v
	}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["OPENAI_API_KEY"] = v
	}
	if v := os.Getenv("OPENROUTER_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["OPENROUTER_API_KEY"] = v
	}
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["ANTHROPIC_API_KEY"] = v
	}
	if v := os.Getenv("GEMINI_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["GEMINI_API_KEY"] = v
	}
	if v := os.Getenv("GOOGLE_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["GOOGLE_API_KEY"] = v
	}

	if cfg.LLM.Headers == nil {
		cfg.LLM.Headers = map[string]string{}
	}
}

func resolveWorkspace(flagValue string) (string, error) {
	ws := strings.TrimSpace(flagValue)
	if ws == "" {
		if v := strings.TrimSpace(os.Getenv("CLAWLET_WORKSPACE")); v != "" {
			ws = v
		} else {
			ws = paths.WorkspaceDir()
		}
	}
	return filepath.Abs(ws)
}

func providerNeedsAPIKey(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "ollama":
		return false
	default:
		return true
	}
}
