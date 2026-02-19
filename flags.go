package main

import (
	"github.com/cneill/mon/internal/config"
	"github.com/urfave/cli/v3"
)

func allFlags() []cli.Flag {
	flags := make([]cli.Flag, 0, len(generalFlags()))
	flags = append(flags, generalFlags()...)
	flags = append(flags, detailsFlags()...)

	return flags
}

const (
	FlagConfig  = "config"
	EnvConfig   = "MON_CONFIG"
	FlagDebug   = "debug"
	EnvDebug    = "MON_DEBUG"
	FlagNoColor = "no-color"
	EnvNoColor  = "MON_NO_COLOR"
	FlagAudio   = "audio"
	EnvAudio    = "MON_AUDIO"
)

func generalFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    FlagConfig,
			Aliases: []string{"c"},
			Sources: cli.EnvVars(EnvConfig),
			Value:   config.DefaultConfigPath(),
			Usage:   "Path to the mon configuration file.",
		},
		&cli.BoolFlag{
			Name:    FlagDebug,
			Aliases: []string{"D"},
			Sources: cli.EnvVars(EnvDebug),
			Value:   false,
			Usage:   "Write debug logs to a file (mon_debug.log) in current directory.",
		},
		&cli.BoolFlag{
			Name:    FlagNoColor,
			Aliases: []string{"C"},
			Sources: cli.EnvVars(EnvNoColor),
			Value:   false,
			Usage:   "Disable coloration.",
		},
		&cli.BoolFlag{
			Name:    FlagAudio,
			Aliases: []string{"A"},
			Sources: cli.EnvVars(EnvAudio),
			Value:   false,
			Usage:   "Enable audio notifications for events.",
		},
	}
}

const (
	FlagShowAllFiles = "all-files"
	EnvShowAllFiles  = "MON_SHOW_ALL_FILES"
)

func detailsFlags() []cli.Flag {
	category := "details"

	return []cli.Flag{
		&cli.BoolFlag{
			Name:     FlagShowAllFiles,
			Category: category,
			Aliases:  []string{"F"},
			Sources:  cli.EnvVars(EnvShowAllFiles),
			Value:    false,
			Usage:    "Show all new, deleted, and written file paths in final session stats.",
		},
	}
}
