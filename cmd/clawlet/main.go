package main

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
)

func main() {
	root := &cli.Command{
		Name:  "clawlet",
		Usage: "minimal Go agent",
		Commands: []*cli.Command{
			cmdOnboard(),
			cmdStatus(),
			cmdAgent(),
			cmdGateway(),
			cmdChannels(),
			cmdCron(),
		},
	}

	if err := root.Run(context.Background(), os.Args); err != nil {
		cli.HandleExitCoder(err)
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
