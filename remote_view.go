package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

var (
	tuiHandoff  = handoff
	tuiStop     = stopRemote
	tuiListings = windowListings
	tuiClose    = func(window string) error { _, err := tmux("kill-window", "-t", window); return err }
	tuiAttach   = func(window string) error {
		if os.Getenv("TMUX") == "" {
			return fmt.Errorf("start this browser inside tmux to attach to a remote session")
		}
		if output, err := exec.Command("tmux", "switch-client", "-t", window).CombinedOutput(); err != nil {
			return fmt.Errorf("attach to the remote session: %v. %s", err, clean(string(output)))
		}
		return nil
	}
)

type remoteHandedOff struct {
	row    session
	result remoteResult
}

type remoteListed struct{ listings []remoteListing }

type remoteStopped struct{ result remoteResult }

type remoteAttached struct{ err error }

type remotePrompt struct {
	row    session
	result remoteResult
}

func handoffCmd(row session, options remoteOptions) tea.Cmd {
	return func() tea.Msg { return remoteHandedOff{row: row, result: tuiHandoff(row, options)} }
}

func listRemoteCmd() tea.Cmd {
	return func() tea.Msg { return remoteListed{listings: tuiListings()} }
}

func (m model) isRemote(row session) bool {
	for _, listing := range m.remote {
		if listing.Client == row.Client && listing.ID == row.ID {
			return true
		}
	}
	return false
}

func (m model) remoteWindow(row session) string {
	for _, listing := range m.remote {
		if listing.Client == row.Client && listing.ID == row.ID {
			return listing.Window
		}
	}
	return ""
}

func (m model) startHandoff() (tea.Model, tea.Cmd) {
	row, ok := m.selected()
	if !ok || m.handingOff || m.loading {
		return m, nil
	}
	m.err, m.notice = nil, ""
	if row.Client != "claude" && row.Client != "codex" {
		m.notice = remoteStatuses["unsupported_client"].message
		return m, nil
	}
	m.handingOff = true
	return m, handoffCmd(row, remoteOptions{})
}

func (m model) handedOff(msg remoteHandedOff) (tea.Model, tea.Cmd) {
	m.handingOff = false
	result := msg.result
	switch result.Status {
	case "open_in_agents_window", "maybe_open":
		if m.width >= 70 && m.height >= 24 {
			m.pendingRemote = &remotePrompt{row: msg.row, result: result}
			return m, nil
		}
	}
	m.notice = remoteNotice(result)
	if remoteStatuses[result.Status].exit >= 3 && result.Status != "unconfirmed" {
		m.err, m.notice = fmt.Errorf("%s", result.Message), ""
	}
	return m, listRemoteCmd()
}

func remoteNotice(result remoteResult) string {
	parts := []string{result.Message}
	if result.LookFor != "" {
		parts = append(parts, result.App+": "+result.LookFor)
	}
	return strings.Join(append(parts, result.Notices...), " · ")
}

func (m model) updateRemotePrompt(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	prompt := *m.pendingRemote
	switch msg.String() {
	case "esc", "n", "q", "ctrl+c":
		m.pendingRemote = nil
	case "y", "Y", "enter":
		m.pendingRemote, m.handingOff = nil, true
		if prompt.result.Status == "maybe_open" {
			return m, handoffCmd(prompt.row, remoteOptions{ConfirmClosed: true})
		}
		window := prompt.result.Window
		return m, func() tea.Msg {
			if err := tuiClose(window); err != nil {
				return remoteHandedOff{row: prompt.row, result: newResult(prompt.row, "tmux_failed")}
			}
			return remoteHandedOff{row: prompt.row, result: tuiHandoff(prompt.row, remoteOptions{})}
		}
	}
	return m, nil
}

func (m model) remotePromptDialog(background string) string {
	prompt := *m.pendingRemote
	question := "Close that window and continue on your phone?"
	if prompt.result.Status == "maybe_open" {
		question = "If you closed it or it is a different session, continue?"
	}
	lines := []string{bold.Render(prompt.row.name()), "", prompt.result.Message, "", question, "", "y continue │ n or Esc cancel"}
	return m.dialogOverlay(background, "CONTINUE ON PHONE?", lines)
}

func (m model) updateRemoteDialog(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.remoteStopping {
		return m, nil
	}
	if m.remoteConfirmStop {
		m.remoteConfirmStop = false
		if (key == "y" || key == "Y" || key == "enter") && m.remoteCursor < len(m.remote) {
			listing := m.remote[m.remoteCursor]
			m.remoteStopping = true
			row := session{Client: listing.Client, ID: listing.ID, Title: m.remoteTitle(listing)}
			return m, func() tea.Msg { return remoteStopped{result: tuiStop(row)} }
		}
		return m, nil
	}
	switch key {
	case "esc", "q", "M", "ctrl+c":
		m.remoteDialog = false
	case "up", "k":
		m.remoteCursor = max(0, m.remoteCursor-1)
	case "down", "j":
		m.remoteCursor = min(max(0, len(m.remote)-1), m.remoteCursor+1)
	case "x":
		m.remoteConfirmStop = len(m.remote) > 0
	}
	return m, nil
}

func (m model) remoteTitle(listing remoteListing) string {
	for _, row := range m.sessions {
		if row.Client == listing.Client && row.ID == listing.ID {
			return limit(row.name(), 50)
		}
	}
	return "Session " + shortID(listing.ID)
}

func (m model) remoteDialogView(background string) string {
	lines := []string{muted.Render("Claude sessions running for the phone in tmux session " + remoteSession), ""}
	if len(m.remote) == 0 {
		lines = append(lines, "No remote sessions.")
	}
	for i, listing := range m.remote {
		title := m.remoteTitle(listing)
		label := fit("· "+title, 56)
		if i == m.remoteCursor {
			label = selectedStyle.Render(fit("● "+title, 56))
		}
		lines = append(lines, label, muted.Render(fit("    "+listing.Client+" · "+listing.ID, 56)))
	}
	hint := "↑↓ choose │ x stop │ Esc close"
	if m.remoteConfirmStop {
		hint = "Stop this remote session? y stop │ any other key cancels"
	} else if m.remoteStopping {
		hint = "Stopping session..."
	}
	return m.dialogOverlay(background, "REMOTE SESSIONS", append(lines, "", hint))
}
