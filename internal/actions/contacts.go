package actions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
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
			{Name: "alias", Kind: spec.String, Help: "Short alias in capitals, e.g. JMR. Refused when any sphere has it."},
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
		Examples: []string{`contact add "Jean-Moïse Rochat" --sphere pro --alias JMR --email jmr@unil.ch`, `contact add Famille --kind group --sphere perso --member AC --member EVE`, `contact add --from 45612C46-72AC-4BBF-9C72-8BB56BE8EC6F:ABPerson --sphere perso --alias AC`},
		Run:      runAdd,
	})
	spec.Register(&spec.Action{
		Category: "contact", Name: "edit", Top: true,
		Summary: "Change a card: name, organisation, phones, e-mails, tags, refs, members, groups, notes.",
		Discussion: "Lists change element by element with --add-… and --rm-…; --phone and --email replace the whole list. " +
			"--remote links the card to an address-book id, none unlinks it.",
		Params: []spec.Param{
			{Name: "alias", Kind: spec.String, Positional: true, Required: true, Help: "Alias of the card."},
			{Name: "name", Kind: spec.String, Help: "New full name."},
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
		Effects:  []string{"Rewrites the card and commits it; joining or leaving a group rewrites the group.", "Fires the updated hooks."},
		Examples: []string{"contact edit JMR --add-phone mobile:+41 79 000 00 00", "contact edit FAM --add-member TAC", "contact edit JC --add-tag jeannette --add-group FAM"},
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
		Discussion: "The views come from the configuration: each runs a command with {ref} replaced by contact:<alias> and {alias} by the alias; " +
			"a failing view is a warning. A group shows its members.",
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

func checkMembers(b *book.Book, group string, members []string) error {
	for _, m := range members {
		if b.Cards[m] == nil {
			return spec.NotFound("no contact %s to add to %s. Create it first: contact add <name> --alias %s --sphere <sphere>", m, group, m)
		}
		if b.WouldCycle(group, m) {
			return spec.Conflict("%s in %s would make a cycle of groups", m, group)
		}
	}
	return nil
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
		if old := b.Cards[c.Alias]; old != nil {
			if ctx.Spheres != nil && !contains(ctx.Spheres, old.Sphere) {
				return nil, spec.Conflict("alias %s is taken in another sphere. Free: %s", c.Alias, b.Suggest(c.Name))
			}
			return nil, spec.Conflict("alias %s is taken by %s (%s). Free: %s", c.Alias, old.Name, old.Sphere, b.Suggest(c.Name))
		}
	} else {
		c.Alias = b.Suggest(c.Name)
	}
	members, err := normAliases(ctx.List("member"))
	if err != nil {
		return nil, err
	}
	if len(members) > 0 && c.Kind != store.Group {
		return nil, spec.UserError("--member needs --kind group")
	}
	b.Cards[c.Alias] = c
	if err := checkMembers(b, c.Alias, members); err != nil {
		return nil, err
	}
	c.Members = members
	groups, err := normAliases(ctx.List("group"))
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		gc := b.Cards[g]
		if gc == nil || gc.Kind != store.Group {
			return nil, spec.NotFound("no group %s. List them with: contact ls --kind group", g)
		}
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
	for _, o := range b.Cards {
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
	g := b.Cards[group]
	if g == nil || g.Kind != store.Group {
		return spec.NotFound("no group %s", group)
	}
	s := b.Stores[g.Sphere]
	var saved *store.Contact
	err := s.WriteAs(func() (string, error) {
		cur, err := s.Get(group)
		if err != nil || cur == nil {
			return "", spec.NotFound("no group %s", group)
		}
		if contains(cur.Members, member) == join {
			return "", nil
		}
		verb := "added"
		if join {
			cur.Members = append(cur.Members, member)
		} else {
			cur.Members = removeAll(cur.Members, []string{member})
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
	addM, err := normAliases(ctx.List("add-member"))
	if err != nil {
		return nil, err
	}
	rmM, err := normAliases(ctx.List("rm-member"))
	if err != nil {
		return nil, err
	}
	addG, err := normAliases(ctx.List("add-group"))
	if err != nil {
		return nil, err
	}
	rmG, err := normAliases(ctx.List("rm-group"))
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
	if err := checkMembers(b, c.Alias, addM); err != nil {
		return nil, err
	}
	for _, g := range addG {
		gc := b.Cards[g]
		if gc == nil || gc.Kind != store.Group {
			return nil, spec.NotFound("no group %s. List them with: contact ls --kind group", g)
		}
		if b.WouldCycle(g, c.Alias) {
			return nil, spec.Conflict("%s in %s would make a cycle of groups", c.Alias, g)
		}
	}
	s := b.Stores[c.Sphere]
	var changed []string
	var saved *store.Contact
	err = s.WriteAs(func() (string, error) {
		cur, err := s.Get(c.Alias)
		if err != nil || cur == nil {
			return "", spec.NotFound("no contact %s", c.Alias)
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
		setList("members", &cur.Members, removeAll(addAll(cur.Members, addM), rmM))
		if len(changed) == 0 {
			saved = cur
			return "", nil
		}
		s.Note(cur, "edited: "+strings.Join(changed, ", "))
		if err := s.Save(cur); err != nil {
			return "", err
		}
		saved = cur
		return fmt.Sprintf("update %s: %s", cur.Alias, strings.Join(changed, ", ")), nil
	})
	if err != nil {
		return nil, err
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
	if len(addG)+len(rmG) > 0 {
		if b, err = LoadBook(ctx, cfg); err == nil {
			saved = b.Cards[c.Alias]
		}
	} else {
		saved.Groups = c.Groups
	}
	return saved, nil
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

// ViewResult is the output of a configured view.
type ViewResult struct {
	Label  string          `json:"label"`
	Result json.RawMessage `json:"result,omitempty"`
	Text   string          `json:"text,omitempty"`
	Error  string          `json:"error,omitempty"`
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
			if mc := b.Cards[m]; mc != nil {
				out.Members = append(out.Members, mc)
			}
		}
	}
	if !ctx.Bool("no-views") {
		for _, v := range cfg.Views {
			r := runView(v, c.Alias)
			if r.Error != "" {
				ctx.Warn(fmt.Sprintf("view %s: %s", v.Label, r.Error))
			}
			out.Views = append(out.Views, r)
		}
	}
	return out, nil
}

func runView(v config.View, alias string) ViewResult {
	r := ViewResult{Label: v.Label}
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
		switch {
		case v.Error != "":
			fmt.Fprintf(w, "  (error: %s)\n", v.Error)
		case v.Text != "":
			for _, l := range strings.Split(v.Text, "\n") {
				fmt.Fprintf(w, "  %s\n", l)
			}
		case len(v.Result) > 0:
			var pretty bytes.Buffer
			if json.Indent(&pretty, v.Result, "  ", "  ") == nil {
				fmt.Fprintf(w, "  %s\n", pretty.String())
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
		if q != "" && !strings.Contains(book.Fold(c.Alias+" "+c.Name+" "+c.Org), q) {
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
		if extra != "" {
			fmt.Fprintf(w, " · %s", extra)
		}
		fmt.Fprintln(w)
	}
}

func tsvLs(w io.Writer, res any) {
	clean := func(s string) string { return strings.NewReplacer("\t", " ", "\n", " ").Replace(s) }
	fmt.Fprintln(w, "alias\tkind\tsphere\tname\torg\ttags\temails\tphones\tgroups")
	for _, c := range res.([]*store.Contact) {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", c.Alias, c.Kind, c.Sphere, clean(c.Name), clean(c.Org),
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
