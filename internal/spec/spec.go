// Package spec holds the single source of truth for every action: the CLI,
// the schema introspection and the MCP server are projections of it.
package spec

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

type ParamKind string

const (
	String     ParamKind = "string"
	Bool       ParamKind = "boolean"
	StringList ParamKind = "string[]"
)

type Param struct {
	Name       string    `json:"name"`
	Kind       ParamKind `json:"type"`
	Positional bool      `json:"positional,omitempty"`
	Required   bool      `json:"required,omitempty"`
	Default    string    `json:"default,omitempty"`
	Enum       []string  `json:"enum,omitempty"`
	Help       string    `json:"description"`
	// Missing replaces the error of a required param left out.
	Missing string `json:"-"`
	// Hidden aliases absorbed on input, never shown in help or schema.
	Aliases []string `json:"-"`
}

type Action struct {
	Category    string   `json:"category"`
	Name        string   `json:"name"`
	Summary     string   `json:"summary"`
	Discussion  string   `json:"discussion,omitempty"`
	Params      []Param  `json:"params"`
	Examples    []string `json:"examples"`
	Effects     []string `json:"effects"`
	Destructive bool     `json:"destructive"`
	// Top actions are called `contact <name>`; the others `contact <category> <name>`.
	Top bool `json:"-"`
	// Meta actions default to text output; action commands default to JSON.
	Meta bool `json:"-"`
	// Run returns the result payload. A nil result yields {"ok": true}.
	Run func(ctx *Context) (any, error) `json:"-"`
	// Text renders the result for --format text. Nil falls back to indented JSON.
	Text func(w io.Writer, result any) `json:"-"`
	// TSV renders the result for --format tsv: one record per line, a header first.
	TSV func(w io.Writer, result any) `json:"-"`
}

// Command is the CLI words that call the action.
func (a *Action) Command() string {
	if a.Top {
		return "contact " + a.Name
	}
	return "contact " + a.Category + " " + a.Name
}

type Context struct {
	Args   map[string]any
	Config string // value of --config, possibly empty
	Format string
	Stdin  io.Reader
	Stdout io.Writer
	// Spheres limits a call that comes through MCP. Nil: every configured sphere.
	Spheres []string
	// Warnings: what went wrong after the action succeeded (a hook, a commit).
	Warnings []string
}

func (c *Context) Warn(m string) { c.Warnings = append(c.Warnings, m) }

func (c *Context) Str(name string) string {
	if v, ok := c.Args[name].(string); ok {
		return v
	}
	return ""
}

func (c *Context) Bool(name string) bool {
	v, _ := c.Args[name].(bool)
	return v
}

func (c *Context) List(name string) []string {
	v, _ := c.Args[name].([]string)
	return v
}

// Error is the structured error carried in the envelope.
type Error struct {
	Code    int    `json:"code"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

const (
	ExitError     = 1
	ExitUsage     = 2
	ExitNotFound  = 3
	ExitConflict  = 4
	ExitLocked    = 5
	ExitForbidden = 6
)

func UserError(format string, a ...any) *Error {
	return &Error{Code: ExitUsage, Kind: "user_error", Message: fmt.Sprintf(format, a...)}
}

func NotFound(format string, a ...any) *Error {
	return &Error{Code: ExitNotFound, Kind: "not_found", Message: fmt.Sprintf(format, a...)}
}

// Conflict is an action refused in the current state of its object.
func Conflict(format string, a ...any) *Error {
	return &Error{Code: ExitConflict, Kind: "conflict", Message: fmt.Sprintf(format, a...)}
}

func Locked(format string, a ...any) *Error {
	return &Error{Code: ExitLocked, Kind: "locked", Message: fmt.Sprintf(format, a...)}
}

func Forbidden(format string, a ...any) *Error {
	return &Error{Code: ExitForbidden, Kind: "forbidden", Message: fmt.Sprintf(format, a...)}
}

func Internal(err error) *Error {
	if e, ok := err.(*Error); ok {
		return e
	}
	return &Error{Code: ExitError, Kind: "error", Message: err.Error()}
}

var registry []*Action

func Register(a *Action) { registry = append(registry, a) }

func All() []*Action { return registry }

func Find(category, name string) *Action {
	for _, a := range registry {
		if a.Category == category && a.Name == name {
			return a
		}
	}
	return nil
}

// Resolve finds the action named by the first CLI words and returns the rest.
func Resolve(words []string) (*Action, []string) {
	if len(words) == 0 {
		return nil, nil
	}
	if len(words) > 1 {
		if a := Find(words[0], words[1]); a != nil && !a.Top {
			return a, words[2:]
		}
	}
	for _, a := range registry {
		if a.Top && a.Name == words[0] {
			return a, words[1:]
		}
	}
	return nil, nil
}

func IsCategory(name string) bool {
	for _, a := range registry {
		if a.Category == name && !a.Top {
			return true
		}
	}
	return false
}

func Categories() []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range registry {
		if !seen[a.Category] {
			seen[a.Category] = true
			out = append(out, a.Category)
		}
	}
	sort.Strings(out)
	return out
}

// Parse maps argv onto the action's params. Global flags are handled by the caller.
func Parse(a *Action, argv []string) (map[string]any, error) {
	out := map[string]any{}
	byName := map[string]*Param{}
	var positionals []*Param
	for i := range a.Params {
		p := &a.Params[i]
		byName[p.Name] = p
		for _, al := range p.Aliases {
			byName[al] = p
		}
		if p.Positional {
			positionals = append(positionals, p)
		}
	}
	pos := 0
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if strings.HasPrefix(arg, "--") && len(arg) > 2 {
			name, value, hasValue := strings.Cut(arg[2:], "=")
			p, ok := byName[name]
			if !ok {
				return nil, UserError("unknown option --%s for `%s`. Usage: %s", name, a.Command(), Usage(a))
			}
			if p.Kind == Bool {
				out[p.Name] = !hasValue || value == "true"
				continue
			}
			if !hasValue {
				if i+1 >= len(argv) {
					return nil, UserError("option --%s needs a value. Example: %s", name, firstExample(a))
				}
				i++
				value = argv[i]
			}
			if err := checkEnum(a, p, value); err != nil {
				return nil, err
			}
			if p.Kind == StringList {
				out[p.Name] = append(asList(out[p.Name]), value)
			} else {
				out[p.Name] = value
			}
			continue
		}
		if pos >= len(positionals) {
			return nil, UserError("unexpected argument %q for `%s`. Usage: %s", arg, a.Command(), Usage(a))
		}
		p := positionals[pos]
		if err := checkEnum(a, p, arg); err != nil {
			return nil, err
		}
		if p.Kind == StringList {
			out[p.Name] = append(asList(out[p.Name]), arg)
			continue
		}
		out[p.Name] = arg
		pos++
	}
	return withDefaults(a, out)
}

func withDefaults(a *Action, out map[string]any) (map[string]any, error) {
	for _, p := range a.Params {
		if _, set := out[p.Name]; set {
			continue
		}
		if p.Required && p.Missing != "" {
			return nil, UserError("%s. Example: %s", p.Missing, firstExample(a))
		}
		if p.Required {
			return nil, UserError("missing %s for `%s`. Example: %s", describeParam(p), a.Command(), firstExample(a))
		}
		if p.Default != "" {
			out[p.Name] = p.Default
		}
	}
	return out, nil
}

func asList(v any) []string {
	l, _ := v.([]string)
	return l
}

func checkEnum(a *Action, p *Param, value string) error {
	if len(p.Enum) == 0 {
		return nil
	}
	for _, e := range p.Enum {
		if e == value {
			return nil
		}
	}
	return UserError("%s got %q; expected one of %s. Example: %s", describeParam(*p), value, strings.Join(p.Enum, "|"), firstExample(a))
}

func describeParam(p Param) string {
	if p.Positional {
		return "<" + p.Name + ">"
	}
	return "--" + p.Name
}

func firstExample(a *Action) string {
	if len(a.Examples) > 0 {
		return a.Examples[0]
	}
	return Usage(a)
}

func Usage(a *Action) string {
	parts := []string{a.Command()}
	for _, p := range a.Params {
		var s string
		switch {
		case p.Positional && p.Kind == StringList:
			s = "<" + p.Name + ">..."
		case p.Positional:
			s = "<" + p.Name + ">"
		case p.Kind == Bool:
			s = "--" + p.Name
		default:
			s = "--" + p.Name + " <" + strings.ReplaceAll(p.Name, "-", "_") + ">"
		}
		if !p.Required {
			s = "[" + s + "]"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}

// Streamed is returned by an action that used Stdout itself (a server):
// no envelope follows.
type Streamed struct{}

// Emit writes the envelope (or text) to w and returns the process exit code.
// Warnings go into the envelope, or to errw in text.
func Emit(w, errw io.Writer, a *Action, format string, result any, err error, warnings ...string) int {
	if _, ok := result.(Streamed); ok && err == nil {
		return 0
	}
	if format == "tsv" {
		if err == nil && a != nil && a.TSV != nil {
			for _, m := range warnings {
				fmt.Fprintf(errw, "warning: %s\n", m)
			}
			a.TSV(w, result)
			return 0
		}
		format = "text"
	}
	if format == "text" || (format == "" && a != nil && a.Meta) {
		for _, m := range warnings {
			fmt.Fprintf(errw, "warning: %s\n", m)
		}
	}
	if format == "" {
		format = "json"
		if a != nil && a.Meta {
			format = "text"
		}
	}
	if err != nil {
		e := Internal(err)
		if format == "text" {
			fmt.Fprintf(errw, "error: %s\n", e.Message)
		} else {
			writeJSON(w, map[string]any{"ok": false, "error": e})
		}
		return e.Code
	}
	if format == "text" {
		switch {
		case a != nil && a.Text != nil && result != nil:
			a.Text(w, result)
		case result == nil:
		case isString(result):
			fmt.Fprintln(w, result)
		default:
			b, _ := json.MarshalIndent(result, "", "  ")
			fmt.Fprintln(w, string(b))
		}
		return 0
	}
	env := map[string]any{"ok": true}
	if result != nil {
		env["result"] = result
	}
	if len(warnings) > 0 {
		env["warnings"] = warnings
	}
	writeJSON(w, env)
	return 0
}

// EmitStd is Emit on the process's standard streams.
func EmitStd(a *Action, format string, result any, err error) int {
	return Emit(os.Stdout, os.Stderr, a, format, result, err)
}

func isString(v any) bool { _, ok := v.(string); return ok }

func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// ArgsFrom maps JSON-like arguments (an MCP tool call) onto the action's
// params, with the same defaults and checks as Parse.
func ArgsFrom(a *Action, in map[string]any) (map[string]any, error) {
	out := map[string]any{}
	known := map[string]*Param{}
	for i := range a.Params {
		p := &a.Params[i]
		known[p.Name] = p
		for _, al := range p.Aliases {
			known[al] = p
		}
	}
	for k, v := range in {
		p, ok := known[k]
		if !ok {
			return nil, UserError("unknown parameter %q for %s. Parameters: %s", k, a.Command(), paramNames(a))
		}
		switch p.Kind {
		case Bool:
			switch b := v.(type) {
			case bool:
				out[p.Name] = b
			case string:
				out[p.Name] = b == "1" || b == "true"
			default:
				return nil, UserError("%s takes true or false, got %v", p.Name, v)
			}
		case StringList:
			switch l := v.(type) {
			case []string:
				out[p.Name] = l
			case []any:
				var ss []string
				for _, x := range l {
					ss = append(ss, fmt.Sprint(x))
				}
				out[p.Name] = ss
			default:
				out[p.Name] = []string{fmt.Sprint(v)}
			}
		default:
			switch x := v.(type) {
			case string:
				out[p.Name] = x
			case float64:
				out[p.Name] = strings.TrimSuffix(fmt.Sprintf("%f", x), ".000000")
			default:
				out[p.Name] = fmt.Sprint(v)
			}
			if err := checkEnum(a, p, out[p.Name].(string)); err != nil {
				return nil, err
			}
		}
	}
	return withDefaults(a, out)
}

func paramNames(a *Action) string {
	var n []string
	for _, p := range a.Params {
		n = append(n, p.Name)
	}
	return strings.Join(n, ", ")
}
