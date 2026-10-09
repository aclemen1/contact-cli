// Package book reads the cards of every sphere as one directory: aliases are
// unique across spheres, groups may name cards of any sphere.
package book

import (
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/aclemen1/contact-cli/internal/spec"
	"github.com/aclemen1/contact-cli/internal/store"
)

type Book struct {
	Stores map[string]*store.Store
	Cards  map[string]*store.Contact // by alias
}

// Load reads every store; an alias found in two spheres is a warning, the first sphere keeps it.
func Load(stores map[string]*store.Store, warn func(string)) (*Book, error) {
	b := &Book{Stores: stores, Cards: map[string]*store.Contact{}}
	var names []string
	for n := range stores {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		cs, err := stores[n].List()
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			if old, ok := b.Cards[c.Alias]; ok {
				if warn != nil {
					warn("alias " + c.Alias + " is in " + old.Sphere + " and " + c.Sphere + "; " + old.Sphere + " is used")
				}
				continue
			}
			b.Cards[c.Alias] = c
		}
	}
	b.computeGroups()
	return b, nil
}

func (b *Book) computeGroups() {
	for _, c := range b.Cards {
		c.Groups = nil
	}
	for _, g := range b.Sorted() {
		if g.Kind != store.Group {
			continue
		}
		for _, m := range g.Members {
			if c := b.Cards[m]; c != nil {
				c.Groups = append(c.Groups, g.Alias)
			}
		}
	}
}

// Sorted lists the cards by alias.
func (b *Book) Sorted() []*store.Contact {
	out := make([]*store.Contact, 0, len(b.Cards))
	for _, c := range b.Cards {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out
}

// Get finds a card by alias, with or without its contact: prefix.
func (b *Book) Get(alias string) (*store.Contact, error) {
	a, err := store.NormAlias(alias)
	if err != nil {
		return nil, err
	}
	c := b.Cards[a]
	if c == nil {
		return nil, spec.NotFound("no contact %s. Find one with: contact resolve <name>", a)
	}
	return c, nil
}

// Members lists the members of a group; recursive unfolds the groups it holds
// into their own members, every card once, and refuses a cycle.
func (b *Book) Members(alias string, recursive bool) ([]*store.Contact, error) {
	g, err := b.Get(alias)
	if err != nil {
		return nil, err
	}
	if g.Kind != store.Group {
		return nil, spec.UserError("%s is a %s, not a group", g.Alias, g.Kind)
	}
	seen := map[string]bool{}
	var out []*store.Contact
	var walk func(g *store.Contact, path []string) error
	walk = func(g *store.Contact, path []string) error {
		for _, m := range g.Members {
			c := b.Cards[m]
			if c == nil {
				continue
			}
			if recursive && c.Kind == store.Group {
				for _, p := range path {
					if p == c.Alias {
						return spec.Conflict("groups form a cycle: %s → %s", strings.Join(path, " → "), c.Alias)
					}
				}
				if err := walk(c, append(append([]string{}, path...), c.Alias)); err != nil {
					return err
				}
				continue
			}
			if !seen[c.Alias] {
				seen[c.Alias] = true
				out = append(out, c)
			}
		}
		return nil
	}
	if err := walk(g, []string{g.Alias}); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alias < out[j].Alias })
	return out, nil
}

// WouldCycle says whether adding member to group closes a cycle of groups.
func (b *Book) WouldCycle(group, member string) bool {
	if group == member {
		return true
	}
	seen := map[string]bool{}
	var reach func(a string) bool
	reach = func(a string) bool {
		if a == group {
			return true
		}
		if seen[a] {
			return false
		}
		seen[a] = true
		c := b.Cards[a]
		if c == nil || c.Kind != store.Group {
			return false
		}
		for _, m := range c.Members {
			if reach(m) {
				return true
			}
		}
		return false
	}
	return reach(member)
}

// Match is a card found by resolve, with what matched.
type Match struct {
	*store.Contact
	By string `json:"by"`
}

// Resolve finds the cards a word designates: alias, tag, e-mail, phone, then name.
func (b *Book) Resolve(q string) []Match {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	if c, err := b.Get(q); err == nil {
		return []Match{{c, "alias"}}
	}
	fq := Fold(q)
	digits := onlyDigits(q)
	var tags, mails, phones, names []Match
	for _, c := range b.Sorted() {
		switch {
		case anyEq(c.Tags, fq):
			tags = append(tags, Match{c, "tag"})
		case strings.Contains(q, "@") && anyContains(c.Emails, fq):
			mails = append(mails, Match{c, "email"})
		case len(digits) >= 6 && anyDigits(c.Phones, digits):
			phones = append(phones, Match{c, "phone"})
		case strings.Contains(Fold(c.Name), fq) || strings.Contains(Fold(c.Org), fq):
			names = append(names, Match{c, "name"})
		}
	}
	for _, l := range [][]Match{tags, mails, phones} {
		if len(l) > 0 {
			return l
		}
	}
	return names
}

// Fold lowers a text and strips its accents, for matching.
func Fold(s string) string {
	var out []rune
	for _, r := range norm.NFD.String(strings.ToLower(strings.TrimSpace(s))) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// Suggest proposes a free alias from a name: the initials of its words (JMR
// for Jean-Moïse Rochat), the first letters of a single word, then a number.
func (b *Book) Suggest(name string) string {
	words := strings.FieldsFunc(Fold(name), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	base := ""
	switch {
	case len(words) == 0:
		base = "X"
	case len(words) == 1:
		w := []rune(strings.ToUpper(words[0]))
		base = string(w[:min(len(w), 4)])
	default:
		for _, w := range words {
			base += strings.ToUpper(string([]rune(w)[0]))
		}
		if len(base) > 6 {
			base = base[:6]
		}
	}
	if base[0] < 'A' || base[0] > 'Z' {
		base = "X" + base
	}
	if b.Cards[base] == nil {
		return base
	}
	for i := 2; ; i++ {
		a := base + itoa(i)
		if b.Cards[a] == nil {
			return a
		}
	}
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

func anyEq(l []string, fq string) bool {
	for _, x := range l {
		if Fold(x) == fq {
			return true
		}
	}
	return false
}

func anyContains(l []string, fq string) bool {
	for _, x := range l {
		if strings.Contains(Fold(x), fq) {
			return true
		}
	}
	return false
}

func anyDigits(l []string, d string) bool {
	tail := func(s string) string { return s[max(0, len(s)-9):] }
	for _, x := range l {
		if xd := onlyDigits(x); len(xd) >= 6 && tail(xd) == tail(d) {
			return true
		}
	}
	return false
}

func onlyDigits(s string) string {
	var out []rune
	for _, r := range s {
		if r >= '0' && r <= '9' {
			out = append(out, r)
		}
	}
	return string(out)
}
