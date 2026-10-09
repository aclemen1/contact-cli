package actions

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/aclemen1/contact-cli/internal/config"
	"github.com/aclemen1/contact-cli/internal/csync"
	"github.com/aclemen1/contact-cli/internal/hooks"
	"github.com/aclemen1/contact-cli/internal/spec"
)

func registerSync() {
	spec.Register(&spec.Action{
		Category: "sync", Name: "contacts",
		Summary: "Sync the cards with the address book, both ways: name, organisation, phones, e-mails, group members.",
		Discussion: "A field changed on one side goes to the other; changed on both, the card wins and the lost value is logged in it. " +
			"A card without remote is created in the address book (contacts.container, default iCloud); a group becomes a group there, a group in a group a subgroup. " +
			"Address-book entries are never imported: take one with add --from. Nothing is deleted in the address book. " +
			"Free notes, alias, kind, tags and refs live only in the card. Only the cards the address book reports changed are read again.",
		Params: []spec.Param{
			{Name: "dry-run", Kind: spec.Bool, Help: "Say what would change, change nothing."},
		},
		Effects: []string{"Writes the address book (macos contacts …, Pro tier).", "Rewrites the cards changed, one commit per sphere.",
			"Keeps the agreed values in ~/.local/state/contact/contacts.json and marks the address-book changes seen.", "Fires the synced hooks."},
		Examples: []string{"contact sync contacts --dry-run", "contact sync contacts"},
		Run: func(ctx *spec.Context) (any, error) {
			cfg, err := config.Load(ctx.Config)
			if err != nil {
				return nil, err
			}
			stores, err := OpenAll(ctx, cfg)
			if err != nil {
				return nil, err
			}
			y := &csync.Sync{Stores: stores, Back: NewBackend(cfg), State: filepath.Join(config.StateDir(), "contacts.json"),
				Container: cfg.Contacts.Container, Client: cfg.Contacts.Client, DryRun: ctx.Bool("dry-run"),
				Fire: func(e csync.Event) {
					hooks.Fire(cfg, hooks.Event{Event: "synced", Sphere: e.Store.Sphere, By: e.Store.By, Contact: e.Contact}, ctx.Warn)
				}}
			rep, err := y.Run()
			if err != nil {
				return rep, err
			}
			for _, e := range rep.Errors {
				ctx.Warn(e)
			}
			return rep, nil
		},
		Text: func(w io.Writer, r any) {
			rep := r.(*csync.Report)
			for _, c := range rep.Changes {
				fmt.Fprintf(w, "%-8s %s", c.Alias, c.Action)
				if c.Detail != "" {
					fmt.Fprintf(w, " (%s)", c.Detail)
				}
				fmt.Fprintln(w)
			}
			prefix := ""
			if rep.DryRun {
				prefix = "dry run: "
			}
			fmt.Fprintf(w, "%s%d change(s), %d linked, %d read\n", prefix, len(rep.Changes), rep.Linked, rep.Read)
		},
	})
}
