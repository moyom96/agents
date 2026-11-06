package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeTerminal struct {
	labels map[string]string
	argv   []string
	live   bool
	pid    int
	screen string
}

// fakeHerdr stands in for hc and herdr-app: terminals live in a map, and a
// terminal labeled with herdr.space / herdr.tab shows up in that tab.
type fakeHerdr struct {
	mu        sync.Mutex
	terminals map[string]*fakeTerminal
	names     map[string]string
	order     []string
	calls     [][]string
	mode      string
	adoptLag  int
}

func installFakeHerdr(t *testing.T, mode string) *fakeHerdr {
	t.Helper()
	fake := &fakeHerdr{terminals: map[string]*fakeTerminal{}, names: map[string]string{}, mode: mode}
	oldApp, oldHC, oldInstalled, oldPoll := herdrApp, hcCmd, herdrInstalled, herdrAdoptPoll
	t.Cleanup(func() { herdrApp, hcCmd, herdrInstalled, herdrAdoptPoll = oldApp, oldHC, oldInstalled, oldPoll })
	herdrApp = func(args ...string) ([]byte, error) { return fake.app(args) }
	hcCmd = func(args ...string) ([]byte, error) { return fake.hc(args) }
	herdrInstalled = func() bool { return true }
	herdrAdoptPoll = time.Millisecond
	return fake
}

func (f *fakeHerdr) add(labels map[string]string, argv []string, live bool, screen string) string {
	address := fmt.Sprintf("t%d", len(f.order)+1)
	f.order = append(f.order, address)
	f.terminals[address] = &fakeTerminal{labels: labels, argv: argv, live: live, pid: 5000 + len(f.order), screen: screen}
	return address
}

func flags(args []string) (map[string][]string, []string) {
	values := map[string][]string{}
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			return values, args[i+1:]
		}
		name, ok := strings.CutPrefix(args[i], "--")
		if !ok {
			values[""] = append(values[""], args[i])
			continue
		}
		if slices.Contains([]string{"bg", "json", "no-parent", "no-notify", "all"}, name) {
			values[name] = []string{"true"}
			continue
		}
		i++
		values[name] = append(values[name], args[i])
	}
	return values, nil
}

func labelMap(pairs []string) map[string]string {
	labels := map[string]string{}
	for _, pair := range pairs {
		k, v, _ := strings.Cut(pair, "=")
		labels[k] = v
	}
	return labels
}

func (f *fakeHerdr) hc(args []string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string{"hc"}, args...))
	values, argv := flags(args[1:])
	target := ""
	if len(values[""]) > 0 {
		target = values[""][0]
	}
	terminal := f.terminals[target]
	switch args[0] {
	case "run":
		live, screen := true, ""
		switch f.mode {
		case "connected":
			screen = "\n  /remote-control is active · Continue here, on your phone\n"
		case "consent":
			screen = "Enable Remote Control? (y/n)\n"
		case "exit":
			live = false
		}
		address := f.add(labelMap(values["label"]), argv, live, screen)
		return json.Marshal(map[string]any{"address": address, "state": "live"})
	case "ls":
		want := labelMap(values["label"])
		var rows []map[string]any
		for _, address := range f.order {
			t := f.terminals[address]
			if t == nil || !t.live && values["all"] == nil {
				continue
			}
			match := true
			for k, v := range want {
				match = match && t.labels[k] == v
			}
			if match {
				rows = append(rows, map[string]any{"address": address, "state": map[bool]string{true: "live", false: "finished"}[t.live]})
			}
		}
		return json.Marshal(map[string]any{"terminals": rows})
	}
	if terminal == nil {
		return []byte(`{"error":{"kind":"not_found"}}`), fmt.Errorf("exit status 3")
	}
	switch args[0] {
	case "get":
		return json.Marshal(map[string]any{"address": target, "pid": terminal.pid, "labels": terminal.labels, "state": map[bool]string{true: "live", false: "finished"}[terminal.live]})
	case "read":
		return []byte(terminal.screen), nil
	case "kill":
		terminal.live = false
		return []byte(`{}`), nil
	case "rm":
		if terminal.live {
			return []byte(`{"error":{"kind":"live"}}`), fmt.Errorf("exit status 1")
		}
		delete(f.terminals, target)
		return []byte(`{}`), nil
	case "label":
		for k, v := range labelMap(values[""][1:]) {
			terminal.labels[k] = v
		}
		return []byte(`{}`), nil
	}
	return nil, fmt.Errorf("unexpected hc %v", args)
}

func (f *fakeHerdr) app(args []string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string{"herdr-app"}, args...))
	if args[0] == "ls" {
		return json.Marshal(f.layout())
	}
	values, _ := flags(args[2:])
	one := func(name string) string {
		if len(values[name]) == 0 {
			return ""
		}
		return values[name][0]
	}
	fail := func(message string) ([]byte, error) {
		return json.Marshal(map[string]string{"error": message})
	}
	switch args[1] {
	case "tab.new":
		var argv []string
		json.Unmarshal([]byte(one("argv")), &argv)
		space := one("space")
		if space == "" {
			space = "spfocused"
		}
		tab := fmt.Sprintf("tbnew%d", len(f.order)+1)
		f.names[tab] = one("name")
		address := f.add(map[string]string{"herdr.space": space, "herdr.tab": tab}, argv, true, "")
		return json.Marshal(map[string]string{"space": space, "tab": tab, "terminal": address})
	case "space.rename", "tab.rename", "space.select", "tab.select":
		if f.adoptLag > 0 {
			f.adoptLag--
			return fail("no such id yet")
		}
		if !f.known(one("id")) {
			return fail("no such id")
		}
		if strings.HasSuffix(args[1], "rename") {
			f.names[one("id")] = one("name")
		}
		return []byte(`{"ok":true}`), nil
	}
	return nil, fmt.Errorf("unexpected herdr-app %v", args)
}

func (f *fakeHerdr) known(id string) bool {
	for _, t := range f.terminals {
		if t.live && (t.labels["herdr.space"] == id || t.labels["herdr.tab"] == id) {
			return true
		}
	}
	return false
}

func (f *fakeHerdr) layout() herdrLayout {
	var layout herdrLayout
	index := map[string]int{}
	for _, address := range f.order {
		t := f.terminals[address]
		if t == nil || !t.live {
			continue
		}
		space := t.labels["herdr.space"]
		if _, ok := index[space]; !ok {
			index[space] = len(layout.Spaces)
			layout.Spaces = append(layout.Spaces, herdrSpace{ID: space, Name: f.names[space]})
		}
		s := &layout.Spaces[index[space]]
		s.Tabs = append(s.Tabs, herdrTab{ID: t.labels["herdr.tab"], Name: f.names[t.labels["herdr.tab"]], Terminals: []herdrTerminal{{Terminal: address}}})
	}
	return layout
}

func (f *fakeHerdr) called(program, command string) [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var found [][]string
	for _, call := range f.calls {
		if call[0] == program && (call[1] == command || len(call) > 2 && call[1] == "do" && call[2] == command) {
			found = append(found, call)
		}
	}
	return found
}

func useHerdrTarget(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := saveLaunchTarget("herdr-tab"); err != nil {
		t.Fatal(err)
	}
}

func herdrFixture(t *testing.T, mode string) (session, *fakeHerdr) {
	t.Helper()
	bin, project := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("SHELL", "/bin/sh")
	for _, name := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY"} {
		t.Setenv(name, "")
	}
	useHerdrTarget(t)
	stubRemote(t, nil)
	startupTimeout, startupPoll = 200*time.Millisecond, 10*time.Millisecond
	return session{Client: "claude", ID: "4895e5d4-test", Title: "Billing fix", Cwd: project}, installFakeHerdr(t, mode)
}

func TestDefaultLaunchTargetFollowsHerdr(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HC_TERMINAL_ID", "t7")
	if config, err := loadSettings(); err != nil || config.Launch.Target != "herdr-tab" {
		t.Fatalf("inside Herdr: %q %v", config.Launch.Target, err)
	}
	t.Setenv("HC_TERMINAL_ID", "")
	if config, _ := loadSettings(); config.Launch.Target != "tmux-window" {
		t.Fatalf("outside Herdr: %q", config.Launch.Target)
	}
	if err := saveLaunchTarget("herdr-tab"); err != nil {
		t.Fatal(err)
	}
	if config, err := loadSettings(); err != nil || config.Launch.Target != "herdr-tab" {
		t.Fatalf("saved herdr-tab: %q %v", config.Launch.Target, err)
	}
	if launchLabel("herdr-tab") != "Herdr tab" {
		t.Fatal(launchLabel("herdr-tab"))
	}
}

func TestHerdrOpenUsesBrowserSpaceWithoutTmux(t *testing.T) {
	useHerdrTarget(t)
	fake := installFakeHerdr(t, "")
	browser := fake.add(map[string]string{"herdr.space": "spbrowser", "herdr.tab": "tbbrowser"}, []string{"agents"}, true, "")
	t.Setenv("HC_TERMINAL_ID", browser)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	row := session{Client: "codex", ID: "session 'with' $(characters)", Cwd: dir}
	if result := openSession(row); result.err != nil || result.target != "herdr-tab" {
		t.Fatalf("open: %+v", result)
	}
	calls := fake.called("herdr-app", "tab.new")
	if len(calls) != 1 {
		t.Fatalf("tab.new calls: %v", fake.calls)
	}
	values, _ := flags(calls[0][3:])
	if values["space"][0] != "spbrowser" || values["folder"][0] != dir || values["name"][0] != "codex:"+filepath.Base(dir) {
		t.Fatalf("tab.new arguments: %q", calls[0])
	}
	var argv []string
	if err := json.Unmarshal([]byte(values["argv"][0]), &argv); err != nil || strings.Join(argv, "\x00") != "codex\x00resume\x00"+row.ID {
		t.Fatalf("argv %q: %v", values["argv"], err)
	}
	opened := fake.terminals["t2"]
	if opened.labels["agents.kind"] != "local" || opened.labels["agents.session"] != "codex:"+row.ID {
		t.Fatalf("opened tab was not labeled: %v", opened.labels)
	}
}

func TestHerdrHandoffStartsInRemoteSpaceAndStops(t *testing.T) {
	row, fake := herdrFixture(t, "connected")
	fake.adoptLag = 2
	result := handoff(row, remoteOptions{})
	if result.Status != "started" || result.LookFor != filepath.Base(row.Cwd)+" - Billing fix" {
		t.Fatalf("handoff: %+v", result)
	}
	terminal := fake.terminals[result.Window]
	if terminal == nil || terminal.labels["agents.kind"] != "remote" || terminal.labels["agents.session"] != "claude:"+row.ID {
		t.Fatalf("remote terminal: %+v", terminal)
	}
	if len(terminal.argv) < 2 || terminal.argv[0] != "/usr/bin/env" || !strings.Contains(terminal.argv[len(terminal.argv)-1], "'--remote-control'") {
		t.Fatalf("payload: %q", terminal.argv)
	}
	space, tab := terminal.labels["herdr.space"], terminal.labels["herdr.tab"]
	if fake.names[space] != remoteSession || fake.names[tab] != result.LookFor {
		t.Fatalf("names: space %q tab %q", fake.names[space], fake.names[tab])
	}
	if again := handoff(row, remoteOptions{}); again.Status != "already_remote" || again.Window != result.Window {
		t.Fatalf("second handoff: %+v", again)
	}
	other := row
	other.ID, other.Title = "second-session", "Other"
	second := handoff(other, remoteOptions{})
	if second.Status != "started" || fake.terminals[second.Window].labels["herdr.space"] != space {
		t.Fatalf("second session did not reuse the space: %+v", second)
	}
	if listings := windowListings(); len(listings) != 2 || listings[0].Window != result.Window {
		t.Fatalf("listings: %+v", listings)
	}
	if stopped := stopRemote(row); stopped.Status != "stopped" || fake.terminals[result.Window] != nil {
		t.Fatalf("stop: %+v", stopped)
	}
	if stopRemote(row).Status != "not_remote" {
		t.Fatal("stopped session still listed")
	}
}

func TestHerdrHandoffRemovesFailedStartups(t *testing.T) {
	for mode, want := range map[string]string{"consent": "consent_required", "exit": "startup_failed"} {
		t.Run(mode, func(t *testing.T) {
			row, fake := herdrFixture(t, mode)
			if result := handoff(row, remoteOptions{}); result.Status != want || result.Window != "" {
				t.Fatalf("handoff: %+v", result)
			}
			if len(fake.terminals) != 0 {
				t.Fatalf("failed handoff left terminals: %v", fake.terminals)
			}
		})
	}
}

func TestHerdrHandoffReapsFinishedTerminals(t *testing.T) {
	row, fake := herdrFixture(t, "connected")
	dead := fake.add(map[string]string{"agents.kind": "remote", "agents.session": "claude:" + row.ID, "herdr.space": "spold", "herdr.tab": "tbold"}, nil, false, "")
	result := handoff(row, remoteOptions{})
	if result.Status != "started" || fake.terminals[dead] != nil {
		t.Fatalf("handoff: %+v, dead terminal kept: %v", result, fake.terminals[dead] != nil)
	}
	if fake.terminals[result.Window].labels["herdr.space"] == "spold" {
		t.Fatal("reused the space of a finished terminal")
	}
}

func TestHerdrHandoffDetectsAgentsTabAndMissingHerdr(t *testing.T) {
	row, fake := herdrFixture(t, "connected")
	local := fake.add(map[string]string{"agents.kind": "local", "agents.session": "claude:" + row.ID}, nil, true, "")
	pid := fake.terminals[local].pid
	listProcesses = func() ([]process, error) {
		return []process{{PID: pid, PPID: 1, Args: []string{"zsh"}}, {PID: 907, PPID: pid, Args: []string{"claude", "--resume", row.ID}}}, nil
	}
	if result := handoff(row, remoteOptions{}); result.Status != "open_in_agents_window" || result.Window != local {
		t.Fatalf("agents tab: %+v", result)
	}
	herdrInstalled = func() bool { return false }
	if result := handoff(row, remoteOptions{}); result.Status != "herdr_missing" {
		t.Fatalf("missing herdr: %+v", result)
	}
}

func TestHerdrAttachSelectsTheRemoteTab(t *testing.T) {
	_, fake := herdrFixture(t, "connected")
	address := fake.add(map[string]string{"herdr.space": "sp1", "herdr.tab": "tb1"}, nil, true, "")
	if err := remote().attach(address); err != nil {
		t.Fatal(err)
	}
	if len(fake.called("herdr-app", "space.select")) != 1 || len(fake.called("herdr-app", "tab.select")) != 1 {
		t.Fatalf("calls: %v", fake.calls)
	}
}
