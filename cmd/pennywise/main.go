// Pennywise — single-tenant, self-hosted personal expense tracker.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Arthurobo/pennywise/internal/cli"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := cli.ExecuteWithContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "pennywise: %v\n", err)
		os.Exit(1)
	}
}
