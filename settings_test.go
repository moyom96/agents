package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestSettingsDialogSavesAndReopensCurrentChoice(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := fixtureModel()
	m, _ = press(m, ',')
	if m.settingsPrompt == nil || m.settingsPrompt.GetValue() != "tmux-window" {
		t.Fatal("settings did not open with current target")
	}
	m, _ = press(m, tea.KeyDown)
	if m.settingsPrompt.GetValue() != "terminal-tab" {
		t.Fatal("selection did not change")
	}
	if m.launchTarget == "terminal-tab" {
		t.Fatal("selection applied before saving")
	}
	for _, key := range []rune{'d', 's', 'q', 'x'} {
		m, _ = press(m, key)
	}
	if m.pendingDelete != nil || m.oldest || m.search.Focused() {
		t.Fatal("modal triggered browser action")
	}
	m, cmd := press(m, tea.KeyEnter)
	if cmd == nil || !m.savingSettings {
		t.Fatal("Enter did not save")
	}
	if _, duplicate := press(m, tea.KeyEnter); duplicate != nil {
		t.Fatal("duplicate save")
	}
	next, _ := m.Update(cmd())
	m = next.(model)
	if m.settingsPrompt != nil || m.launchTarget != "terminal-tab" || m.err != nil {
		t.Fatalf("save did not apply: %v", m.err)
	}
	config, err := loadSettings()
	if err != nil || config.Launch.Target != "terminal-tab" {
		t.Fatalf("choice was not persisted: %v", err)
	}
	m, _ = press(m, ',')
	if m.settingsPrompt.GetValue() != "terminal-tab" {
		t.Fatal("reopen lost saved choice")
	}
	m, _ = press(m, tea.KeyUp)
	m, _ = press(m, tea.KeyEsc)
	config, err = loadSettings()
	if m.settingsPrompt != nil || err != nil || config.Launch.Target != "terminal-tab" {
		t.Fatal("cancel changed saved choice")
	}
}

func TestSettingsDialogFailureAndSmallScreen(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := fixtureModel()
	m, _ = press(m, ',')
	m.width = 30
	if _, cmd := press(m, tea.KeyEnter); cmd != nil {
		t.Fatal("hidden settings were saved")
	}
	m, _ = press(m, tea.KeyEsc)
	if m.settingsPrompt != nil {
		t.Fatal("small-screen cancellation failed")
	}
	m.width = 110
	m, _ = press(m, ',')
	path, _ := settingsPath()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	m, cmd := press(m, tea.KeyEnter)
	next, _ := m.Update(cmd())
	m = next.(model)
	if m.err == nil || m.settingsPrompt == nil || m.savingSettings {
		t.Fatal("save failure did not stay visible")
	}
}

func TestSettingsDialogFitsAndSearchKeepsComma(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := fixtureModel()
	m.width, m.height = 70, 24
	m, _ = press(m, ',')
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"SETTINGS", "New tmux window", "New Alacritty tab", "Enter save", "Esc cancel"} {
		if !strings.Contains(view, want) {
			t.Fatalf("modal missing %q: %s", want, view)
		}
	}
	if len(strings.Split(view, "\n")) > m.height {
		t.Fatal("modal exceeds screen height")
	}
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > m.width {
			t.Fatal("modal exceeds screen width")
		}
	}
	m, _ = press(m, tea.KeyEsc)
	m, _ = press(m, '/')
	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: ',', Text: ","}))
	m = next.(model)
	if m.settingsPrompt != nil || m.search.Value() != "," {
		t.Fatal("comma in search opened settings")
	}
}

func TestSaveSettingsPreservesSymlinkAndRejectsInvalidFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path, _ := settingsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "stowed.toml")
	if err := os.WriteFile(source, []byte("[launch]\ntarget='tmux-window'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../stowed.toml", path); err != nil {
		t.Fatal(err)
	}
	if err := saveLaunchTarget("terminal-tab"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("save replaced stow link")
	}
	info, _ := os.Stat(source)
	if info.Mode().Perm() != 0600 {
		t.Fatal("save changed file permissions")
	}
	data, _ := os.ReadFile(source)
	if !strings.Contains(string(data), "terminal-tab") {
		t.Fatal("symlink source not updated")
	}
	if err := os.WriteFile(source, []byte("invalid ["), 0600); err != nil {
		t.Fatal(err)
	}
	if err := saveLaunchTarget("tmux-window"); err == nil {
		t.Fatal("malformed file overwritten")
	}
	data, _ = os.ReadFile(source)
	if string(data) != "invalid [" {
		t.Fatal("failed save modified file")
	}
}
