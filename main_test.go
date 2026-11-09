package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The default launch target depends on running inside Herdr; tests opt in explicitly.
func TestMain(m *testing.M) {
	os.Unsetenv("HC_TERMINAL_ID")
	os.Exit(m.Run())
}

func fixtureModel() model {
	m := newModel()
	m.loading, m.width, m.height = false, 110, 32
	m.search.SetWidth(102)
	m.sessions = []session{
		{Client: "claude", ID: "a", Title: "Billing fix", Cwd: "/work/api", LastActivity: time.Unix(300, 0)},
		{Client: "codex", ID: "b", Title: "Search workspace", Cwd: "/work/api", LastActivity: time.Unix(200, 0)},
		{Client: "codex", ID: "c", Title: "Billing dashboard", Cwd: "/other/api", LastActivity: time.Unix(100, 0)},
	}
	m.updateProjects()
	m.filter(false)
	return m
}

func press(m model, code rune) (model, tea.Cmd) {
	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: code}))
	return next.(model), cmd
}

func TestFiltersCombineClientExactProjectAndTitle(t *testing.T) {
	m := fixtureModel()
	m.client = 2
	m.updateProjects()
	for i, path := range m.projects {
		if path == "/other/api" {
			m.project = i
		}
	}
	m.search.SetValue("BILLING")
	m.filter(false)
	if len(m.visible) != 1 || m.visible[0].ID != "c" {
		t.Fatalf("combined filters returned %#v", m.visible)
	}
	m.search.SetValue("/other/api")
	m.filter(false)
	if len(m.visible) != 0 {
		t.Fatal("search should match session names, not paths")
	}
}

func TestEnterSearchesMessagesAndMatchesApplyOnlyToTheirQuery(t *testing.T) {
	m := fixtureModel()
	m.search.Focus()
	m.search.SetValue("toggle")
	m.filter(false)
	m, cmd := press(m, tea.KeyEnter)
	if cmd == nil || !m.searchingMessages || m.search.Focused() {
		t.Fatal("Enter should finish editing and start a message search")
	}
	next, _ := m.Update(messagesSearched{query: "old", matches: map[string]string{"codex:c": "stale"}})
	m = next.(model)
	if len(m.visible) != 0 || m.searchingMessages {
		t.Fatalf("a result for another query must be ignored, got %#v", m.visible)
	}
	next, _ = m.Update(messagesSearched{query: "toggle", matches: map[string]string{"codex:b": "added the toggle"}})
	m = next.(model)
	if len(m.visible) != 1 || m.visible[0].ID != "b" {
		t.Fatalf("message matches should be listed, got %#v", m.visible)
	}
	m.project = 0
	for i, path := range m.projects {
		if path == "/other/api" {
			m.project = i
		}
	}
	m.filter(false)
	if len(m.visible) != 0 {
		t.Fatal("message matches must respect the selected project")
	}
	m.project = 0
	m.filter(false)
	if !strings.Contains(ansi.Strip(m.View().Content), "Search workspace · added the toggle") {
		t.Fatal("the matched message text should follow the session name")
	}
	m.search.SetValue("toggl")
	m.filter(false)
	if len(m.visible) != 0 {
		t.Fatal("matches must stop applying once the query changes")
	}
}

func TestTypingSearchesMessagesAfterAPauseOnlyForTheCurrentQuery(t *testing.T) {
	m := fixtureModel()
	m.search.Focus()
	var cmd tea.Cmd
	for _, letter := range "to" {
		m, cmd = typeText(m, letter)
	}
	if _, ok := cmd().(messageSearchRequested); ok {
		t.Fatal("queries under three characters must not search messages")
	}
	m, _ = typeText(m, 'g')
	next, cmd := m.Update(messageSearchRequested("to"))
	if cmd != nil || next.(model).searchingMessages {
		t.Fatal("a pause for an earlier query must not search")
	}
	next, cmd = m.Update(messageSearchRequested("tog"))
	if cmd == nil || !next.(model).searchingMessages {
		t.Fatal("a pause on the current query should search messages")
	}
}

func typeText(m model, letter rune) (model, tea.Cmd) {
	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: letter, Text: string(letter)}))
	return next.(model), cmd
}

func TestUntitledSessionsAreNamedByTheirFirstPrompt(t *testing.T) {
	cases := map[string]session{
		"Billing fix":      {Title: "Billing fix", FirstPrompt: "fix billing"},
		"fix billing":      {FirstPrompt: "fix\nbilling"},
		"Untitled session": {},
	}
	for want, row := range cases {
		if got := row.name(); got != want {
			t.Errorf("name() = %q, want %q", got, want)
		}
	}
}

func TestSortAndRefreshKeepTheSelectedSession(t *testing.T) {
	m := fixtureModel()
	m.cursor = 1
	m, _ = press(m, 's')
	selected, _ := m.selected()
	if m.visible[0].ID != "c" || selected.ID != "b" {
		t.Fatal("sorting lost selection")
	}
	m.sessions = append(m.sessions, session{Client: "claude", ID: "d", LastActivity: time.Unix(400, 0)})
	next, _ := m.Update(sessionsLoaded{sessions: m.sessions})
	m = next.(model)
	selected, _ = m.selected()
	if selected.ID != "b" {
		t.Fatal("refresh lost selection")
	}
}

func TestClientNavigationKeepsTheSelectedProject(t *testing.T) {
	m := fixtureModel()
	m.focus = 1
	for m.projects[m.project] != "/other/api" {
		m, _ = press(m, tea.KeyDown)
	}
	m.focus = 0
	m, _ = press(m, tea.KeyDown)
	if clients[m.client] != "claude" || m.projects[m.project] != "/other/api" || len(m.visible) != 0 {
		t.Fatalf("claude: project %q, %d visible", m.projects[m.project], len(m.visible))
	}
	m, _ = press(m, tea.KeyDown)
	if clients[m.client] != "codex" || m.projects[m.project] != "/other/api" || len(m.visible) != 1 || m.visible[0].ID != "c" {
		t.Fatalf("codex: project %q, visible %#v", m.projects[m.project], m.visible)
	}
}

func TestNavigationResetAndEmptyResults(t *testing.T) {
	m := fixtureModel()
	m.focus = 0
	m, _ = press(m, tea.KeyDown)
	if m.client != 1 || len(m.visible) != 1 {
		t.Fatal("client navigation did not filter")
	}
	m.search.SetValue("missing")
	m.filter(false)
	m.focus = 2
	m, cmd := press(m, tea.KeyEnter)
	if cmd != nil || m.opening {
		t.Fatal("empty results must not launch")
	}
	m, _ = press(m, tea.KeyEsc)
	if len(m.visible) != 3 || m.client != 0 || m.search.Value() != "" {
		t.Fatal("reset failed")
	}
}

func TestViewFitsTerminalAndShowsDates(t *testing.T) {
	m := fixtureModel()
	for _, size := range [][2]int{{70, 24}, {110, 32}, {160, 45}} {
		m.width, m.height = size[0], size[1]
		view := ansi.Strip(m.View().Content)
		if len(strings.Split(view, "\n")) > m.height {
			t.Fatalf("view has %d lines, exceeds height %d:\n%s", len(strings.Split(view, "\n")), m.height, view)
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > m.width {
				t.Fatalf("line exceeds %d columns: %s", m.width, line)
			}
		}
		if !strings.Contains(view, m.sessions[0].LastActivity.Local().Format("02/01/2006")) || !strings.Contains(view, "Billing fix") {
			t.Fatal("missing date or title")
		}
	}
	m.width = 30
	if !strings.Contains(m.View().Content, "Resize") {
		t.Fatal("missing small terminal guidance")
	}
}

func TestSortDirectionIsVisible(t *testing.T) {
	m := fixtureModel()
	if !strings.Contains(m.View().Content, "↓ newest first") {
		t.Fatal("descending sort indicator missing")
	}
	m, _ = press(m, 's')
	if !strings.Contains(m.View().Content, "↑ oldest first") {
		t.Fatal("ascending sort indicator missing")
	}
}

func TestOpenRejectsMissingTmuxAndMissingDirectories(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TMUX", "")
	if err := openSession(session{}).err; err == nil || !strings.Contains(err.Error(), "inside tmux") {
		t.Fatal(err)
	}
	t.Setenv("TMUX", "test")
	t.Setenv("TMUX_PANE", "%1")
	if err := openSession(session{Cwd: "/missing/session/project"}).err; err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatal(err)
	}
}

func TestTmuxLaunchTargetsBrowserSessionAndQuotesArguments(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	script := "#!/bin/sh\nif [ \"$1\" = display-message ]; then printf '$7\\n'; else printf '%s\\n' \"$@\" > \"$RECORD\"; fi\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RECORD", record)
	t.Setenv("TMUX", "test")
	t.Setenv("TMUX_PANE", "%99")
	t.Setenv("SHELL", "/bin/sh")
	row := session{Client: "codex", ID: "session 'with' $(characters)", Cwd: dir, Title: "Example"}
	if result := openSession(row); result.err != nil {
		t.Fatal(result.err)
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if args[0] != "new-window" || args[2] != "$7:" || args[6] != dir {
		t.Fatalf("wrong tmux arguments: %q", args)
	}
	if !strings.Contains(args[len(args)-6], quote(row.ID)) {
		t.Fatal("session ID was not shell quoted")
	}
	if tag := args[len(args)-5:]; tag[0] != ";" || tag[3] != "@agents-local" || tag[4] != "codex:"+row.ID {
		t.Fatalf("window was not tagged for handoff detection: %q", tag)
	}
}

func TestReaderKeepsPartialDataAndWarning(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
printf '%s' '[{"client":"claude","id":"test","cwd":"/project","last_activity":"2026-10-20T12:00:00-06:00"}]'
printf '%s' 'agents: opencode: unavailable' >&2
exit 1
`
	if err := os.WriteFile(filepath.Join(dir, "agents-list"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	result := readSessions()
	if result.err != nil || len(result.sessions) != 1 || !strings.Contains(result.warning, "unavailable") {
		t.Fatalf("partial result lost: %#v", result)
	}
}
