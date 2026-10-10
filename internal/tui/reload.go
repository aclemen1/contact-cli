package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/aclemen1/contact-cli/internal/actions"
	"github.com/aclemen1/contact-cli/internal/config"
)

// reloadedEnv tells the new process that it replaces a TUI after a rebuild.
const reloadedEnv = "CONTACT_TUI_RELOADED"

// watchEvery is how often the TUI looks at its binary; 0 turns the watch off.
var watchEvery = 2 * time.Second

// reloadMsg asks for a reload (SIGUSR1), under the same rules as a rebuild.
type reloadMsg struct{}

type watchMsg struct{ bin string }

// self is the running binary, followed through symlinks.
func self() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// binStamp is the binary's mtime and inode; a rebuild changes one of them.
func binStamp(path string) string {
	if path == "" {
		return ""
	}
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	var ino uint64
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		ino = uint64(st.Ino)
	}
	return fmt.Sprintf("%d/%d", fi.ModTime().UnixNano(), ino)
}

// buildLabel is the version and the build time of a binary.
func buildLabel(path string) string {
	label := "v" + actions.Version
	if fi, err := os.Stat(path); err == nil {
		label += " · " + fi.ModTime().Format("02.01 15:04:05")
	}
	return label
}

func (m *Model) watch() tea.Cmd {
	if watchEvery == 0 || m.exe == "" {
		return nil
	}
	exe := m.exe
	return tea.Tick(watchEvery, func(time.Time) tea.Msg { return watchMsg{binStamp(exe)} })
}

// idle is true when no modal is open and no job runs: nothing is being typed,
// picked or written.
func (m *Model) idle() bool { return !m.modal.Open() && m.busy.Running() == 0 }

// maybeReload quits for a re-exec once a new binary waits and the TUI is idle.
func (m *Model) maybeReload() tea.Cmd {
	if !m.newBin || !m.idle() || m.exe == "" {
		return nil
	}
	m.reexec = true
	return tea.Quit
}

// saved is what a reload keeps: the view, the filters and the selection.
type saved struct {
	Full   bool   `json:"full,omitempty"`
	Alias  string `json:"alias,omitempty"`
	Filter string `json:"filter,omitempty"`
	Kind   int    `json:"kind,omitempty"`
	Sphere int    `json:"sphere,omitempty"`
}

func statePath() string { return filepath.Join(config.StateDir(), "tui.json") }

func (m *Model) persist() {
	if m.statePath == "" {
		return
	}
	s := saved{Full: m.full, Filter: m.filter, Kind: m.kind, Sphere: m.sphere}
	if c := m.current(); c != nil {
		s.Alias = c.Alias
	}
	b, _ := json.Marshal(s)
	if os.MkdirAll(filepath.Dir(m.statePath), 0o755) != nil {
		return
	}
	tmp := m.statePath + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, m.statePath)
	}
}

// restore brings back the state saved before a reload.
func (m *Model) restore() {
	b, err := os.ReadFile(m.statePath)
	if err != nil {
		return
	}
	var s saved
	if json.Unmarshal(b, &s) != nil {
		return
	}
	m.filter, m.kind, m.sphere = s.Filter, s.Kind, s.Sphere
	if m.kind < 0 || m.kind >= len(kinds) {
		m.kind = 0
	}
	if m.sphere < 0 || m.sphere > len(m.spheres) {
		m.sphere = 0
	}
	m.reload()
	m.selectAlias(s.Alias)
	if s.Full && m.current() != nil {
		m.full = true
		m.initCmd = m.loadShown() // run at Init
	}
}

// run starts the program; SIGUSR1 asks for a reload, and a reload replaces
// the process with the binary on disk, which restores the saved state.
func run(m *Model) error {
	p := tea.NewProgram(m)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGUSR1)
	defer signal.Stop(sig)
	go func() {
		for range sig {
			p.Send(reloadMsg{})
		}
	}()
	if _, err := p.Run(); err != nil {
		return err
	}
	if !m.reexec {
		return nil
	}
	m.persist()
	signal.Stop(sig)
	return syscall.Exec(m.exe, os.Args, append(os.Environ(), reloadedEnv+"="+buildLabel(m.exe)))
}
