package proc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

var (
	ErrPermissions = errors.New("missing necessary permissions")
	ErrNoProcess   = errors.New("no process with that PID")
)

/*
Processes to watch:
* those in cwd (could be top-level agents, editors, etc)
* children of these ^ that might be operating in a separate dir (e.g. git worktree)
* should de-dupe children - don't pull info twice for PID=X if it is also a child of PID=Y
* filter out non-agents (claude, cline, opencode, etc) / non-editors (vim, nvim, vscode, whatever)
*/

type MonitorOpts struct {
	ProjectDir string
}

func (m *MonitorOpts) OK() error {
	if m.ProjectDir == "" {
		return fmt.Errorf("must supply project dir")
	}

	return nil
}

type Monitor struct {
	projectDir string

	mutex            sync.RWMutex
	inaccessiblePIDs map[int32]struct{}
}

func NewMonitor(opts *MonitorOpts) (*Monitor, error) {
	if err := opts.OK(); err != nil {
		return nil, fmt.Errorf("monitor options error: %w", err)
	}

	monitor := &Monitor{
		projectDir: opts.ProjectDir,

		inaccessiblePIDs: map[int32]struct{}{},
	}

	return monitor, nil
}

func (m *Monitor) Run(_ context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		processes, err := process.Processes()
		if err != nil {
			slog.Error("failed to list running processes", "error", err)
			return
		}

		for _, process := range processes {
			if m.ignoredPID(process.Pid) {
				continue
			}

			info, err := getBasicProcessInfo(process)
			if err != nil {
				m.ignorePID(process.Pid)
				slog.Error("failed to retrieve information about process", "pid", process, "error", err)

				continue
			}

			if !strings.HasPrefix(info.CWD, m.projectDir) {
				m.ignorePID(process.Pid)
				continue
			}

			// TODO: ignore non-editors / non-agents

			if err := info.AddDetails(); err != nil {
				m.ignorePID(process.Pid)
				slog.Error("failed to retrivee more detailed info about process", "pid", process, "error", err)

				continue
			}

			fmt.Println(info.String())
		}

		fmt.Println("\n===============")
	}
}

type ProcessInfo struct {
	underlying *process.Process

	PID         int32
	Name        string
	CWD         string
	CommandLine []string
	Connections []net.ConnectionStat
	Children    []*ProcessInfo
}

func (p *ProcessInfo) String() string {
	return processInfoStr(p, 0)
}

func (p *ProcessInfo) AddDetails() error {
	cmdline, err := p.underlying.CmdlineSlice()
	if err = handleError("command line", err); err != nil {
		return err
	}

	p.CommandLine = cmdline

	connections, err := p.underlying.Connections()
	if err = handleError("network connections", err); err != nil {
		return err
	}

	p.Connections = connections

	children, err := p.underlying.Children()
	if err = handleError("children", err); err != nil {
		return err
	}

	childSlice := make([]*ProcessInfo, 0, len(children))

	for _, child := range children {
		info, err := getBasicProcessInfo(child)
		if err != nil {
			slog.Error("failed to get info about child process", "pid", child.Pid, "error", err)
			continue
		}

		if err := info.AddDetails(); err != nil {
			slog.Error("failed to get more info about child process", "pid", child.Pid, "error", err)
			continue
		}

		childSlice = append(childSlice, info)
	}

	p.Children = childSlice

	return nil
}

func (m *Monitor) ignorePID(pid int32) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	m.inaccessiblePIDs[pid] = struct{}{}
}

func (m *Monitor) ignoredPID(pid int32) bool {
	m.mutex.RLock()
	defer m.mutex.RUnlock()

	_, ok := m.inaccessiblePIDs[pid]

	return ok
}

func getBasicProcessInfo(process *process.Process) (*ProcessInfo, error) {
	name, err := process.Name()
	if err = handleError("name", err); err != nil {
		return nil, err
	}

	cwd, err := process.Cwd()
	if err = handleError("cwd", err); err != nil {
		return nil, err
	}

	info := &ProcessInfo{
		underlying: process,
		PID:        process.Pid,
		Name:       name,
		CWD:        cwd,
	}

	return info, nil
}

func handleError(operation string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, process.ErrorNotPermitted):
		return fmt.Errorf("failed to get %s: %w", operation, ErrPermissions)
	case errors.Is(err, process.ErrorProcessNotRunning):
		return fmt.Errorf("failed to get %s: %w", operation, ErrNoProcess)
	}

	return fmt.Errorf("failed to get %s: %w", operation, err)
}

func processInfoStr(info *ProcessInfo, level int) string {
	builder := &strings.Builder{}
	indentStr := strings.Repeat(" ", level*2)

	builder.WriteString(indentStr + "PID: " + strconv.FormatInt(int64(info.PID), 10) + "\n")
	builder.WriteString(indentStr + "  Name: " + info.Name + "\n")
	builder.WriteString(indentStr + "  CWD: " + info.CWD + "\n")
	builder.WriteString(indentStr + "  Commandline: " + strings.Join(info.CommandLine, " ") + "\n")

	if len(info.Children) > 0 {
		builder.WriteString(indentStr + "  Children:\n")

		for _, child := range info.Children {
			builder.WriteString(processInfoStr(child, level+2))
		}

		builder.WriteString(indentStr + "\n\n")
	}

	return builder.String()
}
