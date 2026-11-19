package diagnostics

import (
	"path/filepath"
	"testing"

	"github.com/laurentpoirierfr/gopit/internal/diag"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/theme"
)

// at renvoie un clic sur la zone id du dernier rendu.
func at(t *testing.T, m *Model, id string) panel.ClickMsg {
	t.Helper()
	_, segs := panel.Scan(m.View(100, 20))
	for _, s := range segs {
		if s.ID == id {
			return panel.ClickMsg{X: s.X0, Y: s.Y, Zones: panel.ZonesAt(segs, s.X0, s.Y)}
		}
	}
	t.Fatalf("zone %s absente", id)
	return panel.ClickMsg{}
}

func TestMouse(t *testing.T) {
	root := t.TempDir()
	m := New(theme.New(), &project.Project{Root: root})
	a, b := filepath.Join(root, "a.go"), filepath.Join(root, "b.go")
	m.results[diag.Tools[0].Name] = []diag.Diagnostic{
		{File: a, Line: 1, Message: "un", Severity: diag.Error},
		{File: a, Line: 2, Message: "deux", Severity: diag.Error},
		{File: b, Line: 3, Message: "trois", Severity: diag.Warning},
	}
	m.merge()
	// rows : en-tête a, un, deux, en-tête b, trois
	m.Update(at(t, m, "row:2"))
	if d := m.selected(); d == nil || d.Message != "deux" {
		t.Fatalf("clic : %v", d)
	}
	m.Update(at(t, m, "row:3")) // en-tête : son premier diagnostic
	if d := m.selected(); d == nil || d.Message != "trois" {
		t.Fatalf("clic sur l'en-tête : %v", d)
	}
	c := at(t, m, "row:1")
	c.Double = true
	_, cmd := m.Update(c)
	if cmd == nil {
		t.Fatal("double clic : rien d'ouvert")
	}
	if o, ok := cmd().(panel.OpenMsg); !ok || o.File != a || o.Line != 1 {
		t.Errorf("double clic : %#v", cmd())
	}
	l := at(t, m, "list")
	m.Update(panel.WheelMsg{X: l.X, Y: l.Y, Delta: 3, Zones: l.Zones})
	if d := m.selected(); d == nil || d.Message != "trois" {
		t.Errorf("molette : %v", d)
	}
	m.Update(at(t, m, "sev"))
	if m.sevFilter != 1 {
		t.Errorf("clic sur le filtre : %d", m.sevFilter)
	}
}
