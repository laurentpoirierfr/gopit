// Package app assemble le cockpit : barre du haut, navigation entre modules
// à gauche, module actif à droite, barre d'état et raccourcis en bas.
package app

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/editor"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/panels/home"
	"github.com/laurentpoirierfr/gopit/internal/panels/soon"
	"github.com/laurentpoirierfr/gopit/internal/panels/tests"
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/theme"
	"github.com/laurentpoirierfr/gopit/internal/version"
)

type focus int

const (
	focusNav focus = iota
	focusContent
)

const (
	topH    = 1  // barre du haut
	footerH = 2  // barre d'état + raccourcis
	navW    = 24 // largeur de la navigation (bordures comprises)
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
}

// New crée le cockpit pour le projet p.
func New(p *project.Project) *Model {
	th := theme.New()
	return &Model{
		th:   th,
		proj: p,
		panels: []panel.Panel{
			home.New(th, p),
			tests.New(th, p),
			soon.New(th, "Diagnostics", "",
				"Rassemble les problèmes signalés par les outils d'analyse.",
				"go vet, staticcheck et diagnostics gopls",
				"filtre par gravité et par paquet",
				"Entrée : ouvrir l'éditeur sur l'erreur"),
			soon.New(th, "Modules", "",
				"Explore les dépendances du module.",
				"dépendances directes et indirectes (go list -m)",
				"mises à jour disponibles",
				"vulnérabilités connues (govulncheck)"),
			soon.New(th, "Docs", "",
				"Lit la documentation Go sans quitter le terminal.",
				"go doc d'un paquet ou d'un symbole",
				"rendu Markdown avec Glamour",
				"recherche dans les paquets du module"),
		},
	}
}

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

	case editor.DoneMsg:
		if msg.Err != nil {
			return m, m.setFlash("Éditeur : "+msg.Err.Error(), true)
		}
		return m, nil

	case tea.KeyPressMsg:
		return m, m.handleKey(msg)

	case tea.MouseClickMsg:
		mo := tea.Mouse(msg)
		if mo.Button == tea.MouseLeft {
			m.handleClick(mo.X, mo.Y)
		}
		return m, nil
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
	switch s {
	case "ctrl+c", "q":
		return tea.Quit
	case "tab", "shift+tab":
		if m.focus == focusNav {
			m.focus = focusContent
		} else {
			m.focus = focusNav
		}
		return nil
	case "e":
		return editor.Open(m.proj.Root, "", 0)
	}
	if len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
		if i := int(s[0] - '1'); i < len(m.panels) {
			m.cur = i
		}
		return nil
	}

	if m.focus == focusNav {
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

	if s == "esc" {
		m.focus = focusNav
		return nil
	}
	return m.toPanel(k)
}

func (m *Model) toPanel(msg tea.Msg) tea.Cmd {
	np, cmd := m.panels[m.cur].Update(msg)
	m.panels[m.cur] = np
	return cmd
}

func (m *Model) handleClick(x, y int) {
	if y < topH || y >= m.h-footerH {
		return
	}
	if x < navW {
		if i := y - topH - 1; i >= 0 && i < len(m.panels) {
			m.cur = i
		}
		m.focus = focusNav
		return
	}
	m.focus = focusContent
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
		v = tea.NewView("Chargement…")
	} else {
		v = tea.NewView(m.render())
	}
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "gopit — " + m.proj.Module
	return v
}

func (m *Model) render() string {
	bodyH := max(5, m.h-topH-footerH)
	body := lipgloss.JoinHorizontal(lipgloss.Top, m.viewNav(bodyH), m.viewContent(bodyH))
	return lipgloss.JoinVertical(lipgloss.Left, m.viewTop(), body, m.viewStatus(), m.viewHints())
}

// fill complète une ligne avec des espaces stylés jusqu'à la largeur w.
func fill(st lipgloss.Style, s string, w int) string {
	if gap := w - lipgloss.Width(s); gap > 0 {
		return s + st.Render(strings.Repeat(" ", gap))
	}
	return ansi.Truncate(s, w, "…")
}

func (m *Model) viewTop() string {
	t := m.th
	left := t.Brand.Render(" gopit") + t.TopBar.Render(" ") + t.TopBar.Bold(true).Render(m.proj.Module)
	right := t.TopInfo.Render(fmt.Sprintf("go %s · gopit %s ", m.proj.GoVersion, version.Version))
	return fill(t.TopBar, left, m.w-lipgloss.Width(right)) + right
}

func (m *Model) viewNav(h int) string {
	t := m.th
	iw := navW - 2
	lines := make([]string, len(m.panels))
	for i, p := range m.panels {
		label := ansi.Truncate(fmt.Sprintf("%d %s  %s", i+1, p.Icon(), p.Title()), iw-2, "…")
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
	w := max(10, m.w-navW)
	iw, ih := w-4, h-2 // bordures + marge intérieure
	p := m.panels[m.cur]

	header := t.Title.Render(p.Icon() + "  " + p.Title())
	content := header + "\n\n" + p.View(iw, max(1, ih-2))

	box := t.Panel
	if m.focus == focusContent {
		box = t.PanelFocus
	}
	return box.Width(w).Height(h).MaxHeight(h).Padding(0, 1).Render(content)
}

func (m *Model) viewStatus() string {
	t := m.th
	mode := "NAVIGATION"
	if m.focus == focusContent {
		mode = strings.ToUpper(m.panels[m.cur].Title())
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
	right := t.StatusInfo.Render(fmt.Sprintf("%d dép. directes", m.proj.Requires))
	if lipgloss.Width(left)+lipgloss.Width(right) > m.w {
		left = ansi.Truncate(left, max(0, m.w-lipgloss.Width(right)), "…")
	}
	return fill(t.StatusBar, left, m.w-lipgloss.Width(right)) + right
}

func (m *Model) viewHints() string {
	t := m.th
	hints := []panel.Hint{{Key: "↑↓/1-5", Desc: "module"}, {Key: "Tab", Desc: "focus"}}
	hints = append(hints, m.panels[m.cur].Hints()...)
	hints = append(hints, panel.Hint{Key: "e", Desc: "éditeur"}, panel.Hint{Key: "q", Desc: "quitter"})

	parts := make([]string, len(hints))
	for i, h := range hints {
		parts[i] = t.HintKey.Render(h.Key) + t.Hint.Render(" "+h.Desc)
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
