package main

import (
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
)

const officialEndpoint = "https://api.anthropic.com"

var (
	startupTimeout = 20 * time.Second
	startupPoll    = 500 * time.Millisecond
	keepAwake      = func() bool {
		_, err := exec.LookPath("caffeinate")
		return runtime.GOOS == "darwin" && err == nil
	}
	nestedAgentEnv = []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_SSE_PORT", "TMUX", "TMUX_PANE"}
)

const endpointNotice = "Your custom Claude API endpoint is bypassed for this session, so features it adds (such as a compression proxy) are off."

type claudeAuth struct {
	customEndpoint, apiKey, gatewayAuth bool
}

type claudeSettings struct {
	Env          map[string]any `json:"env"`
	APIKeyHelper string         `json:"apiKeyHelper"`
}

func claudeConfig(cwd string) claudeAuth {
	home := os.Getenv("CLAUDE_CONFIG_DIR")
	if home == "" {
		if user, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(user, ".claude")
		}
	}
	env := map[string]string{}
	for _, name := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_CUSTOM_HEADERS"} {
		env[name] = os.Getenv(name)
	}
	helper := false
	for _, path := range []string{filepath.Join(home, "settings.json"), filepath.Join(cwd, ".claude", "settings.json"), filepath.Join(cwd, ".claude", "settings.local.json")} {
		var settings claudeSettings
		if data, err := os.ReadFile(path); err != nil || json.Unmarshal(data, &settings) != nil {
			continue
		}
		helper = helper || settings.APIKeyHelper != ""
		for name := range env {
			if value, ok := settings.Env[name].(string); ok {
				env[name] = value
			}
		}
	}
	return claudeAuth{
		customEndpoint: env["ANTHROPIC_BASE_URL"] != "" && endpointHost(env["ANTHROPIC_BASE_URL"]) != "api.anthropic.com",
		apiKey:         env["ANTHROPIC_API_KEY"] != "" || helper,
		gatewayAuth:    env["ANTHROPIC_AUTH_TOKEN"] != "" || env["ANTHROPIC_CUSTOM_HEADERS"] != "",
	}
}

func endpointHost(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return parsed.Hostname()
}

func endpointOverride(auth claudeAuth) string {
	env := map[string]string{"ANTHROPIC_BASE_URL": officialEndpoint}
	if auth.gatewayAuth {
		env["ANTHROPIC_AUTH_TOKEN"], env["ANTHROPIC_CUSTOM_HEADERS"] = "", ""
	}
	data, _ := json.Marshal(map[string]any{"env": env})
	return string(data)
}

func remoteName(row session) string {
	title := clean(row.Title)
	if title == "" {
		title = shortID(row.ID)
	}
	raw := "claude · " + filepath.Base(row.projectPath()) + " · " + title
	safe := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(" -_.·:/", r) {
			return r
		}
		return -1
	}, raw)
	return limit(strings.TrimLeft(strings.Join(strings.Fields(safe), " "), "-"), 60)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func claudePayload(row session, name, override, shell string, awake bool) []string {
	payload := []string{"/usr/bin/env"}
	for _, variable := range nestedAgentEnv {
		payload = append(payload, "-u", variable)
	}
	payload = append(payload, shell, "-lic", `exec "$@"`, remoteSession)
	if awake {
		payload = append(payload, "caffeinate", "-i")
	}
	return append(payload, "claude", "--settings", override, "--resume", row.ID, "--remote-control", name)
}

func claudeHandoff(row session, options remoteOptions) remoteResult {
	if failure := claudePreflight(row); failure != "" {
		return newResult(row, failure)
	}
	auth := claudeConfig(row.Cwd)
	if auth.apiKey {
		return newResult(row, "api_key_auth")
	}
	unlock, err := lockRemote()
	if err != nil {
		return newResult(row, "tmux_failed")
	}
	window, status := claudeWindow(row, options, auth)
	unlock()
	if status != "" {
		result := newResult(row, status)
		result.Window = window
		return result
	}
	result := newResult(row, waitForRemoteControl(window))
	if result.Status == "started" || result.Status == "unconfirmed" {
		result.App, result.LookFor, result.EndpointOverridden = "Claude app → Code", remoteName(row), auth.customEndpoint
		if auth.customEndpoint {
			result.Notices = append(result.Notices, endpointNotice)
		}
		logRemote(result)
	} else {
		tmux("kill-window", "-t", window)
	}
	return result
}

func claudePreflight(row session) string {
	if _, err := exec.LookPath("claude"); err != nil {
		return "client_missing"
	}
	if info, err := os.Stat(row.Cwd); err != nil || !info.IsDir() {
		return "directory_missing"
	}
	if !tmuxSupported() {
		return "tmux_missing"
	}
	return ""
}

func claudeWindow(row session, options remoteOptions, auth claudeAuth) (string, string) {
	key := row.Client + ":" + row.ID
	if window, found := remoteWindowFor(key); found {
		return window.ID, "already_remote"
	}
	if window, status := openElsewhere(row, key, options); status != "" {
		return window, status
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	name := remoteName(row)
	window, err := createRemoteWindow(name, row.Cwd, claudePayload(row, name, endpointOverride(auth), shell, keepAwake()))
	if err != nil {
		return "", "tmux_failed"
	}
	tagWindow(window, "remain-on-exit", "on")
	if err := tagWindow(window, "@agents-remote", key); err != nil {
		tmux("kill-window", "-t", window)
		return "", "tmux_failed"
	}
	return window, ""
}

func openElsewhere(row session, key string, options remoteOptions) (string, string) {
	rows, err := listProcesses()
	if err != nil {
		return "", ""
	}
	table := newProcessTable(rows)
	exclude := map[int]bool{}
	for _, window := range remoteWindows() {
		exclude[window.PanePID] = true
	}
	state := findOpenSession(row, table, exclude)
	if state.resumedPID != 0 {
		for _, window := range localWindows(key) {
			if table.descendsFrom(state.resumedPID, map[int]bool{window.PanePID: true}) {
				return window.ID, "open_in_agents_window"
			}
		}
		return "", "open_elsewhere"
	}
	if state.sameCwd && !options.ConfirmClosed {
		return "", "maybe_open"
	}
	return "", ""
}

var startupSignals = []struct{ status, prefix string }{
	{"consent_required", "Enable Remote Control?"},
	{"remote_control_unavailable", "Remote Control only available"},
	{"remote_control_unavailable", "Remote Control is only available"},
	{"remote_control_unavailable", "Remote Control failed"},
	{"login_required", "Not logged in"},
	{"login_required", "Please run /login"},
	{"trust_required", "Do you trust the files in this folder"},
	{"started", "/remote-control is active"},
}

func classifyPane(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "⚠"))
		for _, signal := range startupSignals {
			if strings.HasPrefix(line, signal.prefix) {
				return signal.status
			}
		}
		if strings.HasSuffix(line, "· /rc active") {
			return "started"
		}
	}
	return ""
}

func waitForRemoteControl(window string) string {
	deadline := time.Now().Add(startupTimeout)
	for {
		dead, err := tmux("display-message", "-p", "-t", window, "#{pane_dead}")
		text, _ := tmux("capture-pane", "-p", "-J", "-t", window)
		status := classifyPane(text)
		switch {
		case status != "" && status != "started":
			return status
		case err != nil || dead == "1":
			return "startup_failed"
		case status == "started":
			return status
		case time.Now().After(deadline):
			return "unconfirmed"
		}
		time.Sleep(startupPoll)
	}
}
