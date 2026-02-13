package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/mosaxiv/clawlet/agent"
	"github.com/mosaxiv/clawlet/bus"
	"github.com/mosaxiv/clawlet/channels"
	"github.com/mosaxiv/clawlet/channels/discord"
	"github.com/mosaxiv/clawlet/channels/slack"
	"github.com/mosaxiv/clawlet/channels/telegram"
	"github.com/mosaxiv/clawlet/channels/whatsapp"
	"github.com/mosaxiv/clawlet/cron"
	"github.com/mosaxiv/clawlet/heartbeat"
	"github.com/mosaxiv/clawlet/paths"
	"github.com/mosaxiv/clawlet/session"
	"github.com/urfave/cli/v3"
)

func cmdGateway() *cli.Command {
	return &cli.Command{
		Name:  "gateway",
		Usage: "run the long-lived agent gateway (channels + cron + heartbeat)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "workspace", Usage: "workspace directory (default: ~/.clawlet/workspace or CLAWLET_WORKSPACE)"},
			&cli.IntFlag{Name: "max-iters", Value: 20, Usage: "max tool-call iterations"},
			&cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}, Usage: "verbose"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, _, err := loadConfig()
			if err != nil {
				return err
			}

			wsAbs, err := resolveWorkspace(cmd.String("workspace"))
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
			defer stop()

			b := bus.New(256)
			smgr := session.NewManager(paths.SessionsDir())

			var cronSvc *cron.Service
			if cfg.Cron.EnabledValue() {
				cronSvc = cron.NewService(paths.CronStorePath(), func(ctx context.Context, job cron.Job) (string, error) {
					if job.Payload.Kind != "" && job.Payload.Kind != "agent_turn" {
						return "", nil
					}
					ch := job.Payload.Channel
					to := job.Payload.To
					if !job.Payload.Deliver || strings.TrimSpace(ch) == "" || strings.TrimSpace(to) == "" {
						return "", nil
					}
					_ = b.PublishInbound(ctx, bus.InboundMessage{
						Channel:    ch,
						SenderID:   "cron:" + job.ID,
						ChatID:     to,
						Content:    job.Payload.Message,
						SessionKey: ch + ":" + to,
					})
					return "", nil
				})
			}

			loop, err := agent.NewLoop(agent.LoopOptions{
				Config:       cfg,
				WorkspaceDir: wsAbs,
				Model:        cfg.LLM.Model,
				MaxIters:     cmd.Int("max-iters"),
				Bus:          b,
				Sessions:     smgr,
				Cron:         cronSvc,
				Spawn:        nil,
				Verbose:      cmd.Bool("verbose"),
			})
			if err != nil {
				return err
			}

			sa := agent.NewSubagentManager(loop)
			loop.SetSpawn(sa.Spawn)

			if cronSvc != nil {
				if err := cronSvc.Start(ctx); err != nil {
					return err
				}
			}

			hb := heartbeat.New(wsAbs, heartbeat.Options{
				Enabled:     cfg.Heartbeat.EnabledValue(),
				IntervalSec: cfg.Heartbeat.IntervalSec,
				OnHeartbeat: func(ctx context.Context, prompt string) (string, error) {
					return loop.ProcessDirect(ctx, prompt, "heartbeat", "cli", "heartbeat")
				},
			})
			hb.Start(ctx)

			cm := channels.NewManager(b)
			if cfg.Channels.Discord.Enabled {
				cm.Add(discord.New(cfg.Channels.Discord, b))
			}
			var sl *slack.Channel
			if cfg.Channels.Slack.Enabled {
				if strings.TrimSpace(cfg.Channels.Slack.BotToken) == "" {
					return fmt.Errorf("slack enabled but botToken is empty")
				}
				if strings.TrimSpace(cfg.Channels.Slack.AppToken) == "" {
					return fmt.Errorf("slack enabled but appToken is empty")
				}
				sl = slack.New(cfg.Channels.Slack, b)
				cm.Add(sl)
			}
			if cfg.Channels.Telegram.Enabled {
				if strings.TrimSpace(cfg.Channels.Telegram.Token) == "" {
					return fmt.Errorf("telegram enabled but token is empty")
				}
				cm.Add(telegram.New(cfg.Channels.Telegram, b))
			}
			if cfg.Channels.WhatsApp.Enabled {
				cm.Add(whatsapp.New(cfg.Channels.WhatsApp, b))
			}

			if err := cm.StartAll(ctx); err != nil {
				return err
			}

			go func() { _ = loop.Run(ctx) }()

			fmt.Printf("gateway running\n- workspace: %s\n- sessions: %s\n", wsAbs, paths.SessionsDir())
			fmt.Println("stop: Ctrl+C")
			<-ctx.Done()

			_ = cm.StopAll()
			if cronSvc != nil {
				cronSvc.Stop()
			}
			hb.Stop()
			return nil
		},
	}
}
