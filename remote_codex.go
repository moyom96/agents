package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"time"
)

var codexTimeout = 10 * time.Second

func codexHandoff(row session) remoteResult {
	if _, err := exec.LookPath("codex"); err != nil {
		return newResult(row, "client_missing")
	}
	if !codexDaemonRunning() {
		return newResult(row, "daemon_not_running")
	}
	rows, err := listProcesses()
	if err != nil {
		rows = nil
	}
	table := newProcessTable(rows)
	daemon := remoteControlDaemon(table)
	if daemon == 0 {
		return newResult(row, "remote_control_disabled")
	}
	status := "ready"
	if state := findOpenSession(row, table, map[int]bool{daemon: true}); state.resumedPID != 0 || state.sameCwd {
		status = "ready_warning"
	}
	result := newResult(row, status)
	result.App, result.LookFor = "ChatGPT app → Codex", codexLookFor(row)
	if keepAwake() {
		keepDaemonAwake(table, daemon)
	}
	logRemote(result)
	return result
}

func codexDaemonRunning() bool {
	ctx, cancel := context.WithTimeout(context.Background(), codexTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, "codex", "app-server", "daemon", "version").Output()
	var version struct {
		Status string `json:"status"`
	}
	return err == nil && json.Unmarshal(output, &version) == nil && version.Status == "running"
}

func remoteControlDaemon(table processTable) int {
	for _, proc := range table.rows {
		if len(proc.Args) > 2 && filepath.Base(proc.Args[0]) == "codex" && proc.Args[1] == "app-server" && slices.Contains(proc.Args, "--remote-control") {
			return proc.PID
		}
	}
	return 0
}

func codexLookFor(row session) string {
	if title := limit(row.Title, 80); title != "" {
		return title + " (" + shortID(row.ID) + ")"
	}
	return filepath.Base(row.projectPath()) + " · " + row.LastActivity.Local().Format("02/01/2006 15:04") + " (" + shortID(row.ID) + ")"
}

func keepDaemonAwake(table processTable, daemon int) {
	pid := strconv.Itoa(daemon)
	for _, proc := range table.rows {
		if filepath.Base(proc.Args[0]) == "caffeinate" && slices.Contains(proc.Args, "-w") && slices.Contains(proc.Args, pid) {
			return
		}
	}
	cmd := exec.Command("caffeinate", "-i", "-w", pid)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if cmd.Start() == nil {
		cmd.Process.Release()
	}
}
