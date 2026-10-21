package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type sessionOpened struct {
	name   string
	target string
	err    error
}

func quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func resumeArgs(row session) ([]string, error) {
	if row.ID == "" {
		return nil, fmt.Errorf("this session has no ID")
	}
	switch row.Client {
	case "claude":
		return []string{"claude", "--resume", row.ID}, nil
	case "codex":
		return []string{"codex", "resume", row.ID}, nil
	case "opencode":
		return []string{"opencode", "--session", row.ID}, nil
	case "cursor":
		return []string{"cursor-agent", "--resume", row.ID}, nil
	default:
		return nil, fmt.Errorf("unsupported client: %s", clean(row.Client))
	}
}

func windowArgs(row session, target, shell string, resume []string) []string {
	return []string{"new-window", "-t", target + ":", "-n", clean(row.Client + ":" + filepath.Base(row.Cwd)),
		"-c", row.Cwd, "--", shell, "-lic", resumeCommand(shell, resume)}
}

func resumeCommand(shell string, resume []string) string {
	quoted := make([]string, len(resume))
	for i, arg := range resume {
		quoted[i] = quote(arg)
	}
	return strings.Join(quoted, " ") + "; exec " + quote(shell) + " -l"
}

func openSession(row session) sessionOpened {
	fail := func(err error) sessionOpened { return sessionOpened{err: err} }
	config, err := loadSettings()
	if err != nil {
		return fail(err)
	}
	pane := os.Getenv("TMUX_PANE")
	if os.Getenv("TMUX") == "" || pane == "" {
		return fail(fmt.Errorf("start this browser inside tmux to open a session"))
	}
	info, err := os.Stat(row.Cwd)
	if err != nil || !info.IsDir() {
		return fail(fmt.Errorf("project directory is unavailable: %s", clean(row.Cwd)))
	}
	args, err := resumeArgs(row)
	if err != nil {
		return fail(err)
	}
	if _, err := exec.LookPath(args[0]); err != nil {
		return fail(fmt.Errorf("%s is not installed or is missing from PATH", row.Client))
	}
	target, err := exec.Command("tmux", "display-message", "-p", "-t", pane, "#{session_id}").Output()
	if err != nil {
		return fail(fmt.Errorf("cannot find the current tmux session: %w", err))
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	if config.Launch.Target == "terminal-tab" {
		if err := openTerminalTab(row, shell, args); err != nil {
			return fail(err)
		}
		return sessionOpened{name: row.name(), target: config.Launch.Target}
	}
	output, err := exec.Command("tmux", windowArgs(row, strings.TrimSpace(string(target)), shell, args)...).CombinedOutput()
	if err != nil {
		return fail(fmt.Errorf("open tmux window: %v. %s", err, clean(string(output))))
	}
	return sessionOpened{name: row.name(), target: config.Launch.Target}
}
