package csync

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aclemen1/contact-cli/internal/store"
)

type fake struct {
	cards   map[string]*Card
	groups  map[string]*Group
	touched map[string]bool
	full    bool
	seen    []string
	n       int
	writes  []string
}

func newFake() *fake {
	return &fake{cards: map[string]*Card{}, groups: map[string]*Group{}, touched: map[string]bool{}}
}

func (f *fake) id(kind string) string { f.n++; return fmt.Sprintf("ID%d:%s", f.n, kind) }

func (f *fake) Changes(string) (map[string]bool, bool, string, error) {
	t := f.touched
	f.touched = map[string]bool{}
	full := f.full
	f.full = false
	return t, full, "tok", nil
}
func (f *fake) MarkSeen(_, token string) error   { f.seen = append(f.seen, token); return nil }
func (f *fake) Container(string) (string, error) { return "ICLOUD", nil }
func (f *fake) Get(id string) (*Card, error) {
	c := f.cards[id]
	if c == nil {
		return nil, nil
	}
	cp := *c
	return &cp, nil
}
func (f *fake) Create(container string, c Card) (string, error) {
	c.ID, c.Container = f.id("ABPerson"), container
	f.cards[c.ID] = &c
	f.writes = append(f.writes, "create "+c.ID)
	return c.ID, nil
}
func (f *fake) Update(id string, c Card, which []string) error {
	cur := f.cards[id]
	for _, w := range which {
		switch w {
		case "name":
			cur.Given, cur.Family = c.Given, c.Family
			if c.Kind == "organization" {
				cur.Org = c.Org
			}
		case "org":
			cur.Org = c.Org
		case "phones":
			cur.Phones = c.Phones
		case "emails":
			cur.Emails = c.Emails
		}
	}
	f.writes = append(f.writes, "update "+id+" "+strings.Join(which, ","))
	return nil
}
func (f *fake) Groups() (map[string]*Group, error) {
	out := map[string]*Group{}
	for id, g := range f.groups {
		cp := *g
		cp.Members = append([]string{}, g.Members...)
		cp.Subgroups = append([]string{}, g.Subgroups...)
		out[id] = &cp
	}
	return out, nil
}
func (f *fake) GroupCreate(_, name string) (string, error) {
	id := f.id("ABGroup")
	f.groups[id] = &Group{ID: id, Name: name}
	f.writes = append(f.writes, "group create "+name)
	return id, nil
}
func (f *fake) GroupRename(id, name string) error { f.groups[id].Name = name; return nil }
func (f *fake) GroupAdd(g, c string) error {
	f.groups[g].Members = append(f.groups[g].Members, c)
	f.writes = append(f.writes, "group add "+c)
	return nil
}
func (f *fake) GroupRemove(g, c string) error {
	f.groups[g].Members = drop(f.groups[g].Members, c)
	f.writes = append(f.writes, "group remove "+c)
	return nil
}
func (f *fake) GroupAddSub(p, s string) error {
	f.groups[p].Subgroups = append(f.groups[p].Subgroups, s)
	f.writes = append(f.writes, "group add-subgroup "+s)
	return nil
}
func (f *fake) GroupRemoveSub(p, s string) error {
	f.groups[p].Subgroups = drop(f.groups[p].Subgroups, s)
	return nil
}

func drop(l []string, x string) []string {
	var out []string
	for _, y := range l {
		if y != x {
			out = append(out, y)
		}
	}
	return out
}

func setup(t *testing.T) (*Sync, *fake, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	s := &store.Store{Sphere: "perso", Root: filepath.Join(dir, "perso"), VCS: "none", By: "test",
		Now: func() time.Time { return time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC) }}
	if err := store.Init(s.Root, "none"); err != nil {
		t.Fatal(err)
	}
	f := newFake()
	y := &Sync{Stores: map[string]*store.Store{"perso": s}, Back: f, State: filepath.Join(dir, "state.json"), Container: "iCloud", Client: "contact"}
	return y, f, s
}

func save(t *testing.T, s *store.Store, c *store.Contact) {
	t.Helper()
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, s *store.Store, a string) *store.Contact {
	t.Helper()
	c, err := s.Get(a)
	if err != nil || c == nil {
		t.Fatalf("get %s: %v", a, err)
	}
	return c
}

func run(t *testing.T, y *Sync) *Report {
	t.Helper()
	rep, err := y.Run()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Errors) > 0 {
		t.Fatalf("errors: %v", rep.Errors)
	}
	return rep
}

func TestCreateThenTwoWays(t *testing.T) {
	y, f, s := setup(t)
	save(t, s, &store.Contact{Alias: "JMR", Kind: store.Person, Name: "Jean-Moïse Rochat", Emails: []string{"jmr@unil.ch"}})
	save(t, s, &store.Contact{Alias: "RDR", Kind: store.Organisation, Name: "Régie du Rivage"})
	run(t, y)
	jmr := get(t, s, "JMR")
	if jmr.Remote == "" || f.cards[jmr.Remote].Given != "Jean-Moïse" || f.cards[jmr.Remote].Family != "Rochat" || f.cards[jmr.Remote].Container != "ICLOUD" {
		t.Fatalf("card not created right: %+v %+v", jmr, f.cards[jmr.Remote])
	}
	rdr := get(t, s, "RDR")
	if c := f.cards[rdr.Remote]; c.Kind != "organization" || c.Org != "Régie du Rivage" {
		t.Fatalf("organisation: %+v", c)
	}
	if len(f.seen) != 1 {
		t.Fatalf("changes not marked seen: %v", f.seen)
	}

	// A phone added in the address book comes to the card.
	f.cards[jmr.Remote].Phones = []string{"mobile:+41 79 000 00 00"}
	f.touched[jmr.Remote] = true
	run(t, y)
	if got := get(t, s, "JMR").Phones; len(got) != 1 || got[0] != "mobile:+41 79 000 00 00" {
		t.Fatalf("phone not pulled: %v", got)
	}

	// An e-mail changed in the card goes to the address book, without any change reported there.
	jmr = get(t, s, "JMR")
	jmr.Emails = []string{"work:jmr@unil.ch"}
	save(t, s, jmr)
	f.writes = nil
	run(t, y)
	if got := f.cards[jmr.Remote].Emails; len(got) != 1 || got[0] != "work:jmr@unil.ch" {
		t.Fatalf("e-mail not pushed: %v", got)
	}
	// Nothing changed anywhere: nothing read, nothing written.
	f.writes = nil
	rep := run(t, y)
	if rep.Read != 0 || len(f.writes) != 0 || len(rep.Changes) != 0 {
		t.Fatalf("idle run did work: %+v %v", rep, f.writes)
	}
}

func TestConflictKeepsCard(t *testing.T) {
	y, f, s := setup(t)
	save(t, s, &store.Contact{Alias: "JMR", Kind: store.Person, Name: "Jean-Moïse Rochat", Org: "UNIL"})
	run(t, y)
	jmr := get(t, s, "JMR")
	f.cards[jmr.Remote].Org = "EPFL"
	f.touched[jmr.Remote] = true
	jmr.Org = "UNISIS"
	save(t, s, jmr)
	run(t, y)
	if got := f.cards[jmr.Remote].Org; got != "UNISIS" {
		t.Fatalf("address book org = %s, want the card's", got)
	}
	jmr = get(t, s, "JMR")
	last := jmr.Log[len(jmr.Log)-1].What
	if jmr.Org != "UNISIS" || !strings.Contains(last, "EPFL") {
		t.Fatalf("lost value not logged: %+v", jmr.Log)
	}
}

func TestDeletedCardUnlinks(t *testing.T) {
	y, f, s := setup(t)
	save(t, s, &store.Contact{Alias: "JMR", Kind: store.Person, Name: "Jean-Moïse Rochat"})
	run(t, y)
	id := get(t, s, "JMR").Remote
	delete(f.cards, id)
	f.touched[id] = true
	run(t, y)
	if c := get(t, s, "JMR"); c.Remote != "" || c.Name != "Jean-Moïse Rochat" {
		t.Fatalf("card must stay, unlinked: %+v", c)
	}
}

func TestLinkedFromRemote(t *testing.T) {
	y, f, s := setup(t)
	f.cards["X:ABPerson"] = &Card{ID: "X:ABPerson", Kind: "person", Given: "Eve", Family: "Clément", Phones: []string{"+41 79 1"}}
	save(t, s, &store.Contact{Alias: "EVE", Kind: store.Person, Name: "Eve Clément", Phones: []string{"+41 79 1"}, Remote: "X:ABPerson"})
	f.cards["X:ABPerson"].Phones = []string{"+41 79 2"}
	run(t, y)
	if got := get(t, s, "EVE").Phones; got[0] != "+41 79 2" {
		t.Fatalf("a card taken from the address book follows it: %v", got)
	}
	for _, w := range f.writes {
		if strings.HasPrefix(w, "create") {
			t.Fatalf("no card may be created for a linked one: %v", f.writes)
		}
	}
}

func TestGroups(t *testing.T) {
	y, f, s := setup(t)
	save(t, s, &store.Contact{Alias: "AC", Kind: store.Person, Name: "Alain Clément"})
	save(t, s, &store.Contact{Alias: "EVE", Kind: store.Person, Name: "Eve Clément"})
	save(t, s, &store.Contact{Alias: "JC", Kind: store.Person, Name: "Jeannette Clément"})
	save(t, s, &store.Contact{Alias: "KIDS", Kind: store.Group, Name: "Enfants", Members: []string{"EVE"}})
	save(t, s, &store.Contact{Alias: "FAM", Kind: store.Group, Name: "Famille", Members: []string{"AC", "KIDS"}})
	run(t, y)
	fam, kids := get(t, s, "FAM"), get(t, s, "KIDS")
	g := f.groups[fam.Remote]
	if g == nil || g.Name != "Famille" || len(g.Members) != 1 || len(g.Subgroups) != 1 || g.Subgroups[0] != kids.Remote {
		t.Fatalf("group not built: %+v", g)
	}
	// A member added in the address book comes to the card.
	f.groups[fam.Remote].Members = append(f.groups[fam.Remote].Members, get(t, s, "JC").Remote)
	f.touched[fam.Remote] = true
	run(t, y)
	if got := get(t, s, "FAM").Members; strings.Join(got, ",") != "AC,JC,KIDS" {
		t.Fatalf("members = %v", got)
	}
	// A member removed in the card leaves the address-book group.
	fam = get(t, s, "FAM")
	fam.Members = []string{"JC", "KIDS"}
	save(t, s, fam)
	run(t, y)
	if got := f.groups[fam.Remote].Members; len(got) != 1 || got[0] != get(t, s, "JC").Remote {
		t.Fatalf("address-book members = %v", got)
	}
	// An address-book member without a card is left alone.
	f.groups[fam.Remote].Members = append(f.groups[fam.Remote].Members, "STRANGER:ABPerson")
	run(t, y)
	if got := get(t, s, "FAM").Members; strings.Join(got, ",") != "JC,KIDS" {
		t.Fatalf("stranger leaked into the card: %v", got)
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	y, f, s := setup(t)
	save(t, s, &store.Contact{Alias: "JMR", Kind: store.Person, Name: "Jean-Moïse Rochat"})
	y.DryRun = true
	rep := run(t, y)
	if len(rep.Changes) != 1 || rep.Changes[0].Action != "card created" || len(f.cards) != 0 || len(f.seen) != 0 {
		t.Fatalf("dry run: %+v %v", rep, f.cards)
	}
	if get(t, s, "JMR").Remote != "" {
		t.Fatal("dry run wrote the card")
	}
}

func TestRemovedCardForgetsLink(t *testing.T) {
	y, f, s := setup(t)
	save(t, s, &store.Contact{Alias: "JMR", Kind: store.Person, Name: "Jean-Moïse Rochat"})
	run(t, y)
	if err := s.Remove("JMR"); err != nil {
		t.Fatal(err)
	}
	rep := run(t, y)
	if len(f.cards) != 1 || rep.Linked != 0 {
		t.Fatalf("the address book keeps its card, the link goes: %+v", rep)
	}
}

func TestOtherAliases(t *testing.T) {
	y, f, s := setup(t)
	save(t, s, &store.Contact{Alias: "EC", Aliases: []string{"EVE"}, Kind: store.Person, Name: "Eve Clément"})
	save(t, s, &store.Contact{Alias: "FAM", Kind: store.Group, Name: "Famille", Members: []string{"EVE"}})
	run(t, y)
	ec := get(t, s, "EC")
	if g := f.groups[get(t, s, "FAM").Remote]; g == nil || len(g.Members) != 1 || g.Members[0] != ec.Remote {
		t.Fatalf("a member named by another alias joins the group: %+v", g)
	}
	// The main alias changes: the link follows, no second card is created.
	if err := s.Remove("EC"); err != nil {
		t.Fatal(err)
	}
	ec.Alias, ec.Aliases = "EVE", []string{"EC"}
	save(t, s, ec)
	rep := run(t, y)
	if len(f.cards) != 1 || rep.Linked != 2 {
		t.Fatalf("rename lost the link: %d cards, %+v", len(f.cards), rep)
	}
}

func TestAdoptedCardEditWins(t *testing.T) {
	y, f, s := setup(t)
	f.cards["X:ABPerson"] = &Card{ID: "X:ABPerson", Kind: "person", Given: "Jean-Moïse", Family: "Rochat", Org: "UNILTITLE:x"}
	if err := Adopt(y.State, "JMR", "X:ABPerson", f.cards["X:ABPerson"]); err != nil {
		t.Fatal(err)
	}
	save(t, s, &store.Contact{Alias: "JMR", Kind: store.Person, Name: "Jean-Moïse Rochat", Org: "UNIL", Remote: "X:ABPerson"})
	run(t, y)
	if got := f.cards["X:ABPerson"].Org; got != "UNIL" {
		t.Fatalf("address book org = %q, want the edited card's", got)
	}
	if got := get(t, s, "JMR").Org; got != "UNIL" {
		t.Fatalf("card org = %q", got)
	}
}
