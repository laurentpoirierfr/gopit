package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/laurentpoirierfr/gopit/internal/panel"
)

func TestApplyFix(t *testing.T) {
	m := newApp(t, nil)
	path := filepath.Join(m.proj.Root, "a.go")
	base := "package exemple\n\nvar X = 1\n"
	fixed := "package exemple\n\nvar X = 2\n"
	m.Update(panel.ApplyMsg{Path: path, Base: base, Text: fixed, Line: 3, Title: "essai"})
	if m.panels[m.cur].Title() != "Éditeur" {
		t.Fatalf("module affiché : %s", m.panels[m.cur].Title())
	}
	if data, _ := os.ReadFile(path); string(data) != base {
		t.Error("l'éditeur intégré n'enregistre pas")
	}

	// Éditeur externe : le fichier est corrigé directement.
	m.external = true
	_, cmd := m.Update(panel.ApplyMsg{Path: path, Base: base, Text: fixed, Line: 3, Title: "essai"})
	if data, _ := os.ReadFile(path); string(data) != fixed || cmd == nil {
		t.Errorf("fichier : %q", data)
	}
	m.Update(panel.ApplyMsg{Path: path, Base: base, Text: "autre", Line: 3})
	if data, _ := os.ReadFile(path); string(data) != fixed {
		t.Error("fichier changé depuis l'analyse : rien ne doit être écrit")
	}
}
