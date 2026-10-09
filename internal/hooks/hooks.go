// Package hooks runs the commands of the configuration on the events of a
// card, so that other tools follow it without contact knowing them.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aclemen1/contact-cli/internal/config"
	"github.com/aclemen1/contact-cli/internal/store"
)

// Event is what a hook reads on its standard input.
type Event struct {
	Event string `json:"event"`
	// Sphere is the card's sphere; for moved, PreviousSphere is the one it left.
	Sphere         string         `json:"sphere"`
	PreviousSphere string         `json:"previous_sphere,omitempty"`
	By             string         `json:"by"`
	Contact        *store.Contact `json:"contact"`
}

func matches(h config.Hook, e Event) bool {
	if len(h.Events) > 0 {
		ok := false
		for _, x := range h.Events {
			if x == e.Event {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	if h.Kind != "" && h.Kind != e.Contact.Kind {
		return false
	}
	if h.Sphere != "" && h.Sphere != e.Sphere {
		return false
	}
	return true
}

// Fire runs every matching hook, one after the other; a failure is a warning, never an error of the action.
func Fire(cfg *config.Config, e Event, warn func(string)) {
	for _, h := range cfg.Hooks {
		if !matches(h, e) || len(h.Run) == 0 {
			continue
		}
		if err := run(h, e); err != nil && warn != nil {
			warn(fmt.Sprintf("hook %s on %s %s: %v", h.Run[0], e.Event, e.Contact.Alias, err))
		}
	}
}

func run(h config.Hook, e Event) error {
	timeout := 30 * time.Second
	if h.Timeout != "" {
		d, err := time.ParseDuration(h.Timeout)
		if err != nil {
			return fmt.Errorf("timeout %q: %v", h.Timeout, err)
		}
		timeout = d
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	argv := make([]string, len(h.Run))
	for i, a := range h.Run {
		argv[i] = config.Expand(a)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	in, _ := json.Marshal(e)
	cmd.Stdin = bytes.NewReader(in)
	cmd.Env = append(os.Environ(),
		"CONTACT_EVENT="+e.Event, "CONTACT_ALIAS="+e.Contact.Alias, "CONTACT_SPHERE="+e.Sphere,
		"CONTACT_KIND="+e.Contact.Kind, "CONTACT_REF=contact:"+e.Contact.Alias)
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
