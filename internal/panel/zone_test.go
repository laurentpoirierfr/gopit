package panel

import (
	"slices"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestScanZones(t *testing.T) {
	st := lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Border(lipgloss.RoundedBorder())
	list := Zone("list", Row("row:0", 8, "un")+"\n"+Row("row:1", 8, "deux"))
	screen := lipgloss.JoinHorizontal(lipgloss.Top, st.Render(list), " ", Zone("detail", "détail"))
	clean, segs := Scan(screen)
	if clean != lipgloss.JoinHorizontal(lipgloss.Top, st.Render("un      \ndeux    "), " ", "détail") {
		t.Fatalf("marqueurs retirés :\n%s", clean)
	}
	for _, c := range []struct {
		x, y int
		want []string
	}{
		{1, 1, []string{"row:0", "list"}},
		{8, 2, []string{"row:1", "list"}}, // blanc après le texte
		{0, 1, nil},                       // bordure
		{11, 0, []string{"detail"}},
		{17, 0, nil},
	} {
		if got := ZonesAt(segs, c.x, c.y); !slices.Equal(got, c.want) {
			t.Errorf("(%d, %d) : %v, attendu %v", c.x, c.y, got, c.want)
		}
	}
	if i, ok := Index(ZonesAt(segs, 2, 2), "row:"); !ok || i != 1 {
		t.Errorf("Index : %d %v", i, ok)
	}
	if !Has(ZonesAt(segs, 2, 2), "list") || Has(ZonesAt(segs, 2, 2), "detail") {
		t.Error("Has")
	}
}

func TestScanTruncated(t *testing.T) {
	// Fin de zone perdue : la zone va jusqu'au bout de la ligne.
	clean, segs := Scan("ab" + zoneOpen + "z" + zoneST + "cdé")
	if clean != "abcdé" || len(segs) != 1 || segs[0] != (Seg{"z", 0, 2, 5}) {
		t.Errorf("%q %v", clean, segs)
	}
	if s, segs := Scan("sans zone"); s != "sans zone" || segs != nil {
		t.Errorf("%q %v", s, segs)
	}
}
