package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mosaxiv/clawlet/cron"
	"github.com/mosaxiv/clawlet/paths"
	"github.com/urfave/cli/v3"
)

func cmdCron() *cli.Command {
	return &cli.Command{
		Name:  "cron",
		Usage: "manage scheduled jobs",
		Commands: []*cli.Command{
			cronListCmd(),
			cronAddCmd(),
			cronRemoveCmd(),
			cronToggleCmd(),
			cronRunCmd(),
		},
	}
}

func cronListCmd() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "list jobs",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			_, _, err := loadConfig()
			if err != nil {
				return err
			}
			svc := cron.NewService(paths.CronStorePath(), nil)
			jobs := svc.List(true)
			if len(jobs) == 0 {
				fmt.Println("No jobs.")
				return nil
			}
			for _, j := range jobs {
				fmt.Printf("- %s id=%s enabled=%v kind=%s next=%d\n", j.Name, j.ID, j.Enabled, j.Schedule.Kind, j.State.NextRunAtMS)
			}
			return nil
		},
	}
}

func cronAddCmd() *cli.Command {
	return &cli.Command{
		Name:  "add",
		Usage: "add a job",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "name", Usage: "job name"},
			&cli.StringFlag{Name: "message", Usage: "message for agent", Required: true},
			&cli.IntFlag{Name: "every", Usage: "run every N seconds"},
			&cli.StringFlag{Name: "cron", Usage: "cron expression (5-field)"},
			&cli.StringFlag{Name: "at", Usage: "run once at time (RFC3339)"},
			&cli.BoolFlag{Name: "deliver", Value: true, Usage: "deliver response to a channel"},
			&cli.StringFlag{Name: "channel", Usage: "delivery channel (e.g. discord, slack)"},
			&cli.StringFlag{Name: "to", Usage: "delivery chat/user id"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			_, _, err := loadConfig()
			if err != nil {
				return err
			}

			message := strings.TrimSpace(cmd.String("message"))
			jname := strings.TrimSpace(cmd.String("name"))
			if jname == "" {
				jname = message
			}

			var sched cron.Schedule
			switch {
			case cmd.Int("every") > 0:
				sched = cron.Schedule{Kind: "every", EveryMS: int64(cmd.Int("every")) * 1000}
			case strings.TrimSpace(cmd.String("cron")) != "":
				sched = cron.Schedule{Kind: "cron", Expr: strings.TrimSpace(cmd.String("cron"))}
			case strings.TrimSpace(cmd.String("at")) != "":
				t, err := time.Parse(time.RFC3339, strings.TrimSpace(cmd.String("at")))
				if err != nil {
					return err
				}
				sched = cron.Schedule{Kind: "at", AtMS: t.UnixMilli()}
			default:
				return cli.Exit("one of --every/--cron/--at is required", 2)
			}

			payload := cron.Payload{
				Kind:    "agent_turn",
				Message: message,
				Deliver: cmd.Bool("deliver"),
				Channel: cmd.String("channel"),
				To:      cmd.String("to"),
			}

			svc := cron.NewService(paths.CronStorePath(), nil)
			j, err := svc.Add(jname, sched, payload)
			if err != nil {
				return err
			}
			fmt.Printf("Created job %s (id=%s)\n", j.Name, j.ID)
			return nil
		},
	}
}

func cronRemoveCmd() *cli.Command {
	return &cli.Command{
		Name:      "remove",
		Usage:     "remove a job",
		ArgsUsage: "<job_id>",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			_, _, err := loadConfig()
			if err != nil {
				return err
			}
			if cmd.Args().Len() < 1 {
				return cli.Exit("usage: clawlet cron remove <job_id>", 2)
			}
			id := cmd.Args().Get(0)
			svc := cron.NewService(paths.CronStorePath(), nil)
			if svc.Remove(id) {
				fmt.Println("Removed:", id)
			} else {
				fmt.Println("Not found:", id)
			}
			return nil
		},
	}
}

func cronToggleCmd() *cli.Command {
	return &cli.Command{
		Name:      "toggle",
		Usage:     "enable or disable a job",
		ArgsUsage: "<job_id>",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "disable", Usage: "disable instead of enable"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			_, _, err := loadConfig()
			if err != nil {
				return err
			}
			if cmd.Args().Len() < 1 {
				return cli.Exit("usage: clawlet cron toggle [--disable] <job_id>", 2)
			}
			id := cmd.Args().Get(0)
			svc := cron.NewService(paths.CronStorePath(), nil)
			if svc.Toggle(id, cmd.Bool("disable")) {
				if cmd.Bool("disable") {
					fmt.Println("Disabled:", id)
				} else {
					fmt.Println("Enabled:", id)
				}
			} else {
				fmt.Println("Not found:", id)
			}
			return nil
		},
	}
}

func cronRunCmd() *cli.Command {
	return &cli.Command{
		Name:      "run",
		Usage:     "trigger a job immediately",
		ArgsUsage: "<job_id>",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "force", Usage: "run even if disabled"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			_, _, err := loadConfig()
			if err != nil {
				return err
			}
			if cmd.Args().Len() < 1 {
				return cli.Exit("usage: clawlet cron run [--force] <job_id>", 2)
			}
			id := cmd.Args().Get(0)
			svc := cron.NewService(paths.CronStorePath(), nil)
			_, err = svc.RunNow(ctx, id, cmd.Bool("force"))
			if err != nil {
				return err
			}
			fmt.Println("Triggered:", id)
			return nil
		},
	}
}
