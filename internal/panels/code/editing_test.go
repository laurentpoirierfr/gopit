package code

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/textbuf"
)

// kp construit une touche à partir de son nom (« ctrl+shift+k », « alt+up »).
func kp(s string) tea.KeyPressMsg {
	parts := strings.Split(s, "+")
	var k tea.KeyPressMsg
	for _, p := range parts[:len(parts)-1] {
		switch p {
		case "ctrl":
			k.Mod |= tea.ModCtrl
		case "alt":
			k.Mod |= tea.ModAlt
		case "shift":
			k.Mod |= tea.ModShift
		}
	}
	codes := map[string]rune{"up": tea.KeyUp, "down": tea.KeyDown, "enter": tea.KeyEnter,
		"backspace": tea.KeyBackspace, "delete": tea.KeyDelete, "left": tea.KeyLeft, "right": tea.KeyRight}
	last := parts[len(parts)-1]
	if c, ok := codes[last]; ok {
		k.Code = c
	} else {
		k.Code = []rune(last)[0]
		if k.Mod == 0 {
			k.Text = last
		}
	}
	return k
}

// openText ouvre un fichier contenant text, en édition, curseur en at.
func openText(t *testing.T, name, text string, at textbuf.Pos) *Model {
	t.Helper()
	m, dir := newModel(t)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	m.OpenFile(name, 1)
	m.doc().buf.SetCursor(at, false)
	return m
}

func press(m *Model, keys ...string) {
	for _, s := range keys {
		m.Update(kp(s))
	}
}

func TestLineShortcuts(t *testing.T) {
	m := openText(t, "a.txt", "un\ndeux\ntrois", textbuf.Pos{Line: 1, Col: 1})
	b := m.doc().buf
	press(m, "alt+up")
	if b.String() != "deux\nun\ntrois" {
		t.Fatalf("Alt+↑ : %q", b.String())
	}
	press(m, "alt+shift+down")
	if b.String() != "deux\ndeux\nun\ntrois" || b.Cursor().Line != 1 {
		t.Fatalf("Maj+Alt+↓ : %q %v", b.String(), b.Cursor())
	}
	press(m, "ctrl+shift+k")
	if b.String() != "deux\nun\ntrois" {
		t.Fatalf("Ctrl+Maj+K : %q", b.String())
	}
	press(m, "ctrl+l")
	if b.SelectedText() != "un\n" {
		t.Fatalf("Ctrl+L : %q", b.SelectedText())
	}
	press(m, "ctrl+enter")
	if b.String() != "deux\nun\n\ntrois" || b.Cursor() != (textbuf.Pos{Line: 2}) { // sous la ligne sélectionnée
		t.Fatalf("Ctrl+Entrée : %q %v", b.String(), b.Cursor())
	}
	press(m, "x", "y", "ctrl+backspace")
	if b.Line(2) != "" {
		t.Errorf("Ctrl+Retour arrière : %q", b.Line(2))
	}
}

func TestCopyPasteWholeLine(t *testing.T) {
	m := openText(t, "a.go", "package a\n\nfunc f() {\n\tx := 1\n\ty := 2\n}\n", textbuf.Pos{Line: 3, Col: 3})
	b := m.doc().buf
	press(m, "ctrl+c") // sans sélection : la ligne entière
	b.SetCursor(textbuf.Pos{Line: 4, Col: 2}, false)
	m.Update(panel.EditMsg{Action: panel.ActPaste})
	if b.Line(4) != "\tx := 1" || b.Line(5) != "\ty := 2" || b.Cursor() != (textbuf.Pos{Line: 5, Col: 2}) {
		t.Fatalf("collée au-dessus de la ligne : %q %v", b.String(), b.Cursor())
	}
	// Collage par le terminal du même texte : même comportement.
	m.Update(tea.PasteMsg{Content: "\tx := 1\n"})
	if b.Line(5) != "\tx := 1" || b.Line(6) != "\ty := 2" {
		t.Fatalf("collage du terminal : %q", b.String())
	}
	// Une sélection copiée se colle au curseur.
	b.SetCursor(textbuf.Pos{Line: 4, Col: 1}, false)
	b.SetCursor(textbuf.Pos{Line: 4, Col: 2}, true)
	press(m, "ctrl+c")
	b.SetCursor(textbuf.Pos{Line: 0, Col: 0}, false)
	m.Update(panel.EditMsg{Action: panel.ActPaste})
	if b.Line(0) != "xpackage a" {
		t.Errorf("sélection collée au curseur : %q", b.Line(0))
	}
	// Ligne coupée (la dernière) : recollée entière.
	b.SetCursor(textbuf.Pos{Line: b.LineCount() - 1}, false)
	press(m, "ctrl+x")
	if m.clip != "\n" || !m.clipLines {
		t.Errorf("dernière ligne (vide) coupée : %q %v", m.clip, m.clipLines)
	}
}

func TestAutoPair(t *testing.T) {
	m := openText(t, "a.go", "package a\n\n", textbuf.Pos{Line: 2})
	b := m.doc().buf
	press(m, "f", "(", "x", ")")
	if b.Line(2) != "f(x)" || b.Cursor().Col != 4 {
		t.Fatalf("fermant sauté : %q %v", b.Line(2), b.Cursor())
	}
	press(m, "(", "backspace")
	if b.Line(2) != "f(x)" {
		t.Fatalf("paire vide effacée : %q", b.Line(2))
	}
	press(m, " ", "{", "enter")
	if b.String() != "package a\n\nf(x) {\n\t\n}" {
		t.Fatalf("Entrée entre accolades : %q", b.String())
	}
	press(m, "\"", "a", "\"")
	if b.Line(3) != "\t\"a\"" {
		t.Fatalf("guillemets : %q", b.Line(3))
	}
	press(m, " ", "l", "'", "a")
	if b.Line(3) != "\t\"a\" l'a" {
		t.Fatalf("apostrophe après un mot : %q", b.Line(3))
	}
	// Entourer la sélection.
	b.SetCursor(textbuf.Pos{Line: 2, Col: 2}, false)
	b.SetCursor(textbuf.Pos{Line: 2, Col: 3}, true)
	press(m, "[")
	if b.Line(2) != "f([x]) {" || b.SelectedText() != "x" {
		t.Fatalf("entourer : %q %q", b.Line(2), b.SelectedText())
	}
	// Désactivable, et mémorisé.
	m.Update(panel.EditMsg{Action: actAutoPair})
	if m.autoPair || m.State()["pairs"] != "0" {
		t.Fatal("désactivation")
	}
	b.SetCursor(textbuf.Pos{Line: 4, Col: 1}, false)
	press(m, "(")
	if b.Line(4) != "}(" {
		t.Errorf("sans fermeture automatique : %q", b.Line(4))
	}
}

func TestMatchingBracket(t *testing.T) {
	m := openText(t, "a.go", "package a\n\nfunc f() {\n\ts := \"}\"\n}\n", textbuf.Pos{Line: 2, Col: 9})
	m.editing = true
	press(m, "ctrl+m")
	if c := m.doc().buf.Cursor(); c != (textbuf.Pos{Line: 4}) {
		t.Fatalf("Ctrl+M (accolade de la chaîne ignorée) : %v", c)
	}
	marks := m.bracketMarks(m.doc())
	if len(marks[2]) != 1 || marks[2][0] != 9 || len(marks[4]) != 1 {
		t.Errorf("surlignage : %v", marks)
	}
	m.View(80, 20)
}

func TestMultiClick(t *testing.T) {
	m := openText(t, "a.txt", "alpha beta\ngamma", textbuf.Pos{})
	m.View(80, 20)
	x := m.treeW + m.gutterW + 7 // dans « beta »
	click := panel.ClickMsg{X: x, Y: 1}
	m.Update(click)
	m.Update(click)
	if got := m.doc().buf.SelectedText(); got != "beta" {
		t.Fatalf("double clic : %q", got)
	}
	m.Update(click)
	if got := m.doc().buf.SelectedText(); got != "alpha beta\n" {
		t.Fatalf("triple clic : %q", got)
	}
}

func TestApplyText(t *testing.T) {
	m, dir := newModel(t)
	path := filepath.Join(dir, "a.go")
	base := "package a\n\nvar x int = 1\n"
	if err := os.WriteFile(path, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}
	fixed := "package a\n\nvar x = 1\n"
	cmd := m.ApplyText(panel.ApplyMsg{Path: path, Base: base, Text: fixed, Line: 3, Title: "Remove redundant type"})
	b := m.doc().buf
	if b.String() != fixed || !b.Dirty() || b.Cursor().Line != 2 || !m.editing {
		t.Fatalf("appliquée : %q dirty=%v %v", b.String(), b.Dirty(), b.Cursor())
	}
	if f, ok := cmd().(panel.FlashMsg); !ok || !strings.Contains(f.Text, "Remove redundant type") {
		t.Errorf("message : %#v", cmd())
	}
	b.Undo()
	if b.String() != base {
		t.Errorf("annulée en une fois : %q", b.String())
	}
	// Texte modifié depuis l'analyse : rien n'est fait.
	press(m, "x")
	before := b.String()
	cmd = m.ApplyText(panel.ApplyMsg{Path: path, Base: base, Text: fixed, Line: 3})
	if b.String() != before {
		t.Error("correction appliquée sur un texte modifié")
	}
	if f, ok := cmd().(panel.FlashMsg); !ok || !f.Error {
		t.Errorf("erreur attendue : %#v", cmd())
	}
}
