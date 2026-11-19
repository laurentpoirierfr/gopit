// Package diagnostics est le panneau « Diagnostics » : il lance go vet,
// staticcheck, gopls et gofmt en parallèle et affiche leurs problèmes,
// regroupés par fichier, avec un extrait du code source.
package diagnostics

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/diag"
	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/theme"
)

type toolDoneMsg struct {
	gen   int
	tool  string
	diags []diag.Diagnostic
	err   error
	dur   time.Duration
}

type spinMsg struct{ gen int }

// autoMsg relance l'analyse après un enregistrement (si aucun autre n'a
// suivi entre-temps).
type autoMsg struct{ gen int }

// autoDelay regroupe les enregistrements rapprochés en une seule analyse.
const autoDelay = 500 * time.Millisecond

type toolState struct {
	running bool
	missing bool
	err     error
	dur     time.Duration
	count   int
}

// Filtres de gravité, du plus large au plus strict.
var sevFilters = []struct {
	label string
	max   diag.Severity
}{
	{i18n.N("tout"), diag.Info},
	{i18n.N("erreurs + avertissements"), diag.Warning},
	{i18n.N("erreurs"), diag.Error},
}

// row est une ligne de la liste : un en-tête de fichier (d == nil) ou un
// diagnostic.
type row struct {
	file string
	d    *diag.Diagnostic
}

type Model struct {
	th   *theme.Theme
	proj *project.Project

	gen     int
	cancel  context.CancelFunc
	results map[string][]diag.Diagnostic
	state   map[string]*toolState
	all     []diag.Diagnostic

	sevFilter  int
	toolFilter string // "" : tous les outils

	rows   []row
	cursor int // indice dans rows (toujours sur un diagnostic)
	offset int
	listH  int
	spin   int
	source map[string][]string // lignes des fichiers, pour les extraits

	autoGen int  // dernier enregistrement reçu
	quiet   bool // analyse relancée automatiquement : pas de bilan affiché

	fixer     *fixer               // gopls pour les corrections (fix.go), à la demande
	fixes     map[string]*fixState // par diagnostic (key)
	fixSel    int                  // correction affichée, parmi celles du diagnostic
	fixOK     int                  // gopls disponible : 0 inconnu, 1 oui, -1 non
	fixFor    string               // diagnostic auquel se rapporte fixSel et detailOff
	ruleDocs  map[string]ruleDoc   // documentation des règles (explain.go), à la demande
	detailOff int                  // défilement du détail
	active    bool                 // le module a été utilisé : chercher les corrections
}

func New(th *theme.Theme, p *project.Project) *Model {
	return &Model{th: th, proj: p, results: map[string][]diag.Diagnostic{},
		state: map[string]*toolState{}, source: map[string][]string{}, fixes: map[string]*fixState{}}
}

func (m *Model) Title() string { return i18n.N("Diagnostics") }
func (m *Model) Icon() string  { return "" }

// Init lance une première analyse : elle tourne en arrière-plan pendant
// qu'on consulte les autres modules.
func (m *Model) Init() tea.Cmd { return m.runAll() }

func (m *Model) Hints() []panel.Hint {
	return []panel.Hint{
		{Key: "a", Desc: i18n.T("relancer")}, {Key: "Entrée", Desc: i18n.T("ouvrir")},
		{Key: "f", Desc: i18n.T("corriger")}, {Key: "w", Desc: i18n.T("doc de la règle")}, {Key: "Maj+↑↓", Desc: i18n.T("défiler")},
		{Key: "s", Desc: i18n.T("gravité")}, {Key: "t", Desc: i18n.T("outil")},
	}
}

func (m *Model) running() bool {
	for _, s := range m.state {
		if s.running {
			return true
		}
	}
	return false
}

// ─── Exécution ───────────────────────────────────────────────────────────────

func (m *Model) runAll() tea.Cmd {
	if m.cancel != nil {
		m.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.gen++
	m.source = map[string][]string{}
	m.fixes = map[string]*fixState{} // le code a pu changer
	gen, root := m.gen, m.proj.Root

	cmds := []tea.Cmd{spin(gen)}
	for _, tool := range diag.Tools {
		if !tool.Available() {
			m.state[tool.Name] = &toolState{missing: true}
			delete(m.results, tool.Name)
			continue
		}
		m.state[tool.Name] = &toolState{running: true}
		cmds = append(cmds, func() tea.Msg {
			start := time.Now()
			ds, err := tool.Run(ctx, root)
			return toolDoneMsg{gen: gen, tool: tool.Name, diags: ds, err: err, dur: time.Since(start)}
		})
	}
	m.merge()
	return tea.Batch(cmds...)
}

func spin(gen int) tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return spinMsg{gen} })
}

func (m *Model) merge() {
	var lists [][]diag.Diagnostic
	for _, t := range diag.Tools {
		lists = append(lists, m.results[t.Name])
	}
	m.all = diag.Merge(lists...)
	m.rebuild()
}

// ─── Update ──────────────────────────────────────────────────────────────────

func (m *Model) Update(msg tea.Msg) (panel.Panel, tea.Cmd) {
	cmd := m.update(msg)
	switch msg.(type) {
	case tea.KeyPressMsg, panel.ClickMsg, panel.WheelMsg:
		m.active = true
	}
	if d := m.selected(); d != nil && key(d) != m.fixFor {
		m.fixFor, m.fixSel, m.detailOff = key(d), 0, 0 // autre diagnostic : sa première correction
	}
	if m.active { // corrections du diagnostic choisi, s'il a changé
		return m, tea.Batch(cmd, m.ensureFix(), m.ensureRuleDocs())
	}
	return m, cmd
}

func (m *Model) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case fixesMsg:
		if st := m.fixes[msg.key]; st != nil {
			*st = fixState{fixes: msg.fixes, rule: msg.rule, err: msg.err}
		}
		return nil

	case ruleDocsMsg:
		m.ruleDocs = msg.docs
		if m.ruleDocs == nil {
			m.ruleDocs = map[string]ruleDoc{} // gopls absent : pas d'explications
		}
		return nil

	case toolDoneMsg:
		if msg.gen != m.gen {
			return nil
		}
		m.results[msg.tool] = msg.diags
		m.state[msg.tool] = &toolState{err: msg.err, dur: msg.dur, count: len(msg.diags)}
		m.merge()
		if m.running() {
			return nil
		}
		m.cancel = nil
		if m.quiet {
			m.quiet = false
			return nil
		}
		e, w, i := diag.Counts(m.all)
		if e+w+i == 0 {
			return panel.Flash(i18n.T("Diagnostics : aucun problème"), false)
		}
		return panel.Flash(fmt.Sprintf(i18n.T("Diagnostics : %d erreur(s), %d avertissement(s), %d info(s)"), e, w, i), e > 0)

	case panel.SavedMsg:
		// Fichier Go (ou go.mod) enregistré dans l'éditeur : l'analyse est
		// relancée peu après, sans bilan (l'éditeur souligne déjà les
		// erreurs).
		if base := filepath.Base(msg.Path); strings.HasSuffix(base, ".go") || base == "go.mod" || base == "go.sum" {
			m.autoGen++
			gen := m.autoGen
			return tea.Tick(autoDelay, func(time.Time) tea.Msg { return autoMsg{gen} })
		}

	case panel.RepoChangedMsg: // autre branche : le code a changé
		m.autoGen++
		gen := m.autoGen
		return tea.Tick(autoDelay, func(time.Time) tea.Msg { return autoMsg{gen} })

	case autoMsg:
		if msg.gen == m.autoGen {
			m.quiet = true
			return m.runAll()
		}

	case spinMsg:
		if msg.gen == m.gen && m.running() {
			m.spin++
			return spin(msg.gen)
		}

	case tea.KeyPressMsg:
		return m.handleKey(msg.String())

	case panel.ClickMsg:
		return m.click(msg)

	case panel.WheelMsg:
		switch {
		case panel.Has(msg.Zones, "list"):
			m.move(msg.Delta)
		case panel.Has(msg.Zones, "detail"):
			m.detailOff = max(0, m.detailOff+msg.Delta)
		}
	}
	return nil
}

// click choisit le diagnostic cliqué (le premier du fichier, sur un
// en-tête) ; un double clic l'ouvre. Un clic sur le filtre le change.
func (m *Model) click(c panel.ClickMsg) tea.Cmd {
	if c.Drag {
		return nil
	}
	switch {
	case panel.Has(c.Zones, "fix:apply"):
		return m.handleKey("f")
	case panel.Has(c.Zones, "fix:next"):
		return m.handleKey("n")
	case panel.Has(c.Zones, "doc:web"):
		return m.handleKey("w")
	}
	if panel.Has(c.Zones, "sev") {
		return m.handleKey("s")
	}
	if panel.Has(c.Zones, "tool") {
		return m.handleKey("t")
	}
	i, ok := panel.Index(c.Zones, "row:")
	if !ok || i < 0 || i >= len(m.rows) {
		return nil
	}
	for i < len(m.rows) && m.rows[i].d == nil {
		i++
	}
	if i < len(m.rows) {
		m.cursor = i
	}
	if c.Double {
		return m.handleKey("enter")
	}
	return nil
}

func (m *Model) handleKey(s string) tea.Cmd {
	switch s {
	case "a":
		m.quiet = false
		return m.runAll()
	case "enter", "o":
		if d := m.selected(); d != nil {
			return panel.Open(d.File, d.Line)
		}
	case "f": // appliquer la correction (ou la chercher)
		if x, _ := m.currentFix(); x != nil {
			return m.applyFix()
		}
		if d := m.selected(); d != nil {
			if st := m.fixes[key(d)]; st != nil && !st.loading {
				return panel.Flash(i18n.T("Pas de correction automatique pour ce diagnostic"), false)
			}
		}
	case "w":
		return m.openRuleDoc()
	case "shift+up":
		m.detailOff = max(0, m.detailOff-1)
	case "shift+down":
		m.detailOff++
	case "n": // correction suivante
		if _, n := m.currentFix(); n > 1 {
			m.fixSel = (m.fixSel + 1) % n
		}
	case "s":
		m.sevFilter = (m.sevFilter + 1) % len(sevFilters)
		m.rebuild()
		return panel.Flash(i18n.T("Gravité : ")+i18n.T(sevFilters[m.sevFilter].label), false)
	case "t":
		m.toolFilter = m.nextTool()
		m.rebuild()
		label := m.toolFilter
		if label == "" {
			label = i18n.T("tous")
		}
		return panel.Flash(i18n.T("Outil : ")+label, false)
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup":
		m.move(-max(1, m.listH-1))
	case "pgdown":
		m.move(max(1, m.listH-1))
	case "home", "g":
		m.move(-len(m.rows))
	case "end", "G":
		m.move(len(m.rows))
	}
	return nil
}

// nextTool passe au filtre d'outil suivant (parmi les outils installés).
func (m *Model) nextTool() string {
	opts := []string{""}
	for _, t := range diag.Tools {
		if st := m.state[t.Name]; st != nil && !st.missing {
			opts = append(opts, t.Name)
		}
	}
	for i, o := range opts {
		if o == m.toolFilter {
			return opts[(i+1)%len(opts)]
		}
	}
	return ""
}

// move déplace le curseur de delta diagnostics (les en-têtes sont sautés).
func (m *Model) move(delta int) {
	var idx []int
	for i, r := range m.rows {
		if r.d != nil {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		return
	}
	pos := 0
	for i, r := range idx {
		if r == m.cursor {
			pos = i
		}
	}
	pos = min(max(pos+delta, 0), len(idx)-1)
	m.cursor = idx[pos]
}

func (m *Model) selected() *diag.Diagnostic {
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		return m.rows[m.cursor].d
	}
	return nil
}

func key(d *diag.Diagnostic) string {
	return fmt.Sprintf("%s:%d:%s", d.File, d.Line, d.Message)
}

// rebuild applique les filtres et regroupe par fichier, en gardant la
// sélection si elle est toujours visible.
func (m *Model) rebuild() {
	var sel string
	if d := m.selected(); d != nil {
		sel = key(d)
	}
	m.rows = m.rows[:0]
	last := ""
	for i := range m.all {
		d := &m.all[i]
		if d.Severity > sevFilters[m.sevFilter].max {
			continue
		}
		if m.toolFilter != "" && !slices.Contains(d.Tools, m.toolFilter) {
			continue
		}
		if d.File != last {
			m.rows = append(m.rows, row{file: d.File})
			last = d.File
		}
		m.rows = append(m.rows, row{file: d.File, d: d})
	}
	m.cursor = -1
	for i, r := range m.rows {
		if r.d != nil && (m.cursor < 0 || key(r.d) == sel) {
			m.cursor = i
			if key(r.d) == sel {
				break
			}
		}
	}
}

// ─── Vue ─────────────────────────────────────────────────────────────────────

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (m *Model) sevIcon(s diag.Severity) string {
	switch s {
	case diag.Error:
		return m.th.Err.Render("✗")
	case diag.Warning:
		return m.th.Warn.Render("⚠")
	}
	return lipgloss.NewStyle().Foreground(m.th.P.Blue).Render("ℹ")
}

func (m *Model) rel(file string) string {
	if r, err := filepath.Rel(m.proj.Root, file); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return file
}

func (m *Model) View(width, height int) string {
	head := m.viewSummary(width)
	bodyH := max(1, height-len(head)-1)
	top := strings.Join(head, "\n") + "\n\n"

	if len(m.rows) == 0 {
		t := m.th
		var msg string
		switch {
		case m.running():
			msg = t.Dim.Render(i18n.T("Analyse en cours…"))
		case len(m.all) > 0:
			msg = t.Dim.Render(i18n.T("Aucun diagnostic pour ce filtre (s : gravité, t : outil)."))
		default:
			msg = t.OK.Render(i18n.T("✓ Aucun problème trouvé."))
		}
		return top + msg
	}

	listW := max(30, width*50/100)
	detailW := max(10, width-listW-3)
	sep := m.th.Dim.Render(strings.TrimSuffix(strings.Repeat("│\n", bodyH), "\n"))
	return top + lipgloss.JoinHorizontal(lipgloss.Top,
		m.viewList(listW, bodyH), " ", sep, " ", m.viewDetail(detailW, bodyH))
}

// viewSummary renvoie l'état des outils, les compteurs et, si besoin, les
// outils à installer.
func (m *Model) viewSummary(width int) []string {
	t := m.th
	var tools []string
	var missing []diag.Tool
	for _, tool := range diag.Tools {
		st := m.state[tool.Name]
		var s string
		switch {
		case st == nil:
			s = t.Dim.Render(tool.Name)
		case st.missing:
			s = t.Dim.Render(tool.Name + " –")
			missing = append(missing, tool)
		case st.running:
			s = t.Text.Render(tool.Name+" ") + lipgloss.NewStyle().Foreground(t.P.Blue).Render(spinner[m.spin%len(spinner)])
		case st.err != nil:
			s = t.Err.Render(tool.Name + " ✗")
		default:
			s = t.Text.Render(tool.Name) + t.OK.Render(" ✓") + t.Dim.Render(fmt.Sprintf(" %.1fs", st.dur.Seconds()))
		}
		tools = append(tools, s)
	}

	e, w, i := diag.Counts(m.all)
	counts := t.Err.Render(fmt.Sprintf("✗ %d", e)) + "  " + t.Warn.Render(fmt.Sprintf("⚠ %d", w)) + "  " +
		lipgloss.NewStyle().Foreground(t.P.Blue).Render(fmt.Sprintf("ℹ %d", i))
	filter := panel.Zone("sev", t.Dim.Render(i18n.T("filtre "))+t.Value.Render(i18n.T(sevFilters[m.sevFilter].label)))
	if m.toolFilter != "" {
		filter += t.Dim.Render(" · ") + panel.Zone("tool", t.Value.Render(m.toolFilter))
	}

	lines := []string{
		ansi.Truncate(counts+t.Dim.Render("   │   ")+strings.Join(tools, "   ")+t.Dim.Render("   │   ")+filter, width, "…"),
	}
	for _, tool := range missing {
		if tool.Install != "" {
			lines = append(lines, ansi.Truncate(t.Dim.Render(tool.Name+i18n.T(" n'est pas installé : "))+t.Text.Render(tool.Install), width, "…"))
		}
	}
	for _, tool := range diag.Tools {
		if st := m.state[tool.Name]; st != nil && st.err != nil && !st.missing {
			lines = append(lines, ansi.Truncate(t.Err.Render(tool.Name+" : "+st.err.Error()), width, "…"))
		}
	}
	return lines
}

func (m *Model) viewList(w, h int) string {
	t := m.th
	m.listH = h
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	// Montrer l'en-tête du fichier quand on remonte sur son premier diagnostic.
	if m.offset > 0 && m.cursor == m.offset && m.rows[m.offset-1].d == nil {
		m.offset--
	}
	m.offset = min(max(m.offset, 0), max(0, len(m.rows)-h))

	lines := make([]string, 0, h)
	for i := m.offset; i < len(m.rows) && len(lines) < h; i++ {
		r := m.rows[i]
		if r.d == nil {
			n := 0
			for _, x := range m.rows {
				if x.d != nil && x.file == r.file {
					n++
				}
			}
			lines = append(lines, panel.Row(fmt.Sprint("row:", i), w,
				ansi.Truncate(t.Label.Bold(true).Render(m.rel(r.file))+t.Dim.Render(fmt.Sprintf("  (%d)", n)), w, "…")))
			continue
		}
		loc := t.Dim.Render(fmt.Sprintf("%5d:%-3d", r.d.Line, r.d.Col))
		line := ansi.Truncate(loc+" "+m.sevIcon(r.d.Severity)+" "+t.Text.Render(i18n.T(r.d.Message)), w, "…")
		if i == m.cursor {
			line = lipgloss.NewStyle().Background(t.P.Surface0).Width(w).Render(line)
		}
		lines = append(lines, panel.Row(fmt.Sprint("row:", i), w, line))
	}
	return panel.Zone("list", lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(strings.Join(lines, "\n")))
}

func (m *Model) viewDetail(w, h int) string {
	t := m.th
	d := m.selected()
	if d == nil {
		return lipgloss.NewStyle().Width(w).Height(h).Render(t.Dim.Render(i18n.T("Aucune sélection")))
	}

	wrap := lipgloss.NewStyle().Width(w)
	lines := []string{
		m.sevIcon(d.Severity) + " " + t.Value.Render(d.Severity.String()),
		"",
		wrap.Render(t.Text.Render(i18n.T(d.Message))),
		"",
		t.Label.Render(fmt.Sprintf("%-13s", i18n.T("Emplacement"))) + t.Text.Render(fmt.Sprintf("%s:%d:%d", m.rel(d.File), d.Line, d.Col)),
	}
	if d.Code != "" {
		lines = append(lines, t.Label.Render(fmt.Sprintf("%-13s", i18n.T("Règle")))+t.Text.Render(d.Code))
	}
	lines = append(lines, t.Label.Render(fmt.Sprintf("%-13s", i18n.T("Signalé par")))+t.Text.Render(strings.Join(d.Tools, ", ")), "")
	if fx := m.viewFix(d, w); fx != nil {
		lines = append(lines, fx...)
		lines = append(lines, "")
	}
	if ex := m.viewExplain(d, w, 14); ex != nil {
		lines = append(lines, ex...)
		lines = append(lines, "")
	}
	lines = append(lines, m.excerpt(d, w)...)
	m.detailOff = min(m.detailOff, max(0, len(lines)-h))
	return panel.Zone("detail", lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(strings.Join(lines[m.detailOff:], "\n")))
}

// viewFix renvoie la section « Correction proposée » du diagnostic d : état
// de la recherche, ou aperçu de la correction et boutons.
func (m *Model) viewFix(d *diag.Diagnostic, w int) []string {
	t := m.th
	st := m.fixes[key(d)]
	head := t.Label.Render(i18n.T("Correction"))
	switch {
	case st == nil && strings.HasSuffix(d.File, ".go") && (d.Code == "gofmt" || m.canFix()):
		return []string{head + t.Dim.Render(i18n.T("  f : en chercher une (gopls)"))}
	case st == nil:
		return nil
	case st.loading:
		return []string{head + t.Dim.Render(i18n.T("  recherche avec gopls…"))}
	case st.err != nil && len(st.fixes) == 0:
		return []string{ansi.Truncate(head+t.Dim.Render("  ")+t.Err.Render(st.err.Error()), w, "…")}
	case len(st.fixes) == 0:
		return []string{head + t.Dim.Render(i18n.T("  aucune correction automatique"))}
	}
	x, n := m.currentFix()
	apply := panel.Zone("fix:apply", lipgloss.NewStyle().Foreground(t.P.Base).Background(t.P.Green).Bold(true).Render(i18n.T(" f appliquer ")))
	title := head + "  " + apply
	if n > 1 {
		title += " " + panel.Zone("fix:next", t.Dim.Render(fmt.Sprintf(i18n.T("n suivante (%d/%d)"), m.fixSel%n+1, n)))
	}
	out := []string{ansi.Truncate(title, w, "…"), ansi.Truncate(t.Value.Render(x.title), w, "…")}
	del := lipgloss.NewStyle().Foreground(t.P.Red)
	add := lipgloss.NewStyle().Foreground(t.P.Green)
	for _, l := range x.preview(1, 10) {
		code := strings.ReplaceAll(l.text, "\t", "    ")
		num := fmt.Sprintf("%5d ", l.num)
		switch l.op {
		case '-':
			out = append(out, ansi.Truncate(t.Dim.Render(num)+del.Render("- "+code), w, "…"))
		case '+':
			out = append(out, ansi.Truncate(t.Dim.Render(num)+add.Render("+ "+code), w, "…"))
		case '…':
			out = append(out, t.Dim.Render("      ⋮"))
		default:
			out = append(out, ansi.Truncate(t.Dim.Render(num+"  "+code), w, "…"))
		}
	}
	return out
}

// excerpt renvoie quelques lignes de code autour du diagnostic.
func (m *Model) excerpt(d *diag.Diagnostic, w int) []string {
	t := m.th
	src, ok := m.source[d.File]
	if !ok {
		data, err := os.ReadFile(d.File)
		if err != nil {
			m.source[d.File] = nil
			return nil
		}
		src = strings.Split(string(data), "\n")
		m.source[d.File] = src
	}
	if d.Line < 1 || d.Line > len(src) {
		return nil
	}
	const ctx = 4
	numW := len(fmt.Sprint(min(len(src), d.Line+ctx)))
	var out []string
	for n := max(1, d.Line-ctx); n <= min(len(src), d.Line+ctx); n++ {
		code := strings.ReplaceAll(src[n-1], "\t", "    ")
		num := fmt.Sprintf("%*d ", numW, n)
		if n == d.Line {
			out = append(out, ansi.Truncate(t.Warn.Render("▶")+t.Value.Render(num)+t.Text.Render(code), w, "…"))
			continue
		}
		out = append(out, ansi.Truncate(" "+t.Dim.Render(num)+t.Dim.Render(code), w, "…"))
	}
	return out
}
