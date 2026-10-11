package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"sandboxctl/cmd/guest/bootstrap/guestbootstrap"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := guestbootstrap.Run(ctx); err != nil {
		slog.Error("guest bootstrap failed", "error", err)
		os.Exit(1)
	}
}
