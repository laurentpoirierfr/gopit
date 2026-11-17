package diagnostics

import (
	"strconv"

	"github.com/laurentpoirierfr/gopit/internal/panel"
)

// Commands expose les actions du module à la palette (Ctrl+P).
func (m *Model) Commands() []panel.Command {
	return []panel.Command{
		{Title: "Relancer l'analyse", Key: "a"},
		{Title: "Filtrer par gravité", Key: "s"},
		{Title: "Filtrer par outil", Key: "t"},
		{Title: "Ouvrir le diagnostic dans l'éditeur", Key: "o"},
	}
}

// State renvoie les réglages à garder d'une session à l'autre.
func (m *Model) State() map[string]string {
	return map[string]string{"severity": strconv.Itoa(m.sevFilter), "tool": m.toolFilter}
}

// Restore applique les réglages enregistrés.
func (m *Model) Restore(s map[string]string) {
	if n, err := strconv.Atoi(s["severity"]); err == nil && n >= 0 && n < len(sevFilters) {
		m.sevFilter = n
	}
	m.toolFilter = s["tool"]
}
