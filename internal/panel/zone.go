package panel

// Zones cliquables : un module entoure, au rendu, ce qui réagit à la souris
// (une ligne de liste, un onglet, un volet) avec Zone ; les marqueurs sont
// des séquences APC, sans largeur, que les styles, bordures et troncatures
// de Lip Gloss conservent. Le cadre les repère dans l'écran du module (Scan),
// les retire, puis indique dans ClickMsg et WheelMsg les zones sous la
// souris : le module n'a pas à refaire le calcul de sa mise en page.

import (
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const (
	zoneOpen  = "\x1b_z" // suivi de l'identifiant puis de zoneST
	zoneClose = "\x1b_Z\x1b\\"
	zoneST    = "\x1b\\"
)

// Zone marque s (chacune de ses lignes) comme la zone id.
func Zone(id, s string) string {
	if !strings.Contains(s, "\n") {
		return zoneOpen + id + zoneST + s + zoneClose
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = zoneOpen + id + zoneST + l + zoneClose
	}
	return strings.Join(lines, "\n")
}

// Row marque une ligne de liste comme la zone id, sur toute la largeur w
// (complétée d'espaces) : un clic dans le blanc après le texte la vise aussi.
func Row(id string, w int, s string) string {
	if pad := w - ansi.StringWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return Zone(id, s)
}

// Seg est la partie d'une zone sur une ligne de l'écran : colonnes [X0, X1).
type Seg struct {
	ID     string
	Y      int
	X0, X1 int
}

// Scan retire les marqueurs de zones de s et renvoie leurs emplacements.
// Une zone dont la fin a disparu (texte coupé) s'étend jusqu'au bout de la
// ligne.
func Scan(s string) (string, []Seg) {
	if !strings.Contains(s, zoneOpen) && !strings.Contains(s, zoneClose) {
		return s, nil
	}
	lines := strings.Split(s, "\n")
	var segs []Seg
	for y, l := range lines {
		var out strings.Builder
		type open struct {
			id string
			x  int
		}
		var stack []open
		x := 0
		for len(l) > 0 {
			i := strings.Index(l, "\x1b_")
			if i < 0 {
				out.WriteString(l)
				x += ansi.StringWidth(l)
				break
			}
			out.WriteString(l[:i])
			x += ansi.StringWidth(l[:i])
			l = l[i:]
			switch {
			case strings.HasPrefix(l, zoneClose):
				if n := len(stack); n > 0 {
					segs = append(segs, Seg{stack[n-1].id, y, stack[n-1].x, x})
					stack = stack[:n-1]
				}
				l = l[len(zoneClose):]
			case strings.HasPrefix(l, zoneOpen):
				end := strings.Index(l, zoneST)
				if end < 0 {
					l = ""
					break
				}
				stack = append(stack, open{l[len(zoneOpen):end], x})
				l = l[end+len(zoneST):]
			default: // autre séquence APC : gardée telle quelle
				out.WriteString(l[:2])
				l = l[2:]
			}
		}
		for _, o := range stack {
			segs = append(segs, Seg{o.id, y, o.x, max(x, o.x+1)})
		}
		lines[y] = out.String()
	}
	return strings.Join(lines, "\n"), segs
}

// ZonesAt renvoie les zones qui contiennent la cellule (x, y), de la plus
// petite (la plus précise : une ligne) à la plus grande (un volet).
func ZonesAt(segs []Seg, x, y int) []string {
	var hit []Seg
	for _, s := range segs {
		if s.Y == y && x >= s.X0 && x < s.X1 {
			hit = append(hit, s)
		}
	}
	// Tri par largeur croissante (peu d'éléments).
	for i := 1; i < len(hit); i++ {
		for j := i; j > 0 && hit[j].X1-hit[j].X0 < hit[j-1].X1-hit[j-1].X0; j-- {
			hit[j], hit[j-1] = hit[j-1], hit[j]
		}
	}
	ids := make([]string, len(hit))
	for i, s := range hit {
		ids[i] = s.ID
	}
	return ids
}

// In renvoie la première zone de zones qui commence par prefix, et ce qui
// suit ce préfixe (« row:12 » → « 12 »).
func In(zones []string, prefix string) (string, bool) {
	for _, z := range zones {
		if rest, ok := strings.CutPrefix(z, prefix); ok {
			return rest, true
		}
	}
	return "", false
}

// Has indique si la zone id est sous la souris.
func Has(zones []string, id string) bool {
	return slices.Contains(zones, id)
}

// Index est In pour un identifiant numérique (« files:3 » → 3).
func Index(zones []string, prefix string) (int, bool) {
	rest, ok := In(zones, prefix)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil
}

// WheelMsg est un cran de molette dans la zone du module (Delta < 0 : vers
// le haut), avec les zones sous la souris.
type WheelMsg struct {
	X, Y  int
	Delta int
	Zones []string
}
