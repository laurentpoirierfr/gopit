// Package app assemble le cockpit : barre de menus en haut, module affiché
// (avec une barre latérale des modules, masquable), barre d'état et
// raccourcis en bas.
package app

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/config"
	"github.com/laurentpoirierfr/gopit/internal/editor"
	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/panels/benchmarks"
	"github.com/laurentpoirierfr/gopit/internal/panels/code"
	"github.com/laurentpoirierfr/gopit/internal/panels/diagnostics"
	"github.com/laurentpoirierfr/gopit/internal/panels/docs"
	"github.com/laurentpoirierfr/gopit/internal/panels/git"
	"github.com/laurentpoirierfr/gopit/internal/panels/home"
	"github.com/laurentpoirierfr/gopit/internal/panels/modules"
	"github.com/laurentpoirierfr/gopit/internal/panels/tests"
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/theme"
)

type focus int

const (
	focusNav focus = iota
	focusContent
)

const (
	topH = 1  // barre de menus
	navW = 24 // largeur de la barre latérale (bordures comprises)
)

type clearFlashMsg int

type Model struct {
	th     *theme.Theme
	proj   *project.Project
	panels []panel.Panel
	cur    int
	focus  focus
	w, h   int

	flash    string
	flashErr bool
	flashID  int

	pal       palette        // palette de commandes (palette.go)
	menu      menuState      // barre de menus (menu.go)
	modal     modalKind      // fenêtre d'aide ouverte
	sidebar   bool           // barre latérale des modules
	hideHints bool           // ligne des raccourcis masquée
	external  bool           // ouvrir les fichiers dans l'éditeur externe
	quitArmed bool           // fichiers non enregistrés : quitter une 2e fois
	langSet   bool           // langue choisie dans les menus : à enregistrer
	dragging  bool           // glissement de la souris commencé dans le module
	cfg       *config.Config // préférences, enregistrées en quittant
}

// New crée le cockpit pour le projet p.
func New(p *project.Project) *Model {
	th := theme.New()
	m := &Model{
		th:   th,
		proj: p,
		panels: []panel.Panel{
			home.New(th, p),
			tests.New(th, p),
			diagnostics.New(th, p),
			modules.New(th, p),
			docs.New(th, p),
			benchmarks.New(th, p),
			git.New(th, p),
			code.New(th, p),
		},
		menu:  menuState{open: -1},
		focus: focusContent,
	}
	m.restoreConfig()
	return m
}

// footerH renvoie la hauteur du bas de l'écran (barre d'état et raccourcis).
func (m *Model) footerH() int {
	if m.hideHints {
		return 1
	}
	return 2
}

// resize est appelé quand la place disponible change (lignes masquées…) :
// les modules recalculent leur affichage au prochain rendu.
func (m *Model) resize() tea.Cmd { return nil }

func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.RequestBackgroundColor}
	for _, p := range m.panels {
		cmds = append(cmds, p.Init())
	}
	return tea.Batch(cmds...)
}

// ─── Update ──────────────────────────────────────────────────────────────────

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, m.broadcast(msg)

	case tea.BackgroundColorMsg:
		m.th.Set(msg.IsDark())
		return m, nil

	case panel.FlashMsg:
		return m, m.setFlash(msg.Text, msg.Error)

	case clearFlashMsg:
		if int(msg) == m.flashID {
			m.flash = ""
		}
		return m, nil

	case panel.OpenMsg:
		return m, m.openFile(msg.File, msg.Line)

	case tea.PasteMsg:
		// Un collage ne concerne que le module affiché.
		if m.pal.open || m.menu.open >= 0 || m.modal != modalNone {
			return m, nil
		}
		return m, m.toPanel(msg)

	case editor.DoneMsg:
		if msg.Err != nil {
			return m, m.setFlash(i18n.T("Éditeur : ")+msg.Err.Error(), true)
		}
		return m, nil

	case tea.KeyPressMsg:
		return m, m.handleKey(msg)

	case tea.MouseClickMsg:
		mo := tea.Mouse(msg)
		if mo.Button == tea.MouseLeft {
			return m, m.handleClick(mo.X, mo.Y, mo.Mod.Contains(tea.ModShift))
		}
		return m, nil

	case tea.MouseMotionMsg:
		mo := tea.Mouse(msg)
		if m.dragging && mo.Button == tea.MouseLeft {
			x, y := m.contentPos(mo.X, mo.Y)
			return m, m.toPanel(panel.ClickMsg{X: x, Y: y, Drag: true})
		}
		return m, nil

	case tea.MouseReleaseMsg:
		m.dragging = false
		return m, nil

	case tea.MouseWheelMsg:
		// La molette fait défiler le module affiché (et lui seul), en
		// coordonnées relatives à sa zone.
		if m.pal.open || m.menu.open >= 0 || m.modal != modalNone {
			return m, nil
		}
		msg.X, msg.Y = m.contentPos(msg.X, msg.Y)
		return m, m.toPanel(msg)
	}

	// Les autres messages (résultats asynchrones…) vont à tous les panneaux.
	return m, m.broadcast(msg)
}

func (m *Model) broadcast(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	for i, p := range m.panels {
		np, cmd := p.Update(msg)
		m.panels[i] = np
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

func (m *Model) handleKey(k tea.KeyPressMsg) tea.Cmd {
	s := k.String()
	if s == "ctrl+c" && !m.pal.open && m.modal == modalNone && m.menu.open < 0 {
		if c, ok := m.panels[m.cur].(panel.Copier); ok && c.CopiesOnCtrlC() {
			m.quitArmed = false
			return m.toPanel(k)
		}
	}
	if s != "q" && s != "ctrl+c" && s != "ctrl+q" {
		m.quitArmed = false
	}
	if s == "ctrl+c" || s == "ctrl+q" {
		return m.quit()
	}
	// Fenêtres superposées, dans l'ordre : palette, aide, menu ouvert.
	if m.pal.open {
		return m.paletteKey(k)
	}
	if m.modal != modalNone {
		m.modal = modalNone // n'importe quelle touche ferme l'aide
		return nil
	}
	if m.menu.open >= 0 {
		return m.menuKey(s)
	}
	switch s {
	case "ctrl+p":
		m.openPalette()
		return nil
	case "f10":
		m.openMenu(0)
		return nil
	case "f1":
		m.modal = modalHelp
		return nil
	case "ctrl+o":
		return m.openPicker()
	}
	for i, mn := range m.menus() {
		if mn.hotkey != "" && s == mn.hotkey {
			m.openMenu(i)
			return nil
		}
	}
	// Saisie en cours dans le panneau : il reçoit toutes les touches.
	if c, ok := m.panels[m.cur].(panel.Capturer); ok && c.Capturing() {
		return m.toPanel(k)
	}
	switch s {
	case "q":
		return m.quit()
	case "tab", "shift+tab":
		if m.sidebar {
			if m.focus == focusNav {
				m.focus = focusContent
			} else {
				m.focus = focusNav
			}
			return nil
		}
	case "e":
		return editor.Open(m.proj.Root, "", 0)
	}
	if len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
		if i := int(s[0] - '1'); i < len(m.panels) {
			m.cur = i
		}
		return nil
	}

	if m.sidebar && m.focus == focusNav {
		switch s {
		case "up", "k":
			m.cur = (m.cur - 1 + len(m.panels)) % len(m.panels)
		case "down", "j":
			m.cur = (m.cur + 1) % len(m.panels)
		case "enter", "right", "l":
			m.focus = focusContent
		default:
			// Les raccourcis du panneau restent accessibles depuis la navigation.
			return m.toPanel(k)
		}
		return nil
	}
	// Échap va au module (fermer une vue, effacer un filtre…).
	return m.toPanel(k)
}

func (m *Model) toPanel(msg tea.Msg) tea.Cmd {
	np, cmd := m.panels[m.cur].Update(msg)
	m.panels[m.cur] = np
	// Une saisie qui commence (ex. « / » depuis la navigation) prend le focus.
	if c, ok := np.(panel.Capturer); ok && c.Capturing() {
		m.focus = focusContent
	}
	return cmd
}

// contentOrigin renvoie le coin haut gauche de la zone du module (celle de
// Panel.View) à l'écran ; voir viewContent.
func (m *Model) contentOrigin() (x, y int) {
	x = 2 // bordure et marge
	if m.sidebar {
		x += navW
	}
	return x, topH + 1 + 2 // bordure, titre et ligne vide
}

// contentPos convertit une position à l'écran en position dans le module.
func (m *Model) contentPos(x, y int) (int, int) {
	ox, oy := m.contentOrigin()
	return x - ox, y - oy
}

func (m *Model) handleClick(x, y int, shift bool) tea.Cmd {
	m.dragging = false
	switch {
	case m.pal.open:
		return nil
	case m.modal != modalNone:
		m.modal = modalNone
		return nil
	}
	if cmd, ok := m.menuClick(x, y); ok {
		return cmd
	}
	if y < topH || y >= m.h-m.footerH() {
		return nil
	}
	if m.sidebar && x < navW {
		if i := y - topH - 1; i >= 0 && i < len(m.panels) {
			m.cur = i
		}
		m.focus = focusNav
		return nil
	}
	m.focus = focusContent
	if px, py := m.contentPos(x, y); px >= 0 && py >= 0 {
		m.dragging = true
		return m.toPanel(panel.ClickMsg{X: px, Y: py, Shift: shift})
	}
	return nil
}

// openFile ouvre un fichier demandé par un module : dans l'éditeur intégré
// (qui devient le module affiché), ou dans l'éditeur externe.
func (m *Model) openFile(file string, line int) tea.Cmd {
	if !m.external {
		for i, p := range m.panels {
			if o, ok := p.(panel.Opener); ok {
				m.cur, m.focus = i, focusContent
				return o.OpenFile(file, line)
			}
		}
	}
	return editor.Open(m.proj.Root, file, line)
}

// openPicker affiche l'éditeur intégré et sa liste de fichiers à ouvrir.
func (m *Model) openPicker() tea.Cmd {
	for i, p := range m.panels {
		if _, ok := p.(panel.Opener); ok {
			m.cur, m.focus = i, focusContent
			return m.toPanel(panel.EditMsg{Action: panel.ActOpen})
		}
	}
	return nil
}

// unsaved renvoie les fichiers non enregistrés de tous les modules.
func (m *Model) unsaved() []string {
	var out []string
	for _, p := range m.panels {
		if g, ok := p.(panel.Guard); ok {
			out = append(out, g.Unsaved()...)
		}
	}
	return out
}

func (m *Model) setFlash(s string, isErr bool) tea.Cmd {
	m.flashID++
	id := m.flashID
	m.flash, m.flashErr = s, isErr
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return clearFlashMsg(id) })
}

// ─── Vue ─────────────────────────────────────────────────────────────────────

func (m *Model) View() tea.View {
	var v tea.View
	if m.w == 0 || m.h == 0 {
		v = tea.NewView(i18n.T("Chargement…"))
	} else {
		v = tea.NewView(m.render())
	}
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "gopit — " + m.proj.Module
	return v
}

func (m *Model) render() string {
	bodyH := max(5, m.h-topH-m.footerH())
	body := m.viewContent(bodyH)
	if m.sidebar {
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.viewNav(bodyH), body)
	}
	parts := []string{m.viewMenuBar(), body, m.viewStatus()}
	if !m.hideHints {
		parts = append(parts, m.viewHints())
	}
	return m.overlays(lipgloss.JoinVertical(lipgloss.Left, parts...))
}

// ─── Préférences ─────────────────────────────────────────────────────────────

// restoreConfig relit les préférences : dernier module affiché et réglages
// de chaque module.
func (m *Model) restoreConfig() {
	cfg, _ := config.Load() // préférences illisibles : on repart de zéro
	m.cfg = cfg
	m.sidebar, m.hideHints, m.external = cfg.Sidebar, cfg.HideHints, cfg.ExternalEditor
	for i, p := range m.panels {
		if p.Title() == cfg.LastModule {
			m.cur = i
		}
		if ps, ok := p.(panel.Persister); ok {
			if st := cfg.Panels[p.Title()]; st != nil {
				ps.Restore(st)
			}
		}
	}
}

func (m *Model) saveConfig() error {
	if m.cfg == nil {
		return nil
	}
	m.cfg.LastModule = m.panels[m.cur].Title()
	m.cfg.Sidebar, m.cfg.HideHints, m.cfg.ExternalEditor = m.sidebar, m.hideHints, m.external
	if m.langSet {
		m.cfg.Language = string(i18n.Current())
	}
	for _, p := range m.panels {
		if ps, ok := p.(panel.Persister); ok {
			m.cfg.Panels[p.Title()] = ps.State()
		}
	}
	return m.cfg.Save()
}

// quit enregistre les préférences puis quitte. S'il reste des fichiers non
// enregistrés, il faut confirmer en quittant une deuxième fois.
func (m *Model) quit() tea.Cmd {
	if u := m.unsaved(); len(u) > 0 && !m.quitArmed {
		m.quitArmed = true
		what := u[0]
		if len(u) > 1 {
			what = fmt.Sprintf(i18n.T("%d fichiers"), len(u))
		}
		return m.setFlash(what+i18n.T(" non enregistré(s) : q ou Ctrl+Q encore pour quitter sans enregistrer"), true)
	}
	_ = m.saveConfig() // une erreur d'écriture ne doit pas empêcher de quitter
	return tea.Quit
}

// fill complète une ligne avec des espaces stylés jusqu'à la largeur w.
func fill(st lipgloss.Style, s string, w int) string {
	if gap := w - lipgloss.Width(s); gap > 0 {
		return s + st.Render(strings.Repeat(" ", gap))
	}
	return ansi.Truncate(s, w, "…")
}

func (m *Model) viewNav(h int) string {
	t := m.th
	iw := navW - 2
	lines := make([]string, len(m.panels))
	for i, p := range m.panels {
		label := ansi.Truncate(fmt.Sprintf("%d %s  %s", i+1, p.Icon(), i18n.T(p.Title())), iw-2, "…")
		st := t.NavItem
		if i == m.cur {
			st = t.NavItemBlur
			if m.focus == focusNav {
				st = t.NavItemSel
			}
		}
		lines[i] = st.Width(iw).Render(label)
	}
	box := t.Panel
	if m.focus == focusNav {
		box = t.PanelFocus
	}
	return box.Width(navW).Height(h).MaxHeight(h).Render(strings.Join(lines, "\n"))
}

func (m *Model) viewContent(h int) string {
	t := m.th
	w := m.w
	if m.sidebar {
		w = max(10, m.w-navW)
	}
	iw, ih := w-4, h-2 // bordures + marge intérieure
	p := m.panels[m.cur]

	header := t.Title.Render(p.Icon() + "  " + i18n.T(p.Title()))
	content := header + "\n\n" + p.View(iw, max(1, ih-2))

	box := t.Panel
	if m.focus == focusContent {
		box = t.PanelFocus
	}
	return box.Width(w).Height(h).MaxHeight(h).Padding(0, 1).Render(content)
}

func (m *Model) viewStatus() string {
	t := m.th
	mode := strings.ToUpper(i18n.T(m.panels[m.cur].Title()))
	if m.sidebar && m.focus == focusNav {
		mode = i18n.T("NAVIGATION")
	}
	left := t.Badge.Render(mode)
	switch {
	case m.flash != "" && m.flashErr:
		left += t.FlashErr.Render(m.flash)
	case m.flash != "":
		left += t.FlashOK.Render(m.flash)
	default:
		left += t.StatusBar.Render(" " + homeRel(m.proj.Root))
	}
	right := t.StatusInfo.Render(fmt.Sprintf(i18n.T("%d dép. directes"), m.proj.Requires))
	if lipgloss.Width(left)+lipgloss.Width(right) > m.w {
		left = ansi.Truncate(left, max(0, m.w-lipgloss.Width(right)), "…")
	}
	return fill(t.StatusBar, left, m.w-lipgloss.Width(right)) + right
}

func (m *Model) viewHints() string {
	t := m.th
	hints := []panel.Hint{{Key: "F10", Desc: i18n.T("menus")}, {Key: fmt.Sprintf("1-%d", len(m.panels)), Desc: i18n.T("module")}}
	if m.sidebar {
		hints = append(hints, panel.Hint{Key: "Tab", Desc: i18n.T("focus")})
	}
	hints = append(hints, m.panels[m.cur].Hints()...)
	hints = append(hints, panel.Hint{Key: "Ctrl+P", Desc: i18n.T("commandes")}, panel.Hint{Key: "F1", Desc: i18n.T("aide")}, panel.Hint{Key: "q", Desc: i18n.T("quitter")})

	parts := make([]string, len(hints))
	for i, h := range hints {
		parts[i] = t.HintKey.Render(i18n.Key(h.Key)) + t.Hint.Render(" "+h.Desc)
	}
	return fill(lipgloss.NewStyle(), " "+strings.Join(parts, t.Hint.Render("  ·  ")), m.w)
}

// homeRel remplace le répertoire personnel par « ~ ».
func homeRel(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
