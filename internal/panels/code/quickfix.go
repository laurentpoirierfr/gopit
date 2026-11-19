package code

// Corrections rapides (Ctrl+. ou Alt+Entrée) : les corrections de gopls
// pour les problèmes de la ligne du curseur (import à ajouter, variable
// inutilisée, simplification…), dans une liste comme celle de la
// complétion ; Entrée applique la correction choisie.

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/lsp"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/textbuf"
)

const actQuickFix panel.EditAction = "quick-fix"

// quickRetry espace les nouvelles demandes quand gopls n'a pas encore de
// correction pour un problème signalé.
var quickRetry = 700 * time.Millisecond

// quickMsg est la réponse de gopls : les corrections pour le curseur at de
// la version ver du tampon.
type quickMsg struct {
	path    string
	at      textbuf.Pos
	ver     int
	actions []lsp.CodeAction
	err     error
}

// requestQuickFix demande à gopls les corrections de la ligne du curseur.
func (m *Model) requestQuickFix() tea.Cmd {
	d := m.doc()
	switch {
	case d == nil || !isGo(d):
		return panel.Flash(i18n.T("Les corrections rapides ne concernent que les fichiers Go"), true)
	case m.lsp == nil || !d.lspOpen:
		return panel.Flash(i18n.T("gopls n'est pas disponible"), true)
	}
	m.sendChange(d)
	cur := d.buf.Cursor()
	diags := m.lineDiags(d, cur.Line)
	// Zone : les problèmes de la ligne, ou le curseur seul.
	start, end := lsp.Pos(cur), lsp.Pos(cur)
	for i, g := range diags {
		if i == 0 || lspBefore(g.Start, start) {
			start = g.Start
		}
		if i == 0 || lspBefore(end, g.End) {
			end = g.End
		}
	}
	c, path, ver := m.lsp, d.path, d.buf.Version()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		acts, err := c.CodeActions(ctx, path, start, end, diags, "quickfix")
		// Un problème sur la ligne mais pas encore de correction : gopls
		// finit peut-être son analyse (fichier tout juste ouvert ou modifié).
		for try := 0; try < 3 && err == nil && len(acts) == 0 && len(diags) > 0; try++ {
			time.Sleep(quickRetry)
			acts, err = c.CodeActions(ctx, path, start, end, diags, "quickfix")
		}
		return quickMsg{path: path, at: cur, ver: ver, actions: acts, err: err}
	}
}

func lspBefore(a, b lsp.Pos) bool { return a.Line < b.Line || (a.Line == b.Line && a.Col < b.Col) }

// onQuickFix ouvre la liste des corrections, si rien n'a bougé entre-temps.
func (m *Model) onQuickFix(msg quickMsg) tea.Cmd {
	d := m.doc()
	if d == nil || d.path != msg.path || d.buf.Version() != msg.ver || d.buf.Cursor() != msg.at {
		return nil
	}
	if msg.err != nil {
		return panel.Flash("gopls : "+firstLine(msg.err.Error()), true)
	}
	var acts []lsp.CodeAction
	for _, a := range msg.actions {
		if onlyIn(a, d.path) {
			acts = append(acts, a)
		}
	}
	if len(acts) == 0 {
		return panel.Flash(i18n.T("Aucune correction rapide pour cette ligne"), false)
	}
	c := &completion{path: d.path, start: msg.at, actions: acts}
	for i, a := range acts {
		c.items = append(c.items, lsp.Item{Label: a.Title})
		c.shown = append(c.shown, i)
	}
	m.compl, m.editing = c, true
	return nil
}

// onlyIn indique si une correction ne modifie que le fichier path.
func onlyIn(a lsp.CodeAction, path string) bool {
	for _, e := range a.Edits {
		if e.Path != path {
			return false
		}
	}
	return true
}

// applyAction applique une correction au document, en une étape
// d'annulation, le curseur suivant son texte.
func (m *Model) applyAction(d *doc, a lsp.CodeAction) tea.Cmd {
	old := d.buf.String()
	text := lsp.Apply(old, d.path, a.Edits)
	d.buf.SetTextAt(text, textbuf.MapPos(old, text, d.buf.Cursor()))
	m.follow = true
	return panel.Flash(i18n.T("Correction appliquée : ")+a.Title, false)
}

// complClick traite un clic dans la liste (complétion ou corrections)
// ouverte : une ligne est choisie et appliquée ; ok est faux si le clic est
// ailleurs.
func (m *Model) complClick(c panel.ClickMsg) (tea.Cmd, bool) {
	l := m.compl
	if l == nil || c.Drag {
		return nil, false
	}
	bx, by, bw, bh := m.complBox[0], m.complBox[1], m.complBox[2], m.complBox[3]
	x, y := c.X-m.treeW, c.Y-1 // dans le texte : sous la ligne des onglets
	if x < bx || x >= bx+bw || y < by || y >= by+bh {
		return nil, false
	}
	if row := y - by - 1; row >= 0 && row < complRows && l.offset+row < len(l.shown) { // bordure du haut
		l.sel = l.offset + row
		return m.acceptCompletion(), true
	}
	return nil, true
}
