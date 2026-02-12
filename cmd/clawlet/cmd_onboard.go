package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"text/template"
	"time"

	"github.com/mosaxiv/clawlet/paths"
	"github.com/urfave/cli/v3"
)

//go:embed templates/*.tmpl
var onboardTemplates embed.FS

func cmdOnboard() *cli.Command {
	return &cli.Command{
		Name:  "onboard",
		Usage: "initialize config and workspace scaffolding",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "overwrite", Usage: "overwrite existing config if present"},
			&cli.StringFlag{Name: "workspace", Usage: "workspace directory to initialize (default: ~/.clawlet/workspace or CLAWLET_WORKSPACE)"},
			&cli.StringFlag{Name: "model", Usage: "set agents.defaults.model (e.g. openrouter/anthropic/claude-sonnet-4-5)"},
			&cli.StringFlag{Name: "openrouter-api-key", Usage: "write env.OPENROUTER_API_KEY into config.json"},
			&cli.StringFlag{Name: "openai-api-key", Usage: "write env.OPENAI_API_KEY into config.json"},
			&cli.StringFlag{Name: "anthropic-api-key", Usage: "write env.ANTHROPIC_API_KEY into config.json"},
			&cli.StringFlag{Name: "gemini-api-key", Usage: "write env.GEMINI_API_KEY into config.json"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfgPath, err := paths.ConfigPath()
			if err != nil {
				return err
			}

			if _, err := os.Stat(cfgPath); err == nil && !cmd.Bool("overwrite") {
				fmt.Printf("config already exists: %s\n(use --overwrite to replace)\n", cfgPath)
				return nil
			}

			model := cmd.String("model")
			orKey := cmd.String("openrouter-api-key")
			oaKey := cmd.String("openai-api-key")
			anthropicKey := cmd.String("anthropic-api-key")
			geminiKey := cmd.String("gemini-api-key")
			if err := saveMinimalConfig(cfgPath, model, orKey, oaKey, anthropicKey, geminiKey); err != nil {
				return err
			}

			if err := paths.EnsureStateDirs(); err != nil {
				return err
			}

			wsAbs, err := resolveWorkspace(cmd.String("workspace"))
			if err != nil {
				return err
			}
			if err := initWorkspace(wsAbs); err != nil {
				return err
			}

			fmt.Printf("initialized:\n- config: %s\n- sessions: %s\n- workspace: %s\n", cfgPath, paths.SessionsDir(), wsAbs)
			return nil
		},
	}
}

func saveMinimalConfig(path string, model string, openrouterKey string, openaiKey string, anthropicKey string, geminiKey string) error {
	root := map[string]any{}

	env := map[string]string{}
	if openrouterKey != "" {
		env["OPENROUTER_API_KEY"] = openrouterKey
	}
	if openaiKey != "" {
		env["OPENAI_API_KEY"] = openaiKey
	}
	if anthropicKey != "" {
		env["ANTHROPIC_API_KEY"] = anthropicKey
	}
	if geminiKey != "" {
		env["GEMINI_API_KEY"] = geminiKey
	}
	if len(env) > 0 {
		root["env"] = env
	}

	if model != "" {
		root["agents"] = map[string]any{
			"defaults": map[string]any{
				"model": model,
			},
		}
	}

	// If no flags are provided, write an empty object and let clawlet defaults apply.
	if len(root) == 0 {
		root = map[string]any{}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o600)
}

func initWorkspace(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, "memory"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o755); err != nil {
		return err
	}

	data := struct {
		AgentName         string
		HeartbeatInterval string
	}{
		AgentName:         "clawlet",
		HeartbeatInterval: "30m",
	}

	files := []struct {
		OutName  string
		TmplPath string
	}{
		{OutName: "AGENTS.md", TmplPath: "templates/AGENTS.md.tmpl"},
		{OutName: "SOUL.md", TmplPath: "templates/SOUL.md.tmpl"},
		{OutName: "USER.md", TmplPath: "templates/USER.md.tmpl"},
		{OutName: "TOOLS.md", TmplPath: "templates/TOOLS.md.tmpl"},
		{OutName: "IDENTITY.md", TmplPath: "templates/IDENTITY.md.tmpl"},
		{OutName: "HEARTBEAT.md", TmplPath: "templates/HEARTBEAT.md.tmpl"},
	}

	for _, f := range files {
		outPath := filepath.Join(dir, f.OutName)
		if _, err := os.Stat(outPath); err == nil {
			continue
		}
		b, err := onboardTemplates.ReadFile(f.TmplPath)
		if err != nil {
			return err
		}
		tpl, err := template.New(filepath.Base(f.TmplPath)).Option("missingkey=error").Parse(string(b))
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		if err := tpl.Execute(&buf, data); err != nil {
			return err
		}
		_ = os.WriteFile(outPath, buf.Bytes(), 0o644)
	}

	mem := filepath.Join(dir, "memory", "MEMORY.md")
	if _, err := os.Stat(mem); err != nil {
		_ = os.WriteFile(mem, []byte("# Long-term Memory\n\n"), 0o644)
	}
	today := filepath.Join(dir, "memory", time.Now().Format("2006-01-02")+".md")
	if _, err := os.Stat(today); err != nil {
		_ = os.WriteFile(today, []byte("# "+time.Now().Format("2006-01-02")+"\n\n"), 0o644)
	}
	return nil
}
