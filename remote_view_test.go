package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type handoffCall struct {
	row     session
	options remoteOptions
}

func stubTUI(t *testing.T, results ...remoteResult) *[]handoffCall {
	t.Helper()
	oldHandoff, oldStop, oldListings, oldClose, oldAttach := tuiHandoff, tuiStop, tuiListings, tuiClose, tuiAttach
	t.Cleanup(func() {
		tuiHandoff, tuiStop, tuiListings, tuiClose, tuiAttach = oldHandoff, oldStop, oldListings, oldClose, oldAttach
	})
	calls := &[]handoffCall{}
	tuiHandoff = func(row session, options remoteOptions) remoteResult {
		*calls = append(*calls, handoffCall{row, options})
		result := results[min(len(*calls), len(results))-1]
		result.Message = remoteStatuses[result.Status].message
		return result
	}
	tuiListings = func() []remoteListing { return nil }
	return calls
}

func run(m model, cmd tea.Cmd) model {
	for cmd != nil {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, inner := range batch {
				m = run(m, inner)
			}
			return m
		}
		var next tea.Model
		next, cmd = m.Update(msg)
		m = next.(model)
	}
	return m
}

func pressKey(m model, text string) (model, tea.Cmd) {
	code := []rune(text)[0]
	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: code, Text: text}))
	return next.(model), cmd
}

func TestPhoneKeyHandsOffAndShowsWhereToLook(t *testing.T) {
	calls := stubTUI(t, remoteResult{Status: "started", App: "Claude app → Code", LookFor: "claude · api · Billing fix", Notices: []string{endpointNotice}})
	m, cmd := pressKey(fixtureModel(), "m")
	if !m.handingOff || !strings.Contains(ansi.Strip(m.View().Content), "Preparing the session") {
		t.Fatal("handoff progress not shown")
	}
	m = run(m, cmd)
	if len(*calls) != 1 || (*calls)[0].row.ID != "a" || m.handingOff {
		t.Fatalf("calls: %+v", *calls)
	}
	for _, want := range []string{"Claude app → Code", "claude · api · Billing fix", "endpoint is bypassed"} {
		if !strings.Contains(m.notice, want) {
			t.Fatalf("notice %q missing %q", m.notice, want)
		}
	}
}

func TestPhoneKeyRejectsUnsupportedClientsWithoutCallingCore(t *testing.T) {
	calls := stubTUI(t, remoteResult{Status: "started"})
	m := fixtureModel()
	m.sessions = []session{{Client: "opencode", ID: "o", Title: "Notes", Cwd: "/work/api"}}
	m.updateProjects()
	m.filter(false)
	m, cmd := pressKey(m, "m")
	if cmd != nil || len(*calls) != 0 || !strings.Contains(m.notice, "OpenCode or Cursor") {
		t.Fatalf("notice %q calls %v", m.notice, *calls)
	}
}

func TestPhoneKeyOffersToCloseAgentsWindow(t *testing.T) {
	calls := stubTUI(t, remoteResult{Status: "open_in_agents_window", Window: "@7"}, remoteResult{Status: "started"})
	closed := ""
	tuiClose = func(window string) error { closed = window; return nil }
	m, cmd := pressKey(fixtureModel(), "m")
	m = run(m, cmd)
	if m.pendingRemote == nil || !strings.Contains(ansi.Strip(m.View().Content), "CONTINUE ON PHONE?") {
		t.Fatal("close-window prompt not shown")
	}
	declined, cmd := pressKey(m, "n")
	if declined.pendingRemote != nil || cmd != nil || closed != "" {
		t.Fatal("declining still acted")
	}
	m, cmd = pressKey(m, "y")
	m = run(m, cmd)
	if closed != "@7" || len(*calls) != 2 || m.pendingRemote != nil {
		t.Fatalf("closed %q calls %d", closed, len(*calls))
	}
}

func TestClosingAgentsWindowWaitsForTheClientToExit(t *testing.T) {
	calls := stubTUI(t, remoteResult{Status: "open_in_agents_window", Window: "@7"}, remoteResult{Status: "open_elsewhere"}, remoteResult{Status: "started"})
	tuiClose = func(string) error { return nil }
	old := closeWait
	closeWait = time.Millisecond
	t.Cleanup(func() { closeWait = old })
	m, cmd := pressKey(fixtureModel(), "m")
	m = run(m, cmd)
	m, cmd = pressKey(m, "y")
	m = run(m, cmd)
	if len(*calls) != 3 || m.err != nil || !strings.Contains(m.notice, "Claude app") {
		t.Fatalf("calls %d err %v notice %q", len(*calls), m.err, m.notice)
	}
}

func TestPhoneKeyRetriesMaybeOpenWithConfirmation(t *testing.T) {
	calls := stubTUI(t, remoteResult{Status: "maybe_open"}, remoteResult{Status: "started"})
	m, cmd := pressKey(fixtureModel(), "m")
	m = run(m, cmd)
	m, cmd = pressKey(m, "y")
	run(m, cmd)
	if len(*calls) != 2 || (*calls)[0].options.ConfirmClosed || !(*calls)[1].options.ConfirmClosed {
		t.Fatalf("calls: %+v", *calls)
	}
}

func TestPhoneKeyShowsCodexWarningAndFailures(t *testing.T) {
	stubTUI(t, remoteResult{Status: "ready_warning", App: "ChatGPT app → Codex", LookFor: "Search workspace (b)"})
	m := fixtureModel()
	m.cursor = 1
	m, cmd := pressKey(m, "m")
	m = run(m, cmd)
	if !strings.Contains(m.notice, "diverge") || m.err != nil {
		t.Fatalf("warning notice %q err %v", m.notice, m.err)
	}
	stubTUI(t, remoteResult{Status: "daemon_not_running"})
	m, cmd = pressKey(m, "m")
	m = run(m, cmd)
	if m.err == nil || !strings.Contains(m.err.Error(), "codex remote-control start") {
		t.Fatalf("failure not shown as error: %v", m.err)
	}
}

func TestRemoteSessionsAreMarkedAndEnterAttaches(t *testing.T) {
	stubTUI(t, remoteResult{Status: "started"})
	attached := ""
	tuiAttach = func(window string) error { attached = window; return nil }
	m := fixtureModel()
	next, _ := m.Update(remoteListed{listings: []remoteListing{{Client: "claude", ID: "a", Window: "@3"}}})
	m = next.(model)
	if !strings.Contains(ansi.Strip(m.View().Content), "on phone") {
		t.Fatal("remote marker missing from the session list")
	}
	if expanded, _ := pressKey(m, "p"); !strings.Contains(ansi.Strip(expanded.View().Content), "Running for the phone") {
		t.Fatal("remote marker missing from session details")
	}
	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	run(next.(model), cmd)
	if attached != "@3" {
		t.Fatalf("Enter did not attach: %q", attached)
	}
}

func TestRemoteDialogStopsTheSelectedSession(t *testing.T) {
	stubTUI(t, remoteResult{Status: "started"})
	listings := []remoteListing{{Client: "claude", ID: "a", Window: "@3"}, {Client: "claude", ID: "z", Window: "@4"}}
	tuiListings = func() []remoteListing { return listings }
	stopped := ""
	tuiStop = func(row session) remoteResult {
		stopped = row.ID
		listings = listings[:1]
		return remoteResult{Status: "stopped", Message: remoteStatuses["stopped"].message}
	}
	m, cmd := pressKey(fixtureModel(), "M")
	m = run(m, cmd)
	view := ansi.Strip(m.View().Content)
	if !m.remoteDialog || !strings.Contains(view, "Billing fix") || !strings.Contains(view, "Session z") {
		t.Fatal("remote dialog did not list sessions")
	}
	m, _ = pressKey(m, "j")
	m, _ = pressKey(m, "x")
	m, cmd = pressKey(m, "y")
	m = run(m, cmd)
	if stopped != "z" || len(m.remote) != 1 || m.notice != remoteStatuses["stopped"].message {
		t.Fatalf("stopped %q remote %v notice %q", stopped, m.remote, m.notice)
	}
}

func TestPhoneKeyTypesIntoFocusedSearch(t *testing.T) {
	calls := stubTUI(t, remoteResult{Status: "started"})
	m, _ := pressKey(fixtureModel(), "/")
	m, _ = pressKey(m, "m")
	if m.search.Value() != "m" || len(*calls) != 0 {
		t.Fatalf("search %q calls %d", m.search.Value(), len(*calls))
	}
}

func TestPhonePromptNeedsMinimumSize(t *testing.T) {
	stubTUI(t, remoteResult{Status: "maybe_open"})
	m := fixtureModel()
	m.width, m.height = 60, 20
	m, cmd := pressKey(m, "m")
	m = run(m, cmd)
	if m.pendingRemote != nil || m.err == nil {
		t.Fatal("prompt opened in a small terminal")
	}
}
