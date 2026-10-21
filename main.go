package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

var clients = []string{"All clients", "claude", "codex", "opencode", "cursor"}

type model struct {
	sessions                        []session
	visible                         []session
	projects                        []string
	client, project, cursor         int
	focus, width, height            int
	search                          textinput.Model
	loading, opening, oldest        bool
	warning, notice                 string
	err                             error
	refreshed                       time.Time
	pendingDelete                   *session
	deleting                        bool
	deletePrompt                    *huh.Confirm
	launchTarget                    string
	settingsPrompt                  *huh.Select[string]
	savingSettings                  bool
	previewData                     previewData
	previewKey                      string
	previewToken                    uint64
	previewLoading, previewExpanded bool
	previewViewport                 viewport.Model
}

func newModel() model {
	search := textinput.New()
	search.Prompt = "/ "
	search.Placeholder = "Search session names..."
	search.CharLimit = 200
	styles := textinput.DefaultStyles(true)
	styles.Focused.Prompt, styles.Blurred.Prompt = accent, muted
	styles.Focused.Placeholder, styles.Blurred.Placeholder = muted, muted
	styles.Blurred.Text = bold.UnsetBold()
	search.SetStyles(styles)
	config, _ := loadSettings()
	return model{search: search, focus: 2, loading: true, projects: []string{""}, launchTarget: config.Launch.Target, previewViewport: viewport.New()}
}

func (m model) Init() tea.Cmd {
	return func() tea.Msg { return readSessions() }
}

func (m model) selected() (session, bool) {
	if m.cursor >= 0 && m.cursor < len(m.visible) {
		return m.visible[m.cursor], true
	}
	return session{}, false
}

func (m *model) filter(keepSelection bool) {
	previous, _ := m.selected()
	m.visible = nil
	for _, row := range m.sessions {
		if m.client > 0 && row.Client != clients[m.client] {
			continue
		}
		if m.project > 0 && row.projectPath() != m.projects[m.project] {
			continue
		}
		if !strings.Contains(strings.ToLower(row.name()), strings.ToLower(m.search.Value())) {
			continue
		}
		m.visible = append(m.visible, row)
	}
	sort.SliceStable(m.visible, func(i, j int) bool {
		if m.oldest {
			return m.visible[i].LastActivity.Before(m.visible[j].LastActivity)
		}
		return m.visible[i].LastActivity.After(m.visible[j].LastActivity)
	})
	m.cursor = 0
	if keepSelection {
		for i, row := range m.visible {
			if row.ID == previous.ID && row.Client == previous.Client {
				m.cursor = i
				break
			}
		}
	}
}

func (m *model) updateProjects() {
	previous := ""
	if m.project < len(m.projects) {
		previous = m.projects[m.project]
	}
	paths := map[string]bool{}
	for _, row := range m.sessions {
		if (m.client == 0 || row.Client == clients[m.client]) && row.projectPath() != "" {
			paths[row.projectPath()] = true
		}
	}
	m.projects = []string{""}
	for path := range paths {
		m.projects = append(m.projects, path)
	}
	sort.Slice(m.projects[1:], func(i, j int) bool {
		a, b := m.projects[i+1], m.projects[j+1]
		if filepath.Base(a) == filepath.Base(b) {
			return a < b
		}
		return filepath.Base(a) < filepath.Base(b)
	})
	m.project = 0
	for i, path := range m.projects {
		if path == previous {
			m.project = i
		}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	before, hadSelection := m.selected()
	next, cmd := m.update(msg)
	updated := next.(model)
	after, hasSelection := updated.selected()
	_, refreshed := msg.(sessionsLoaded)
	if refreshed || hadSelection != hasSelection || sessionKey(before) != sessionKey(after) {
		previewCmd := updated.queuePreview()
		if cmd == nil {
			return updated, previewCmd
		}
		if previewCmd != nil {
			return updated, tea.Batch(cmd, previewCmd)
		}
	}
	return updated, cmd
}

func (m model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case previewRequested:
		if msg.token == m.previewToken && sessionKey(msg.row) == m.previewKey {
			return m, func() tea.Msg { return readPreview(msg) }
		}
	case previewLoaded:
		if msg.token == m.previewToken {
			m.previewData, m.previewLoading = msg.data, false
			m.updatePreviewViewport()
			m.previewViewport.GotoBottom()
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.search.SetWidth(max(10, m.width-8))
		m.updatePreviewViewport()
	case sessionsLoaded:
		m.loading, m.err, m.warning = false, msg.err, msg.warning
		if msg.err == nil {
			m.sessions, m.refreshed = msg.sessions, time.Now()
			m.updateProjects()
			m.filter(true)
		}
	case sessionOpened:
		m.opening, m.err = false, msg.err
		if msg.err == nil {
			m.notice = "Opened " + msg.name + " in a new " + launchLabel(msg.target)
		}
	case settingsSaved:
		m.savingSettings, m.err = false, msg.err
		if msg.err == nil {
			m.launchTarget, m.settingsPrompt = msg.target, nil
			m.notice = "Settings saved · Sessions open in a new " + launchLabel(msg.target)
		}
	case sessionDeleted:
		m.deleting, m.err = false, msg.err
		if msg.err == nil {
			m.pendingDelete = nil
			rows := make([]session, 0, len(m.sessions))
			for _, row := range m.sessions {
				if row.ID != msg.row.ID || row.Client != msg.row.Client {
					rows = append(rows, row)
				}
			}
			m.sessions = rows
			cursor := m.cursor
			m.updateProjects()
			m.filter(false)
			m.cursor = min(cursor, max(0, len(m.visible)-1))
			m.notice, m.loading = "Deleted "+msg.row.name(), true
			return m, m.Init()
		}
	case tea.KeyPressMsg:
		key := msg.String()
		if m.previewExpanded {
			return m.updatePreviewDialog(msg)
		}
		if m.settingsPrompt != nil {
			return m.updateSettingsDialog(msg)
		}
		if m.pendingDelete != nil {
			return m.updateDeleteDialog(msg)
		}
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		if m.search.Focused() {
			switch key {
			case "esc", "enter", "tab":
				m.search.Blur()
				return m, nil
			default:
				var cmd tea.Cmd
				m.search, cmd = m.search.Update(msg)
				m.filter(false)
				return m, cmd
			}
		}
		switch key {
		case "p":
			if _, ok := m.selected(); ok && m.width >= 70 && m.height >= 24 {
				m.previewExpanded = true
				m.updatePreviewViewport()
				m.previewViewport.GotoBottom()
			}
		case ",":
			if !m.opening && m.width >= 70 && m.height >= 24 {
				config, err := loadSettings()
				m.err = err
				if err == nil {
					m.settingsPrompt = newSettingsPrompt(config.Launch.Target)
				}
			}
		case "q":
			return m, tea.Quit
		case "/":
			return m, m.search.Focus()
		case "tab":
			m.focus = (m.focus + 2) % 3
		case "shift+tab":
			m.focus = (m.focus + 1) % 3
		case "1", "2", "3":
			m.focus = int(key[0] - '1')
		case "up", "k", "down", "j", "pgup", "pgdown", "home", "end":
			m.navigate(key)
		case "esc":
			m.search.SetValue("")
			m.client, m.project, m.err, m.notice = 0, 0, nil, ""
			m.updateProjects()
			m.filter(false)
		case "s":
			m.oldest = !m.oldest
			m.filter(true)
		case "d":
			if row, ok := m.selected(); ok && !m.loading && !m.opening && m.width >= 70 && m.height >= 24 {
				m.pendingDelete, m.err = &row, nil
				m.deletePrompt = newDeletePrompt()
			}
		case "r":
			if !m.loading {
				m.loading, m.err, m.notice = true, nil, ""
				return m, m.Init()
			}
		case "enter":
			if m.focus != 2 {
				m.focus = 2
			} else if row, ok := m.selected(); ok && !m.opening {
				config, err := loadSettings()
				if err != nil {
					m.err = err
					return m, nil
				}
				m.launchTarget = config.Launch.Target
				m.opening, m.err, m.notice = true, nil, ""
				return m, func() tea.Msg { return openSession(row) }
			}
		default:
			if letters := []rune(key); len(letters) == 1 && unicode.IsLetter(letters[0]) {
				m.search.SetValue(key)
				m.search.CursorEnd()
				m.filter(false)
				return m, m.search.Focus()
			}
		}
	default:
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) updateDeleteDialog(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.deleting {
		return m, nil
	}
	if (m.width < 70 || m.height < 24) && key != "esc" && key != "n" && key != "q" && key != "ctrl+c" {
		return m, nil
	}
	switch key {
	case "esc", "n", "q", "ctrl+c":
		m.pendingDelete, m.err = nil, nil
	case "tab", "shift+tab", "left", "right":
		m.deletePrompt.Update(msg)
	case "y", "Y":
		m.deletePrompt.Update(msg)
		fallthrough
	case "enter":
		if !m.deletePrompt.GetValue().(bool) {
			m.pendingDelete = nil
			return m, nil
		}
		row := *m.pendingDelete
		m.deleting, m.err = true, nil
		return m, func() tea.Msg { return deleteSession(row) }
	}
	return m, nil
}

func (m *model) navigate(key string) {
	position, count := &m.cursor, len(m.visible)
	if m.focus == 0 {
		position, count = &m.client, len(clients)
	} else if m.focus == 1 {
		position, count = &m.project, len(m.projects)
	}
	delta := 1
	if key == "up" || key == "k" || key == "pgup" {
		delta = -1
	}
	if key == "pgup" || key == "pgdown" {
		delta *= 5
	}
	*position = max(0, min(count-1, *position+delta))
	if key == "home" {
		*position = 0
	} else if key == "end" {
		*position = max(0, count-1)
	}
	if m.focus == 0 {
		m.project = 0
		m.updateProjects()
	}
	if m.focus != 2 {
		m.filter(false)
	}
}

func main() {
	if _, err := tea.NewProgram(newModel()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "ai-sessions-tui:", err)
		os.Exit(1)
	}
}
