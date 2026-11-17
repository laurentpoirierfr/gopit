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
		{Title: "Changer la durée de chaque mesure (-benchtime)", Key: "t"},
		{Title: "Arrêter l'exécution", Key: "x"},
	}
}

// State renvoie les réglages à garder d'une session à l'autre.
func (m *Model) State() map[string]string {
	return map[string]string{"count": strconv.Itoa(m.count()), "benchtime": m.benchTime()}
}

// Restore applique les réglages enregistrés.
func (m *Model) Restore(s map[string]string) {
	if n, err := strconv.Atoi(s["count"]); err == nil {
		for i, c := range counts {
			if c == n {
				m.countIdx = i
			}
		}
	}
	for i, bt := range benchTimes {
		if bt == s["benchtime"] {
			m.timeIdx = i
		}
	}
}
