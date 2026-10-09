package actions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/aclemen1/contact-cli/internal/book"
	"github.com/aclemen1/contact-cli/internal/config"
	"github.com/aclemen1/contact-cli/internal/csync"
	"github.com/aclemen1/contact-cli/internal/spec"
	"github.com/aclemen1/contact-cli/internal/store"
)

// Backend is the address book: the sync's operations and the reading of one raw card.
type Backend interface {
	csync.Backend
	GetRaw(id string) (*csync.RawCard, error)
}

// NewBackend builds the address-book backend; tests replace it.
var NewBackend = func(cfg *config.Config) Backend { return csync.Macos{Bin: cfg.Contacts.Bin} }

func kindParam() spec.Param {
	return spec.Param{Name: "kind", Kind: spec.String, Enum: store.Kinds, Help: "person, organisation or group."}
}

func registerContacts() {
	spec.Register(&spec.Action{
		Category: "contact", Name: "add", Top: true,
		Summary: "Create a card: a person, an organisation or a group, under a short alias unique across spheres.",
		Discussion: "Without --alias, one is proposed from the initials of the name (JMR for Jean-Moïse Rochat), with a number when taken. " +
			"--from takes an address-book card (its id, e.g. 45612C46-…:ABPerson) and fills the card from it, linked for the sync; the name is then optional. " +
			"Only cards of the configured address book are taken, never a merged (unified) one. A card is cited elsewhere as contact:<alias>.",
		Params: []spec.Param{
			{Name: "name", Kind: spec.String, Positional: true, Help: "Full name: « Jean-Moïse Rochat », « Régie du Rivage », « Famille »."},
			{Name: "sphere", Kind: spec.String, Required: true, Help: "Sphere of the main relationship: perso or pro. A contact of both is read from both; it is stored once.",
				Missing: "a new card needs --sphere: the sphere of the main relationship; there is no default"},
			kindParam(),
			{Name: "alias", Kind: spec.String, Help: "Main alias in capitals, e.g. JMR: the file name and the form other tools are given. Refused when any card of any sphere has it."},
			{Name: "also", Kind: spec.StringList, Help: "Other alias of the card, repeatable, e.g. EVE beside EC; same namespace as the main ones."},
			{Name: "given", Kind: spec.String, Help: "Given name of a person, when the last word of the name is not the family name."},
			{Name: "family", Kind: spec.String, Help: "Family name of a person, e.g. « de Ricou »."},
			{Name: "org", Kind: spec.String, Help: "Organisation a person works for."},
			{Name: "phone", Kind: spec.StringList, Help: "Phone, repeatable; label:value keeps the label (mobile:+41 79 000 00 00)."},
			{Name: "email", Kind: spec.StringList, Help: "E-mail, repeatable; label:value keeps the label (work:a@b.ch)."},
			{Name: "tag", Kind: spec.StringList, Help: "Word another tool uses for this contact, repeatable, e.g. jeannette."},
			{Name: "ref", Kind: spec.StringList, Help: "What the card belongs to or concerns, <tool>:<id>, repeatable."},
			{Name: "member", Kind: spec.StringList, Help: "Group: alias of a member, repeatable."},
			{Name: "group", Kind: spec.StringList, Help: "Alias of a group the card joins, repeatable."},
			{Name: "body", Kind: spec.String, Help: "Free notes, the body of the card."},
			{Name: "from", Kind: spec.String, Help: "Address-book card to take the card from, by id."},
		},
		Effects:  []string{"Writes <alias>.md in the sphere's store and commits it.", "Adds the alias to the members of each --group, in its own store.", "Fires the added hooks (updated for each group)."},
		Examples: []string{`contact add "Jean-Moïse Rochat" --sphere pro --alias JMR --email jmr@unil.ch`, `contact add "Eve Clément" --sphere perso --alias EC --also EVE`, `contact add Famille --kind group --sphere perso --member AC --member EVE`, `contact add --from 45612C46-72AC-4BBF-9C72-8BB56BE8EC6F:ABPerson --sphere perso --alias AC`},
		Run:      runAdd,
	})
	spec.Register(&spec.Action{
		Category: "contact", Name: "edit", Top: true,
		Summary: "Change a card: name, organisation, phones, e-mails, tags, refs, members, groups, notes.",
		Discussion: "Lists change element by element with --add-… and --rm-…; --phone and --email replace the whole list. " +
			"--remote links the card to an address-book id, none unlinks it.",
		Params: []spec.Param{
			{Name: "alias", Kind: spec.String, Positional: true, Required: true, Help: "Alias of the card, main or other."},
			{Name: "name", Kind: spec.String, Help: "New full name."},
			{Name: "add-alias", Kind: spec.StringList, Help: "Add another alias; refused when any card holds it."},
			{Name: "rm-alias", Kind: spec.StringList, Help: "Remove another alias; the main one changes only by --primary."},
			{Name: "primary", Kind: spec.String, Help: "Make this alias the main one: the file is renamed, the old main alias stays as another, groups naming it are rewritten."},
			{Name: "given", Kind: spec.String, Help: "Given name; none clears it."},
			{Name: "family", Kind: spec.String, Help: "Family name; none clears it."},
			{Name: "org", Kind: spec.String, Help: "Organisation of a person; none clears it."},
			kindParam(),
			{Name: "phone", Kind: spec.StringList, Help: "Replace the phones, repeatable."},
			{Name: "email", Kind: spec.StringList, Help: "Replace the e-mails, repeatable."},
			{Name: "add-phone", Kind: spec.StringList, Help: "Add a phone."},
			{Name: "rm-phone", Kind: spec.StringList, Help: "Remove a phone."},
			{Name: "add-email", Kind: spec.StringList, Help: "Add an e-mail."},
			{Name: "rm-email", Kind: spec.StringList, Help: "Remove an e-mail."},
			{Name: "add-tag", Kind: spec.StringList, Help: "Add a tag."},
			{Name: "rm-tag", Kind: spec.StringList, Help: "Remove a tag."},
			{Name: "add-ref", Kind: spec.StringList, Help: "Add a ref, <tool>:<id>."},
			{Name: "rm-ref", Kind: spec.StringList, Help: "Remove a ref."},
			{Name: "add-member", Kind: spec.StringList, Help: "Group: add a member by alias."},
			{Name: "rm-member", Kind: spec.StringList, Help: "Group: remove a member."},
			{Name: "add-group", Kind: spec.StringList, Help: "Join a group, by its alias."},
			{Name: "rm-group", Kind: spec.StringList, Help: "Leave a group."},
			{Name: "body", Kind: spec.String, Help: "Replace the free notes."},
			{Name: "append", Kind: spec.String, Help: "Add a paragraph to the free notes."},
			{Name: "remote", Kind: spec.String, Help: "Address-book id to link; none unlinks."},
		},
		Effects:  []string{"Rewrites the card and commits it; joining or leaving a group rewrites the group.", "--primary renames the file and rewrites the groups naming the old main alias, one commit per store.", "Fires the updated hooks."},
		Examples: []string{"contact edit JMR --add-phone mobile:+41 79 000 00 00", "contact edit FAM --add-member TAC", "contact edit JC --add-tag jeannette --add-group FAM", "contact edit EC --add-alias EVE", "contact edit EC --primary EVE"},
		Run:      runEdit,
	})
	spec.Register(&spec.Action{
		Category: "contact", Name: "rm", Top: true, Destructive: true,
		Summary:    "Remove a card. The address book keeps its card; the sync only forgets the link.",
		Discussion: "A card still member of a group is refused unless --force, which also takes it out of its groups.",
		Params: []spec.Param{
			{Name: "alias", Kind: spec.String, Positional: true, Required: true, Help: "Alias of the card."},
			{Name: "force", Kind: spec.Bool, Help: "Remove it from its groups too."},
		},
		Effects:  []string{"Deletes <alias>.md and commits.", "Fires the removed hooks."},
		Examples: []string{"contact rm JMR", "contact rm OLD --force"},
		Run:      runRm,
	})
	spec.Register(&spec.Action{
		Category: "contact", Name: "mv", Top: true,
		Summary:  "Move a card to another sphere; its alias and its links stay.",
		Params:   []spec.Param{{Name: "alias", Kind: spec.String, Positional: true, Required: true, Help: "Alias of the card."}, {Name: "to", Kind: spec.String, Required: true, Help: "Sphere to move it to."}},
		Effects:  []string{"Writes the card in the new store and removes it from the old one, one commit in each.", "Fires the moved hooks."},
		Examples: []string{"contact mv JMR --to pro"},
		Run:      runMv,
	})
	spec.Register(&spec.Action{
		Category: "contact", Name: "show", Top: true,
		Summary: "Show a card and what cites it elsewhere.",
		Discussion: "The views come from the configuration: each runs once per alias of the card, with {ref} replaced by contact:<alias> and {alias} by the alias; " +
			"their rows are merged, each once, with the alias that found it. A failing view is a warning. A group shows its members.",
		Params: []spec.Param{
			{Name: "alias", Kind: spec.String, Positional: true, Required: true, Help: "Alias of the card, with or without contact:."},
			{Name: "no-views", Kind: spec.Bool, Help: "Show the card alone."},
		},
		Examples: []string{"contact show JMR", "contact show contact:FAM --no-views"},
		Run:      runShow,
		Text:     textShow,
	})
	spec.Register(&spec.Action{
		Category: "contact", Name: "ls", Top: true,
		Summary: "List the cards, by alias.",
		Params: []spec.Param{
			readSphereParam(),
			kindParam(),
			{Name: "group", Kind: spec.String, Help: "Only the members of this group, its subgroups unfolded."},
			{Name: "ref", Kind: spec.String, Help: "Only the cards citing this ref."},
			{Name: "tag", Kind: spec.String, Help: "Only the cards with this tag."},
			{Name: "query", Kind: spec.String, Help: "Only the cards whose name, organisation or alias contains this text, accents ignored."},
		},
		Examples: []string{"contact ls", "contact ls --kind group", "contact ls --group FAM --format text", "contact ls --format tsv"},
		Run:      runLs,
		Text:     textLs,
		TSV:      tsvLs,
	})
	spec.Register(&spec.Action{
		Category: "contact", Name: "members", Top: true,
		Summary:    "List the members of a group; --recursive unfolds its subgroups into people and organisations.",
		Discussion: "A situation trigger with:contact:<group> holds for each member given by --recursive.",
		Params: []spec.Param{
			{Name: "alias", Kind: spec.String, Positional: true, Required: true, Help: "Alias of the group."},
			{Name: "recursive", Kind: spec.Bool, Help: "Unfold subgroups; a cycle is refused."},
		},
		Examples: []string{"contact members FAM", "contact members DIR --recursive --format tsv"},
		Run:      runMembers,
		Text:     textLs,
		TSV:      tsvLs,
	})
	spec.Register(&spec.Action{
		Category: "contact", Name: "resolve", Top: true,
		Summary:    "Find the alias a word designates: an alias, a tag, an e-mail, a phone, then a part of a name.",
		Discussion: "The first kind of match that finds something wins; names match without accents or case.",
		Params:     []spec.Param{{Name: "query", Kind: spec.String, Positional: true, Required: true, Help: "Alias, tag, e-mail, phone or name."}},
		Examples:   []string{"contact resolve jeannette", "contact resolve Rochat", "contact resolve 079 000 00 00"},
		Run:        runResolve,
		Text: func(w io.Writer, r any) {
			for _, m := range r.([]book.Match) {
				fmt.Fprintf(w, "%-8s %-12s %s (%s)\n", m.Alias, m.Kind, m.Name, m.By)
			}
		},
	})
}

func cfgAndBook(ctx *spec.Context) (*config.Config, *book.Book, error) {
	cfg, err := config.Load(ctx.Config)
	if err != nil {
		return nil, nil, err
	}
	b, err := LoadBook(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	return cfg, b, nil
}

// cardOf finds a card the call may see.
func cardOf(ctx *spec.Context, b *book.Book, alias string) (*store.Contact, error) {
	c, err := b.Get(alias)
	if err != nil {
		return nil, err
	}
	if ctx.Spheres != nil && !contains(ctx.Spheres, c.Sphere) {
		return nil, spec.NotFound("no contact %s here", c.Alias)
	}
	return c, nil
}

func normAliases(l []string) ([]string, error) {
	var out []string
	for _, a := range l {
		n, err := store.NormAlias(a)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// checkMembers checks the members of a group and gives them by their main alias.
func checkMembers(b *book.Book, group string, members []string) ([]string, error) {
	var out []string
	for _, m := range members {
		mc := b.Lookup(m)
		if mc == nil {
			return nil, spec.NotFound("no contact %s to add to %s. Create it first: contact add <name> --alias %s --sphere <sphere>", m, group, m)
		}
		if b.WouldCycle(group, mc.Alias) {
			return nil, spec.Conflict("%s in %s would make a cycle of groups", m, group)
		}
		out = append(out, mc.Alias)
	}
	return out, nil
}

// checkFree refuses an alias another card holds, as main or other alias, in any sphere.
func checkFree(ctx *spec.Context, b *book.Book, self *store.Contact, a, name string) error {
	old := b.Lookup(a)
	if old == nil || old == self {
		return nil
	}
	if ctx.Spheres != nil && !contains(ctx.Spheres, old.Sphere) {
		return spec.Conflict("alias %s is taken in another sphere. Free: %s", a, b.Suggest(name))
	}
	return spec.Conflict("alias %s is taken by %s (%s, %s). Free: %s", a, old.Alias, old.Name, old.Sphere, b.Suggest(name))
}

func cleanList(l []string) []string {
	var out []string
	for _, x := range l {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func runAdd(ctx *spec.Context) (any, error) {
	cfg, b, err := cfgAndBook(ctx)
	if err != nil {
		return nil, err
	}
	sphere := ctx.Str("sphere")
	if err := checkSphere(ctx, cfg, sphere); err != nil {
		return nil, err
	}
	s := b.Stores[sphere]
	if s == nil {
		if s, err = OpenStore(ctx, cfg, sphere); err != nil {
			return nil, err
		}
	}
	now := Now().Format(time.RFC3339)
	c := &store.Contact{Kind: ctx.Str("kind"), Name: strings.TrimSpace(ctx.Str("name")), Given: ctx.Str("given"), Family: ctx.Str("family"),
		Org: ctx.Str("org"), Phones: cleanList(ctx.List("phone")), Emails: cleanList(ctx.List("email")), Tags: cleanList(ctx.List("tag")),
		Refs: cleanList(ctx.List("ref")), Body: ctx.Str("body"), Created: now, Updated: now}
	if from := ctx.Str("from"); from != "" {
		if err := fillFrom(cfg, b, c, from); err != nil {
			return nil, err
		}
	}
	if c.Kind == "" {
		c.Kind = store.Person
	}
	if c.Name == "" {
		return nil, spec.UserError(`a card needs a name. Example: contact add "Jean-Moïse Rochat" --sphere pro`)
	}
	if a := ctx.Str("alias"); a != "" {
		if c.Alias, err = store.NormAlias(a); err != nil {
			return nil, err
		}
		if err := checkFree(ctx, b, nil, c.Alias, c.Name); err != nil {
			return nil, err
		}
	} else {
		c.Alias = b.Suggest(c.Name)
	}
	also, err := normAliases(ctx.List("also"))
	if err != nil {
		return nil, err
	}
	for _, a := range also {
		if a == c.Alias || contains(c.Aliases, a) {
			continue
		}
		if err := checkFree(ctx, b, nil, a, c.Name); err != nil {
			return nil, err
		}
		c.Aliases = append(c.Aliases, a)
	}
	members, err := normAliases(ctx.List("member"))
	if err != nil {
		return nil, err
	}
	if len(members) > 0 && c.Kind != store.Group {
		return nil, spec.UserError("--member needs --kind group")
	}
	b.Put(c)
	if members, err = checkMembers(b, c.Alias, members); err != nil {
		return nil, err
	}
	c.Members = members
	groups, err := normAliases(ctx.List("group"))
	if err != nil {
		return nil, err
	}
	for i, g := range groups {
		gc := b.Lookup(g)
		if gc == nil || gc.Kind != store.Group {
			return nil, spec.NotFound("no group %s. List them with: contact ls --kind group", g)
		}
		groups[i] = gc.Alias
		if b.WouldCycle(g, c.Alias) {
			return nil, spec.Conflict("%s in %s would make a cycle of groups", c.Alias, g)
		}
	}
	err = s.WriteAs(func() (string, error) {
		if old, _ := s.Get(c.Alias); old != nil {
			return "", spec.Conflict("alias %s is taken", c.Alias)
		}
		what := "created"
		if c.Remote != "" {
			what = "created from the address book"
		}
		s.Note(c, what)
		if err := s.Save(c); err != nil {
			return "", err
		}
		return fmt.Sprintf("add %s: %s", c.Alias, c.Name), nil
	})
	if err != nil {
		return nil, err
	}
	fire(ctx, cfg, s, c, "added", "")
	for _, g := range groups {
		if err := joinGroup(ctx, cfg, b, g, c.Alias, true); err != nil {
			ctx.Warn(fmt.Sprintf("%s not added to %s: %v", c.Alias, g, err))
		}
	}
	return c, nil
}

// fillFrom fills a new card from an address-book card.
func fillFrom(cfg *config.Config, b *book.Book, c *store.Contact, id string) error {
	for _, o := range b.Sorted() {
		if o.Remote == id {
			return spec.Conflict("%s is already linked to %s (%s)", id, o.Alias, o.Name)
		}
	}
	back := NewBackend(cfg)
	r, err := back.GetRaw(id)
	if err != nil {
		return err
	}
	if r == nil {
		return spec.NotFound("no address-book card %s. Find one with: macos contacts list --matches-name <name> --no-unify", id)
	}
	if r.IsUnified {
		return spec.UserError("%s is a merged card; take one of its individual cards (contacts list --no-unify)", id)
	}
	container, err := back.Container(cfg.Contacts.Container)
	if err != nil {
		return err
	}
	if r.ContainerID != "" && r.ContainerID != container {
		return spec.UserError("%s is not in the %s address book; take its %s card (contacts list --no-unify --container %s)", id, cfg.Contacts.Container, cfg.Contacts.Container, container)
	}
	card := r.Card()
	if card.Kind == "organization" {
		if c.Kind == "" {
			c.Kind = store.Organisation
		}
		if c.Name == "" {
			c.Name = card.Org
		}
	} else {
		if c.Kind == "" {
			c.Kind = store.Person
		}
		if c.Name == "" {
			c.Name = strings.TrimSpace(card.Given + " " + card.Family)
			if c.Name == "" {
				c.Name = r.DisplayName
			}
			if len(strings.Fields(card.Family)) > 1 {
				c.Given, c.Family = card.Given, card.Family
			}
		}
		if c.Org == "" {
			c.Org = card.Org
		}
	}
	if len(c.Phones) == 0 {
		c.Phones = card.Phones
	}
	if len(c.Emails) == 0 {
		c.Emails = card.Emails
	}
	c.Remote = id
	return nil
}

// joinGroup adds (or, with join false, removes) a member to a group, in the group's store.
func joinGroup(ctx *spec.Context, cfg *config.Config, b *book.Book, group, member string, join bool) error {
	g := b.Lookup(group)
	if g == nil || g.Kind != store.Group {
		return spec.NotFound("no group %s", group)
	}
	group = g.Alias
	forms := []string{member}
	if mc := b.Lookup(member); mc != nil {
		member, forms = mc.Alias, mc.AllAliases()
	}
	s := b.Stores[g.Sphere]
	var saved *store.Contact
	err := s.WriteAs(func() (string, error) {
		cur, err := s.Get(group)
		if err != nil || cur == nil {
			return "", spec.NotFound("no group %s", group)
		}
		in := len(removeAll(cur.Members, forms)) < len(cur.Members)
		if in == join {
			return "", nil
		}
		verb := "added"
		if join {
			cur.Members = append(cur.Members, member)
		} else {
			cur.Members = removeAll(cur.Members, forms)
			verb = "removed"
		}
		s.Note(cur, "member "+verb+": "+member)
		if err := s.Save(cur); err != nil {
			return "", err
		}
		saved = cur
		return fmt.Sprintf("update %s: member %s %s", group, verb, member), nil
	})
	if err == nil && saved != nil {
		fire(ctx, cfg, s, saved, "updated", "")
	}
	return err
}

func removeAll(l, del []string) []string {
	var out []string
	for _, x := range l {
		if !contains(del, x) {
			out = append(out, x)
		}
	}
	return out
}

func addAll(l, add []string) []string {
	out := append([]string{}, l...)
	for _, x := range add {
		if !contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

func clearable(v string) string {
	if strings.EqualFold(v, "none") {
		return ""
	}
	return v
}

func runEdit(ctx *spec.Context) (any, error) {
	cfg, b, err := cfgAndBook(ctx)
	if err != nil {
		return nil, err
	}
	c, err := cardOf(ctx, b, ctx.Str("alias"))
	if err != nil {
		return nil, err
	}
	old := c.Alias
	primary := old
	if p := ctx.Str("primary"); p != "" {
		if primary, err = store.NormAlias(p); err != nil {
			return nil, err
		}
		if err := checkFree(ctx, b, c, primary, c.Name); err != nil {
			return nil, err
		}
	}
	addA, err := normAliases(ctx.List("add-alias"))
	if err != nil {
		return nil, err
	}
	for _, a := range addA {
		if err := checkFree(ctx, b, c, a, c.Name); err != nil {
			return nil, err
		}
	}
	rmA, err := normAliases(ctx.List("rm-alias"))
	if err != nil {
		return nil, err
	}
	if contains(rmA, primary) {
		return nil, spec.UserError("%s is the main alias of the card; make another one main first with --primary", primary)
	}
	others := removeAll(addAll(c.Aliases, addA), rmA)
	if primary != old {
		others = addAll(removeAll(others, []string{primary}), []string{old})
		others = removeAll(others, rmA)
	}
	addM, err := normAliases(ctx.List("add-member"))
	if err != nil {
		return nil, err
	}
	rmM, err := normAliases(ctx.List("rm-member"))
	if err != nil {
		return nil, err
	}
	var rmForms []string
	for _, m := range rmM {
		if mc := b.Lookup(m); mc != nil {
			rmForms = append(rmForms, mc.AllAliases()...)
		} else {
			rmForms = append(rmForms, m)
		}
	}
	addG, err := canonGroups(b, ctx.List("add-group"), true)
	if err != nil {
		return nil, err
	}
	rmG, err := canonGroups(b, ctx.List("rm-group"), false)
	if err != nil {
		return nil, err
	}
	kind := ctx.Str("kind")
	if kind != "" && kind != c.Kind {
		if c.Remote != "" {
			return nil, spec.Conflict("%s is linked to the address book, whose kind cannot change; unlink it first with --remote none", c.Alias)
		}
		if c.Kind == store.Group && len(c.Members) > 0 {
			return nil, spec.Conflict("%s still has members", c.Alias)
		}
	}
	effKind := c.Kind
	if kind != "" {
		effKind = kind
	}
	if len(addM) > 0 && effKind != store.Group {
		return nil, spec.UserError("%s is a %s; only a group has members", c.Alias, effKind)
	}
	if addM, err = checkMembers(b, c.Alias, addM); err != nil {
		return nil, err
	}
	for _, g := range addG {
		if b.WouldCycle(g, c.Alias) {
			return nil, spec.Conflict("%s in %s would make a cycle of groups", c.Alias, g)
		}
	}
	s := b.Stores[c.Sphere]
	var changed []string
	var saved *store.Contact
	err = s.WriteAs(func() (string, error) {
		cur, err := s.Get(old)
		if err != nil || cur == nil {
			return "", spec.NotFound("no contact %s", old)
		}
		set := func(name string, dst *string, v string, ok bool) {
			if ok && *dst != v {
				*dst = v
				changed = append(changed, name)
			}
		}
		setList := func(name string, dst *[]string, v []string) {
			if strings.Join(*dst, "\x00") != strings.Join(v, "\x00") {
				*dst = v
				changed = append(changed, name)
			}
		}
		_, hasName := ctx.Args["name"]
		set("name", &cur.Name, strings.TrimSpace(ctx.Str("name")), hasName && strings.TrimSpace(ctx.Str("name")) != "")
		_, ok := ctx.Args["given"]
		set("given", &cur.Given, clearable(ctx.Str("given")), ok)
		_, ok = ctx.Args["family"]
		set("family", &cur.Family, clearable(ctx.Str("family")), ok)
		_, ok = ctx.Args["org"]
		set("org", &cur.Org, clearable(ctx.Str("org")), ok)
		set("kind", &cur.Kind, kind, kind != "")
		_, ok = ctx.Args["remote"]
		set("remote", &cur.Remote, clearable(ctx.Str("remote")), ok)
		_, ok = ctx.Args["body"]
		set("notes", &cur.Body, ctx.Str("body"), ok)
		if a := strings.TrimSpace(ctx.Str("append")); a != "" {
			if cur.Body != "" {
				cur.Body = strings.TrimSpace(cur.Body) + "\n\n" + a
			} else {
				cur.Body = a
			}
			changed = append(changed, "notes")
		}
		phones := cur.Phones
		if _, ok := ctx.Args["phone"]; ok {
			phones = cleanList(ctx.List("phone"))
		}
		setList("phones", &cur.Phones, removeAll(addAll(phones, cleanList(ctx.List("add-phone"))), ctx.List("rm-phone")))
		emails := cur.Emails
		if _, ok := ctx.Args["email"]; ok {
			emails = cleanList(ctx.List("email"))
		}
		setList("emails", &cur.Emails, removeAll(addAll(emails, cleanList(ctx.List("add-email"))), ctx.List("rm-email")))
		setList("tags", &cur.Tags, removeAll(addAll(cur.Tags, cleanList(ctx.List("add-tag"))), ctx.List("rm-tag")))
		setList("refs", &cur.Refs, removeAll(addAll(cur.Refs, cleanList(ctx.List("add-ref"))), ctx.List("rm-ref")))
		setList("members", &cur.Members, removeAll(addAll(cur.Members, addM), rmForms))
		setList("aliases", &cur.Aliases, sortedCopy(others))
		renamed := primary != old
		if renamed {
			cur.Alias = primary
			changed = append(changed, "main alias")
		}
		if len(changed) == 0 {
			saved = cur
			return "", nil
		}
		what := "edited: " + strings.Join(changed, ", ")
		if renamed {
			what = "renamed " + old + " → " + primary
			if len(changed) > 1 {
				what += "; edited: " + strings.Join(changed[:len(changed)-1], ", ")
			}
		}
		s.Note(cur, what)
		if err := s.Save(cur); err != nil {
			return "", err
		}
		saved = cur
		if !renamed {
			return fmt.Sprintf("update %s: %s", cur.Alias, strings.Join(changed, ", ")), nil
		}
		if err := s.Remove(old); err != nil {
			return "", err
		}
		if _, err := renameInGroups(s, old, primary); err != nil {
			return "", err
		}
		return fmt.Sprintf("rename %s → %s", old, primary), nil
	})
	if err != nil {
		return nil, err
	}
	if primary != old {
		for n, st := range b.Stores {
			if n == c.Sphere {
				continue
			}
			if err := st.WriteAs(func() (string, error) {
				n, err := renameInGroups(st, old, primary)
				if err != nil || n == 0 {
					return "", err
				}
				return fmt.Sprintf("rename %s → %s", old, primary), nil
			}); err != nil {
				ctx.Warn(fmt.Sprintf("groups of %s still name %s: %v", n, old, err))
			}
		}
		c = saved
		b.Put(saved)
	}
	if len(changed) > 0 {
		fire(ctx, cfg, s, saved, "updated", "")
	}
	for _, g := range addG {
		if err := joinGroup(ctx, cfg, b, g, c.Alias, true); err != nil {
			return nil, err
		}
	}
	for _, g := range rmG {
		if err := joinGroup(ctx, cfg, b, g, c.Alias, false); err != nil {
			return nil, err
		}
	}
	if len(addG)+len(rmG) > 0 || primary != old {
		if b, err = LoadBook(ctx, cfg); err == nil && b.Cards[saved.Alias] != nil {
			saved = b.Cards[saved.Alias]
		}
	} else {
		saved.Groups = c.Groups
	}
	return saved, nil
}

// renameInGroups rewrites, in one store, the groups whose members name old; the caller holds the lock.
func renameInGroups(s *store.Store, old, now string) (int, error) {
	cs, err := s.List()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, g := range cs {
		if g.Kind != store.Group || !contains(g.Members, old) {
			continue
		}
		g.Members = addAll(removeAll(g.Members, []string{old}), []string{now})
		s.Note(g, "member renamed: "+old+" → "+now)
		if err := s.Save(g); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// canonGroups gives groups by their main alias; strict refuses one that is not a group.
func canonGroups(b *book.Book, l []string, strict bool) ([]string, error) {
	as, err := normAliases(l)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, a := range as {
		g := b.Lookup(a)
		if g == nil || g.Kind != store.Group {
			if strict {
				return nil, spec.NotFound("no group %s. List them with: contact ls --kind group", a)
			}
			out = append(out, a)
			continue
		}
		out = append(out, g.Alias)
	}
	return out, nil
}

func sortedCopy(l []string) []string {
	if len(l) == 0 {
		return nil
	}
	out := append([]string{}, l...)
	sort.Strings(out)
	return out
}

func runRm(ctx *spec.Context) (any, error) {
	cfg, b, err := cfgAndBook(ctx)
	if err != nil {
		return nil, err
	}
	c, err := cardOf(ctx, b, ctx.Str("alias"))
	if err != nil {
		return nil, err
	}
	if len(c.Groups) > 0 && !ctx.Bool("force") {
		return nil, spec.Conflict("%s is a member of %s; remove it there first, or pass --force", c.Alias, strings.Join(c.Groups, ", "))
	}
	for _, g := range c.Groups {
		if err := joinGroup(ctx, cfg, b, g, c.Alias, false); err != nil {
			return nil, err
		}
	}
	s := b.Stores[c.Sphere]
	if err := s.WriteAs(func() (string, error) {
		if err := s.Remove(c.Alias); err != nil {
			return "", err
		}
		return fmt.Sprintf("rm %s: %s", c.Alias, c.Name), nil
	}); err != nil {
		return nil, err
	}
	fire(ctx, cfg, s, c, "removed", "")
	return map[string]any{"removed": c.Alias, "sphere": c.Sphere}, nil
}

func runMv(ctx *spec.Context) (any, error) {
	cfg, b, err := cfgAndBook(ctx)
	if err != nil {
		return nil, err
	}
	c, err := cardOf(ctx, b, ctx.Str("alias"))
	if err != nil {
		return nil, err
	}
	to := ctx.Str("to")
	if err := checkSphere(ctx, cfg, to); err != nil {
		return nil, err
	}
	if to == c.Sphere {
		return c, nil
	}
	dst := b.Stores[to]
	if dst == nil {
		if dst, err = OpenStore(ctx, cfg, to); err != nil {
			return nil, err
		}
	}
	src, from := b.Stores[c.Sphere], c.Sphere
	if err := dst.WriteAs(func() (string, error) {
		dst.Note(c, "moved from "+from)
		if err := dst.Save(c); err != nil {
			return "", err
		}
		return fmt.Sprintf("mv %s: from %s", c.Alias, from), nil
	}); err != nil {
		return nil, err
	}
	if err := src.WriteAs(func() (string, error) {
		if err := src.Remove(c.Alias); err != nil {
			return "", err
		}
		return fmt.Sprintf("mv %s: to %s", c.Alias, to), nil
	}); err != nil {
		return nil, err
	}
	fire(ctx, cfg, dst, c, "moved", from)
	return c, nil
}

// ViewRow is one line of text, or one item of a JSON result, found under an alias.
type ViewRow struct {
	Alias string          `json:"alias"`
	Text  string          `json:"text,omitempty"`
	Item  json.RawMessage `json:"item,omitempty"`
}

// ViewResult is the output of a configured view, every alias merged.
type ViewResult struct {
	Label string    `json:"label"`
	Rows  []ViewRow `json:"rows"`
	Error string    `json:"error,omitempty"`
}

// viewOut is the output of one run of a view.
type viewOut struct {
	Result json.RawMessage
	Text   string
	Error  string
}

type Shown struct {
	Contact *store.Contact   `json:"contact"`
	Members []*store.Contact `json:"members,omitempty"`
	Views   []ViewResult     `json:"views,omitempty"`
}

func runShow(ctx *spec.Context) (any, error) {
	cfg, b, err := cfgAndBook(ctx)
	if err != nil {
		return nil, err
	}
	c, err := cardOf(ctx, b, ctx.Str("alias"))
	if err != nil {
		return nil, err
	}
	out := &Shown{Contact: c}
	if c.Kind == store.Group {
		for _, m := range c.Members {
			if mc := b.Lookup(m); mc != nil {
				out.Members = append(out.Members, mc)
			}
		}
	}
	if !ctx.Bool("no-views") {
		for _, v := range cfg.Views {
			r := mergeView(v, c.AllAliases())
			if r.Error != "" {
				ctx.Warn(fmt.Sprintf("view %s: %s", v.Label, r.Error))
			}
			out.Views = append(out.Views, r)
		}
	}
	return out, nil
}

// mergeView runs a view once per alias and keeps each row once, under the first alias that found it.
func mergeView(v config.View, aliases []string) ViewResult {
	r := ViewResult{Label: v.Label, Rows: []ViewRow{}}
	seen := map[string]bool{}
	var errs []string
	add := func(row ViewRow, key string) {
		if !seen[key] {
			seen[key] = true
			r.Rows = append(r.Rows, row)
		}
	}
	for _, a := range aliases {
		o := runView(v, a)
		switch {
		case o.Error != "":
			if len(aliases) > 1 {
				errs = append(errs, a+": "+o.Error)
			} else {
				errs = append(errs, o.Error)
			}
		case len(o.Result) > 0:
			var items []json.RawMessage
			if json.Unmarshal(o.Result, &items) != nil {
				items = []json.RawMessage{o.Result}
			}
			for _, it := range items {
				var buf bytes.Buffer
				if json.Compact(&buf, it) != nil {
					buf.Reset()
					buf.Write(it)
				}
				add(ViewRow{Alias: a, Item: json.RawMessage(buf.Bytes())}, "j"+buf.String())
			}
		default:
			for _, l := range strings.Split(o.Text, "\n") {
				if strings.TrimSpace(l) != "" {
					add(ViewRow{Alias: a, Text: l}, "t"+l)
				}
			}
		}
	}
	r.Error = strings.Join(errs, "; ")
	return r
}

func runView(v config.View, alias string) viewOut {
	var r viewOut
	timeout := 20 * time.Second
	if v.Timeout != "" {
		if d, err := time.ParseDuration(v.Timeout); err == nil {
			timeout = d
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	argv := make([]string, len(v.Run))
	for i, a := range v.Run {
		a = strings.ReplaceAll(a, "{ref}", "contact:"+alias)
		argv[i] = config.Expand(strings.ReplaceAll(a, "{alias}", alias))
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	err := cmd.Run()
	var env struct {
		OK     *bool           `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(out.Bytes(), &env) == nil && env.OK != nil {
		if !*env.OK {
			r.Error = "failed"
			if env.Error != nil {
				r.Error = env.Error.Message
			}
			return r
		}
		r.Result = env.Result
		return r
	}
	if err != nil {
		r.Error = strings.TrimSpace(err.Error() + ": " + errb.String())
		return r
	}
	r.Text = strings.TrimSpace(out.String())
	return r
}

func textShow(w io.Writer, res any) {
	s := res.(*Shown)
	c := s.Contact
	fmt.Fprintf(w, "%s · %s · %s (%s)\n", c.Alias, c.Name, c.Kind, c.Sphere)
	if len(c.Aliases) > 0 {
		fmt.Fprintf(w, "  %-9s %s\n", "aliases", strings.Join(c.Aliases, ", "))
	}
	line := func(label string, v string) {
		if v != "" {
			fmt.Fprintf(w, "  %-9s %s\n", label, v)
		}
	}
	line("org", c.Org)
	line("phones", strings.Join(c.Phones, " · "))
	line("emails", strings.Join(c.Emails, " · "))
	line("groups", strings.Join(c.Groups, ", "))
	line("tags", strings.Join(c.Tags, ", "))
	line("refs", strings.Join(c.Refs, ", "))
	line("remote", c.Remote)
	if len(s.Members) > 0 {
		fmt.Fprintln(w, "\nMembers:")
		for _, m := range s.Members {
			fmt.Fprintf(w, "  %-8s %s (%s)\n", m.Alias, m.Name, m.Kind)
		}
	}
	if c.Body != "" {
		fmt.Fprintf(w, "\n%s\n", c.Body)
	}
	for _, v := range s.Views {
		fmt.Fprintf(w, "\n%s:\n", v.Label)
		if v.Error != "" {
			fmt.Fprintf(w, "  (error: %s)\n", v.Error)
		}
		for _, row := range v.Rows {
			via := ""
			if len(c.Aliases) > 0 {
				via = "  [" + row.Alias + "]"
			}
			if row.Text != "" {
				fmt.Fprintf(w, "  %s%s\n", row.Text, via)
				continue
			}
			var pretty bytes.Buffer
			if json.Indent(&pretty, row.Item, "  ", "  ") == nil {
				fmt.Fprintf(w, "  %s%s\n", pretty.String(), via)
			}
		}
	}
}

func runLs(ctx *spec.Context) (any, error) {
	cfg, b, err := cfgAndBook(ctx)
	if err != nil {
		return nil, err
	}
	spheres, err := ReadSpheres(ctx, cfg)
	if err != nil {
		return nil, err
	}
	var in map[string]bool
	if g := ctx.Str("group"); g != "" {
		ms, err := b.Members(g, true)
		if err != nil {
			return nil, err
		}
		in = map[string]bool{}
		for _, m := range ms {
			in[m.Alias] = true
		}
	}
	kind, ref, tag, q := ctx.Str("kind"), ctx.Str("ref"), book.Fold(ctx.Str("tag")), book.Fold(ctx.Str("query"))
	out := []*store.Contact{}
	for _, c := range b.Sorted() {
		if !visible(c, spheres) || (kind != "" && c.Kind != kind) || (in != nil && !in[c.Alias]) {
			continue
		}
		if ref != "" && !containsFold(c.Refs, ref) {
			continue
		}
		if tag != "" && !containsFold(c.Tags, tag) {
			continue
		}
		if q != "" && !strings.Contains(book.Fold(strings.Join(c.AllAliases(), " ")+" "+c.Name+" "+c.Org), q) {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func containsFold(l []string, x string) bool {
	for _, y := range l {
		if book.Fold(y) == book.Fold(x) {
			return true
		}
	}
	return false
}

func textLs(w io.Writer, res any) {
	for _, c := range res.([]*store.Contact) {
		extra := c.Org
		if c.Kind == store.Group {
			extra = strings.Join(c.Members, ", ")
		}
		fmt.Fprintf(w, "%-8s %-12s %-6s %s", c.Alias, c.Kind, c.Sphere, c.Name)
		if len(c.Aliases) > 0 {
			fmt.Fprintf(w, " [%s]", strings.Join(c.Aliases, ", "))
		}
		if extra != "" {
			fmt.Fprintf(w, " · %s", extra)
		}
		fmt.Fprintln(w)
	}
}

func tsvLs(w io.Writer, res any) {
	clean := func(s string) string { return strings.NewReplacer("\t", " ", "\n", " ").Replace(s) }
	fmt.Fprintln(w, "alias\taliases\tkind\tsphere\tname\torg\ttags\temails\tphones\tgroups")
	for _, c := range res.([]*store.Contact) {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", c.Alias, strings.Join(c.Aliases, ","), c.Kind, c.Sphere, clean(c.Name), clean(c.Org),
			strings.Join(c.Tags, ","), clean(strings.Join(c.Emails, ",")), clean(strings.Join(c.Phones, ",")), strings.Join(c.Groups, ","))
	}
}

func runMembers(ctx *spec.Context) (any, error) {
	_, b, err := cfgAndBook(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := cardOf(ctx, b, ctx.Str("alias")); err != nil {
		return nil, err
	}
	ms, err := b.Members(ctx.Str("alias"), ctx.Bool("recursive"))
	if err != nil {
		return nil, err
	}
	if ms == nil {
		ms = []*store.Contact{}
	}
	return ms, nil
}

func runResolve(ctx *spec.Context) (any, error) {
	_, b, err := cfgAndBook(ctx)
	if err != nil {
		return nil, err
	}
	out := []book.Match{}
	for _, m := range b.Resolve(ctx.Str("query")) {
		if ctx.Spheres == nil || contains(ctx.Spheres, m.Sphere) {
			out = append(out, m)
		}
	}
	return out, nil
}
