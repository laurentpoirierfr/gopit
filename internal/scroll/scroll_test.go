package scroll

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestThumb(t *testing.T) {
	cases := []struct {
		h, total, offset int
		pos, size        int
		ok               bool
	}{
		{10, 5, 0, 0, 0, false},  // tout tient : pas d'ascenseur
		{10, 10, 0, 0, 0, false}, // exactement plein
		{10, 100, 0, 0, 1, true}, // début
		{10, 100, 90, 9, 1, true},
		{10, 100, 45, 5, 1, true}, // milieu
		{10, 20, 0, 0, 5, true},   // moitié visible : curseur de 5
		{10, 20, 10, 5, 5, true},  // fin
		{10, 20, 99, 5, 5, true},  // décalage trop grand : borné
	}
	for _, c := range cases {
		pos, size, ok := Thumb(c.h, c.total, c.offset)
		if pos != c.pos || size != c.size || ok != c.ok {
			t.Errorf("Thumb(%d, %d, %d) = %d, %d, %v ; attendu %d, %d, %v", c.h, c.total, c.offset, pos, size, ok, c.pos, c.size, c.ok)
		}
	}
	if Clamp(-3, 10, 20) != 0 || Clamp(50, 10, 20) != 10 || Clamp(4, 10, 5) != 0 {
		t.Error("Clamp")
	}
}

func TestRender(t *testing.T) {
	st := lipgloss.NewStyle()
	out := Render([]string{"un", "deux"}, 6, 4, 8, 4, st, st)
	lines := strings.Split(ansi.Strip(out), "\n")
	want := []string{"un     │", "deux   │", "       ┃", "       ┃"}
	if len(lines) != 4 {
		t.Fatalf("%d lignes", len(lines))
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("ligne %d : %q, attendu %q", i, lines[i], want[i])
		}
	}
	// Sans débordement : pas de piste, mais la colonne reste réservée pour
	// que la largeur du texte ne change pas.
	if out := ansi.Strip(Render([]string{"a"}, 3, 2, 1, 0, st, st)); out != "a    \n     " {
		t.Errorf("sans ascenseur : %q", out)
	}
}
