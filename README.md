# mon

What are those coding agents up to, after all?

## Installation

**With Go:**

```bash
go install github.com/cneill/mon@latest
```

[**Or grab the latest release**](https://github.com/cneill/mon/releases/latest)

## Stats tracked

* Files created/deleted
* Lines added/deleted in git commits
* Git commits
* Untracked changes
* File write counts
* Dependencies added/deleted/modified
    * Golang (go.mod)
    * NPM (package.json)
    * Python (requirements.txt / pyproject.toml)

## Flags / Arguments

```
USAGE:
   mon [global options] [PROJECT_DIRECTORY]

GLOBAL OPTIONS:
   --debug, -D     Write debug logs to a file (mon_debug.log) in current directory. [$MON_DEBUG]
   --help, -h      show help
   --no-color, -C  Disable coloration. [$MON_NO_COLOR]
   --version, -v   print the version

   details

   --all-files, -F  Show all new, deleted, and written file paths in final session stats. [$MON_SHOW_ALL_FILES]
```

## Screenshots

**While running:**

![Status line](img/status_line.png)

**On exit:**

![Final status](img/final_status.png)
