// Package theme définit les couleurs et les styles partagés par tous les
// panneaux (palette Catppuccin, claire ou sombre selon le terminal).
package theme

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Palette Catppuccin : Latte (fond clair) / Mocha (fond sombre).
type Palette struct {
	Base, Mantle, Surface0, Surface1, Overlay0 color.Color
	Text, Subtext, Accent, Blue, Green, Red    color.Color
	Yellow, Peach                              color.Color
}

func newPalette(isDark bool) Palette {
	ld := lipgloss.LightDark(isDark)
	c := func(light, dark string) color.Color { return ld(lipgloss.Color(light), lipgloss.Color(dark)) }
	return Palette{
		Base:     c("#eff1f5", "#1e1e2e"),
		Mantle:   c("#e6e9ef", "#181825"),
		Surface0: c("#ccd0da", "#313244"),
		Surface1: c("#bcc0cc", "#45475a"),
		Overlay0: c("#9ca0b0", "#6c7086"),
		Text:     c("#4c4f69", "#cdd6f4"),
		Subtext:  c("#6c6f85", "#a6adc8"),
		Accent:   c("#8839ef", "#cba6f7"),
		Blue:     c("#1e66f5", "#89b4fa"),
		Green:    c("#40a02b", "#a6e3a1"),
		Red:      c("#d20f39", "#f38ba8"),
		Yellow:   c("#df8e1d", "#f9e2af"),
		Peach:    c("#fe640b", "#fab387"),
	}
}

// Theme regroupe la palette et les styles. Les panneaux gardent un pointeur
// vers le même Theme : Set le met à jour pour tout le monde.
type Theme struct {
	P    Palette
	Dark bool // fond sombre (choix du style Markdown, etc.)

	// Cadre de l'application
	Brand, TopBar, TopInfo           lipgloss.Style
	Panel, PanelFocus                lipgloss.Style
	NavItem, NavItemSel, NavItemBlur lipgloss.Style
	StatusBar, Badge, StatusInfo     lipgloss.Style
	FlashOK, FlashErr                lipgloss.Style
	HintKey, Hint                    lipgloss.Style

	// Contenu des panneaux
	Title, Label, Value, Dim, Text lipgloss.Style
	OK, Warn, Err                  lipgloss.Style
}

// New crée un thème (sombre par défaut, en attendant la couleur du terminal).
func New() *Theme {
	t := &Theme{}
	t.Set(true)
	return t
}

// Set recalcule les styles pour un fond sombre ou clair.
func (t *Theme) Set(isDark bool) {
	p := newPalette(isDark)
	ns := lipgloss.NewStyle
	*t = Theme{P: p, Dark: isDark}

	t.Brand = ns().Bold(true).Foreground(p.Base).Background(p.Accent).Padding(0, 1)
	t.TopBar = ns().Background(p.Mantle).Foreground(p.Text)
	t.TopInfo = ns().Background(p.Mantle).Foreground(p.Subtext)

	t.Panel = ns().Border(lipgloss.RoundedBorder()).BorderForeground(p.Surface1)
	t.PanelFocus = ns().Border(lipgloss.RoundedBorder()).BorderForeground(p.Accent)
	t.NavItem = ns().Foreground(p.Text).Padding(0, 1)
	t.NavItemSel = ns().Foreground(p.Base).Background(p.Accent).Bold(true).Padding(0, 1)
	t.NavItemBlur = ns().Foreground(p.Text).Background(p.Surface0).Padding(0, 1)

	t.StatusBar = ns().Background(p.Mantle).Foreground(p.Text)
	t.Badge = ns().Bold(true).Foreground(p.Base).Background(p.Blue).Padding(0, 1)
	t.StatusInfo = ns().Background(p.Surface0).Foreground(p.Text).Padding(0, 1)
	t.FlashOK = ns().Background(p.Green).Foreground(p.Base).Padding(0, 1)
	t.FlashErr = ns().Background(p.Red).Foreground(p.Base).Bold(true).Padding(0, 1)
	t.HintKey = ns().Foreground(p.Subtext).Bold(true)
	t.Hint = ns().Foreground(p.Overlay0)

	t.Title = ns().Bold(true).Foreground(p.Accent)
	t.Label = ns().Foreground(p.Subtext)
	t.Value = ns().Foreground(p.Text).Bold(true)
	t.Dim = ns().Foreground(p.Overlay0)
	t.Text = ns().Foreground(p.Text)
	t.OK = ns().Foreground(p.Green)
	t.Warn = ns().Foreground(p.Yellow)
	t.Err = ns().Foreground(p.Red)
}
