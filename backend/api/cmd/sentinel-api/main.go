// Command sentinel-api runs Sentinel's API and its maintenance subcommands.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/Gigman2/sentinel/backend/api/internal/db"
)

const usage = `usage: sentinel-api <command>

commands:
  migrate   apply pending database migrations (reads DATABASE_URL)`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "migrate":
		err = runMigrate(ctx)
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func runMigrate(ctx context.Context) error {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("DATABASE_URL is not set")
	}
	results, err := db.MigrateWithResults(ctx, dsn)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		fmt.Println("no pending migrations")
		return nil
	}
	for _, r := range results {
		fmt.Printf("applied %s (%s)\n", r.Source.Path, r.Duration)
	}
	return nil
}
