package main

import (
	"context"
	"fmt"
	"github.com/kylemclaren/sprite-tunnel/cmd"
	"os"
	"os/signal"
	"syscall"
)

var version = "dev"
var commit = "unknown"
var date = "unknown"

func main() {
	cmd.Version, cmd.Commit, cmd.BuildDate = version, commit, date
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := cmd.Run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sprite-tunnel:", err)
		os.Exit(1)
	}
}
