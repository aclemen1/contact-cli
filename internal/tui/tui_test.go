package tui

import (
	"fmt"
	"github.com/aclemen1/tuikit/complete"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/aclemen1/tuikit"
	"github.com/charmbracelet/x/ansi"

	"github.com/aclemen1/contact-cli/internal/config"
	"github.com/aclemen1/contact-cli/internal/store"
)

func TestBrowse(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg, _ := config.Load(cfgPath)
	root := filepath.Join(dir, "perso")
	if err := store.Init(root, "none"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddSphere("perso", config.Sphere{Root: root, VCS: "none"}); err != nil {
		t.Fatal(err)
	}
	s := &store.Store{Sphere: "perso", Root: root}
	s.Save(&store.Contact{Alias: "AC", Kind: store.Person, Name: "Alain Clément"})
	s.Save(&store.Contact{Alias: "FAM", Kind: store.Group, Name: "Famille", Members: []string{"AC"}})
	m := New(cfgPath, []string{"perso"})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m.reload()
	if len(m.items) != 2 {
		t.Fatalf("items = %d (%s)", len(m.items), m.status)
	}
	m.key("j")
	drive(m, m.key("enter"))
	if m.shown == nil || m.shown.Contact.Alias != "FAM" {
		t.Fatalf("detail of FAM not loaded: %+v", m.shown)
	}
	if v := m.render(); !strings.Contains(v, "FAM · Famille") || !strings.Contains(v, "membres") {
		t.Fatalf("render:\n%s", v)
	}
	m.key("4")
	if len(m.items) != 1 || m.items[0].Alias != "FAM" {
		t.Fatalf("kind filter: %d", len(m.items))
	}
	m.key("c")
	if !m.modal.Open() {
		t.Fatal("c opens the add modal")
	}
}

func newTestModel(t *testing.T) (*Model, *store.Store, string) {
	t.Helper()
	watchEvery = 0
	dir := t.TempDir()
	t.Setenv("CONTACT_STATE", filepath.Join(dir, "state"))
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg, _ := config.Load(cfgPath)
	root := filepath.Join(dir, "perso")
	if err := store.Init(root, "none"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.AddSphere("perso", config.Sphere{Root: root, VCS: "none"}); err != nil {
		t.Fatal(err)
	}
	s := &store.Store{Sphere: "perso", Root: root}
	s.Save(&store.Contact{Alias: "AC", Kind: store.Person, Name: "Alain Clément"})
	s.Save(&store.Contact{Alias: "JMR", Kind: store.Person, Name: "Jean-Moïse Rochat", Aliases: []string{"JM"}})
	m := New(cfgPath, []string{"perso"})
	m.cfg, _ = config.Load(cfgPath)
	m.statePath = statePath()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.reload()
	return m, s, dir
}

func values(id, v string) tuikit.Values {
	var vs tuikit.Values
	vs.Set(id, v)
	return vs
}

func TestEditByE(t *testing.T) {
	m, s, _ := newTestModel(t)
	m.key("e")
	if m.modal.Open() {
		t.Fatal("e is free: no form")
	}
	m.key("E")
	if !m.modal.Open() || m.editing != "AC" {
		t.Fatal("E opens the edit form")
	}
	m.modal = nil
	c, _ := s.Get("AC")
	var vs tuikit.Values
	vs.Set("name", c.Name)
	vs.Set("body", "Préfère le matin.")
	drive(m, m.done(tuikit.DoneMsg{ID: "edit", Values: vs}))
	if c, _ := s.Get("AC"); c == nil || c.Body != "Préfère le matin." || c.Name == "" {
		t.Fatalf("body not saved from the form: %+v (%s)", c, m.status)
	}
}

func TestNoteByN(t *testing.T) {
	m, s, dir := newTestModel(t)
	log := filepath.Join(dir, "notes.log")
	add := filepath.Join(dir, "note-add")
	ls := filepath.Join(dir, "note-ls")
	os.WriteFile(add, []byte("#!/bin/sh\n{ echo \"$@\"; cat; } >> "+log+"\necho '{\"ok\":true,\"result\":{\"id\":\"PN-0001\"}}'\n"), 0o755)
	os.WriteFile(ls, []byte("#!/bin/sh\ncase \"$2\" in contact:JMR) echo '{\"ok\":true,\"result\":[{\"id\":\"PN-0001\",\"created\":\"2026-10-10T09:00:00+02:00\",\"body\":\"Rappelé ce matin\"}]}';; contact:JM) echo '{\"ok\":true,\"result\":[{\"id\":\"PN-0001\",\"created\":\"2026-10-10T09:00:00+02:00\",\"body\":\"Rappelé ce matin\"}]}';; *) echo '{\"ok\":true,\"result\":[]}';; esac\n"), 0o755)
	f, _ := os.OpenFile(m.cfgPath, os.O_APPEND|os.O_WRONLY, 0o644)
	fmt.Fprintf(f, "notes:\n    add: [%q, \"--ref\", \"{ref}\", \"--sphere\", \"{sphere}\"]\n    ls: [%q, \"--ref\", \"{ref}\"]\n", add, ls)
	f.Close()
	m.cfg, _ = config.Load(m.cfgPath)
	m.key("j")
	m.key("N")
	if !m.modal.Open() || m.editing != "JMR" {
		t.Fatal("N opens the note editor")
	}
	m.modal = nil
	drive(m, m.done(tuikit.DoneMsg{ID: "note", Values: values("note", "Rappelé ce matin")}))
	b, _ := os.ReadFile(log)
	if got := string(b); !strings.Contains(got, "--ref contact:JMR --sphere perso") || !strings.Contains(got, "Rappelé ce matin") {
		t.Fatalf("notes.add got %q (%s)", got, m.status)
	}
	if c, _ := s.Get("JMR"); c.Body != "" {
		t.Fatal("a note must not touch the body")
	}
	drive(m, m.key("enter"))
	if m.shown == nil || len(m.shown.Notes) != 1 {
		t.Fatalf("notes of JMR, once across aliases: %+v", m.shown)
	}
	if v := m.render(); !strings.Contains(v, "Rappelé ce matin") {
		t.Fatalf("render:\n%s", v)
	}
}

func TestReloadOnRebuild(t *testing.T) {
	m, _, dir := newTestModel(t)
	bin := filepath.Join(dir, "contact")
	os.WriteFile(bin, []byte("v1"), 0o755)
	m.exe, m.exeStamp = bin, binStamp(bin)
	quits := func(cmd tea.Cmd) bool {
		if cmd == nil {
			return false
		}
		_, ok := cmd().(tea.QuitMsg)
		return ok
	}
	if _, cmd := m.Update(watchMsg{bin: binStamp(bin)}); quits(cmd) || m.newBin {
		t.Fatal("same binary: no reload")
	}
	time.Sleep(10 * time.Millisecond)
	os.Remove(bin)
	os.WriteFile(bin, []byte("v2"), 0o755)
	m.key("c")
	if _, cmd := m.Update(watchMsg{bin: binStamp(bin)}); quits(cmd) || !m.newBin || m.reexec {
		t.Fatal("a rebuild waits while a modal is open")
	}
	m.modal = nil
	m.key("j")
	if _, cmd := m.Update(watchMsg{bin: binStamp(bin)}); !quits(cmd) || !m.reexec {
		t.Fatal("idle with a new binary: quit to re-exec")
	}
	m.persist()
	m2 := New(m.cfgPath, []string{"perso"})
	m2.statePath, m2.exe = m.statePath, bin
	m2.restore()
	if c := m2.current(); c == nil || c.Alias != "JMR" {
		t.Fatalf("selection not restored: %+v", c)
	}
	if _, cmd := m2.Update(reloadMsg{}); !quits(cmd) {
		t.Fatal("SIGUSR1 reloads when idle")
	}
}

func TestPick(t *testing.T) {
	m, _, _ := newTestModel(t)
	if !m.pick("contact:jmr") || m.items[m.sel].Alias != "JMR" {
		t.Fatalf("contact:jmr not selected: %d", m.sel)
	}
	if m.pick("NOPE") {
		t.Fatal("unknown alias must not be picked")
	}
}

func TestRefsCompletedAndSaved(t *testing.T) {
	m, s, dir := newTestModel(t)
	refs := filepath.Join(dir, "refs.yaml")
	os.WriteFile(refs, []byte("sources:\n  office:\n    prefix: \"office:\"\n    static:\n      - {value: P-0053, label: contact}\n"), 0o644)
	t.Setenv("TUIKIT_REFS", refs)
	m.cfg, _ = config.Load(m.cfgPath)
	m.cfg.Complete = map[string]complete.Uses{"refs": {{Source: "office"}}}
	if got := m.refComplete("P-005"); len(got) != 1 {
		t.Fatalf("shared source not used: %v", got)
	}
	m.key("E")
	if !m.modal.Open() {
		t.Fatal("E opens the edit form")
	}
	m.modal = nil
	c, _ := s.Get("AC")
	var vs tuikit.Values
	vs.Set("name", c.Name)
	drive(m, m.saveEdit(vs, []string{"office:P-0053"}))
	if m.busy.Unread() > 0 {
		t.Fatal(plainView(m))
	}
	if c, _ := s.Get("AC"); len(c.Refs) != 1 || c.Refs[0] != "office:P-0053" {
		t.Fatalf("refs not saved: %+v (%s)", c.Refs, m.status)
	}
}

func TestRefsWithoutSources(t *testing.T) {
	m, _, dir := newTestModel(t)
	t.Setenv("TUIKIT_REFS", filepath.Join(dir, "absent.yaml"))
	m.cfg, _ = config.Load(m.cfgPath)
	m.cfg.Complete = map[string]complete.Uses{"refs": {{Source: "office"}}}
	if got := m.refComplete("office:"); len(got) != 0 {
		t.Fatalf("no file: nothing proposed, got %v", got)
	}
	if !strings.Contains(m.status, "office") {
		t.Fatalf("missing source not reported: %q", m.status)
	}
}

// drive runs cmd and feeds its messages back to m, batches included; the Busy
// ticks and what does not answer within a second are dropped.
func drive(m *Model, cmd tea.Cmd) {
	for _, msg := range collect(cmd) {
		_, next := m.Update(msg)
		drive(m, next)
	}
}

func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(time.Second):
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, collect(c)...)
		}
		return out
	}
	if msg == nil || fmt.Sprintf("%T", msg) == "tuikit.busyTickMsg" {
		return nil
	}
	return []tea.Msg{msg}
}

func plainView(m *Model) string { return ansi.Strip(tuikit.Overlay(m.render(), m.modal, m.w, m.h)) }

func TestBusy(t *testing.T) {
	m, _, dir := newTestModel(t)
	add := filepath.Join(dir, "note-add")
	os.WriteFile(add, []byte("#!/bin/sh\ncat >/dev/null\nsleep 0.2\necho 'notes indisponible' >&2\nexit 3\n"), 0o755)
	f, _ := os.OpenFile(m.cfgPath, os.O_APPEND|os.O_WRONLY, 0o644)
	fmt.Fprintf(f, "notes:\n    add: [%q, \"--ref\", \"{ref}\"]\n", add)
	f.Close()
	m.cfg, _ = config.Load(m.cfgPath)
	m.key("N")
	m.modal = nil
	cmd := m.done(tuikit.DoneMsg{ID: "note", Values: values("note", "Rappelé")})
	if v := plainView(m); !strings.Contains(v, "note sur AC") {
		t.Fatalf("a job under way shows in the header:\n%s", v)
	}
	drive(m, cmd)
	m.key("j")
	if v := plainView(m); !strings.Contains(v, "✗ note sur AC") || !strings.Contains(v, "! voir l'échec") {
		t.Fatalf("a failure stays in sight, with ! in the footer:\n%s", v)
	}
	m.key("!")
	if !m.modal.Open() || !strings.Contains(plainView(m), "Travaux") || !strings.Contains(plainView(m), "notes indisponible") {
		t.Fatalf("! opens the list of jobs:\n%s", plainView(m))
	}
	if m.busy.Unread() != 0 {
		t.Fatal("opening the list reads the failure")
	}
	m.modal = nil
	drive(m, m.key("r"))
	if v := plainView(m); !strings.Contains(v, "✓ 2 fiche(s) relue(s)") {
		t.Fatalf("a reload ends with its text:\n%s", v)
	}
	m.key("c")
	m.Update(tea.KeyPressMsg{Code: '!', Text: "!"})
	if !m.modal.Open() || strings.Contains(plainView(m), "Travaux") {
		t.Fatal("! in a form is typed, not a key of the TUI")
	}
}
