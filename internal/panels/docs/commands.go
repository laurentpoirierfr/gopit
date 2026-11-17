package docs

import "github.com/laurentpoirierfr/gopit/internal/panel"

// Commands expose les actions du module à la palette (Ctrl+P).
func (m *Model) Commands() []panel.Command {
	return []panel.Command{
		{Title: "Rechercher un paquet ou un symbole", Key: "/"},
		{Title: "Ouvrir la page pkg.go.dev", Key: "w"},
		{Title: "Ouvrir la source dans l'éditeur", Key: "o"},
	}
}
