package textbuf

import "testing"

func TestMoveLines(t *testing.T) {
	b, _ := buf("a\nb\nc")
	b.SetCursor(Pos{1, 1}, false)
	if !b.MoveLines(-1) || b.String() != "b\na\nc" || b.Cursor() != (Pos{0, 1}) {
		t.Fatalf("vers le haut : %q %v", b.String(), b.Cursor())
	}
	if b.MoveLines(-1) {
		t.Error("première ligne : rien à faire")
	}
	b.MoveLines(1)
	b.MoveLines(1)
	if b.String() != "a\nc\nb" || b.Cursor() != (Pos{2, 1}) {
		t.Fatalf("vers le bas : %q %v", b.String(), b.Cursor())
	}
	b.Undo()
	if b.String() != "a\nb\nc" {
		t.Errorf("une annulation par déplacement : %q", b.String())
	}

	// Une sélection de deux lignes reste sélectionnée.
	b, _ = buf("1\n2\n3\n4")
	b.SetCursor(Pos{1, 0}, false)
	b.SetCursor(Pos{2, 1}, true)
	b.MoveLines(1)
	if b.String() != "1\n4\n2\n3" || b.SelectedText() != "2\n3" {
		t.Errorf("bloc : %q, sélection %q", b.String(), b.SelectedText())
	}
}

func TestDuplicateAndDeleteLines(t *testing.T) {
	b, _ := buf("x := 1\ny")
	b.SetCursor(Pos{0, 2}, false)
	b.DuplicateLines(1)
	if b.String() != "x := 1\nx := 1\ny" || b.Cursor() != (Pos{1, 2}) {
		t.Fatalf("dupliquer vers le bas : %q %v", b.String(), b.Cursor())
	}
	b.DuplicateLines(0)
	if b.LineCount() != 4 || b.Cursor() != (Pos{1, 2}) {
		t.Fatalf("dupliquer vers le haut : %q %v", b.String(), b.Cursor())
	}
	b.DeleteLines()
	if b.String() != "x := 1\nx := 1\ny" || b.Cursor() != (Pos{1, 2}) {
		t.Fatalf("supprimer : %q %v", b.String(), b.Cursor())
	}
	b.SetCursor(Pos{2, 0}, false)
	b.DeleteLines()
	if b.String() != "x := 1\nx := 1" || b.Cursor().Line != 1 {
		t.Fatalf("supprimer la dernière : %q %v", b.String(), b.Cursor())
	}
	b, _ = buf("seule")
	b.DeleteLines()
	if b.String() != "" {
		t.Errorf("supprimer la seule ligne : %q", b.String())
	}
}

func TestSelectLineAndWord(t *testing.T) {
	b, _ := buf("un deux\ntrois\nquatre")
	b.SetCursor(Pos{0, 4}, false)
	b.SelectLine()
	if b.SelectedText() != "un deux\n" {
		t.Fatalf("ligne : %q", b.SelectedText())
	}
	b.SelectLine()
	b.SelectLine()
	if b.SelectedText() != "un deux\ntrois\nquatre" {
		t.Fatalf("lignes suivantes : %q", b.SelectedText())
	}
	b.SelectWord(Pos{0, 4})
	if b.SelectedText() != "deux" {
		t.Errorf("mot : %q", b.SelectedText())
	}
	b.SelectWord(Pos{0, 7})
	if b.SelectedText() != "deux" {
		t.Errorf("juste après le mot : %q", b.SelectedText())
	}
}

func TestInsertLines(t *testing.T) {
	b, _ := buf("\tif x {\n\t}")
	b.SetCursor(Pos{0, 2}, false)
	b.InsertLineBelow()
	if b.String() != "\tif x {\n\t\t\n\t}" || b.Cursor() != (Pos{1, 2}) {
		t.Fatalf("en dessous : %q %v", b.String(), b.Cursor())
	}
	b.SetCursor(Pos{0, 3}, false)
	b.InsertLineAbove()
	if b.Line(0) != "\t" || b.Cursor() != (Pos{0, 1}) {
		t.Fatalf("au-dessus : %q %v", b.String(), b.Cursor())
	}
}

func TestPasteLinesReindents(t *testing.T) {
	b, _ := buf("func f() {\n\t\treturn\n}")
	b.SetCursor(Pos{1, 4}, false)
	b.PasteLines("    a := 1\n        b := 2\n")
	want := "func f() {\n\t\ta := 1\n\t\t    b := 2\n\t\treturn\n}"
	if b.String() != want || b.Cursor() != (Pos{3, 4}) {
		t.Fatalf("collage : %q %v", b.String(), b.Cursor())
	}
}

func TestDeleteWord(t *testing.T) {
	b, _ := buf("foo.barBaz qux")
	b.SetCursor(Pos{0, 10}, false)
	b.DeleteWordLeft()
	if b.String() != "foo. qux" {
		t.Fatalf("à gauche : %q", b.String())
	}
	b.SetCursor(Pos{0, 0}, false)
	b.DeleteWordRight()
	if b.String() != ". qux" || b.Cursor() != (Pos{0, 0}) {
		t.Fatalf("à droite : %q %v", b.String(), b.Cursor())
	}
}

func TestSurroundAndPairs(t *testing.T) {
	b, _ := buf("x + y")
	b.SetCursor(Pos{0, 0}, false)
	b.SetCursor(Pos{0, 5}, true)
	b.Surround("(", ")")
	if b.String() != "(x + y)" || b.SelectedText() != "x + y" {
		t.Fatalf("entourer : %q %q", b.String(), b.SelectedText())
	}
	b, _ = buf("f")
	b.SetCursor(Pos{0, 1}, false)
	b.InsertPair("(", ")")
	if b.String() != "f()" || b.Cursor() != (Pos{0, 2}) {
		t.Fatalf("paire : %q %v", b.String(), b.Cursor())
	}
	b.DeletePair()
	if b.String() != "f" {
		t.Errorf("effacer la paire : %q", b.String())
	}
}

func TestNewlineBetweenBraces(t *testing.T) {
	b, _ := buf("\tif x {}")
	b.SetCursor(Pos{0, 7}, false)
	b.Newline()
	if b.String() != "\tif x {\n\t\t\n\t}" || b.Cursor() != (Pos{1, 2}) {
		t.Fatalf("entre accolades : %q %v", b.String(), b.Cursor())
	}
}

func TestMatchBracket(t *testing.T) {
	b, _ := buf("f(a[1], g())\n{\n\t\"(\"\n}")
	for _, c := range []struct{ at, want Pos }{
		{Pos{0, 1}, Pos{0, 11}}, {Pos{0, 11}, Pos{0, 1}}, {Pos{0, 3}, Pos{0, 5}}, {Pos{1, 0}, Pos{3, 0}},
	} {
		got, ok := b.MatchBracket(c.at, nil)
		if !ok || got != c.want {
			t.Errorf("%v : %v %v, attendu %v", c.at, got, ok, c.want)
		}
	}
	// Chaîne ignorée : la parenthèse entre guillemets ne compte pas.
	b, _ = buf("(\"(\")")
	in := func(p Pos) bool { return p.Col >= 1 && p.Col <= 3 }
	if got, ok := b.MatchBracket(Pos{0, 0}, in); !ok || got != (Pos{0, 4}) {
		t.Errorf("chaîne : %v %v", got, ok)
	}
	if p, ok := b.BracketNear(Pos{0, 5}); !ok || p != (Pos{0, 4}) {
		t.Errorf("après une fermante : %v %v", p, ok)
	}
}

func TestMapPos(t *testing.T) {
	old := "package a\n\nfunc f() {\n\tx := strings.Repeat(\"a\", 2)\n}\n"
	added := "package a\n\nimport \"strings\"\n\nfunc f() {\n\tx := strings.Repeat(\"a\", 2)\n}\n"
	if p := MapPos(old, added, Pos{3, 5}); p != (Pos{5, 5}) {
		t.Errorf("après les imports ajoutés : %v", p)
	}
	if p := MapPos(old, added, Pos{0, 3}); p != (Pos{0, 3}) {
		t.Errorf("avant : %v", p)
	}
	b, _ := buf(old)
	b.SetCursor(Pos{3, 5}, false)
	b.SetTextAt(added, MapPos(old, added, b.Cursor()))
	if b.String() != added || b.Cursor() != (Pos{5, 5}) {
		t.Fatalf("SetTextAt : %v", b.Cursor())
	}
	b.Undo()
	if b.String() != old {
		t.Error("une seule annulation")
	}
}
