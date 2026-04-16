package app

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// WaitForShutdown blocks until SIGINT or SIGTERM is received,
// then cancels the returned context. Use this in the composition root.
func WaitForShutdown() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		sig := <-ch
		slog.Info("shutdown signal received", "signal", sig.String())
		cancel()
	}()

	return ctx, cancel
}
