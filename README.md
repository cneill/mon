# mon

Track what your agent(s) do while you're not watching.

## Installation

**With Go:**

```bash
go install github.com/cneill/mon@latest
```

[**Grab the latest release**](https://github.com/cneill/mon/releases/latest)

## Flags

```
GLOBAL OPTIONS:
   --help, -h     show help
   --version, -v  print the version

   general

   --debug, -D     Write debug logs to a file (mon_debug.log) in current directory. [$MON_DEBUG]
   --no-color, -C  Disable coloration. [$MON_NO_COLOR]

   project

   --project-dir DIRECTORY, -d DIRECTORY  The DIRECTORY you want to monitor. (default: ".") [$project]
```

## Screenshots

**While running:**

![Status line](img/status_line.png)

**On exit:**

![Final status](img/final_status.png)
