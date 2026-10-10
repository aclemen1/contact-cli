// Package config reads ~/.config/contact/config.yaml.
package config

import (
	"errors"
	"fmt"
	"github.com/aclemen1/tuikit/complete"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Spheres  map[string]Sphere `yaml:"spheres"`
	Contacts Contacts          `yaml:"contacts,omitempty"`
	Hooks    []Hook            `yaml:"hooks,omitempty"`
	// Views are the commands whose output show adds under a card: what cites it elsewhere.
	Views []View `yaml:"views,omitempty"`
	// Notes are kept by another tool: add reads the text on stdin, ls writes JSON; {ref} is contact:<alias>, {sphere}, {by}.
	Notes Notes `yaml:"notes,omitempty"`
	// Complete names, per form field, the shared completion sources of the TUI.
	Complete map[string]complete.Uses `yaml:"complete,omitempty"`

	path string
}

type Notes struct {
	Add []string `yaml:"add,omitempty"`
	Ls  []string `yaml:"ls,omitempty"`
}

// Hook runs a command after an event of a card; the event comes as JSON on stdin.
type Hook struct {
	Events  []string `yaml:"events,omitempty"`  // empty: every event
	Kind    string   `yaml:"kind,omitempty"`    // keep cards of this kind
	Sphere  string   `yaml:"sphere,omitempty"`  // keep cards of this sphere
	Run     []string `yaml:"run"`               // argv; a leading ~ is expanded
	Timeout string   `yaml:"timeout,omitempty"` // default 30s
}

// View is a command run by show; {ref} becomes contact:<alias>, {alias} the alias.
// A JSON envelope {ok, result} on stdout is kept as its result, other output as text.
type View struct {
	Label   string   `yaml:"label"`
	Run     []string `yaml:"run"`
	Timeout string   `yaml:"timeout,omitempty"` // default 20s
}

// HookEvents are the events a hook can listen to.
var HookEvents = []string{"added", "updated", "removed", "moved", "synced"}

// Contacts is the two-way sync of the cards with an address book.
type Contacts struct {
	Bin string `yaml:"bin,omitempty"` // CLI of the address book, default macos
	// Container is the address book the cards live in, by name or id; default iCloud.
	Container string `yaml:"container,omitempty"`
	// Client names the change cursor of the sync in the address book; default contact.
	Client string `yaml:"client,omitempty"`
}

type Sphere struct {
	Root string `yaml:"root"`
	VCS  string `yaml:"vcs,omitempty"` // jj (default), git, none
}

var sphereName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// Path resolves the configuration file: the flag, then CONTACT_CONFIG, then
// ~/.config/contact/config.yaml.
func Path(flag string) string {
	if flag != "" {
		return Expand(flag)
	}
	if env := os.Getenv("CONTACT_CONFIG"); env != "" {
		return Expand(env)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "contact", "config.yaml")
}

// Load reads the configuration. A missing file yields an empty one.
func Load(flag string) (*Config, error) {
	p := Path(flag)
	c := &Config{Spheres: map[string]Sphere{}, path: p}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		c.fill()
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	c.fill()
	for i, h := range c.Hooks {
		if len(h.Run) == 0 {
			return nil, fmt.Errorf("%s: hook %d has no run", p, i+1)
		}
		for _, e := range h.Events {
			if !contains(HookEvents, e) {
				return nil, fmt.Errorf("%s: hook %d: event %q; expected one of %s", p, i+1, e, strings.Join(HookEvents, ", "))
			}
		}
	}
	for i, v := range c.Views {
		if len(v.Run) == 0 {
			return nil, fmt.Errorf("%s: view %d has no run", p, i+1)
		}
	}
	return c, nil
}

func (c *Config) fill() {
	if c.Spheres == nil {
		c.Spheres = map[string]Sphere{}
	}
	if c.Contacts.Bin == "" {
		c.Contacts.Bin = "macos"
	}
	if c.Contacts.Container == "" {
		c.Contacts.Container = "iCloud"
	}
	if c.Contacts.Client == "" {
		c.Contacts.Client = "contact"
	}
	for name, s := range c.Spheres {
		s.Root = Expand(s.Root)
		if s.VCS == "" {
			s.VCS = "jj"
		}
		c.Spheres[name] = s
	}
}

func (c *Config) File() string { return c.path }

func (c *Config) Names() []string {
	var out []string
	for n := range c.Spheres {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// AddSphere declares a sphere and writes the file.
func (c *Config) AddSphere(name string, s Sphere) error {
	if !sphereName.MatchString(name) {
		return fmt.Errorf("sphere name %q: use lower-case letters, digits, - or _, e.g. perso", name)
	}
	c.Spheres[name] = s
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	raw := map[string]any{}
	if b, err := os.ReadFile(c.path); err == nil {
		if err := yaml.Unmarshal(b, &raw); err != nil {
			return fmt.Errorf("%s: %w", c.path, err)
		}
	}
	spheres, _ := raw["spheres"].(map[string]any)
	if spheres == nil {
		spheres = map[string]any{}
	}
	spheres[name] = s
	raw["spheres"] = spheres
	b, err := yaml.Marshal(raw)
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, b, 0o644)
}

// Expand replaces a leading ~ with the home directory.
func Expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}

// StateDir is ~/.local/state/contact, or CONTACT_STATE.
func StateDir() string {
	if env := os.Getenv("CONTACT_STATE"); env != "" {
		return Expand(env)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "contact")
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
