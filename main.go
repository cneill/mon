package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/urfave/cli/v3"

	"github.com/cneill/mon/internal/version"
	"github.com/cneill/mon/pkg/mon"
	"github.com/fatih/color"
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
	color.NoColor = cmd.Bool(FlagNoColor)

	if cmd.Bool(FlagDebug) {
		file, err := setupLogging(cmd)
		if err != nil {
			return fmt.Errorf("failed to set up logging: %w", err)
		}

		defer file.Close()
	}

	rawProjectDir := cmd.String(FlagProjectDir)

	projectDir, err := filepath.Abs(filepath.Clean(rawProjectDir))
	if err != nil {
		return fmt.Errorf("invalid project path %q: %w", rawProjectDir, err)
	}

	opts := &mon.Opts{
		NoColor:    cmd.Bool(FlagNoColor),
		ProjectDir: projectDir,
	}

	mon, err := mon.New(opts) //nolint:contextcheck
	if err != nil {
		return fmt.Errorf("failed to set up mon: %w", err)
	}

	defer mon.Teardown()

	if err := mon.Run(ctx); err != nil {
		return fmt.Errorf("mon run error: %w", err)
	}

	return nil
}

func setupLogging(cmd *cli.Command) (*os.File, error) {
	level := slog.LevelInfo
	if cmd.Bool(FlagDebug) {
		level = slog.LevelDebug
	}

	var (
		logFileName = "mon_debug.log"
		err         error
	)

	logFile, err := os.OpenFile(logFileName, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	sync.OnceFunc(func() {
		handler := slog.NewJSONHandler(logFile, &slog.HandlerOptions{AddSource: true, Level: level})
		logger := slog.New(handler)
		slog.SetDefault(logger)
	})()

	return logFile, nil
}

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatalf("ERROR: %v", err)
	}
}
