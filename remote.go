package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type remoteResult struct {
	Client             string   `json:"client,omitempty"`
	ID                 string   `json:"id,omitempty"`
	Title              string   `json:"title,omitempty"`
	Status             string   `json:"status"`
	Message            string   `json:"message"`
	App                string   `json:"app,omitempty"`
	LookFor            string   `json:"look_for,omitempty"`
	Window             string   `json:"window,omitempty"`
	Notices            []string `json:"notices,omitempty"`
	EndpointOverridden bool     `json:"endpoint_overridden,omitempty"`
}

type remoteOptions struct {
	ConfirmClosed bool
}

var remoteStatuses = map[string]struct {
	exit    int
	message string
}{
	"started":                    {0, "Continue on your phone: open the Claude app, tap Code, and look for this session."},
	"already_remote":             {0, "This session is already available on your phone."},
	"ready":                      {0, "Continue on your phone: open the ChatGPT app, go to Codex, and look for this session. If this Mac isn't listed, pair once at the laptop with `codex remote-control pair`."},
	"ready_warning":              {0, "Continue on your phone in the ChatGPT app under Codex. This session also looks open in a terminal, so messages from the phone and the terminal can diverge."},
	"stopped":                    {0, "Stopped the remote session."},
	"unconfirmed":                {3, "Claude started, but Remote Control did not confirm the connection in time. Check the Claude app, or stop the session with `agents remote --stop`."},
	"unknown_session":            {2, "No saved session has this ID. Run `agents-list` to find it."},
	"ambiguous_session":          {2, "This ID matches sessions in more than one client."},
	"unsupported_client":         {2, "No mobile app can drive local OpenCode or Cursor sessions."},
	"not_remote":                 {2, "This session is not running remotely."},
	"open_in_agents_window":      {4, "This session is open in a tmux window that agents opened. Close that window first."},
	"open_elsewhere":             {4, "This session is open in another terminal. Run /rc inside it to continue on your phone, or close it first."},
	"maybe_open":                 {4, "A Claude session is running in this project and may be this one. Run /rc inside it to continue on your phone, or close it and retry with --confirm-closed."},
	"consent_required":           {5, "Remote Control needs a one-time confirmation. At the laptop, run `claude --remote-control` once and accept it."},
	"login_required":             {5, "Claude is not signed in to claude.ai. At the laptop, run `claude` and use /login."},
	"trust_required":             {5, "Claude has not trusted this folder yet. At the laptop, open Claude there once and accept the trust prompt."},
	"api_key_auth":               {5, "Remote Control needs a claude.ai login, but Claude is configured to use an API key (ANTHROPIC_API_KEY or apiKeyHelper)."},
	"remote_control_unavailable": {5, "Claude reported that Remote Control is unavailable for this setup."},
	"startup_failed":             {5, "Claude exited during startup."},
	"daemon_not_running":         {5, "The Codex remote-control daemon is not running. At the laptop, run `codex remote-control start`, and pair once with `codex remote-control pair`."},
	"remote_control_disabled":    {5, "The Codex daemon is running without remote control. At the laptop, run `codex remote-control start`."},
	"client_missing":             {5, "The client is not installed or is missing from PATH."},
	"tmux_missing":               {5, "tmux 3.2 or later is required to continue Claude sessions on the phone."},
	"tmux_failed":                {5, "tmux could not create the remote window."},
	"directory_missing":          {5, "The session's project directory no longer exists."},
}

func newResult(row session, status string) remoteResult {
	return remoteResult{Client: row.Client, ID: row.ID, Title: limit(row.name(), 120), Status: status, Message: remoteStatuses[status].message}
}

func limit(value string, size int) string {
	runes := []rune(clean(value))
	if len(runes) > size {
		return string(runes[:size])
	}
	return string(runes)
}

var loadIndex = func() ([]session, error) {
	loaded := readSessions()
	return loaded.sessions, loaded.err
}

func handoff(row session, options remoteOptions) remoteResult {
	switch row.Client {
	case "claude":
		return claudeHandoff(row, options)
	case "codex":
		return codexHandoff(row)
	default:
		return newResult(row, "unsupported_client")
	}
}

func stopRemote(row session) remoteResult {
	window, found := remoteWindowFor(row.Client + ":" + row.ID)
	if !found {
		return newResult(row, "not_remote")
	}
	if _, err := tmux("kill-window", "-t", window.ID); err != nil {
		return newResult(row, "tmux_failed")
	}
	result := newResult(row, "stopped")
	logRemote(result)
	return result
}

func runRemote(args []string, stdout io.Writer) int {
	var asJSON, list, stop, confirmClosed bool
	var id string
	for _, arg := range args {
		switch arg {
		case "--json":
			asJSON = true
		case "--list":
			list = true
		case "--stop":
			stop = true
		case "--confirm-closed":
			confirmClosed = true
		default:
			if strings.HasPrefix(arg, "-") || id != "" {
				fmt.Fprintln(stdout, remoteUsage)
				return 2
			}
			id = arg
		}
	}
	if list {
		return printRemoteList(stdout, asJSON)
	}
	if id == "" {
		fmt.Fprintln(stdout, remoteUsage)
		return 2
	}
	row, result := findSession(id)
	if result.Status == "" {
		if stop {
			result = stopRemote(row)
		} else {
			result = handoff(row, remoteOptions{ConfirmClosed: confirmClosed})
		}
	}
	printRemote(stdout, result, asJSON)
	return remoteStatuses[result.Status].exit
}

const remoteUsage = "usage: agents remote <session-id> [--json] [--confirm-closed]\n       agents remote --stop <session-id> [--json]\n       agents remote --list [--json]"

func findSession(id string) (session, remoteResult) {
	rows, err := loadIndex()
	var matches []session
	if err == nil {
		for _, row := range rows {
			if row.ID == id {
				matches = append(matches, row)
			}
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], remoteResult{}
	case 0:
		return session{}, remoteResult{Status: "unknown_session", Message: remoteStatuses["unknown_session"].message}
	}
	clients := make([]string, len(matches))
	for i, row := range matches {
		clients[i] = row.Client
	}
	result := remoteResult{Status: "ambiguous_session", Message: remoteStatuses["ambiguous_session"].message + " Clients: " + strings.Join(clients, ", ") + "."}
	return session{}, result
}

func printRemote(out io.Writer, result remoteResult, asJSON bool) {
	if asJSON {
		data, _ := json.MarshalIndent(result, "", "  ")
		fmt.Fprintln(out, string(data))
		return
	}
	fmt.Fprintln(out, result.Message)
	if result.App != "" {
		fmt.Fprintln(out, "  App: "+result.App)
	}
	if result.LookFor != "" {
		fmt.Fprintln(out, "  Look for: "+result.LookFor)
	}
	for _, notice := range result.Notices {
		fmt.Fprintln(out, "  Note: "+notice)
	}
}

type remoteListing struct {
	Client string `json:"client"`
	ID     string `json:"id"`
	Title  string `json:"title"`
	Window string `json:"window"`
}

func windowListings() []remoteListing {
	listings := []remoteListing{}
	for _, window := range liveRemoteWindows() {
		client, id, _ := strings.Cut(window.Key, ":")
		listings = append(listings, remoteListing{Client: client, ID: id, Window: window.ID})
	}
	return listings
}

func remoteListings() []remoteListing {
	titles := map[string]string{}
	if rows, err := loadIndex(); err == nil {
		for _, row := range rows {
			titles[row.Client+":"+row.ID] = limit(row.name(), 120)
		}
	}
	listings := windowListings()
	for i := range listings {
		listings[i].Title = titles[listings[i].Client+":"+listings[i].ID]
	}
	return listings
}

func printRemoteList(out io.Writer, asJSON bool) int {
	listings := remoteListings()
	if asJSON {
		data, _ := json.MarshalIndent(listings, "", "  ")
		fmt.Fprintln(out, string(data))
		return 0
	}
	if len(listings) == 0 {
		fmt.Fprintln(out, "No remote sessions.")
	}
	for _, listing := range listings {
		fmt.Fprintf(out, "%-7s %s  %s\n", listing.Client, listing.ID, listing.Title)
	}
	return 0
}

func stateDir() (string, error) {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".local", "state")
	}
	dir = filepath.Join(dir, "agents")
	return dir, os.MkdirAll(dir, 0o700)
}

func lockRemote() (func(), error) {
	dir, err := stateDir()
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, "remote.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
	}, nil
}

const remoteLogLimit = 1 << 20

func logRemote(result remoteResult) {
	dir, err := stateDir()
	if err != nil {
		return
	}
	path := filepath.Join(dir, "remote.log")
	if info, err := os.Stat(path); err == nil && info.Size() >= remoteLogLimit {
		os.Rename(path, path+".1")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	action := "handoff"
	if result.Status == "stopped" {
		action = "stop"
	}
	entry, _ := json.Marshal(map[string]any{"time": time.Now().Format(time.RFC3339), "action": action, "client": result.Client,
		"id": result.ID, "status": result.Status, "endpoint_overridden": result.EndpointOverridden})
	file.Write(append(entry, '\n'))
}
