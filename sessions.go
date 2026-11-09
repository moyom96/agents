package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode"
)

type session struct {
	Client       string    `json:"client"`
	ID           string    `json:"id"`
	Cwd          string    `json:"cwd"`
	Title        string    `json:"title"`
	LastActivity time.Time `json:"last_activity"`
	Project      string    `json:"-"`
	Worktree     string    `json:"-"`
}

type sessionsLoaded struct {
	sessions []session
	warning  string
	err      error
}

func readSessions() sessionsLoaded {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "agents-list", "--json", "-n", "0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil && len(output) == 0 {
		return sessionsLoaded{err: fmt.Errorf("read sessions: %v. %s", err, clean(stderr.String()))}
	}
	var rows []session
	if err := json.Unmarshal(output, &rows); err != nil {
		return sessionsLoaded{err: fmt.Errorf("read session data: %w", err)}
	}
	groupProjects(ctx, rows)
	return sessionsLoaded{sessions: rows, warning: clean(stderr.String())}
}

type messagesSearched struct {
	query   string
	matches map[string]string
	err     error
}

func searchMessages(query string) messagesSearched {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "agents-list", "--json", "-n", "0", "--grep="+query).Output()
	result := messagesSearched{query: query, matches: map[string]string{}}
	if err != nil && len(output) == 0 {
		result.err = fmt.Errorf("search messages: %v", err)
		return result
	}
	var rows []struct {
		session
		Match struct {
			Text string `json:"text"`
		} `json:"match"`
	}
	if err := json.Unmarshal(output, &rows); err != nil {
		result.err = fmt.Errorf("read message search: %w", err)
		return result
	}
	for _, row := range rows {
		result.matches[sessionKey(row.session)] = clean(row.Match.Text)
	}
	return result
}

func clean(value string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)), " ")
}

func (s session) name() string {
	if title := clean(s.Title); title != "" {
		return title
	}
	return "Untitled session"
}
