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
}

func New(th *theme.Theme, p *project.Project) *Model {
	return &Model{th: th, proj: p, results: map[string][]diag.Diagnostic{},
		state: map[string]*toolState{}, source: map[string][]string{}}
}

func (m *Model) Title() string { return i18n.N("Diagnostics") }
func (m *Model) Icon() string  { return "" }

// Init lance une première analyse : elle tourne en arrière-plan pendant
// qu'on consulte les autres modules.
func (m *Model) Init() tea.Cmd { return m.runAll() }

func (m *Model) Hints() []panel.Hint {
	return []panel.Hint{
		{Key: "a", Desc: i18n.T("relancer")}, {Key: "Entrée", Desc: i18n.T("ouvrir")},
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
	switch msg := msg.(type) {
	case toolDoneMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.results[msg.tool] = msg.diags
		m.state[msg.tool] = &toolState{err: msg.err, dur: msg.dur, count: len(msg.diags)}
		m.merge()
		if m.running() {
			return m, nil
		}
		m.cancel = nil
		e, w, i := diag.Counts(m.all)
		if e+w+i == 0 {
			return m, panel.Flash(i18n.T("Diagnostics : aucun problème"), false)
		}
		return m, panel.Flash(fmt.Sprintf(i18n.T("Diagnostics : %d erreur(s), %d avertissement(s), %d info(s)"), e, w, i), e > 0)

	case spinMsg:
		if msg.gen == m.gen && m.running() {
			m.spin++
			return m, spin(msg.gen)
		}

	case tea.KeyPressMsg:
		return m, m.handleKey(msg.String())
	}
	return m, nil
}

func (m *Model) handleKey(s string) tea.Cmd {
	switch s {
	case "a", "f5":
		return m.runAll()
	case "enter", "o":
		if d := m.selected(); d != nil {
			return panel.Open(d.File, d.Line)
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
	filter := t.Dim.Render(i18n.T("filtre ")) + t.Value.Render(i18n.T(sevFilters[m.sevFilter].label))
	if m.toolFilter != "" {
		filter += t.Dim.Render(" · ") + t.Value.Render(m.toolFilter)
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
			lines = append(lines, ansi.Truncate(t.Label.Bold(true).Render(m.rel(r.file))+t.Dim.Render(fmt.Sprintf("  (%d)", n)), w, "…"))
			continue
		}
		loc := t.Dim.Render(fmt.Sprintf("%5d:%-3d", r.d.Line, r.d.Col))
		line := ansi.Truncate(loc+" "+m.sevIcon(r.d.Severity)+" "+t.Text.Render(i18n.T(r.d.Message)), w, "…")
		if i == m.cursor {
			line = lipgloss.NewStyle().Background(t.P.Surface0).Width(w).Render(line)
		}
		lines = append(lines, line)
	}
	return lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(strings.Join(lines, "\n"))
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
	lines = append(lines, m.excerpt(d, w)...)
	return lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(strings.Join(lines, "\n"))
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
