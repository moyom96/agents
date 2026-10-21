package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var (
	accent        = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	muted         = lipgloss.NewStyle()
	bold          = lipgloss.NewStyle().Bold(true)
	selectedStyle = lipgloss.NewStyle().Reverse(true).Bold(true)
)

func fit(text string, width int) string {
	text = ansi.Truncate(text, max(0, width), "…")
	return text + strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
}

func panel(title string, lines []string, width, height int, active bool) string {
	color := lipgloss.Color("8")
	titleStyle := bold
	if active {
		color = lipgloss.Color("12")
		titleStyle = accent.Bold(true)
	}
	content := []string{titleStyle.Render(title)}
	content = append(content, lines...)
	for i := range content {
		content[i] = fit(content[i], width-4)
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(color).
		Padding(0, 1).Width(width).Height(height).
		Render(strings.Join(content[:min(len(content), height-2)], "\n"))
}

func windowStart(cursor, count, size int) int {
	return max(0, min(cursor-size/2, count-size))
}

func (m model) clientPanel(width int) string {
	var lines []string
	for i, client := range clients {
		count := 0
		for _, row := range m.sessions {
			if i == 0 || row.Client == client {
				count++
			}
		}
		label := fmt.Sprintf("  %-12s %3d", client, count)
		if i == m.client {
			label = "›" + label[1:]
			label = accent.Bold(true).Render(label)
		}
		lines = append(lines, label)
	}
	return panel("CLIENTS", lines, width, len(clients)+4, m.focus == 0)
}

func (m model) projectPanel(width, height int) string {
	var lines []string
	size := max(1, height-3)
	start := windowStart(m.project, len(m.projects), size)
	for i := start; i < min(start+size, len(m.projects)); i++ {
		name := "All projects"
		if i > 0 {
			name = clean(filepath.Base(m.projects[i]))
			for j, path := range m.projects {
				if j != i && filepath.Base(path) == filepath.Base(m.projects[i]) {
					name = clean(filepath.Join(filepath.Base(filepath.Dir(m.projects[i])), filepath.Base(m.projects[i])))
					break
				}
			}
		}
		label := "  " + name
		if i == m.project {
			label = accent.Bold(true).Render("› " + name)
		}
		lines = append(lines, label)
	}
	return panel(fmt.Sprintf("PROJECTS · %d", len(m.projects)-1), lines, width, height, m.focus == 1)
}

func (m model) sessionPanel(width, height int) string {
	var lines []string
	size := max(1, (height-2)/3)
	start := windowStart(m.cursor, len(m.visible), size)
	for i := start; i < min(start+size, len(m.visible)); i++ {
		row := m.visible[i]
		if i > start {
			lines = append(lines, "")
		}
		label := fit("· "+row.name(), width-4)
		if i == m.cursor {
			label = fit("● "+row.name(), width-4)
			if m.focus == 2 {
				label = selectedStyle.Render(label)
			} else {
				label = accent.Bold(true).Render(label)
			}
		}
		date := row.LastActivity.Local().Format("02/01/2006 15:04")
		meta := fmt.Sprintf("    %s  %-8s %s", date, row.Client, row.locationLabel())
		lines = append(lines, label, muted.Render(fit(meta, width-4)))
	}
	if len(m.visible) == 0 {
		message := "No sessions match these filters."
		if m.loading {
			message = "Loading your sessions..."
		}
		lines = []string{"", muted.Render(message), "", "Esc clears filters · r refreshes"}
	}
	position := 0
	if len(m.visible) > 0 {
		position = m.cursor + 1
	}
	return panel(fmt.Sprintf("SESSIONS · %d / %d", position, len(m.visible)), lines, width, height, m.focus == 2)
}

func shortcutLines(width int) []string {
	items := []string{"Tab panels", "↑↓/jk move", "/ search", "Enter open", "p preview", "d delete", "s sort", "r refresh", ", settings", "Esc reset", "q quit"}
	lines := []string{" " + items[0]}
	for _, item := range items[1:] {
		last := len(lines) - 1
		if ansi.StringWidth(lines[last]+" │ "+item) > width {
			lines = append(lines, " "+item)
		} else {
			lines[last] += " │ " + item
		}
	}
	return lines
}

func (m model) detailsLines() []string {
	row, ok := m.selected()
	lines := []string{muted.Render("Select a session to see its details.")}
	if ok {
		lines = []string{
			bold.Render(row.name()),
			muted.Render("PROJECT  ") + clean(row.projectPath()),
			muted.Render("DIRECTORY ") + clean(row.Cwd),
			muted.Render("UPDATED  ") + row.LastActivity.Local().Format("02/01/2006 15:04:05 MST"),
			muted.Render("CLIENT   ") + clean(row.Client),
			muted.Render("SESSION  ") + clean(row.ID),
		}
	}
	return lines
}

func (m model) View() tea.View {
	if m.width < 70 || m.height < 24 {
		view := tea.NewView("Resize the terminal to at least 70 columns × 24 rows.\nPress q to quit.")
		view.AltScreen = true
		return view
	}
	sortOrder := "↓ newest first"
	if m.oldest {
		sortOrder = "↑ oldest first"
	}
	header := accent.Bold(true).Render(" AGENTS ") + muted.Render(fmt.Sprintf(" %d sessions · %s", len(m.sessions), sortOrder))
	subtitle := " Find a conversation. Pick up where you left off."
	leftWidth := min(30, m.width/3)
	rightWidth := m.width - leftWidth - 1
	help := shortcutLines(m.width)
	bodyHeight := m.height - 6 - len(help)
	left := lipgloss.JoinVertical(lipgloss.Left, m.clientPanel(leftWidth), "", m.projectPanel(leftWidth, bodyHeight-len(clients)-5))
	previewHeight := max(9, min(18, bodyHeight/2))
	right := lipgloss.JoinVertical(lipgloss.Left, m.sessionPanel(rightWidth, bodyHeight-previewHeight-1), "", m.previewPanel(rightWidth, previewHeight))
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	status := "Enter opens a new " + launchLabel(m.launchTarget) + ". The browser stays here."
	if !m.refreshed.IsZero() {
		status += " · Updated " + m.refreshed.Format(time.TimeOnly)
	}
	if m.notice != "" {
		status = m.notice
	}
	if m.loading {
		status = "Refreshing sessions..."
	} else if m.opening {
		status = "Opening session..."
	} else if m.err != nil {
		status = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(clean(m.err.Error()))
	} else if m.warning != "" {
		status = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render(m.warning)
	}
	view := tea.NewView(strings.Join([]string{
		fit(header, m.width), muted.Render(fit(subtitle, m.width)),
		fit(" "+m.search.View(), m.width), "", body,
		fit(" "+status, m.width), muted.Render(strings.Join(help, "\n")), "",
	}, "\n"))
	view.AltScreen = true
	if m.pendingDelete != nil {
		view.Content = m.deleteDialog(view.Content)
	}
	if m.settingsPrompt != nil {
		view.Content = m.settingsDialog(view.Content)
	}
	if m.previewExpanded {
		view.Content = m.previewDialog(view.Content)
	}
	return view
}

func (m model) deleteDialog(background string) string {
	row := *m.pendingDelete
	prompt := m.deletePrompt
	if prompt == nil {
		prompt = newDeletePrompt()
	}
	hint := "Tab choose │ Enter confirm │ Esc cancel"
	if m.deleting {
		hint = "Deleting session..."
	} else if m.err != nil {
		hint = clean(m.err.Error())
	}
	scope := "Deletes saved history and child sessions."
	if row.Client == "cursor" {
		scope = "Deletes this saved terminal conversation."
	}
	lines := []string{
		bold.Render(row.name()),
		clean(row.Client) + " · " + clean(row.ID),
		clean(row.Cwd), "",
		scope,
		"This cannot be undone. Project files are kept.", "",
		prompt.View(), "", hint,
	}
	return m.dialogOverlay(background, "DELETE SESSION?", lines)
}

func (m model) dialogOverlay(background, title string, lines []string) string {
	width := min(64, m.width-4)
	modal := panel(title, lines, width, len(lines)+3, true)
	return lipgloss.NewCompositor(
		lipgloss.NewLayer(background),
		lipgloss.NewLayer(modal).X((m.width-width)/2).Y((m.height-lipgloss.Height(modal))/2),
	).Render()
}

func newDeletePrompt() *huh.Confirm {
	keys := huh.NewDefaultKeyMap()
	keys.Confirm.Toggle.SetKeys("tab", "shift+tab", "left", "right")
	prompt := huh.NewConfirm().Affirmative("Delete").Negative("Cancel")
	prompt.WithTheme(dialogTheme())
	prompt.WithKeyMap(keys)
	prompt.WithWidth(54)
	prompt.Focus()
	return prompt
}

func dialogTheme() huh.Theme {
	return huh.ThemeFunc(func(dark bool) *huh.Styles {
		styles := huh.ThemeBase(dark)
		styles.Focused.Base = lipgloss.NewStyle()
		styles.Focused.FocusedButton = selectedStyle.Padding(0, 2)
		styles.Focused.BlurredButton = lipgloss.NewStyle().Padding(0, 2)
		styles.Focused.Option = lipgloss.NewStyle()
		styles.Focused.SelectedOption = selectedStyle
		styles.Focused.UnselectedOption = lipgloss.NewStyle()
		styles.Focused.SelectSelector = accent.SetString("● ")
		styles.Blurred = styles.Focused
		return styles
	})
}
