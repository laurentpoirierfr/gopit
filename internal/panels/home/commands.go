package home

import (
	"github.com/laurentpoirierfr/gopit/internal/panel"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
)

// Commands expose les actions du module à la palette (Ctrl+P).
func (m *Model) Commands() []panel.Command {
	return []panel.Command{{Title: i18n.T("Actualiser le résumé du projet"), Key: "r"}}
}
