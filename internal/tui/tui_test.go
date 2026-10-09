package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/aclemen1/tuikit"

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
	m.key("enter")
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

func TestBodyByE(t *testing.T) {
	m, s, _ := newTestModel(t)
	m.key("E")
	if !m.modal.Open() || m.editing != "AC" {
		t.Fatal("E opens the body editor")
	}
	m.modal = nil
	m.done(tuikit.DoneMsg{ID: "body", Values: values("body", "Préfère le matin.")})
	if c, _ := s.Get("AC"); c == nil || c.Body != "Préfère le matin." {
		t.Fatalf("body not saved: %+v (%s)", c, m.status)
	}
	m.key("e")
	if !m.modal.Open() {
		t.Fatal("e opens the edit form")
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
	m.done(tuikit.DoneMsg{ID: "note", Values: values("note", "Rappelé ce matin")})
	b, _ := os.ReadFile(log)
	if got := string(b); !strings.Contains(got, "--ref contact:JMR --sphere perso") || !strings.Contains(got, "Rappelé ce matin") {
		t.Fatalf("notes.add got %q (%s)", got, m.status)
	}
	if c, _ := s.Get("JMR"); c.Body != "" {
		t.Fatal("a note must not touch the body")
	}
	m.key("enter")
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
