// Package tests est le panneau « Tests » : il lance go test -json et affiche
// en direct l'arbre paquets → tests, avec la sortie de l'élément sélectionné.
package tests

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/cover"
	"github.com/laurentpoirierfr/gopit/internal/gotest"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/theme"
	"github.com/laurentpoirierfr/gopit/internal/watch"
)

// ─── Messages ────────────────────────────────────────────────────────────────

// eventsMsg apporte un lot d'événements d'une exécution.
type eventsMsg struct {
	run    int
	events []gotest.Event
	done   bool
	err    error
}

type spinMsg struct{ run int }

// changeMsg signale des fichiers Go modifiés (surveillance active).
type changeMsg struct {
	dirs   []string
	closed bool
}

// affectedMsg donne les paquets à relancer après une modification.
type affectedMsg struct {
	pkgs []string
	err  error
}

// openMsg demande l'ouverture de l'éditeur une fois le fichier localisé.
type openMsg struct {
	file string
	line int
	err  error
}

// ─── Modèle ──────────────────────────────────────────────────────────────────

type row struct {
	pkg  *gotest.Package
	test *gotest.Test // nil pour une ligne de paquet
}

func (r row) key() string {
	if r.test == nil {
		return r.pkg.Path
	}
	return r.pkg.Path + "\x00" + r.test.Name
}

type Model struct {
	th   *theme.Theme
	proj *project.Project

	res     *gotest.Results
	run     *gotest.Run // exécution en cours
	runID   int
	cancel  context.CancelFunc
	running bool
	scope   string // description de l'exécution en cours / dernière
	started time.Time
	elapsed time.Duration
	runErr  error
	noCache bool
	showAll bool // afficher aussi les paquets sans tests

	cover     bool           // mesure de la couverture (touche v)
	coverFile string         // profil de l'exécution en cours
	prof      *cover.Profile // couverture cumulée des dernières exécutions

	watcher *watch.Watcher  // surveillance des fichiers (touche w)
	queued  map[string]bool // paquets modifiés pendant une exécution

	rows      []row
	cursor    int
	offset    int
	expanded  map[string]bool // choix explicite de l'utilisateur par paquet
	detailOff int             // défilement de la sortie (lignes depuis le bas)
	spin      int
	listH     int // hauteur de la liste au dernier rendu (pour pgup/pgdown)
}

func New(th *theme.Theme, p *project.Project) *Model {
	return &Model{th: th, proj: p, res: gotest.NewResults(), expanded: map[string]bool{}, queued: map[string]bool{}}
}

func (m *Model) Title() string { return "Tests" }
func (m *Model) Icon() string  { return "" }
func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Hints() []panel.Hint {
	if m.running {
		return []panel.Hint{{Key: "x", Desc: "arrêter"}, {Key: "Entrée", Desc: "relancer la sélection"}}
	}
	return []panel.Hint{
		{Key: "a", Desc: "tout"}, {Key: "Entrée", Desc: "lancer la sélection"},
		{Key: "f", Desc: "échecs"}, {Key: "o", Desc: "ouvrir l'erreur"},
		{Key: "w", Desc: "surveillance"}, {Key: "v", Desc: "couverture"}, {Key: "c", Desc: "cache"}, {Key: "h", Desc: "paquets sans tests"},
	}
}

// ─── Exécution ───────────────────────────────────────────────────────────────

// start lance go test avec les arguments donnés (motifs de paquets compris).
func (m *Model) start(scope string, args ...string) tea.Cmd {
	if m.cancel != nil {
		m.cancel()
	}
	if m.noCache {
		args = append([]string{"-count=1"}, args...)
	}
	if m.coverFile != "" {
		os.Remove(m.coverFile) // profil d'une exécution interrompue
		m.coverFile = ""
	}
	if m.cover {
		f, err := os.CreateTemp("", "gopit-cover-*.out")
		if err != nil {
			return panel.Flash("Couverture : "+err.Error(), true)
		}
		f.Close()
		m.coverFile = f.Name()
		args = append([]string{"-covermode=set", "-coverprofile=" + m.coverFile}, args...)
	}
	ctx, cancel := context.WithCancel(context.Background())
	run, err := gotest.Start(ctx, m.proj.Root, args...)
	if err != nil {
		cancel()
		return panel.Flash("go test : "+err.Error(), true)
	}
	m.runID++
	m.run, m.cancel, m.running, m.scope, m.runErr = run, cancel, true, scope, nil
	m.started, m.detailOff = time.Now(), 0
	id := m.runID
	return tea.Batch(wait(id, run), spin(id))
}

// wait lit les événements disponibles et les renvoie par lots, pour ne pas
// redessiner l'écran à chaque ligne de sortie.
func wait(id int, run *gotest.Run) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-run.Events
		if !ok {
			return eventsMsg{run: id, done: true, err: run.Err()}
		}
		evs := []gotest.Event{e}
		timeout := time.After(40 * time.Millisecond)
		for len(evs) < 1000 {
			select {
			case e, ok := <-run.Events:
				if !ok {
					return eventsMsg{run: id, events: evs, done: true, err: run.Err()}
				}
				evs = append(evs, e)
			case <-timeout:
				return eventsMsg{run: id, events: evs}
			}
		}
		return eventsMsg{run: id, events: evs}
	}
}

func spin(id int) tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return spinMsg{id} })
}

// ─── Couverture ──────────────────────────────────────────────────────────────

// readCoverage lit le profil de l'exécution terminée et le fusionne avec la
// couverture déjà connue.
func (m *Model) readCoverage() {
	if m.coverFile == "" {
		return
	}
	defer func() {
		os.Remove(m.coverFile)
		m.coverFile = ""
	}()
	f, err := os.Open(m.coverFile)
	if err != nil {
		return
	}
	defer f.Close()
	p, err := cover.Parse(f)
	if err != nil || len(p.Files) == 0 {
		return
	}
	if m.prof == nil {
		m.prof = cover.New()
	}
	m.prof.Merge(p)
}

func (m *Model) coverStyle(pct float64) lipgloss.Style {
	switch {
	case pct >= 80:
		return m.th.OK
	case pct >= 50:
		return m.th.Warn
	}
	return m.th.Err
}

// coverLines renvoie le tableau de couverture d'un paquet, fichier par
// fichier (du moins couvert au plus couvert).
func (m *Model) coverLines(pkg string, w int) []string {
	if m.prof == nil {
		return nil
	}
	files := m.prof.PackageFiles(pkg)
	if len(files) == 0 {
		return nil
	}
	t := m.th
	const barW = 12
	lines := []string{t.Label.Render("Couverture par fichier")}
	for _, f := range files {
		pct := f.Percent()
		full := int(pct/100*barW + 0.5)
		bar := m.coverStyle(pct).Render(strings.Repeat("█", full)) + t.Dim.Render(strings.Repeat("░", barW-full))
		c, tot := f.Stmts()
		line := m.coverStyle(pct).Render(fmt.Sprintf("%5.1f%%", pct)) + " " + bar + " " +
			t.Text.Render(path.Base(f.Name)) + t.Dim.Render(fmt.Sprintf("  %d/%d", c, tot))
		lines = append(lines, ansi.Truncate(line, w, "…"))
	}
	return append(lines, t.Dim.Render("o : ouvrir le premier bloc non couvert"), "")
}

// firstUncovered renvoie le premier bloc non couvert du fichier le moins
// couvert du paquet.
func (m *Model) firstUncovered(pkg string) (file string, line int, ok bool) {
	if m.prof == nil {
		return "", 0, false
	}
	for _, f := range m.prof.PackageFiles(pkg) {
		if u := f.Uncovered(); len(u) > 0 {
			if p, ok := cover.DiskPath(m.proj.Root, m.proj.Module, f.Name); ok {
				return p, u[0].StartLine, true
			}
		}
	}
	return "", 0, false
}

// ─── Surveillance des fichiers ───────────────────────────────────────────────

func (m *Model) toggleWatch() tea.Cmd {
	if m.watcher != nil {
		m.watcher.Close()
		m.watcher, m.queued = nil, map[string]bool{}
		return panel.Flash("Surveillance arrêtée", false)
	}
	w, err := watch.New(m.proj.Root, 300*time.Millisecond)
	if err != nil {
		return panel.Flash("Surveillance impossible : "+err.Error(), true)
	}
	m.watcher = w
	return tea.Batch(waitChange(w), panel.Flash("Surveillance active : les tests se relancent à chaque enregistrement", false))
}

func waitChange(w *watch.Watcher) tea.Cmd {
	return func() tea.Msg {
		dirs, ok := <-w.Changes
		return changeMsg{dirs: dirs, closed: !ok}
	}
}

// affected traduit les répertoires modifiés en paquets, puis ajoute ceux qui
// en dépendent (go list).
func (m *Model) affected(dirs []string) tea.Cmd {
	root, module := m.proj.Root, m.proj.Module
	return func() tea.Msg {
		var pkgs []string
		for _, d := range dirs {
			if d == watch.AllDirs {
				return affectedMsg{pkgs: []string{"./..."}}
			}
			rel, err := filepath.Rel(root, d)
			if err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			if rel == "." {
				pkgs = append(pkgs, module)
			} else {
				pkgs = append(pkgs, module+"/"+filepath.ToSlash(rel))
			}
		}
		deps, err := gotest.Dependents(context.Background(), root, pkgs)
		return affectedMsg{pkgs: deps, err: err}
	}
}

// runPkgs relance les tests des paquets donnés (relance automatique).
func (m *Model) runPkgs(pkgs []string) tea.Cmd {
	if len(pkgs) == 1 && pkgs[0] == "./..." {
		return m.runAll()
	}
	for _, p := range pkgs {
		m.res.ResetPackage(p)
	}
	scope := "modifié : " + strings.TrimPrefix(strings.TrimPrefix(pkgs[0], m.proj.Module), "/")
	if len(pkgs) > 1 {
		scope = fmt.Sprintf("modifiés : %d paquets", len(pkgs))
	}
	return m.start(scope, pkgs...)
}

func (m *Model) runAll() tea.Cmd {
	m.res = gotest.NewResults()
	m.cursor, m.offset = 0, 0
	return m.start("tous les tests", "./...")
}

func (m *Model) runSelected() tea.Cmd {
	r, ok := m.selected()
	if !ok {
		return m.runAll()
	}
	if r.test == nil {
		m.res.ResetPackage(r.pkg.Path)
		return m.start(r.pkg.Path, r.pkg.Path)
	}
	// Un sous-test se relance via son test de premier niveau ou directement.
	name := r.test.Name
	m.res.ResetTest(r.pkg.Path, name)
	m.expanded[r.pkg.Path] = true
	return m.start(name, "-run", gotest.RunPattern(name), r.pkg.Path)
}

func (m *Model) runFailures() tea.Cmd {
	fails := m.res.Failures()
	if len(fails) == 0 {
		return panel.Flash("Aucun échec à relancer", false)
	}
	var pkgs, names []string
	whole := false
	for path, tests := range fails {
		pkgs = append(pkgs, path)
		if tests == nil {
			whole = true
			m.res.ResetPackage(path)
			continue
		}
		for _, t := range tests {
			m.res.ResetTest(path, t)
			names = append(names, regexp.QuoteMeta(t))
		}
	}
	sort.Strings(pkgs)
	args := pkgs
	// Si un paquet doit être relancé en entier, pas de filtre -run.
	if !whole && len(names) > 0 {
		args = append([]string{"-run", "^(" + strings.Join(names, "|") + ")$"}, pkgs...)
	}
	return m.start(fmt.Sprintf("%d échec(s)", len(fails)), args...)
}

// ─── Update ──────────────────────────────────────────────────────────────────

func (m *Model) Update(msg tea.Msg) (panel.Panel, tea.Cmd) {
	switch msg := msg.(type) {
	case eventsMsg:
		if msg.run != m.runID {
			return m, nil
		}
		for _, e := range msg.events {
			m.res.Apply(e)
		}
		m.rebuild()
		if !msg.done {
			return m, wait(m.runID, m.run) // lire la suite
		}
		m.running, m.cancel, m.run = false, nil, nil
		m.elapsed, m.runErr = time.Since(m.started), msg.err
		m.readCoverage()
		c := m.res.Counts()
		var flash tea.Cmd
		switch {
		case msg.err != nil:
			flash = panel.Flash("Tests : "+msg.err.Error(), msg.err != gotest.ErrInterrupted)
		case c.Fail > 0:
			flash = panel.Flash(fmt.Sprintf("%d test(s) en échec", c.Fail), true)
		default:
			flash = panel.Flash("Tests réussis", false)
		}
		// Des fichiers ont changé pendant l'exécution : relancer ces paquets.
		if len(m.queued) > 0 && msg.err != gotest.ErrInterrupted {
			pkgs := make([]string, 0, len(m.queued))
			for p := range m.queued {
				pkgs = append(pkgs, p)
			}
			m.queued = map[string]bool{}
			sort.Strings(pkgs)
			return m, tea.Batch(flash, m.runPkgs(pkgs))
		}
		return m, flash

	case changeMsg:
		if msg.closed || m.watcher == nil {
			return m, nil
		}
		return m, tea.Batch(m.affected(msg.dirs), waitChange(m.watcher))

	case affectedMsg:
		if msg.err != nil {
			return m, panel.Flash("Surveillance : "+msg.err.Error(), true)
		}
		if len(msg.pkgs) == 0 || m.watcher == nil {
			return m, nil
		}
		if m.running {
			for _, p := range msg.pkgs {
				m.queued[p] = true
			}
			return m, nil
		}
		return m, m.runPkgs(msg.pkgs)

	case spinMsg:
		if msg.run == m.runID && m.running {
			m.spin++
			return m, spin(msg.run)
		}

	case openMsg:
		if msg.err != nil {
			return m, panel.Flash(msg.err.Error(), true)
		}
		return m, panel.Open(msg.file, msg.line)

	case tea.KeyPressMsg:
		return m, m.handleKey(msg.String())
	}
	return m, nil
}

func (m *Model) handleKey(s string) tea.Cmd {
	switch s {
	case "w":
		return m.toggleWatch()
	case "v":
		m.cover = !m.cover
		if m.cover {
			return panel.Flash("Couverture activée : elle sera mesurée aux prochaines exécutions (a pour tout relancer)", false)
		}
		return panel.Flash("Couverture désactivée", false)
	case "a", "f5":
		return m.runAll()
	case "enter":
		return m.runSelected()
	case "f":
		return m.runFailures()
	case "x":
		if m.cancel != nil {
			m.cancel()
		}
	case "c":
		m.noCache = !m.noCache
		if m.noCache {
			return panel.Flash("Cache désactivé (-count=1)", false)
		}
		return panel.Flash("Cache activé", false)
	case "h":
		m.showAll = !m.showAll
		m.rebuild()
	case "o":
		return m.openFailure()
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "home", "g":
		m.move(-len(m.rows))
	case "end", "G":
		m.move(len(m.rows))
	case "right", "l", "space", " ":
		m.toggle(true, s == "space" || s == " ")
	case "left":
		m.toggle(false, false)
	case "pgup":
		m.detailOff += max(1, m.listH/2)
	case "pgdown":
		m.detailOff = max(0, m.detailOff-max(1, m.listH/2))
	}
	return nil
}

func (m *Model) move(d int) {
	if len(m.rows) == 0 {
		return
	}
	m.cursor = min(max(m.cursor+d, 0), len(m.rows)-1)
	m.detailOff = 0
}

// toggle déplie (open) ou replie le paquet sélectionné ; flip inverse l'état.
func (m *Model) toggle(open, flip bool) {
	r, ok := m.selected()
	if !ok {
		return
	}
	if flip {
		open = !m.isExpanded(r.pkg)
	}
	if !open && r.test != nil {
		// Replier depuis un test : remonter sur le paquet.
		m.expanded[r.pkg.Path] = false
		m.rebuild()
		m.selectKey(r.pkg.Path)
		return
	}
	m.expanded[r.pkg.Path] = open
	m.rebuild()
}

func (m *Model) isExpanded(p *gotest.Package) bool {
	if v, ok := m.expanded[p.Path]; ok {
		return v
	}
	return p.Status == gotest.Fail // par défaut : déplier les paquets en échec
}

func (m *Model) selected() (row, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return row{}, false
	}
	return m.rows[m.cursor], true
}

// rebuild recalcule les lignes visibles en gardant la sélection.
func (m *Model) rebuild() {
	var key string
	if r, ok := m.selected(); ok {
		key = r.key()
	}
	m.rows = m.rows[:0]
	for _, p := range m.res.Packages {
		if p.NoTests && !m.showAll {
			continue
		}
		m.rows = append(m.rows, row{pkg: p})
		if m.isExpanded(p) {
			for _, t := range p.Tests {
				m.rows = append(m.rows, row{pkg: p, test: t})
			}
		}
	}
	if !m.selectKey(key) {
		m.cursor = min(max(m.cursor, 0), max(0, len(m.rows)-1))
	}
}

func (m *Model) selectKey(key string) bool {
	for i, r := range m.rows {
		if r.key() == key {
			m.cursor = i
			return true
		}
	}
	return false
}

// ─── Ouverture de l'erreur dans l'éditeur ────────────────────────────────────

// locRe repère « fichier.go:12 » dans la sortie d'un test.
var locRe = regexp.MustCompile(`([\w./\\-]+\.go):(\d+)`)

func (m *Model) openFailure() tea.Cmd {
	r, ok := m.selected()
	if !ok {
		return nil
	}
	lines := r.pkg.Output
	if r.test != nil {
		lines = r.test.Output
	}
	var file string
	var line int
	for _, l := range lines {
		if mt := locRe.FindStringSubmatch(l); mt != nil {
			file = mt[1]
			line, _ = strconv.Atoi(mt[2])
			break
		}
	}
	if file == "" && r.test == nil {
		if f, l, ok := m.firstUncovered(r.pkg.Path); ok {
			return panel.Open(f, l)
		}
	}
	if file == "" {
		return panel.Flash("Aucun emplacement fichier:ligne dans cette sortie", true)
	}
	root, pkg := m.proj.Root, r.pkg.Path
	return func() tea.Msg {
		if filepath.IsAbs(file) {
			return openMsg{file: file, line: line}
		}
		// Les chemins des tests sont relatifs au répertoire du paquet.
		cmd := exec.Command("go", "list", "-f", "{{.Dir}}", pkg)
		cmd.Dir = root
		out, err := cmd.Output()
		if err != nil {
			return openMsg{err: fmt.Errorf("répertoire de %s introuvable", pkg)}
		}
		return openMsg{file: filepath.Join(strings.TrimSpace(string(out)), file), line: line}
	}
}

// ─── Vue ─────────────────────────────────────────────────────────────────────

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (m *Model) icon(s gotest.Status) string {
	t := m.th
	switch s {
	case gotest.Running:
		return lipgloss.NewStyle().Foreground(t.P.Blue).Render(spinner[m.spin%len(spinner)])
	case gotest.Pass:
		return t.OK.Render("✓")
	case gotest.Fail:
		return t.Err.Render("✗")
	case gotest.Skip:
		return t.Warn.Render("↷")
	}
	return t.Dim.Render("·")
}

func (m *Model) View(width, height int) string {
	summary := m.viewSummary(width)
	bodyH := max(1, height-2)
	if len(m.res.Packages) == 0 && !m.running {
		msg := m.th.Dim.Render("Appuie sur ") + m.th.Value.Render("a") +
			m.th.Dim.Render(" pour lancer tous les tests (go test -json ./...).")
		return summary + "\n\n" + msg
	}

	listW := max(28, width*45/100)
	detailW := max(10, width-listW-3)
	list := m.viewList(listW, bodyH)
	sep := m.th.Dim.Render(strings.TrimSuffix(strings.Repeat("│\n", bodyH), "\n"))
	detail := m.viewDetail(detailW, bodyH)
	body := lipgloss.JoinHorizontal(lipgloss.Top, list, " ", sep, " ", detail)
	return summary + "\n\n" + body
}

func (m *Model) viewSummary(width int) string {
	t := m.th
	c := m.res.Counts()
	counts := t.OK.Render(fmt.Sprintf("✓ %d", c.Pass)) + "  " +
		t.Err.Render(fmt.Sprintf("✗ %d", c.Fail)) + "  " +
		t.Warn.Render(fmt.Sprintf("↷ %d", c.Skip))

	var state string
	switch {
	case m.running:
		state = lipgloss.NewStyle().Foreground(t.P.Blue).Render(spinner[m.spin%len(spinner)]) +
			t.Text.Render(fmt.Sprintf(" %s… %.1fs", m.scope, time.Since(m.started).Seconds()))
	case m.runErr != nil:
		state = t.Err.Render(m.scope + " : " + m.runErr.Error())
	case m.scope != "":
		state = t.Dim.Render(fmt.Sprintf("%s · %.1fs", m.scope, m.elapsed.Seconds()))
	default:
		state = t.Dim.Render("aucune exécution")
	}
	cache := t.Dim.Render("cache ") + t.Value.Render("on")
	if m.noCache {
		cache = t.Dim.Render("cache ") + t.Warn.Render("off")
	}
	line := counts + t.Dim.Render("   │   ") + state + t.Dim.Render("   │   ") + cache
	if m.cover || m.prof != nil {
		cov := t.Dim.Render("couverture ")
		switch {
		case m.prof != nil && len(m.prof.Files) > 0:
			cov += m.coverStyle(m.prof.Total()).Render(fmt.Sprintf("%.1f%%", m.prof.Total()))
		case m.cover:
			cov += t.Value.Render("on")
		}
		if !m.cover {
			cov += t.Dim.Render(" (off)")
		}
		line += t.Dim.Render("   │   ") + cov
	}
	if m.watcher != nil {
		line += t.Dim.Render("   │   ") + t.OK.Render("◉ surveillance")
		if len(m.queued) > 0 {
			line += t.Dim.Render(fmt.Sprintf(" (%d en attente)", len(m.queued)))
		}
	}
	return ansi.Truncate(line, width, "…")
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
	m.offset = min(max(m.offset, 0), max(0, len(m.rows)-h))

	lines := make([]string, 0, h)
	for i := m.offset; i < len(m.rows) && len(lines) < h; i++ {
		r := m.rows[i]
		var name, extra string
		var status gotest.Status
		if r.test == nil {
			arrow := "▸"
			if m.isExpanded(r.pkg) {
				arrow = "▾"
			}
			name = arrow + " " + strings.TrimPrefix(strings.TrimPrefix(r.pkg.Path, m.proj.Module), "/")
			if name == arrow+" " {
				name = arrow + " " + m.proj.Name() + " (racine)"
			}
			status = r.pkg.Status
			switch {
			case r.pkg.BuildFailed:
				extra = "compilation"
			case r.pkg.NoTests:
				extra = "sans tests"
			case r.pkg.Cached:
				extra = "cache"
			case r.pkg.Status == gotest.Pass || r.pkg.Status == gotest.Fail:
				extra = fmt.Sprintf("%.2fs", r.pkg.Elapsed)
			}
		} else {
			name = strings.Repeat("  ", r.test.Depth()+1) + r.test.Short()
			status = r.test.Status
			if status == gotest.Pass || status == gotest.Fail {
				extra = fmt.Sprintf("%.2fs", r.test.Elapsed)
			}
		}

		left := m.icon(status) + " " + name
		right := t.Dim.Render(extra)
		if r.test == nil && m.prof != nil {
			if pct, ok := m.prof.PackagePercent(r.pkg.Path); ok {
				right = m.coverStyle(pct).Render(fmt.Sprintf("%5.1f%%", pct)) + " " + right
			}
		}
		room := w - lipgloss.Width(right) - 1
		left = ansi.Truncate(left, max(1, room), "…")
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
	r, ok := m.selected()
	if !ok {
		return lipgloss.NewStyle().Width(w).Height(h).Render(t.Dim.Render("Aucune sélection"))
	}

	var title string
	var out []string
	if r.test == nil {
		title = r.pkg.Path
		out = r.pkg.Output
	} else {
		title = r.test.Name
		out = r.test.Output
	}
	if len(out) == 0 {
		out = []string{"(pas de sortie)"}
	}
	if r.test == nil && len(m.res.Stderr) > 0 && r.pkg.Status == gotest.Fail {
		out = append(append([]string{}, out...), m.res.Stderr...)
	}

	// Mettre en forme et replier les lignes à la largeur disponible.
	var lines []string
	wrap := lipgloss.NewStyle().Width(w)
	for _, raw := range out {
		for l := range strings.SplitSeq(strings.TrimRight(raw, "\n"), "\n") {
			l = strings.ReplaceAll(l, "\t", "    ")
			for wl := range strings.SplitSeq(wrap.Render(l), "\n") {
				lines = append(lines, m.colorize(wl))
			}
		}
	}

	head := []string{ansi.Truncate(t.Value.Render(title), w, "…"), ""}
	if r.test == nil {
		head = append(head, m.coverLines(r.pkg.Path, w)...)
	}
	avail := max(1, h-len(head))
	m.detailOff = min(m.detailOff, max(0, len(lines)-avail))
	end := len(lines) - m.detailOff
	start := max(0, end-avail)
	view := append(head, lines[start:end]...)
	return lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(strings.Join(view, "\n"))
}

func (m *Model) colorize(l string) string {
	t := m.th
	trim := strings.TrimSpace(l)
	switch {
	case strings.HasPrefix(trim, "--- FAIL"), strings.HasPrefix(trim, "FAIL"), strings.HasPrefix(trim, "panic:"):
		return t.Err.Render(l)
	case strings.HasPrefix(trim, "--- PASS"), strings.HasPrefix(trim, "ok "), trim == "PASS":
		return t.OK.Render(l)
	case strings.HasPrefix(trim, "--- SKIP"):
		return t.Warn.Render(l)
	case strings.HasPrefix(trim, "=== "):
		return t.Dim.Render(l)
	}
	if loc := locRe.FindStringIndex(l); loc != nil {
		return t.Text.Render(l[:loc[0]]) + lipgloss.NewStyle().Foreground(t.P.Blue).Render(l[loc[0]:loc[1]]) + t.Text.Render(l[loc[1]:])
	}
	return t.Text.Render(l)
}
