// Command migrate applies the database migrations to DATABASE_URL (also read from .env).
//
//	go run ./cmd/migrate up          apply every pending migration
//	go run ./cmd/migrate down [N]    revert the latest N migrations (default 1)
//	go run ./cmd/migrate version     print the current version
//	go run ./cmd/migrate force V     mark version V as applied after fixing a dirty database by hand
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"

	"github.com/joho/godotenv"

	"dashboardgo/internal/migrate"
)

const usage = "usage: migrate up | down [N] | version | force V"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("load .env: %w", err)
	}
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return errors.New("DATABASE_URL is required")
	}
	if len(args) == 0 {
		return errors.New(usage)
	}

	m, err := migrate.New(dsn)
	if err != nil {
		return err
	}
	defer func() { _ = m.Close() }()

	switch args[0] {
	case "up":
		err = m.Up()
	case "down":
		steps := 1
		if len(args) > 1 {
			if steps, err = strconv.Atoi(args[1]); err != nil {
				return fmt.Errorf("down: N must be a whole number, got %q", args[1])
			}
		}
		err = m.Down(steps)
	case "version":
	case "force":
		if len(args) < 2 {
			return errors.New("force: version required")
		}
		v, convErr := strconv.Atoi(args[1])
		if convErr != nil {
			return fmt.Errorf("force: version must be a whole number, got %q", args[1])
		}
		err = m.Force(v)
	default:
		return fmt.Errorf("unknown command %q; %s", args[0], usage)
	}

	if errors.Is(err, migrate.ErrNoChange) {
		fmt.Println("no change")
	} else if err != nil {
		return err
	}

	v, dirty, err := m.Version()
	if err != nil {
		return err
	}
	fmt.Printf("version %d (dirty: %t)\n", v, dirty)
	return nil
}
