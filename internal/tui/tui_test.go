package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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
