package modules

import (
	"strconv"

	"github.com/laurentpoirierfr/gopit/internal/panel"
)

// Commands expose les actions du module à la palette (Ctrl+P).
func (m *Model) Commands() []panel.Command {
	return []panel.Command{
		{Title: "Vérifier les mises à jour (réseau)", Key: "u"},
		{Title: "Rechercher les vulnérabilités (govulncheck)", Key: "v"},
		{Title: "Mettre à jour la dépendance sélectionnée", Key: "g"},
		{Title: "Afficher les dépendances indirectes", Key: "i"},
		{Title: "Changer de filtre", Key: "f"},
		{Title: "Ouvrir la page pkg.go.dev", Key: "w"},
		{Title: "Recharger la liste", Key: "a"},
	}
}

// State renvoie les réglages à garder d'une session à l'autre.
func (m *Model) State() map[string]string {
	return map[string]string{"indirect": strconv.FormatBool(m.showIndirect), "filter": strconv.Itoa(m.filter)}
}

// Restore applique les réglages enregistrés.
func (m *Model) Restore(s map[string]string) {
	m.showIndirect = s["indirect"] == "true"
	if n, err := strconv.Atoi(s["filter"]); err == nil && n >= 0 && n < len(filters) {
		m.filter = n
	}
}
