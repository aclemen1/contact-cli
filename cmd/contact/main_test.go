package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aclemen1/contact-cli/internal/actions"
	"github.com/aclemen1/contact-cli/internal/config"
	"github.com/aclemen1/contact-cli/internal/csync"
)

type env struct {
	t   *testing.T
	dir string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CONTACT_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("CONTACT_STATE", filepath.Join(dir, "state"))
	t.Setenv("CONTACT_SPHERE", "")
	e := &env{t, dir}
	e.ok("init", "--sphere", "perso", "--root", filepath.Join(dir, "perso"), "--vcs", "none")
	e.ok("init", "--sphere", "pro", "--root", filepath.Join(dir, "pro"), "--vcs", "none")
	return e
}

func (e *env) call(args ...string) (int, map[string]any, string) {
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(""), &out, &errb)
	var m map[string]any
	_ = json.Unmarshal(out.Bytes(), &m)
	return code, m, out.String() + errb.String()
}

func (e *env) ok(args ...string) map[string]any {
	e.t.Helper()
	code, m, raw := e.call(args...)
	if code != 0 {
		e.t.Fatalf("contact %s: exit %d: %s", strings.Join(args, " "), code, raw)
	}
	return m
}

func (e *env) fails(code int, args ...string) string {
	e.t.Helper()
	got, _, raw := e.call(args...)
	if got != code {
		e.t.Fatalf("contact %s: exit %d, want %d: %s", strings.Join(args, " "), got, code, raw)
	}
	return raw
}

func result(m map[string]any) map[string]any { r, _ := m["result"].(map[string]any); return r }

func aliases(m map[string]any) string {
	var out []string
	for _, x := range m["result"].([]any) {
		out = append(out, x.(map[string]any)["alias"].(string))
	}
	return strings.Join(out, " ")
}

func TestAddAliasesAndSpheres(t *testing.T) {
	e := newEnv(t)
	r := result(e.ok("add", "Jean-Moïse Rochat", "--sphere", "pro", "--email", "jmr@unil.ch"))
	if r["alias"] != "JMR" || r["sphere"] != "pro" {
		t.Fatalf("add: %v", r)
	}
	raw := e.fails(4, "add", "Jean-Marc Rey", "--sphere", "perso", "--alias", "jmr")
	if !strings.Contains(raw, "JMR2") {
		t.Fatalf("a taken alias must propose a free one: %s", raw)
	}
	if a := result(e.ok("add", "Jean-Marc Rey", "--sphere", "perso"))["alias"]; a != "JMR2" {
		t.Fatalf("suggested alias = %v", a)
	}
	e.fails(2, "add", "Sans sphère")
	if got := aliases(e.ok("ls")); got != "JMR JMR2" {
		t.Fatalf("ls = %s", got)
	}
	if got := aliases(e.ok("ls", "--sphere", "pro")); got != "JMR" {
		t.Fatalf("ls pro = %s", got)
	}
	// Moving keeps the alias.
	e.ok("mv", "JMR2", "--to", "pro")
	if got := aliases(e.ok("ls", "--sphere", "pro")); got != "JMR JMR2" {
		t.Fatalf("after mv = %s", got)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "perso", "JMR2.md")); !os.IsNotExist(err) {
		t.Fatal("mv left the old file")
	}
}

func TestGroupsAndMembers(t *testing.T) {
	e := newEnv(t)
	e.ok("add", "Alain Clément", "--sphere", "perso", "--alias", "AC")
	e.ok("add", "Eve Clément", "--sphere", "perso", "--alias", "EVE", "--tag", "eve")
	e.ok("add", "Jean-Moïse Rochat", "--sphere", "pro", "--alias", "JMR")
	e.ok("add", "Enfants", "--kind", "group", "--sphere", "perso", "--alias", "KIDS", "--member", "EVE")
	e.ok("add", "Famille", "--kind", "group", "--sphere", "perso", "--alias", "FAM", "--member", "AC", "--member", "KIDS")
	e.fails(3, "add", "X", "--kind", "group", "--sphere", "perso", "--member", "NOPE")
	e.fails(4, "edit", "KIDS", "--add-member", "FAM")
	e.ok("add", "Jeannette Clément", "--sphere", "perso", "--alias", "JC", "--group", "FAM")
	if got := aliases(e.ok("members", "FAM")); got != "AC JC KIDS" {
		t.Fatalf("members = %s", got)
	}
	if got := aliases(e.ok("members", "contact:FAM", "--recursive")); got != "AC EVE JC" {
		t.Fatalf("recursive = %s", got)
	}
	if got := aliases(e.ok("ls", "--group", "FAM")); got != "AC EVE JC" {
		t.Fatalf("ls --group = %s", got)
	}
	// Groups may hold cards of another sphere.
	e.ok("edit", "JMR", "--add-group", "FAM")
	shown := result(e.ok("show", "JMR", "--no-views"))
	if g := shown["contact"].(map[string]any)["groups"].([]any); len(g) != 1 || g[0] != "FAM" {
		t.Fatalf("groups of JMR = %v", g)
	}
	e.fails(4, "rm", "EVE")
	e.ok("rm", "EVE", "--force")
	if got := aliases(e.ok("members", "KIDS")); got != "" {
		t.Fatalf("EVE still in KIDS: %s", got)
	}
	if got := result(e.ok("resolve", "Jeannette")); got != nil {
		t.Fatalf("resolve returns a list")
	}
	_, m, _ := e.call("resolve", "rochat")
	if l := m["result"].([]any); len(l) != 1 || l[0].(map[string]any)["alias"] != "JMR" {
		t.Fatalf("resolve rochat = %v", l)
	}
}

func TestEditAndTSV(t *testing.T) {
	e := newEnv(t)
	e.ok("add", "Jeannette Clément", "--sphere", "perso", "--alias", "JC", "--phone", "+41 79 1", "--ref", "office:P-0006")
	r := result(e.ok("edit", "JC", "--add-tag", "jeannette", "--add-phone", "mobile:+41 79 2", "--rm-phone", "+41 79 1", "--append", "Lupus."))
	if p := r["phones"].([]any); len(p) != 1 || p[0] != "mobile:+41 79 2" || r["body"] != "Lupus." {
		t.Fatalf("edit: %v", r)
	}
	if got := aliases(e.ok("ls", "--ref", "office:P-0006")); got != "JC" {
		t.Fatalf("ls --ref = %s", got)
	}
	var out bytes.Buffer
	run([]string{"ls", "--format", "tsv"}, strings.NewReader(""), &out, &out)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[1], "JC\tperson\tperso\tJeannette Clément") {
		t.Fatalf("tsv = %q", out.String())
	}
	b, _ := os.ReadFile(filepath.Join(e.dir, "perso", "JC.md"))
	if !strings.Contains(string(b), "alias: JC") || !strings.HasSuffix(string(b), "Lupus.\n") {
		t.Fatalf("card file:\n%s", b)
	}
}

func TestShowViews(t *testing.T) {
	e := newEnv(t)
	cfg := filepath.Join(e.dir, "config.yaml")
	b, _ := os.ReadFile(cfg)
	b = append(b, []byte(`views:
  - label: Tâches
    run: [sh, -c, 'printf "{\"ok\":true,\"result\":[\"%s\"]}" "$0"', "{ref}"]
  - label: Texte
    run: [echo, "cite {alias}"]
  - label: Cassée
    run: [false]
`)...)
	os.WriteFile(cfg, b, 0o644)
	e.ok("add", "Jean-Moïse Rochat", "--sphere", "pro", "--alias", "JMR")
	m := e.ok("show", "JMR")
	views := result(m)["views"].([]any)
	if len(views) != 3 {
		t.Fatalf("views = %v", views)
	}
	if r := views[0].(map[string]any)["result"].([]any); r[0] != "contact:JMR" {
		t.Fatalf("json view = %v", views[0])
	}
	if views[1].(map[string]any)["text"] != "cite JMR" || views[2].(map[string]any)["error"] == nil {
		t.Fatalf("views = %v", views)
	}
	if w, _ := m["warnings"].([]any); len(w) != 1 {
		t.Fatalf("a failing view is a warning: %v", m["warnings"])
	}
}

// fakeBack serves one address-book card for add --from.
type fakeBack struct{ csync.Backend }

func (fakeBack) Container(string) (string, error) { return "ICLOUD", nil }
func (fakeBack) GetRaw(id string) (*csync.RawCard, error) {
	switch id {
	case "A:ABPerson":
		return &csync.RawCard{ID: id, ContactType: "person", GivenName: "Alain", FamilyName: "Clément", Organization: "UNIL", ContainerID: "ICLOUD"}, nil
	case "G:ABPerson":
		return &csync.RawCard{ID: id, ContactType: "person", GivenName: "Alain", FamilyName: "Clément", ContainerID: "GOOGLE"}, nil
	}
	return nil, nil
}

func TestAddFrom(t *testing.T) {
	e := newEnv(t)
	old := actions.NewBackend
	actions.NewBackend = func(_ *config.Config) actions.Backend { return fakeBack{} }
	defer func() { actions.NewBackend = old }()
	r := result(e.ok("add", "--from", "A:ABPerson", "--sphere", "perso", "--alias", "AC"))
	if r["name"] != "Alain Clément" || r["org"] != "UNIL" || r["remote"] != "A:ABPerson" {
		t.Fatalf("add --from: %v", r)
	}
	e.fails(4, "add", "--from", "A:ABPerson", "--sphere", "perso")
	e.fails(2, "add", "--from", "G:ABPerson", "--sphere", "perso")
	e.fails(3, "add", "--from", "Z:ABPerson", "--sphere", "perso")
}

func TestHooks(t *testing.T) {
	e := newEnv(t)
	cfg := filepath.Join(e.dir, "config.yaml")
	log := filepath.Join(e.dir, "hooks.log")
	b, _ := os.ReadFile(cfg)
	b = append(b, []byte("hooks:\n  - events: [added, moved]\n    run: [sh, -c, 'echo \"$CONTACT_EVENT $CONTACT_REF $CONTACT_SPHERE\" >> "+log+"']\n")...)
	os.WriteFile(cfg, b, 0o644)
	e.ok("add", "Jean-Moïse Rochat", "--sphere", "perso", "--alias", "JMR")
	e.ok("edit", "JMR", "--org", "UNIL")
	e.ok("mv", "JMR", "--to", "pro")
	got, _ := os.ReadFile(log)
	if want := "added contact:JMR perso\nmoved contact:JMR pro\n"; string(got) != want {
		t.Fatalf("hooks ran:\n%s\nwant:\n%s", got, want)
	}
}
