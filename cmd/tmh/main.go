package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/AllenReder/tmh/internal/cli"
)

func main() {
	ctx, stop := notifyContext(context.Background())
	defer stop()

	os.Exit(cli.Run(ctx, os.Args, os.Stdin, os.Stdout, os.Stderr))
}

func notifyContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
}
