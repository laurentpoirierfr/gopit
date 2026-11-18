package textbuf

import (
	"testing"
	"time"
)

// clock fournit une horloge contrôlée (regroupement des annulations).
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func buf(text string) (*Buffer, *clock) {
	b := New(text)
	c := &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	b.now = c.now
	return b, c
}

func typeText(b *Buffer, s string) {
	for _, r := range s {
		if r == '\n' {
			b.Newline()
		} else {
			b.Insert(string(r))
		}
	}
}

func TestInsertAndUndoGrouping(t *testing.T) {
	b, c := buf("")
	typeText(b, "func f()")
	if b.String() != "func f()" || b.Cursor() != (Pos{0, 8}) || !b.Dirty() {
		t.Fatalf("saisie : %q %v", b.String(), b.Cursor())
	}
	c.t = c.t.Add(2 * time.Second) // pause : nouveau groupe
	typeText(b, " {")
	b.Undo()
	if b.String() != "func f()" {
		t.Errorf("une annulation doit retirer « {» d'un coup : %q", b.String())
	}
	b.Undo()
	if b.String() != "" || b.Dirty() {
		t.Errorf("deuxième annulation : %q dirty=%v", b.String(), b.Dirty())
	}
	b.Redo()
	b.Redo()
	if b.String() != "func f() {" || b.Cursor() != (Pos{0, 10}) {
		t.Errorf("rétablir : %q %v", b.String(), b.Cursor())
	}
	if b.Redo() {
		t.Error("plus rien à rétablir")
	}
}

func TestNewlineIndent(t *testing.T) {
	b, _ := buf("\tif x {")
	b.MoveCursor(LineEnd, 1, false)
	b.Newline()
	typeText(b, "y()")
	b.Newline()
	if b.String() != "\tif x {\n\t\ty()\n\t\t" || b.Cursor() != (Pos{2, 2}) {
		t.Errorf("indentation : %q %v", b.String(), b.Cursor())
	}
}

func TestBackspaceDeleteJoinLines(t *testing.T) {
	b, _ := buf("ab\ncd")
	b.SetCursor(Pos{1, 0}, false)
	b.Backspace()
	if b.String() != "abcd" || b.Cursor() != (Pos{0, 2}) {
		t.Errorf("retour arrière en début de ligne : %q %v", b.String(), b.Cursor())
	}
	b.MoveCursor(LineEnd, 1, false)
	b.Delete() // fin du texte : rien
	b.SetCursor(Pos{0, 0}, false)
	b.Backspace() // début du texte : rien
	if b.String() != "abcd" {
		t.Errorf("bornes : %q", b.String())
	}
	b.Undo()
	if b.String() != "ab\ncd" {
		t.Errorf("annulation de la jonction : %q", b.String())
	}
}

func TestSelectionReplaceUnicode(t *testing.T) {
	b, _ := buf("déjà vu\nçà")
	b.SetCursor(Pos{0, 1}, false)
	b.MoveCursor(Right, 1, true)
	b.MoveCursor(Right, 1, true)
	if b.SelectedText() != "éj" {
		t.Fatalf("sélection : %q", b.SelectedText())
	}
	b.Insert("E")
	if b.String() != "dEà vu\nçà" {
		t.Errorf("remplacement : %q", b.String())
	}
	// Sélection sur plusieurs lignes puis copie/coupe.
	b.SetCursor(Pos{0, 3}, false)
	b.SetCursor(Pos{1, 1}, true)
	if got := b.CopyText(); got != " vu\nç" {
		t.Errorf("copie : %q", got)
	}
	if got := b.CutLine(); got != " vu\nç" || b.String() != "dEàà" {
		t.Errorf("coupe : %q → %q", got, b.String())
	}
	// Sans sélection : la ligne entière.
	b2, _ := buf("un\ndeux\ntrois")
	b2.SetCursor(Pos{1, 2}, false)
	if got := b2.CutLine(); got != "deux\n" || b2.String() != "un\ntrois" {
		t.Errorf("coupe de ligne : %q → %q", got, b2.String())
	}
}

func TestMovesAndWords(t *testing.T) {
	b, _ := buf("\tfoo.Bar(baz)\nx")
	b.MoveCursor(LineEnd, 1, false)
	b.MoveCursor(WordLeft, 1, false)
	if b.Cursor() != (Pos{0, 9}) {
		t.Errorf("mot précédent : %v", b.Cursor())
	}
	b.MoveCursor(WordLeft, 1, false)
	if b.Cursor() != (Pos{0, 5}) {
		t.Errorf("mot précédent (2) : %v", b.Cursor())
	}
	b.MoveCursor(LineStart, 1, false)
	if b.Cursor() != (Pos{0, 1}) {
		t.Errorf("début de ligne : après l'indentation attendu, %v", b.Cursor())
	}
	b.MoveCursor(LineStart, 1, false)
	if b.Cursor() != (Pos{0, 0}) {
		t.Errorf("début de ligne (2) : %v", b.Cursor())
	}
	// Colonne visée conservée en passant par une ligne plus courte.
	b3, _ := buf("abcdef\nx\nabcdef")
	b3.SetCursor(Pos{0, 5}, false)
	b3.MoveCursor(Down, 1, false)
	b3.MoveCursor(Down, 1, false)
	if b3.Cursor() != (Pos{2, 5}) {
		t.Errorf("colonne visée : %v", b3.Cursor())
	}
	b3.MoveCursor(Down, 10, false)
	b3.MoveCursor(DocStart, 1, true)
	if b3.SelectedText() != "abcdef\nx\nabcdef" {
		t.Errorf("sélection jusqu'au début : %q", b3.SelectedText())
	}
}

func TestFindAndSetText(t *testing.T) {
	b, _ := buf("Alpha beta\ngamma ALPHA")
	if !b.Find("alpha", true) || b.Cursor() != (Pos{0, 5}) {
		t.Fatalf("première occurrence : %v", b.Cursor())
	}
	if !b.Find("alpha", true) || b.Cursor() != (Pos{1, 11}) {
		t.Errorf("occurrence suivante : %v", b.Cursor())
	}
	if !b.Find("alpha", true) || b.Cursor() != (Pos{0, 5}) {
		t.Errorf("retour au début : %v", b.Cursor())
	}
	if b.Find("absent", true) {
		t.Error("texte absent")
	}
	b.SetText("tout\nnouveau")
	if b.String() != "tout\nnouveau" || !b.Undo() || b.String() != "Alpha beta\ngamma ALPHA" {
		t.Errorf("SetText en une étape d'annulation : %q", b.String())
	}
	b.MarkSaved()
	if b.Dirty() {
		t.Error("MarkSaved")
	}
}

func TestDirtyAfterUndoThenEdit(t *testing.T) {
	b, c := buf("")
	b.Insert("a")
	b.MarkSaved()
	b.Undo()
	if !b.Dirty() {
		t.Error("après annulation : modifié")
	}
	c.t = c.t.Add(2 * time.Second)
	b.Insert("b") // même longueur d'historique, texte différent
	if !b.Dirty() {
		t.Errorf("annuler puis modifier autrement : %q doit être modifié", b.String())
	}
	b.Undo()
	b.Redo() // rétablit « b », pas « a »
	if !b.Dirty() {
		t.Error("rétablir une autre modification : modifié")
	}

	// Revenir exactement à l'état enregistré : non modifié.
	b2, c2 := buf("")
	b2.Insert("a")
	b2.MarkSaved()
	c2.t = c2.t.Add(2 * time.Second)
	b2.Insert("b")
	b2.Undo()
	if b2.Dirty() {
		t.Error("retour à l'état enregistré : non modifié")
	}
	b2.Redo()
	if !b2.Dirty() {
		t.Error("rétabli après l'enregistrement : modifié")
	}
	// La frappe ne se regroupe pas dans la modification enregistrée.
	b2.Undo()
	b2.Insert("c")
	b2.Undo()
	if b2.String() != "a" || b2.Dirty() {
		t.Errorf("regroupement avec l'état enregistré : %q dirty=%v", b2.String(), b2.Dirty())
	}
}

func TestForwardDeleteGrouping(t *testing.T) {
	b, _ := buf("abcdef")
	b.SetCursor(Pos{0, 1}, false)
	b.Delete()
	b.Delete()
	b.Delete()
	if b.String() != "aef" {
		t.Fatalf("Suppr : %q", b.String())
	}
	b.Undo()
	if b.String() != "abcdef" || b.Cursor() != (Pos{0, 1}) {
		t.Errorf("une annulation pour les Suppr successifs : %q %v", b.String(), b.Cursor())
	}
}

func TestFindRuneCount(t *testing.T) {
	// « İ » devient deux runes avec strings.ToLower : les colonnes doivent
	// rester justes.
	b, _ := buf("İx ab")
	if !b.Find("ab", true) || b.SelectedText() != "ab" {
		t.Errorf("sélection décalée : %q %v", b.SelectedText(), b.Cursor())
	}
	if !b.Find("İX", true) || b.SelectedText() != "İx" {
		t.Errorf("casse ignorée : %q", b.SelectedText())
	}
}
