package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"
)

var sessionUUID = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

type sessionDeleted struct {
	row session
	err error
}

func deleteSession(row session) sessionDeleted {
	result := sessionDeleted{row: row}
	if row.Client == "claude" {
		result.err = deleteClaudeSession(row.ID)
		return result
	}
	if row.Client == "cursor" {
		result.err = deleteCursorSession(row.ID)
		return result
	}
	var args []string
	switch row.Client {
	case "codex":
		if !sessionUUID.MatchString(row.ID) {
			result.err = fmt.Errorf("invalid Codex session ID")
			return result
		}
		args = []string{"codex", "delete", "--force", row.ID}
	case "opencode":
		if !regexp.MustCompile(`^ses_[A-Za-z0-9]+$`).MatchString(row.ID) {
			result.err = fmt.Errorf("invalid OpenCode session ID")
			return result
		}
		args = []string{"opencode", "session", "delete", row.ID}
	default:
		result.err = fmt.Errorf("unsupported client: %s", clean(row.Client))
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
	if err != nil {
		result.err = fmt.Errorf("delete session: %v. %s", err, clean(string(output)))
	}
	return result
}

func deleteCursorSession(id string) error {
	if !sessionUUID.MatchString(id) {
		return fmt.Errorf("invalid Cursor session ID")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	paths, err := filepath.Glob(filepath.Join(home, ".cursor", "chats", "*", id))
	if err != nil {
		return err
	}
	if len(paths) != 1 {
		return fmt.Errorf("expected one saved Cursor chat, found %d", len(paths))
	}
	return os.RemoveAll(paths[0])
}

func deleteClaudeSession(id string) error {
	if !sessionUUID.MatchString(id) {
		return fmt.Errorf("invalid Claude session ID")
	}
	home := os.Getenv("CLAUDE_CONFIG_DIR")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		home = filepath.Join(userHome, ".claude")
	}
	paths, err := filepath.Glob(filepath.Join(home, "projects", "*", id+".jsonl"))
	if err != nil {
		return err
	}
	if len(paths) != 1 {
		return fmt.Errorf("expected one saved Claude transcript, found %d", len(paths))
	}
	if err := os.RemoveAll(filepath.Join(filepath.Dir(paths[0]), id)); err != nil {
		return fmt.Errorf("delete companion session data: %w", err)
	}
	if err := os.Remove(paths[0]); err != nil {
		return fmt.Errorf("delete transcript: %w", err)
	}
	return nil
}
