package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Herdr has two CLIs: herdr-app arranges spaces and tabs in the window,
// and hc (herdr-core) runs the terminals inside them.
var (
	herdrApp       = func(args ...string) ([]byte, error) { return runTool("herdr-app", args...) }
	hcCmd          = func(args ...string) ([]byte, error) { return runTool("hc", args...) }
	herdrInstalled = func() bool {
		_, appErr := exec.LookPath("herdr-app")
		_, hcErr := exec.LookPath("hc")
		return appErr == nil && hcErr == nil
	}
	herdrAdoptPoll = 100 * time.Millisecond
)

const herdrAdoptTries = 30

func runTool(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = withoutEnv(os.Environ(), nestedAgentEnv...)
	return cmd.Output()
}

type herdrTerminal struct {
	Terminal string `json:"terminal"`
}

type herdrTab struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Terminals []herdrTerminal `json:"terminals"`
}

type herdrSpace struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Machine *string    `json:"machine"`
	Tabs    []herdrTab `json:"tabs"`
}

type herdrLayout struct {
	Spaces []herdrSpace `json:"spaces"`
}

func loadHerdrLayout() (herdrLayout, error) {
	var layout herdrLayout
	output, err := herdrApp("ls")
	if err == nil {
		err = json.Unmarshal(output, &layout)
	}
	return layout, err
}

// herdrDo runs an app command; herdr-app prints {"error": ...} when one fails.
func herdrDo(args ...string) (herdrTerminal, error) {
	output, err := herdrApp(append([]string{"do"}, args...)...)
	var made struct {
		Terminal string `json:"terminal"`
		Error    string `json:"error"`
	}
	json.Unmarshal(output, &made)
	if made.Error != "" {
		return herdrTerminal{}, errors.New(clean(made.Error))
	}
	return herdrTerminal{Terminal: made.Terminal}, err
}

// browserSpace is the local space whose tab holds this browser's terminal.
func browserSpace() string {
	self := os.Getenv("HC_TERMINAL_ID")
	layout, err := loadHerdrLayout()
	if self == "" || err != nil {
		return ""
	}
	for _, space := range layout.Spaces {
		for _, tab := range space.Tabs {
			for _, terminal := range tab.Terminals {
				if space.Machine == nil && terminal.Terminal == self {
					return space.ID
				}
			}
		}
	}
	return ""
}

// Herdr runs argv in the user's interactive shell, which stays open after
// the client exits, like the tmux window does.
func openHerdrTab(row session, resume []string) error {
	if !herdrInstalled() {
		return fmt.Errorf("herdr-tab needs hc and herdr-app on PATH")
	}
	argv, _ := json.Marshal(resume)
	args := []string{"tab.new", "--name", clean(row.Client + ":" + filepath.Base(row.Cwd)), "--folder", row.Cwd, "--argv", string(argv)}
	if space := browserSpace(); space != "" {
		args = append(args, "--space", space)
	}
	made, err := herdrDo(args...)
	if err != nil || made.Terminal == "" {
		return fmt.Errorf("open Herdr tab: %v", err)
	}
	if _, err := hcCmd("label", made.Terminal, "agents.kind=local", "agents.session="+sessionKey(row)); err != nil {
		return fmt.Errorf("the tab opened, but labeling it for phone handoff failed: %v", err)
	}
	return nil
}

type herdrBackend struct{}

type hcTerminal struct {
	Address string            `json:"address"`
	State   string            `json:"state"`
	PID     int               `json:"pid"`
	Labels  map[string]string `json:"labels"`
}

func hcGet(address string) (hcTerminal, error) {
	var terminal hcTerminal
	output, err := hcCmd("get", address, "--json")
	if err == nil {
		err = json.Unmarshal(output, &terminal)
	}
	return terminal, err
}

// hcList returns the terminals carrying every label, with their labels and PIDs.
func hcList(all bool, labels ...string) []hcTerminal {
	args := []string{"ls", "--json"}
	if all {
		args = append(args, "--all")
	}
	for _, label := range labels {
		args = append(args, "--label", label)
	}
	output, err := hcCmd(args...)
	var listed struct {
		Terminals []hcTerminal `json:"terminals"`
	}
	if err != nil || json.Unmarshal(output, &listed) != nil {
		return nil
	}
	var terminals []hcTerminal
	for _, row := range listed.Terminals {
		if terminal, err := hcGet(row.Address); err == nil {
			terminals = append(terminals, terminal)
		}
	}
	return terminals
}

func (herdrBackend) name() string { return "herdr" }

func (herdrBackend) where() string { return "Herdr space " + remoteSession }

func (herdrBackend) available() bool { return herdrInstalled() }

func (herdrBackend) windows() []remoteWindow {
	var windows []remoteWindow
	for _, terminal := range hcList(true, "agents.kind=remote") {
		windows = append(windows, remoteWindow{ID: terminal.Address, Key: terminal.Labels["agents.session"], Dead: terminal.State != "live", PanePID: terminal.PID})
	}
	return windows
}

func (herdrBackend) localWindows(key string) []localWindow {
	var windows []localWindow
	for _, terminal := range hcList(false, "agents.kind=local", "agents.session="+key) {
		windows = append(windows, localWindow{ID: terminal.Address, PanePID: terminal.PID})
	}
	return windows
}

// remoteSpace finds the agents-remote space, or picks an id for a new one.
// A space closes with its last tab, so a new id is normal after all handoffs end.
func remoteSpace() (string, bool) {
	for _, terminal := range hcList(false, "agents.kind=remote") {
		if space := terminal.Labels["herdr.space"]; space != "" {
			return space, true
		}
	}
	if layout, err := loadHerdrLayout(); err == nil {
		for _, space := range layout.Spaces {
			if space.Machine == nil && space.Name == remoteSession {
				return space.ID, true
			}
		}
	}
	return herdrID("sp"), false
}

func herdrID(prefix string) string {
	buf := make([]byte, 16)
	rand.Read(buf)
	return prefix + hex.EncodeToString(buf)
}

// hc runs the payload directly rather than through tab.new, whose wrapping
// shell would outlive Claude and hide a failed startup. When Claude exits, the
// terminal is finished but keeps its screen until removed, like remain-on-exit.
// The herdr.space and herdr.tab labels put it in a Herdr tab.
func (herdrBackend) create(name, cwd, key string, payload []string) (string, error) {
	space, exists := remoteSpace()
	tab := herdrID("tb")
	args := []string{"run", "--bg", "--json", "--no-parent", "--no-notify", "--cwd", cwd,
		"--label", "herdr.space=" + space, "--label", "herdr.tab=" + tab,
		"--label", "agents.kind=remote", "--label", "agents.session=" + key, "--"}
	output, err := hcCmd(append(args, payload...)...)
	var made hcTerminal
	if err != nil || json.Unmarshal(output, &made) != nil || made.Address == "" {
		return "", fmt.Errorf("hc could not start the remote terminal")
	}
	if !exists {
		renameWhenAdopted("space.rename", space, remoteSession)
	}
	renameWhenAdopted("tab.rename", tab, name)
	return made.Address, nil
}

// Herdr adopts a labeled terminal asynchronously, so its new space and tab
// can't be renamed at once. Names are cosmetic: give up quietly.
func renameWhenAdopted(command, id, name string) {
	for range herdrAdoptTries {
		if _, err := herdrDo(command, "--id", id, "--name", name); err == nil {
			return
		}
		time.Sleep(herdrAdoptPoll)
	}
}

func (herdrBackend) kill(id string) error {
	hcCmd("kill", id, "--json")
	_, err := hcCmd("rm", id, "--json")
	return err
}

func (herdrBackend) screen(id string) (string, bool, error) {
	terminal, err := hcGet(id)
	if err != nil {
		return "", true, err
	}
	text, _ := hcCmd("read", id, "--lines", "0")
	return string(text), terminal.State != "live", nil
}

func (herdrBackend) attach(id string) error {
	terminal, err := hcGet(id)
	space, tab := terminal.Labels["herdr.space"], terminal.Labels["herdr.tab"]
	if err != nil || space == "" || tab == "" {
		return fmt.Errorf("cannot find the Herdr tab for this remote session")
	}
	if _, err := herdrDo("space.select", "--id", space); err != nil {
		return fmt.Errorf("show the remote session: %v", err)
	}
	if _, err := herdrDo("tab.select", "--id", tab); err != nil {
		return fmt.Errorf("show the remote session: %v", err)
	}
	return nil
}
