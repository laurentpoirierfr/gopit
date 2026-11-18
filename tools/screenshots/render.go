package main

// Rendu d'un écran de gopit (texte avec séquences ANSI) en image : grille
// de cellules décodée par l'émulateur VT, glyphes dessinés avec une police
// Nerd Font, puis fenêtre (barre de titre, ombre, coins arrondis) sur un
// fond dégradé.

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// renderer dessine des cellules de terminal.
type renderer struct {
	regular, bold, italic font.Face
	cw, ch, ascent        int        // cellule et ligne de base, en pixels
	fg, bg                color.RGBA // couleurs par défaut du terminal
	size                  float64
	fallback              map[rune]font.Face // police de secours par caractère (nil : aucune)
}

// fallbackFace trouve, comme un vrai terminal, une police qui contient un
// caractère absent de la police principale (fontconfig : fc-match).
func (r *renderer) fallbackFace(c rune) font.Face {
	if f, ok := r.fallback[c]; ok {
		return f
	}
	r.fallback[c] = nil
	out, err := exec.Command("fc-match", "-f", "%{file}", fmt.Sprintf("monospace:charset=%x", c)).Output()
	if err != nil || len(out) == 0 {
		return nil
	}
	f, err := loadFace(string(out), r.size)
	if err != nil {
		return nil
	}
	if _, ok := f.GlyphAdvance(c); !ok {
		return nil
	}
	r.fallback[c] = f
	return f
}

func loadFace(path string, size float64) (font.Face, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := opentype.Parse(data)
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
}

// newRenderer charge les trois graisses de la police (dir/<nom>-Regular.ttf…).
func newRenderer(dir, name string, size float64, fg, bg color.Color) (*renderer, error) {
	r := &renderer{fg: toRGBA(fg), bg: toRGBA(bg), size: size, fallback: map[rune]font.Face{}}
	var err error
	for _, v := range []struct {
		face  *font.Face
		style string
	}{{&r.regular, "Regular"}, {&r.bold, "Bold"}, {&r.italic, "Italic"}} {
		if *v.face, err = loadFace(filepath.Join(dir, name+"-"+v.style+".ttf"), size); err != nil {
			return nil, fmt.Errorf("police : %w", err)
		}
	}
	adv, _ := r.regular.GlyphAdvance('M')
	m := r.regular.Metrics()
	// Hauteur de ligne sans interligne : les traits verticaux (│) se touchent.
	r.cw, r.ch, r.ascent = adv.Ceil(), (m.Ascent + m.Descent).Ceil(), m.Ascent.Ceil()
	return r, nil
}

func toRGBA(c color.Color) color.RGBA {
	r, g, b, a := c.RGBA()
	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}

func mix(a, b color.RGBA, t float64) color.RGBA {
	l := func(x, y uint8) uint8 { return uint8(float64(x)*(1-t) + float64(y)*t + 0.5) }
	return color.RGBA{l(a.R, b.R), l(a.G, b.G), l(a.B, b.B), 255}
}

// terminal dessine un écran de cols×rows cellules.
func (r *renderer) terminal(screen string, cols, rows int) *image.RGBA {
	emu := vt.NewEmulator(cols, rows)
	_, _ = emu.WriteString(strings.ReplaceAll(screen, "\n", "\r\n"))
	img := image.NewRGBA(image.Rect(0, 0, cols*r.cw, rows*r.ch))
	draw.Draw(img, img.Bounds(), image.NewUniform(r.bg), image.Point{}, draw.Src)
	for y := range rows {
		for x := range cols {
			c := emu.CellAt(x, y)
			if c == nil || c.Width == 0 {
				continue // suite d'un caractère large
			}
			r.cell(img, x, y, c)
		}
	}
	return img
}

func (r *renderer) cell(img *image.RGBA, x, y int, c *uv.Cell) {
	st := c.Style
	fg, bg := r.fg, r.bg
	if st.Fg != nil {
		fg = toRGBA(st.Fg)
	}
	if st.Bg != nil {
		bg = toRGBA(st.Bg)
	}
	if st.Attrs&uv.AttrReverse != 0 {
		fg, bg = bg, fg
	}
	if st.Attrs&uv.AttrFaint != 0 {
		fg = mix(fg, bg, 0.45)
	}
	w := max(1, c.Width)
	px, py := x*r.cw, y*r.ch
	cellRect := image.Rect(px, py, px+w*r.cw, py+r.ch)
	draw.Draw(img, cellRect, image.NewUniform(bg), image.Point{}, draw.Src)

	if text := c.Content; text != "" && text != " " {
		face := r.regular
		switch {
		case st.Attrs&uv.AttrBold != 0:
			face = r.bold
		case st.Attrs&uv.AttrItalic != 0:
			face = r.italic
		}
		if first := []rune(text)[0]; first > 0x7f {
			if _, ok := face.GlyphAdvance(first); !ok {
				if fb := r.fallbackFace(first); fb != nil {
					face = fb
				}
			}
		}
		d := font.Drawer{Dst: img, Src: image.NewUniform(fg), Face: face, Dot: fixed.P(px, py+r.ascent)}
		d.DrawString(text)
	}

	if st.Underline != uv.UnderlineNone {
		ul := fg
		if st.UnderlineColor != nil {
			ul = toRGBA(st.UnderlineColor)
		}
		base := py + r.ascent + max(2, r.ch/10)
		for i := px; i < px+w*r.cw; i++ {
			yy := base
			if st.Underline == uv.UnderlineCurly { // vague, comme dans les éditeurs
				yy += int(math.Round(1.6 * math.Sin(float64(i)*math.Pi/float64(max(2, r.cw/2)))))
			}
			img.SetRGBA(i, yy, ul)
			img.SetRGBA(i, yy+1, ul)
		}
	}
}

// ─── Fenêtre et fond ─────────────────────────────────────────────────────────

// frame place l'écran dans une fenêtre aux coins arrondis, avec une barre de
// titre et ses trois boutons, une ombre, sur un fond dégradé.
func frame(term *image.RGBA, winBg color.RGBA, scale float64) *image.RGBA {
	s := func(v float64) int { return int(v*scale + 0.5) }
	pad, inner, titleH, radius := s(64), s(14), s(40), float64(s(12))
	tw, th := term.Bounds().Dx(), term.Bounds().Dy()
	winW, winH := tw+2*inner, th+titleH+inner
	W, H := winW+2*pad, winH+2*pad
	out := image.NewRGBA(image.Rect(0, 0, W, H))

	// Fond : dégradé diagonal lavande → bleu → turquoise.
	stops := []color.RGBA{{0xd0, 0xa8, 0xf0, 255}, {0x96, 0xb4, 0xf7, 255}, {0x9e, 0xe6, 0xd9, 255}}
	for y := range H {
		for x := range W {
			t := (float64(x)/float64(W) + float64(y)/float64(H)) / 2
			var c color.RGBA
			if t < 0.5 {
				c = mix(stops[0], stops[1], t*2)
			} else {
				c = mix(stops[1], stops[2], (t-0.5)*2)
			}
			out.SetRGBA(x, y, c)
		}
	}

	// Ombre douce sous la fenêtre.
	wx, wy := pad, pad
	blur := float64(s(28))
	for y := wy - int(blur); y < wy+winH+int(blur)*2; y++ {
		for x := wx - int(blur); x < wx+winW+int(blur); x++ {
			if x < 0 || y < 0 || x >= W || y >= H {
				continue
			}
			d := roundedDist(float64(x-wx), float64(y-wy-s(10)), float64(winW), float64(winH), radius)
			if d <= 0 {
				continue
			}
			a := 0.35 * math.Exp(-d*d/(2*(blur/2.2)*(blur/2.2)))
			out.SetRGBA(x, y, mix(out.RGBAAt(x, y), color.RGBA{10, 10, 30, 255}, a))
		}
	}

	// Fenêtre (anticrénelée sur les bords arrondis), barre de titre un peu
	// plus sombre que l'écran.
	titleBg := mix(winBg, color.RGBA{0, 0, 0, 255}, 0.18)
	for y := range winH {
		for x := range winW {
			cov := 0.5 - roundedDist(float64(x)+0.5, float64(y)+0.5, float64(winW), float64(winH), radius)
			if cov <= 0 {
				continue
			}
			c := winBg
			if y < titleH {
				c = titleBg
			}
			out.SetRGBA(wx+x, wy+y, mix(out.RGBAAt(wx+x, wy+y), c, math.Min(1, cov)))
		}
	}
	// Trois boutons de fenêtre.
	for i, c := range []color.RGBA{{0xff, 0x5f, 0x57, 255}, {0xfe, 0xbc, 0x2e, 255}, {0x28, 0xc8, 0x40, 255}} {
		cx, cy, rr := float64(wx+s(22)+i*s(22)), float64(wy+titleH/2), float64(s(6.5))
		for y := int(cy - rr - 1); y <= int(cy+rr+1); y++ {
			for x := int(cx - rr - 1); x <= int(cx+rr+1); x++ {
				d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy) - rr
				if cov := 0.5 - d; cov > 0 {
					out.SetRGBA(x, y, mix(out.RGBAAt(x, y), c, math.Min(1, cov)))
				}
			}
		}
	}
	draw.Draw(out, image.Rect(wx+inner, wy+titleH, wx+inner+tw, wy+titleH+th), term, image.Point{}, draw.Src)
	return out
}

// roundedDist est la distance (négative à l'intérieur) d'un point au bord
// d'un rectangle w×h aux coins arrondis de rayon r.
func roundedDist(x, y, w, h, r float64) float64 {
	qx := math.Abs(x-w/2) - (w/2 - r)
	qy := math.Abs(y-h/2) - (h/2 - r)
	return math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - r
}
