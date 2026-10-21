package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

const deleteTestID = "12345678-1234-1234-1234-123456789abc"

func TestDeleteDialogDefaultsToCancelAndBlocksOtherActions(t *testing.T) {
	m := fixtureModel()
	m, cmd := press(m, 'd')
	if cmd != nil || m.pendingDelete == nil || m.deletePrompt.GetValue().(bool) {
		t.Fatal("deletion was not gated by the dialog")
	}
	m, _ = press(m, 'x')
	if m.search.Focused() || m.search.Value() != "" {
		t.Fatal("modal key started a search")
	}
	m, cmd = press(m, tea.KeyEnter)
	if cmd != nil || m.pendingDelete != nil {
		t.Fatal("default Enter should cancel")
	}
	m, _ = press(m, 'd')
	m.width = 30
	m, cmd = press(m, 'y')
	if cmd != nil || m.deleting {
		t.Fatal("hidden confirmation dialog allowed deletion")
	}
	m.width = 110
	m, _ = press(m, tea.KeyTab)
	m, cmd = press(m, tea.KeyEnter)
	if cmd == nil || !m.deleting {
		t.Fatal("explicit confirmation did not start deletion")
	}
	_, duplicate := press(m, 'y')
	if duplicate != nil {
		t.Fatal("deletion started twice")
	}
}

func TestDeleteResultRemovesOnlyChosenSessionAndKeepsFailures(t *testing.T) {
	m := fixtureModel()
	row := m.visible[0]
	m.pendingDelete = &row
	next, cmd := m.Update(sessionDeleted{row: row, err: fmt.Errorf("client refused deletion")})
	m = next.(model)
	if len(m.sessions) != 3 || m.pendingDelete == nil || cmd != nil {
		t.Fatal("failure removed session or closed dialog")
	}
	next, cmd = m.Update(sessionDeleted{row: row})
	m = next.(model)
	if len(m.sessions) != 2 || m.pendingDelete != nil || cmd == nil {
		t.Fatal("success did not remove session and refresh")
	}
	for _, existing := range m.sessions {
		if existing.ID == row.ID {
			t.Fatal("deleted session still visible")
		}
	}
}

func TestUnmappedLetterStartsSearchAndMappedKeysKeepTheirActions(t *testing.T) {
	m := fixtureModel()
	m, _ = press(m, 'B')
	if !m.search.Focused() || m.search.Value() != "B" || len(m.visible) != 2 {
		t.Fatal("unmapped letter did not seed case-insensitive search")
	}
	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'd', Text: "d"}))
	m = next.(model)
	if m.pendingDelete != nil || m.search.Value() != "Bd" {
		t.Fatal("typing d in search opened a delete dialog")
	}
	m = fixtureModel()
	m, _ = press(m, 's')
	if !m.oldest || m.search.Focused() {
		t.Fatal("mapped sort key started searching")
	}
	m, _ = press(m, 'é')
	if !m.search.Focused() || m.search.Value() != "é" {
		t.Fatal("Unicode letter did not start search")
	}
}

func TestDeleteDialogFitsTerminal(t *testing.T) {
	m := fixtureModel()
	row := m.visible[0]
	m.pendingDelete = &row
	m.width, m.height = 70, 24
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "DELETE SESSION?") || !strings.Contains(view, "Cancel") {
		t.Fatal("confirmation dialog missing")
	}
	if len(strings.Split(view, "\n")) > m.height {
		t.Fatal("modal exceeds terminal height")
	}
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > m.width {
			t.Fatal("modal exceeds terminal width")
		}
	}
}

func TestClaudeDeletionOnlyRemovesSelectedTranscriptAndCompanions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	dir := filepath.Join(home, "projects", "project")
	companion := filepath.Join(dir, deleteTestID)
	if err := os.MkdirAll(companion, 0755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(dir, deleteTestID+".jsonl")
	other := filepath.Join(dir, "other.jsonl")
	projectFile := filepath.Join(home, "project-source.rb")
	for _, path := range []string{transcript, other, projectFile, filepath.Join(companion, "tool-result")} {
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := deleteClaudeSession(deleteTestID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{transcript, companion} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("session data remains: %s", path)
		}
	}
	for _, path := range []string{other, projectFile} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("unrelated file was removed", err)
		}
	}
	if err := deleteClaudeSession("../../project-source.rb"); err == nil {
		t.Fatal("invalid session ID was accepted")
	}
}

func TestNativeDeletionUsesExactClientCommands(t *testing.T) {
	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$RECORD\"\n"
	for _, client := range []string{"codex", "opencode"} {
		if err := os.WriteFile(filepath.Join(dir, client), []byte(script), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RECORD", record)
	for _, test := range []struct {
		client, id string
		args       []string
	}{
		{"codex", deleteTestID, []string{"delete", "--force", deleteTestID}},
		{"opencode", "ses_test123", []string{"session", "delete", "ses_test123"}},
	} {
		if result := deleteSession(session{Client: test.client, ID: test.id}); result.err != nil {
			t.Fatal(result.err)
		}
		data, err := os.ReadFile(record)
		if err != nil {
			t.Fatal(err)
		}
		if args := strings.Split(strings.TrimSpace(string(data)), "\n"); !reflect.DeepEqual(args, test.args) {
			t.Fatalf("wrong delete arguments: %q", args)
		}
	}
}

func TestClaudeDeletionDoesNotFollowCompanionSymlinks(t *testing.T) {
	home, source := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	dir := filepath.Join(home, "projects", "project")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(source, "source.rb")
	if err := os.WriteFile(marker, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, deleteTestID+".jsonl"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, filepath.Join(dir, deleteTestID)); err != nil {
		t.Fatal(err)
	}
	if err := deleteClaudeSession(deleteTestID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("symlink target was deleted", err)
	}
}

func TestCursorDeletionKeepsOtherChatsAndProjectFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	chat := filepath.Join(home, ".cursor", "chats", "project-hash", deleteTestID)
	other := filepath.Join(home, ".cursor", "chats", "project-hash", "other-session")
	for _, path := range []string{chat, other} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "meta.json"), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(home, "project-source.rb")
	if err := os.WriteFile(marker, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	if result := deleteSession(session{Client: "cursor", ID: deleteTestID}); result.err != nil {
		t.Fatal(result.err)
	}
	if _, err := os.Stat(chat); !os.IsNotExist(err) {
		t.Fatal("chosen chat still exists")
	}
	for _, path := range []string{other, marker} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("unrelated data was removed", err)
		}
	}
	if err := deleteCursorSession("../../project-source.rb"); err == nil {
		t.Fatal("invalid Cursor ID was accepted")
	}
}
