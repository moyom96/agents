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
	"usage_error":                {2, remoteUsage},
	"open_in_agents_window":      {4, "This session is open in a tmux window that agents opened. Close that window first."},
	"open_elsewhere":             {4, "This session is open in another terminal. Run /rc inside it to continue on your phone, or close it first."},
	"maybe_open":                 {4, "A Claude session is running in this project and may be this one. Run /rc inside it to continue on your phone, or close it and retry with --confirm-closed."},
	"consent_required":           {5, "Remote Control needs a one-time confirmation. At the laptop, run `claude --remote-control` once and accept it."},
	"login_required":             {5, "Claude is not signed in to claude.ai. At the laptop, run `claude` and use /login."},
	"trust_required":             {5, "Claude has not trusted this folder yet. At the laptop, open Claude there once and accept the trust prompt."},
	"api_key_auth":               {5, "Remote Control needs a claude.ai login, but Claude is configured to get its API key from apiKeyHelper."},
	"index_unavailable":          {5, "Could not read saved sessions. Run `agents-list` to see the error."},
	"scan_failed":                {5, "Could not check whether this session is open elsewhere, so it was not handed off."},
	"busy":                       {5, "Another handoff is still starting. Try again in a moment."},
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
	window, found := remoteWindowFor(sessionKey(row))
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

type remoteCommand struct {
	asJSON, list, stop, confirmClosed, help bool
	id                                      string
}

func parseRemote(args []string) (remoteCommand, bool) {
	var command remoteCommand
	for _, arg := range args {
		switch arg {
		case "--json":
			command.asJSON = true
		case "--list":
			command.list = true
		case "--stop":
			command.stop = true
		case "--confirm-closed":
			command.confirmClosed = true
		case "-h", "--help":
			command.help = true
		default:
			if strings.HasPrefix(arg, "-") || command.id != "" {
				return command, false
			}
			command.id = arg
		}
	}
	if command.help {
		return command, true
	}
	if command.list {
		return command, command.id == "" && !command.stop && !command.confirmClosed
	}
	return command, command.id != "" && !(command.stop && command.confirmClosed)
}

func runRemote(args []string, stdout, stderr io.Writer) int {
	command, valid := parseRemote(args)
	switch {
	case command.help:
		fmt.Fprintln(stdout, remoteUsage+"\n\n"+remoteHelp)
		return 0
	case !valid && command.asJSON:
		printRemote(stdout, remoteResult{Status: "usage_error", Message: remoteUsage}, true)
		return 2
	case !valid:
		fmt.Fprintln(stderr, remoteUsage)
		return 2
	case command.list:
		return printRemoteList(stdout, command.asJSON)
	}
	row, result := findSession(command.id)
	switch {
	case command.stop && result.Status != "":
		result = stopByID(command.id, result)
	case command.stop:
		result = stopRemote(row)
	case result.Status == "":
		result = handoff(row, remoteOptions{ConfirmClosed: command.confirmClosed})
	}
	printRemote(stdout, result, command.asJSON)
	return remoteStatuses[result.Status].exit
}

func stopByID(id string, lookup remoteResult) remoteResult {
	for _, window := range liveRemoteWindows() {
		if client, windowID, _ := strings.Cut(window.Key, ":"); windowID == id {
			return stopRemote(session{Client: client, ID: id})
		}
	}
	return lookup
}

const remoteUsage = "usage: agents remote <session-id> [--json] [--confirm-closed]\n       agents remote --stop <session-id> [--json]\n       agents remote --list [--json]"

const remoteHelp = `Continue a saved Claude Code or Codex session from its phone app.

--json prints status, message, app, look_for, notices, and window.
--confirm-closed hands off even though a Claude session runs in the same
project; pass it only when that session is closed or is a different one.

Exit codes: 0 continue on the phone (started, already_remote, ready,
ready_warning, stopped); 2 bad usage, unknown or unsupported session;
3 unconfirmed; 4 open elsewhere; 5 a prerequisite is missing.
See docs/mobile-handoff.md.`

func findSession(id string) (session, remoteResult) {
	rows, err := loadIndex()
	if err != nil {
		return session{}, remoteResult{ID: id, Status: "index_unavailable", Message: remoteStatuses["index_unavailable"].message}
	}
	var matches []session
	for _, row := range rows {
		if row.ID == id {
			matches = append(matches, row)
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
			titles[sessionKey(row)] = limit(row.name(), 120)
		}
	}
	listings := windowListings()
	for i := range listings {
		listings[i].Title = titles[sessionKey(session{Client: listings[i].Client, ID: listings[i].ID})]
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

var lockTimeout = 45 * time.Second

func lockRemote() (func(), string) {
	dir, err := stateDir()
	if err != nil {
		return nil, "tmux_failed"
	}
	file, err := os.OpenFile(filepath.Join(dir, "remote.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, "tmux_failed"
	}
	deadline := time.Now().Add(lockTimeout)
	for syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		if time.Now().After(deadline) {
			file.Close()
			return nil, "busy"
		}
		time.Sleep(100 * time.Millisecond)
	}
	return func() {
		syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		file.Close()
	}, ""
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
