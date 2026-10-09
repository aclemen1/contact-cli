// Package actions declares every contact action once; the CLI, the schema and
// the MCP server are projections of these declarations.
package actions

import (
	"os"
	"strings"
	"time"

	"github.com/aclemen1/contact-cli/internal/book"
	"github.com/aclemen1/contact-cli/internal/config"
	"github.com/aclemen1/contact-cli/internal/hooks"
	"github.com/aclemen1/contact-cli/internal/spec"
	"github.com/aclemen1/contact-cli/internal/store"
)

const Version = "0.1.0"

// clock replaces the real clock in tests.
var clock func() time.Time

// SetClock fixes the time the actions see; nil restores the real clock.
func SetClock(f func() time.Time) { clock = f }

func Now() time.Time {
	if clock != nil {
		return clock()
	}
	return time.Now()
}

func readSphereParam() spec.Param {
	return spec.Param{Name: "sphere", Kind: spec.String, Help: "Read only this sphere, e.g. pro. Defaults to $CONTACT_SPHERE, else every sphere."}
}

func checkSphere(ctx *spec.Context, cfg *config.Config, sphere string) error {
	if ctx.Spheres != nil && !contains(ctx.Spheres, sphere) {
		return spec.Forbidden("sphere %q is not served here; served: %s", sphere, strings.Join(ctx.Spheres, ", "))
	}
	if _, ok := cfg.Spheres[sphere]; !ok {
		return spec.UserError("unknown sphere %q; configured: %s. Example: contact init --sphere perso --root ~/contact/perso", sphere, strings.Join(cfg.Names(), ", "))
	}
	return nil
}

// ReadSpheres are the spheres a read covers: the one given, else $CONTACT_SPHERE
// on the command line, else every sphere configured or served.
func ReadSpheres(ctx *spec.Context, cfg *config.Config) ([]string, error) {
	sphere := ctx.Str("sphere")
	if sphere == "" && ctx.Spheres == nil {
		sphere = os.Getenv("CONTACT_SPHERE")
	}
	if sphere != "" {
		return []string{sphere}, checkSphere(ctx, cfg, sphere)
	}
	if ctx.Spheres != nil {
		return ctx.Spheres, nil
	}
	if len(cfg.Spheres) == 0 {
		return nil, spec.UserError("no sphere is configured. Example: contact init --sphere perso --root ~/contact/perso")
	}
	return cfg.Names(), nil
}

// OpenStore opens a sphere's store with the call's author and clock.
func OpenStore(ctx *spec.Context, cfg *config.Config, sphere string) (*store.Store, error) {
	by := os.Getenv("CONTACT_BY")
	if ctx != nil && ctx.Spheres != nil && !strings.HasPrefix(by, "agent:") {
		by = "agent:mcp"
	}
	s, err := store.OpenStore(cfg, sphere, by)
	if err != nil {
		return nil, err
	}
	s.Now = Now
	if ctx != nil {
		s.Warn = ctx.Warn
	}
	return s, nil
}

// OpenAll opens every configured sphere whose store exists: aliases are unique across all of them.
func OpenAll(ctx *spec.Context, cfg *config.Config) (map[string]*store.Store, error) {
	out := map[string]*store.Store{}
	for _, n := range cfg.Names() {
		if _, err := os.Stat(cfg.Spheres[n].Root); err != nil {
			continue
		}
		s, err := OpenStore(ctx, cfg, n)
		if err != nil {
			return nil, err
		}
		out[n] = s
	}
	if len(out) == 0 {
		return nil, spec.UserError("no sphere is configured. Example: contact init --sphere perso --root ~/contact/perso")
	}
	return out, nil
}

// LoadBook reads every sphere; the cards a call may see are filtered by Visible.
func LoadBook(ctx *spec.Context, cfg *config.Config) (*book.Book, error) {
	stores, err := OpenAll(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return book.Load(stores, ctx.Warn)
}

// visible says whether a card's sphere is among those read.
func visible(c *store.Contact, spheres []string) bool { return contains(spheres, c.Sphere) }

func fire(ctx *spec.Context, cfg *config.Config, s *store.Store, c *store.Contact, event, prev string) {
	hooks.Fire(cfg, hooks.Event{Event: event, Sphere: s.Sphere, PreviousSphere: prev, By: s.By, Contact: c}, ctx.Warn)
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func init() {
	registerContacts()
	registerSync()
	registerMeta()
}

func registerMeta() {
	spec.Register(&spec.Action{
		Category: "setup", Name: "init", Top: true,
		Summary: "Declare a sphere and create its store of cards.",
		Params: []spec.Param{
			{Name: "sphere", Kind: spec.String, Required: true, Help: "Sphere name, e.g. perso."},
			{Name: "root", Kind: spec.String, Required: true, Help: "Directory of the sphere's cards, e.g. ~/contact/perso."},
			{Name: "vcs", Kind: spec.String, Default: "jj", Enum: []string{"jj", "git", "none"}, Help: "Version control of the store: a commit after each change."},
		},
		Effects:  []string{"Adds the sphere to the configuration file.", "Creates the store directory and, with jj or git, its repository."},
		Examples: []string{"contact init --sphere perso --root ~/contact/perso", "contact init --sphere pro --root ~/contact/pro"},
		Run: func(ctx *spec.Context) (any, error) {
			cfg, err := config.Load(ctx.Config)
			if err != nil {
				return nil, err
			}
			name, root, vcs := ctx.Str("sphere"), config.Expand(ctx.Str("root")), ctx.Str("vcs")
			if old, ok := cfg.Spheres[name]; ok && old.Root != root {
				return nil, spec.Conflict("sphere %s already has its cards at %s", name, old.Root)
			}
			if err := store.Init(root, vcs); err != nil {
				return nil, err
			}
			if err := cfg.AddSphere(name, config.Sphere{Root: root, VCS: vcs}); err != nil {
				return nil, spec.UserError("%v", err)
			}
			return map[string]any{"sphere": name, "root": root, "vcs": vcs, "config": cfg.File()}, nil
		},
	})
	spec.Register(&spec.Action{
		Category: "meta", Name: "version", Top: true, Meta: true, Summary: "Print the contact version.",
		Examples: []string{"contact version"},
		Run:      func(*spec.Context) (any, error) { return Version, nil },
	})
	spec.Register(&spec.Action{
		Category: "meta", Name: "schema", Top: true, Meta: true,
		Summary: "Browse actions: catalog, category, or one action's full spec.",
		Params: []spec.Param{
			{Name: "category", Kind: spec.String, Positional: true, Help: "Category to list."},
			{Name: "action", Kind: spec.String, Positional: true, Help: "Action to describe."},
			{Name: "search", Kind: spec.String, Help: "Match actions across categories."},
		},
		Examples: []string{"contact schema", "contact schema contact", "contact schema contact add", "contact schema --search group"},
		Run: func(ctx *spec.Context) (any, error) {
			if q := ctx.Str("search"); q != "" {
				return spec.Search(q), nil
			}
			cat, act := ctx.Str("category"), ctx.Str("action")
			switch {
			case cat == "":
				return spec.Catalog(), nil
			case act == "":
				l := spec.ActionsIn(cat)
				if len(l) == 0 {
					return nil, spec.NotFound("no category %q. Categories: %s", cat, strings.Join(spec.Categories(), ", "))
				}
				return l, nil
			}
			a := spec.Find(cat, act)
			if a == nil {
				return nil, spec.NotFound("no action %q in %q. Try `contact schema %s`", act, cat, cat)
			}
			return spec.Leaf{Action: a, Usage: spec.Usage(a)}, nil
		},
		Text: spec.TextSchema,
	})
}
