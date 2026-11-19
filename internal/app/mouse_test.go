package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/panel"
)

// find renvoie la position à l'écran du texte s (sans les couleurs).
func find(t *testing.T, m *Model, s string) (x, y int) {
	t.Helper()
	for y, l := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		if i := strings.Index(l, s); i >= 0 {
			return ansi.StringWidth(l[:i]), y
		}
	}
	t.Fatalf("« %s » absent de l'écran", s)
	return 0, 0
}

// click clique en (x, y) et exécute les commandes renvoyées (un niveau).
func click(m *Model, x, y int) {
	_, cmd := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	if cmd != nil {
		if msg := cmd(); msg != nil {
			m.Update(msg)
		}
	}
}

func TestMouseZones(t *testing.T) {
	m := newApp(t, nil)
	if strings.Contains(m.View().Content, "\x1b_") {
		t.Fatal("marqueurs de zones restés à l'écran")
	}
	// Accueil : une ligne marquée « → » mène au module.
	x, y := find(t, m, "Paquets")
	click(m, x, y)
	if got := m.panels[m.cur].Title(); got != "Docs" {
		t.Fatalf("clic sur « Paquets » : module %s", got)
	}

	// Palette : un clic exécute la commande, un clic dehors la ferme.
	press(m, "ctrl+p")
	typeText(m, "aller git")
	m.View()
	x, y = find(t, m, "Aller à : Git")
	click(m, x+1, y)
	if m.pal.open || m.panels[m.cur].Title() != "Git" {
		t.Fatalf("clic dans la palette : ouverte %v, module %s", m.pal.open, m.panels[m.cur].Title())
	}
	press(m, "ctrl+p")
	m.View()
	click(m, 0, m.h-1)
	if m.pal.open {
		t.Error("un clic hors de la palette la ferme")
	}
}

// recorder est un module qui garde les messages de souris reçus.
type recorder struct {
	panel.Panel
	clicks []panel.ClickMsg
	wheels []panel.WheelMsg
}

func (r *recorder) Update(msg tea.Msg) (panel.Panel, tea.Cmd) {
	switch msg := msg.(type) {
	case panel.ClickMsg:
		r.clicks = append(r.clicks, msg)
	case panel.WheelMsg:
		r.wheels = append(r.wheels, msg)
	}
	return r, nil
}

func (r *recorder) View(w, h int) string {
	return "titre\n" + panel.Zone("list", panel.Row("row:0", 20, "première")+"\n"+panel.Row("row:1", 20, "seconde"))
}

func TestMouseDoubleClickAndWheel(t *testing.T) {
	m := newApp(t, nil)
	r := &recorder{Panel: m.panels[m.cur]}
	m.panels[m.cur] = r
	m.View()
	x, y := find(t, m, "seconde")
	click(m, x, y)
	click(m, x+3, y)
	click(m, x+3, y) // troisième clic : simple de nouveau
	x0, y0 := find(t, m, "première")
	click(m, x0, y0)
	if len(r.clicks) != 4 {
		t.Fatalf("%d clics", len(r.clicks))
	}
	if i, ok := panel.Index(r.clicks[0].Zones, "row:"); !ok || i != 1 || !panel.Has(r.clicks[0].Zones, "list") {
		t.Errorf("zones du clic : %v", r.clicks[0].Zones)
	}
	if r.clicks[0].Double || !r.clicks[1].Double || r.clicks[2].Double || r.clicks[3].Double {
		t.Errorf("double clic : %v %v %v %v", r.clicks[0].Double, r.clicks[1].Double, r.clicks[2].Double, r.clicks[3].Double)
	}
	m.Update(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown})
	m.Update(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelUp})
	if len(r.wheels) != 2 || r.wheels[0].Delta <= 0 || r.wheels[1].Delta >= 0 || !panel.Has(r.wheels[0].Zones, "row:1") {
		t.Errorf("molette : %+v", r.wheels)
	}
}
