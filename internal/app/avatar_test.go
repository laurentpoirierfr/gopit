package app

import (
	"image"
	"image/color"
	"runtime"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
)

func TestCircle(t *testing.T) {
	img := avatarImage()
	if img == nil {
		t.Fatal("avatar embarqué illisible")
	}
	b := img.Bounds()
	if b.Dx() != b.Dy() {
		t.Fatalf("avatar carré attendu : %v", b)
	}
	n := b.Dx()
	for _, p := range []image.Point{{0, 0}, {n - 1, 0}, {0, n - 1}, {n - 1, n - 1}} {
		if a := img.RGBAAt(p.X, p.Y).A; a != 0 {
			t.Errorf("coin %v opaque (%d) : le cercle doit le rendre transparent", p, a)
		}
	}
	for _, p := range []image.Point{{n / 2, n / 2}, {n / 2, 2}, {2, n / 2}} {
		if a := img.RGBAAt(p.X, p.Y).A; a != 255 {
			t.Errorf("%v transparent (%d) : intérieur du cercle", p, a)
		}
	}
}

func TestHalfBlocks(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range src.Pix {
		src.Pix[i] = 255
	}
	out := halfBlocks(circle(src), 10, 5, color.Black)
	lines := strings.Split(out, "\n")
	if len(lines) != 5 {
		t.Fatalf("%d lignes", len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != 10 {
			t.Errorf("ligne %d : largeur %d", i, w)
		}
	}
}

func TestAboutWindow(t *testing.T) {
	t.Setenv("IMGVIEW_NO_KITTY", "1") // demi-blocs, quel que soit le terminal
	m := newApp(t, nil)
	press(m, "alt+d") // menu Aide
	press(m, "down")
	if cmd := press(m, "enter"); cmd != nil || m.modal != modalAbout {
		t.Fatalf("À propos : fenêtre %d, commande %v (rien à envoyer au terminal en demi-blocs)", m.modal, cmd != nil)
	}
	out := ansi.Strip(m.render())
	if !strings.Contains(out, authorName) || !strings.Contains(out, authorURL) || !strings.Contains(out, "▀") {
		t.Errorf("fenêtre À propos :\n%s", out)
	}
	// L'avatar est à gauche du texte, sur les mêmes lignes.
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, authorName) && !strings.Contains(l, "▀") {
			t.Errorf("avatar attendu à gauche de « %s » : %q", authorName, l)
		}
	}
	for _, l := range i18n.Langs {
		i18n.Set(l)
		for n, line := range strings.Split(m.render(), "\n") {
			if w := ansi.StringWidth(line); w > width {
				t.Errorf("%s, ligne %d : largeur %d", l, n, w)
			}
		}
	}
}

func TestAboutWindowHD(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pas d'images HD sous Windows")
	}
	t.Setenv("IMGVIEW_NO_KITTY", "")
	t.Setenv("TERM_PROGRAM", "ghostty")
	m := newApp(t, nil)
	if cmd := m.avatarCmd(); cmd == nil {
		t.Fatal("avatar envoyé au terminal attendu")
	}
	if m.avatarCmd() != nil {
		t.Error("l'avatar n'est envoyé qu'une fois")
	}
	m.modal = modalAbout
	if out := m.render(); !strings.ContainsRune(out, '\U0010EEEE') {
		t.Error("caractères de remplacement Kitty attendus")
	}
}
