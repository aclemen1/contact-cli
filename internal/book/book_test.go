package book

import (
	"testing"

	"github.com/aclemen1/contact-cli/internal/store"
)

func newBook(cs ...*store.Contact) *Book {
	b := &Book{Cards: map[string]*store.Contact{}}
	for _, c := range cs {
		if c.Sphere == "" {
			c.Sphere = "perso"
		}
		b.Put(c)
	}
	b.computeGroups()
	return b
}

func TestSuggest(t *testing.T) {
	b := newBook(&store.Contact{Alias: "JMR", Name: "x"})
	cases := map[string]string{"Jean-Moïse Rochat": "JMR2", "Ève Clément": "EC", "Assura": "ASSU", "Régie du Rivage": "RDR"}
	for name, want := range cases {
		if got := b.Suggest(name); got != want {
			t.Errorf("Suggest(%q) = %s, want %s", name, got, want)
		}
	}
}

func TestMembersRecursive(t *testing.T) {
	b := newBook(
		&store.Contact{Alias: "AC", Kind: store.Person, Name: "Alain"},
		&store.Contact{Alias: "EVE", Kind: store.Person, Name: "Eve"},
		&store.Contact{Alias: "JC", Kind: store.Person, Name: "Jeannette"},
		&store.Contact{Alias: "KIDS", Kind: store.Group, Name: "Enfants", Members: []string{"EVE"}},
		&store.Contact{Alias: "FAM", Kind: store.Group, Name: "Famille", Members: []string{"AC", "JC", "KIDS"}},
	)
	direct, err := b.Members("FAM", false)
	if err != nil || len(direct) != 3 {
		t.Fatalf("direct members: %v %v", direct, err)
	}
	all, err := b.Members("contact:fam", true)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range all {
		got = append(got, c.Alias)
	}
	if want := "AC EVE JC"; join(got) != want {
		t.Fatalf("recursive members = %s, want %s", join(got), want)
	}
	if g := b.Cards["EVE"].Groups; len(g) != 1 || g[0] != "KIDS" {
		t.Fatalf("groups of EVE = %v", g)
	}
	if !b.WouldCycle("KIDS", "FAM") || b.WouldCycle("FAM", "EVE") {
		t.Fatal("WouldCycle wrong")
	}
	b.Cards["KIDS"].Members = append(b.Cards["KIDS"].Members, "FAM")
	if _, err := b.Members("FAM", true); err == nil {
		t.Fatal("a cycle must be refused")
	}
}

func TestResolve(t *testing.T) {
	b := newBook(
		&store.Contact{Alias: "JC", Kind: store.Person, Name: "Jeannette Clément", Tags: []string{"jeannette"}, Phones: []string{"mobile:+41 79 123 45 67"}},
		&store.Contact{Alias: "AC", Kind: store.Person, Name: "Alain Clément", Emails: []string{"alain@clement.aero"}},
	)
	check := func(q, want, by string) {
		t.Helper()
		m := b.Resolve(q)
		if len(m) == 0 || m[0].Alias != want || m[0].By != by {
			t.Fatalf("Resolve(%q) = %+v, want %s by %s", q, m, want, by)
		}
	}
	check("jc", "JC", "alias")
	check("Jeannette", "JC", "tag")
	check("alain@clement.aero", "AC", "email")
	check("079 123 45 67", "JC", "phone")
	if m := b.Resolve("clement"); len(m) != 2 {
		t.Fatalf("name match without accents: %+v", m)
	}
}

func join(l []string) string {
	s := ""
	for i, x := range l {
		if i > 0 {
			s += " "
		}
		s += x
	}
	return s
}
