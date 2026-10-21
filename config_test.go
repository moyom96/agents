package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchSettings(t *testing.T) {
	for _, tt := range []struct{ name, content, target, wantErr string }{
		{"missing", "", "tmux-window", ""},
		{"default", "[launch]\n", "tmux-window", ""},
		{"tab", "[launch]\ntarget = 'terminal-tab'\n", "terminal-tab", ""},
		{"invalid target", "[launch]\ntarget = 'window'\n", "", "launch.target"},
		{"typo", "[launch]\ntargets = 'terminal-tab'\n", "", "unknown setting"},
		{"invalid TOML", "[launch", "", "read settings"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			if tt.content != "" {
				if err := os.Mkdir(filepath.Join(dir, "agents"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "agents", "config.toml"), []byte(tt.content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			config, err := loadSettings()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v", err)
				}
			} else if err != nil || config.Launch.Target != tt.target {
				t.Fatalf("target = %q, error = %v", config.Launch.Target, err)
			}
		})
	}
}

func TestLaunchSettingsUsesHomeAndReloads(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "agents")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	for _, target := range []string{"terminal-tab", "tmux-window"} {
		if err := os.WriteFile(path, []byte("[launch]\ntarget = '"+target+"'\n"), 0644); err != nil {
			t.Fatal(err)
		}
		config, err := loadSettings()
		if err != nil || config.Launch.Target != target {
			t.Fatalf("settings were not reloaded: %v", err)
		}
	}
}
