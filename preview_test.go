package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestPreviewReaderFixtures(t *testing.T) {
	cmd := exec.Command("python3", "preview_reader_test.py")
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("reader fixtures: %v: %s", err, output)
	}
}

func TestPreviewSelectionDebouncesAndDiscardsStaleMessages(t *testing.T) {
	m := fixtureModel()
	m, cmd := press(m, tea.KeyDown)
	if cmd == nil || !m.previewLoading || m.previewKey != sessionKey(m.visible[1]) {
		t.Fatal("selection did not schedule preview")
	}
	old := m.previewToken
	m, _ = press(m, tea.KeyDown)
	next, staleCmd := m.Update(previewRequested{row: m.visible[1], token: old})
	m = next.(model)
	if staleCmd != nil {
		t.Fatal("stale request read a transcript")
	}
	next, _ = m.Update(previewLoaded{token: old, data: previewData{Messages: []previewMessage{{Text: "WRONG SESSION"}}}})
	m = next.(model)
	if len(m.previewData.Messages) != 0 || !m.previewLoading {
		t.Fatal("stale messages replaced current preview")
	}
	next, _ = m.Update(previewLoaded{token: m.previewToken, data: previewData{Messages: []previewMessage{{Role: "assistant", Text: "Correct"}}}})
	m = next.(model)
	if m.previewLoading || m.previewData.Messages[0].Text != "Correct" {
		t.Fatal("current preview was not applied")
	}
	m.search.SetValue("no matches")
	m.search.Focus()
	next, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}))
	m = next.(model)
	if len(m.visible) != 0 || m.previewLoading || m.previewKey != "" || len(m.previewData.Messages) != 0 {
		t.Fatal("empty results retained another session's preview")
	}
}

func TestExpandedPreviewScrollsAndBlocksBrowserActions(t *testing.T) {
	m := fixtureModel()
	m.previewData = previewData{Messages: []previewMessage{{Role: "user", Text: strings.Repeat("A line\n", 100), Timestamp: "2026-10-20T12:00:00Z"}}}
	m, _ = press(m, 'p')
	if !m.previewExpanded {
		t.Fatal("p did not expand")
	}
	if !m.previewViewport.AtBottom() || m.previewViewport.YOffset() == 0 {
		t.Fatal("preview did not open at the latest message")
	}
	bottom := m.previewViewport.YOffset()
	m, _ = press(m, tea.KeyUp)
	if m.previewViewport.YOffset() >= bottom || m.cursor != 0 {
		t.Fatal("preview did not scroll independently")
	}
	m, _ = press(m, 'd')
	if m.pendingDelete != nil || m.search.Focused() {
		t.Fatal("preview triggered a browser action")
	}
	m, _ = press(m, tea.KeyEsc)
	if m.previewExpanded {
		t.Fatal("Esc did not close preview")
	}
	m, _ = press(m, '/')
	next, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'p', Text: "p"}))
	m = next.(model)
	if m.previewExpanded || m.search.Value() != "p" {
		t.Fatal("p in search expanded preview")
	}
}

func TestPreviewRoleFormattingAndWrappedIndentation(t *testing.T) {
	m := fixtureModel()
	m.previewData.Messages = []previewMessage{
		{Role: "user", Text: "A user message that wraps across several lines"},
		{Role: "assistant", Text: "An assistant reply that wraps across several lines"},
	}
	lines := m.previewLines(24)
	if !strings.HasPrefix(ansi.Strip(lines[0]), "> ") || !strings.Contains(lines[0], "\x1b[7m") {
		t.Fatalf("user message has no marker or background: %q", lines[0])
	}
	inAssistant := false
	for _, line := range lines {
		plain := ansi.Strip(line)
		if strings.HasPrefix(plain, "  · ") {
			inAssistant = true
			continue
		}
		if plain != "" && inAssistant && !strings.HasPrefix(plain, "    ") {
			t.Fatalf("assistant line has no left margin: %q", plain)
		}
		if ansi.StringWidth(line) > 24 {
			t.Fatal("indented message exceeds preview width")
		}
	}
	compact := ansi.Strip(m.previewPanel(50, 12))
	if !strings.Contains(compact, "> A user message") || !strings.Contains(compact, "  · An assistant reply") {
		t.Fatal("compact preview lost role formatting")
	}
	for _, view := range []string{compact, ansi.Strip(strings.Join(lines, "\n"))} {
		if strings.Contains(view, "USER") || strings.Contains(view, "ASSISTANT") {
			t.Fatal("preview still contains role labels")
		}
	}
}

func TestPreviewLateLoadAndReopenStartAtBottom(t *testing.T) {
	m := fixtureModel()
	m.previewLoading = true
	m, _ = press(m, 'p')
	data := previewData{Messages: []previewMessage{{Role: "assistant", Text: strings.Repeat("Earlier reply\n", 100) + "LATEST MESSAGE"}}}
	next, _ := m.Update(previewLoaded{token: m.previewToken, data: data})
	m = next.(model)
	if !m.previewViewport.AtBottom() || !strings.Contains(m.previewViewport.View(), "LATEST MESSAGE") {
		t.Fatal("asynchronous preview did not reveal latest message")
	}
	m, _ = press(m, tea.KeyUp)
	m, _ = press(m, tea.KeyEsc)
	m, _ = press(m, 'p')
	if !m.previewViewport.AtBottom() {
		t.Fatal("reopening retained an older scroll position")
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 70, Height: 24})
	m = next.(model)
	if !m.previewViewport.AtBottom() {
		t.Fatal("resize lost the bottom position")
	}
}

func TestPreviewFitsMinimumTerminalAndSanitizesText(t *testing.T) {
	m := fixtureModel()
	m.width, m.height = 70, 24
	m.previewData = previewData{Messages: []previewMessage{{Role: "assistant", Text: strings.Repeat("Long unicode 界 message ", 80)}}}
	m, _ = press(m, 'p')
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "LAST MESSAGES") || !strings.Contains(view, "p/Esc close") {
		t.Fatalf("missing preview controls: %s", view)
	}
	if len(strings.Split(view, "\n")) > m.height {
		t.Fatal("preview exceeds screen height")
	}
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > m.width {
			t.Fatal("preview exceeds screen width")
		}
	}
	if text := previewText("hello\x1b[2Jworld\x00\nnext"); text != "helloworld\nnext" {
		t.Fatalf("unsafe text: %q", text)
	}
}
