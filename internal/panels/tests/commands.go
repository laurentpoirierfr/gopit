package tests

import (
	"strconv"

	"github.com/laurentpoirierfr/gopit/internal/panel"
)

// Commands expose les actions du module à la palette (Ctrl+P).
func (m *Model) Commands() []panel.Command {
	return []panel.Command{
		{Title: "Lancer tous les tests", Key: "a"},
		{Title: "Relancer les tests en échec", Key: "f"},
		{Title: "Lancer la sélection", Key: "enter"},
		{Title: "Arrêter l'exécution", Key: "x"},
		{Title: "Surveillance des fichiers (relance automatique)", Key: "w"},
		{Title: "Couverture de code", Key: "v"},
		{Title: "Cache de go test", Key: "c"},
		{Title: "Afficher les paquets sans tests", Key: "h"},
		{Title: "Ouvrir l'erreur ou le code non couvert", Key: "o"},
	}
}

// State renvoie les réglages à garder d'une session à l'autre.
func (m *Model) State() map[string]string {
	return map[string]string{
		"cover":    strconv.FormatBool(m.cover),
		"no_cache": strconv.FormatBool(m.noCache),
		"show_all": strconv.FormatBool(m.showAll),
	}
}

// Restore applique les réglages enregistrés.
func (m *Model) Restore(s map[string]string) {
	m.cover = s["cover"] == "true"
	m.noCache = s["no_cache"] == "true"
	m.showAll = s["show_all"] == "true"
}
