package code

// Affichage des images (PNG, JPEG, GIF, BMP, TIFF, WebP…) dans l'éditeur :
// en haute définition avec le protocole graphique Kitty (Kitty, Ghostty),
// sinon en texte coloré (demi-blocs, ou chafa s'il est installé). Les
// paquets de github.com/laurentpoirierfr/imgview font le rendu. Une image est
// un onglet en lecture seule.

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/laurentpoirierfr/imgview/pkg/imgrender"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/termimg"
	"github.com/laurentpoirierfr/gopit/internal/textbuf"
)

// imageView est l'état d'une image ouverte.
type imageView struct {
	info    imgrender.Info
	key     string // rendu affiché : taille de la zone (vide : à préparer)
	pending string // rendu en préparation
	text    string // rendu en texte
	id      uint32 // image envoyée au terminal (HD), 0 : aucune
	cols    int    // taille de l'image HD, en cellules
	rows    int
	err     string
}

// imageMsg apporte un rendu préparé en arrière-plan.
type imageMsg struct {
	path, key  string
	text       string
	png        []byte
	cols, rows int
	err        error
}

func isImage(path string) bool { return imgrender.IsImage(path) }

// newImageDoc ouvre une image (sans lire son contenu comme du texte).
func (m *Model) newImageDoc(path string) *doc {
	d := &doc{path: path, rel: m.relName(path), buf: textbuf.New(""), hlVer: -1,
		image: &imageView{info: imgrender.ReadInfo(path)}}
	if st, err := os.Stat(path); err == nil {
		d.mtime = st.ModTime().UnixNano()
	}
	return d
}

// imageSync prépare le rendu de l'image affichée pour la taille de la zone
// de texte (appelée après chaque message, comme lspSync).
func (m *Model) imageSync() tea.Cmd {
	d := m.doc()
	if d == nil || d.image == nil || m.textW <= 0 || m.textH <= 0 {
		return nil
	}
	img := d.image
	key := fmt.Sprintf("%d×%d", m.textW, m.textH)
	if img.key == key || img.pending == key {
		return nil
	}
	img.pending = key
	path, w, h, hd, cw, ch, info := d.path, m.textW, m.textH, m.hd, m.cellW, m.cellH, img.info
	return func() tea.Msg {
		msg := imageMsg{path: path, key: key}
		if hd && info.OK {
			msg.cols, msg.rows = termimg.Fit(info.W, info.H, w, h, cw, ch)
			pw, ph := cw, ch
			if pw <= 0 || ph <= 0 {
				pw, ph = 10, 20 // taille habituelle d'une cellule
			}
			msg.png, _, _, msg.err = imgrender.PNG(path, msg.cols*pw, msg.rows*ph)
			return msg
		}
		kind := imgrender.Internal
		if imgrender.ChafaAvailable() {
			kind = imgrender.Chafa
		}
		msg.text, msg.err = imgrender.Render(path, w, h, kind)
		return msg
	}
}

// onImage affiche un rendu préparé (envoie l'image au terminal en HD).
func (m *Model) onImage(msg imageMsg) tea.Cmd {
	var d *doc
	for _, x := range m.docs {
		if x.path == msg.path && x.image != nil {
			d = x
		}
	}
	if d == nil || d.image.pending != msg.key {
		return nil // fermée, ou une autre taille est demandée
	}
	img := d.image
	img.pending, img.key, img.err = "", msg.key, ""
	if msg.err != nil {
		img.err = msg.err.Error()
		return nil
	}
	if msg.png == nil {
		img.text = msg.text
		return nil
	}
	var raw strings.Builder
	if img.id != 0 {
		raw.WriteString(termimg.Delete(img.id)) // l'ancienne taille n'est plus utile
	}
	m.hdNextID++
	img.id, img.cols, img.rows = m.hdNextID, msg.cols, msg.rows
	raw.WriteString(termimg.Transmit(img.id, msg.png, img.cols, img.rows))
	return tea.Raw(raw.String())
}

// closeImage libère l'image dans le terminal.
func closeImage(d *doc) tea.Cmd {
	if d.image == nil || d.image.id == 0 {
		return nil
	}
	return tea.Raw(termimg.Delete(d.image.id))
}

// viewImage dessine l'image centrée dans une zone de w×h cellules.
func (m *Model) viewImage(d *doc, w, h int) string {
	t := m.th
	img := d.image
	var lines []string
	switch {
	case img.err != "":
		lines = []string{t.Err.Render(i18n.T("Image illisible : ") + img.err)}
	case img.key == "":
		lines = []string{t.Dim.Render(i18n.T("Préparation de l'image…"))}
	case img.id != 0:
		lines = strings.Split(termimg.Placeholder(img.id, img.cols, img.rows), "\n")
	default:
		lines = strings.Split(strings.TrimRight(img.text, "\n"), "\n")
	}
	widest := 0
	for _, l := range lines {
		widest = max(widest, ansi.StringWidth(l))
	}
	pad := strings.Repeat(" ", max(0, (w-widest)/2))
	top := max(0, (h-len(lines))/2)
	out := make([]string, 0, h)
	for range top {
		out = append(out, "")
	}
	for _, l := range lines {
		out = append(out, pad+l)
	}
	return strings.Join(out, "\n")
}

// imageStatus décrit l'image pour la barre d'état.
func imageStatus(info imgrender.Info) string {
	s := strings.ToUpper(info.Format)
	if info.OK {
		s += fmt.Sprintf(" · %d×%d", info.W, info.H)
	}
	return s + " · " + imgrender.HumanSize(info.Size)
}
