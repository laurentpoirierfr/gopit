package app

// Palette de commandes (Ctrl+P) : toutes les actions de tous les modules,
// filtrées au fil de la saisie.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/config"
	"github.com/laurentpoirierfr/gopit/internal/editor"
	"github.com/laurentpoirierfr/gopit/internal/panel"
)

type paletteItem struct {
	module string // module concerné (vide : commande générale)
	title  string
	key    string // touche équivalente, affichée pour mémoire
	run    func(m *Model) tea.Cmd
}

type palette struct {
	open    bool
	query   string
	items   []paletteItem
	matches []int
	cursor  int
	offset  int
}

const paletteRows = 12

// openPalette rassemble les commandes du moment et ouvre la palette.
func (m *Model) openPalette() {
	var items []paletteItem
	for i, p := range m.panels {
		items = append(items, paletteItem{title: "Aller à : " + p.Title(), key: fmt.Sprint(i + 1),
			run: func(m *Model) tea.Cmd { m.cur, m.focus = i, focusContent; return nil }})
	}
	for i, p := range m.panels {
		c, ok := p.(panel.Commander)
		if !ok {
			continue
		}
		for _, cmd := range c.Commands() {
			items = append(items, paletteItem{module: p.Title(), title: cmd.Title, key: cmd.Key,
				run: func(m *Model) tea.Cmd {
					m.cur, m.focus = i, focusContent
					return m.toPanel(commandMsg(cmd))
				}})
		}
	}
	items = append(items,
		paletteItem{title: "Ouvrir l'éditeur sur le projet", key: "e",
			run: func(m *Model) tea.Cmd { return editor.Open(m.proj.Root, "", 0) }},
		paletteItem{title: "Ouvrir le fichier de préférences",
			run: func(m *Model) tea.Cmd {
				m.saveConfig() // pour qu'il existe et reflète l'état actuel
				p, err := config.Path()
				if err != nil {
					return m.setFlash("Préférences : "+err.Error(), true)
				}
				return editor.Open(m.proj.Root, p, 0)
			}},
		paletteItem{title: "Quitter gopit", key: "q", run: func(m *Model) tea.Cmd { return m.quit() }},
	)
	m.menu.open = -1
	m.pal = palette{open: true, items: items}
	m.filterPalette()
}

// commandMsg renvoie le message qui exécute une commande de module.
func commandMsg(c panel.Command) tea.Msg {
	if c.Msg != nil {
		return c.Msg
	}
	return keyMsg(c.Key)
}

// keyMsg construit le message clavier correspondant à une touche nommée.
func keyMsg(key string) tea.KeyPressMsg {
	switch key {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "f5":
		return tea.KeyPressMsg{Code: tea.KeyF5}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	}
	r := []rune(key)
	return tea.KeyPressMsg{Code: r[0], Text: key}
}

// filterPalette garde les commandes qui contiennent tous les mots saisis.
func (m *Model) filterPalette() {
	p := &m.pal
	words := strings.Fields(strings.ToLower(p.query))
	p.matches = p.matches[:0]
	for i, it := range p.items {
		text := strings.ToLower(it.module + " " + it.title)
		ok := true
		for _, w := range words {
			if !strings.Contains(text, w) {
				ok = false
				break
			}
		}
		if ok {
			p.matches = append(p.matches, i)
		}
	}
	p.cursor, p.offset = 0, 0
}

func (m *Model) paletteKey(k tea.KeyPressMsg) tea.Cmd {
	p := &m.pal
	switch s := k.String(); s {
	case "esc", "ctrl+p":
		p.open = false
	case "enter":
		p.open = false
		if p.cursor < len(p.matches) {
			return p.items[p.matches[p.cursor]].run(m)
		}
	case "up", "ctrl+k":
		p.cursor = max(0, p.cursor-1)
	case "down", "ctrl+j":
		p.cursor = min(len(p.matches)-1, p.cursor+1)
	case "backspace":
		if r := []rune(p.query); len(r) > 0 {
			p.query = string(r[:len(r)-1])
			m.filterPalette()
		}
	default:
		if k.Text != "" {
			p.query += k.Text
			m.filterPalette()
		}
	}
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+paletteRows {
		p.offset = p.cursor - paletteRows + 1
	}
	return nil
}

func (m *Model) viewPalette() string {
	t := m.th
	p := &m.pal
	w := min(76, m.w-6)
	inner := w - 4
	bg := lipgloss.NewStyle().Background(t.P.Mantle)
	line := func(s string) string { return fill(bg, ansi.Truncate(s, inner, "…"), inner) }

	lines := []string{
		line(bg.Foreground(t.P.Accent).Bold(true).Render("› ") + bg.Foreground(t.P.Text).Render(p.query) +
			bg.Foreground(t.P.Accent).Render("▌")),
		line(bg.Foreground(t.P.Surface1).Render(strings.Repeat("─", inner))),
	}
	for i := p.offset; i < len(p.matches) && i < p.offset+paletteRows; i++ {
		it := p.items[p.matches[i]]
		key := ""
		if it.key != "" {
			key = "[" + it.key + "]"
		}
		module := ""
		if it.module != "" {
			module = it.module + " · "
		}
		left := " " + module + it.title
		gap := max(1, inner-lipgloss.Width(left)-lipgloss.Width(key)-1)
		text := left + strings.Repeat(" ", gap) + key + " "
		if i == p.cursor {
			lines = append(lines, line(lipgloss.NewStyle().Background(t.P.Accent).Foreground(t.P.Base).Bold(true).Render(text)))
			continue
		}
		lines = append(lines, line(bg.Foreground(t.P.Subtext).Render(" "+module)+bg.Foreground(t.P.Text).Render(it.title)+
			bg.Render(strings.Repeat(" ", gap))+bg.Foreground(t.P.Overlay0).Render(key+" ")))
	}
	if len(p.matches) == 0 {
		lines = append(lines, line(bg.Foreground(t.P.Overlay0).Render(" Aucune commande")))
	}
	lines = append(lines, line(""), line(bg.Foreground(t.P.Overlay0).Render(
		fmt.Sprintf(" %d commande(s) · ↑↓ choisir · Entrée exécuter · Échap fermer", len(p.matches)))))

	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.P.Accent).
		BorderBackground(t.P.Mantle).Background(t.P.Mantle).Padding(0, 1).Render(strings.Join(lines, "\n"))
}
