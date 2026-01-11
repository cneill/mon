package main

import "github.com/urfave/cli/v3"

func allFlags() []cli.Flag {
	flags := make([]cli.Flag, 0, len(generalFlags()))
	flags = append(flags, generalFlags()...)

	return flags
}

const (
	FlagDebug   = "debug"
	EnvDebug    = "MON_DEBUG"
	FlagNoColor = "no-color"
	EnvNoColor  = "MON_NO_COLOR"
)

func generalFlags() []cli.Flag {
	return []cli.Flag{
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
	}
}
