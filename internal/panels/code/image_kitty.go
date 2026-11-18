//go:build !windows

package code

import "github.com/laurentpoirierfr/imgview/pkg/kittygfx"

// Images haute définition par le protocole graphique Kitty (Kitty, Ghostty) :
// voir github.com/laurentpoirierfr/imgview/pkg/kittygfx.

func hdSupported() bool { return kittygfx.Supported() }

func cellSize() (w, h int) { return kittygfx.CellSize() }

func hdFit(imgW, imgH, maxCols, maxRows, cellW, cellH int) (cols, rows int) {
	return kittygfx.Fit(imgW, imgH, maxCols, maxRows, cellW, cellH)
}

func hdTransmit(id uint32, png []byte, cols, rows int) string {
	return kittygfx.Transmit(id, png, cols, rows)
}

func hdDelete(id uint32) string { return kittygfx.Delete(id) }

func hdPlaceholder(id uint32, cols, rows int) string { return kittygfx.Placeholder(id, cols, rows) }
