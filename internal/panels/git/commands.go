package git

import (
	"github.com/laurentpoirierfr/gopit/internal/panel"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
)

// Commands expose les actions du module à la palette (Ctrl+P).
func (m *Model) Commands() []panel.Command {
	return []panel.Command{
		{Title: i18n.T("Indexer le fichier sélectionné"), Key: "s"},
		{Title: i18n.T("Désindexer le fichier sélectionné"), Key: "u"},
		{Title: i18n.T("Tout indexer"), Key: "A"},
		{Title: i18n.T("Commiter les fichiers indexés"), Key: "c"},
		{Title: i18n.T("Ouvrir le fichier dans l'éditeur"), Key: "o"},
		{Title: i18n.T("Actualiser l'état du dépôt"), Key: "r"},
	}
}
