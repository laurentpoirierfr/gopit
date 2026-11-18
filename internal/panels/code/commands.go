package code

import "github.com/laurentpoirierfr/gopit/internal/panel"

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
		c("Ouvrir un fichier du projet", "Ctrl+O", panel.ActOpen),
		c("Enregistrer (avec gofmt)", "Ctrl+S", panel.ActSave),
		c("Fermer le fichier", "Ctrl+W", panel.ActClose),
		c("Afficher / masquer l'arbre des fichiers", "Ctrl+B", actTree),
		c("Rechercher", "Ctrl+F", panel.ActFind),
		c("Occurrence suivante", "F3", panel.ActFindNext),
		c("Aller à la ligne", "Ctrl+G", panel.ActGoto),
		c("Commenter / décommenter", "Ctrl+/", panel.ActComment),
		c("Formater (gofmt)", "Alt+Maj+F", panel.ActFormat),
	}
}
