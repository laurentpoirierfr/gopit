package git

import "github.com/laurentpoirierfr/gopit/internal/panel"

// Commands expose les actions du module à la palette (Ctrl+P).
func (m *Model) Commands() []panel.Command {
	return []panel.Command{
		{Title: "Indexer le fichier sélectionné", Key: "s"},
		{Title: "Désindexer le fichier sélectionné", Key: "u"},
		{Title: "Tout indexer", Key: "A"},
		{Title: "Commiter les fichiers indexés", Key: "c"},
		{Title: "Ouvrir le fichier dans l'éditeur", Key: "o"},
		{Title: "Actualiser l'état du dépôt", Key: "r"},
	}
}
