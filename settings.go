package main

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

type settingsSaved struct {
	target string
	err    error
}

func newSettingsPrompt(target string) *huh.Select[string] {
	prompt := huh.NewSelect[string]().Options(
		huh.NewOption("New tmux window", "tmux-window"),
		huh.NewOption("New Alacritty tab (macOS)", "terminal-tab"),
	).Value(&target)
	prompt.WithTheme(dialogTheme())
	prompt.WithKeyMap(huh.NewDefaultKeyMap())
	prompt.WithWidth(54)
	prompt.Focus()
	return prompt
}

func (m model) updateSettingsDialog(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.savingSettings {
		return m, nil
	}
	key := msg.String()
	if key == "esc" || key == "ctrl+c" {
		m.settingsPrompt, m.err = nil, nil
		return m, nil
	}
	if m.width < 70 || m.height < 24 {
		return m, nil
	}
	switch key {
	case "up", "down", "left", "right", "j", "k":
		m.settingsPrompt.Update(msg)
	case "enter":
		target := m.settingsPrompt.GetValue().(string)
		m.savingSettings, m.err = true, nil
		return m, func() tea.Msg {
			err := saveLaunchTarget(target)
			if err != nil {
				err = fmt.Errorf("save settings: %w", err)
			}
			return settingsSaved{target: target, err: err}
		}
	}
	return m, nil
}

func (m model) settingsDialog(background string) string {
	hint := "↑↓ choose │ Enter save │ Esc cancel"
	if m.savingSettings {
		hint = "Saving settings..."
	} else if m.err != nil {
		hint = clean(m.err.Error())
	}
	lines := []string{bold.Render("Open sessions in"), ""}
	lines = append(lines, strings.Split(m.settingsPrompt.View(), "\n")...)
	lines = append(lines, "",
		"Tabs require Alacritty on macOS with tmux startup.",
		"macOS Accessibility access is required for tabs.", "",
		hint)
	return m.dialogOverlay(background, "SETTINGS", lines)
}
