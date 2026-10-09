package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aclemen1/contact-cli/internal/actions"
	_ "github.com/aclemen1/contact-cli/internal/mcpserver"
	"github.com/aclemen1/contact-cli/internal/spec"
	_ "github.com/aclemen1/contact-cli/internal/tui"
)

const rootHelp = `contact — the directory of people, organisations and groups, one card per alias, cited as contact:<alias>.

Usage: contact <category> <action> [arguments] [--sphere <name>] [--config <path>] [--format json|text|tsv]

MESSAGE FOR LLM / AI AGENTS: run ` + "`contact schema`" + ` to list categories, then
` + "`contact schema <category> <action>`" + ` for the exact parameters, examples and
effects of one action. Copy an example from there.

Actions by category:
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var configFlag, format string
	help := false
	var rest []string
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--config" && i+1 < len(argv):
			configFlag = argv[i+1]
			i++
		case strings.HasPrefix(a, "--config="):
			configFlag = strings.TrimPrefix(a, "--config=")
		case a == "--format" && i+1 < len(argv):
			format = argv[i+1]
			i++
		case strings.HasPrefix(a, "--format="):
			format = strings.TrimPrefix(a, "--format=")
		case a == "--json":
			format = "json"
		case a == "--help" || a == "-h":
			help = true
		case a == "--version":
			rest = append(rest, "version")
		default:
			rest = append(rest, a)
		}
	}
	if format != "" && format != "json" && format != "text" && format != "tsv" {
		return spec.Emit(stdout, stderr, nil, "json", nil, spec.UserError("--format takes json, text or tsv, got %q. Example: contact ls --format text", format))
	}
	if len(rest) == 0 {
		printRoot(stdout)
		return 0
	}
	act, args := spec.Resolve(rest)
	if act == nil {
		if spec.IsCategory(rest[0]) {
			if help || len(rest) == 1 {
				spec.TextSchema(stdout, spec.ActionsIn(rest[0]))
				return 0
			}
			return spec.Emit(stdout, stderr, nil, format, nil, spec.UserError("unknown action %q in %s. Actions: run `contact schema %s`", rest[1], rest[0], rest[0]))
		}
		return spec.Emit(stdout, stderr, nil, format, nil, spec.UserError("unknown command %q. Run `contact schema` to list categories, for example `contact add`", rest[0]))
	}
	if help {
		spec.TextSchema(stdout, spec.Leaf{Action: act, Usage: spec.Usage(act)})
		return 0
	}
	parsed, err := spec.Parse(act, args)
	if err != nil {
		return spec.Emit(stdout, stderr, act, format, nil, err)
	}
	ctx := &spec.Context{Args: parsed, Config: configFlag, Format: format, Stdin: stdin, Stdout: stdout}
	result, err := act.Run(ctx)
	return spec.Emit(stdout, stderr, act, format, result, err, ctx.Warnings...)
}

func printRoot(w io.Writer) {
	fmt.Fprint(w, rootHelp)
	for _, c := range spec.Categories() {
		var names []string
		for _, a := range spec.ActionsIn(c) {
			names = append(names, strings.TrimPrefix(a.Command, "contact "))
		}
		fmt.Fprintf(w, "  %-10s %s\n", c, strings.Join(names, ", "))
	}
	fmt.Fprintf(w, "\nVersion %s\n", actions.Version)
}
