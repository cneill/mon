package main

import "github.com/urfave/cli/v3"

func allFlags() []cli.Flag {
	flags := make([]cli.Flag, 0, len(generalFlags()))
	flags = append(flags, generalFlags()...)
	flags = append(flags, projectFlags()...)

	return flags
}

const (
	FlagDebug   = "debug"
	EnvDebug    = "MON_DEBUG"
	FlagNoColor = "no-color"
	EnvNoColor  = "MON_NO_COLOR"
)

func generalFlags() []cli.Flag {
	category := "general"

	return []cli.Flag{
		&cli.BoolFlag{
			Name:     FlagDebug,
			Aliases:  []string{"D"},
			Category: category,
			Sources:  cli.EnvVars(EnvDebug),
			Value:    false,
			Usage:    "Write debug logs to a file (mon_debug.log) in current directory.",
		},
		&cli.BoolFlag{
			Name:     FlagNoColor,
			Aliases:  []string{"C"},
			Category: category,
			Sources:  cli.EnvVars(EnvNoColor),
			Value:    false,
			Usage:    "Disable coloration.",
		},
	}
}

const (
	FlagProjectDir = "project-dir"
	EnvProjectDir  = "MON_PROJECT_DIR"
)

func projectFlags() []cli.Flag {
	category := "project"

	return []cli.Flag{
		&cli.StringFlag{
			Name:     FlagProjectDir,
			Aliases:  []string{"d"},
			Category: category,
			Sources:  cli.EnvVars(category),
			Value:    ".",
			Usage:    "The `DIRECTORY` you want to monitor.",
		},
	}
}
