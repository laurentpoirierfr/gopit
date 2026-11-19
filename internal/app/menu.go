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
	"github.com/laurentpoirierfr/gopit/internal/i18n"
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
	accel  int    // place (en runes) de la lettre soulignée dans title (-1 : aucune)
	items  []menuItem
}

// accelMenu crée un menu dont le titre traduit marque sa lettre de raccourci
// par « & » (« &Fichier », « A&yuda ») : Alt+cette lettre ouvre le menu,
// et elle est soulignée dans la barre. Chaque langue choisit ses lettres.
func accelMenu(marked string, items []menuItem) menu {
	mn := menu{title: marked, accel: -1, items: items}
	r := []rune(marked)
	for i := 0; i < len(r)-1; i++ {
		if r[i] == '&' {
			mn.title = string(r[:i]) + string(r[i+1:])
			mn.accel = i
			mn.hotkey = "alt+" + foldLetter(r[i+1])
			break
		}
	}
	return mn
}

// foldLetter met une lettre en minuscule sans accent (« É » → « e ») :
// c'est la touche que l'on presse avec Alt.
func foldLetter(r rune) string {
	s := strings.ToLower(string(r))
	return strings.NewReplacer("é", "e", "è", "e", "ê", "e", "ë", "e", "à", "a", "á", "a", "â", "a",
		"í", "i", "î", "i", "ó", "o", "ô", "o", "ú", "u", "ù", "u", "û", "u", "ü", "u", "ñ", "n", "ç", "c").Replace(s)
}

// hotkeyLetters renvoie les lettres des menus (« F, E, A, M, D ») pour l'aide.
func hotkeyLetters(menus []menu) string {
	var ls []string
	for _, mn := range menus {
		if k, ok := strings.CutPrefix(mn.hotkey, "alt+"); ok {
			ls = append(ls, strings.ToUpper(k))
		}
	}
	return strings.Join(ls, ", ")
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
		act(i18n.T("Annuler"), "Ctrl+Z", panel.ActUndo), act(i18n.T("Rétablir"), "Ctrl+Y", panel.ActRedo), {sep: true},
		act(i18n.T("Couper"), "Ctrl+X", panel.ActCut), act(i18n.T("Copier"), "Ctrl+C", panel.ActCopy),
		act(i18n.T("Coller"), "Ctrl+V", panel.ActPaste), act(i18n.T("Tout sélectionner"), "Ctrl+A", panel.ActSelectAll), {sep: true},
		act(i18n.T("Rechercher"), "Ctrl+F", panel.ActFind), act(i18n.T("Occurrence suivante"), "F3", panel.ActFindNext),
		act(i18n.T("Remplacer…"), "Ctrl+H", panel.ActReplace),
		act(i18n.T("Aller à la ligne"), "Ctrl+G", panel.ActGoto), {sep: true},
		act(i18n.T("Commenter / décommenter"), "Ctrl+/", panel.ActComment), act(i18n.T("Formater (gofmt)"), "Alt+Maj+F", panel.ActFormat), {sep: true},
		act(i18n.T("Aller à la définition"), "F12", panel.ActDefinition), act(i18n.T("Documentation du symbole"), "Ctrl+K", panel.ActDoc),
		act(i18n.T("Revenir à la position précédente"), "Ctrl+T", panel.ActBack),
	}
	if !canEdit {
		edit = append(edit, menuItem{label: i18n.T("Ouvre un fichier dans l'Éditeur pour modifier"), disabled: true})
	}

	var modules []menuItem
	for i, p := range m.panels {
		modules = append(modules, menuItem{label: p.Icon() + "  " + i18n.T(p.Title()), key: moduleKey(i), checked: yes(i == m.cur),
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
		actions = []menuItem{{label: i18n.T("Aucune action"), disabled: true}}
	}

	return []menu{
		accelMenu(i18n.T("&Fichier"), []menuItem{
			{label: i18n.T("Nouveau fichier…"), key: "Ctrl+N", run: func(m *Model) tea.Cmd { return m.editorAction(panel.ActNew) }},
			{label: i18n.T("Ouvrir un fichier…"), key: "Ctrl+O", run: func(m *Model) tea.Cmd { return m.editorAction(panel.ActOpen) }},
			{label: i18n.T("Fermer le fichier"), key: "Ctrl+W", disabled: !canEdit,
				run: func(m *Model) tea.Cmd { return m.toPanel(panel.EditMsg{Action: panel.ActClose}) }},
			{label: i18n.T("Ouvrir le projet dans l'éditeur externe"), key: "e",
				run: func(m *Model) tea.Cmd { return editor.Open(m.proj.Root, "", 0) }},
			{label: i18n.T("Ouvrir les fichiers dans l'éditeur externe"), checked: yes(m.external),
				run: func(m *Model) tea.Cmd { m.external = !m.external; return nil }},
			{label: i18n.T("Préférences…"), run: func(m *Model) tea.Cmd {
				m.saveConfig()
				p, err := config.Path()
				if err != nil {
					return m.setFlash(i18n.T("Préférences : ")+err.Error(), true)
				}
				return editor.Open(m.proj.Root, p, 0)
			}},
			{sep: true},
			{label: i18n.T("Quitter"), key: "Ctrl+Q", run: func(m *Model) tea.Cmd { return m.quit() }},
		}),
		accelMenu(i18n.T("&Édition"), edit),
		accelMenu(i18n.T("&Affichage"), append([]menuItem{
			{label: i18n.T("Barre latérale des modules"), checked: yes(m.sidebar), run: func(m *Model) tea.Cmd {
				m.sidebar = !m.sidebar
				if !m.sidebar {
					m.focus = focusContent
				}
				return nil
			}},
			{label: i18n.T("Ligne des raccourcis"), checked: yes(!m.hideHints), run: func(m *Model) tea.Cmd {
				m.hideHints = !m.hideHints
				return m.resize()
			}},
			{sep: true},
			{label: i18n.T("Palette de commandes"), key: "Ctrl+P", run: func(m *Model) tea.Cmd { m.openPalette(); return nil }},
			{sep: true},
		}, m.langItems()...)),
		accelMenu(i18n.T("&Modules"), modules),
		{title: i18n.T(cur.Title()), accel: -1, items: actions},
		accelMenu(i18n.T("Ai&de"), []menuItem{
			{label: i18n.T("Raccourcis clavier"), key: "F1", run: func(m *Model) tea.Cmd { m.modal = modalHelp; return nil }},
			{label: i18n.T("À propos de gopit"), run: func(m *Model) tea.Cmd { m.modal = modalAbout; return m.avatarCmd() }},
		}),
	}
}

// langItems propose les langues de l'interface (menu Affichage, palette).
func (m *Model) langItems() []menuItem {
	items := make([]menuItem, len(i18n.Langs))
	for i, l := range i18n.Langs {
		items[i] = menuItem{label: l.Name(), checked: yes(i18n.Current() == l), run: func(m *Model) tea.Cmd {
			i18n.Set(l)
			m.langSet = true
			return tea.Batch(m.setFlash(i18n.T("Langue de l'interface : ")+l.Name(), false), m.broadcast(panel.LangMsg{}))
		}}
	}
	return items
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
		if mn.accel < 0 {
			b.WriteString(st.Render(mn.title))
			continue
		}
		r := []rune(mn.title)
		plain := st.UnsetPadding()
		b.WriteString(plain.Render(" "+string(r[:mn.accel])) + plain.Underline(true).Render(string(r[mn.accel])) +
			plain.Render(string(r[mn.accel+1:])+" "))
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
		kw = max(kw, lipgloss.Width(i18n.Key(it.key)))
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
		key := i18n.Key(it.key)
		pad := strings.Repeat(" ", max(1, inner-lipgloss.Width(left)-lipgloss.Width(key)-1))
		switch {
		case i == m.menu.cursor && !it.disabled:
			lines[i] = lipgloss.NewStyle().Background(t.P.Accent).Foreground(t.P.Base).Bold(true).Render(left + pad + key + " ")
		case it.disabled:
			lines[i] = bg.Foreground(t.P.Surface1).Render(left + pad + key + " ")
		default:
			lines[i] = bg.Foreground(t.P.Text).Render(left+pad) + bg.Foreground(t.P.Overlay0).Render(key+" ")
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
	row := func(k, d string) string { return keySt.Render(fmt.Sprintf("  %-14s", i18n.Key(k))) + text.Render(d) }

	var lines []string
	switch m.modal {
	case modalHelp:
		lines = []string{
			title.Render(i18n.T("Raccourcis clavier")), "",
			title.Render(i18n.T("Général")),
			row("F10 / Alt+…", fmt.Sprintf(i18n.T("barre de menus (Alt+%s)"), hotkeyLetters(m.menus()))),
			row("Ctrl+P", i18n.T("palette de commandes")),
			row("1…"+moduleKey(len(m.panels)-1), i18n.T("changer de module")),
			row("F5 / Maj+F5", i18n.T("déboguer : lancer, continuer / arrêter")),
			row("F6 F7 F8", i18n.T("pas à pas : principal, entrer, sortir")),
			row("F9", i18n.T("point d'arrêt (éditeur)")),
			row("F1", i18n.T("cette aide")),
			row("e", i18n.T("éditeur externe")),
			row("Ctrl+O", i18n.T("ouvrir un fichier dans l'éditeur intégré")),
			row("Ctrl+N", i18n.T("créer un fichier dans l'éditeur intégré")),
			row("q / Ctrl+Q", i18n.T("quitter (Ctrl+C hors de l'éditeur)")),
			"",
			title.Render(i18n.T("Module ") + i18n.T(m.panels[m.cur].Title())),
		}
		for _, h := range m.panels[m.cur].Hints() {
			lines = append(lines, row(h.Key, h.Desc))
		}
	case modalAbout:
		// Carte de profil : l'avatar rond à gauche, le texte à droite.
		info := []string{
			title.Render(brandText + " " + version.Version), "",
			text.Render(i18n.T("Le cockpit du développeur Go dans le terminal.")),
			text.Render("Bubble Tea v2 · Lip Gloss v2 · Glamour v2"), "",
			text.Render(i18n.T("Auteur : ") + authorName),
			bg.Foreground(t.P.Blue).Underline(true).Render(authorURL), "",
			text.Render(i18n.T("Projet : ") + m.proj.Module),
			text.Render("Go " + m.proj.GoVersion),
		}
		avatar := m.viewAvatar(t.P.Mantle)
		if avatar == "" {
			lines = info
			break
		}
		av := strings.Split(avatar, "\n")
		blank, gap := bg.Render(strings.Repeat(" ", avatarCols)), bg.Render("    ")
		off := max(0, (len(av)-len(info))/2) // texte centré en hauteur
		for i := range max(len(av), len(info)+off) {
			left, right := blank, ""
			if i < len(av) {
				left = av[i]
			}
			if j := i - off; j >= 0 && j < len(info) {
				right = info[j]
			}
			lines = append(lines, left+gap+right)
		}
	}
	lines = append(lines, "", bg.Foreground(t.P.Overlay0).Render(i18n.T("Échap pour fermer")))
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
		m.palW, m.palH = lipgloss.Width(box), lipgloss.Height(box)
		m.palX, m.palY = max(0, (m.w-m.palW)/2), max(0, min(4, m.h-m.palH))
		layers = append(layers, lipgloss.NewLayer(box).X(m.palX).Y(m.palY).Z(3))
	}
	if len(layers) == 1 {
		return base
	}
	return lipgloss.NewCompositor(layers...).Render()
}
