// Package benchmarks est le panneau « Benchmarks » : il lance go test -bench,
// garde l'historique des exécutions et compare la dernière à une exécution
// de référence.
package benchmarks

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/bench"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/theme"
)

// keepRuns est le nombre d'exécutions conservées dans l'historique.
const keepRuns = 50

var counts = []int{1, 3, 5, 10}

// benchTimes sont les durées de mesure proposées (-benchtime) ; 1s est la
// valeur par défaut de go test.
var benchTimes = []string{"200ms", "1s", "3s"}

type historyMsg struct {
	runs []*bench.Run
	err  error
}

type linesMsg struct {
	run   int
	lines []string
	done  bool
	err   error
}

type spinMsg struct{ run int }

type row struct {
	header string
	res    *bench.Result
}

type Model struct {
	th   *theme.Theme
	proj *project.Project

	hist    *bench.History
	histErr error
	runs    []*bench.Run
	base    int // exécution de référence (-1 : aucune)

	// Exécution en cours.
	exec    *bench.Exec
	parser  *bench.Parser
	cancel  context.CancelFunc
	runID   int
	label   string
	started time.Time
	current string // dernier benchmark mesuré
	output  []string
	runErr  error

	countIdx int
	timeIdx  int       // durée de mesure (benchTimes)
	pr       profState // profilage et vue profil (profile.go)
	rows     []row
	cursor   int
	offset   int
	spin     int
}

func New(th *theme.Theme, p *project.Project) *Model {
	m := &Model{th: th, proj: p, base: -1, countIdx: 2, timeIdx: 1}
	m.hist, m.histErr = bench.HistoryFor(p.Module)
	return m
}

func (m *Model) Title() string { return "Benchmarks" }
func (m *Model) Icon() string  { return "" }

func (m *Model) Init() tea.Cmd {
	if m.hist == nil {
		return nil
	}
	h := m.hist
	return func() tea.Msg {
		runs, err := h.Load()
		return historyMsg{runs: runs, err: err}
	}
}

func (m *Model) Hints() []panel.Hint {
	switch {
	case m.pr.open:
		return m.profileHints()
	case m.exec != nil:
		return []panel.Hint{{Key: "x", Desc: "arrêter"}}
	}
	return []panel.Hint{
		{Key: "a", Desc: "tout lancer"}, {Key: "Entrée", Desc: "lancer la sélection"},
		{Key: "b", Desc: "référence"}, {Key: "n", Desc: "répétitions"}, {Key: "t", Desc: "durée"},
		{Key: "p/m", Desc: "profil CPU/mémoire"},
	}
}

func (m *Model) count() int { return counts[m.countIdx] }

func (m *Model) benchTime() string { return benchTimes[m.timeIdx] }

// last renvoie la dernière exécution enregistrée, ou nil.
func (m *Model) last() *bench.Run {
	if len(m.runs) == 0 {
		return nil
	}
	return m.runs[len(m.runs)-1]
}

func (m *Model) baseline() *bench.Run {
	if m.base < 0 || m.base >= len(m.runs)-1 {
		return nil
	}
	return m.runs[m.base]
}

// results renvoie les benchmarks affichés : ceux de l'exécution en cours, ou
// ceux de la dernière exécution.
func (m *Model) results() []*bench.Result {
	if m.exec != nil {
		return m.parser.Results()
	}
	if r := m.last(); r != nil {
		return r.Results
	}
	return nil
}

// ─── Exécution ───────────────────────────────────────────────────────────────

func (m *Model) start(label string, o bench.Options) tea.Cmd {
	if m.cancel != nil {
		m.cancel()
	}
	if o.Count == 0 {
		o.Count = m.count()
	}
	if o.BenchTime == "" {
		o.BenchTime = m.benchTime()
	}
	ctx, cancel := context.WithCancel(context.Background())
	e, err := bench.Start(ctx, m.proj.Root, o)
	if err != nil {
		cancel()
		return panel.Flash("go test -bench : "+err.Error(), true)
	}
	m.runID++
	m.exec, m.parser, m.cancel, m.label = e, bench.NewParser(), cancel, label
	m.started, m.current, m.output, m.runErr = time.Now(), "", nil, nil
	m.rebuild()
	id := m.runID
	return tea.Batch(wait(id, e), spin(id))
}

func wait(id int, e *bench.Exec) tea.Cmd {
	return func() tea.Msg {
		line, ok := <-e.Lines
		if !ok {
			return linesMsg{run: id, done: true, err: e.Err()}
		}
		lines := []string{line}
		timeout := time.After(50 * time.Millisecond)
		for {
			select {
			case l, ok := <-e.Lines:
				if !ok {
					return linesMsg{run: id, lines: lines, done: true, err: e.Err()}
				}
				lines = append(lines, l)
			case <-timeout:
				return linesMsg{run: id, lines: lines}
			}
		}
	}
}

func spin(id int) tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return spinMsg{id} })
}

func (m *Model) runAll() tea.Cmd {
	m.abortProfile()
	return m.start("tous les benchmarks", bench.Options{})
}

func (m *Model) runSelected() tea.Cmd {
	m.abortProfile()
	r := m.selected()
	if r == nil {
		return m.runAll()
	}
	return m.start(r.Name, bench.Options{Pattern: pattern(r.Name), Pkgs: []string{r.Pkg}})
}

// pattern construit l'expression -bench qui sélectionne exactement un
// benchmark (sous-benchmarks compris : « Tri/taille_100 »).
func pattern(name string) string {
	parts := strings.Split(name, "/")
	for i, p := range parts {
		if i == 0 {
			p = "Benchmark" + p
		}
		parts[i] = "^" + regexp.QuoteMeta(p) + "$"
	}
	return strings.Join(parts, "/")
}

// finish enregistre l'exécution terminée dans l'historique.
func (m *Model) finish(err error) tea.Cmd {
	res := m.parser.Results()
	m.exec, m.cancel, m.runErr = nil, nil, err
	if len(res) == 0 {
		m.rebuild()
		if err != nil {
			return panel.Flash("Benchmarks : "+err.Error(), err != bench.ErrInterrupted)
		}
		return panel.Flash("Aucun benchmark trouvé (fonctions BenchmarkXxx)", true)
	}
	run := &bench.Run{Time: time.Now(), Label: m.label, Count: m.count(), CPU: m.parser.CPU(), Results: res}
	var saveErr error
	if m.hist != nil {
		saveErr = m.hist.Save(run)
		_ = m.hist.Prune(keepRuns)
	}
	m.runs = append(m.runs, run)
	if len(m.runs) > keepRuns {
		m.runs = m.runs[len(m.runs)-keepRuns:]
	}
	m.base = m.defaultBase()
	m.rebuild()
	switch {
	case saveErr != nil:
		return panel.Flash("Historique non enregistré : "+saveErr.Error(), true)
	case err != nil:
		return panel.Flash(fmt.Sprintf("%d benchmark(s) mesuré(s), puis : %v", len(res), err), true)
	}
	return panel.Flash(fmt.Sprintf("%d benchmark(s) mesuré(s) en %.1fs", len(res), time.Since(m.started).Seconds()), false)
}

// defaultBase choisit comme référence l'exécution précédente la plus
// récente qui partage au moins un benchmark avec la dernière.
func (m *Model) defaultBase() int {
	last := m.last()
	for i := len(m.runs) - 2; i >= 0; i-- {
		for _, r := range last.Results {
			if m.runs[i].Result(r.Key()) != nil {
				return i
			}
		}
	}
	return -1
}

// ─── Update ──────────────────────────────────────────────────────────────────

func (m *Model) Update(msg tea.Msg) (panel.Panel, tea.Cmd) {
	switch msg := msg.(type) {
	case historyMsg:
		m.runs, m.histErr = msg.runs, msg.err
		if len(m.runs) > 0 {
			m.base = m.defaultBase()
		}
		m.rebuild()

	case linesMsg:
		if msg.run != m.runID || m.exec == nil {
			return m, nil
		}
		for _, l := range msg.lines {
			if r := m.parser.Line(l); r != nil {
				m.current = r.Name
			} else if useful(l) {
				m.output = append(m.output, l) // erreurs de compilation, échecs, journaux…
			}
		}
		if !msg.done {
			m.rebuild()
			return m, wait(m.runID, m.exec)
		}
		if m.pr.kind != "" {
			return m, m.finishProfile(msg.err)
		}
		return m, m.finish(msg.err)

	case spinMsg:
		if msg.run == m.runID && m.exec != nil {
			m.spin++
			return m, spin(msg.run)
		}

	case tea.KeyPressMsg:
		return m, m.handleKey(msg.String())
	}
	return m, nil
}

// useful indique si une ligne de sortie (hors résultats) mérite d'être
// montrée : on écarte les en-têtes et le bilan normal de go test.
func useful(l string) bool {
	t := strings.TrimSpace(l)
	for _, p := range []string{"goos:", "goarch:", "pkg:", "cpu:", "ok ", "?   "} {
		if strings.HasPrefix(t, p) {
			return false
		}
	}
	return t != "" && t != "PASS"
}

func (m *Model) handleKey(s string) tea.Cmd {
	if m.pr.open {
		return m.profileKey(s)
	}
	switch s {
	case "p":
		return m.startProfile("cpu")
	case "m":
		return m.startProfile("mem")
	case "P":
		if m.pr.report != nil {
			m.pr.open = true
		}
	case "a", "f5":
		return m.runAll()
	case "enter":
		return m.runSelected()
	case "x":
		if m.cancel != nil {
			m.cancel()
		}
	case "n":
		m.countIdx = (m.countIdx + 1) % len(counts)
		return panel.Flash(fmt.Sprintf("Répétitions : -count=%d", m.count()), false)
	case "t":
		m.timeIdx = (m.timeIdx + 1) % len(benchTimes)
		return panel.Flash("Durée de chaque mesure : -benchtime="+m.benchTime(), false)
	case "b":
		return m.cycleBase()
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "home", "g":
		m.move(-len(m.rows))
	case "end", "G":
		m.move(len(m.rows))
	}
	return nil
}

// cycleBase passe à l'exécution de référence précédente (puis « aucune »).
func (m *Model) cycleBase() tea.Cmd {
	if len(m.runs) < 2 {
		return panel.Flash("Il faut au moins deux exécutions pour comparer", true)
	}
	switch {
	case m.base < 0:
		m.base = len(m.runs) - 2
	case m.base == 0:
		m.base = -1
	default:
		m.base--
	}
	if b := m.baseline(); b != nil {
		return panel.Flash(fmt.Sprintf("Référence : %s (%s)", b.Time.Format("02/01 15:04:05"), b.Label), false)
	}
	return panel.Flash("Sans référence", false)
}

func (m *Model) selected() *bench.Result {
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		return m.rows[m.cursor].res
	}
	return nil
}

func (m *Model) move(d int) {
	var idx []int
	for i, r := range m.rows {
		if r.res != nil {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		return
	}
	pos := 0
	for i, x := range idx {
		if x == m.cursor {
			pos = i
		}
	}
	m.cursor = idx[min(max(pos+d, 0), len(idx)-1)]
}

func (m *Model) rebuild() {
	sel := ""
	if r := m.selected(); r != nil {
		sel = r.Key()
	}
	m.rows = m.rows[:0]
	last := ""
	for _, r := range m.results() {
		if r.Pkg != last {
			m.rows = append(m.rows, row{header: r.Pkg})
			last = r.Pkg
		}
		m.rows = append(m.rows, row{res: r})
	}
	m.cursor = -1
	for i, r := range m.rows {
		if r.res == nil {
			continue
		}
		if m.cursor < 0 || r.res.Key() == sel {
			m.cursor = i
		}
		if r.res.Key() == sel {
			break
		}
	}
}

// ─── Mise en forme ───────────────────────────────────────────────────────────

// FormatValue présente une valeur selon son unité (durées, octets…).
func FormatValue(v float64, unit string) string {
	switch unit {
	case bench.NsOp:
		switch {
		case v >= 1e9:
			return fmt.Sprintf("%.2f s", v/1e9)
		case v >= 1e6:
			return fmt.Sprintf("%.2f ms", v/1e6)
		case v >= 1e3:
			return fmt.Sprintf("%.2f µs", v/1e3)
		}
		return fmt.Sprintf("%.1f ns", v)
	case bench.BytesOp:
		switch {
		case v >= 1<<20:
			return fmt.Sprintf("%.1f Mio", v/(1<<20))
		case v >= 1<<10:
			return fmt.Sprintf("%.1f Kio", v/(1<<10))
		}
		return fmt.Sprintf("%.0f o", v)
	case bench.AllocsOp:
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.4g %s", v, unit)
}

// deltaText met en forme une variation : en vert si la valeur baisse
// (plus rapide, moins d'allocations), en rouge si elle monte.
func (m *Model) deltaText(d bench.Delta) string {
	t := m.th
	switch {
	case d.Noise:
		return t.Dim.Render("≈")
	case math.IsInf(d.Pct, 1):
		return t.Err.Render("nouveau > 0")
	case d.Pct <= -0.5 && d.New.Mean > 0:
		// Gros gain : un rapport est plus parlant que « -99.9% ».
		return t.OK.Render("÷" + ratio(d.Old.Mean/d.New.Mean))
	case d.Pct >= 1:
		return t.Err.Render("×" + ratio(d.New.Mean/d.Old.Mean))
	case d.Pct < 0:
		return t.OK.Render(fmt.Sprintf("%+.1f%%", 100*d.Pct))
	}
	return t.Err.Render(fmt.Sprintf("%+.1f%%", 100*d.Pct))
}

// ratio présente un facteur : « 2.4 », « 37 », « 2454 ».
func ratio(r float64) string {
	if r < 10 {
		return fmt.Sprintf("%.1f", r)
	}
	return fmt.Sprintf("%.0f", r)
}

var sparks = []rune("▁▂▃▄▅▆▇█")

// sparkline dessine l'évolution d'une série (de la plus ancienne à la plus
// récente).
func sparkline(vals []float64) string {
	if len(vals) == 0 {
		return ""
	}
	lo, hi := vals[0], vals[0]
	for _, v := range vals {
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	var b strings.Builder
	for _, v := range vals {
		i := 0
		if hi > lo {
			i = int((v - lo) / (hi - lo) * float64(len(sparks)-1))
		}
		b.WriteRune(sparks[i])
	}
	return b.String()
}

// ─── Vue ─────────────────────────────────────────────────────────────────────

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (m *Model) View(width, height int) string {
	if m.pr.open && m.pr.report != nil {
		return m.viewProfile(width, height)
	}
	t := m.th
	top := ansi.Truncate(m.viewSummary(), width, "…") + "\n\n"
	bodyH := max(1, height-2)

	if len(m.rows) == 0 {
		var msg string
		switch {
		case m.exec != nil:
			msg = t.Dim.Render("Compilation et premières mesures…")
		case len(m.output) > 0:
			msg = t.Err.Render(strings.Join(m.output, "\n"))
		default:
			msg = t.Dim.Render("Appuie sur ") + t.Value.Render("a") +
				t.Dim.Render(" pour lancer les benchmarks (go test -run=^$ -bench=. -benchmem).")
		}
		return top + lipgloss.NewStyle().Width(width).MaxHeight(bodyH).Render(msg)
	}

	listW := max(34, width*50/100)
	detailW := max(10, width-listW-3)
	sep := t.Dim.Render(strings.TrimSuffix(strings.Repeat("│\n", bodyH), "\n"))
	return top + lipgloss.JoinHorizontal(lipgloss.Top, m.viewList(listW, bodyH), " ", sep, " ", m.viewDetail(detailW, bodyH))
}

func (m *Model) viewSummary() string {
	t := m.th
	sepr := t.Dim.Render("   │   ")
	var parts []string
	switch {
	case m.exec != nil:
		s := lipgloss.NewStyle().Foreground(t.P.Blue).Render(spinner[m.spin%len(spinner)]) +
			t.Text.Render(fmt.Sprintf(" %s… %.1fs", m.label, time.Since(m.started).Seconds()))
		if m.current != "" {
			s += t.Dim.Render(" · " + m.current)
		}
		parts = append(parts, s)
	case m.runErr != nil && m.runErr != bench.ErrInterrupted:
		parts = append(parts, t.Err.Render(m.runErr.Error()))
	case m.last() != nil:
		l := m.last()
		parts = append(parts, t.Text.Render(l.Label)+t.Dim.Render(" · "+l.Time.Format("02/01 15:04:05")))
	}
	if b := m.baseline(); b != nil {
		parts = append(parts, t.Dim.Render("référence ")+t.Value.Render(b.Time.Format("02/01 15:04:05")))
	} else if len(m.runs) > 0 {
		parts = append(parts, t.Dim.Render("sans référence"))
	}
	parts = append(parts, t.Dim.Render("-count=")+t.Value.Render(fmt.Sprint(m.count()))+
		t.Dim.Render(" -benchtime=")+t.Value.Render(m.benchTime()),
		t.Dim.Render(fmt.Sprintf("%d exécution(s) enregistrée(s)", len(m.runs))))
	if m.histErr != nil {
		parts = append(parts, t.Err.Render("historique : "+m.histErr.Error()))
	}
	return strings.Join(parts, sepr)
}

func (m *Model) viewList(w, h int) string {
	t := m.th
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	if m.offset > 0 && m.cursor == m.offset && m.rows[m.offset-1].res == nil {
		m.offset--
	}
	m.offset = min(max(m.offset, 0), max(0, len(m.rows)-h))
	base := m.baseline()

	lines := make([]string, 0, h)
	for i := m.offset; i < len(m.rows) && len(lines) < h; i++ {
		r := m.rows[i]
		if r.res == nil {
			name := strings.TrimPrefix(strings.TrimPrefix(r.header, m.proj.Module), "/")
			if name == "" {
				name = m.proj.Name()
			}
			lines = append(lines, ansi.Truncate(t.Label.Bold(true).Render(name), w, "…"))
			continue
		}
		right := ""
		if ns, ok := r.res.Metrics[bench.NsOp]; ok {
			right = t.Text.Render(fmt.Sprintf("%10s", FormatValue(ns.Mean, bench.NsOp)))
		}
		if base != nil && m.exec == nil {
			if d, ok := bench.Compare(base.Result(r.res.Key()), r.res, bench.NsOp); ok {
				right += " " + lipgloss.NewStyle().Width(8).Align(lipgloss.Right).Render(m.deltaText(d))
			} else {
				right += " " + lipgloss.NewStyle().Width(8).Align(lipgloss.Right).Render(t.Dim.Render("nouveau"))
			}
		}
		left := "  " + t.Text.Render(r.res.Name)
		room := w - lipgloss.Width(right) - 1
		left = ansi.Truncate(left, max(4, room), "…")
		pad := strings.Repeat(" ", max(1, w-lipgloss.Width(left)-lipgloss.Width(right)))
		line := left + pad + right
		if i == m.cursor {
			line = lipgloss.NewStyle().Background(t.P.Surface0).Width(w).Render(line)
		}
		lines = append(lines, line)
	}
	return lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(strings.Join(lines, "\n"))
}

func (m *Model) viewDetail(w, h int) string {
	t := m.th
	box := lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h)
	r := m.selected()
	if r == nil {
		return box.Render(t.Dim.Render("Aucune sélection"))
	}

	lines := []string{
		ansi.Truncate(t.Value.Render("Benchmark"+r.Name), w, "…"),
		t.Dim.Render(fmt.Sprintf("%s · GOMAXPROCS=%d · %d itérations", r.Pkg, r.Procs, r.Iters)),
		"",
	}

	// Tableau des métriques : référence, actuel, variation.
	base := m.baseline()
	var old *bench.Result
	if base != nil && m.exec == nil {
		old = base.Result(r.Key())
	}
	units := []string{bench.NsOp, bench.BytesOp, bench.AllocsOp}
	for unit := range r.Metrics {
		if unit != bench.NsOp && unit != bench.BytesOp && unit != bench.AllocsOp {
			units = append(units, unit)
		}
	}
	col := func(s string, n int) string { return lipgloss.NewStyle().Width(n).Align(lipgloss.Right).Render(s) }
	lines = append(lines, t.Label.Render(fmt.Sprintf("%-10s", ""))+col(t.Label.Render("référence"), 14)+col(t.Label.Render("actuel"), 14)+col(t.Label.Render("Δ"), 9))
	for _, unit := range units {
		cur, ok := r.Metrics[unit]
		if !ok {
			continue
		}
		oldS, delta := t.Dim.Render("–"), ""
		if d, ok := bench.Compare(old, r, unit); ok {
			oldS, delta = t.Text.Render(FormatValue(d.Old.Mean, unit)), m.deltaText(d)
		}
		lines = append(lines, ansi.Truncate(t.Label.Render(fmt.Sprintf("%-10s", unit))+col(oldS, 14)+
			col(t.Value.Render(FormatValue(cur.Mean, unit)), 14)+col(delta, 9), w, "…"))
	}
	if ns, ok := r.Metrics[bench.NsOp]; ok && ns.N > 1 {
		lines = append(lines, "", t.Dim.Render(fmt.Sprintf("%d mesures, écart ±%.1f%% (min %s, max %s)",
			ns.N, 100*ns.Spread(), FormatValue(ns.Min, bench.NsOp), FormatValue(ns.Max, bench.NsOp))))
	}

	// Historique de ns/op sur les exécutions enregistrées.
	var vals []float64
	for _, run := range m.runs {
		if res := run.Result(r.Key()); res != nil {
			if ns, ok := res.Metrics[bench.NsOp]; ok {
				vals = append(vals, ns.Mean)
			}
		}
	}
	if len(vals) > 1 {
		if len(vals) > w-4 {
			vals = vals[len(vals)-(w-4):]
		}
		lo, hi := vals[0], vals[0]
		for _, v := range vals {
			lo, hi = math.Min(lo, v), math.Max(hi, v)
		}
		lines = append(lines, "", t.Label.Render(fmt.Sprintf("Historique (%d exécutions)", len(vals))),
			lipgloss.NewStyle().Foreground(t.P.Accent).Render(sparkline(vals)),
			t.Dim.Render(fmt.Sprintf("min %s · max %s", FormatValue(lo, bench.NsOp), FormatValue(hi, bench.NsOp))))
	}

	if len(m.output) > 0 {
		lines = append(lines, "", t.Label.Render("Sortie"))
		for _, l := range m.output {
			lines = append(lines, ansi.Truncate(t.Text.Render(strings.ReplaceAll(l, "\t", "    ")), w, "…"))
		}
	}
	return box.Render(strings.Join(lines, "\n"))
}
