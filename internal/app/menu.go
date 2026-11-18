package app

// Barre de menus (Fichier, Édition, Affichage, Modules, actions du module
// affiché, Aide) et fenêtres d'aide.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/laurentpoirierfr/gopit/internal/config"
	"github.com/laurentpoirierfr/gopit/internal/editor"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/version"
)

// menuItem est une entrée de menu ; un séparateur n'a pas de libellé.
type menuItem struct {
	label    string
	key      string // raccourci affiché
	sep      bool
	disabled bool
	checked  *bool // case à cocher (nil : pas de case)
	run      func(m *Model) tea.Cmd
}

type menu struct {
	title  string
	hotkey string // Alt+lettre (vide : aucun)
	items  []menuItem
}

// menuState est l'état de la barre de menus.
type menuState struct {
	open   int // menu ouvert (-1 : aucun)
	cursor int
}

const brandText = " gopit"

func yes(b bool) *bool { return &b }

// menus construit les menus selon l'état courant (module affiché…).
func (m *Model) menus() []menu {
	// Le menu Édition agit sur le module affiché s'il édite du texte.
	ed, canEdit := m.panels[m.cur].(panel.Editor)
	canEdit = canEdit && ed.CanEdit()
	act := func(label, key string, a panel.EditAction) menuItem {
		return menuItem{label: label, key: key, disabled: !canEdit,
			run: func(m *Model) tea.Cmd { m.focus = focusContent; return m.toPanel(panel.EditMsg{Action: a}) }}
	}
	edit := []menuItem{
		act("Annuler", "Ctrl+Z", panel.ActUndo), act("Rétablir", "Ctrl+Y", panel.ActRedo), {sep: true},
		act("Couper", "Ctrl+X", panel.ActCut), act("Copier", "Ctrl+Insert", panel.ActCopy),
		act("Coller", "Ctrl+V", panel.ActPaste), act("Tout sélectionner", "Ctrl+A", panel.ActSelectAll), {sep: true},
		act("Rechercher", "Ctrl+F", panel.ActFind), act("Occurrence suivante", "F3", panel.ActFindNext),
		act("Aller à la ligne", "Ctrl+G", panel.ActGoto), {sep: true},
		act("Commenter / décommenter", "Ctrl+/", panel.ActComment), act("Formater (gofmt)", "Alt+Maj+F", panel.ActFormat),
	}
	if !canEdit {
		edit = append(edit, menuItem{label: "Ouvre un fichier dans l'Éditeur pour modifier", disabled: true})
	}

	var modules []menuItem
	for i, p := range m.panels {
		modules = append(modules, menuItem{label: p.Icon() + "  " + p.Title(), key: fmt.Sprint(i + 1), checked: yes(i == m.cur),
			run: func(m *Model) tea.Cmd { m.cur, m.focus = i, focusContent; return nil }})
	}

	cur := m.panels[m.cur]
	var actions []menuItem
	if c, ok := cur.(panel.Commander); ok {
		for _, cmd := range c.Commands() {
			actions = append(actions, menuItem{label: cmd.Title, key: keyLabel(cmd.Key),
				run: func(m *Model) tea.Cmd { m.focus = focusContent; return m.toPanel(commandMsg(cmd)) }})
		}
	}
	if len(actions) == 0 {
		actions = []menuItem{{label: "Aucune action", disabled: true}}
	}

	return []menu{
		{title: "Fichier", hotkey: "alt+f", items: []menuItem{
			{label: "Ouvrir un fichier…", key: "Ctrl+O", run: func(m *Model) tea.Cmd { return m.openPicker() }},
			{label: "Fermer le fichier", key: "Ctrl+W", disabled: !canEdit,
				run: func(m *Model) tea.Cmd { return m.toPanel(panel.EditMsg{Action: panel.ActClose}) }},
			{label: "Ouvrir le projet dans l'éditeur externe", key: "e",
				run: func(m *Model) tea.Cmd { return editor.Open(m.proj.Root, "", 0) }},
			{label: "Ouvrir les fichiers dans l'éditeur externe", checked: yes(m.external),
				run: func(m *Model) tea.Cmd { m.external = !m.external; return nil }},
			{label: "Préférences…", run: func(m *Model) tea.Cmd {
				m.saveConfig()
				p, err := config.Path()
				if err != nil {
					return m.setFlash("Préférences : "+err.Error(), true)
				}
				return editor.Open(m.proj.Root, p, 0)
			}},
			{sep: true},
			{label: "Quitter", key: "q", run: func(m *Model) tea.Cmd { return m.quit() }},
		}},
		{title: "Édition", hotkey: "alt+e", items: edit},
		{title: "Affichage", hotkey: "alt+a", items: []menuItem{
			{label: "Barre latérale des modules", checked: yes(m.sidebar), run: func(m *Model) tea.Cmd {
				m.sidebar = !m.sidebar
				if !m.sidebar {
					m.focus = focusContent
				}
				return nil
			}},
			{label: "Ligne des raccourcis", checked: yes(!m.hideHints), run: func(m *Model) tea.Cmd {
				m.hideHints = !m.hideHints
				return m.resize()
			}},
			{sep: true},
			{label: "Palette de commandes", key: "Ctrl+P", run: func(m *Model) tea.Cmd { m.openPalette(); return nil }},
		}},
		{title: "Modules", hotkey: "alt+m", items: modules},
		{title: cur.Title(), items: actions},
		{title: "Aide", hotkey: "alt+h", items: []menuItem{
			{label: "Raccourcis clavier", key: "F1", run: func(m *Model) tea.Cmd { m.modal = modalHelp; return nil }},
			{label: "À propos de gopit", run: func(m *Model) tea.Cmd { m.modal = modalAbout; return nil }},
		}},
	}
}

// keyLabel présente une touche pour l'affichage.
func keyLabel(k string) string {
	switch k {
	case "enter":
		return "Entrée"
	case "esc":
		return "Échap"
	case "f5":
		return "F5"
	}
	return k
}

// menuSpans renvoie la position horizontale de chaque titre de menu.
func (m *Model) menuSpans(menus []menu) []span {
	x := lipgloss.Width(m.th.Brand.Render(brandText)) + 1
	spans := make([]span, len(menus))
	for i, mn := range menus {
		w := lipgloss.Width(mn.title) + 2
		spans[i] = span{x, w}
		x += w
	}
	return spans
}

type span struct{ x, w int }

func (m *Model) openMenu(i int) {
	menus := m.menus()
	n := len(menus)
	m.menu.open = ((i % n) + n) % n
	m.menu.cursor = nextItem(menus[m.menu.open].items, -1, 1)
}

// nextItem renvoie l'entrée sélectionnable suivante (séparateurs et entrées
// grisées sautés), ou -1 s'il n'y en a aucune.
func nextItem(items []menuItem, cur, d int) int {
	n := len(items)
	for i := 1; i <= n; i++ {
		j := ((cur+d*i)%n + n) % n
		if !items[j].sep && !items[j].disabled {
			return j
		}
	}
	return -1
}

func (m *Model) menuKey(s string) tea.Cmd {
	menus := m.menus()
	mn := menus[m.menu.open]
	switch s {
	case "esc", "f10":
		m.menu.open = -1
	case "left", "h":
		m.openMenu(m.menu.open - 1)
	case "right", "l":
		m.openMenu(m.menu.open + 1)
	case "up", "k", "shift+tab":
		m.menu.cursor = nextItem(mn.items, m.menu.cursor, -1)
	case "down", "j", "tab":
		m.menu.cursor = nextItem(mn.items, m.menu.cursor, 1)
	case "enter", "space", " ":
		m.menu.open = -1
		if c := m.menu.cursor; c >= 0 && c < len(mn.items) && mn.items[c].run != nil && !mn.items[c].disabled {
			return mn.items[c].run(m)
		}
	}
	return nil
}

// dropdownRect renvoie la position et la taille du menu déroulant ouvert.
func (m *Model) dropdownRect() (x, y, w, h int) {
	menus := m.menus()
	box := m.viewDropdown(menus)
	return m.menuSpans(menus)[m.menu.open].x, topH, lipgloss.Width(box), lipgloss.Height(box)
}

// menuClick traite un clic sur la barre de menus ou un menu ouvert ; il
// renvoie false si le clic ne concerne pas les menus.
func (m *Model) menuClick(x, y int) (tea.Cmd, bool) {
	menus := m.menus()
	if m.menu.open >= 0 {
		dx, dy, dw, dh := m.dropdownRect()
		if x >= dx && x < dx+dw && y >= dy && y < dy+dh {
			items := menus[m.menu.open].items
			if i := y - dy - 1; i >= 0 && i < len(items) && !items[i].sep && !items[i].disabled && items[i].run != nil {
				m.menu.open = -1
				return items[i].run(m), true
			}
			return nil, true
		}
	}
	if y >= topH {
		if m.menu.open >= 0 {
			m.menu.open = -1 // clic ailleurs : fermer le menu
			return nil, true
		}
		return nil, false
	}
	for i, sp := range m.menuSpans(menus) {
		if x >= sp.x && x < sp.x+sp.w {
			if m.menu.open == i {
				m.menu.open = -1
			} else {
				m.openMenu(i)
			}
			return nil, true
		}
	}
	m.menu.open = -1
	return nil, true
}

// ─── Vue ─────────────────────────────────────────────────────────────────────

func (m *Model) viewMenuBar() string {
	t := m.th
	menus := m.menus()
	bar := lipgloss.NewStyle().Background(t.P.Mantle).Foreground(t.P.Text)
	var b strings.Builder
	b.WriteString(t.Brand.Render(brandText))
	b.WriteString(bar.Render(" "))
	for i, mn := range menus {
		st := bar.Padding(0, 1)
		switch {
		case i == m.menu.open:
			st = lipgloss.NewStyle().Background(t.P.Surface1).Foreground(t.P.Accent).Bold(true).Padding(0, 1)
		case i == len(menus)-2: // menu du module affiché
			st = st.Foreground(t.P.Accent)
		}
		b.WriteString(st.Render(mn.title))
	}
	right := t.TopInfo.Render(fmt.Sprintf("go %s · gopit %s ", m.proj.GoVersion, version.Version))
	return fill(bar, b.String(), m.w-lipgloss.Width(right)) + right
}

func (m *Model) viewDropdown(menus []menu) string {
	t := m.th
	mn := menus[m.menu.open]
	lw, kw := 0, 0
	for _, it := range mn.items {
		lw = max(lw, lipgloss.Width(it.label))
		kw = max(kw, lipgloss.Width(it.key))
	}
	inner := 1 + 2 + lw + 3 + kw + 1
	bg := lipgloss.NewStyle().Background(t.P.Mantle)

	lines := make([]string, len(mn.items))
	for i, it := range mn.items {
		if it.sep {
			lines[i] = bg.Foreground(t.P.Surface1).Render(strings.Repeat("─", inner))
			continue
		}
		mark := "  "
		if it.checked != nil && *it.checked {
			mark = "✓ "
		}
		left := " " + mark + it.label
		pad := strings.Repeat(" ", max(1, inner-lipgloss.Width(left)-lipgloss.Width(it.key)-1))
		switch {
		case i == m.menu.cursor && !it.disabled:
			lines[i] = lipgloss.NewStyle().Background(t.P.Accent).Foreground(t.P.Base).Bold(true).Render(left + pad + it.key + " ")
		case it.disabled:
			lines[i] = bg.Foreground(t.P.Surface1).Render(left + pad + it.key + " ")
		default:
			lines[i] = bg.Foreground(t.P.Text).Render(left+pad) + bg.Foreground(t.P.Overlay0).Render(it.key+" ")
		}
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.P.Accent).
		BorderBackground(t.P.Mantle).Background(t.P.Mantle).Render(strings.Join(lines, "\n"))
}

// ─── Fenêtres d'aide ─────────────────────────────────────────────────────────

type modalKind int

const (
	modalNone modalKind = iota
	modalHelp
	modalAbout
)

func (m *Model) viewModal() string {
	t := m.th
	bg := lipgloss.NewStyle().Background(t.P.Mantle)
	title := bg.Foreground(t.P.Accent).Bold(true)
	keySt := bg.Foreground(t.P.Blue).Bold(true)
	text := bg.Foreground(t.P.Text)
	row := func(k, d string) string { return keySt.Render(fmt.Sprintf("  %-14s", k)) + text.Render(d) }

	var lines []string
	switch m.modal {
	case modalHelp:
		lines = []string{
			title.Render("Raccourcis clavier"), "",
			title.Render("Général"),
			row("F10 / Alt+…", "barre de menus (Alt+F, E, A, M, H)"),
			row("Ctrl+P", "palette de commandes"),
			row(fmt.Sprintf("1…%d", len(m.panels)), "changer de module"),
			row("F1", "cette aide"),
			row("e", "éditeur externe"),
			row("Ctrl+O", "ouvrir un fichier dans l'éditeur intégré"),
			row("q / Ctrl+C", "quitter"),
			"",
			title.Render("Module " + m.panels[m.cur].Title()),
		}
		for _, h := range m.panels[m.cur].Hints() {
			lines = append(lines, row(h.Key, h.Desc))
		}
	case modalAbout:
		lines = []string{
			title.Render(brandText + " " + version.Version), "",
			text.Render("Le cockpit du développeur Go dans le terminal."),
			text.Render("Bubble Tea v2 · Lip Gloss v2 · Glamour v2"), "",
			text.Render("Projet : " + m.proj.Module),
			text.Render("Go " + m.proj.GoVersion),
		}
	}
	lines = append(lines, "", bg.Foreground(t.P.Overlay0).Render("Échap pour fermer"))
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.P.Accent).
		BorderBackground(t.P.Mantle).Background(t.P.Mantle).Padding(1, 3).Render(strings.Join(lines, "\n"))
}

// overlays superpose le menu ouvert, la palette et les fenêtres d'aide.
func (m *Model) overlays(base string) string {
	layers := []*lipgloss.Layer{lipgloss.NewLayer(base)}
	if m.menu.open >= 0 {
		x, y, _, _ := m.dropdownRect()
		layers = append(layers, lipgloss.NewLayer(m.viewDropdown(m.menus())).X(x).Y(y).Z(1))
	}
	if m.modal != modalNone {
		box := m.viewModal()
		layers = append(layers, lipgloss.NewLayer(box).
			X(max(0, (m.w-lipgloss.Width(box))/2)).Y(max(0, (m.h-lipgloss.Height(box))/2)).Z(2))
	}
	if m.pal.open {
		box := m.viewPalette()
		layers = append(layers, lipgloss.NewLayer(box).
			X(max(0, (m.w-lipgloss.Width(box))/2)).Y(max(0, min(4, m.h-lipgloss.Height(box)))).Z(3))
	}
	if len(layers) == 1 {
		return base
	}
	return lipgloss.NewCompositor(layers...).Render()
}
