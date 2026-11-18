package code

import (
	"github.com/laurentpoirierfr/gopit/internal/panel"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
)

// actTree affiche ou masque l'arbre (action propre à ce module).
const actTree panel.EditAction = "tree"

// Commands expose les actions du module à la palette (Ctrl+P). Elles passent
// par des messages plutôt que par des touches : en édition, une lettre
// serait insérée dans le texte.
func (m *Model) Commands() []panel.Command {
	c := func(title, key string, a panel.EditAction) panel.Command {
		return panel.Command{Title: title, Key: key, Msg: panel.EditMsg{Action: a}}
	}
	return []panel.Command{
		c(i18n.T("Ouvrir un fichier du projet"), "Ctrl+O", panel.ActOpen),
		c(i18n.T("Enregistrer (avec gofmt)"), "Ctrl+S", panel.ActSave),
		c(i18n.T("Fermer le fichier"), "Ctrl+W", panel.ActClose),
		c(i18n.T("Afficher / masquer l'arbre des fichiers"), "Ctrl+B", actTree),
		c(i18n.T("Rechercher"), "Ctrl+F", panel.ActFind),
		c(i18n.T("Occurrence suivante"), "F3", panel.ActFindNext),
		c(i18n.T("Aller à la ligne"), "Ctrl+G", panel.ActGoto),
		c(i18n.T("Commenter / décommenter"), "Ctrl+/", panel.ActComment),
		c(i18n.T("Formater (gofmt)"), "Alt+Maj+F", panel.ActFormat),
	}
}
