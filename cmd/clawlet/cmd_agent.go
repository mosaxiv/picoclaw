package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mosaxiv/clawlet/agent"
	"github.com/urfave/cli/v3"
)

func cmdAgent() *cli.Command {
	return &cli.Command{
		Name:  "agent",
		Usage: "run an agent in CLI mode",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "message", Aliases: []string{"m"}, Usage: "single message (non-interactive)"},
			&cli.StringFlag{Name: "session", Aliases: []string{"s"}, Value: "cli:default", Usage: "session key"},
			&cli.StringFlag{Name: "workspace", Usage: "workspace directory (default: ~/.clawlet/workspace or CLAWLET_WORKSPACE)"},
			&cli.IntFlag{Name: "max-iters", Value: 20, Usage: "max tool-call iterations"},
			&cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}, Usage: "verbose (print tool calls)"},
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

			a, err := agent.New(agent.Options{
				Config:       cfg,
				WorkspaceDir: wsAbs,
				SessionKey:   cmd.String("session"),
				MaxIters:     cmd.Int("max-iters"),
				Verbose:      cmd.Bool("verbose"),
			})
			if err != nil {
				return err
			}

			msg := cmd.String("message")
			if msg != "" {
				out, err := a.Process(ctx, msg)
				if err != nil {
					return err
				}
				fmt.Println(out)
				return nil
			}

			in := bufio.NewScanner(os.Stdin)
			fmt.Printf("workspace: %s\nsession: %s\n(type /exit to quit)\n", wsAbs, cmd.String("session"))
			for {
				fmt.Print("> ")
				if !in.Scan() {
					break
				}
				line := strings.TrimSpace(in.Text())
				if line == "" {
					continue
				}
				if line == "/exit" || line == "/quit" {
					break
				}
				start := time.Now()
				out, err := a.Process(ctx, line)
				if err != nil {
					fmt.Fprintln(os.Stderr, "error:", err)
					continue
				}
				fmt.Println(out)
				if cmd.Bool("verbose") {
					fmt.Fprintf(os.Stderr, "(took %s)\n", time.Since(start).Truncate(time.Millisecond))
				}
			}
			return in.Err()
		},
	}
}
