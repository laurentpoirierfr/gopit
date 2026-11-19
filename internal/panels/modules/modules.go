// Package modules est le panneau « Modules » : dépendances du module, mises
// à jour disponibles, raison de leur présence et vulnérabilités connues.
//
// La liste se charge localement au démarrage ; la recherche de mises à jour
// (u) et de vulnérabilités (v) accède au réseau et ne se lance qu'à la
// demande. La mise à jour d'une dépendance (g) modifie go.mod après
// confirmation.
package modules

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/gomod"
	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/theme"
)

// ─── Messages ────────────────────────────────────────────────────────────────

type listMsg struct {
	gen     int
	mods    []gomod.Module
	updates bool
	err     error
}

type goVersionMsg struct{ version string }

type vulnMsg struct {
	vulns []gomod.Vuln
	err   error
	dur   time.Duration
}

type whyMsg struct {
	path, text string
	err        error
}

type getMsg struct {
	path, version string
	err           error
}

type spinMsg struct{}

// ─── Modèle ──────────────────────────────────────────────────────────────────

// Filtres de la liste.
var filters = []string{i18n.N("toutes"), i18n.N("à mettre à jour"), i18n.N("vulnérables")}

type row struct {
	header string        // en-tête de section si mod est nil
	mod    *gomod.Module // nil pour un en-tête
}

type Model struct {
	th   *theme.Theme
	proj *project.Project

	gen       int
	loading   bool
	listErr   error
	mods      []gomod.Module // dépendances (sans le module principal)
	goVersion string         // ex. go1.27.3

	updChecked bool // mises à jour vérifiées au moins une fois
	updLoading bool

	vulnLoading bool
	vulnChecked bool
	vulnErr     error
	vulnDur     time.Duration
	vulns       map[string][]gomod.Vuln // par chemin de module (« stdlib » compris)

	why        map[string]string // go mod why, par module
	whyPending map[string]bool

	showIndirect bool
	filter       int
	rows         []row
	cursor       int
	offset       int
	detailOff    int
	listH        int
	spin         int
	spinning     bool // un tic d'animation est programmé

	confirm *gomod.Version // mise à jour en attente de confirmation
	getting bool
}

func New(th *theme.Theme, p *project.Project) *Model {
	return &Model{th: th, proj: p, vulns: map[string][]gomod.Vuln{},
		why: map[string]string{}, whyPending: map[string]bool{}}
}

func (m *Model) Title() string { return i18n.N("Modules") }
func (m *Model) Icon() string  { return "" }

func (m *Model) Init() tea.Cmd {
	root := m.proj.Root
	return tea.Batch(m.load(false), func() tea.Msg {
		v, _ := gomod.GoVersion(context.Background(), root)
		return goVersionMsg{v}
	})
}

func (m *Model) Hints() []panel.Hint {
	if m.confirm != nil {
		return []panel.Hint{{Key: "y", Desc: i18n.T("confirmer")}, {Key: "n", Desc: i18n.T("annuler")}}
	}
	return []panel.Hint{
		{Key: "u", Desc: i18n.T("mises à jour")}, {Key: "v", Desc: i18n.T("vulnérabilités")},
		{Key: "g", Desc: i18n.T("mettre à jour")}, {Key: "i", Desc: i18n.T("indirectes")},
		{Key: "f", Desc: i18n.T("filtre")}, {Key: "w", Desc: i18n.T("page web")},
	}
}

func (m *Model) busy() bool { return m.loading || m.updLoading || m.vulnLoading || m.getting }

// ─── Commandes ───────────────────────────────────────────────────────────────

func (m *Model) load(updates bool) tea.Cmd {
	m.gen++
	gen, root := m.gen, m.proj.Root
	m.loading = true
	if updates {
		m.updLoading = true
	}
	return tea.Batch(m.startSpin(), func() tea.Msg {
		mods, err := gomod.List(context.Background(), root, updates)
		return listMsg{gen: gen, mods: mods, updates: updates, err: err}
	})
}

func (m *Model) checkVulns() tea.Cmd {
	if !gomod.VulnAvailable() {
		return panel.Flash(i18n.T("govulncheck n'est pas installé : ")+gomod.VulnInstall, true)
	}
	m.vulnLoading = true
	root := m.proj.Root
	return tea.Batch(m.startSpin(), func() tea.Msg {
		start := time.Now()
		vulns, err := gomod.Vulns(context.Background(), root)
		return vulnMsg{vulns: vulns, err: err, dur: time.Since(start)}
	})
}

func (m *Model) startSpin() tea.Cmd {
	if m.spinning {
		return nil // un tic est déjà programmé
	}
	m.spinning = true
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return spinMsg{} })
}

// ensureWhy charge « go mod why » pour le module sélectionné.
func (m *Model) ensureWhy() tea.Cmd {
	mod := m.selected()
	if mod == nil || mod.Path == gomod.StdlibPath {
		return nil
	}
	if _, ok := m.why[mod.Path]; ok || m.whyPending[mod.Path] {
		return nil
	}
	m.whyPending[mod.Path] = true
	root, path := m.proj.Root, mod.Path
	return func() tea.Msg {
		text, err := gomod.Why(context.Background(), root, path)
		return whyMsg{path: path, text: text, err: err}
	}
}

// ─── Update ──────────────────────────────────────────────────────────────────

func (m *Model) Update(msg tea.Msg) (panel.Panel, tea.Cmd) {
	switch msg := msg.(type) {
	case listMsg:
		if msg.gen != m.gen {
			return m, nil
		}
		m.loading, m.listErr = false, msg.err
		if msg.updates {
			m.updLoading, m.updChecked = false, true
		}
		if msg.err != nil && len(msg.mods) == 0 {
			return m, panel.Flash("go list : "+msg.err.Error(), true)
		}
		m.mods = m.mods[:0]
		for _, mod := range msg.mods {
			if !mod.Main {
				m.mods = append(m.mods, mod)
			}
		}
		m.rebuild()
		var flash tea.Cmd
		if msg.updates {
			n := 0
			for _, mod := range m.mods {
				if mod.Update != nil && (mod.Direct() || m.showIndirect) {
					n++
				}
			}
			flash = panel.Flash(fmt.Sprintf(i18n.T("%d mise(s) à jour disponible(s)"), n), false)
		}
		return m, tea.Batch(flash, m.ensureWhy())

	case goVersionMsg:
		m.goVersion = msg.version
		m.rebuild()

	case vulnMsg:
		m.vulnLoading, m.vulnChecked, m.vulnErr, m.vulnDur = false, true, msg.err, msg.dur
		m.vulns = map[string][]gomod.Vuln{}
		called := 0
		for _, v := range msg.vulns {
			m.vulns[v.Module] = append(m.vulns[v.Module], v)
			if v.Level == gomod.LevelSymbol {
				called++
			}
		}
		m.rebuild()
		switch {
		case msg.err != nil:
			return m, panel.Flash("govulncheck : "+msg.err.Error(), true)
		case len(msg.vulns) == 0:
			return m, panel.Flash(i18n.T("Aucune vulnérabilité connue"), false)
		}
		return m, panel.Flash(fmt.Sprintf(i18n.T("%d vulnérabilité(s), dont %d appelée(s) par le code"), len(msg.vulns), called), called > 0)

	case whyMsg:
		delete(m.whyPending, msg.path)
		if msg.err != nil {
			m.why[msg.path] = "(" + msg.err.Error() + ")"
		} else {
			m.why[msg.path] = msg.text
		}

	case getMsg:
		m.getting = false
		if msg.err != nil {
			return m, panel.Flash("go get : "+msg.err.Error(), true)
		}
		delete(m.why, msg.path)
		return m, tea.Batch(m.load(m.updChecked),
			panel.Flash(fmt.Sprintf(i18n.T("%s mis à jour en %s (go.mod modifié) — v pour revérifier les vulnérabilités"), msg.path, msg.version), false))

	case spinMsg:
		if m.busy() {
			m.spin++
			return m, tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return spinMsg{} })
		}
		m.spinning = false

	case tea.KeyPressMsg:
		cmd := m.handleKey(msg.String())
		return m, tea.Batch(cmd, m.ensureWhy())

	case panel.ClickMsg:
		return m, tea.Batch(m.click(msg), m.ensureWhy())

	case panel.WheelMsg:
		switch {
		case panel.Has(msg.Zones, "detail"):
			m.detailOff = max(0, m.detailOff+msg.Delta)
		case panel.Has(msg.Zones, "list"):
			m.move(msg.Delta)
			return m, m.ensureWhy()
		}
	}
	return m, nil
}

// click choisit le module cliqué ; un clic dans le bilan (indirectes,
// mises à jour, vulnérabilités, filtre) fait comme la touche indiquée.
func (m *Model) click(c panel.ClickMsg) tea.Cmd {
	if c.Drag || m.confirm != nil {
		return nil
	}
	for zone, key := range map[string]string{"indirect": "i", "updates": "u", "vulns": "v", "filter": "f"} {
		if panel.Has(c.Zones, zone) {
			return m.handleKey(key)
		}
	}
	if i, ok := panel.Index(c.Zones, "row:"); ok && i >= 0 && i < len(m.rows) && m.rows[i].mod != nil {
		if m.cursor != i {
			m.cursor, m.detailOff = i, 0
		}
	}
	return nil
}

func (m *Model) handleKey(s string) tea.Cmd {
	if m.confirm != nil {
		switch s {
		case "y", "o":
			target := *m.confirm
			m.confirm, m.getting = nil, true
			root := m.proj.Root
			return tea.Batch(m.startSpin(), func() tea.Msg {
				err := gomod.Get(context.Background(), root, target.Path, target.Version)
				return getMsg{path: target.Path, version: target.Version, err: err}
			})
		case "n", "esc":
			m.confirm = nil
			return panel.Flash(i18n.T("Mise à jour annulée"), false)
		}
		return nil
	}

	switch s {
	case "a":
		return m.load(m.updChecked)
	case "u":
		return m.load(true)
	case "v":
		return m.checkVulns()
	case "g":
		return m.askUpgrade()
	case "w":
		return m.openWeb()
	case "i":
		m.showIndirect = !m.showIndirect
		m.rebuild()
	case "f":
		m.filter = (m.filter + 1) % len(filters)
		m.rebuild()
		return panel.Flash(i18n.T("Filtre : ")+i18n.T(filters[m.filter]), false)
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "home":
		m.move(-len(m.rows))
	case "end", "G":
		m.move(len(m.rows))
	case "pgup":
		m.detailOff = max(0, m.detailOff-max(1, m.listH/2))
	case "pgdown":
		m.detailOff += max(1, m.listH/2)
	}
	return nil
}

// askUpgrade prépare la mise à jour du module sélectionné : vers la version
// la plus récente si elle est connue, sinon vers la version qui corrige ses
// vulnérabilités.
func (m *Model) askUpgrade() tea.Cmd {
	mod := m.selected()
	switch {
	case mod == nil:
		return nil
	case mod.Path == gomod.StdlibPath:
		return panel.Flash(i18n.T("La bibliothèque standard se met à jour avec Go lui-même (directive toolchain ou nouvelle version de Go)"), true)
	case mod.Replace != nil:
		return panel.Flash(i18n.T("Module remplacé (replace) : à mettre à jour dans go.mod"), true)
	}
	target := ""
	if mod.Update != nil {
		target = mod.Update.Version
	} else if fix := m.fixVersion(mod); fix != "" {
		target = fix
	}
	if target == "" {
		if !m.updChecked {
			return panel.Flash(i18n.T("Vérifie d'abord les mises à jour (u)"), true)
		}
		return panel.Flash(mod.Path+i18n.T(" est à jour"), false)
	}
	m.confirm = &gomod.Version{Path: mod.Path, Version: target}
	return nil
}

// fixVersion renvoie la version la plus haute qui corrige toutes les
// vulnérabilités connues du module.
func (m *Model) fixVersion(mod *gomod.Module) string {
	best := ""
	for _, v := range m.vulnsOf(mod) {
		if v.Fixed != "" && (best == "" || gomod.Kind(best, v.Fixed) != gomod.NoUpdate) {
			best = v.Fixed
		}
	}
	return best
}

func (m *Model) openWeb() tea.Cmd {
	mod := m.selected()
	if mod == nil {
		return nil
	}
	url := "https://pkg.go.dev/" + mod.Path + "@" + mod.Version
	if mod.Path == gomod.StdlibPath {
		url = "https://pkg.go.dev/std"
	}
	if vs := m.vulnsOf(mod); len(vs) > 0 {
		url = vs[0].URL() // la vulnérabilité la plus grave
	}
	if err := exec.Command("xdg-open", url).Start(); err != nil {
		return panel.Flash(i18n.T("Impossible d'ouvrir le navigateur : ")+err.Error(), true)
	}
	return panel.Flash(i18n.T("Ouverture de ")+url, false)
}

func (m *Model) move(d int) {
	var idx []int
	for i, r := range m.rows {
		if r.mod != nil {
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
	m.cursor = idx[min(max(pos+d, 0), len(idx)-1)]
	m.detailOff = 0
}

// vulnsOf renvoie les vulnérabilités qui visent la version utilisée du
// module : après une mise à jour, celles de l'ancienne version ne
// s'appliquent plus (relancer v pour revérifier).
func (m *Model) vulnsOf(mod *gomod.Module) []gomod.Vuln {
	var out []gomod.Vuln
	for _, v := range m.vulns[mod.Path] {
		if v.Version == mod.Version {
			out = append(out, v)
		}
	}
	return out
}

func (m *Model) selected() *gomod.Module {
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		return m.rows[m.cursor].mod
	}
	return nil
}

// stdlib renvoie une pseudo-dépendance pour la bibliothèque standard.
func (m *Model) stdlib() *gomod.Module {
	v := strings.TrimPrefix(m.goVersion, "go")
	if v != "" {
		v = "v" + v
	}
	return &gomod.Module{Path: gomod.StdlibPath, Version: v, GoVersion: m.proj.GoVersion}
}

func (m *Model) keep(mod *gomod.Module) bool {
	switch m.filter {
	case 1:
		return mod.Update != nil || len(m.vulnsOf(mod)) > 0
	case 2:
		return len(m.vulnsOf(mod)) > 0
	}
	return true
}

// rebuild recalcule les lignes (sections Go, directes, indirectes) en
// gardant la sélection.
func (m *Model) rebuild() {
	sel := ""
	if mod := m.selected(); mod != nil {
		sel = mod.Path
	}
	m.rows = m.rows[:0]

	if std := m.stdlib(); m.keep(std) {
		m.rows = append(m.rows, row{header: "Go"}, row{mod: std})
	}
	var direct, indirect []*gomod.Module
	for i := range m.mods {
		mod := &m.mods[i]
		if !m.keep(mod) {
			continue
		}
		if mod.Direct() {
			direct = append(direct, mod)
		} else {
			indirect = append(indirect, mod)
		}
	}
	m.rows = append(m.rows, row{header: fmt.Sprintf(i18n.T("Directes (%d)"), len(direct))})
	for _, mod := range direct {
		m.rows = append(m.rows, row{mod: mod})
	}
	if m.showIndirect {
		m.rows = append(m.rows, row{header: fmt.Sprintf(i18n.T("Indirectes (%d)"), len(indirect))})
		for _, mod := range indirect {
			m.rows = append(m.rows, row{mod: mod})
		}
	} else if len(indirect) > 0 {
		m.rows = append(m.rows, row{header: fmt.Sprintf(i18n.T("▸ Indirectes (%d) — i pour afficher"), len(indirect))})
	}

	m.cursor = -1
	for i, r := range m.rows {
		if r.mod == nil {
			continue
		}
		if m.cursor < 0 || r.mod.Path == sel {
			m.cursor = i
		}
		if r.mod.Path == sel {
			break
		}
	}
	// Sélection par défaut : la première dépendance directe plutôt que Go.
	if sel == "" {
		for i, r := range m.rows {
			if r.mod != nil && r.mod.Path != gomod.StdlibPath {
				m.cursor = i
				break
			}
		}
	}
}

// ─── Vue ─────────────────────────────────────────────────────────────────────

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (m *Model) spinIcon() string {
	return lipgloss.NewStyle().Foreground(m.th.P.Blue).Render(spinner[m.spin%len(spinner)])
}

func (m *Model) kindStyle(k gomod.UpdateKind) lipgloss.Style {
	switch k {
	case gomod.Patch:
		return m.th.OK
	case gomod.Minor:
		return m.th.Warn
	}
	return lipgloss.NewStyle().Foreground(m.th.P.Peach)
}

// vulnBadge résume les vulnérabilités d'un module : rouge si une fonction
// vulnérable est appelée, jaune sinon.
func (m *Model) vulnBadge(mod *gomod.Module) string {
	vs := m.vulnsOf(mod)
	if len(vs) == 0 {
		return ""
	}
	for _, v := range vs {
		if v.Level == gomod.LevelSymbol {
			return m.th.Err.Render(fmt.Sprintf("✗%d", len(vs)))
		}
	}
	return m.th.Warn.Render(fmt.Sprintf("⚠%d", len(vs)))
}

func (m *Model) View(width, height int) string {
	head := m.viewSummary(width)
	bodyH := max(1, height-len(head)-1)
	top := strings.Join(head, "\n") + "\n\n"
	if len(m.mods) == 0 && m.goVersion == "" {
		if m.loading {
			return top + m.th.Dim.Render(i18n.T("Chargement des dépendances…"))
		}
		return top + m.th.Dim.Render(i18n.T("Aucune dépendance."))
	}
	listW := max(34, width*50/100)
	detailW := max(10, width-listW-3)
	sep := m.th.Dim.Render(strings.TrimSuffix(strings.Repeat("│\n", bodyH), "\n"))
	return top + lipgloss.JoinHorizontal(lipgloss.Top,
		m.viewList(listW, bodyH), " ", sep, " ", m.viewDetail(detailW, bodyH))
}

func (m *Model) viewSummary(width int) []string {
	t := m.th
	direct, indirect, upd := 0, 0, 0
	for _, mod := range m.mods {
		if mod.Direct() {
			direct++
		} else {
			indirect++
		}
		if mod.Update != nil && mod.Direct() {
			upd++
		}
	}
	parts := []string{
		t.Value.Render(fmt.Sprint(direct)) + t.Dim.Render(i18n.T(" directes · ")) + panel.Zone("indirect", t.Value.Render(fmt.Sprint(indirect))+t.Dim.Render(i18n.T(" indirectes"))),
	}

	switch {
	case m.updLoading:
		parts = append(parts, m.spinIcon()+t.Text.Render(i18n.T(" mises à jour…")))
	case m.updChecked:
		parts = append(parts, t.Warn.Render(fmt.Sprintf("↑ %d", upd))+t.Dim.Render(i18n.T(" mises à jour (directes)")))
	default:
		parts = append(parts, panel.Zone("updates", t.Dim.Render(i18n.T("mises à jour non vérifiées (u)"))))
	}

	switch {
	case m.vulnLoading:
		parts = append(parts, m.spinIcon()+t.Text.Render(" govulncheck…"))
	case m.vulnErr != nil:
		parts = append(parts, t.Err.Render(i18n.T("govulncheck en échec")))
	case m.vulnChecked:
		n, called := 0, 0
		all := []*gomod.Module{m.stdlib()}
		for i := range m.mods {
			all = append(all, &m.mods[i])
		}
		for _, mod := range all {
			for _, v := range m.vulnsOf(mod) {
				n++
				if v.Level == gomod.LevelSymbol {
					called++
				}
			}
		}
		switch {
		case n == 0:
			parts = append(parts, t.OK.Render(i18n.T("✓ aucune vulnérabilité")))
		case called == 0:
			parts = append(parts, t.Warn.Render(fmt.Sprintf(i18n.T("⚠ %d vulnérabilité(s)"), n))+t.Dim.Render(i18n.T(", aucune appelée")))
		default:
			parts = append(parts, t.Err.Render(fmt.Sprintf(i18n.T("✗ %d appelée(s)"), called))+t.Dim.Render(fmt.Sprintf(i18n.T(" sur %d vulnérabilité(s)"), n)))
		}
	case !gomod.VulnAvailable():
		parts = append(parts, t.Dim.Render("govulncheck –"))
	default:
		parts = append(parts, panel.Zone("vulns", t.Dim.Render(i18n.T("vulnérabilités non vérifiées (v)"))))
	}
	if m.filter != 0 {
		parts = append(parts, panel.Zone("filter", t.Dim.Render(i18n.T("filtre "))+t.Value.Render(i18n.T(filters[m.filter]))))
	}

	lines := []string{ansi.Truncate(strings.Join(parts, t.Dim.Render("   │   ")), width, "…")}
	if m.confirm != nil {
		lines = append(lines, ansi.Truncate(t.Warn.Bold(true).Render(fmt.Sprintf(i18n.T("Mettre %s en %s ? go.mod et go.sum seront modifiés. (y/n)"),
			m.confirm.Path, m.confirm.Version)), width, "…"))
	} else if m.getting {
		lines = append(lines, m.spinIcon()+t.Text.Render(i18n.T(" go get en cours…")))
	}
	if m.listErr != nil {
		lines = append(lines, ansi.Truncate(t.Err.Render("go list : "+m.listErr.Error()), width, "…"))
	}
	if !gomod.VulnAvailable() && !m.vulnChecked {
		lines = append(lines, ansi.Truncate(t.Dim.Render(i18n.T("govulncheck n'est pas installé : "))+t.Text.Render(gomod.VulnInstall), width, "…"))
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
	if m.offset > 0 && m.cursor == m.offset && m.rows[m.offset-1].mod == nil {
		m.offset-- // montrer l'en-tête de section
	}
	m.offset = min(max(m.offset, 0), max(0, len(m.rows)-h))

	lines := make([]string, 0, h)
	for i := m.offset; i < len(m.rows) && len(lines) < h; i++ {
		r := m.rows[i]
		if r.mod == nil {
			lines = append(lines, ansi.Truncate(t.Label.Bold(true).Render(r.header), w, "…"))
			continue
		}
		mod := r.mod
		name := mod.Path
		if name == gomod.StdlibPath {
			name = i18n.T("bibliothèque standard")
		}
		version := mod.Version
		if mod.Replace != nil {
			version += " ⇒"
		}
		right := t.Dim.Render(version)
		if mod.Update != nil {
			k := gomod.Kind(mod.Version, mod.Update.Version)
			right += m.kindStyle(k).Render(" → " + mod.Update.Version)
		}
		if b := m.vulnBadge(mod); b != "" {
			right += " " + b
		}
		if mod.Deprecated != "" {
			right += " " + t.Err.Render(i18n.T("déprécié"))
		}
		room := w - 2 - lipgloss.Width(right) - 1
		left := "  " + ansi.Truncate(t.Text.Render(name), max(4, room), "…")
		pad := strings.Repeat(" ", max(1, w-lipgloss.Width(left)-lipgloss.Width(right)))
		line := ansi.Truncate(left+pad+right, w, "…")
		if i == m.cursor {
			line = lipgloss.NewStyle().Background(t.P.Surface0).Width(w).Render(line)
		}
		lines = append(lines, panel.Row(fmt.Sprint("row:", i), w, line))
	}
	return panel.Zone("list", lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(strings.Join(lines, "\n")))
}

func date(tm *time.Time) string {
	if tm == nil {
		return ""
	}
	return tm.Format("02/01/2006")
}

func (m *Model) viewDetail(w, h int) string {
	t := m.th
	mod := m.selected()
	if mod == nil {
		return lipgloss.NewStyle().Width(w).Height(h).Render(t.Dim.Render(i18n.T("Aucune sélection")))
	}
	wrap := lipgloss.NewStyle().Width(w)
	field := func(label, value string) string {
		return t.Label.Render(fmt.Sprintf("%-14s", label)) + value
	}

	var lines []string
	if mod.Path == gomod.StdlibPath {
		lines = append(lines, t.Value.Render(i18n.T("Bibliothèque standard de Go")), "",
			field(i18n.T("Go installé"), t.Text.Render(m.goVersion)),
			field("go.mod", t.Text.Render("go "+m.proj.GoVersion)))
	} else {
		kind := i18n.T("dépendance indirecte")
		if mod.Direct() {
			kind = i18n.T("dépendance directe")
		}
		lines = append(lines, wrap.Render(t.Value.Render(mod.Path)), t.Dim.Render(kind), "")
		ver := t.Text.Render(mod.Version)
		if d := date(mod.Time); d != "" {
			ver += t.Dim.Render("  (" + d + ")")
		}
		lines = append(lines, field(i18n.T("Version"), ver))

		switch {
		case mod.Update != nil:
			k := gomod.Kind(mod.Version, mod.Update.Version)
			u := m.kindStyle(k).Render(mod.Update.Version)
			if d := date(mod.Update.Time); d != "" {
				u += t.Dim.Render("  (" + d + ")")
			}
			if k != gomod.NoUpdate {
				u += t.Dim.Render(i18n.T("  version ") + k.String())
			}
			lines = append(lines, field(i18n.T("Mise à jour"), u))
		case m.updChecked:
			lines = append(lines, field(i18n.T("Mise à jour"), t.OK.Render(i18n.T("à jour"))))
		default:
			lines = append(lines, field(i18n.T("Mise à jour"), t.Dim.Render(i18n.T("non vérifiée (u)"))))
		}
		if mod.GoVersion != "" {
			lines = append(lines, field(i18n.T("Go requis"), t.Text.Render(mod.GoVersion)))
		}
		if mod.Replace != nil {
			lines = append(lines, field(i18n.T("Remplacé par"), t.Warn.Render(strings.TrimSpace(mod.Replace.Path+" "+mod.Replace.Version))))
		}
		if mod.Deprecated != "" {
			lines = append(lines, field(i18n.T("Déprécié"), wrap.Render(t.Err.Render(mod.Deprecated))))
		}
		if len(mod.Retracted) > 0 {
			lines = append(lines, field(i18n.T("Retirée"), t.Err.Render(strings.Join(mod.Retracted, " ; "))))
		}
		if mod.Error != nil {
			lines = append(lines, field(i18n.T("Erreur"), t.Err.Render(mod.Error.Err)))
		}

		lines = append(lines, "", t.Label.Bold(true).Render(i18n.T("Pourquoi ce module ?")))
		switch why, ok := m.why[mod.Path]; {
		case !ok:
			lines = append(lines, t.Dim.Render("…"))
		case why == "":
			lines = append(lines, t.Dim.Render(i18n.T("(pas d'explication)")))
		default:
			for i, l := range strings.Split(why, "\n") {
				prefix := "  "
				if i > 0 {
					prefix = "  " + strings.Repeat(" ", min(i-1, 6)) + "└ "
				}
				lines = append(lines, ansi.Truncate(t.Text.Render(prefix+l), w, "…"))
			}
		}
	}

	lines = append(lines, "", t.Label.Bold(true).Render(i18n.T("Vulnérabilités")))
	vs := m.vulnsOf(mod)
	switch {
	case m.vulnLoading:
		lines = append(lines, m.spinIcon()+t.Dim.Render(i18n.T(" analyse en cours…")))
	case !m.vulnChecked:
		lines = append(lines, t.Dim.Render(i18n.T("non vérifiées (v)")))
	case len(vs) == 0:
		lines = append(lines, t.OK.Render(i18n.T("✓ aucune connue")))
	}
	for _, v := range vs {
		lines = append(lines, "")
		icon := t.Warn.Render("⚠")
		if v.Level == gomod.LevelSymbol {
			icon = t.Err.Render("✗")
		}
		id := v.ID
		if len(v.Aliases) > 0 {
			id += " · " + v.Aliases[0]
		}
		lines = append(lines, icon+" "+t.Value.Render(id))
		if v.Summary != "" {
			lines = append(lines, wrap.Render(t.Text.Render(v.Summary)))
		}
		fixed := t.Err.Render(i18n.T("aucune"))
		if v.Fixed != "" {
			fixed = t.OK.Render(v.Fixed)
		}
		lines = append(lines, field(i18n.T("Corrigée en"), fixed), field(i18n.T("Portée"), t.Text.Render(v.Level.String())))
		if len(v.Trace) > 0 {
			lines = append(lines, t.Dim.Render(i18n.T("Chaîne d'appels :")))
			for i := len(v.Trace) - 1; i >= 0; i-- {
				f := v.Trace[i]
				s := f.Name()
				if f.File != "" {
					s += t.Dim.Render(fmt.Sprintf("  %s:%d", f.File, f.Line))
				}
				lines = append(lines, ansi.Truncate(t.Text.Render(strings.Repeat(" ", 2*min(len(v.Trace)-1-i, 6))+"→ "+s), w, "…"))
			}
		}
	}

	// Défilement du détail (PgUp / PgDn).
	var flat []string
	for _, l := range lines {
		flat = append(flat, strings.Split(l, "\n")...)
	}
	m.detailOff = min(m.detailOff, max(0, len(flat)-h))
	flat = flat[m.detailOff:]
	return panel.Zone("detail", lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(strings.Join(flat, "\n")))
}
