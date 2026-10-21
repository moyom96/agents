package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewTabClientExcludesExistingAndOtherApplications(t *testing.T) {
	before := map[string]string{"browser": "1"}
	after := map[string]string{"browser": "1", "other-terminal": "2", "new-tab": "3"}
	client, err := newTabClient(before, after, func(pid string) bool { return pid == "3" })
	if err != nil || client != "new-tab" {
		t.Fatalf("client = %q, error = %v", client, err)
	}
	client, err = newTabClient(before, after, func(pid string) bool { return false })
	if err != nil || client != "" {
		t.Fatal("selected an unrelated client")
	}
	client, err = newTabClient(before, after, func(pid string) bool { return true })
	if err == nil || client != "" {
		t.Fatal("ambiguous clients should not launch anything")
	}
}

func TestTerminalTabRejectsUnsupportedEnvironment(t *testing.T) {
	t.Setenv("ALACRITTY_SOCKET", "")
	if err := openTerminalTab(session{}, "/bin/sh", nil); err == nil {
		t.Fatal("expected unsupported terminal error")
	}
}

func TestTerminalTabLauncher(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS launcher")
	}
	dir := t.TempDir()
	record := filepath.Join(dir, "record")
	script := `#!/bin/sh
case "$1" in
list-clients)
  printf 'browser\t10\n'
  if [ -f "$TAB_CREATED" ]; then printf 'new-tab\t200\n'; fi
  ;;
detach-client) printf '%s\n' "$@" > "$RECORD" ;;
esac
`
	for name, content := range map[string]string{
		"tmux":      script,
		"osascript": "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$APPLESCRIPT_RECORD\"\ntouch \"$TAB_CREATED\"\n",
		"ps":        "#!/bin/sh\nprintf '123\\n'\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("TAB_CREATED", filepath.Join(dir, "created"))
	t.Setenv("RECORD", record)
	t.Setenv("APPLESCRIPT_RECORD", filepath.Join(dir, "script"))
	t.Setenv("ALACRITTY_SOCKET", "/tmp/Alacritty-123.sock")
	row := session{Cwd: "/project with 'quotes'", ID: "id'$(touch nope)"}
	if err := openTerminalTab(row, "/bin/sh", []string{"codex", "resume", row.ID}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(args) != 5 || args[0] != "detach-client" || args[2] != "new-tab" || args[3] != "-E" {
		t.Fatalf("wrong client: %q", args)
	}
	if !strings.Contains(args[4], "cd "+quote(row.Cwd)) || !strings.Contains(args[4], quote(resumeCommand("/bin/sh", []string{"codex", "resume", row.ID}))) {
		t.Fatal("directory or command was not quoted")
	}
	if !strings.Contains(args[4], "-u TMUX -u TMUX_PANE") {
		t.Fatal("new tab inherits tmux state")
	}
	appleScript, err := os.ReadFile(filepath.Join(dir, "script"))
	if err != nil || !strings.HasSuffix(string(appleScript), "123\n") {
		t.Fatal("did not target the originating Alacritty process")
	}
}

func TestAlacrittyTabLive(t *testing.T) {
	if os.Getenv("AI_SESSIONS_TAB_SMOKE") != "1" {
		t.Skip("opt-in test opens and closes a native Alacritty tab")
	}
	dir := t.TempDir()
	record := filepath.Join(dir, "result")
	shell := filepath.Join(dir, "fixture-shell")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\nif [ \"$1\" = -lic ]; then exec /bin/sh -c \"$2\"; fi\n"), 0755); err != nil {
		t.Fatal(err)
	}
	before, err := tmuxClients(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	command := "pwd > " + quote(record) + "; printf '%s\\n' \"${TMUX-unset}\" >> " + quote(record)
	if err := openTerminalTab(session{Cwd: dir}, shell, []string{"/bin/sh", "-c", command}); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("/bin/sh", "-c", "for i in 1 2 3 4 5; do [ -f "+quote(record)+" ] && exit 0; sleep 1; done; exit 1").CombinedOutput()
	if err != nil {
		t.Fatalf("dummy command did not run: %v %s", err, output)
	}
	data, err := os.ReadFile(record)
	if err != nil || strings.TrimSpace(string(data)) != dir+"\nunset" {
		t.Fatalf("wrong directory or inherited tmux: %q %v", data, err)
	}
	after, err := tmuxClients(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for name, pid := range before {
		if after[name] != pid {
			t.Fatalf("existing client changed: %s", name)
		}
	}
}
