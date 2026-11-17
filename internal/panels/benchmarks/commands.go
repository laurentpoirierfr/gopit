package benchmarks

import (
	"strconv"

	"github.com/laurentpoirierfr/gopit/internal/panel"
)

// Commands expose les actions du module à la palette (Ctrl+P).
func (m *Model) Commands() []panel.Command {
	return []panel.Command{
		{Title: "Lancer tous les benchmarks", Key: "a"},
		{Title: "Lancer le benchmark sélectionné", Key: "enter"},
		{Title: "Profil CPU du benchmark sélectionné", Key: "p"},
		{Title: "Profil mémoire du benchmark sélectionné", Key: "m"},
		{Title: "Revoir le dernier profil", Key: "P"},
		{Title: "Changer l'exécution de référence", Key: "b"},
		{Title: "Changer le nombre de répétitions (-count)", Key: "n"},
		{Title: "Arrêter l'exécution", Key: "x"},
	}
}

// State renvoie les réglages à garder d'une session à l'autre.
func (m *Model) State() map[string]string {
	return map[string]string{"count": strconv.Itoa(m.count())}
}

// Restore applique les réglages enregistrés.
func (m *Model) Restore(s map[string]string) {
	n, err := strconv.Atoi(s["count"])
	if err != nil {
		return
	}
	for i, c := range counts {
		if c == n {
			m.countIdx = i
		}
	}
}
