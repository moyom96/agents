package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const fakeClaude = `#!/bin/sh
case "$FAKE_MODE" in
connected) printf '\n  /remote-control is active · Continue here, on your phone\n'; exec sleep 30 ;;
consent) printf 'Enable Remote Control? (y/n)\n'; exec sleep 30 ;;
exit) exit 1 ;;
*) exec sleep 30 ;;
esac
`

func remoteFixture(t *testing.T, mode string) session {
	t.Helper()
	if !tmuxSupported() {
		t.Skip("tmux 3.2 or later is not installed")
	}
	bin, project := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	socket := fmt.Sprintf("/tmp/agents-test-%d-%d.sock", os.Getpid(), time.Now().UnixNano())
	t.Cleanup(func() { tmux("kill-server"); os.Remove(socket); remoteTmuxSocket = "" })
	remoteTmuxSocket = socket
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_MODE", mode)
	if err := os.WriteFile(filepath.Join(bin, "login-shell"), []byte("#!/bin/sh\nshift 3\nexec \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", filepath.Join(bin, "login-shell"))
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	stubRemote(t, nil)
	startupTimeout, startupPoll = 3*time.Second, 100*time.Millisecond
	return session{Client: "claude", ID: "4895e5d4-test", Title: "Billing fix", Cwd: project}
}

func stubRemote(t *testing.T, procs []process) {
	t.Helper()
	oldProcesses, oldCwd, oldAwake, oldIndex := listProcesses, processCwd, keepAwake, loadIndex
	oldTimeout, oldPoll := startupTimeout, startupPoll
	t.Cleanup(func() {
		listProcesses, processCwd, keepAwake, loadIndex = oldProcesses, oldCwd, oldAwake, oldIndex
		startupTimeout, startupPoll = oldTimeout, oldPoll
	})
	listProcesses = func() ([]process, error) { return procs, nil }
	processCwd = func(int) string { return "" }
	keepAwake = func() bool { return false }
}

func logLines(t *testing.T) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(os.Getenv("XDG_STATE_HOME"), "agents", "remote.log"))
	if err != nil {
		return nil
	}
	var lines []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(string(data)), "\n") {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) == nil {
			lines = append(lines, entry)
		}
	}
	return lines
}

func TestRemoteNameIsSafeAndBounded(t *testing.T) {
	row := session{ID: "abcdef1234", Cwd: "/work/api", Title: "--fix\n`billing` $(touch ~/pwned); and a very long tail that keeps going on and on"}
	name := remoteName(row)
	if strings.ContainsAny(name, "`$();\n~") || len([]rune(name)) > 60 || !strings.HasPrefix(name, "claude · api · --fix billing touch /pwned") {
		t.Fatalf("unsafe name: %q", name)
	}
	if name := remoteName(session{ID: "abcdef1234", Cwd: "/work/api"}); name != "claude · api · abcdef12" {
		t.Fatalf("untitled name: %q", name)
	}
}

func TestClaudePayloadPassesArgumentsSeparately(t *testing.T) {
	row := session{ID: "id-1", Cwd: "/work/api"}
	payload := claudePayload(row, "claude · api · x", `{"env":{}}`, "/bin/zsh", true)
	joined := strings.Join(payload, "\x00")
	for _, want := range []string{"-u\x00CLAUDECODE", "-u\x00TMUX", "/bin/zsh\x00-lic\x00exec \"$@\"\x00agents-remote\x00caffeinate\x00-i\x00claude",
		"--settings\x00{\"env\":{}}", "--resume\x00id-1", "--remote-control\x00claude · api · x"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("payload missing %q: %q", want, payload)
		}
	}
	if slicesContain(claudePayload(row, "n", "{}", "/bin/sh", false), "caffeinate") {
		t.Fatal("caffeinate added without keep-awake")
	}
}

func slicesContain(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func TestClaudeConfigDetectsEndpointKeysAndGatewayAuth(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	for _, name := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_CUSTOM_HEADERS"} {
		t.Setenv(name, "")
	}
	if auth := claudeConfig(project); auth != (claudeAuth{}) {
		t.Fatalf("nothing configured: %+v", auth)
	}
	write := func(path, body string) {
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:8787","ANTHROPIC_AUTH_TOKEN":"x"}}`)
	auth := claudeConfig(project)
	if !auth.customEndpoint || !auth.gatewayAuth || auth.apiKey {
		t.Fatalf("proxy settings: %+v", auth)
	}
	override := endpointOverride(auth)
	if !strings.Contains(override, `"ANTHROPIC_BASE_URL":"https://api.anthropic.com"`) || !strings.Contains(override, `"ANTHROPIC_AUTH_TOKEN":""`) {
		t.Fatalf("override: %s", override)
	}
	write(filepath.Join(home, "settings.json"), `{"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com"}}`)
	if claudeConfig(project).customEndpoint {
		t.Fatal("official endpoint reported as custom")
	}
	write(filepath.Join(project, ".claude", "settings.local.json"), `{"apiKeyHelper":"/bin/key"}`)
	if !claudeConfig(project).apiKey {
		t.Fatal("apiKeyHelper not detected")
	}
}

func TestClassifyPaneIgnoresTranscriptText(t *testing.T) {
	cases := map[string]string{
		" ~/projects/agents · /rc active":                          "started",
		"  /remote-control is active · Continue here":              "started",
		"Enable Remote Control? (y/n)":                             "consent_required",
		"⚠ Remote Control failed: not available":                   "remote_control_unavailable",
		"⚠ Your login expires in 3 days · run /login to renew":     "",
		"  ⎿  /remote-control is active · quoted in a tool result": "",
		"> Enable Remote Control? is what the docs say":            "",
	}
	for text, want := range cases {
		if got := classifyPane(text); got != want {
			t.Errorf("classifyPane(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestFindOpenSessionMatchesResumeArgvAndSameCwd(t *testing.T) {
	row := session{Client: "claude", ID: "id-1", Cwd: "/work/api"}
	self := os.Getpid()
	table := newProcessTable([]process{
		{PID: 900, PPID: 1, Args: []string{"zsh", "-c", "agents", "remote", "id-1", "--resume", "id-1"}},
		{PID: self, PPID: 900, Args: []string{"agents", "remote", "id-1"}},
		{PID: 901, PPID: 1, Args: []string{"claude", "--resume", "other"}},
		{PID: 902, PPID: 1, Args: []string{"claude", "remote-control"}},
	})
	processCwd = func(int) string { return "/work/api" }
	t.Cleanup(func() { processCwd = func(int) string { return "" } })
	if state := findOpenSession(row, table, nil); state.resumedPID != 0 || state.sameCwd {
		t.Fatalf("own ancestors, other sessions, and servers must not match: %+v", state)
	}
	table = newProcessTable(append(table.rows, process{PID: 903, PPID: 1, Args: []string{"/opt/bin/claude", "-r", "id-1"}}))
	if state := findOpenSession(row, table, nil); state.resumedPID != 903 {
		t.Fatalf("resume argv not matched: %+v", state)
	}
	if state := findOpenSession(row, table, map[int]bool{903: true}); state.resumedPID != 0 {
		t.Fatal("excluded remote pane matched")
	}
	table = newProcessTable([]process{{PID: 904, PPID: 1, Args: []string{"claude"}}})
	if state := findOpenSession(row, table, nil); !state.sameCwd {
		t.Fatal("plain claude in the project not flagged")
	}
}

func TestClaudeHandoffStartsOnceAndStops(t *testing.T) {
	row := remoteFixture(t, "connected")
	result := handoff(row, remoteOptions{})
	if result.Status != "started" || result.App == "" || result.LookFor != "claude · "+filepath.Base(row.Cwd)+" · Billing fix" {
		t.Fatalf("handoff: %+v", result)
	}
	if again := handoff(row, remoteOptions{}); again.Status != "already_remote" {
		t.Fatalf("second handoff: %+v", again)
	}
	if windows := liveRemoteWindows(); len(windows) != 1 || windows[0].Key != "claude:"+row.ID {
		t.Fatalf("windows: %+v", windows)
	}
	if stopped := stopRemote(row); stopped.Status != "stopped" {
		t.Fatalf("stop: %+v", stopped)
	}
	if stopRemote(row).Status != "not_remote" || len(liveRemoteWindows()) != 0 {
		t.Fatal("window survived stop")
	}
	lines := logLines(t)
	if len(lines) != 2 || lines[0]["action"] != "handoff" || lines[1]["action"] != "stop" {
		t.Fatalf("log: %v", lines)
	}
	info, _ := os.Stat(filepath.Join(os.Getenv("XDG_STATE_HOME"), "agents", "remote.log"))
	dir, _ := os.Stat(filepath.Join(os.Getenv("XDG_STATE_HOME"), "agents"))
	if info.Mode().Perm() != 0o600 || dir.Mode().Perm() != 0o700 {
		t.Fatalf("log modes %v %v", info.Mode().Perm(), dir.Mode().Perm())
	}
}

func TestClaudeHandoffReportsStartupFailures(t *testing.T) {
	for mode, want := range map[string]string{"consent": "consent_required", "exit": "startup_failed"} {
		t.Run(mode, func(t *testing.T) {
			row := remoteFixture(t, mode)
			result := handoff(row, remoteOptions{})
			if result.Status != want || strings.Contains(result.Message, "(y/n)") {
				t.Fatalf("handoff: %+v", result)
			}
			if len(liveRemoteWindows()) != 0 || len(logLines(t)) != 0 {
				t.Fatal("failed handoff left a window or a log line")
			}
		})
	}
}

func TestClaudeHandoffKeepsUnconfirmedWindow(t *testing.T) {
	row := remoteFixture(t, "silent")
	startupTimeout = 500 * time.Millisecond
	if result := handoff(row, remoteOptions{}); result.Status != "unconfirmed" || len(liveRemoteWindows()) != 1 {
		t.Fatalf("handoff: %+v", result)
	}
}

func TestClaudeHandoffRefusesPossiblyOpenSession(t *testing.T) {
	row := remoteFixture(t, "connected")
	listProcesses = func() ([]process, error) { return []process{{PID: 905, PPID: 1, Args: []string{"claude"}}}, nil }
	processCwd = func(int) string { return row.Cwd }
	if result := handoff(row, remoteOptions{}); result.Status != "maybe_open" || len(liveRemoteWindows()) != 0 {
		t.Fatalf("handoff: %+v", result)
	}
	if result := handoff(row, remoteOptions{ConfirmClosed: true}); result.Status != "started" {
		t.Fatalf("confirmed handoff: %+v", result)
	}
}

func TestConcurrentClaudeHandoffsCreateOneWindow(t *testing.T) {
	row := remoteFixture(t, "connected")
	var wg sync.WaitGroup
	statuses := make([]string, 2)
	for i := range statuses {
		wg.Go(func() { statuses[i] = handoff(row, remoteOptions{}).Status })
	}
	wg.Wait()
	if len(liveRemoteWindows()) != 1 {
		t.Fatalf("windows after concurrent handoffs: %v %v", statuses, liveRemoteWindows())
	}
}

func TestRemoteCommandContract(t *testing.T) {
	stubRemote(t, nil)
	loadIndex = func() ([]session, error) {
		return []session{{Client: "opencode", ID: "ses_1", Title: "Notes"}, {Client: "claude", ID: "dup"}, {Client: "codex", ID: "dup"}}, nil
	}
	run := func(args ...string) (int, map[string]any) {
		var out bytes.Buffer
		code := runRemote(args, &out)
		var result map[string]any
		json.Unmarshal(out.Bytes(), &result)
		return code, result
	}
	if code, result := run("missing", "--json"); code != 2 || result["status"] != "unknown_session" {
		t.Fatalf("unknown: %d %v", code, result)
	}
	if code, result := run("ses_1", "--json"); code != 2 || result["status"] != "unsupported_client" {
		t.Fatalf("unsupported: %d %v", code, result)
	}
	if code, result := run("dup", "--json"); code != 2 || !strings.Contains(result["message"].(string), "claude, codex") {
		t.Fatalf("ambiguous: %d %v", code, result)
	}
	if code, _ := run("--bogus"); code != 2 {
		t.Fatal("unknown flag accepted")
	}
	if code, _ := run(); code != 2 {
		t.Fatal("missing id accepted")
	}
}

const fakeCodex = `#!/bin/sh
case "$FAKE_DAEMON" in
running) printf '{"status":"running"}\n' ;;
stopped) printf '{"status":"stopped"}\n' ;;
hang) exec sleep 5 ;;
*) exit 1 ;;
esac
`

func codexFixture(t *testing.T, daemon string, procs []process) session {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(fakeCodex), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_DAEMON", daemon)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	stubRemote(t, procs)
	old := codexTimeout
	codexTimeout = 500 * time.Millisecond
	t.Cleanup(func() { codexTimeout = old })
	return session{Client: "codex", ID: "019a2b3c-4d5e", Title: "Search workspace", Cwd: "/work/api", LastActivity: time.Unix(0, 0)}
}

var daemonProcess = process{PID: 700, PPID: 1, Args: []string{"/x/bin/codex", "app-server", "--remote-control", "--listen", "unix://"}}

func TestCodexHandoffReportsReadiness(t *testing.T) {
	row := codexFixture(t, "running", []process{daemonProcess})
	result := handoff(row, remoteOptions{})
	if result.Status != "ready" || result.LookFor != "Search workspace (019a2b3c)" || remoteStatuses[result.Status].exit != 0 {
		t.Fatalf("ready: %+v", result)
	}
	if lines := logLines(t); len(lines) != 1 || lines[0]["client"] != "codex" {
		t.Fatalf("log: %v", lines)
	}
	row.Title = ""
	if lookFor := codexLookFor(row); !strings.HasPrefix(lookFor, "api · ") || !strings.HasSuffix(lookFor, "(019a2b3c)") {
		t.Fatalf("untitled look_for: %q", lookFor)
	}
}

func TestCodexHandoffWarnsWhenOpenInTerminal(t *testing.T) {
	row := codexFixture(t, "running", []process{daemonProcess, {PID: 701, PPID: 1, Args: []string{"codex", "resume", "019a2b3c-4d5e"}}})
	if result := handoff(row, remoteOptions{}); result.Status != "ready_warning" || remoteStatuses[result.Status].exit != 0 {
		t.Fatalf("warning: %+v", result)
	}
}

func TestCodexHandoffFailures(t *testing.T) {
	for daemon, want := range map[string]string{"stopped": "daemon_not_running", "error": "daemon_not_running", "hang": "daemon_not_running"} {
		row := codexFixture(t, daemon, []process{daemonProcess})
		if result := handoff(row, remoteOptions{}); result.Status != want {
			t.Fatalf("%s: %+v", daemon, result)
		}
	}
	row := codexFixture(t, "running", []process{{PID: 702, PPID: 1, Args: []string{"/x/bin/codex", "app-server", "--listen", "unix://"}}})
	if result := handoff(row, remoteOptions{}); result.Status != "remote_control_disabled" {
		t.Fatalf("disabled: %+v", result)
	}
	t.Setenv("PATH", t.TempDir())
	if result := handoff(row, remoteOptions{}); result.Status != "client_missing" {
		t.Fatalf("missing: %+v", result)
	}
}

func TestRemoteLogRotates(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir, _ := stateDir()
	path := filepath.Join(dir, "remote.log")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), remoteLogLimit), 0o600); err != nil {
		t.Fatal(err)
	}
	logRemote(remoteResult{Client: "codex", ID: "a", Status: "ready"})
	if info, err := os.Stat(path + ".1"); err != nil || info.Size() != remoteLogLimit {
		t.Fatal("log did not rotate")
	}
	if len(logLines(t)) != 1 {
		t.Fatal("new log missing entry")
	}
}

func TestRemoteSubcommandDoesNotNeedATerminal(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	binary := filepath.Join(t.TempDir(), "agents")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	output, _ := exec.Command(binary, "remote").CombinedOutput()
	if !strings.Contains(string(output), "usage: agents remote") {
		t.Fatalf("usage not printed: %s", output)
	}
}
