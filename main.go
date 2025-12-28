package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/cneill/mon/internal/version"
	"github.com/cneill/mon/pkg/mon"
)

func run(ctx context.Context) error {
	cmd := cli.Command{
		Name:    "mon",
		Version: version.String(),
		Flags:   allFlags(),
		Action:  setupMon,
	}

	if err := cmd.Run(ctx, os.Args); err != nil {
		return fmt.Errorf("command: %w", err)
	}

	return nil
}

func setupMon(ctx context.Context, cmd *cli.Command) error {
	opts := &mon.Opts{
		GitWatch:   cmd.Bool(FlagGitWatch),
		NoColor:    cmd.Bool(FlagNoColor),
		ProjectDir: cmd.String(FlagProjectDir),
	}

	mon, err := mon.New(opts)
	if err != nil {
		return err
	}

	if err := mon.Run(ctx); err != nil {
		return fmt.Errorf("mon error: %w", err)
	}

	return nil
}

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatalf("ERROR: %v", err)
	}
}
