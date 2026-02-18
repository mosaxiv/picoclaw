package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/mosaxiv/clawlet/paths"
	"github.com/urfave/cli/v3"
)

func cmdStatus() *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "print effective configuration status",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, cfgPath, err := loadConfig()
			if err != nil {
				return err
			}
			fmt.Printf("config: %s\n", cfgPath)
			fmt.Printf("workspace: %s\n", paths.WorkspaceDir())
			fmt.Printf("llm.provider: %s\n", cfg.LLM.Provider)
			fmt.Printf("llm.baseURL: %s\n", cfg.LLM.BaseURL)
			fmt.Printf("llm.model: %s\n", cfg.LLM.Model)
			if strings.TrimSpace(cfg.Agents.Defaults.Model) != "" {
				fmt.Printf("agents.defaults.model: %s\n", cfg.Agents.Defaults.Model)
			}
			fmt.Printf("agents.defaults.maxTokens: %d\n", cfg.Agents.Defaults.MaxTokensValue())
			fmt.Printf("agents.defaults.temperature: %.2f\n", cfg.Agents.Defaults.TemperatureValue())
			fmt.Printf("tools.restrictToWorkspace: %v\n", cfg.Tools.RestrictToWorkspaceValue())
			fmt.Printf("tools.exec.timeoutSec: %d\n", cfg.Tools.Exec.TimeoutSec)
			fmt.Printf("tools.web.braveApiKey: %v\n", cfg.Tools.Web.BraveAPIKey != "")
			fmt.Printf("cron.enabled: %v\n", cfg.Cron.EnabledValue())
			fmt.Printf("heartbeat.enabled: %v\n", cfg.Heartbeat.EnabledValue())
			fmt.Printf("heartbeat.intervalSec: %d\n", cfg.Heartbeat.IntervalSec)
			fmt.Printf("gateway.listen: %s\n", cfg.Gateway.Listen)
			fmt.Printf("gateway.allowPublicBind: %v\n", cfg.Gateway.AllowPublicBind)
			fmt.Printf("channels.discord.enabled: %v\n", cfg.Channels.Discord.Enabled)
			fmt.Printf("channels.slack.enabled: %v\n", cfg.Channels.Slack.Enabled)
			fmt.Printf("channels.telegram.enabled: %v\n", cfg.Channels.Telegram.Enabled)
			fmt.Printf("channels.whatsapp.enabled: %v\n", cfg.Channels.WhatsApp.Enabled)
			return nil
		},
	}
}
