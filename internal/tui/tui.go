// Package tui is contact's terminal interface: the cards on the left, the
// selected one on the right, modals for every input. Each change goes through
// the same actions as the CLI.
package tui

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/aclemen1/tuikit"
	"github.com/aclemen1/tuikit/complete"

	"github.com/aclemen1/contact-cli/internal/actions"
	"github.com/aclemen1/contact-cli/internal/book"
	"github.com/aclemen1/contact-cli/internal/config"
	"github.com/aclemen1/contact-cli/internal/csync"
	"github.com/aclemen1/contact-cli/internal/spec"
	"github.com/aclemen1/contact-cli/internal/store"
)

func init() {
	spec.Register(&spec.Action{
		Category: "setup", Name: "tui", Top: true,
		Summary: "Open the terminal interface: the cards, the selected one with what cites it, modals to add and edit.",
		Params: []spec.Param{
			{Name: "sphere", Kind: spec.String, Help: "Show only this sphere. Defaults to every sphere."},
			{Name: "select", Kind: spec.String, Help: "Open on this contact, selected and visible: any of its aliases, with or without contact: (JMR, contact:JMR). An unknown alias opens the list as usual, with a message."},
		},
		Effects:  []string{"Runs until q; every change goes through the same actions as the CLI."},
		Examples: []string{"contact tui", "contact tui --sphere pro", "contact tui --select JMR"},
		Run: func(ctx *spec.Context) (any, error) {
			cfg, err := config.Load(ctx.Config)
			if err != nil {
				return nil, err
			}
			spheres, err := actions.ReadSpheres(ctx, cfg)
			if err != nil {
				return nil, err
			}
			m := New(ctx.Config, spheres)
			m.cfg = cfg
			m.statePath = statePath()
			m.exe = self()
			m.exeStamp, m.build = binStamp(m.exe), buildLabel(m.exe)
			if v := os.Getenv(reloadedEnv); v != "" {
				os.Unsetenv(reloadedEnv)
				m.restore()
				m.say("rechargé "+v, false)
			} else {
				m.reload()
			}
			if a := ctx.Str("select"); a != "" && !m.pick(a) {
				m.say("contact inconnu : "+a, true)
			}
			return spec.Streamed{}, run(m)
		},
	})
}

var kinds = []string{"", store.Person, store.Organisation, store.Group}

var (
	sTitle   = lipgloss.NewStyle().Bold(true)
	sMuted   = lipgloss.NewStyle().Faint(true)
	sSel     = lipgloss.NewStyle().Reverse(true)
	sError   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	sSection = lipgloss.NewStyle().Bold(true).Underline(true)
)

type Model struct {
	cfgPath  string
	spheres  []string
	all      []*store.Contact
	items    []*store.Contact
	shown    *actions.Shown // the selected card with its views, after enter
	sel      int
	kind     int
	sphere   int // 0: every sphere, else spheres[sphere-1]
	filter   string
	full     bool
	helpOn   bool
	pendingG bool
	status   string
	statusE  bool
	w, h     int
	modal    *tuikit.Modal
	editing  string // alias of the card a modal works on
	cfg      *config.Config

	// refsC completes the refs from the shared sources, loaded at the first form.
	refsC     *complete.Completer
	membersC  *complete.Completer
	refsTried bool

	// exe is the running binary and exeStamp its mtime and inode; build labels it.
	// newBin: a rebuild waits for the TUI to be idle; reexec: quit to run it.
	exe, exeStamp, build string
	newBin, reexec       bool
	statePath            string
}

func New(cfgPath string, spheres []string) *Model {
	return &Model{cfgPath: cfgPath, spheres: spheres, w: 100, h: 30}
}

func (m *Model) Init() tea.Cmd { return m.watch() }

// call runs an action as the CLI would.
func (m *Model) call(name string, args map[string]any) (any, error) {
	a := spec.Find("contact", name)
	if a == nil {
		a = spec.Find("sync", name)
	}
	if a == nil {
		return nil, fmt.Errorf("no action %s", name)
	}
	full := map[string]any{}
	for _, p := range a.Params {
		if p.Default != "" {
			full[p.Name] = p.Default
		}
	}
	for k, v := range args {
		full[k] = v
	}
	ctx := &spec.Context{Args: full, Config: m.cfgPath, Format: "json"}
	res, err := a.Run(ctx)
	if err == nil && len(ctx.Warnings) > 0 {
		m.say(strings.Join(ctx.Warnings, " · "), true)
	}
	return res, err
}

func (m *Model) say(s string, isErr bool) { m.status, m.statusE = s, isErr }

func (m *Model) reload() {
	args := map[string]any{}
	if m.sphere > 0 {
		args["sphere"] = m.spheres[m.sphere-1]
	}
	res, err := m.call("ls", args)
	if err != nil {
		m.say(err.Error(), true)
		return
	}
	m.all = res.([]*store.Contact)
	var keep []*store.Contact
	for _, c := range m.all {
		if !contains(m.spheres, c.Sphere) {
			continue
		}
		if kinds[m.kind] != "" && c.Kind != kinds[m.kind] {
			continue
		}
		if m.filter != "" && !strings.Contains(book.Fold(strings.Join(c.AllAliases(), " ")+" "+c.Name+" "+c.Org+" "+strings.Join(c.Tags, " ")), book.Fold(m.filter)) {
			continue
		}
		keep = append(keep, c)
	}
	cur := m.current()
	m.items = keep
	m.sel = 0
	if cur != nil {
		for i, c := range m.items {
			if c.Alias == cur.Alias {
				m.sel = i
			}
		}
	}
	if m.full {
		m.loadShown()
	}
}

func (m *Model) current() *store.Contact {
	if m.sel < 0 || m.sel >= len(m.items) {
		return nil
	}
	return m.items[m.sel]
}

func (m *Model) loadShown() {
	c := m.current()
	if c == nil {
		m.shown = nil
		return
	}
	res, err := m.call("show", map[string]any{"alias": c.Alias})
	if err != nil {
		m.say(err.Error(), true)
		return
	}
	m.shown = res.(*actions.Shown)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case reloadMsg:
		m.newBin = true
		return m, m.maybeReload()
	case watchMsg:
		if msg.bin != "" && m.exeStamp != "" && msg.bin != m.exeStamp {
			m.newBin = true
		}
		if cmd := m.maybeReload(); cmd != nil {
			return m, cmd
		}
		return m, m.watch()
	}
	if m.modal.Open() {
		if ws, ok := msg.(tea.WindowSizeMsg); ok {
			m.w, m.h = ws.Width, ws.Height
		}
		return m, m.modal.Update(msg)
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tuikit.DoneMsg:
		return m, tea.Batch(m.done(msg), m.maybeReload())
	case tuikit.CancelMsg:
		m.say("annulé", false)
		return m, m.maybeReload()
	case tea.KeyPressMsg:
		return m, m.key(msg.String())
	}
	return m, nil
}

func (m *Model) open(title string, c tuikit.Content) tea.Cmd {
	m.modal = tuikit.NewModal(title, c).SetSize(m.w, m.h)
	return nil
}

func (m *Model) key(k string) tea.Cmd {
	m.status = ""
	if m.pendingG {
		m.pendingG = false
		if k == "g" {
			m.sel = 0
			return nil
		}
	}
	c := m.current()
	switch k {
	case "q", "ctrl+c":
		return tea.Quit
	case "?":
		m.helpOn = !m.helpOn
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "g":
		m.pendingG = true
	case "home":
		m.sel = 0
	case "end", "G":
		m.sel = max(0, len(m.items)-1)
	case "ctrl+d", "pgdown":
		m.move(max(1, m.h/2))
	case "ctrl+u", "pgup":
		m.move(-max(1, m.h/2))
	case "enter", "l", "right":
		if c != nil {
			m.full = true
			m.loadShown()
		}
	case "esc", "h", "left":
		m.full, m.shown = false, nil
	case "1", "2", "3", "4":
		m.kind = int(k[0] - '1')
		m.reload()
		m.say("sorte : "+kindName(kinds[m.kind]), false)
	case "s":
		m.sphere = (m.sphere + 1) % (len(m.spheres) + 1)
		m.reload()
		m.say("sphère : "+m.sphereName(), false)
	case "/":
		return m.open("Filtrer", tuikit.NewForm("filter", tuikit.Text("q", "Texte").Default(m.filter).Help("alias, nom, organisation ou tag ; vide pour tout voir")))
	case "r", "u":
		m.reload()
		m.say("relu", false)
	case "R":
		return m.open("Synchroniser avec le carnet d'adresses ?", tuikit.NewConfirm("sync", "Écrire dans le carnet d'adresses les changements des fiches ?"))
	case "c", "n":
		return m.openAdd()
	case "E":
		if c != nil {
			return m.openEdit(c)
		}
	case "N":
		if c != nil {
			m.editing = c.Alias
			if m.cfg == nil || len(m.cfg.Notes.Add) == 0 {
				m.say("pas de notes.add dans la configuration", true)
				return nil
			}
			return m.open("Note de suivi · "+c.Alias, tuikit.NewEditor("note", "Note de suivi", ""))
		}
	case "M":
		if c != nil && c.Kind == store.Group {
			m.editing = c.Alias
			return m.open("Membres de "+c.Alias, tuikit.NewForm("members",
				tuikit.Refs("members", "Membres", m.memberComplete).Default(append([]string{}, c.Members...)).Strict()))
		}
	case "#":
		if c != nil {
			m.editing = c.Alias
			return m.open("Supprimer "+c.Alias, tuikit.NewConfirmTyped("rm", "Supprimer la fiche "+c.Alias+" ("+c.Name+") ? Le carnet d'adresses garde sa carte. Tapez l'alias pour confirmer.", c.Alias))
		}
	}
	return nil
}

func (m *Model) move(d int) {
	if len(m.items) == 0 {
		return
	}
	m.sel = min(len(m.items)-1, max(0, m.sel+d))
	if m.full {
		m.loadShown()
	}
}

func (m *Model) complete(q string) []tuikit.Item {
	var out []tuikit.Item
	fq := book.Fold(q)
	for _, c := range m.all {
		if fq == "" || strings.Contains(book.Fold(strings.Join(c.AllAliases(), " ")+" "+c.Name), fq) {
			out = append(out, tuikit.Item{Value: c.Alias, Label: c.Alias + " · " + c.Name})
		}
	}
	return out
}

func (m *Model) groupComplete(q string) []tuikit.Item {
	var out []tuikit.Item
	for _, it := range m.complete(q) {
		for _, c := range m.all {
			if c.Alias == it.Value && c.Kind == store.Group {
				out = append(out, it)
			}
		}
	}
	return out
}

// loadSources builds a completer from the configured sources of a field; the
// sources missing from the shared file are reported, the others kept.
func (m *Model) loadSources(uses complete.Uses) *complete.Completer {
	if len(uses) == 0 {
		return nil
	}
	c, missing := complete.FromConfig(uses)
	if len(missing) > 0 {
		m.say("complétion indisponible : "+strings.Join(missing, ", "), true)
	}
	return c
}

func (m *Model) loadCompleters() {
	if m.refsTried {
		return
	}
	m.refsTried = true
	if m.cfg != nil {
		m.refsC = m.loadSources(m.cfg.Complete["refs"])
		m.membersC = m.loadSources(m.cfg.Complete["members"])
	}
}

func (m *Model) refComplete(q string) []tuikit.Item {
	m.loadCompleters()
	if m.refsC == nil {
		return nil
	}
	return m.refsC.Complete(q)
}

// memberComplete proposes aliases from the configured sources, or else from
// the cards in memory.
func (m *Model) memberComplete(q string) []tuikit.Item {
	m.loadCompleters()
	if m.membersC == nil {
		return m.complete(q)
	}
	return m.membersC.Complete(q)
}

func (m *Model) openAdd() tea.Cmd {
	m.loadCompleters()
	sphere := m.spheres[0]
	if m.sphere > 0 {
		sphere = m.spheres[m.sphere-1]
	}
	f := tuikit.NewForm("add",
		tuikit.Text("name", "Nom").Required(),
		tuikit.Choice("kind", "Sorte", store.Kinds...).Default(store.Person),
		tuikit.Choice("sphere", "Sphère", m.spheres...).Default(sphere),
		tuikit.Text("alias", "Alias").Help("vide : initiales du nom"),
		tuikit.Text("org", "Organisation").ShowIf("kind", store.Person),
		tuikit.Text("phones", "Téléphones").Help("séparés par des virgules ; mobile:+41 … garde le libellé").ShowIf("kind", store.Person, store.Organisation),
		tuikit.Text("emails", "E-mails").Help("séparés par des virgules").ShowIf("kind", store.Person, store.Organisation),
		tuikit.Text("tags", "Tags").Help("séparés par des virgules"),
		tuikit.Refs("groups", "Groupes", m.groupComplete).Strict(),
		tuikit.Refs("members", "Membres", m.memberComplete).Strict().ShowIf("kind", store.Group),
		tuikit.Refs("refs", "Réfs", m.refComplete).Help("<outil>:<id>"),
	)
	return m.open("Nouvelle fiche", f)
}

// saveEdit writes the edit form; refs come apart since a Refs field holds a list.
func (m *Model) saveEdit(v tuikit.Values, refs []string) error {
	args := map[string]any{"alias": m.editing, "name": v.String("name"), "org": orNone(v.String("org")),
		"phone": split(v.String("phones")), "email": split(v.String("emails")), "body": v.String("body")}
	if cur := m.byAlias(m.editing); cur != nil {
		tags := split(v.String("tags"))
		args["add-tag"], args["rm-tag"] = minus(tags, cur.Tags), minus(cur.Tags, tags)
		args["add-ref"], args["rm-ref"] = minus(refs, cur.Refs), minus(cur.Refs, refs)
	}
	if _, err := m.call("edit", args); err != nil {
		return err
	}
	m.reload()
	m.say("fiche "+m.editing+" modifiée", false)
	return nil
}

func (m *Model) openEdit(c *store.Contact) tea.Cmd {
	m.editing = c.Alias
	m.refComplete("")
	f := tuikit.NewForm("edit",
		tuikit.Text("name", "Nom").Default(c.Name).Required(),
		tuikit.Text("org", "Organisation").Default(c.Org),
		tuikit.Text("phones", "Téléphones").Default(strings.Join(c.Phones, ", ")),
		tuikit.Text("emails", "E-mails").Default(strings.Join(c.Emails, ", ")),
		tuikit.Text("tags", "Tags").Default(strings.Join(c.Tags, ", ")),
		tuikit.Refs("refs", "Réfs", m.refComplete).Default(append([]string{}, c.Refs...)).Help("<outil>:<id>"),
		tuikit.TextArea("body", "Notes libres").Default(c.Body).Help("jamais envoyées au carnet d'adresses"),
	)
	return m.open("Modifier "+c.Alias, f)
}

func split(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func (m *Model) done(msg tuikit.DoneMsg) tea.Cmd {
	v := msg.Values
	var err error
	switch msg.ID {
	case "filter":
		m.filter = v.String("q")
		m.reload()
		return nil
	case "sync":
		var res any
		if res, err = m.call("contacts", nil); err == nil {
			m.reload()
			m.say(fmt.Sprintf("carnet synchronisé : %d changement(s)", len(res.(*csync.Report).Changes)), false)
		}
	case "add":
		args := map[string]any{"name": v.String("name"), "kind": v.String("kind"), "sphere": v.String("sphere"),
			"phone": split(v.String("phones")), "email": split(v.String("emails")), "tag": split(v.String("tags")),
			"group": v.Strings("groups"), "member": v.Strings("members"), "ref": v.Strings("refs")}
		if a := v.String("alias"); a != "" {
			args["alias"] = a
		}
		if o := v.String("org"); o != "" {
			args["org"] = o
		}
		var res any
		if res, err = m.call("add", args); err == nil {
			c := res.(*store.Contact)
			m.reload()
			m.selectAlias(c.Alias)
			m.say("fiche "+c.Alias+" créée", false)
		}
	case "edit":
		err = m.saveEdit(v, v.Strings("refs"))
	case "note":
		text := strings.TrimSpace(v.String("note"))
		if text == "" {
			m.say("note vide : rien n'est ajouté", false)
			return nil
		}
		if _, err = m.call("note", map[string]any{"alias": m.editing, "text": text}); err == nil {
			m.reload()
			m.say("note ajoutée à "+m.editing, false)
		}
	case "members":
		cur := m.byAlias(m.editing)
		if cur != nil {
			want := v.Strings("members")
			if _, err = m.call("edit", map[string]any{"alias": m.editing, "add-member": minus(want, cur.Members), "rm-member": minus(cur.Members, want)}); err == nil {
				m.reload()
				m.say("membres de "+m.editing+" enregistrés", false)
			}
		}
	case "rm":
		if _, err = m.call("rm", map[string]any{"alias": m.editing, "force": true}); err == nil {
			m.full, m.shown = false, nil
			m.reload()
			m.say("fiche "+m.editing+" supprimée", false)
		}
	}
	if err != nil {
		m.say(err.Error(), true)
	}
	return nil
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func minus(a, b []string) []string {
	var out []string
	for _, x := range a {
		if !contains(b, x) {
			out = append(out, x)
		}
	}
	return out
}

func (m *Model) byAlias(a string) *store.Contact {
	for _, c := range m.all {
		if c.Alias == a {
			return c
		}
	}
	return nil
}

func (m *Model) selectAlias(a string) {
	for i, c := range m.items {
		if c.Alias == a {
			m.sel = i
		}
	}
}

// pick selects the card that has this alias, main or other; false when none is listed.
func (m *Model) pick(a string) bool {
	a, err := store.NormAlias(a)
	if err != nil {
		return false
	}
	for i, c := range m.items {
		for _, x := range c.AllAliases() {
			if x == a {
				m.sel = i
				return true
			}
		}
	}
	return false
}

func (m *Model) sphereName() string {
	if m.sphere == 0 {
		return "toutes"
	}
	return m.spheres[m.sphere-1]
}

func kindName(k string) string {
	switch k {
	case store.Person:
		return "personnes"
	case store.Organisation:
		return "organisations"
	case store.Group:
		return "groupes"
	}
	return "toutes"
}

const help = "j/k bouger · gg/G début/fin · entrée détail · esc retour · / filtrer · 1-4 sorte · s sphère · c nouvelle · E modifier · N note de suivi · M membres · # supprimer · R synchroniser · r relire · q quitter"

func (m *Model) View() tea.View {
	v := tea.NewView(tuikit.Overlay(m.render(), m.modal, m.w, m.h))
	v.AltScreen = true
	return v
}

func (m *Model) render() string {
	header := sTitle.Render("contact") + sMuted.Render(fmt.Sprintf("  %d fiche(s) · sorte %s · sphère %s", len(m.items), kindName(kinds[m.kind]), m.sphereName()))
	if m.filter != "" {
		header += sMuted.Render(" · filtre « " + m.filter + " »")
	}
	if m.build != "" {
		header += "  " + sMuted.Render(m.build)
	}
	if m.newBin {
		header += " " + sError.Render("● nouvelle version")
	}
	bodyH := max(3, m.h-4)
	leftW := min(48, m.w/2)
	var left []string
	top := max(0, m.sel-bodyH+1)
	for i := top; i < len(m.items) && len(left) < bodyH; i++ {
		c := m.items[i]
		line := truncate(fmt.Sprintf("%-7s %s %s", c.Alias, kindMark(c.Kind), c.Name), leftW)
		if i == m.sel {
			line = sSel.Render(pad(line, leftW))
		}
		left = append(left, line)
	}
	if len(m.items) == 0 {
		left = append(left, sMuted.Render("aucune fiche"))
	}
	right := m.detail(max(20, m.w-leftW-3))
	body := lipgloss.JoinHorizontal(lipgloss.Top, lipgloss.NewStyle().Width(leftW).Height(bodyH).Render(strings.Join(left, "\n")), " │ ",
		lipgloss.NewStyle().Height(bodyH).MaxHeight(bodyH).Render(strings.Join(right, "\n")))
	foot := sMuted.Render("? aide")
	if m.helpOn {
		foot = sMuted.Render(help)
	}
	if m.status != "" {
		if m.statusE {
			foot = sError.Render(m.status)
		} else {
			foot = m.status
		}
	}
	return strings.Join([]string{header, "", body, foot}, "\n")
}

func kindMark(k string) string {
	switch k {
	case store.Organisation:
		return "◆"
	case store.Group:
		return "●"
	}
	return "·"
}

func (m *Model) detail(width int) []string {
	c := m.current()
	if c == nil {
		return nil
	}
	if m.full && m.shown != nil && m.shown.Contact.Alias == c.Alias {
		c = m.shown.Contact
	}
	out := []string{sTitle.Render(c.Alias+" · "+c.Name) + sMuted.Render("  "+c.Kind+" · "+c.Sphere), ""}
	row := func(label, v string) {
		if v != "" {
			out = append(out, sMuted.Render(pad(label, 12))+truncate(v, width-12))
		}
	}
	row("organisation", c.Org)
	for i, p := range c.Phones {
		row(map[bool]string{true: "téléphones", false: ""}[i == 0], p)
	}
	for i, e := range c.Emails {
		row(map[bool]string{true: "e-mails", false: ""}[i == 0], e)
	}
	row("alias", strings.Join(c.Aliases, ", "))
	row("membres", strings.Join(c.Members, ", "))
	row("groupes", strings.Join(c.Groups, ", "))
	row("tags", strings.Join(c.Tags, ", "))
	row("réfs", strings.Join(c.Refs, ", "))
	row("carnet", c.Remote)
	if c.Body != "" {
		out = append(out, "")
		for _, l := range strings.Split(c.Body, "\n") {
			out = append(out, truncate(l, width))
		}
	}
	if m.full && m.shown != nil {
		if len(m.shown.Notes) > 0 {
			out = append(out, "", sSection.Render("Notes"))
			for _, n := range m.shown.Notes {
				lines := strings.Split(strings.TrimSpace(n.Body), "\n")
				out = append(out, truncate(sMuted.Render(shortDate(n.Created)+"  ")+lines[0], width))
				for _, l := range lines[1:] {
					out = append(out, truncate("            "+l, width))
				}
			}
		}
		for _, v := range m.shown.Views {
			out = append(out, "", sSection.Render(v.Label))
			if v.Error != "" {
				out = append(out, sError.Render(truncate(v.Error, width)))
			}
			for _, r := range v.Rows {
				l := r.Text
				if l == "" {
					l = string(r.Item)
				}
				out = append(out, truncate(l, width))
			}
		}
	} else if !m.full {
		out = append(out, "", sMuted.Render("entrée : ce qui cite contact:"+c.Alias))
	}
	return out
}

func truncate(s string, w int) string {
	r := []rune(s)
	if w <= 1 || len(r) <= w {
		return s
	}
	return string(r[:w-1]) + "…"
}

func pad(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// shortDate shows the day of an RFC 3339 time.
func shortDate(t string) string {
	if len(t) >= 10 {
		return t[8:10] + "." + t[5:7] + "." + t[0:4]
	}
	return t
}
