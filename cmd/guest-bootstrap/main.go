package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"sandboxctl/internal/guestbootstrap"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := guestbootstrap.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "sandboxctl-bootstrap:", err)
		os.Exit(1)
	}
	log.Print("sandboxctl-bootstrap: guest configuration is complete")
}
