// Command seed fills the database at DATABASE_URL with development data.
//
//	go run ./cmd/seed --preset=screenshot           the numbers from the dashboard screenshots (Oct 2026)
//	go run ./cmd/seed --preset=demo --months=12     generated history ending today, reproducible with --seed
//	add --reset to empty every table first
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/joho/godotenv"

	"dashboardgo/internal/config"
	"dashboardgo/internal/database"
	"dashboardgo/internal/devdata"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run() error {
	preset := flag.String("preset", devdata.PresetScreenshot, "screenshot or demo")
	months := flag.Int("months", 12, fmt.Sprintf("demo: months of history (%d-%d)", devdata.MinDemoMonths, devdata.MaxDemoMonths))
	seed := flag.Uint64("seed", 42, "demo: random seed; the same seed gives the same data")
	reset := flag.Bool("reset", false, "empty every table before seeding")
	flag.Parse()

	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("load .env: %w", err)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.IsProduction() {
		return errors.New("refusing to seed with APP_ENV=production")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := database.NewPool(ctx, database.PoolConfig{URL: cfg.DatabaseURL, MaxConns: 2, MinConns: 0})
	if err != nil {
		return err
	}
	defer pool.Close()

	summary, err := devdata.Seed(ctx, pool, devdata.SeedOptions{
		Preset: *preset,
		Reset:  *reset,
		Demo:   devdata.DemoOptions{Months: *months, Seed: *seed, Today: devdata.Today(cfg.BusinessTZ)},
	})
	if err != nil {
		return err
	}

	fmt.Printf("seeded preset %q\n", *preset)
	for _, t := range summary {
		fmt.Printf("  %-15s %6d\n", t.Table, t.Rows)
	}
	return nil
}
