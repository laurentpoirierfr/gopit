package code

// Imports organisés par gopls (comme goimports) : imports manquants
// ajoutés, inutilisés retirés, triés. À l'enregistrement (désactivable), ou
// à la demande (Alt+Maj+O). Sans gopls, ou s'il tarde, l'enregistrement se
// fait sans.

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/lsp"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/textbuf"
)

// organizeTimeout borne l'attente de gopls avant d'enregistrer sans lui.
var organizeTimeout = 3 * time.Second

// organizedMsg est la réponse de gopls : text est le contenu de path aux
// imports organisés, calculé sur la version ver du tampon.
type organizedMsg struct {
	path string
	ver  int
	text string
	save bool // enregistrer ensuite
	err  error
}

// organize demande à gopls d'organiser les imports de d ; nil si ce n'est
// pas possible (pas un fichier Go, gopls absent, réglage désactivé à
// l'enregistrement).
func (m *Model) organize(d *doc, save bool) tea.Cmd {
	if !isGo(d) || m.lsp == nil || !d.lspOpen || (save && !m.imports) {
		return nil
	}
	m.sendChange(d) // gopls doit avoir le texte du tampon
	c, path, text, ver := m.lsp, d.path, d.buf.String(), d.buf.Version()
	lines := strings.Split(text, "\n")
	end := lsp.Pos{Line: len(lines) - 1, Col: utf8.RuneCountInString(lines[len(lines)-1])}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), organizeTimeout)
		defer cancel()
		out := text
		acts, err := c.CodeActions(ctx, path, lsp.Pos{}, end, nil, "source.organizeImports")
		if err == nil && len(acts) > 0 {
			out = lsp.Apply(text, path, acts[0].Edits)
		}
		return organizedMsg{path: path, ver: ver, text: out, save: save, err: err}
	}
}

// onOrganized applique la réponse de gopls, puis enregistre si demandé. Si
// le texte a changé entre-temps, il est enregistré sans les imports.
func (m *Model) onOrganized(msg organizedMsg) tea.Cmd {
	var d *doc
	for _, x := range m.docs {
		if x.path == msg.path {
			d = x
		}
	}
	if d == nil {
		return nil
	}
	current := d.buf.Version() == msg.ver
	if msg.save {
		if !current {
			return m.write(d, "")
		}
		return m.write(d, msg.text)
	}
	switch {
	case msg.err != nil:
		return panel.Flash(i18n.T("Imports : ")+firstLine(msg.err.Error()), true)
	case !current:
		return nil // texte modifié pendant la demande : on laisse tel quel
	case msg.text == d.buf.String():
		return panel.Flash(i18n.T("Imports déjà en ordre"), false)
	}
	d.buf.SetTextAt(msg.text, textbuf.MapPos(d.buf.String(), msg.text, d.buf.Cursor()))
	m.follow = true
	return panel.Flash(i18n.T("Imports organisés"), false)
}
