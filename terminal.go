package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const newAlacrittyTab = `on run argv
tell application "System Events"
    set appProcess to first application process whose unix id is (item 1 of argv as integer)
    set frontmost of appProcess to true
    tell appProcess to keystroke "t" using command down
end tell
end run`

func tmuxClients(ctx context.Context) (map[string]string, error) {
	output, err := exec.CommandContext(ctx, "tmux", "list-clients", "-F", "#{client_name}\t#{client_pid}").Output()
	if err != nil {
		return nil, fmt.Errorf("list tmux clients: %w", err)
	}
	clients := make(map[string]string)
	for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		if name, pid, ok := strings.Cut(line, "\t"); ok {
			clients[name] = pid
		}
	}
	return clients, nil
}

func belongsToProcess(ctx context.Context, pid, ancestor string) bool {
	for range 12 {
		if pid == ancestor {
			return true
		}
		if n, err := strconv.Atoi(pid); err != nil || n <= 1 {
			return false
		}
		output, err := exec.CommandContext(ctx, "ps", "-p", pid, "-o", "ppid=").Output()
		if err != nil {
			return false
		}
		pid = strings.TrimSpace(string(output))
	}
	return false
}

func openTerminalTab(row session, shell string, resume []string) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("terminal-tab currently supports Alacritty on macOS; use tmux-window here")
	}
	socket := filepath.Base(os.Getenv("ALACRITTY_SOCKET"))
	appPID := strings.TrimSuffix(strings.TrimPrefix(socket, "Alacritty-"), ".sock")
	if n, err := strconv.Atoi(appPID); err != nil || n <= 1 || socket != "Alacritty-"+appPID+".sock" {
		return fmt.Errorf("terminal-tab requires Alacritty on macOS with its default IPC socket")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	before, err := tmuxClients(ctx)
	if err != nil {
		return err
	}
	output, err := exec.CommandContext(ctx, "osascript", "-e", newAlacrittyTab, appPID).CombinedOutput()
	if err != nil {
		if strings.Contains(string(output), "not allowed") || strings.Contains(string(output), "not authorized") {
			return fmt.Errorf("macOS blocked tab opening; allow Alacritty in Privacy & Security > Accessibility / Automation")
		}
		return fmt.Errorf("open Alacritty tab: %v. %s", err, clean(string(output)))
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("new tab did not attach to tmux within 10 seconds; terminal-tab requires Alacritty's startup shell to attach to the same tmux server")
		case <-ticker.C:
			after, err := tmuxClients(ctx)
			if err != nil {
				return err
			}
			client, err := newTabClient(before, after, func(pid string) bool { return belongsToProcess(ctx, pid, appPID) })
			if err != nil {
				return err
			}
			if client == "" {
				continue
			}
			command := "cd " + quote(row.Cwd) + " && exec /usr/bin/env -u TMUX -u TMUX_PANE " + quote(shell) + " -lic " + quote(resumeCommand(shell, resume))
			output, err := exec.CommandContext(ctx, "tmux", "detach-client", "-t", client, "-E", command).CombinedOutput()
			if err != nil {
				return fmt.Errorf("resume in new tab: %v. %s", err, clean(string(output)))
			}
			return nil
		}
	}
}

func newTabClient(before, after map[string]string, belongs func(string) bool) (string, error) {
	selected := ""
	for name, pid := range after {
		if _, exists := before[name]; exists || !belongs(pid) {
			continue
		}
		if selected != "" {
			return "", fmt.Errorf("multiple new Alacritty tabs attached to tmux; no session was resumed, try again")
		}
		selected = name
	}
	return selected, nil
}
