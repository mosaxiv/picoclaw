package main

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"
)

func cmdChannels() *cli.Command {
	return &cli.Command{
		Name:  "channels",
		Usage: "channel utilities",
		Commands: []*cli.Command{
			{
				Name:  "status",
				Usage: "show configured channel enablement",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, _, err := loadConfig()
					if err != nil {
						return err
					}
					fmt.Printf("discord.enabled=%v\n", cfg.Channels.Discord.Enabled)
					fmt.Printf("slack.enabled=%v\n", cfg.Channels.Slack.Enabled)
					return nil
				},
			},
		},
	}
}
