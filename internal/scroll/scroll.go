// Package scroll fournit un ascenseur vertical pour les zones qui défilent :
// une piste sur toute la hauteur et un curseur dont la taille et la position
// reflètent la partie visible du contenu.
package scroll

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Thumb renvoie la position (première ligne) et la taille du curseur pour
// une zone de h lignes affichant le contenu total à partir de offset.
// ok vaut false si tout le contenu tient dans la zone (pas d'ascenseur).
func Thumb(h, total, offset int) (pos, size int, ok bool) {
	if h <= 0 || total <= h {
		return 0, 0, false
	}
	size = max(1, h*h/total)
	maxOff := total - h
	offset = min(max(offset, 0), maxOff)
	// Arrondi au plus proche ; le curseur touche le bas en fin de contenu.
	pos = (offset*(h-size) + maxOff/2) / maxOff
	return pos, size, true
}

// Clamp borne un décalage pour que la zone reste pleine.
func Clamp(offset, h, total int) int {
	return min(max(offset, 0), max(0, total-h))
}

// Render place l'ascenseur à droite des lignes visibles. Les lignes doivent
// déjà être tronquées ou repliées à la largeur w ; chaque ligne est complétée
// à w puis suivie d'un espace et de la piste. track et thumb colorent la
// piste et le curseur.
func Render(lines []string, w, h, total, offset int, track, thumb lipgloss.Style) string {
	pos, size, ok := Thumb(h, total, offset)
	out := make([]string, h)
	for i := range h {
		l := ""
		if i < len(lines) {
			l = lines[i]
		}
		if pad := w - lipgloss.Width(l); pad > 0 {
			l += strings.Repeat(" ", pad)
		}
		bar := " "
		if ok {
			if i >= pos && i < pos+size {
				bar = thumb.Render("┃")
			} else {
				bar = track.Render("│")
			}
		}
		out[i] = l + " " + bar
	}
	return strings.Join(out, "\n")
}
