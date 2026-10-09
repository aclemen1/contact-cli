package mcpserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/aclemen1/contact-cli/internal/config"
	"github.com/aclemen1/contact-cli/internal/store"
)

func TestServeOnlyGivenSpheres(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg, _ := config.Load(cfgPath)
	for _, name := range []string{"perso", "pro"} {
		root := filepath.Join(dir, name)
		if err := store.Init(root, "none"); err != nil {
			t.Fatal(err)
		}
		if err := cfg.AddSphere(name, config.Sphere{Root: root, VCS: "none"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := (&store.Store{Sphere: "pro", Root: filepath.Join(dir, "pro")}).Save(&store.Contact{Alias: "JMR", Kind: store.Person, Name: "Jean-Moïse Rochat"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := New(cfgPath, []string{"perso"}).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
	}
	for _, want := range []string{"contact_add", "contact_ls", "contact_show", "contact_resolve", "contact_members"} {
		if !names[want] {
			t.Errorf("missing tool %s", want)
		}
	}
	if names["setup_init"] || names["sync_contacts"] || names["meta_schema"] {
		t.Error("setup, sync and meta actions must not be served")
	}

	call := func(name string, args map[string]any) (bool, map[string]any, string) {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		text := res.Content[0].(*mcp.TextContent).Text
		var env map[string]any
		_ = json.Unmarshal([]byte(text), &env)
		return res.IsError, env, text
	}
	if isErr, env, _ := call("contact_add", map[string]any{"name": "Eve Clément", "sphere": "perso"}); isErr {
		t.Fatalf("add: %v", env)
	} else if r := env["result"].(map[string]any); r["alias"] != "EC" || !strings.HasPrefix(r["log"].([]any)[0].(map[string]any)["by"].(string), "agent:") {
		t.Fatalf("add: %v", r)
	}
	if isErr, _, _ := call("contact_add", map[string]any{"name": "x", "sphere": "pro"}); !isErr {
		t.Fatal("a sphere not served must be refused")
	}
	if isErr, _, text := call("contact_add", map[string]any{"name": "Jean-Marc Rey", "sphere": "perso", "alias": "JMR"}); !isErr || strings.Contains(text, "Rochat") {
		t.Fatalf("an alias of another sphere is taken, without naming its card: %s", text)
	}
	if isErr, env, _ := call("contact_ls", map[string]any{}); isErr || len(env["result"].([]any)) != 1 {
		t.Fatalf("ls shows only the served sphere: %v", env)
	}
	if isErr, _, _ := call("contact_show", map[string]any{"alias": "JMR"}); !isErr {
		t.Fatal("a card of a sphere not served must stay hidden")
	}
}
