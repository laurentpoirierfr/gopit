package docs

import (
	"github.com/laurentpoirierfr/gopit/internal/panel"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
)

// Commands expose les actions du module à la palette (Ctrl+P).
func (m *Model) Commands() []panel.Command {
	return []panel.Command{
		{Title: i18n.T("Rechercher un paquet ou un symbole"), Key: "/"},
		{Title: i18n.T("Ouvrir la page pkg.go.dev"), Key: "w"},
		{Title: i18n.T("Ouvrir la source dans l'éditeur"), Key: "o"},
	}
}
