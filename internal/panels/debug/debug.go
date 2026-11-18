// Package debug est le module Débogage : il pilote Delve (« dlv dap »)
// pour lancer le programme ou les tests d'un paquet, s'arrêter aux points
// d'arrêt posés dans l'éditeur (F9), avancer pas à pas et lire la pile
// d'appels et les variables. F5 à F8 fonctionnent depuis n'importe quel
// module.
package debug

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/dap"
	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/theme"
)

type status int

const (
	idle     status = iota // pas de session
	starting               // compilation et lancement
	running
	paused
	ended
)

const (
	maxOutput   = 2000 // lignes de sortie gardées
	stackLevels = 50
)

// startFunc démarre Delve ; remplacée dans les tests.
type startFunc func(ctx context.Context, dir string, onEvent func(dap.Event)) (*dap.Client, error)

type startedMsg struct {
	c   *dap.Client
	err error
}

type eventMsg dap.Event

type lostMsg struct{} // connexion avec Delve perdue

type stackMsg struct {
	frames []dap.Frame
	err    error
}

type varsMsg struct {
	frame int
	nodes []*node
	err   error
}

type childrenMsg struct {
	n    *node
	vars []dap.Variable
	err  error
}

type errMsg struct{ err error }

// node est une variable de l'arbre (ou un groupe : Locals…).
type node struct {
	v        dap.Variable
	depth    int
	expanded bool
	loaded   bool
	children []*node
}

type Model struct {
	th    *theme.Theme
	proj  *project.Project
	state *panel.DebugState
	start startFunc

	target string // « . », « ./cmd/x -- args », « test ./pkg -run TestX »
	typing bool   // saisie de la cible
	input  string

	status   status
	c        *dap.Client
	events   chan tea.Msg
	thread   int
	exitCode int
	reason   string

	frames   []dap.Frame
	frameSel int
	vars     []*node // racines (groupes de variables) du niveau choisi
	cursor   int     // dans les variables affichées
	offset   int

	output  []string
	partial string // fin de sortie sans retour à la ligne
	varsH   int
}

func New(th *theme.Theme, p *project.Project) *Model {
	return &Model{th: th, proj: p, state: panel.NewDebugState(), start: dap.Start, target: ".", varsH: 10}
}

func (m *Model) Title() string { return i18n.N("Débogage") }
func (m *Model) Icon() string  { return "" } // insecte (Font Awesome)
func (m *Model) Init() tea.Cmd { return nil }

// UseDebug partage l'état de débogage avec l'éditeur.
func (m *Model) UseDebug(s *panel.DebugState) { m.state = s }

func (m *Model) Capturing() bool { return m.typing }

func (m *Model) Hints() []panel.Hint {
	if m.typing {
		return []panel.Hint{{Key: "Entrée", Desc: i18n.T("valider")}, {Key: "Échap", Desc: i18n.T("annuler")}}
	}
	switch m.status {
	case paused:
		return []panel.Hint{{Key: "F5", Desc: i18n.T("continuer")}, {Key: "F6", Desc: i18n.T("pas à pas")},
			{Key: "F7", Desc: i18n.T("entrer")}, {Key: "F8", Desc: i18n.T("sortir")}, {Key: "Maj+F5", Desc: i18n.T("arrêter")},
			{Key: "u / d", Desc: i18n.T("pile")}, {Key: "→ ←", Desc: i18n.T("déplier / replier")}}
	case running, starting:
		return []panel.Hint{{Key: "F5", Desc: i18n.T("pause")}, {Key: "Maj+F5", Desc: i18n.T("arrêter")}}
	}
	return []panel.Hint{{Key: "F5", Desc: i18n.T("lancer")}, {Key: "c", Desc: i18n.T("cible")}, {Key: "F9", Desc: i18n.T("point d'arrêt (éditeur)")}}
}

func (m *Model) Commands() []panel.Command {
	return []panel.Command{
		{Title: i18n.T("Lancer le débogage / continuer"), Key: "F5", Msg: keyMsg("f5")},
		{Title: i18n.T("Arrêter le débogage"), Key: "Maj+F5", Msg: keyMsg("shift+f5")},
		{Title: i18n.T("Pas à pas principal"), Key: "F6", Msg: keyMsg("f6")},
		{Title: i18n.T("Entrer dans la fonction"), Key: "F7", Msg: keyMsg("f7")},
		{Title: i18n.T("Sortir de la fonction"), Key: "F8", Msg: keyMsg("f8")},
		{Title: i18n.T("Changer la cible du débogage"), Key: "c", Msg: keyMsg("c")},
	}
}

// keyMsg rejoue une touche du débogueur (palette).
type keyMsg string

// State garde la cible et les points d'arrêt (chemins relatifs au projet).
func (m *Model) State() map[string]string {
	var bps []string
	for _, f := range m.state.Files() {
		rel, err := filepath.Rel(m.proj.Root, f)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		for _, l := range m.state.Lines(f) {
			bps = append(bps, fmt.Sprintf("%s:%d", filepath.ToSlash(rel), l))
		}
	}
	return map[string]string{"target": m.target, "breakpoints": strings.Join(bps, "\n")}
}

func (m *Model) Restore(s map[string]string) {
	if t := strings.TrimSpace(s["target"]); t != "" {
		m.target = t
	}
	for _, bp := range strings.Split(s["breakpoints"], "\n") {
		file, line, ok := strings.Cut(bp, ":")
		var n int
		if _, err := fmt.Sscan(line, &n); !ok || err != nil || n < 1 {
			continue
		}
		if path := filepath.Join(m.proj.Root, filepath.FromSlash(file)); !m.state.Has(path, n) {
			m.state.Toggle(path, n)
		}
	}
}

// ─── Cible ───────────────────────────────────────────────────────────────────

// parseTarget lit la cible : « [test] paquet [arguments] ». Pour un
// programme, les arguments suivent « -- » ; pour des tests, les options
// courtes de go test (-run, -v…) deviennent celles du binaire (-test.run…).
func parseTarget(root, target string) dap.Launch {
	f := strings.Fields(target)
	l := dap.Launch{Mode: "debug", Program: root, Dir: root}
	if len(f) > 0 && f[0] == "test" {
		l.Mode, f = "test", f[1:]
	}
	if len(f) > 0 && f[0] != "--" {
		l.Program = filepath.Join(root, f[0])
		f = f[1:]
	}
	if len(f) > 0 && f[0] == "--" {
		f = f[1:]
	}
	for _, a := range f {
		if l.Mode == "test" && strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "-test.") {
			a = "-test." + strings.TrimLeft(a, "-")
		}
		l.Args = append(l.Args, a)
	}
	if l.Mode == "test" {
		l.Dir = l.Program // go test s'exécute dans le répertoire du paquet
	}
	return l
}

// ─── Touches F5 à F8 ─────────────────────────────────────────────────────────

// DebugKey traite F5 (lancer, continuer ou interrompre), Maj+F5 (arrêter)
// et F6 à F8 (pas à pas), quel que soit le module affiché.
func (m *Model) DebugKey(key string) tea.Cmd {
	switch key {
	case "f5":
		switch m.status {
		case idle, ended:
			return m.launch()
		case paused:
			return m.resume(func(ctx context.Context, c *dap.Client, t int) error { return c.Continue(ctx, t) })
		case running:
			c, t := m.c, m.thread
			return m.async(func(ctx context.Context) error { return c.Pause(ctx, t) })
		}
	case "shift+f5":
		if m.c != nil || m.status == starting {
			return m.stop(i18n.T("Débogage arrêté"))
		}
	case "f6", "f7", "f8":
		if m.status != paused {
			return nil
		}
		step := map[string]func(ctx context.Context, c *dap.Client, t int) error{
			"f6": func(ctx context.Context, c *dap.Client, t int) error { return c.Next(ctx, t) },
			"f7": func(ctx context.Context, c *dap.Client, t int) error { return c.StepIn(ctx, t) },
			"f8": func(ctx context.Context, c *dap.Client, t int) error { return c.StepOut(ctx, t) },
		}[key]
		return m.resume(step)
	}
	return nil
}

// launch démarre Delve, compile et lance la cible, pose les points d'arrêt.
func (m *Model) launch() tea.Cmd {
	m.status, m.output, m.partial, m.frames, m.vars = starting, nil, "", nil, nil
	m.reason, m.exitCode = "", 0
	m.events = make(chan tea.Msg, 512)
	events, start, root, state := m.events, m.start, m.proj.Root, m.state
	l := parseTarget(root, m.target)
	bps := map[string][]int{}
	for _, f := range state.Files() {
		bps[f] = state.Lines(f)
	}
	m.addOutput(fmt.Sprintf(i18n.T("Compilation et lancement de %s…"), m.target) + "\n")
	return tea.Batch(func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		c, err := start(ctx, root, func(e dap.Event) { events <- eventMsg(e) })
		if err != nil {
			return startedMsg{err: err}
		}
		go func() { <-c.Done(); events <- lostMsg{} }()
		if err := c.Launch(ctx, l); err != nil {
			c.Close()
			return startedMsg{err: err}
		}
		for f, lines := range bps {
			_, _ = c.SetBreakpoints(ctx, f, lines) // un point refusé ne bloque pas le lancement
		}
		if err := c.ConfigurationDone(ctx); err != nil {
			c.Close()
			return startedMsg{err: err}
		}
		return startedMsg{c: c}
	}, m.waitEvent())
}

// resume reprend l'exécution (continuer ou un pas).
func (m *Model) resume(f func(ctx context.Context, c *dap.Client, thread int) error) tea.Cmd {
	c, t := m.c, m.thread
	m.status = running
	m.clearStop()
	return m.async(func(ctx context.Context) error { return f(ctx, c, t) })
}

// async exécute une requête sans attendre sa réponse dans la boucle.
func (m *Model) async(f func(ctx context.Context) error) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := f(ctx); err != nil && !errors.Is(err, dap.ErrClosed) {
			return errMsg{err}
		}
		return nil
	}
}

// stop termine la session.
func (m *Model) stop(why string) tea.Cmd {
	c := m.c
	m.c, m.status = nil, ended
	m.frames, m.frameSel, m.vars, m.cursor, m.offset = nil, 0, nil, 0, 0 // plus rien à inspecter
	m.clearStop()
	if why != "" {
		m.addOutput(why + "\n")
	}
	if c == nil {
		return nil
	}
	return tea.Batch(func() tea.Msg { c.Close(); return nil }, panel.Flash(why, false))
}

func (m *Model) clearStop() { m.state.StopPath, m.state.StopLine = "", 0 }

func (m *Model) waitEvent() tea.Cmd {
	ch := m.events
	return func() tea.Msg { return <-ch }
}

// ─── Update ──────────────────────────────────────────────────────────────────

func (m *Model) Update(msg tea.Msg) (panel.Panel, tea.Cmd) {
	switch msg := msg.(type) {
	case keyMsg:
		if msg == "c" {
			return m, m.key("c")
		}
		return m, m.DebugKey(string(msg))

	case startedMsg:
		if msg.err != nil {
			m.status = ended
			if errors.Is(msg.err, dap.ErrMissing) {
				m.addOutput(msg.err.Error() + " : " + dap.Install + "\n")
			} else {
				m.addOutput(msg.err.Error() + "\n")
			}
			return m, panel.Flash(i18n.T("Débogage : ")+msg.err.Error(), true)
		}
		if m.status != starting { // arrêté pendant le lancement
			c := msg.c
			return m, func() tea.Msg { c.Close(); return nil }
		}
		m.c, m.status = msg.c, running
		return m, nil

	case eventMsg:
		return m, tea.Batch(m.onEvent(dap.Event(msg)), m.waitEvent())

	case lostMsg:
		if m.c != nil {
			return m, m.stop(i18n.T("Session de débogage terminée"))
		}
		return m, nil

	case stackMsg:
		if msg.err != nil || m.status != paused {
			return m, nil
		}
		m.frames, m.frameSel = msg.frames, 0
		return m, m.selectFrame()

	case varsMsg:
		if m.status == paused && msg.err == nil && m.frameSel < len(m.frames) && m.frames[m.frameSel].ID == msg.frame {
			m.vars, m.cursor, m.offset = msg.nodes, 0, 0
		}
		return m, nil

	case childrenMsg:
		if msg.err == nil {
			msg.n.loaded = true
			for _, v := range msg.vars {
				msg.n.children = append(msg.n.children, &node{v: v, depth: msg.n.depth + 1})
			}
		}
		return m, nil

	case errMsg:
		return m, panel.Flash(i18n.T("Débogage : ")+msg.err.Error(), true)

	case panel.BreakpointsMsg:
		if c := m.c; c != nil { // session en cours : Delve suit les changements
			path, lines := msg.Path, m.state.Lines(msg.Path)
			return m, m.async(func(ctx context.Context) error {
				_, err := c.SetBreakpoints(ctx, path, lines)
				return err
			})
		}

	case tea.KeyPressMsg:
		return m, m.key(msg.String())
	}
	return m, nil
}

// onEvent traite un événement de Delve.
func (m *Model) onEvent(e dap.Event) tea.Cmd {
	switch e.Name {
	case "output":
		// stdout et stderr du programme (et de la compilation) ; les
		// messages de Delve lui-même (« console ») n'apprennent rien.
		var o dap.Output
		if json.Unmarshal(e.Body, &o) == nil && o.Category != "console" {
			m.addOutput(o.Output)
		}
	case "stopped":
		var s dap.Stopped
		if json.Unmarshal(e.Body, &s) != nil {
			return nil
		}
		m.status, m.thread, m.reason = paused, s.ThreadID, s.Reason
		c, t := m.c, s.ThreadID
		if c == nil {
			return nil
		}
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			frames, err := c.StackTrace(ctx, t, stackLevels)
			return stackMsg{frames: frames, err: err}
		}
	case "continued":
		m.status = running
		m.clearStop()
	case "exited":
		var x dap.Exited
		if json.Unmarshal(e.Body, &x) == nil {
			m.exitCode = x.ExitCode
		}
	case "terminated":
		return m.stop(fmt.Sprintf(i18n.T("Programme terminé (code %d)"), m.exitCode))
	}
	return nil
}

// selectFrame montre le niveau choisi de la pile dans l'éditeur et charge
// ses variables.
func (m *Model) selectFrame() tea.Cmd {
	if m.frameSel >= len(m.frames) {
		return nil
	}
	f := m.frames[m.frameSel]
	m.vars = nil
	var show tea.Cmd
	if f.Source.Path != "" && f.Line > 0 {
		if m.frameSel == 0 {
			m.state.StopPath, m.state.StopLine = f.Source.Path, f.Line
		}
		path, line := f.Source.Path, f.Line
		show = func() tea.Msg { return panel.ShowMsg{File: path, Line: line} }
	}
	c := m.c
	if c == nil {
		return show
	}
	load := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		scopes, err := c.Scopes(ctx, f.ID)
		if err != nil {
			return varsMsg{frame: f.ID, err: err}
		}
		var nodes []*node
		for i, s := range scopes {
			n := &node{v: dap.Variable{Name: s.Name, Ref: s.Ref}}
			if i == 0 { // le premier groupe (variables locales) est déplié
				vars, err := c.Variables(ctx, s.Ref)
				if err == nil {
					n.expanded, n.loaded = true, true
					for _, v := range vars {
						n.children = append(n.children, &node{v: v, depth: 1})
					}
				}
			}
			nodes = append(nodes, n)
		}
		return varsMsg{frame: f.ID, nodes: nodes}
	}
	return tea.Batch(show, load)
}

func (m *Model) addOutput(s string) {
	s = m.partial + s
	lines := strings.Split(s, "\n")
	m.partial = lines[len(lines)-1]
	for _, l := range lines[:len(lines)-1] {
		m.output = append(m.output, strings.ReplaceAll(ansi.Strip(l), "\t", "    "))
	}
	if extra := len(m.output) - maxOutput; extra > 0 {
		m.output = m.output[extra:]
	}
}

// ─── Touches du module ───────────────────────────────────────────────────────

func (m *Model) key(s string) tea.Cmd {
	if m.typing {
		switch s {
		case "enter":
			if t := strings.TrimSpace(m.input); t != "" {
				m.target = t
			}
			m.typing = false
		case "esc":
			m.typing = false
		case "backspace":
			if r := []rune(m.input); len(r) > 0 {
				m.input = string(r[:len(r)-1])
			}
		default:
			if len([]rune(s)) == 1 || s == "space" {
				if s == "space" {
					s = " "
				}
				m.input += s
			}
		}
		return nil
	}
	rows := m.rows()
	switch s {
	case "c":
		m.typing, m.input = true, m.target
	case "up", "k":
		m.cursor = max(0, m.cursor-1)
	case "down", "j":
		m.cursor = min(len(rows)-1, m.cursor+1)
	case "right", "l", "enter":
		if m.cursor < len(rows) {
			return m.expand(rows[m.cursor])
		}
	case "left", "h":
		if m.cursor < len(rows) {
			rows[m.cursor].expanded = false
		}
	case "u": // niveau appelant
		if m.status == paused && m.frameSel+1 < len(m.frames) {
			m.frameSel++
			return m.selectFrame()
		}
	case "d": // niveau appelé
		if m.status == paused && m.frameSel > 0 {
			m.frameSel--
			return m.selectFrame()
		}
	case "o":
		if m.frameSel < len(m.frames) {
			f := m.frames[m.frameSel]
			return panel.Open(f.Source.Path, f.Line)
		}
	}
	m.cursor = min(max(0, m.cursor), max(0, len(m.rows())-1))
	return nil
}

// expand déplie une variable (en chargeant ses éléments au besoin).
func (m *Model) expand(n *node) tea.Cmd {
	if n.v.Ref == 0 {
		return nil
	}
	n.expanded = true
	if n.loaded || m.c == nil {
		return nil
	}
	c, ref := m.c, n.v.Ref
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		vars, err := c.Variables(ctx, ref)
		return childrenMsg{n: n, vars: vars, err: err}
	}
}

// rows renvoie les variables visibles (arbre déplié).
func (m *Model) rows() []*node {
	var out []*node
	var walk func(ns []*node)
	walk = func(ns []*node) {
		for _, n := range ns {
			out = append(out, n)
			if n.expanded {
				walk(n.children)
			}
		}
	}
	walk(m.vars)
	return out
}

// ─── Vue ─────────────────────────────────────────────────────────────────────

func (m *Model) View(width, height int) string {
	t := m.th
	if height < 6 {
		return ""
	}
	var b []string
	b = append(b, m.viewStatus(width), "")

	// Pile d'appels (au plus 6 lignes).
	b = append(b, t.Label.Render(i18n.T("Pile d'appels")))
	stackH := min(6, len(m.frames))
	if stackH == 0 {
		b = append(b, t.Dim.Render("  —"))
		stackH = 1
	}
	from := max(0, min(m.frameSel-stackH+1, len(m.frames)-stackH))
	for i := from; i < len(m.frames) && i < from+stackH; i++ {
		f := m.frames[i]
		mark := "  "
		if i == m.frameSel {
			mark = lipgloss.NewStyle().Foreground(t.P.Yellow).Render("▶ ")
		}
		loc := fmt.Sprintf("%s:%d", m.rel(f.Source.Path), f.Line)
		b = append(b, ansi.Truncate(mark+t.Text.Render(f.Name)+t.Dim.Render("  "+loc), width, "…"))
	}
	b = append(b, "")

	// Sortie du programme : le dernier tiers.
	outH := max(3, (height-len(b))/3)
	m.varsH = max(1, height-len(b)-outH-3)

	b = append(b, t.Label.Render(i18n.T("Variables")))
	rows := m.rows()
	if m.cursor < m.offset {
		m.offset = m.cursor
	} else if m.cursor >= m.offset+m.varsH {
		m.offset = m.cursor - m.varsH + 1
	}
	shown := 0
	if len(rows) == 0 {
		b = append(b, t.Dim.Render("  —"))
		shown = 1
	}
	for i := m.offset; i < len(rows) && shown < m.varsH; i++ {
		b = append(b, m.viewVar(rows[i], i == m.cursor, width))
		shown++
	}
	for ; shown < m.varsH; shown++ {
		b = append(b, "")
	}

	b = append(b, "", t.Label.Render(i18n.T("Sortie")))
	out := m.output
	if m.partial != "" {
		out = append(out[:len(out):len(out)], m.partial)
	}
	for _, l := range out[max(0, len(out)-(outH-1)):] {
		b = append(b, ansi.Truncate(t.Text.Render(l), width, "…"))
	}
	return lipgloss.NewStyle().Height(height).MaxHeight(height).Render(strings.Join(b, "\n"))
}

func (m *Model) viewStatus(w int) string {
	t := m.th
	var s string
	switch m.status {
	case idle:
		s = t.Dim.Render(fmt.Sprintf(i18n.T("○ Aucune session — F5 pour déboguer %s (c pour changer de cible)"), m.target))
	case starting:
		s = t.Warn.Render(fmt.Sprintf(i18n.T("◌ Compilation et lancement de %s…"), m.target))
	case running:
		s = lipgloss.NewStyle().Foreground(t.P.Green).Render(i18n.T("▶ En cours — F5 pour interrompre"))
	case paused:
		where := ""
		if len(m.frames) > 0 {
			f := m.frames[0]
			where = fmt.Sprintf(" — %s:%d", m.rel(f.Source.Path), f.Line)
		}
		s = lipgloss.NewStyle().Foreground(t.P.Yellow).Bold(true).Render(fmt.Sprintf(i18n.T("❚❚ En pause (%s)"), m.reason) + where)
	case ended:
		s = t.Dim.Render(fmt.Sprintf(i18n.T("■ Session terminée — F5 pour relancer %s"), m.target))
	}
	if m.typing {
		s = t.Label.Render(i18n.T("Cible : ")) + t.Value.Render(m.input) + t.Text.Reverse(true).Render(" ") +
			t.Dim.Render(i18n.T("   ex. « . », « ./cmd/outil -- arg », « test ./pkg -run TestX »"))
	}
	return ansi.Truncate(s, w, "…")
}

func (m *Model) viewVar(n *node, sel bool, w int) string {
	t := m.th
	mark := "  "
	switch {
	case n.v.Ref != 0 && n.expanded:
		mark = "▾ "
	case n.v.Ref != 0:
		mark = "▸ "
	}
	text := strings.Repeat("  ", n.depth) + mark + n.v.Name
	if n.v.Value != "" {
		text += " = " + strings.ReplaceAll(n.v.Value, "\n", " ")
	}
	if n.v.Type != "" && n.depth > 0 {
		text += "  (" + n.v.Type + ")"
	}
	text = ansi.Truncate(text, w, "…")
	if sel {
		return lipgloss.NewStyle().Background(t.P.Surface1).Foreground(t.P.Text).Render(text + strings.Repeat(" ", max(0, w-ansi.StringWidth(text))))
	}
	if n.depth == 0 {
		return t.Label.Render(text)
	}
	return t.Text.Render(text)
}

func (m *Model) rel(path string) string {
	if r, err := filepath.Rel(m.proj.Root, path); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return path
}
