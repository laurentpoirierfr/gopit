package diagnostics

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/theme"
)

// finish termine l'analyse en cours (sans lancer les outils) et renvoie la
// commande de la dernière réponse.
func finish(m *Model) tea.Cmd {
	var cmd tea.Cmd
	for name, st := range m.state {
		if st.running {
			_, cmd = m.Update(toolDoneMsg{gen: m.gen, tool: name})
		}
	}
	return cmd
}

func TestRerunAfterSave(t *testing.T) {
	m := New(theme.New(), &project.Project{Root: t.TempDir()})
	m.runAll() // analyse de départ (outils non exécutés)
	finish(m)
	gen := m.gen

	// Autre fichier qu'un fichier Go : rien.
	if _, cmd := m.Update(panel.SavedMsg{Path: "/p/notes.txt"}); cmd != nil {
		t.Error("notes.txt : pas de relance")
	}

	// Deux enregistrements rapprochés : une seule analyse, après le second.
	m.Update(panel.SavedMsg{Path: "/p/a.go"})
	if _, cmd := m.Update(panel.SavedMsg{Path: "/p/go.mod"}); cmd == nil {
		t.Fatal("relance programmée attendue")
	}
	m.Update(autoMsg{gen: m.autoGen - 1}) // délai du premier : dépassé
	if m.gen != gen {
		t.Fatal("le premier enregistrement ne relance pas à lui seul")
	}
	m.Update(autoMsg{gen: m.autoGen})
	if m.gen != gen+1 || !m.quiet {
		t.Fatalf("relance automatique attendue : gen %d → %d, silencieuse %v", gen, m.gen, m.quiet)
	}

	// Fin d'une analyse automatique : pas de bilan affiché.
	if m.running() {
		if cmd := finish(m); cmd != nil {
			t.Errorf("bilan affiché après une relance automatique : %#v", cmd())
		}
	}
	if m.quiet {
		t.Error("mode silencieux à réinitialiser")
	}

	// Relance demandée (a) : bilan affiché.
	m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if m.running() {
		cmd := finish(m)
		if cmd == nil {
			t.Fatal("bilan attendu")
		}
		if _, ok := cmd().(panel.FlashMsg); !ok {
			t.Errorf("bilan : %#v", cmd())
		}
	}
}
