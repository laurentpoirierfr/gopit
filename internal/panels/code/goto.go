package code

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/laurentpoirierfr/gopit/internal/gopls"
	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/lsp"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/textbuf"
)

// mark est une position à laquelle revenir (Ctrl+T).
type mark struct {
	path string
	pos  textbuf.Pos
}

// maxBack borne la pile des retours.
const maxBack = 50

// defineFunc interroge gopls ; remplacée dans les tests.
type defineFunc func(ctx context.Context, root, file string, offset int) (*gopls.Definition, error)

// defMsg est la réponse de gopls : la définition ou la documentation du
// symbole demandé.
type defMsg struct {
	from mark  // position de la demande
	jump bool  // aller à la définition (sinon : afficher la documentation)
	to   *mark // définition (nil : aucune)
	doc  string
	err  error
}

// lookup demande à gopls la définition (jump) ou la documentation du
// symbole sous le curseur. Avec la connexion à gopls, le texte affiché fait
// foi, même non enregistré ; sans elle, gopls lit le fichier sur le disque.
func (m *Model) lookup(jump bool) tea.Cmd {
	d := m.doc()
	if d == nil {
		return nil
	}
	if !isGo(d) {
		return panel.Flash(i18n.T("gopls ne s'applique qu'aux fichiers Go"), true)
	}
	cur := d.buf.Cursor()
	from := mark{d.path, cur}
	if c := m.lsp; c != nil {
		m.sendChange(d) // gopls doit voir le texte affiché
		pos := lsp.Pos{Line: cur.Line, Col: cur.Col}
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			msg := defMsg{from: from, jump: jump}
			if !jump {
				msg.doc, msg.err = c.Hover(ctx, from.path, pos)
				return msg
			}
			loc, err := c.Definition(ctx, from.path, pos)
			msg.err = err
			if loc != nil {
				msg.to = &mark{loc.Path, textbuf.Pos{Line: loc.Pos.Line, Col: loc.Pos.Col}}
			}
			return msg
		}
	}

	if d.buf.Dirty() {
		return panel.Flash(i18n.T("Enregistre d'abord (Ctrl+S) : gopls lit le fichier sur le disque"), true)
	}
	lines := make([]string, d.buf.LineCount())
	for i := range lines {
		lines[i] = d.buf.Line(i)
	}
	off := gopls.Offset(lines, cur.Line, cur.Col)
	root, define := m.proj.Root, m.define
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		msg := defMsg{from: from, jump: jump}
		def, err := define(ctx, root, from.path, off)
		if err != nil {
			msg.err = err
			return msg
		}
		msg.doc = def.Doc
		src, err := os.ReadFile(def.File)
		if err != nil {
			msg.err = err
			return msg
		}
		line, col := gopls.LineCol(src, def.Offset)
		msg.to = &mark{def.File, textbuf.Pos{Line: line, Col: col}}
		return msg
	}
}

// onDefinition traite la réponse de gopls.
func (m *Model) onDefinition(msg defMsg) tea.Cmd {
	switch {
	case errors.Is(msg.err, gopls.ErrMissing):
		return panel.Flash(i18n.T("gopls n'est pas installé : ")+gopls.Install, true)
	case msg.err != nil:
		return panel.Flash("gopls : "+msg.err.Error(), !errors.Is(msg.err, gopls.ErrNoIdent))
	}
	// Le curseur a bougé ou le fichier a changé pendant la demande : la
	// réponse ne correspond plus à ce que l'on regarde.
	if d := m.doc(); d == nil || d.path != msg.from.path || d.buf.Cursor() != msg.from.pos {
		return nil
	}
	if !msg.jump {
		m.info = msg.doc
		if m.info == "" {
			return panel.Flash(i18n.T("Pas de documentation pour ce symbole"), false)
		}
		return nil
	}
	if msg.to == nil {
		return panel.Flash(gopls.ErrNoIdent.Error(), false)
	}
	to := *msg.to
	if to == msg.from {
		return panel.Flash(i18n.T("Déjà sur la définition"), false)
	}
	m.back = append(m.back, msg.from)
	if len(m.back) > maxBack {
		m.back = m.back[1:]
	}
	return m.jumpTo(to)
}

// goBack revient à la position d'avant le dernier saut.
func (m *Model) goBack() tea.Cmd {
	if len(m.back) == 0 {
		return panel.Flash(i18n.T("Aucune position précédente"), false)
	}
	to := m.back[len(m.back)-1]
	m.back = m.back[:len(m.back)-1]
	return m.jumpTo(to)
}

// jumpTo affiche une position, dans le mode (lecture / édition) courant.
func (m *Model) jumpTo(to mark) tea.Cmd {
	editing := m.editing || m.doc() == nil
	if cmd := m.OpenFile(to.path, to.pos.Line+1); cmd != nil {
		return cmd
	}
	m.doc().buf.SetCursor(to.pos, false)
	m.editing, m.follow, m.center = editing, true, true
	return nil
}

// viewInfo dessine la documentation du symbole dans un cadre de largeur w,
// au plus maxH lignes.
func (m *Model) viewInfo(w, maxH int) string {
	t := m.th
	inner := max(10, w-4)
	lines := strings.Split(m.info, "\n")
	for i, l := range lines {
		lines[i] = strings.ReplaceAll(l, "\t", "    ")
	}
	sig := lipgloss.NewStyle().Foreground(t.P.Accent).Bold(true).Width(inner).Render(lines[0])
	body := lipgloss.NewStyle().Foreground(t.P.Text).Width(inner).Render(strings.Join(lines[1:], "\n"))
	content := sig
	if strings.TrimSpace(body) != "" {
		content += "\n" + body
	}
	rows := strings.Split(content, "\n")
	if limit := max(1, maxH-3); len(rows) > limit {
		rows = append(rows[:limit-1], t.Dim.Render("…"))
	}
	rows = append(rows, t.Dim.Render(i18n.Key(i18n.T("Échap pour fermer"))))
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.P.Accent).
		Padding(0, 1).Width(w).Render(strings.Join(rows, "\n"))
}
