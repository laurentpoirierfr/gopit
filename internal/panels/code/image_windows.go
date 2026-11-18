//go:build windows

package code

// Sous Windows, pas d'images haute définition : rendu en texte seulement.

func hdSupported() bool                              { return false }
func cellSize() (w, h int)                           { return 0, 0 }
func hdFit(_, _, _, _, _, _ int) (cols, rows int)    { return 0, 0 }
func hdTransmit(_ uint32, _ []byte, _, _ int) string { return "" }
func hdDelete(_ uint32) string                       { return "" }
func hdPlaceholder(_ uint32, _, _ int) string        { return "" }
