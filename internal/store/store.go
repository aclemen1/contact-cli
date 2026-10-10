// Package store keeps the cards of a sphere: one Markdown file per card,
// named by its alias, front matter for the fields, body for free notes.
package store

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/aclemen1/contact-cli/internal/config"
	"github.com/aclemen1/contact-cli/internal/spec"
)

const (
	Person       = "person"
	Organisation = "organisation"
	Group        = "group"
)

var Kinds = []string{Person, Organisation, Group}

type LogEntry struct {
	At   string `yaml:"at" json:"at"`
	By   string `yaml:"by" json:"by"`
	What string `yaml:"what" json:"what"`
}

type Contact struct {
	Alias string `yaml:"alias" json:"alias"`
	// Aliases are the card's other aliases, unique across spheres like the main one.
	Aliases []string `yaml:"aliases,omitempty" json:"aliases,omitempty"`
	Kind    string   `yaml:"kind" json:"kind"`
	Name    string   `yaml:"name" json:"name"`
	// Given and Family split the name of a person for the address book; empty: the last word is the family name.
	Given  string `yaml:"given,omitempty" json:"given,omitempty"`
	Family string `yaml:"family,omitempty" json:"family,omitempty"`
	// Org is the organisation a person works for, as the address book has it.
	Org string `yaml:"org,omitempty" json:"org,omitempty"`
	// Members are the aliases of a group's members: people, organisations or groups.
	Members []string `yaml:"members,omitempty" json:"members,omitempty"`
	// Refs are what the card belongs to or concerns, as <tool>:<id>.
	Refs []string `yaml:"refs,omitempty" json:"refs,omitempty"`
	// Tags are the words other tools use for this contact.
	Tags []string `yaml:"tags,omitempty" json:"tags,omitempty"`
	// Remote is the address-book card or group the card is synced with.
	Remote string `yaml:"remote,omitempty" json:"remote,omitempty"`
	// Local keeps the card out of the address-book sync.
	Local bool `yaml:"local,omitempty" json:"local,omitempty"`
	// Phones and Emails are values, or label:value (mobile:+41 79 000 00 00).
	Phones  []string   `yaml:"phones,omitempty" json:"phones,omitempty"`
	Emails  []string   `yaml:"emails,omitempty" json:"emails,omitempty"`
	Created string     `yaml:"created" json:"created"`
	Updated string     `yaml:"updated,omitempty" json:"updated,omitempty"`
	Log     []LogEntry `yaml:"log,omitempty" json:"log,omitempty"`
	Body    string     `yaml:"-" json:"body,omitempty"`
	File    string     `yaml:"-" json:"file"`
	Sphere  string     `yaml:"-" json:"sphere"`
	// Groups are the groups whose members name this card, computed on reading.
	Groups []string `yaml:"-" json:"groups,omitempty"`
}

// AllAliases lists the main alias, then the others.
func (c *Contact) AllAliases() []string { return append([]string{c.Alias}, c.Aliases...) }

// Has says whether a is one of the card's aliases.
func (c *Contact) Has(a string) bool {
	for _, x := range c.AllAliases() {
		if x == a {
			return true
		}
	}
	return false
}

var aliasRe = regexp.MustCompile(`^[A-Z][A-Z0-9-]{0,23}$`)

// NormAlias upper-cases an alias, with or without its contact: prefix, and checks its form.
func NormAlias(a string) (string, error) {
	a = strings.TrimSpace(a)
	if len(a) > 8 && strings.EqualFold(a[:8], "contact:") {
		a = a[8:]
	}
	a = strings.ToUpper(a)
	if !aliasRe.MatchString(a) {
		return "", spec.UserError("alias %q: one capital letter, then capitals, digits or -, at most 24, e.g. JMR", a)
	}
	return a, nil
}

type Store struct {
	Sphere string
	Root   string
	VCS    string
	By     string
	Now    func() time.Time
	Warn   func(string)
}

// OpenStore opens a configured sphere.
func OpenStore(cfg *config.Config, sphere, by string) (*Store, error) {
	s, ok := cfg.Spheres[sphere]
	if !ok {
		return nil, spec.UserError("unknown sphere %q; configured: %s. Example: contact init --sphere perso --root ~/contact/perso", sphere, strings.Join(cfg.Names(), ", "))
	}
	if _, err := os.Stat(s.Root); err != nil {
		return nil, spec.UserError("the cards of %s are missing at %s. Run: contact init --sphere %s --root %s", sphere, s.Root, sphere, s.Root)
	}
	if by == "" {
		by = "user"
	}
	return &Store{Sphere: sphere, Root: s.Root, VCS: s.VCS, By: by, Now: time.Now,
		Warn: func(m string) { fmt.Fprintln(os.Stderr, "contact: warning: "+m) }}, nil
}

// Init creates the store directory and, with a VCS, its repository.
func Init(root, vcs string) error {
	if err := os.MkdirAll(filepath.Join(root, ".contact"), 0o755); err != nil {
		return err
	}
	ignore := filepath.Join(root, ".gitignore")
	if _, err := os.Stat(ignore); os.IsNotExist(err) {
		if err := os.WriteFile(ignore, []byte(".contact/\n"), 0o644); err != nil {
			return err
		}
	}
	switch vcs {
	case "jj":
		if _, err := os.Stat(filepath.Join(root, ".jj")); os.IsNotExist(err) {
			if out, err := runIn(root, "jj", "git", "init"); err != nil {
				return fmt.Errorf("jj git init: %v: %s", err, out)
			}
		}
	case "git":
		if _, err := os.Stat(filepath.Join(root, ".git")); os.IsNotExist(err) {
			if out, err := runIn(root, "git", "init", "-q"); err != nil {
				return fmt.Errorf("git init: %v: %s", err, out)
			}
		}
	}
	return nil
}

func runIn(dir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// WriteAs runs fn under the store's lock, then commits with the message it
// returns; an empty one commits nothing.
func (s *Store) WriteAs(fn func() (string, error)) error {
	if err := os.MkdirAll(filepath.Join(s.Root, ".contact"), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.Root, ".contact", "lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return spec.Locked("cannot lock the cards of %s: %v", s.Sphere, err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	msg, err := fn()
	if err != nil {
		return err
	}
	if msg != "" {
		s.commit(msg)
	}
	return nil
}

func (s *Store) commit(msg string) {
	var out string
	var err error
	switch s.VCS {
	case "jj":
		out, err = runIn(s.Root, "jj", "commit", "-m", msg)
	case "git":
		if out, err = runIn(s.Root, "git", "add", "-A"); err == nil {
			out, err = runIn(s.Root, "git", "commit", "-q", "--allow-empty", "-m", msg)
		}
	default:
		return
	}
	if err != nil && s.Warn != nil {
		s.Warn(fmt.Sprintf("%s commit failed in %s: %v: %s", s.VCS, s.Root, err, out))
	}
}

// Note appends a line to the card's history and stamps it updated.
func (s *Store) Note(c *Contact, what string) {
	now := s.Now().Format(time.RFC3339)
	c.Log = append(c.Log, LogEntry{At: now, By: s.By, What: what})
	c.Updated = now
}

func (s *Store) path(alias string) string { return filepath.Join(s.Root, alias+".md") }

// List reads every card, by alias.
func (s *Store) List() ([]*Contact, error) {
	files, err := filepath.Glob(filepath.Join(s.Root, "*.md"))
	if err != nil {
		return nil, err
	}
	var out []*Contact
	for _, f := range files {
		if !aliasRe.MatchString(strings.TrimSuffix(filepath.Base(f), ".md")) {
			continue
		}
		c, err := read(f)
		if err != nil {
			if s.Warn != nil {
				s.Warn(err.Error())
			}
			continue
		}
		c.Sphere = s.Sphere
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out, nil
}

// Get reads one card; nil without error when the sphere has none of that alias.
func (s *Store) Get(alias string) (*Contact, error) {
	c, err := read(s.path(alias))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.Sphere = s.Sphere
	return c, nil
}

// Save writes the card's file atomically; the file name follows the alias.
func (s *Store) Save(c *Contact) error {
	c.Aliases = sorted(c.Aliases)
	c.Members = sorted(c.Members)
	c.Refs = sorted(c.Refs)
	c.Tags = sorted(c.Tags)
	fm, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(fm)
	b.WriteString("---\n")
	if body := strings.TrimSpace(c.Body); body != "" {
		b.WriteString("\n" + body + "\n")
	}
	c.File = s.path(c.Alias)
	c.Sphere = s.Sphere
	tmp := c.File + ".tmp"
	if err := os.WriteFile(tmp, b.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.File)
}

// Remove deletes the card's file.
func (s *Store) Remove(alias string) error { return os.Remove(s.path(alias)) }

func read(path string) (*Contact, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	str := string(b)
	if !strings.HasPrefix(str, "---\n") {
		return nil, fmt.Errorf("%s: no front matter", path)
	}
	fm, body, ok := strings.Cut(str[4:], "\n---")
	if !ok {
		return nil, fmt.Errorf("%s: front matter not closed", path)
	}
	c := &Contact{}
	if err := yaml.Unmarshal([]byte(fm), c); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	c.Body = strings.TrimSpace(strings.TrimPrefix(body, "\n"))
	c.File = path
	want := strings.TrimSuffix(filepath.Base(path), ".md")
	if c.Alias == "" {
		c.Alias = want
	}
	if c.Alias != want {
		return nil, fmt.Errorf("%s: alias %s does not match the file name", path, c.Alias)
	}
	if c.Kind == "" {
		c.Kind = Person
	}
	var aliases []string
	for _, a := range c.Aliases {
		n, err := NormAlias(a)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", path, err)
		}
		if n != c.Alias {
			aliases = append(aliases, n)
		}
	}
	c.Aliases = sorted(aliases)
	return c, nil
}

func sorted(l []string) []string {
	if len(l) == 0 {
		return nil
	}
	out := append([]string{}, l...)
	sort.Strings(out)
	var dedup []string
	for i, x := range out {
		if i == 0 || x != out[i-1] {
			dedup = append(dedup, x)
		}
	}
	return dedup
}
