package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type repository struct {
	project, worktree string
}

func groupProjects(ctx context.Context, rows []session) {
	locations := map[string]repository{}
	projects := map[string]string{}
	for i := range rows {
		cwd := rows[i].Cwd
		location, found := locations[cwd]
		if !found {
			location = findRepository(ctx, cwd, projects)
			locations[cwd] = location
		}
		rows[i].Project, rows[i].Worktree = location.project, location.worktree
	}
}

func findRepository(ctx context.Context, cwd string, projects map[string]string) repository {
	fallback := repository{project: cwd}
	if cwd == "" {
		return fallback
	}
	directory := cwd
	for {
		if info, err := os.Stat(directory); err == nil && info.IsDir() {
			break
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return fallback
		}
		directory = parent
	}
	output, err := exec.CommandContext(ctx, "git", "-C", directory, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir").Output()
	if err != nil {
		return fallback
	}
	paths := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(paths) != 2 {
		return fallback
	}
	worktree, common := paths[0], paths[1]
	project, found := projects[common]
	if !found {
		output, err = exec.CommandContext(ctx, "git", "--git-dir", common, "worktree", "list", "--porcelain", "-z").Output()
		if err != nil {
			return fallback
		}
		first, _, _ := strings.Cut(string(output), "\x00")
		project, found = strings.CutPrefix(first, "worktree ")
		if !found || project == "" {
			return fallback
		}
		projects[common] = project
	}
	return repository{project: project, worktree: worktree}
}

func (s session) projectPath() string {
	if s.Project != "" {
		return s.Project
	}
	return s.Cwd
}

func (s session) locationLabel() string {
	label := filepath.Base(s.projectPath())
	if s.Worktree != "" && s.Worktree != s.projectPath() {
		label += " / " + filepath.Base(s.Worktree)
	}
	return clean(label)
}
