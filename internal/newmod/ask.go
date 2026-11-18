package newmod

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
)

// asker est le petit écran qui demande le chemin du module.
type asker struct {
	dir, def string
	input    []rune
	err      string
	done     bool // validé
	quit     bool // abandonné
}

func (a *asker) Init() tea.Cmd { return nil }

func (a *asker) value() string {
	if s := strings.TrimSpace(string(a.input)); s != "" {
		return s
	}
	return a.def
}

func (a *asker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "enter":
			if err := Check(a.value()); err != nil {
				a.err = err.Error()
				return a, nil
			}
			a.done = true
			return a, tea.Quit
		case "esc", "ctrl+c", "ctrl+q":
			a.quit = true
			return a, tea.Quit
		case "tab": // reprendre la proposition pour la modifier
			if len(a.input) == 0 {
				a.input = []rune(a.def)
			}
		case "backspace":
			if len(a.input) > 0 {
				a.input = a.input[:len(a.input)-1]
			}
		case "ctrl+u":
			a.input = nil
		default:
			if msg.Text != "" && !msg.Mod.Contains(tea.ModCtrl) && !msg.Mod.Contains(tea.ModAlt) {
				a.input = append(a.input, []rune(msg.Text)...)
			}
		}
		a.err = ""
	case tea.PasteMsg:
		a.input = append(a.input, []rune(strings.TrimSpace(msg.Content))...)
	}
	return a, nil
}

func (a *asker) View() tea.View {
	if a.done || a.quit {
		return tea.NewView("")
	}
	bold := lipgloss.NewStyle().Bold(true)
	dim := lipgloss.NewStyle().Faint(true)
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	field := string(a.input) + "▌"
	if len(a.input) == 0 {
		field = "▌" + dim.Render(a.def) // proposition grisée
	}
	lines := []string{
		fmt.Sprintf(i18n.T("Aucun go.mod trouvé dans %s."), a.dir),
		bold.Render(i18n.T("Créer un nouveau module Go ici ?")),
		"",
		"  " + accent.Render(i18n.T("Chemin du module : ")) + field,
	}
	if a.err != "" {
		lines = append(lines, "  "+lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(a.err))
	}
	lines = append(lines, "", dim.Render("  "+i18n.T("Entrée créer · Tab modifier la proposition · Échap annuler")), "")
	return tea.NewView(strings.Join(lines, "\n"))
}

// Ask demande le chemin du module à créer dans dir ; ok est faux si
// l'utilisateur renonce.
func Ask(dir, def string) (path string, ok bool, err error) {
	a := &asker{dir: dir, def: def}
	if _, err := tea.NewProgram(a).Run(); err != nil {
		return "", false, err
	}
	return a.value(), a.done, nil
}
