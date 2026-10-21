package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type previewMessage struct {
	Role      string `json:"role"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
}

type previewData struct {
	Messages []previewMessage `json:"messages"`
	Notice   string           `json:"notice"`
}

type previewRequested struct {
	row   session
	token uint64
}

type previewLoaded struct {
	data  previewData
	token uint64
}

func sessionKey(row session) string { return row.Client + ":" + row.ID }

func (m *model) queuePreview() tea.Cmd {
	m.previewToken++
	m.previewData = previewData{}
	m.previewViewport.GotoTop()
	row, ok := m.selected()
	if !ok {
		m.previewLoading, m.previewExpanded, m.previewKey = false, false, ""
		return nil
	}
	m.previewLoading, m.previewKey = true, sessionKey(row)
	request := previewRequested{row: row, token: m.previewToken}
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return request })
}

func readPreview(request previewRequested) previewLoaded {
	result := previewLoaded{token: request.token}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "agents-list", "--client", request.row.Client, "--preview", request.row.ID, "--json").Output()
	if decodeErr := json.Unmarshal(output, &result.data); decodeErr != nil {
		if err == nil {
			err = decodeErr
		}
		result.data.Notice = fmt.Sprintf("Preview unavailable: %v", err)
	} else if err != nil && result.data.Notice == "" {
		result.data.Notice = fmt.Sprintf("Preview unavailable: %v", err)
	}
	for i := range result.data.Messages {
		result.data.Messages[i].Text = previewText(result.data.Messages[i].Text)
	}
	return result
}

func previewText(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, ansi.Strip(text))
}

func messageDate(message previewMessage) string {
	if parsed, err := time.Parse(time.RFC3339Nano, message.Timestamp); err == nil {
		return parsed.Local().Format("02/01/2006 15:04")
	}
	return ""
}

func previewMessageLine(message previewMessage, text string, width int, heading bool) string {
	prefix := "    "
	if message.Role == "user" {
		prefix = "  "
		if heading {
			prefix = "> "
		}
		return lipgloss.NewStyle().Reverse(true).Render(fit(prefix+text, width))
	}
	if heading {
		prefix = "  · "
	}
	line := fit(prefix+text, width)
	if heading {
		return accent.Bold(true).Render(line)
	}
	return line
}

func (m model) previewLines(width int) []string {
	if m.previewLoading {
		return []string{"Loading saved messages..."}
	}
	var lines []string
	for _, message := range m.previewData.Messages {
		lines = append(lines, previewMessageLine(message, messageDate(message), width, true))
		indent := 4
		if message.Role == "user" {
			indent = 2
		}
		for _, line := range strings.Split(ansi.Hardwrap(previewText(message.Text), max(1, width-indent), true), "\n") {
			lines = append(lines, previewMessageLine(message, line, width, false))
		}
		lines = append(lines, "")
	}
	if m.previewData.Notice != "" {
		lines = append(lines, clean(m.previewData.Notice))
	}
	if len(lines) == 0 {
		lines = []string{"No saved messages to preview."}
	}
	return lines
}

func (m *model) updatePreviewViewport() {
	atBottom := m.previewViewport.AtBottom()
	m.previewViewport.SetWidth(max(1, m.width-8))
	m.previewViewport.SetHeight(max(1, m.height-9))
	lines := append(m.detailsLines(), "")
	lines = append(lines, m.previewLines(max(1, m.width-8))...)
	m.previewViewport.SetContent(strings.Join(lines, "\n"))
	if atBottom {
		m.previewViewport.GotoBottom()
	}
}

func (m model) updatePreviewDialog(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "p", "esc", "q", "ctrl+c":
		m.previewExpanded = false
		return m, nil
	}
	if m.width >= 70 && m.height >= 24 {
		var cmd tea.Cmd
		m.previewViewport, cmd = m.previewViewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) previewDialog(background string) string {
	width, height := m.width-4, m.height-4
	lines := strings.Split(m.previewViewport.View(), "\n")
	lines = append(lines, "", "↑↓/jk scroll │ PgUp/PgDn page │ p/Esc close")
	modal := panel("LAST MESSAGES", lines, width, height, true)
	return lipgloss.NewCompositor(lipgloss.NewLayer(background), lipgloss.NewLayer(modal).X(2).Y(2)).Render()
}

func (m model) previewPanel(width, height int) string {
	row, ok := m.selected()
	if !ok {
		return panel("PREVIEW · p expand", []string{"Select a session to preview."}, width, height, false)
	}
	lines := []string{bold.Render(row.name()), "DIRECTORY " + clean(row.Cwd),
		row.LastActivity.Local().Format("02/01/2006 15:04:05 MST") + " · " + row.Client, ""}
	if m.previewLoading || len(m.previewData.Messages) == 0 {
		lines = append(lines, m.previewLines(width-4)...)
	} else {
		available := max(1, height-7)
		messages := m.previewData.Messages[max(0, len(m.previewData.Messages)-available):]
		for _, message := range messages {
			lines = append(lines, previewMessageLine(message, clean(message.Text), width-4, true))
		}
	}
	return panel("PREVIEW · p expand", lines, width, height, false)
}
