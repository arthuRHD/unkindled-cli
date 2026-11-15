package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/arthuRHD/unkindled-cli/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(app.New().Run(ctx, os.Args[1:]))
}
