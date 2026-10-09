// Package csync keeps the cards and an address book in step, both ways. Each
// linked pair remembers the values it last agreed on (its base): a field
// changed on one side only goes to the other; changed on both, the card wins,
// since the card is the reference, and the lost value goes to its log.
//
// Only cards are imported, never address-book entries: an address-book card
// enters through add --from. A card without remote is created in the address
// book. Nothing is ever deleted there: a removed card only loses its link.
package csync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/aclemen1/contact-cli/internal/store"
)

// Card is an address-book card.
type Card struct {
	ID        string
	Kind      string // person or organization
	Given     string
	Family    string
	Org       string
	Phones    []string // value or label:value
	Emails    []string
	Container string
}

// Group is an address-book group with the ids of its member cards and subgroups.
type Group struct {
	ID        string
	Name      string
	Members   []string
	Subgroups []string
}

// Backend is the address book the sync drives.
type Backend interface {
	// Changes lists the ids touched since the client last marked its changes seen;
	// full is true when the history restarted and everything must be read.
	Changes(client string) (touched map[string]bool, full bool, token string, err error)
	MarkSeen(client, token string) error
	// Container resolves an address book by name or id.
	Container(name string) (string, error)
	// Get reads one card; nil when it no longer exists.
	Get(id string) (*Card, error)
	Create(container string, c Card) (string, error)
	// Update writes the fields named: name, org, phones, emails.
	Update(id string, c Card, which []string) error
	// Groups lists every group, with its members.
	Groups() (map[string]*Group, error)
	GroupCreate(container, name string) (string, error)
	GroupRename(id, name string) error
	GroupAdd(group, card string) error
	GroupRemove(group, card string) error
	GroupAddSub(parent, sub string) error
	GroupRemoveSub(parent, sub string) error
}

// Fields are the values the sync compares.
type Fields struct {
	Name    string   `json:"name"`
	Org     string   `json:"org,omitempty"`
	Phones  []string `json:"phones,omitempty"`
	Emails  []string `json:"emails,omitempty"`
	Members []string `json:"members,omitempty"` // groups: aliases of the synced members
}

type link struct {
	ID   string `json:"id"`
	Base Fields `json:"base"`
}

type State struct {
	Version int             `json:"version"`
	Links   map[string]link `json:"links"` // by alias
}

func LoadState(path string) (*State, error) {
	st := &State{Version: 1, Links: map[string]link{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	var old State
	if err := json.Unmarshal(b, &old); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	if old.Version == 1 && old.Links != nil {
		return &old, nil
	}
	return st, nil
}

func (st *State) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(st, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Change is one thing the sync did, or would do in a dry run.
// Adopt records the address-book card a new card was taken from as the base
// of their link, so that the first sync tells which side changed since.
func Adopt(path, alias, id string, r *Card) error {
	st, err := LoadState(path)
	if err != nil {
		return err
	}
	if _, ok := st.Links[alias]; ok {
		return nil
	}
	st.Links[alias] = link{ID: id, Base: remoteFields(r)}
	return st.Save(path)
}

type Change struct {
	Alias  string `json:"alias"`
	Action string `json:"action"`
	Detail string `json:"detail,omitempty"`
}

type Report struct {
	DryRun  bool     `json:"dry_run,omitempty"`
	Linked  int      `json:"linked"`
	Read    int      `json:"read"` // cards and groups read from the address book
	Changes []Change `json:"changes"`
	Errors  []string `json:"errors,omitempty"`
}

// Event is a card changed by the sync, for the hooks.
type Event struct {
	Store   *store.Store
	Contact *store.Contact
}

type Sync struct {
	Stores    map[string]*store.Store
	Back      Backend
	State     string
	Container string
	Client    string
	DryRun    bool
	// Fire receives the cards changed once the stores are written.
	Fire func(Event)
}

func nfc(s string) string { return norm.NFC.String(strings.TrimSpace(s)) }

func nfcs(l []string) []string {
	out := make([]string, 0, len(l))
	for _, x := range l {
		if x = nfc(x); x != "" {
			out = append(out, x)
		}
	}
	return sorted(out)
}

// SplitName gives the given and family names of a person's card.
func SplitName(c *store.Contact) (string, string) {
	if c.Given != "" || c.Family != "" {
		return c.Given, c.Family
	}
	words := strings.Fields(c.Name)
	if len(words) < 2 {
		return "", c.Name
	}
	return strings.Join(words[:len(words)-1], " "), words[len(words)-1]
}

func cardFields(c *store.Contact) Fields {
	f := Fields{Name: nfc(c.Name), Phones: nfcs(c.Phones), Emails: nfcs(c.Emails)}
	if c.Kind == store.Person {
		f.Org = nfc(c.Org)
	}
	return f
}

func remoteFields(c *Card) Fields {
	f := Fields{Phones: nfcs(c.Phones), Emails: nfcs(c.Emails)}
	if c.Kind == "organization" {
		f.Name = nfc(c.Org)
	} else {
		f.Name = nfc(strings.TrimSpace(c.Given + " " + c.Family))
		f.Org = nfc(c.Org)
	}
	return f
}

func toCard(c *store.Contact, f Fields) Card {
	out := Card{Phones: f.Phones, Emails: f.Emails}
	if c.Kind == store.Organisation {
		out.Kind, out.Org = "organization", f.Name
		return out
	}
	tmp := *c
	if f.Name != nfc(c.Name) {
		tmp.Name, tmp.Given, tmp.Family = f.Name, "", ""
	}
	out.Kind = "person"
	out.Given, out.Family = SplitName(&tmp)
	out.Org = f.Org
	return out
}

func eq(a, b []string) bool { return strings.Join(a, "\x00") == strings.Join(b, "\x00") }

// merge compares each field with the base: a change on one side wins; on both, the card.
func merge(base, c, r Fields) (out Fields, toCard, toRemote, lost []string) {
	out = base
	pick := func(name string, b, cv, rv string, set func(string)) {
		switch {
		case cv == rv:
			set(cv)
		case cv == b:
			set(rv)
			toCard = append(toCard, name)
		case rv == b:
			set(cv)
			toRemote = append(toRemote, name)
		default:
			set(cv)
			toRemote = append(toRemote, name)
			lost = append(lost, name+" in the address book: "+rv)
		}
	}
	join := func(l []string) string { return strings.Join(l, " · ") }
	split := func(s string) []string {
		if s == "" {
			return nil
		}
		return strings.Split(s, " · ")
	}
	pick("name", base.Name, c.Name, r.Name, func(v string) { out.Name = v })
	pick("org", base.Org, c.Org, r.Org, func(v string) { out.Org = v })
	pick("phones", join(base.Phones), join(c.Phones), join(r.Phones), func(v string) { out.Phones = split(v) })
	pick("emails", join(base.Emails), join(c.Emails), join(r.Emails), func(v string) { out.Emails = split(v) })
	return
}

// mergeSet merges member sets element by element: a change on one side wins; on both, the card.
func mergeSet(base, c, r []string) []string {
	in := func(l []string, x string) bool {
		for _, y := range l {
			if y == x {
				return true
			}
		}
		return false
	}
	var out []string
	for _, x := range sorted(append(append(append([]string{}, base...), c...), r...)) {
		ic, ir, ib := in(c, x), in(r, x), in(base, x)
		keep := ic
		if ic == ir {
			keep = ic
		} else if ic == ib {
			keep = ir
		}
		if keep {
			out = append(out, x)
		}
	}
	return out
}

func setCard(c *store.Contact, f Fields, which []string, given, family string) {
	for _, w := range which {
		switch w {
		case "name":
			c.Name = f.Name
			if c.Given != "" || c.Family != "" {
				c.Given, c.Family = given, family
			}
		case "org":
			c.Org = f.Org
		case "phones":
			c.Phones = f.Phones
		case "emails":
			c.Emails = f.Emails
		}
	}
}

// pending is a change to a card, applied under its store's lock.
type pending struct {
	alias string
	apply func(c *store.Contact) bool
}

func (y *Sync) Run() (*Report, error) {
	rep := &Report{DryRun: y.DryRun, Changes: []Change{}}
	st, err := LoadState(y.State)
	if err != nil {
		return nil, err
	}
	touched, full, token, err := y.Back.Changes(y.Client)
	if err != nil {
		return nil, err
	}
	if len(st.Links) == 0 {
		full = true
	}
	container, err := y.Back.Container(y.Container)
	if err != nil {
		return nil, err
	}
	cards := map[string]*store.Contact{}
	owner := map[string]*store.Store{}
	var aliases []string
	var names []string
	for n := range y.Stores {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		cs, err := y.Stores[n].List()
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			if cards[c.Alias] != nil {
				continue
			}
			cards[c.Alias], owner[c.Alias] = c, y.Stores[n]
			aliases = append(aliases, c.Alias)
		}
	}
	sort.Strings(aliases)
	// canon maps every alias, main or other, to the main one.
	canon := map[string]string{}
	for _, a := range aliases {
		canon[a] = a
	}
	for _, a := range aliases {
		for _, o := range cards[a].Aliases {
			if canon[o] == "" {
				canon[o] = a
			}
		}
	}
	// A card whose main alias changed keeps its link, found under one of its other aliases.
	for a, l := range st.Links {
		if m := canon[a]; cards[a] == nil && m != "" && m != a {
			if _, ok := st.Links[m]; !ok {
				st.Links[m] = l
				delete(st.Links, a)
			}
		}
	}
	mainOf := func(m string) string {
		if c := canon[m]; c != "" {
			return c
		}
		return m
	}
	var todo []pending
	edit := func(a string, f func(c *store.Contact) bool) { todo = append(todo, pending{a, f}) }
	note := func(a, action, detail string) {
		rep.Changes = append(rep.Changes, Change{Alias: a, Action: action, Detail: detail})
	}
	fail := func(a string, err error) { rep.Errors = append(rep.Errors, fmt.Sprintf("%s: %v", a, err)) }
	do := func(f func() error) error {
		if y.DryRun {
			return nil
		}
		return f()
	}
	// remoteOf is the address-book id of a card in this run, created ones included.
	remoteOf := map[string]string{}
	aliasOf := map[string]string{}
	setRemote := func(a, id string) { remoteOf[a], aliasOf[id] = id, a }
	for a, l := range st.Links {
		if cards[a] != nil {
			setRemote(a, l.ID)
		}
	}

	// People and organisations.
	for _, a := range aliases {
		c := cards[a]
		if c.Kind == store.Group {
			continue
		}
		l, linked := st.Links[a]
		force := false
		if !linked && c.Remote != "" {
			l, linked, force = link{ID: c.Remote, Base: cardFields(c)}, true, true
			st.Links[a] = l
			setRemote(a, c.Remote)
			note(a, "linked", c.Remote)
		}
		if linked && c.Remote != "" && c.Remote != l.ID {
			l, force = link{ID: c.Remote, Base: cardFields(c)}, true
			st.Links[a] = l
			setRemote(a, c.Remote)
		}
		cf := cardFields(c)
		if !linked {
			card := toCard(c, cf)
			var id string
			if err := do(func() error {
				var err error
				id, err = y.Back.Create(container, card)
				return err
			}); err != nil {
				fail(a, err)
				continue
			}
			note(a, "card created", "")
			if y.DryRun {
				continue
			}
			setRemote(a, id)
			st.Links[a] = link{ID: id, Base: cf}
			edit(a, func(c *store.Contact) bool {
				c.Remote = id
				owner[a].Note(c, "created in the address book")
				return true
			})
			continue
		}
		if !full && !force && !touched[l.ID] && fieldsEq(cf, l.Base) {
			continue
		}
		r, err := y.Back.Get(l.ID)
		rep.Read++
		if err != nil {
			fail(a, err)
			continue
		}
		if r == nil {
			delete(st.Links, a)
			delete(remoteOf, a)
			edit(a, func(c *store.Contact) bool {
				c.Remote = ""
				owner[a].Note(c, "unlinked: card deleted in the address book")
				return true
			})
			note(a, "unlinked", "card deleted in the address book")
			continue
		}
		rf := remoteFields(r)
		merged, toC, toR, lost := merge(l.Base, cf, rf)
		if len(toR) > 0 {
			card := toCard(c, merged)
			if err := do(func() error { return y.Back.Update(l.ID, card, toR) }); err != nil {
				fail(a, err)
				continue
			}
			note(a, "card updated", strings.Join(toR, ", "))
		}
		if len(toC) > 0 || len(lost) > 0 {
			m, which, lost, given, family := merged, toC, lost, r.Given, r.Family
			edit(a, func(c *store.Contact) bool {
				setCard(c, m, which, given, family)
				var parts []string
				if len(which) > 0 {
					parts = append(parts, "from the address book: "+strings.Join(which, ", "))
				}
				if len(lost) > 0 {
					parts = append(parts, "conflict kept the card ("+strings.Join(lost, "; ")+")")
				}
				owner[a].Note(c, strings.Join(parts, "; "))
				return true
			})
			if len(toC) > 0 {
				note(a, "card file updated", strings.Join(toC, ", "))
			}
		}
		l.Base = merged
		st.Links[a] = l
	}

	// Groups: created first, so that subgroups have an id, then their members.
	var groups map[string]*Group
	needGroups := false
	for _, a := range aliases {
		if cards[a].Kind == store.Group {
			needGroups = true
		}
	}
	if needGroups {
		if groups, err = y.Back.Groups(); err != nil {
			return nil, err
		}
		rep.Read += len(groups)
	}
	var groupAliases []string
	for _, a := range aliases {
		c := cards[a]
		if c.Kind != store.Group {
			continue
		}
		l, linked := st.Links[a]
		if !linked && c.Remote != "" && groups[c.Remote] != nil {
			l, linked = link{ID: c.Remote, Base: Fields{Name: nfc(c.Name)}}, true
			st.Links[a] = l
			note(a, "linked again", "")
		}
		if linked && groups[l.ID] == nil {
			delete(st.Links, a)
			edit(a, func(c *store.Contact) bool {
				c.Remote = ""
				owner[a].Note(c, "unlinked: group deleted in the address book")
				return true
			})
			note(a, "unlinked", "group deleted in the address book")
			continue
		}
		if !linked {
			var id string
			if err := do(func() error {
				var err error
				id, err = y.Back.GroupCreate(container, nfc(c.Name))
				return err
			}); err != nil {
				fail(a, err)
				continue
			}
			note(a, "group created", "")
			if y.DryRun {
				continue
			}
			l = link{ID: id, Base: Fields{Name: nfc(c.Name)}}
			st.Links[a] = l
			groups[id] = &Group{ID: id, Name: nfc(c.Name)}
			edit(a, func(c *store.Contact) bool {
				c.Remote = id
				owner[a].Note(c, "created in the address book")
				return true
			})
		}
		setRemote(a, l.ID)
		groupAliases = append(groupAliases, a)
	}
	for _, a := range groupAliases {
		c := cards[a]
		l := st.Links[a]
		g := groups[l.ID]
		if g == nil {
			continue
		}
		// Name.
		cn, rn := nfc(c.Name), nfc(g.Name)
		switch {
		case cn == rn:
		case rn == l.Base.Name:
			if err := do(func() error { return y.Back.GroupRename(l.ID, cn) }); err != nil {
				fail(a, err)
				continue
			}
			note(a, "group renamed", cn)
		case cn == l.Base.Name:
			edit(a, func(c *store.Contact) bool {
				c.Name = rn
				owner[a].Note(c, "from the address book: name")
				return true
			})
			note(a, "card file updated", "name")
			cn = rn
		default:
			if err := do(func() error { return y.Back.GroupRename(l.ID, cn) }); err != nil {
				fail(a, err)
				continue
			}
			lost := rn
			edit(a, func(c *store.Contact) bool {
				owner[a].Note(c, "conflict kept the card (name in the address book: "+lost+")")
				return true
			})
			note(a, "group renamed", cn)
		}
		// Members: only those with an address-book id take part.
		var cm []string
		for _, m := range c.Members {
			if m = mainOf(m); remoteOf[m] != "" {
				cm = append(cm, m)
			}
		}
		cm = sorted(cm)
		var rm []string
		for _, id := range append(append([]string{}, g.Members...), g.Subgroups...) {
			if x := aliasOf[id]; x != "" {
				rm = append(rm, x)
			}
		}
		rm = sorted(rm)
		merged := mergeSet(l.Base.Members, cm, rm)
		addR, delR := diff(rm, merged)
		addC, delC := diff(cm, merged)
		for _, m := range addR {
			id, sub := remoteOf[m], cards[m] != nil && cards[m].Kind == store.Group
			if err := do(func() error {
				if sub {
					return y.Back.GroupAddSub(l.ID, id)
				}
				return y.Back.GroupAdd(l.ID, id)
			}); err != nil {
				fail(a, err)
				continue
			}
			note(a, "member added in the address book", m)
		}
		for _, m := range delR {
			id, sub := remoteOf[m], cards[m] != nil && cards[m].Kind == store.Group
			if err := do(func() error {
				if sub {
					return y.Back.GroupRemoveSub(l.ID, id)
				}
				return y.Back.GroupRemove(l.ID, id)
			}); err != nil {
				fail(a, err)
				continue
			}
			note(a, "member removed in the address book", m)
		}
		if len(addC)+len(delC) > 0 {
			add, del := addC, delC
			edit(a, func(c *store.Contact) bool {
				for i, m := range c.Members {
					c.Members[i] = mainOf(m)
				}
				c.Members = sorted(append(remove(c.Members, del), add...))
				var parts []string
				if len(add) > 0 {
					parts = append(parts, "added "+strings.Join(add, ", "))
				}
				if len(del) > 0 {
					parts = append(parts, "removed "+strings.Join(del, ", "))
				}
				owner[a].Note(c, "members from the address book: "+strings.Join(parts, "; "))
				return true
			})
			note(a, "card file updated", "members")
		}
		l.Base = Fields{Name: cn, Members: merged}
		st.Links[a] = l
	}

	// Links of removed cards: forgotten, the address book keeps its card.
	for a, l := range st.Links {
		if cards[a] == nil {
			delete(st.Links, a)
			note(a, "unlinked", "card file removed; "+l.ID+" stays in the address book")
		}
	}

	rep.Linked = len(st.Links)
	sort.SliceStable(rep.Changes, func(i, j int) bool { return rep.Changes[i].Alias < rep.Changes[j].Alias })
	if y.DryRun {
		return rep, nil
	}
	if err := y.apply(todo, owner); err != nil {
		return rep, err
	}
	if err := st.Save(y.State); err != nil {
		return rep, err
	}
	if token != "" && len(rep.Errors) == 0 {
		if err := y.Back.MarkSeen(y.Client, token); err != nil {
			rep.Errors = append(rep.Errors, "mark seen: "+err.Error())
		}
	}
	return rep, nil
}

func fieldsEq(a, b Fields) bool {
	return a.Name == b.Name && a.Org == b.Org && eq(a.Phones, b.Phones) && eq(a.Emails, b.Emails)
}

// diff gives what to add to from and remove from it to reach to.
func diff(from, to []string) (add, del []string) {
	in := map[string]bool{}
	for _, x := range from {
		in[x] = true
	}
	out := map[string]bool{}
	for _, x := range to {
		out[x] = true
		if !in[x] {
			add = append(add, x)
		}
	}
	for _, x := range from {
		if !out[x] {
			del = append(del, x)
		}
	}
	return
}

func remove(l, del []string) []string {
	var out []string
	for _, x := range l {
		keep := true
		for _, d := range del {
			if x == d {
				keep = false
			}
		}
		if keep {
			out = append(out, x)
		}
	}
	return out
}

// apply writes the card changes, one commit per store, then fires their events.
func (y *Sync) apply(todo []pending, owner map[string]*store.Store) error {
	byStore := map[*store.Store][]pending{}
	var order []*store.Store
	for _, p := range todo {
		s := owner[p.alias]
		if _, ok := byStore[s]; !ok {
			order = append(order, s)
		}
		byStore[s] = append(byStore[s], p)
	}
	var events []Event
	for _, s := range order {
		ps := byStore[s]
		err := s.WriteAs(func() (string, error) {
			changed := map[string]*store.Contact{}
			var seq []string
			for _, p := range ps {
				c := changed[p.alias]
				if c == nil {
					var err error
					if c, err = s.Get(p.alias); err != nil {
						return "", err
					}
					if c == nil {
						continue
					}
				}
				if p.apply(c) {
					if changed[p.alias] == nil {
						seq = append(seq, p.alias)
					}
					changed[p.alias] = c
				}
			}
			for _, a := range seq {
				if err := s.Save(changed[a]); err != nil {
					return "", err
				}
				events = append(events, Event{Store: s, Contact: changed[a]})
			}
			if len(seq) == 0 {
				return "", nil
			}
			return fmt.Sprintf("sync contacts %s: %s", s.Sphere, strings.Join(seq, ", ")), nil
		})
		if err != nil {
			return err
		}
	}
	if y.Fire != nil {
		for _, e := range events {
			y.Fire(e)
		}
	}
	return nil
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
