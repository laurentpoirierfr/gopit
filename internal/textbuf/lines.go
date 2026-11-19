package textbuf

// Opérations sur les lignes entières (déplacer, dupliquer, supprimer,
// sélectionner, coller au-dessus), par mot (effacer, sélectionner) et sur les
// parenthèses (entourer, paire correspondante). Chacune est une seule étape
// d'annulation.

import "strings"

// block renvoie les lignes touchées (sélection ou ligne du curseur), sans
// retour à la ligne final.
func (b *Buffer) block() (first, last int, text string) {
	first, last = b.lineRange()
	parts := make([]string, 0, last-first+1)
	for l := first; l <= last; l++ {
		parts = append(parts, string(b.lines[l]))
	}
	return first, last, strings.Join(parts, "\n")
}

// shiftLines décale le curseur et l'ancre de d lignes (sélection gardée).
func (b *Buffer) shiftLines(cur Pos, anchor *Pos, d int) {
	b.cursor = b.clamp(Pos{cur.Line + d, cur.Col})
	if anchor != nil {
		a := b.clamp(Pos{anchor.Line + d, anchor.Col})
		b.anchor = &a
	}
	b.goal = b.cursor.Col
}

// MoveLines déplace les lignes touchées d'une ligne vers le haut (dir < 0)
// ou vers le bas ; il renvoie false au bord du texte.
func (b *Buffer) MoveLines(dir int) bool {
	first, last, block := b.block()
	cur, anchor := b.cursor, b.anchor
	switch {
	case dir < 0 && first > 0:
		above := string(b.lines[first-1])
		b.apply(Pos{first - 1, 0}, Pos{last, len(b.lines[last])}, block+"\n"+above)
		b.shiftLines(cur, anchor, -1)
	case dir > 0 && last < len(b.lines)-1:
		below := string(b.lines[last+1])
		b.apply(Pos{first, 0}, Pos{last + 1, len(b.lines[last+1])}, below+"\n"+block)
		b.shiftLines(cur, anchor, 1)
	default:
		return false
	}
	return true
}

// DuplicateLines copie les lignes touchées juste en dessous ; avec dir > 0,
// le curseur (et la sélection) passent sur la copie.
func (b *Buffer) DuplicateLines(dir int) {
	_, last, block := b.block()
	first, _ := b.lineRange()
	cur, anchor := b.cursor, b.anchor
	b.apply(Pos{last, len(b.lines[last])}, Pos{last, len(b.lines[last])}, "\n"+block)
	n := 0
	if dir > 0 {
		n = last - first + 1
	}
	b.shiftLines(cur, anchor, n)
}

// DeleteLines supprime les lignes touchées ; le curseur garde sa colonne.
func (b *Buffer) DeleteLines() {
	first, last := b.lineRange()
	col := b.cursor.Col
	switch {
	case last < len(b.lines)-1:
		b.apply(Pos{first, 0}, Pos{last + 1, 0}, "")
	case first > 0:
		b.apply(Pos{first - 1, len(b.lines[first-1])}, Pos{last, len(b.lines[last])}, "")
		first--
	default:
		b.apply(Pos{0, 0}, Pos{last, len(b.lines[last])}, "")
	}
	b.cursor = b.clamp(Pos{first, col})
	b.goal = b.cursor.Col
}

// SelectLine sélectionne la ligne du curseur (avec son retour à la ligne) ;
// si des lignes entières sont déjà sélectionnées, ajoute la suivante.
func (b *Buffer) SelectLine() {
	start, end := Pos{b.cursor.Line, 0}, b.cursor
	if s, e, ok := b.Selection(); ok && s.Col == 0 && (e.Col == 0 || e.Line == len(b.lines)-1) {
		start, end = s, e
	} else {
		end = Pos{b.cursor.Line, 0}
	}
	if end.Line+1 < len(b.lines) {
		end = Pos{end.Line + 1, 0}
	} else {
		end = Pos{end.Line, len(b.lines[end.Line])}
	}
	b.selectRange(start, end)
}

// InsertLineBelow ouvre une ligne sous la ligne du curseur (ou sous les
// lignes sélectionnées), quelle que soit sa position, avec l'indentation qui
// convient.
func (b *Buffer) InsertLineBelow() {
	_, last := b.lineRange()
	b.ClearSelection()
	b.cursor = Pos{last, len(b.lines[last])}
	b.Newline()
}

// InsertLineAbove ouvre une ligne au-dessus de la ligne du curseur, à la
// même indentation.
func (b *Buffer) InsertLineAbove() {
	l, _ := b.lineRange()
	b.ClearSelection()
	indent := string(b.lines[l][:indentWidth(b.lines[l])])
	b.apply(Pos{l, 0}, Pos{l, 0}, indent+"\n")
	b.cursor = Pos{l, len([]rune(indent))}
	b.goal = b.cursor.Col
}

// PasteLines colle des lignes entières (copiées sans sélection) au-dessus de
// la ligne du curseur, réindentées au niveau de cette ligne ; le curseur
// reste sur son texte.
func (b *Buffer) PasteLines(text string) {
	b.ClearSelection()
	text = strings.TrimSuffix(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	l, col := b.cursor.Line, b.cursor.Col
	indent := string(b.lines[l][:indentWidth(b.lines[l])])
	text = reindent(text, indent) + "\n"
	b.apply(Pos{l, 0}, Pos{l, 0}, text)
	b.cursor = b.clamp(Pos{l + strings.Count(text, "\n"), col})
	b.goal = b.cursor.Col
}

// reindent remplace l'indentation commune des lignes non vides de text par
// indent (les lignes vides restent vides).
func reindent(text, indent string) string {
	lines := strings.Split(text, "\n")
	common := ""
	first := true
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		lead := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		switch {
		case first:
			common, first = lead, false
		default:
			for !strings.HasPrefix(lead, common) {
				common = common[:len(common)-1]
			}
		}
	}
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			lines[i] = ""
			continue
		}
		lines[i] = indent + strings.TrimPrefix(l, common)
	}
	return strings.Join(lines, "\n")
}

// DeleteWordLeft efface jusqu'au début du mot (ou la sélection).
func (b *Buffer) DeleteWordLeft() {
	if s, e, ok := b.Selection(); ok {
		b.apply(s, e, "")
		return
	}
	if s := b.wordLeft(b.cursor); s != b.cursor {
		b.apply(s, b.cursor, "")
	}
}

// DeleteWordRight efface jusqu'à la fin du mot (ou la sélection).
func (b *Buffer) DeleteWordRight() {
	if s, e, ok := b.Selection(); ok {
		b.apply(s, e, "")
		return
	}
	if e := b.wordRight(b.cursor); e != b.cursor {
		c := b.cursor
		b.apply(c, e, "")
		b.cursor = c
	}
}

// SelectWord sélectionne le mot en p (ou le caractère, hors d'un mot).
func (b *Buffer) SelectWord(p Pos) {
	p = b.clamp(p)
	l := b.lines[p.Line]
	if len(l) == 0 {
		b.SetCursor(p, false)
		return
	}
	at := min(p.Col, len(l)-1)
	if !isWord(l[at]) && at > 0 && isWord(l[at-1]) {
		at-- // juste après un mot : ce mot
	}
	s, e := at, at+1
	if isWord(l[at]) {
		for s > 0 && isWord(l[s-1]) {
			s--
		}
		for e < len(l) && isWord(l[e]) {
			e++
		}
	}
	b.selectRange(Pos{p.Line, s}, Pos{p.Line, e})
}

// RuneAt renvoie le caractère en p (0 au-delà de la fin de la ligne).
func (b *Buffer) RuneAt(p Pos) rune {
	if p.Line < 0 || p.Line >= len(b.lines) || p.Col < 0 || p.Col >= len(b.lines[p.Line]) {
		return 0
	}
	return b.lines[p.Line][p.Col]
}

// Surround entoure la sélection de open et close, et garde le texte entouré
// sélectionné ; sans sélection, il renvoie false.
func (b *Buffer) Surround(open, close string) bool {
	s, e, ok := b.Selection()
	if !ok {
		return false
	}
	inner := b.text(s, e)
	b.apply(s, e, open+inner+close)
	in := Pos{s.Line, s.Col + len([]rune(open))}
	out := endOf(in, inner)
	b.selectRange(in, out)
	return true
}

// InsertPair insère open et close et place le curseur entre les deux.
func (b *Buffer) InsertPair(open, close string) {
	b.Insert(open + close)
	b.cursor.Col -= len([]rune(close))
	b.goal = b.cursor.Col
}

// DeletePair efface le caractère avant le curseur et celui après (paire
// vide « () » ouverte automatiquement).
func (b *Buffer) DeletePair() {
	c := b.cursor
	b.apply(Pos{c.Line, c.Col - 1}, Pos{c.Line, c.Col + 1}, "")
}

// MatchBracket renvoie la parenthèse, le crochet ou l'accolade qui
// correspond à celle située en p ; ignore indique les positions à sauter
// (chaînes, commentaires).
func (b *Buffer) MatchBracket(p Pos, ignore func(Pos) bool) (Pos, bool) {
	pairs := map[rune]rune{'(': ')', '[': ']', '{': '}', ')': '(', ']': '[', '}': '{'}
	r := b.RuneAt(p)
	other, ok := pairs[r]
	if !ok || (ignore != nil && ignore(p)) {
		return Pos{}, false
	}
	dir := 1
	if strings.ContainsRune(")]}", r) {
		dir = -1
	}
	depth := 0
	for q := p; ; {
		// Position suivante (ou précédente), ligne par ligne.
		q.Col += dir
		for q.Col < 0 || q.Col >= len(b.lines[q.Line]) {
			q.Line += dir
			if q.Line < 0 || q.Line >= len(b.lines) {
				return Pos{}, false
			}
			if dir > 0 {
				q.Col = 0
			} else {
				q.Col = len(b.lines[q.Line]) - 1
			}
			if len(b.lines[q.Line]) > 0 {
				break
			}
		}
		if ignore != nil && ignore(q) {
			continue
		}
		switch b.RuneAt(q) {
		case r:
			depth++
		case other:
			if depth == 0 {
				return q, true
			}
			depth--
		}
	}
}

// BracketNear renvoie la position d'une parenthèse au curseur, ou juste
// avant lui (curseur placé après une parenthèse fermante).
func (b *Buffer) BracketNear(p Pos) (Pos, bool) {
	isBracket := func(r rune) bool { return strings.ContainsRune("()[]{}", r) }
	if isBracket(b.RuneAt(p)) {
		return p, true
	}
	if p.Col > 0 && isBracket(b.RuneAt(Pos{p.Line, p.Col - 1})) {
		return Pos{p.Line, p.Col - 1}, true
	}
	return Pos{}, false
}

// IsWordRune indique si r fait partie d'un identifiant.
func IsWordRune(r rune) bool { return isWord(r) }
