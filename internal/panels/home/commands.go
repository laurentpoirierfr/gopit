package home

import "github.com/laurentpoirierfr/gopit/internal/panel"

// Commands expose les actions du module à la palette (Ctrl+P).
func (m *Model) Commands() []panel.Command {
	return []panel.Command{{Title: "Actualiser le résumé du projet", Key: "r"}}
}
