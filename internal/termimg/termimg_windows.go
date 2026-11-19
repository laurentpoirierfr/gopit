//go:build windows

// Package termimg : pas d'images haute définition sous Windows.
package termimg

func Supported() bool                              { return false }
func CellSize() (w, h int)                         { return 0, 0 }
func Fit(_, _, _, _, _, _ int) (cols, rows int)    { return 0, 0 }
func Transmit(_ uint32, _ []byte, _, _ int) string { return "" }
func Delete(_ uint32) string                       { return "" }
func Placeholder(_ uint32, _, _ int) string        { return "" }
