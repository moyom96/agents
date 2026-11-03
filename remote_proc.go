package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

type process struct {
	PID, PPID int
	Args      []string
}

var listProcesses = func() ([]process, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,args=").Output()
	if err != nil {
		return nil, err
	}
	var rows []process
	for line := range strings.SplitSeq(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		ppid, ppidErr := strconv.Atoi(fields[1])
		if pidErr == nil && ppidErr == nil {
			rows = append(rows, process{PID: pid, PPID: ppid, Args: fields[2:]})
		}
	}
	return rows, nil
}

var processCwd = func(pid int) string {
	if runtime.GOOS == "linux" {
		path, _ := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd")
		return path
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, "lsof", "-a", "-d", "cwd", "-p", strconv.Itoa(pid), "-Fn").Output()
	if err != nil {
		return ""
	}
	for line := range strings.SplitSeq(string(output), "\n") {
		if path, ok := strings.CutPrefix(line, "n"); ok {
			return path
		}
	}
	return ""
}

type processTable struct {
	rows    []process
	parents map[int]int
}

func newProcessTable(rows []process) processTable {
	parents := make(map[int]int, len(rows))
	for _, row := range rows {
		parents[row.PID] = row.PPID
	}
	return processTable{rows: rows, parents: parents}
}

func (t processTable) descendsFrom(pid int, ancestors map[int]bool) bool {
	for range 64 {
		if ancestors[pid] {
			return true
		}
		parent, found := t.parents[pid]
		if !found || parent <= 1 || parent == pid {
			return false
		}
		pid = parent
	}
	return false
}

func (t processTable) ancestorsOf(pid int) map[int]bool {
	ancestors := map[int]bool{}
	for range 64 {
		ancestors[pid] = true
		parent, found := t.parents[pid]
		if !found || parent <= 1 || parent == pid {
			break
		}
		pid = parent
	}
	return ancestors
}

var resumeFlags = map[string]bool{"--resume": true, "-r": true, "resume": true, "--session": true, "--session-id": true}

func resumedSession(args []string) (string, bool) {
	for i, arg := range args {
		if id, ok := strings.CutPrefix(arg, "--resume="); ok {
			return id, true
		}
		if resumeFlags[arg] && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func clientProcess(args []string, client string) bool {
	if len(args) == 0 {
		return false
	}
	program := filepath.Base(args[0])
	if program == "node" && len(args) > 1 {
		program = filepath.Base(args[1])
	}
	return program == client && !slices.Contains(args, "remote-control") && !slices.Contains(args, "app-server")
}

type openState struct {
	resumedPID int
	sameCwd    bool
}

func findOpenSession(row session, table processTable, exclude map[int]bool) openState {
	self := table.ancestorsOf(os.Getpid())
	var state openState
	for _, proc := range table.rows {
		if self[proc.PID] && !clientProcess(proc.Args, row.Client) || table.descendsFrom(proc.PID, exclude) {
			continue
		}
		id, resumed := resumedSession(proc.Args)
		if resumed && id == row.ID {
			return openState{resumedPID: proc.PID}
		}
		if !resumed && !state.sameCwd && clientProcess(proc.Args, row.Client) && samePath(processCwd(proc.PID), row.Cwd) {
			state.sameCwd = true
		}
	}
	return state
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(a); err == nil {
		a = resolved
	}
	if resolved, err := filepath.EvalSymlinks(b); err == nil {
		b = resolved
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
