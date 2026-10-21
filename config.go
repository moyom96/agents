package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type settings struct {
	Launch struct {
		Target string `toml:"target"`
	} `toml:"launch"`
}

func loadSettings() (settings, error) {
	var config settings
	config.Launch.Target = "tmux-window"
	path, err := settingsPath()
	if err != nil {
		return config, err
	}
	meta, err := toml.DecodeFile(path, &config)
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}
	if err != nil {
		return config, fmt.Errorf("read settings: %w", err)
	}
	if keys := meta.Undecoded(); len(keys) != 0 {
		return config, fmt.Errorf("unknown setting: %s", keys[0])
	}
	if config.Launch.Target != "tmux-window" && config.Launch.Target != "terminal-tab" {
		return config, fmt.Errorf("launch.target must be tmux-window or terminal-tab")
	}
	return config, nil
}

func settingsPath() (string, error) {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "agents", "config.toml"), nil
}

func saveLaunchTarget(target string) error {
	if target != "tmux-window" && target != "terminal-tab" {
		return fmt.Errorf("invalid launch target")
	}
	config, err := loadSettings()
	if err != nil {
		return err
	}
	path, err := settingsPath()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("settings symlink has no target")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".settings-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	mode := os.FileMode(0644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	config.Launch.Target = target
	if err := toml.NewEncoder(file).Encode(config); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func launchLabel(target string) string {
	if target == "terminal-tab" {
		return "terminal tab"
	}
	return "tmux window"
}
