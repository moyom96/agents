package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

const remoteSession = "agents-remote"

var remoteTmuxSocket = ""

type remoteWindow struct {
	ID, Key string
	Dead    bool
	PanePID int
}

func tmux(args ...string) (string, error) {
	if remoteTmuxSocket != "" {
		args = append([]string{"-S", remoteTmuxSocket}, args...)
	}
	cmd := exec.Command("tmux", args...)
	cmd.Env = withoutEnv(os.Environ(), "TMUX", "TMUX_PANE")
	output, err := cmd.Output()
	return strings.TrimSpace(string(output)), err
}

func withoutEnv(environ []string, names ...string) []string {
	kept := make([]string, 0, len(environ))
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		drop := false
		for _, removed := range names {
			drop = drop || name == removed
		}
		if !drop {
			kept = append(kept, entry)
		}
	}
	return kept
}

var tmuxVersion = regexp.MustCompile(`(\d+)\.(\d+)`)

func tmuxSupported() bool {
	if _, err := exec.LookPath("tmux"); err != nil {
		return false
	}
	output, err := exec.Command("tmux", "-V").Output()
	match := tmuxVersion.FindStringSubmatch(string(output))
	if err != nil || match == nil {
		return false
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	return major > 3 || major == 3 && minor >= 2
}

func remoteWindows() []remoteWindow {
	if _, err := tmux("has-session", "-t", "="+remoteSession); err != nil {
		return nil
	}
	output, err := tmux("list-windows", "-t", "="+remoteSession, "-F", "#{window_id}\t#{@agents-remote}\t#{pane_dead}\t#{pane_pid}")
	if err != nil {
		return nil
	}
	var windows []remoteWindow
	for line := range strings.SplitSeq(output, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != 4 || fields[1] == "" {
			continue
		}
		pid, _ := strconv.Atoi(fields[3])
		windows = append(windows, remoteWindow{ID: fields[0], Key: fields[1], Dead: fields[2] == "1", PanePID: pid})
	}
	return windows
}

func liveRemoteWindows() []remoteWindow {
	var live []remoteWindow
	for _, window := range remoteWindows() {
		if window.Dead {
			tmux("kill-window", "-t", window.ID)
			continue
		}
		live = append(live, window)
	}
	return live
}

func remoteWindowFor(key string) (remoteWindow, bool) {
	for _, window := range liveRemoteWindows() {
		if window.Key == key {
			return window, true
		}
	}
	return remoteWindow{}, false
}

func createRemoteWindow(name, cwd string, payload []string) (string, error) {
	command := []string{"new-window", "-d", "-P", "-F", "#{window_id}", "-t", "=" + remoteSession + ":", "-n", name, "-c", cwd, "--"}
	if _, err := tmux("has-session", "-t", "="+remoteSession); err != nil {
		command = []string{"new-session", "-d", "-P", "-F", "#{window_id}", "-s", remoteSession, "-x", "200", "-y", "50", "-n", name, "-c", cwd, "--"}
	}
	id, err := tmux(append(command, payload...)...)
	if err != nil || !strings.HasPrefix(id, "@") {
		return "", fmt.Errorf("tmux could not create the remote window")
	}
	return id, nil
}

func tagWindow(id, option, value string) error {
	_, err := tmux("set-option", "-w", "-t", id, option, value)
	return err
}

type localWindow struct {
	ID      string
	PanePID int
}

func localWindows(key string) []localWindow {
	output, err := tmux("list-panes", "-a", "-F", "#{window_id}\t#{@agents-local}\t#{pane_pid}")
	if err != nil {
		return nil
	}
	var windows []localWindow
	for line := range strings.SplitSeq(output, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) == 3 && fields[1] == key {
			pid, _ := strconv.Atoi(fields[2])
			windows = append(windows, localWindow{ID: fields[0], PanePID: pid})
		}
	}
	return windows
}
