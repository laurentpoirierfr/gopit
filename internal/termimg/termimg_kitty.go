//go:build !windows

// Package termimg affiche des images en haute définition dans le terminal
// avec le protocole graphique Kitty (Kitty, Ghostty), par les « Unicode
// placeholders » : une image envoyée une fois, puis des caractères ordinaires
// à sa place. Voir github.com/laurentpoirierfr/imgview/pkg/kittygfx.
// Sous Windows, rien n'est disponible (Supported renvoie false).
package termimg

import "github.com/laurentpoirierfr/imgview/pkg/kittygfx"

// Supported indique si le terminal affiche les images Kitty.
func Supported() bool { return kittygfx.Supported() }

// CellSize renvoie la taille d'une cellule en pixels (0, 0 : inconnue).
func CellSize() (w, h int) { return kittygfx.CellSize() }

// Fit calcule la place (en cellules) d'une image imgW×imgH pixels.
func Fit(imgW, imgH, maxCols, maxRows, cellW, cellH int) (cols, rows int) {
	return kittygfx.Fit(imgW, imgH, maxCols, maxRows, cellW, cellH)
}

// Transmit renvoie la séquence qui envoie une image PNG au terminal.
func Transmit(id uint32, png []byte, cols, rows int) string {
	return kittygfx.Transmit(id, png, cols, rows)
}

// Delete renvoie la séquence qui libère une image dans le terminal.
func Delete(id uint32) string { return kittygfx.Delete(id) }

// Placeholder renvoie le texte qui affiche l'image (cols×rows cellules).
func Placeholder(id uint32, cols, rows int) string { return kittygfx.Placeholder(id, cols, rows) }
