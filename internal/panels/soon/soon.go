// Package soon fournit un panneau d'attente pour les modules pas encore
// développés : il décrit ce que le module apportera.
package soon

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/theme"
)

type Model struct {
	th      *theme.Theme
	title   string
	icon    string
	desc    string
	planned []string
}

// New crée un panneau « à venir ».
func New(th *theme.Theme, title, icon, desc string, planned ...string) *Model {
	return &Model{th: th, title: title, icon: icon, desc: desc, planned: planned}
}

func (m *Model) Title() string                         { return m.title }
func (m *Model) Icon() string                          { return m.icon }
func (m *Model) Init() tea.Cmd                         { return nil }
func (m *Model) Update(tea.Msg) (panel.Panel, tea.Cmd) { return m, nil }
func (m *Model) Hints() []panel.Hint                   { return nil }

func (m *Model) View(width, height int) string {
	t := m.th
	lines := []string{
		t.Text.Render(m.desc),
		"",
		t.Label.Render("Prévu :"),
	}
	for _, p := range m.planned {
		lines = append(lines, t.Dim.Render("  • ")+t.Text.Render(p))
	}
	lines = append(lines, "", t.Warn.Render("Module en cours de construction."))
	return lipgloss.NewStyle().Width(width).MaxHeight(height).Render(strings.Join(lines, "\n"))
}
