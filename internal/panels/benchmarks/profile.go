package benchmarks

// Profilage d'un benchmark (p : CPU, m : mémoire) et exploration du profil :
// fonctions les plus coûteuses et code source annoté ligne par ligne.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/bench"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/prof"
)

// profState est l'état de la vue profil.
type profState struct {
	kind   string // « cpu » ou « mem » pendant le profilage
	dir    string // répertoire temporaire (profil, binaire de test)
	target string // benchmark profilé

	report   *prof.Report
	open     bool // vue profil affichée
	funcs    []prof.Func
	byCum    bool
	mineOnly bool
	cursor   int
	offset   int
	source   map[string][]string
}

// startProfile profile le benchmark sélectionné. Le binaire de test et le
// profil vont dans un répertoire temporaire, pas dans le projet.
func (m *Model) startProfile(kind string) tea.Cmd {
	r := m.selected()
	if r == nil {
		return panel.Flash("Sélectionne d'abord un benchmark (a pour les lancer)", true)
	}
	dir, err := os.MkdirTemp("", "gopit-prof-*")
	if err != nil {
		return panel.Flash("Profil : "+err.Error(), true)
	}
	flag, label := "-cpuprofile=", "profil CPU de "+r.Name
	if kind == "mem" {
		flag, label = "-memprofile=", "profil mémoire de "+r.Name
	}
	m.pr = profState{kind: kind, dir: dir, target: r.Name, source: map[string][]string{}}
	return m.start(label, bench.Options{
		Pattern: pattern(r.Name), Pkgs: []string{r.Pkg}, Count: 1,
		Extra: []string{flag + filepath.Join(dir, kind+".prof"), "-o", filepath.Join(dir, "bench.test")},
	})
}

// abortProfile abandonne un profilage en cours (une autre exécution démarre).
func (m *Model) abortProfile() {
	if m.pr.kind != "" {
		os.RemoveAll(m.pr.dir)
		m.pr.kind, m.pr.dir = "", ""
	}
}

// finishProfile charge le profil produit et ouvre la vue profil.
func (m *Model) finishProfile(err error) tea.Cmd {
	m.exec, m.cancel, m.runErr = nil, nil, err
	m.rebuild()
	defer os.RemoveAll(m.pr.dir)
	kind := m.pr.kind
	m.pr.kind = ""
	if err != nil {
		return panel.Flash("Profilage : "+err.Error(), err != bench.ErrInterrupted)
	}
	r, lerr := prof.Load(filepath.Join(m.pr.dir, kind+".prof"), "")
	if lerr != nil {
		return panel.Flash("Profil : "+lerr.Error(), true)
	}
	m.pr.report, m.pr.open, m.pr.cursor, m.pr.offset = r, true, 0, 0
	m.filterFuncs()
	return panel.Flash(fmt.Sprintf("Profil %s de %s : %s mesurés", r.Kind, m.pr.target, r.Format(r.Total)), false)
}

func (m *Model) filterFuncs() {
	m.pr.report.Sort(m.pr.byCum)
	m.pr.funcs = m.pr.funcs[:0]
	for _, f := range m.pr.report.Funcs {
		if m.pr.mineOnly && !strings.HasPrefix(f.Name, m.proj.Module) {
			continue
		}
		m.pr.funcs = append(m.pr.funcs, f)
	}
	m.pr.cursor = min(m.pr.cursor, max(0, len(m.pr.funcs)-1))
}

func (m *Model) profileHints() []panel.Hint {
	return []panel.Hint{{Key: "Échap", Desc: "benchmarks"}, {Key: "s", Desc: "tri propre/cumulé"},
		{Key: "t", Desc: "mesure"}, {Key: "f", Desc: "mon code"}, {Key: "o", Desc: "ouvrir"}}
}

func (m *Model) profileKey(s string) tea.Cmd {
	p := &m.pr
	switch s {
	case "esc", "backspace":
		p.open = false
	case "up", "k":
		p.cursor = max(0, p.cursor-1)
	case "down", "j":
		p.cursor = min(len(p.funcs)-1, p.cursor+1)
	case "home", "g":
		p.cursor = 0
	case "end", "G":
		p.cursor = max(0, len(p.funcs)-1)
	case "s":
		p.byCum = !p.byCum
		m.filterFuncs()
	case "f":
		p.mineOnly = !p.mineOnly
		m.filterFuncs()
		if p.mineOnly {
			return panel.Flash("Seulement les fonctions du module", false)
		}
		return panel.Flash("Toutes les fonctions", false)
	case "t":
		types := p.report.Types
		next := types[0]
		for i, k := range types {
			if k == p.report.Kind {
				next = types[(i+1)%len(types)]
			}
		}
		r, err := p.report.As(next)
		if err != nil {
			return panel.Flash(err.Error(), true)
		}
		p.report = r
		m.filterFuncs()
		return panel.Flash("Mesure : "+next, false)
	case "o", "enter":
		if p.cursor < len(p.funcs) {
			f := p.funcs[p.cursor]
			if _, err := os.Stat(f.File); err != nil {
				return panel.Flash("Source introuvable : "+f.File, true)
			}
			return panel.Open(f.File, f.Line)
		}
	}
	return nil
}

// ─── Vue profil ──────────────────────────────────────────────────────────────

func (m *Model) viewProfile(width, height int) string {
	t := m.th
	r := m.pr.report
	sort := "coût propre"
	if m.pr.byCum {
		sort = "coût cumulé"
	}
	head := t.Value.Render("Profil "+r.Kind) + t.Dim.Render(" · ") + t.Text.Render(m.pr.target) +
		t.Dim.Render(" · total ") + t.Value.Render(r.Format(r.Total))
	if r.Duration > 0 {
		head += t.Dim.Render(fmt.Sprintf(" sur %.2fs", r.Duration.Seconds()))
	}
	head += t.Dim.Render("   │   tri : ") + t.Value.Render(sort)
	if m.pr.mineOnly {
		head += t.Dim.Render(" · ") + t.Value.Render("module seulement")
	}
	bodyH := max(1, height-2)
	listW := max(40, width*55/100)
	detailW := max(10, width-listW-3)
	sep := t.Dim.Render(strings.TrimSuffix(strings.Repeat("│\n", bodyH), "\n"))
	return ansi.Truncate(head, width, "…") + "\n\n" +
		lipgloss.JoinHorizontal(lipgloss.Top, m.viewFuncs(listW, bodyH), " ", sep, " ", m.viewSource(detailW, bodyH))
}

func (m *Model) viewFuncs(w, h int) string {
	t := m.th
	p := &m.pr
	r := p.report
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+h-1 {
		p.offset = p.cursor - h + 2
	}
	p.offset = max(0, p.offset)

	lines := []string{t.Label.Render(fmt.Sprintf("%7s %6s %7s %6s  %s", "propre", "%", "cumulé", "%", "fonction"))}
	for i := p.offset; i < len(p.funcs) && len(lines) < h; i++ {
		f := p.funcs[i]
		heat := m.heat(r.Percent(f.Flat))
		if p.byCum {
			heat = m.heat(r.Percent(f.Cum))
		}
		name := prof.ShortName(f.Name)
		nameSt := t.Text
		if strings.HasPrefix(f.Name, m.proj.Module) {
			nameSt = t.Value // le code du module ressort
		}
		flat := heat.Render(fmt.Sprintf("%7s %5.1f%%", r.Format(f.Flat), r.Percent(f.Flat)))
		if f.Flat == 0 {
			flat = t.Dim.Render(fmt.Sprintf("%7s %6s", "·", "·"))
		}
		line := flat +
			t.Dim.Render(fmt.Sprintf(" %7s %5.1f%%", r.Format(f.Cum), r.Percent(f.Cum))) + "  " + nameSt.Render(name)
		line = ansi.Truncate(line, w, "…")
		if i == p.cursor {
			line = lipgloss.NewStyle().Background(t.P.Surface0).Width(w).Render(line)
		}
		lines = append(lines, line)
	}
	if len(p.funcs) == 0 {
		lines = append(lines, t.Dim.Render("Aucune fonction (f : afficher tout)"))
	}
	return lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(strings.Join(lines, "\n"))
}

// heat colore une part du total : rouge au-delà de 20 %, jaune au-delà de 5 %.
func (m *Model) heat(pct float64) lipgloss.Style {
	switch {
	case pct >= 20:
		return m.th.Err
	case pct >= 5:
		return m.th.Warn
	}
	return m.th.Text
}

// viewSource affiche le code de la fonction sélectionnée, annoté du coût de
// chaque ligne (comme « go tool pprof -list »).
func (m *Model) viewSource(w, h int) string {
	t := m.th
	box := lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h)
	p := &m.pr
	if p.cursor >= len(p.funcs) {
		return box.Render("")
	}
	f := p.funcs[p.cursor]
	r := p.report
	lines := []string{
		ansi.Truncate(t.Value.Render(prof.ShortName(f.Name)), w, "…"),
		ansi.Truncate(t.Dim.Render(fmt.Sprintf("%s:%d", m.rel(f.File), f.Line)), w, "…"),
		t.Dim.Render(fmt.Sprintf("propre %s (%.1f%%) · cumulé %s (%.1f%%)", r.Format(f.Flat), r.Percent(f.Flat), r.Format(f.Cum), r.Percent(f.Cum))),
		"",
	}

	costs := r.Lines(f.Name)
	src, ok := p.source[f.File]
	if !ok {
		if data, err := os.ReadFile(f.File); err == nil {
			src = strings.Split(string(data), "\n")
		}
		p.source[f.File] = src
	}
	if len(costs) == 0 || src == nil {
		return box.Render(strings.Join(append(lines, t.Dim.Render("Source non disponible")), "\n"))
	}

	// Lignes coûteuses, avec deux lignes de contexte autour.
	byLine := map[int]prof.LineCost{}
	show := map[int]bool{}
	for _, c := range costs {
		byLine[c.Line] = c
		for n := c.Line - 2; n <= c.Line+2; n++ {
			show[n] = true
		}
	}
	prev := 0
	for n := 1; n <= len(src); n++ {
		if !show[n] {
			continue
		}
		if prev != 0 && n != prev+1 {
			lines = append(lines, t.Dim.Render("      ⋮"))
		}
		prev = n
		code := strings.ReplaceAll(src[n-1], "\t", "    ")
		cost := "        "
		st := t.Dim
		if c, ok := byLine[n]; ok && c.Cum > 0 {
			cost = fmt.Sprintf("%8s", r.Format(c.Cum))
			st = m.heat(r.Percent(c.Cum))
		}
		lines = append(lines, ansi.Truncate(st.Render(cost)+t.Dim.Render(fmt.Sprintf(" %4d ", n))+t.Text.Render(code), w, "…"))
	}
	return box.Render(strings.Join(lines, "\n"))
}

func (m *Model) rel(p string) string {
	if r, err := filepath.Rel(m.proj.Root, p); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return p
}
