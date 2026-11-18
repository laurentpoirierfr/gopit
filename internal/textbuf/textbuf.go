// Package textbuf est le tampon de texte de l'éditeur : lignes, curseur,
// sélection, modifications, annuler/rétablir. Il ne dépend pas de
// l'interface et peut être testé seul.
//
// Les colonnes sont comptées en runes (caractères), pas en octets.
package textbuf

import (
	"strings"
	"time"
	"unicode"
)

// Pos est une position dans le texte (ligne et colonne à partir de 0).
type Pos struct {
	Line, Col int
}

// Before indique si p précède q.
func (p Pos) Before(q Pos) bool {
	return p.Line < q.Line || (p.Line == q.Line && p.Col < q.Col)
}

// edit est une modification réversible : à la position at, le texte removed
// a été remplacé par inserted.
type edit struct {
	at       Pos
	removed  string
	inserted string
	before   Pos // curseur avant
	after    Pos // curseur après
	when     time.Time
	id       int // identifiant unique (état enregistré)
}

// Buffer est un texte modifiable.
type Buffer struct {
	lines  [][]rune
	cursor Pos
	anchor *Pos // début de la sélection (nil : pas de sélection)
	goal   int  // colonne visée lors des déplacements verticaux

	undo, redo []edit
	saved      int // id de la dernière modification enregistrée (0 : aucune)
	nextID     int
	version    int // incrémentée à chaque modification (caches)
	now        func() time.Time
}

// New crée un tampon à partir d'un texte. Les fins de ligne \r\n sont
// ramenées à \n.
func New(text string) *Buffer {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	b := &Buffer{now: time.Now}
	for l := range strings.SplitSeq(text, "\n") {
		b.lines = append(b.lines, []rune(l))
	}
	return b
}

// String renvoie le texte complet.
func (b *Buffer) String() string {
	parts := make([]string, len(b.lines))
	for i, l := range b.lines {
		parts[i] = string(l)
	}
	return strings.Join(parts, "\n")
}

// LineCount renvoie le nombre de lignes.
func (b *Buffer) LineCount() int { return len(b.lines) }

// Line renvoie la ligne i.
func (b *Buffer) Line(i int) string { return string(b.lines[i]) }

// Cursor renvoie la position du curseur.
func (b *Buffer) Cursor() Pos { return b.cursor }

// Version change à chaque modification du texte.
func (b *Buffer) Version() int { return b.version }

// top renvoie l'id de la dernière modification de l'historique (0 : aucune).
func (b *Buffer) top() int {
	if n := len(b.undo); n > 0 {
		return b.undo[n-1].id
	}
	return 0
}

// Dirty indique des modifications non enregistrées. L'état est repéré par
// l'id de la dernière modification : annuler puis modifier autrement ne
// ramène pas à l'état enregistré, même si l'historique a la même longueur.
func (b *Buffer) Dirty() bool { return b.top() != b.saved }

// MarkSaved note que le texte actuel vient d'être enregistré.
func (b *Buffer) MarkSaved() { b.saved = b.top() }

// clamp ramène une position dans le texte.
func (b *Buffer) clamp(p Pos) Pos {
	p.Line = min(max(p.Line, 0), len(b.lines)-1)
	p.Col = min(max(p.Col, 0), len(b.lines[p.Line]))
	return p
}

// ─── Sélection ───────────────────────────────────────────────────────────────

// Selection renvoie la sélection ordonnée ; ok vaut false sans sélection.
func (b *Buffer) Selection() (start, end Pos, ok bool) {
	if b.anchor == nil || *b.anchor == b.cursor {
		return Pos{}, Pos{}, false
	}
	start, end = *b.anchor, b.cursor
	if end.Before(start) {
		start, end = end, start
	}
	return start, end, true
}

// SelectedText renvoie le texte sélectionné.
func (b *Buffer) SelectedText() string {
	s, e, ok := b.Selection()
	if !ok {
		return ""
	}
	return b.text(s, e)
}

// SelectAll sélectionne tout le texte.
func (b *Buffer) SelectAll() {
	b.anchor = &Pos{}
	last := len(b.lines) - 1
	b.cursor = Pos{last, len(b.lines[last])}
}

// ClearSelection annule la sélection.
func (b *Buffer) ClearSelection() { b.anchor = nil }

// text renvoie le texte entre deux positions ordonnées.
func (b *Buffer) text(s, e Pos) string {
	if s.Line == e.Line {
		return string(b.lines[s.Line][s.Col:e.Col])
	}
	var sb strings.Builder
	sb.WriteString(string(b.lines[s.Line][s.Col:]))
	for l := s.Line + 1; l < e.Line; l++ {
		sb.WriteByte('\n')
		sb.WriteString(string(b.lines[l]))
	}
	sb.WriteByte('\n')
	sb.WriteString(string(b.lines[e.Line][:e.Col]))
	return sb.String()
}

// ─── Déplacements ────────────────────────────────────────────────────────────

// Move déplace le curseur ; avec extend, la sélection est étendue.
type Move int

const (
	Left Move = iota
	Right
	Up
	Down
	LineStart // début de ligne (après l'indentation, puis colonne 0)
	LineEnd
	WordLeft
	WordRight
	DocStart
	DocEnd
)

// MoveCursor déplace le curseur. n sert aux déplacements Up/Down (pages).
func (b *Buffer) MoveCursor(mv Move, n int, extend bool) {
	if extend && b.anchor == nil {
		a := b.cursor
		b.anchor = &a
	}
	if !extend {
		// Sans Maj, une flèche gauche/droite réduit la sélection à son bord.
		if s, e, ok := b.Selection(); ok && (mv == Left || mv == Right) {
			b.anchor = nil
			if mv == Left {
				b.cursor = s
			} else {
				b.cursor = e
			}
			b.goal = b.cursor.Col
			return
		}
		b.anchor = nil
	}
	c := b.cursor
	n = max(n, 1)
	vertical := false
	switch mv {
	case Left:
		if c.Col > 0 {
			c.Col--
		} else if c.Line > 0 {
			c.Line--
			c.Col = len(b.lines[c.Line])
		}
	case Right:
		if c.Col < len(b.lines[c.Line]) {
			c.Col++
		} else if c.Line < len(b.lines)-1 {
			c = Pos{c.Line + 1, 0}
		}
	case Up:
		c.Line -= n
		c.Col = b.goal
		vertical = true
		if c.Line < 0 { // au-delà de la première ligne : début du texte
			c, vertical = Pos{}, false
		}
	case Down:
		c.Line += n
		c.Col = b.goal
		vertical = true
		if last := len(b.lines) - 1; c.Line > last { // au-delà : fin du texte
			c, vertical = Pos{last, len(b.lines[last])}, false
		}
	case LineStart:
		indent := indentWidth(b.lines[c.Line])
		if c.Col != indent {
			c.Col = indent
		} else {
			c.Col = 0
		}
	case LineEnd:
		c.Col = len(b.lines[c.Line])
	case WordLeft:
		c = b.wordLeft(c)
	case WordRight:
		c = b.wordRight(c)
	case DocStart:
		c = Pos{}
	case DocEnd:
		last := len(b.lines) - 1
		c = Pos{last, len(b.lines[last])}
	}
	b.cursor = b.clamp(c)
	if !vertical {
		b.goal = b.cursor.Col
	}
}

// SetCursor place le curseur (clic de souris).
func (b *Buffer) SetCursor(p Pos, extend bool) {
	if extend && b.anchor == nil {
		a := b.cursor
		b.anchor = &a
	} else if !extend {
		b.anchor = nil
	}
	b.cursor = b.clamp(p)
	b.goal = b.cursor.Col
}

func isWord(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func (b *Buffer) wordLeft(c Pos) Pos {
	if c.Col == 0 {
		if c.Line == 0 {
			return c
		}
		return Pos{c.Line - 1, len(b.lines[c.Line-1])}
	}
	l := b.lines[c.Line]
	i := c.Col
	for i > 0 && !isWord(l[i-1]) {
		i--
	}
	for i > 0 && isWord(l[i-1]) {
		i--
	}
	return Pos{c.Line, i}
}

func (b *Buffer) wordRight(c Pos) Pos {
	l := b.lines[c.Line]
	if c.Col >= len(l) {
		if c.Line == len(b.lines)-1 {
			return c
		}
		return Pos{c.Line + 1, 0}
	}
	i := c.Col
	for i < len(l) && !isWord(l[i]) {
		i++
	}
	for i < len(l) && isWord(l[i]) {
		i++
	}
	return Pos{c.Line, i}
}

func indentWidth(l []rune) int {
	i := 0
	for i < len(l) && (l[i] == ' ' || l[i] == '\t') {
		i++
	}
	return i
}

// ─── Modifications ───────────────────────────────────────────────────────────

// replace remplace le texte entre s et e (ordonnées) par text, sans
// toucher à l'historique ; elle renvoie la position de fin du texte inséré.
func (b *Buffer) replace(s, e Pos, text string) Pos {
	head := append([]rune{}, b.lines[s.Line][:s.Col]...)
	tail := append([]rune{}, b.lines[e.Line][e.Col:]...)
	parts := strings.Split(text, "\n")
	repl := make([][]rune, len(parts))
	for i, p := range parts {
		repl[i] = []rune(p)
	}
	end := Pos{s.Line + len(repl) - 1, len(repl[len(repl)-1])}
	if len(repl) == 1 {
		end.Col += len(head)
	}
	repl[0] = append(head, repl[0]...)
	repl[len(repl)-1] = append(repl[len(repl)-1], tail...)

	lines := make([][]rune, 0, len(b.lines)-(e.Line-s.Line)+len(repl)-1)
	lines = append(lines, b.lines[:s.Line]...)
	lines = append(lines, repl...)
	lines = append(lines, b.lines[e.Line+1:]...)
	b.lines = lines
	b.version++
	return end
}

// apply effectue une modification et l'ajoute à l'historique, en la
// regroupant avec la précédente quand il s'agit d'une frappe continue.
func (b *Buffer) apply(s, e Pos, text string) {
	removed := b.text(s, e)
	before := b.cursor
	end := b.replace(s, e, text)
	b.cursor, b.anchor = end, nil
	b.goal = end.Col
	ed := edit{at: s, removed: removed, inserted: text, before: before, after: end, when: b.now()}
	b.redo = nil

	// Regrouper la saisie de caractères (hors retours à la ligne) et les
	// effacements successifs, tant qu'il n'y a pas de pause de plus d'une
	// seconde — mais jamais dans la modification enregistrée.
	if n := len(b.undo); n > 0 && b.undo[n-1].id != b.saved {
		last := &b.undo[n-1]
		recent := ed.when.Sub(last.when) < time.Second
		typing := last.removed == "" && removed == "" && !strings.Contains(text, "\n") &&
			!strings.Contains(last.inserted, "\n") && last.after == s && len([]rune(text)) == 1
		erasable := last.inserted == "" && text == "" && removed != "" &&
			!strings.Contains(removed, "\n") && !strings.Contains(last.removed, "\n")
		backward := erasable && e == last.at // retour arrière
		forward := erasable && s == last.at  // Suppr
		if recent && typing {
			last.inserted += text
			last.after, last.when = end, ed.when
			return
		}
		if recent && backward {
			last.at = s
			last.removed = removed + last.removed
			last.after, last.when = end, ed.when
			return
		}
		if recent && forward {
			last.removed += removed
			last.after, last.when = end, ed.when
			return
		}
	}
	b.nextID++
	ed.id = b.nextID
	b.undo = append(b.undo, ed)
}

// Insert insère du texte au curseur (en remplaçant la sélection).
func (b *Buffer) Insert(text string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	s, e, ok := b.Selection()
	if !ok {
		s, e = b.cursor, b.cursor
	}
	b.apply(s, e, text)
}

// Newline insère un retour à la ligne en reprenant l'indentation de la
// ligne courante, avec un niveau de plus après « { », « ( » ou « [ ».
func (b *Buffer) Newline() {
	l := b.lines[b.cursor.Line]
	indent := string(l[:indentWidth(l)])
	before := strings.TrimRight(string(l[:min(b.cursor.Col, len(l))]), " \t")
	if strings.HasSuffix(before, "{") || strings.HasSuffix(before, "(") || strings.HasSuffix(before, "[") {
		indent += "\t"
	}
	b.Insert("\n" + indent)
}

// Backspace efface la sélection ou le caractère avant le curseur.
func (b *Buffer) Backspace() {
	if s, e, ok := b.Selection(); ok {
		b.apply(s, e, "")
		return
	}
	c := b.cursor
	if c == (Pos{}) {
		return
	}
	prev := c
	if c.Col > 0 {
		prev.Col--
	} else {
		prev = Pos{c.Line - 1, len(b.lines[c.Line-1])}
	}
	b.apply(prev, c, "")
}

// Delete efface la sélection ou le caractère après le curseur.
func (b *Buffer) Delete() {
	if s, e, ok := b.Selection(); ok {
		b.apply(s, e, "")
		return
	}
	c := b.cursor
	next := c
	switch {
	case c.Col < len(b.lines[c.Line]):
		next.Col++
	case c.Line < len(b.lines)-1:
		next = Pos{c.Line + 1, 0}
	default:
		return
	}
	b.apply(c, next, "")
}

// CutLine coupe la sélection, ou la ligne entière sans sélection ; elle
// renvoie le texte coupé.
func (b *Buffer) CutLine() string {
	if s, e, ok := b.Selection(); ok {
		t := b.text(s, e)
		b.apply(s, e, "")
		return t
	}
	l := b.cursor.Line
	s, e := Pos{l, 0}, Pos{l + 1, 0}
	text := string(b.lines[l]) + "\n"
	if l == len(b.lines)-1 {
		e = Pos{l, len(b.lines[l])}
		text = string(b.lines[l])
		if l > 0 {
			s = Pos{l - 1, len(b.lines[l-1])}
			text = "\n" + text
		}
	}
	b.apply(s, e, "")
	b.cursor = b.clamp(Pos{b.cursor.Line, indentWidth(b.lines[b.cursor.Line])})
	return text
}

// CopyText renvoie la sélection, ou la ligne entière sans sélection.
func (b *Buffer) CopyText() string {
	if t := b.SelectedText(); t != "" {
		return t
	}
	return string(b.lines[b.cursor.Line]) + "\n"
}

// SetText remplace tout le texte (formatage…), en une seule étape
// d'annulation, en gardant le curseur au plus près.
func (b *Buffer) SetText(text string) {
	if text == b.String() {
		return
	}
	cur := b.cursor
	last := len(b.lines) - 1
	b.apply(Pos{}, Pos{last, len(b.lines[last])}, strings.ReplaceAll(text, "\r\n", "\n"))
	b.cursor = b.clamp(cur)
	b.goal = b.cursor.Col
}

// ─── Opérations sur des lignes entières ──────────────────────────────────────

// lineRange renvoie les lignes touchées par la sélection, ou la ligne du
// curseur. Une sélection qui s'arrête en colonne 0 n'inclut pas sa dernière
// ligne.
func (b *Buffer) lineRange() (first, last int) {
	s, e, ok := b.Selection()
	if !ok {
		return b.cursor.Line, b.cursor.Line
	}
	last = e.Line
	if e.Col == 0 && e.Line > s.Line {
		last--
	}
	return s.Line, last
}

// mapLines transforme les lignes touchées en une seule étape d'annulation.
// Avec une sélection, les lignes transformées restent sélectionnées ; sinon
// le curseur suit le texte de sa ligne.
func (b *Buffer) mapLines(f func(l []rune, multi bool) []rune) {
	first, last := b.lineRange()
	_, _, hadSel := b.Selection()
	cur, oldLen := b.cursor, len(b.lines[b.cursor.Line])
	parts := make([]string, 0, last-first+1)
	changed := false
	for l := first; l <= last; l++ {
		nl := string(f(b.lines[l], last > first))
		changed = changed || nl != string(b.lines[l])
		parts = append(parts, nl)
	}
	if !changed {
		return
	}
	b.apply(Pos{first, 0}, Pos{last, len(b.lines[last])}, strings.Join(parts, "\n"))
	if hadSel {
		b.anchor = &Pos{first, 0}
		b.cursor = Pos{last, len(b.lines[last])}
	} else {
		b.cursor = b.clamp(Pos{cur.Line, max(0, cur.Col+len(b.lines[cur.Line])-oldLen)})
	}
	b.goal = b.cursor.Col
}

// Indent ajoute une tabulation en tête des lignes touchées (les lignes vides
// d'un bloc sont laissées telles quelles).
func (b *Buffer) Indent() {
	b.mapLines(func(l []rune, multi bool) []rune {
		if multi && len(l) == 0 {
			return l
		}
		return append([]rune{'\t'}, l...)
	})
}

// Dedent retire un niveau d'indentation (une tabulation ou jusqu'à quatre
// espaces) des lignes touchées.
func (b *Buffer) Dedent() {
	b.mapLines(func(l []rune, _ bool) []rune {
		if len(l) > 0 && l[0] == '\t' {
			return l[1:]
		}
		n := 0
		for n < 4 && n < len(l) && l[n] == ' ' {
			n++
		}
		return l[n:]
	})
}

// ToggleComment commente les lignes touchées avec « // », ou les décommente
// si elles le sont toutes déjà. Les lignes blanches sont ignorées.
func (b *Buffer) ToggleComment() {
	first, last := b.lineRange()
	all := true
	for l := first; l <= last; l++ {
		line := b.lines[l]
		rest := string(line[indentWidth(line):])
		if rest != "" && !strings.HasPrefix(rest, "//") {
			all = false
			break
		}
	}
	b.mapLines(func(l []rune, _ bool) []rune {
		i := indentWidth(l)
		rest := string(l[i:])
		switch {
		case rest == "":
			return l
		case all:
			rest = strings.TrimPrefix(rest, "//")
			rest = strings.TrimPrefix(rest, " ")
		default:
			rest = "// " + rest
		}
		return []rune(string(l[:i]) + rest)
	})
}

// ─── Annuler / rétablir ──────────────────────────────────────────────────────

// endOf renvoie la fin d'un texte inséré à partir de at.
func endOf(at Pos, text string) Pos {
	parts := strings.Split(text, "\n")
	if len(parts) == 1 {
		return Pos{at.Line, at.Col + len([]rune(text))}
	}
	return Pos{at.Line + len(parts) - 1, len([]rune(parts[len(parts)-1]))}
}

// Undo annule la dernière modification ; il renvoie false s'il n'y en a pas.
func (b *Buffer) Undo() bool {
	n := len(b.undo)
	if n == 0 {
		return false
	}
	ed := b.undo[n-1]
	b.undo = b.undo[:n-1]
	b.replace(ed.at, endOf(ed.at, ed.inserted), ed.removed)
	b.cursor, b.anchor = b.clamp(ed.before), nil
	b.goal = b.cursor.Col
	b.redo = append(b.redo, ed)
	return true
}

// Redo rétablit la dernière modification annulée.
func (b *Buffer) Redo() bool {
	n := len(b.redo)
	if n == 0 {
		return false
	}
	ed := b.redo[n-1]
	b.redo = b.redo[:n-1]
	b.replace(ed.at, endOf(ed.at, ed.removed), ed.inserted)
	b.cursor, b.anchor = b.clamp(ed.after), nil
	b.goal = b.cursor.Col
	ed.when = time.Time{} // ne pas regrouper avec la frappe suivante
	b.undo = append(b.undo, ed)
	return true
}

// ─── Recherche ───────────────────────────────────────────────────────────────

// Find cherche query après le curseur (en revenant au début si besoin) et
// sélectionne la première occurrence ; la casse est ignorée.
func (b *Buffer) Find(query string, forward bool) bool {
	if query == "" {
		return false
	}
	q := foldRunes([]rune(query))
	n := len(b.lines)
	start := b.cursor
	if s, e, ok := b.Selection(); ok {
		start = e
		if !forward {
			start = s
		}
	}
	match := func(l []rune, col int) bool {
		if col < 0 || col+len(q) > len(l) {
			return false
		}
		for i, r := range q {
			if unicode.ToLower(l[col+i]) != r {
				return false
			}
		}
		return true
	}
	for step := 0; step <= n; step++ {
		var li int
		if forward {
			li = (start.Line + step) % n
		} else {
			li = ((start.Line-step)%n + n) % n
		}
		l := b.lines[li]
		if forward {
			from := 0
			if step == 0 {
				from = start.Col
			}
			for col := from; col+len(q) <= len(l); col++ {
				if match(l, col) {
					b.selectRange(Pos{li, col}, Pos{li, col + len(q)})
					return true
				}
			}
		} else {
			to := len(l) - len(q)
			if step == 0 {
				to = start.Col - len(q)
			}
			for col := to; col >= 0; col-- {
				if match(l, col) {
					b.selectRange(Pos{li, col}, Pos{li, col + len(q)})
					return true
				}
			}
		}
	}
	return false
}

// foldRunes met chaque rune en minuscule, une à une : contrairement à
// strings.ToLower, le nombre de runes est conservé (ex. « İ »), ce qui
// garde les colonnes de la sélection justes.
func foldRunes(rs []rune) []rune {
	out := make([]rune, len(rs))
	for i, r := range rs {
		out[i] = unicode.ToLower(r)
	}
	return out
}

func (b *Buffer) selectRange(s, e Pos) {
	b.anchor = &s
	b.cursor = e
	b.goal = e.Col
}
