package code

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/laurentpoirierfr/gopit/internal/gopls"
	"github.com/laurentpoirierfr/gopit/internal/i18n"
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

// defMsg est la réponse de gopls.
type defMsg struct {
	from mark // position de la demande
	jump bool // aller à la définition (sinon : afficher la documentation)
	def  *gopls.Definition
	err  error
}

// lookup demande à gopls la définition du symbole sous le curseur.
func (m *Model) lookup(jump bool) tea.Cmd {
	d := m.doc()
	if d == nil {
		return nil
	}
	if filepath.Ext(d.path) != ".go" {
		return panel.Flash(i18n.T("gopls ne s'applique qu'aux fichiers Go"), true)
	}
	if d.buf.Dirty() {
		return panel.Flash(i18n.T("Enregistre d'abord (Ctrl+S) : gopls lit le fichier sur le disque"), true)
	}
	lines := make([]string, d.buf.LineCount())
	for i := range lines {
		lines[i] = d.buf.Line(i)
	}
	cur := d.buf.Cursor()
	off := gopls.Offset(lines, cur.Line, cur.Col)
	from, root, define := mark{d.path, cur}, m.proj.Root, m.define
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		def, err := define(ctx, root, from.path, off)
		return defMsg{from: from, jump: jump, def: def, err: err}
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
		m.info = msg.def.Doc
		if m.info == "" {
			return panel.Flash(i18n.T("Pas de documentation pour ce symbole"), false)
		}
		return nil
	}
	src, err := os.ReadFile(msg.def.File)
	if err != nil {
		return panel.Flash(i18n.T("Ouverture : ")+err.Error(), true)
	}
	line, col := gopls.LineCol(src, msg.def.Offset)
	to := mark{msg.def.File, textbuf.Pos{Line: line, Col: col}}
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
