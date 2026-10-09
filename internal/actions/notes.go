package actions

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/aclemen1/contact-cli/internal/config"
	"github.com/aclemen1/contact-cli/internal/spec"
	"github.com/aclemen1/contact-cli/internal/store"
)

// NoteEntry is a note of a card, as notes.ls gives it.
type NoteEntry struct {
	ID      string `json:"id"`
	Created string `json:"created"`
	By      string `json:"by,omitempty"`
	Body    string `json:"body"`
}

// fill replaces {ref}, {sphere} and {by} in argv.
func fill(argv []string, vars map[string]string) []string {
	out := make([]string, len(argv))
	for i, a := range argv {
		a = config.Expand(a)
		for k, v := range vars {
			a = strings.ReplaceAll(a, "{"+k+"}", v)
		}
		out[i] = a
	}
	return out
}

func runCmd(argv []string, stdin string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return nil, fmt.Errorf("%s: %v: %s", argv[0], err, msg)
	}
	return out.Bytes(), nil
}

// NotesOf reads the notes of a card under each of its aliases by notes.ls,
// newest first; nil without notes.ls.
func NotesOf(cfg *config.Config, c *store.Contact) ([]NoteEntry, error) {
	if len(cfg.Notes.Ls) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	var all []NoteEntry
	for _, a := range c.AllAliases() {
		out, err := runCmd(fill(cfg.Notes.Ls, map[string]string{"ref": "contact:" + a, "sphere": c.Sphere}), "", 20*time.Second)
		if err != nil {
			return nil, err
		}
		list, err := parseNotes(out)
		if err != nil {
			return nil, err
		}
		for _, n := range list {
			if !seen[n.ID] {
				seen[n.ID] = true
				all = append(all, n)
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Created > all[j].Created })
	return all, nil
}

func parseNotes(out []byte) ([]NoteEntry, error) {
	var env struct {
		Result json.RawMessage `json:"result"`
	}
	raw := json.RawMessage(out)
	if json.Unmarshal(out, &env) == nil && len(env.Result) > 0 {
		raw = env.Result
	}
	var list []NoteEntry
	if err := json.Unmarshal(raw, &list); err != nil {
		var wrap struct {
			Items []NoteEntry `json:"items"`
			Notes []NoteEntry `json:"notes"`
		}
		if json.Unmarshal(raw, &wrap) != nil {
			return nil, fmt.Errorf("notes.ls: unreadable answer: %.200s", out)
		}
		list = append(wrap.Items, wrap.Notes...)
	}
	return list, nil
}

func init() {
	spec.Register(&spec.Action{
		Category: "contact", Name: "note", Top: true,
		Summary:    "Add a note to a card: a dated entry of its follow-up, kept by the note tool of the config.",
		Discussion: "Runs notes.add of the config with the text on stdin, {ref} = contact:<main alias> and {sphere} = the card's sphere. The body of the card stays its free notes; notes are its journal, shown by contact show.",
		Params: []spec.Param{
			{Name: "alias", Kind: spec.String, Positional: true, Required: true, Help: "Alias of the card, with or without contact:."},
			{Name: "text", Kind: spec.String, Positional: true, Required: true, Help: "Text of the note; - reads stdin."},
		},
		Effects:  []string{"Runs notes.add; the card itself does not change."},
		Examples: []string{`contact note JMR "Préfère être appelé le matin"`},
		Run: func(ctx *spec.Context) (any, error) {
			cfg, b, err := cfgAndBook(ctx)
			if err != nil {
				return nil, err
			}
			if len(cfg.Notes.Add) == 0 {
				return nil, spec.UserError("no notes.add in the config: write in the body with contact edit --append")
			}
			c, err := cardOf(ctx, b, ctx.Str("alias"))
			if err != nil {
				return nil, err
			}
			text := ctx.Str("text")
			if text == "-" && ctx.Stdin != nil {
				bs, _ := io.ReadAll(ctx.Stdin)
				text = string(bs)
			}
			if strings.TrimSpace(text) == "" {
				return nil, spec.UserError("the note is empty")
			}
			s, err := OpenStore(ctx, cfg, c.Sphere)
			if err != nil {
				return nil, err
			}
			out, err := runCmd(fill(cfg.Notes.Add, map[string]string{"ref": "contact:" + c.Alias, "sphere": c.Sphere, "by": s.By}), text, 30*time.Second)
			if err != nil {
				return nil, spec.Internal(err)
			}
			var env struct {
				Result struct {
					ID string `json:"id"`
				} `json:"result"`
			}
			_ = json.Unmarshal(out, &env)
			return map[string]string{"contact": c.Alias, "note": env.Result.ID}, nil
		},
	})
}
