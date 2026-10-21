package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func testGit(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func TestWorktreesGroupUnderMainRepositoryAndKeepTheirDirectory(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main project")
	worktree := filepath.Join(dir, "feature branch")
	other := filepath.Join(dir, "other", "main project")
	testGit(t, "init", "-b", "main", main)
	testGit(t, "-C", main, "-c", "user.name=Session Test", "-c", "user.email=session@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "--allow-empty", "-m", "Initial")
	testGit(t, "-C", main, "worktree", "add", "-b", "feature", worktree)
	testGit(t, "init", "-b", "main", other)
	rows := []session{
		{Client: "claude", ID: "main", Cwd: main},
		{Client: "codex", ID: "worktree", Cwd: worktree},
		{Client: "codex", ID: "other", Cwd: other},
		{Client: "claude", ID: "removed", Cwd: filepath.Join(main, ".worktrees", "removed")},
	}
	groupProjects(context.Background(), rows)
	if rows[0].Project != main || rows[1].Project != main || rows[3].Project != main {
		t.Fatalf("worktrees were not grouped: %#v", rows)
	}
	if rows[1].Cwd != worktree || rows[1].Worktree != worktree {
		t.Fatal("worktree launch directory was changed")
	}
	if rows[2].Project != other {
		t.Fatal("unrelated repositories with the same basename were combined")
	}
	if rows[1].locationLabel() != "main project / feature branch" {
		t.Fatal(rows[1].locationLabel())
	}
	m := newModel()
	m.sessions = rows
	m.updateProjects()
	if len(m.projects) != 3 {
		t.Fatalf("expected all + two repositories, got %v", m.projects)
	}
	for i, project := range m.projects {
		if project == main {
			m.project = i
		}
	}
	m.filter(false)
	if len(m.visible) != 3 {
		t.Fatalf("group filter omitted worktree sessions: %#v", m.visible)
	}
	m.client = 2
	m.updateProjects()
	m.filter(false)
	if len(m.visible) != 1 || m.visible[0].ID != "worktree" {
		t.Fatalf("combined client/project filter returned %#v", m.visible)
	}
}

func TestNonRepositoryFoldersStaySeparate(t *testing.T) {
	dir := t.TempDir()
	rows := []session{{Cwd: dir}, {Cwd: filepath.Join(dir, "missing")}, {Cwd: ""}}
	groupProjects(context.Background(), rows)
	for _, row := range rows {
		if row.Project != row.Cwd {
			t.Fatalf("non-repository folder was regrouped: %#v", row)
		}
	}
}
