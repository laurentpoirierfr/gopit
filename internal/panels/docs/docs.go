// Package docs est le panneau « Docs » : documentation des paquets du module
// et de la bibliothèque standard, rendue en Markdown avec Glamour.
package docs

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/editor"
	"github.com/laurentpoirierfr/gopit/internal/godoc"
	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/theme"
)

type listMsg struct {
	module, std []godoc.Pkg
	err         error
}

type loadMsg struct {
	path string
	doc  *godoc.Doc
	err  error
}

type mode int

const (
	modePkgs    mode = iota // liste des paquets
	modeSymbols             // symboles d'un paquet
)

// item est une ligne de la liste : en-tête, paquet, vue d'ensemble ou symbole.
type item struct {
	header   string
	pkg      *godoc.Pkg
	overview bool
	sym      *godoc.Symbol
}

func (it item) selectable() bool { return it.header == "" }

type Model struct {
	th   *theme.Theme
	proj *project.Project

	module, std []godoc.Pkg
	listErr     error
	listed      bool

	mode      mode
	open      *godoc.Pkg // paquet dont on parcourt les symboles
	filtering bool
	query     string

	items  []item
	cursor int
	offset int
	listH  int

	docs    map[string]*godoc.Doc
	docErr  map[string]error
	pending map[string]bool

	rendered  map[string][]string // rendu Glamour par clé (paquet, symbole, largeur, thème)
	renderer  *glamour.TermRenderer
	rWidth    int
	rDark     bool
	detailOff int
}

func New(th *theme.Theme, p *project.Project) *Model {
	return &Model{th: th, proj: p, docs: map[string]*godoc.Doc{}, docErr: map[string]error{},
		pending: map[string]bool{}, rendered: map[string][]string{}}
}

func (m *Model) Title() string { return i18n.N("Docs") }
func (m *Model) Icon() string  { return "" }

// Capturing : pendant une recherche, toutes les touches vont au champ.
func (m *Model) Capturing() bool { return m.filtering }

func (m *Model) Hints() []panel.Hint {
	switch {
	case m.filtering:
		return []panel.Hint{{Key: "Entrée", Desc: i18n.T("valider")}, {Key: "Échap", Desc: i18n.T("effacer")}}
	case m.mode == modeSymbols:
		return []panel.Hint{{Key: "/", Desc: i18n.T("filtrer")}, {Key: "Échap", Desc: i18n.T("paquets")},
			{Key: "o", Desc: i18n.T("source")}, {Key: "w", Desc: "pkg.go.dev"}, {Key: "PgUp/PgDn", Desc: i18n.T("défiler")}}
	}
	return []panel.Hint{{Key: "/", Desc: i18n.T("rechercher")}, {Key: "Entrée", Desc: i18n.T("symboles")},
		{Key: "w", Desc: "pkg.go.dev"}, {Key: "PgUp/PgDn", Desc: i18n.T("défiler")}}
}

func (m *Model) Init() tea.Cmd {
	root := m.proj.Root
	return func() tea.Msg {
		ctx := context.Background()
		mod, err := godoc.ListModule(ctx, root)
		std, err2 := godoc.ListStd(ctx, root)
		if err == nil {
			err = err2
		}
		return listMsg{module: mod, std: std, err: err}
	}
}

// ─── Update ──────────────────────────────────────────────────────────────────

func (m *Model) Update(msg tea.Msg) (panel.Panel, tea.Cmd) {
	switch msg := msg.(type) {
	case listMsg:
		m.module, m.std, m.listErr, m.listed = msg.module, msg.std, msg.err, true
		m.rebuild()
		return m, m.ensureLoad()

	case loadMsg:
		delete(m.pending, msg.path)
		if msg.err != nil {
			m.docErr[msg.path] = msg.err
		} else {
			m.docs[msg.path] = msg.doc
		}
		if m.mode == modeSymbols && m.open != nil && m.open.ImportPath == msg.path {
			m.rebuild()
		}

	case tea.KeyPressMsg:
		cmd := m.handleKey(msg)
		return m, tea.Batch(cmd, m.ensureLoad())
	}
	return m, nil
}

func (m *Model) handleKey(k tea.KeyPressMsg) tea.Cmd {
	s := k.String()
	if m.filtering {
		switch s {
		case "enter":
			m.filtering = false
		case "esc":
			m.filtering, m.query = false, ""
			m.rebuild()
		case "backspace":
			if r := []rune(m.query); len(r) > 0 {
				m.query = string(r[:len(r)-1])
				m.rebuild()
			}
		case "up", "down":
			m.move(map[string]int{"up": -1, "down": 1}[s])
		default:
			if k.Text != "" {
				m.query += k.Text
				m.rebuild()
			}
		}
		return nil
	}

	switch s {
	case "/":
		m.filtering = true
	case "esc", "backspace", "left", "h":
		switch {
		case m.query != "" && s == "esc":
			m.query = ""
			m.rebuild()
		case m.mode == modeSymbols:
			m.closePkg()
		}
	case "enter", "right", "l":
		if it := m.selected(); it != nil && it.pkg != nil && m.mode == modePkgs {
			m.openPkg(it.pkg)
		}
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "home", "g":
		m.move(-len(m.items))
	case "end", "G":
		m.move(len(m.items))
	case "pgup", "ctrl+u":
		m.detailOff = max(0, m.detailOff-max(1, m.listH/2))
	case "pgdown", "ctrl+d":
		m.detailOff += max(1, m.listH/2)
	case "o":
		return m.openSource()
	case "w":
		return m.openWeb()
	}
	return nil
}

func (m *Model) openPkg(p *godoc.Pkg) {
	m.mode, m.open, m.query = modeSymbols, p, ""
	m.cursor, m.offset, m.detailOff = 0, 0, 0
	m.rebuild()
}

func (m *Model) closePkg() {
	prev := m.open
	m.mode, m.open, m.query = modePkgs, nil, ""
	m.rebuild()
	for i, it := range m.items {
		if it.pkg != nil && prev != nil && it.pkg.ImportPath == prev.ImportPath {
			m.cursor = i
		}
	}
	m.detailOff = 0
}

// current renvoie le paquet et le symbole affichés à droite.
func (m *Model) current() (*godoc.Pkg, *godoc.Symbol) {
	it := m.selected()
	if m.mode == modeSymbols {
		if it == nil {
			return m.open, nil
		}
		return m.open, it.sym
	}
	if it == nil {
		return nil, nil
	}
	return it.pkg, nil
}

// ensureLoad charge la documentation du paquet affiché.
func (m *Model) ensureLoad() tea.Cmd {
	p, _ := m.current()
	if p == nil {
		return nil
	}
	path := p.ImportPath
	if m.docs[path] != nil || m.docErr[path] != nil || m.pending[path] {
		return nil
	}
	m.pending[path] = true
	pkg := *p
	return func() tea.Msg {
		d, err := godoc.Load(pkg)
		return loadMsg{path: pkg.ImportPath, doc: d, err: err}
	}
}

func (m *Model) openSource() tea.Cmd {
	p, sym := m.current()
	switch {
	case sym != nil:
		return panel.Open(sym.File, sym.Line)
	case p != nil:
		return editor.Open(p.Dir, "", 0)
	}
	return nil
}

func (m *Model) openWeb() tea.Cmd {
	p, sym := m.current()
	if p == nil {
		return nil
	}
	url := "https://pkg.go.dev/" + p.ImportPath
	if sym != nil {
		url += "#" + sym.Name
	}
	if err := exec.Command("xdg-open", url).Start(); err != nil {
		return panel.Flash(i18n.T("Impossible d'ouvrir le navigateur : ")+err.Error(), true)
	}
	return panel.Flash(i18n.T("Ouverture de ")+url, false)
}

func (m *Model) selected() *item {
	if m.cursor >= 0 && m.cursor < len(m.items) && m.items[m.cursor].selectable() {
		return &m.items[m.cursor]
	}
	return nil
}

func (m *Model) move(d int) {
	var idx []int
	for i, it := range m.items {
		if it.selectable() {
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
	m.detailOff = 0
}

func (m *Model) match(s string) bool {
	return m.query == "" || strings.Contains(strings.ToLower(s), strings.ToLower(m.query))
}

// rebuild recalcule la liste selon le mode et le filtre.
func (m *Model) rebuild() {
	m.items = m.items[:0]
	if m.mode == modeSymbols && m.open != nil {
		m.items = append(m.items, item{header: m.open.ImportPath})
		if m.query == "" {
			m.items = append(m.items, item{overview: true})
		}
		if d := m.docs[m.open.ImportPath]; d != nil {
			for i := range d.Symbols {
				if m.match(d.Symbols[i].Name) {
					m.items = append(m.items, item{sym: &d.Symbols[i]})
				}
			}
		}
	} else {
		add := func(title string, pkgs []godoc.Pkg) {
			var keep []item
			for i := range pkgs {
				if m.match(pkgs[i].ImportPath) {
					keep = append(keep, item{pkg: &pkgs[i]})
				}
			}
			if len(keep) > 0 {
				m.items = append(m.items, item{header: fmt.Sprintf("%s (%d)", title, len(keep))})
				m.items = append(m.items, keep...)
			}
		}
		add(i18n.T("Module"), m.module)
		add(i18n.T("Bibliothèque standard"), m.std)
	}
	if m.selected() == nil {
		m.cursor = -1
		m.move(0)
	}
}

// ─── Vue ─────────────────────────────────────────────────────────────────────

func (m *Model) View(width, height int) string {
	t := m.th
	var bar string
	switch {
	case m.filtering:
		bar = t.Label.Render(i18n.T("Recherche : ")) + t.Value.Render(m.query) + lipgloss.NewStyle().Foreground(t.P.Accent).Render("▌")
	case m.query != "":
		bar = t.Label.Render(i18n.T("Filtre : ")) + t.Value.Render(m.query) + t.Dim.Render(i18n.T("  (Échap pour effacer)"))
	case m.mode == modeSymbols:
		bar = t.Dim.Render(i18n.T("Symboles de ")) + t.Value.Render(m.open.ImportPath) + t.Dim.Render(i18n.T("  ·  / pour filtrer"))
	default:
		bar = t.Dim.Render(fmt.Sprintf(i18n.T("%d paquets du module · %d de la bibliothèque standard  ·  / pour rechercher"), len(m.module), len(m.std)))
	}
	bodyH := max(1, height-2)
	top := ansi.Truncate(bar, width, "…") + "\n\n"

	switch {
	case !m.listed:
		return top + t.Dim.Render(i18n.T("Chargement des paquets…"))
	case m.listErr != nil && len(m.module)+len(m.std) == 0:
		return top + t.Err.Render("go list : "+m.listErr.Error())
	}

	listW := min(max(28, width*35/100), 48)
	detailW := max(10, width-listW-3)
	sep := t.Dim.Render(strings.TrimSuffix(strings.Repeat("│\n", bodyH), "\n"))
	return top + lipgloss.JoinHorizontal(lipgloss.Top, m.viewList(listW, bodyH), " ", sep, " ", m.viewDetail(detailW, bodyH))
}

func (m *Model) kindIcon(k godoc.Kind) string {
	t := m.th
	switch k {
	case godoc.Type:
		return lipgloss.NewStyle().Foreground(t.P.Blue).Render("T")
	case godoc.Func:
		return lipgloss.NewStyle().Foreground(t.P.Accent).Render("ƒ")
	case godoc.Method:
		return lipgloss.NewStyle().Foreground(t.P.Accent).Render("m")
	case godoc.Const:
		return t.Warn.Render("c")
	}
	return t.OK.Render("v")
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
	if m.offset > 0 && m.cursor == m.offset && !m.items[m.offset-1].selectable() {
		m.offset--
	}
	m.offset = min(max(m.offset, 0), max(0, len(m.items)-h))

	lines := make([]string, 0, h)
	for i := m.offset; i < len(m.items) && len(lines) < h; i++ {
		it := m.items[i]
		var line string
		switch {
		case it.header != "":
			line = t.Label.Bold(true).Render(it.header)
		case it.overview:
			line = "  " + t.Text.Render(i18n.T("Vue d'ensemble"))
		case it.sym != nil:
			indent := "  "
			if it.sym.Parent != "" {
				indent = "    "
			}
			name := it.sym.Name
			if it.sym.Kind == godoc.Method {
				name = strings.TrimPrefix(name, it.sym.Parent+".")
			}
			line = indent + m.kindIcon(it.sym.Kind) + " " + t.Text.Render(name)
		case it.pkg != nil:
			path := it.pkg.ImportPath
			if !it.pkg.Std {
				if rel := strings.TrimPrefix(strings.TrimPrefix(path, m.proj.Module), "/"); rel != "" {
					path = rel
				} else {
					path = m.proj.Name() + i18n.T(" (racine)")
				}
			}
			line = "  " + t.Text.Render(path)
		}
		line = ansi.Truncate(line, w, "…")
		if i == m.cursor {
			line = lipgloss.NewStyle().Background(t.P.Surface0).Width(w).Render(line)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = append(lines, t.Dim.Render(i18n.T("Aucun résultat")))
	}
	return lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(strings.Join(lines, "\n"))
}

func (m *Model) viewDetail(w, h int) string {
	t := m.th
	box := lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h)
	p, sym := m.current()
	if p == nil {
		return box.Render(t.Dim.Render(i18n.T("Aucune sélection")))
	}
	if err := m.docErr[p.ImportPath]; err != nil {
		return box.Render(t.Err.Render(err.Error()))
	}
	d := m.docs[p.ImportPath]
	if d == nil {
		return box.Render(t.Dim.Render(i18n.T("Chargement de la documentation…")))
	}

	name := ""
	if sym != nil {
		name = sym.Name
	}
	lines := m.render(d, name, w)
	m.detailOff = min(m.detailOff, max(0, len(lines)-h))
	view := lines[m.detailOff:]
	if m.detailOff > 0 || len(lines) > h {
		pos := t.Dim.Render(fmt.Sprintf("%d%%", 100*min(m.detailOff+h, len(lines))/max(1, len(lines))))
		if len(view) >= h {
			view = append(view[:h-1:h-1], lipgloss.PlaceHorizontal(w, lipgloss.Right, pos))
		}
	}
	return box.Render(strings.Join(view, "\n"))
}

// render convertit la documentation en texte coloré, avec un cache par
// paquet, symbole, largeur et thème.
func (m *Model) render(d *godoc.Doc, sym string, w int) []string {
	key := fmt.Sprintf("%s|%s|%d|%v|%s", d.Pkg.ImportPath, sym, w, m.th.Dark, i18n.Current()) // titres traduits
	if out, ok := m.rendered[key]; ok {
		return out
	}
	md := d.Overview()
	if sym != "" {
		md = d.Symbol(sym)
	}

	if m.renderer == nil || m.rWidth != w || m.rDark != m.th.Dark {
		style := "light"
		if m.th.Dark {
			style = "dark"
		}
		r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(max(20, w-4)))
		if err == nil {
			m.renderer, m.rWidth, m.rDark = r, w, m.th.Dark
		}
	}
	out := md
	if m.renderer != nil {
		if s, err := m.renderer.Render(md); err == nil {
			out = s
		}
	}
	lines := strings.Split(strings.Trim(out, "\n"), "\n")
	for i, l := range lines {
		lines[i] = ansi.Truncate(strings.TrimRight(l, " "), w, "…")
	}
	m.rendered[key] = lines
	return lines
}
