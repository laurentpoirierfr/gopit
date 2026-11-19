package code

// Aides à l'édition : opérations sur les lignes (actions propres au module),
// copier-coller de lignes entières, fermeture automatique des parenthèses
// et des guillemets, parenthèse correspondante.

import (
	"strings"

	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/textbuf"
)

const (
	actMoveUp     panel.EditAction = "line-up"
	actMoveDown   panel.EditAction = "line-down"
	actDupUp      panel.EditAction = "dup-up"
	actDupDown    panel.EditAction = "dup-down"
	actDeleteLine panel.EditAction = "delete-line"
	actSelectLine panel.EditAction = "select-line"
	actLineBelow  panel.EditAction = "line-below"
	actLineAbove  panel.EditAction = "line-above"
	actMatch      panel.EditAction = "match-bracket"
	actAutoPair   panel.EditAction = "auto-pair"
)

// pairs associe chaque ouvrant à son fermant.
var pairs = map[string]string{"(": ")", "[": "]", "{": "}", `"`: `"`, "`": "`", "'": "'"}

// pasteText colle text : des lignes entières copiées sans sélection vont
// au-dessus de la ligne du curseur (comme dans VS Code), réindentées.
func (m *Model) pasteText(b *textbuf.Buffer, text string) {
	if text == "" {
		return
	}
	if _, _, sel := b.Selection(); !sel && m.clipLines && text == m.clip {
		b.PasteLines(text)
		return
	}
	b.Insert(text)
}

// typeText insère un caractère tapé, avec la fermeture automatique.
func (m *Model) typeText(b *textbuf.Buffer, text string) {
	if !m.autoPair {
		b.Insert(text)
		return
	}
	c := b.Cursor()
	next := b.RuneAt(c)
	if closeOf, ok := pairs[text]; ok {
		if _, _, sel := b.Selection(); sel {
			b.Surround(text, closeOf) // entourer la sélection
			return
		}
		quote := text == closeOf
		if quote && string(next) == text {
			b.MoveCursor(textbuf.Right, 1, false) // guillemet fermant déjà là
			return
		}
		if canPair(b, c, quote) {
			b.InsertPair(text, closeOf)
			return
		}
		b.Insert(text)
		return
	}
	if strings.Contains(")]}", text) && string(next) == text {
		if _, _, sel := b.Selection(); !sel {
			b.MoveCursor(textbuf.Right, 1, false) // fermant déjà là : on passe
			return
		}
	}
	b.Insert(text)
}

// canPair indique s'il convient d'ajouter le fermant : devant un blanc, une
// fin de ligne ou un fermant, et pour un guillemet, pas collé à un mot (une
// apostrophe dans « l'arbre » reste seule).
func canPair(b *textbuf.Buffer, c textbuf.Pos, quote bool) bool {
	next := b.RuneAt(c)
	if next != 0 && next != ' ' && next != '\t' && !strings.ContainsRune(")]},;:", next) {
		return false
	}
	if quote && c.Col > 0 {
		prev := b.RuneAt(textbuf.Pos{Line: c.Line, Col: c.Col - 1})
		if textbuf.IsWordRune(prev) || prev == '"' || prev == '`' || prev == '\'' || prev == '\\' {
			return false
		}
	}
	return true
}

// inEmptyPair indique si le curseur est entre un ouvrant et son fermant
// (« (|) ») : le retour arrière efface les deux.
func (m *Model) inEmptyPair(b *textbuf.Buffer) bool {
	if _, _, sel := b.Selection(); sel {
		return false
	}
	c := b.Cursor()
	if c.Col == 0 {
		return false
	}
	prev := b.RuneAt(textbuf.Pos{Line: c.Line, Col: c.Col - 1})
	closeOf, ok := pairs[string(prev)]
	return ok && string(b.RuneAt(c)) == closeOf
}

// ignoredAt indique si une position est dans une chaîne ou un commentaire
// (les parenthèses n'y comptent pas).
func ignoredAt(d *doc) func(textbuf.Pos) bool {
	cls := d.classes()
	return func(p textbuf.Pos) bool {
		if p.Line >= len(cls) || p.Col >= len(cls[p.Line]) {
			return false
		}
		k := cls[p.Line][p.Col]
		return k == clString || k == clComment
	}
}

// matchingBracket renvoie la parenthèse qui correspond à celle du curseur.
func (m *Model) matchingBracket(d *doc) (textbuf.Pos, bool) {
	at, ok := d.buf.BracketNear(d.buf.Cursor())
	if !ok {
		return textbuf.Pos{}, false
	}
	return d.buf.MatchBracket(at, ignoredAt(d))
}

// bracketMarks renvoie, par ligne, les parenthèses à surligner : celle du
// curseur et sa correspondante.
func (m *Model) bracketMarks(d *doc) map[int][]int {
	if !m.editing {
		return nil
	}
	at, ok := d.buf.BracketNear(d.buf.Cursor())
	if !ok {
		return nil
	}
	other, ok := d.buf.MatchBracket(at, ignoredAt(d))
	if !ok {
		return nil
	}
	marks := map[int][]int{}
	marks[at.Line] = append(marks[at.Line], at.Col)
	marks[other.Line] = append(marks[other.Line], other.Col)
	return marks
}
