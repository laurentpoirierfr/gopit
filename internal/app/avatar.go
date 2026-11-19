package app

// Avatar de l'auteur dans la fenêtre « À propos » : la photo du profil
// GitHub, embarquée dans le binaire, découpée en cercle comme sur GitHub.
// En haute définition dans Kitty et Ghostty (protocole graphique Kitty),
// sinon en demi-blocs colorés.

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/image/draw"

	"github.com/laurentpoirierfr/gopit/internal/termimg"
)

//go:embed assets/avatar.png
var avatarPNG []byte

const (
	authorName = "Laurent Poirier"
	authorURL  = "github.com/laurentpoirierfr"

	avatarCols = 26 // place de l'avatar, en cellules (un peu plus de deux fois plus hautes que larges)
	avatarRows = 12

	// avatarID est le numéro de l'image dans le terminal (l'éditeur numérote
	// les siennes à partir de 1).
	avatarID = 0xFFFFFE
)

// avatarImage renvoie l'avatar découpé en cercle (bord adouci).
var avatarImage = sync.OnceValue(func() *image.RGBA {
	src, err := png.Decode(bytes.NewReader(avatarPNG))
	if err != nil {
		return nil
	}
	return circle(src)
})

// circle rend transparent tout ce qui dépasse du cercle inscrit dans l'image,
// avec un pixel de dégradé sur le bord.
func circle(src image.Image) *image.RGBA {
	b := src.Bounds()
	size := min(b.Dx(), b.Dy())
	out := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(out, out.Bounds(), src, b.Min, draw.Src)
	r := float64(size) / 2
	for y := range size {
		for x := range size {
			d := math.Hypot(float64(x)+0.5-r, float64(y)+0.5-r)
			cover := math.Min(1, math.Max(0, r-d+0.5))
			if cover >= 1 {
				continue
			}
			c := out.RGBAAt(x, y)
			out.SetRGBA(x, y, color.RGBA{ // alpha prémultiplié
				uint8(float64(c.R) * cover), uint8(float64(c.G) * cover),
				uint8(float64(c.B) * cover), uint8(float64(c.A) * cover),
			})
		}
	}
	return out
}

// halfBlocks dessine une image en cols×rows cellules avec des demi-blocs « ▀ »
// (deux pixels par cellule), sur la couleur de fond bg (parties transparentes).
func halfBlocks(img image.Image, cols, rows int, bg color.Color) string {
	small := image.NewRGBA(image.Rect(0, 0, cols, rows*2))
	draw.Draw(small, small.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
	draw.CatmullRom.Scale(small, small.Bounds(), img, img.Bounds(), draw.Over, nil)
	var sb strings.Builder
	for y := range rows {
		if y > 0 {
			sb.WriteByte('\n')
		}
		for x := range cols {
			top, bot := small.RGBAAt(x, 2*y), small.RGBAAt(x, 2*y+1)
			fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", top.R, top.G, top.B, bot.R, bot.G, bot.B)
		}
		sb.WriteString("\x1b[0m")
	}
	return sb.String()
}

// avatarHD indique si l'avatar s'affiche en haute définition.
func (m *Model) avatarHD() bool { return termimg.Supported() }

// avatarCmd envoie l'avatar au terminal (une seule fois) pour l'affichage en
// haute définition.
func (m *Model) avatarCmd() tea.Cmd {
	if m.avatarSent || !m.avatarHD() || avatarImage() == nil {
		return nil
	}
	var buf bytes.Buffer
	if png.Encode(&buf, avatarImage()) != nil {
		return nil
	}
	// Place réelle de l'image ronde, d'après la taille des cellules du
	// terminal (sinon l'image serait déformée en ovale).
	cw, ch := termimg.CellSize()
	m.avatarC, m.avatarR = termimg.Fit(avatarImage().Bounds().Dx(), avatarImage().Bounds().Dy(), avatarCols, avatarRows, cw, ch)
	if m.avatarC == 0 {
		return nil
	}
	m.avatarSent = true
	return tea.Raw(termimg.Transmit(avatarID, buf.Bytes(), m.avatarC, m.avatarR))
}

// viewAvatar dessine l'avatar rond (sur la couleur de fond bg).
func (m *Model) viewAvatar(bg color.Color) string {
	img := avatarImage()
	if img == nil {
		return ""
	}
	if m.avatarHD() && m.avatarSent {
		// Centré dans la place prévue (avatarCols×avatarRows).
		lines := strings.Split(termimg.Placeholder(avatarID, m.avatarC, m.avatarR), "\n")
		left := strings.Repeat(" ", (avatarCols-m.avatarC)/2)
		right := strings.Repeat(" ", avatarCols-m.avatarC-len(left))
		out := make([]string, 0, avatarRows)
		for range (avatarRows - m.avatarR) / 2 {
			out = append(out, strings.Repeat(" ", avatarCols))
		}
		for _, l := range lines {
			out = append(out, left+l+right)
		}
		return strings.Join(out, "\n")
	}
	return halfBlocks(img, avatarCols, avatarRows, bg)
}
