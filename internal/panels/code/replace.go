package code

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/textbuf"
)

// Remplacement (Ctrl+H) en trois temps : texte cherché (promptReplace, avec
// recherche pendant la frappe), texte de remplacement (promptWith), puis
// revue des occurrences une à une (promptConfirm).

// replace est l'état d'un remplacement en cours.
type replace struct {
	find, with string
	count      int         // remplacements effectués
	start      textbuf.Pos // début de la revue : on s'arrête en y revenant
	wrapped    bool        // la recherche est repartie du début du fichier
}

// startReview commence la revue des occurrences, depuis la position de
// départ de la recherche.
func (m *Model) startReview() tea.Cmd {
	d := m.doc()
	d.buf.SetCursor(m.origin, false)
	m.repl.count, m.repl.start, m.repl.wrapped = 0, m.origin, false
	if !d.buf.Find(m.repl.find, true) {
		m.prompt = promptNone
		return panel.Flash(i18n.T("Introuvable : ")+m.repl.find, true)
	}
	m.prompt, m.follow = promptConfirm, true
	m.noteWrap(m.origin)
	return nil
}

// noteWrap note si l'occurrence sélectionnée est revenue avant prev (la
// recherche est repartie du début du fichier).
func (m *Model) noteWrap(prev textbuf.Pos) {
	if s, _, ok := m.doc().buf.Selection(); ok && before(s, prev) {
		m.repl.wrapped = true
	}
}

func before(a, b textbuf.Pos) bool {
	return a.Line < b.Line || (a.Line == b.Line && a.Col < b.Col)
}

// confirmKey traite une touche pendant la revue des occurrences.
func (m *Model) confirmKey(s string) tea.Cmd {
	b := m.doc().buf
	switch s {
	case "enter", "y":
		b.Insert(m.repl.with)
		m.repl.count++
		return m.reviewNext()
	case "space", " ", "f3", "down", "n":
		return m.reviewNext()
	case "a", "A":
		m.repl.count += b.ReplaceAll(m.repl.find, m.repl.with)
		return m.endReview()
	case "esc", "q":
		return m.endReview()
	}
	return nil
}

// reviewNext passe à l'occurrence suivante, ou termine après un tour
// complet du fichier.
func (m *Model) reviewNext() tea.Cmd {
	b := m.doc().buf
	prev := b.Cursor()
	if s, _, ok := b.Selection(); ok {
		prev = s
	}
	if !b.Find(m.repl.find, true) {
		return m.endReview()
	}
	m.noteWrap(prev)
	if s, _, _ := b.Selection(); m.repl.wrapped && !before(s, m.repl.start) {
		b.ClearSelection()
		return m.endReview()
	}
	m.follow = true
	return nil
}

func (m *Model) endReview() tea.Cmd {
	m.prompt, m.follow = promptNone, true
	m.lastFind = m.repl.find
	return panel.Flash(fmt.Sprintf(i18n.T("%d remplacement(s)"), m.repl.count), false)
}
