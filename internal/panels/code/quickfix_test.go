package code

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/lsp"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/textbuf"
)

func TestQuickFixMenu(t *testing.T) {
	m, dir := newModel(t)
	path := filepath.Join(dir, "q.go")
	src := "package q\n\nfunc F() int {\n\tx := 1\n\treturn rand.Int()\n}\n"
	os.WriteFile(path, []byte(src), 0o600)
	m.OpenFile("q.go", 5)
	b := m.doc().buf
	b.SetCursor(textbuf.Pos{Line: 4, Col: 8}, false)
	imp := func(pkg string) lsp.CodeAction {
		return lsp.CodeAction{Title: `Add import: "` + pkg + `"`, Kind: "quickfix", Edits: []lsp.TextEdit{
			{Path: m.doc().path, Start: lsp.Pos{Line: 1}, End: lsp.Pos{Line: 1}, NewText: "\nimport \"" + pkg + "\"\n"}}}
	}
	other := lsp.CodeAction{Title: "ailleurs", Edits: []lsp.TextEdit{{Path: "/autre.go"}}}
	msg := quickMsg{path: m.doc().path, at: b.Cursor(), ver: b.Version(), actions: []lsp.CodeAction{imp("math/rand"), imp("crypto/rand"), other}}
	m.Update(msg)
	if m.compl == nil || len(m.compl.shown) != 2 {
		t.Fatalf("deux corrections (celle d'un autre fichier écartée) : %+v", m.compl)
	}
	out := ansi.Strip(m.View(80, 14))
	if !strings.Contains(out, `» Add import: "crypto/rand"`) {
		t.Fatalf("liste non affichée :\n%s", out)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd := m.Update(key("enter"))
	if !strings.Contains(b.String(), `import "crypto/rand"`) || m.compl != nil {
		t.Fatalf("crypto/rand choisi :\n%s", b.String())
	}
	if c := b.Cursor(); c.Line != 6 || b.Line(c.Line) != "\treturn rand.Int()" {
		t.Errorf("curseur : %v", c)
	}
	if f, ok := findFlash(cmd); !ok || !strings.Contains(f, "crypto/rand") {
		t.Errorf("message : %q", f)
	}
	b.Undo()

	// À la souris : un clic sur la première ligne de la liste l'applique.
	m.Update(quickMsg{path: m.doc().path, at: b.Cursor(), ver: b.Version(), actions: msg.actions})
	m.View(80, 14)
	bx, by := m.complBox[0], m.complBox[1]
	m.Update(panel.ClickMsg{X: m.treeW + bx + 3, Y: 1 + by + 1})
	if !strings.Contains(b.String(), `import "math/rand"`) {
		t.Fatalf("clic :\n%s", b.String())
	}

	// La frappe ferme la liste ; une réponse périmée est ignorée.
	m.Update(quickMsg{path: m.doc().path, at: b.Cursor(), ver: b.Version(), actions: msg.actions})
	m.Update(key("z"))
	if m.compl != nil {
		t.Error("la frappe ferme la liste")
	}
	m.Update(quickMsg{path: m.doc().path, at: b.Cursor(), ver: b.Version() - 1, actions: msg.actions})
	if m.compl != nil {
		t.Error("réponse périmée")
	}
}

func TestQuickFixGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil || testing.Short() {
		t.Skip("gopls absent, ou mode court")
	}
	m, dir := newModel(t)
	m.start = lsp.Start
	t.Cleanup(func() {
		if m.lsp != nil {
			m.lsp.Close()
		}
	})
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module m\n\ngo 1.22\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "m.go"), []byte("package m\n\nfunc F() {\n\tx := 1\n}\n"), 0o600)
	m.OpenFile("m.go", 4)
	path := m.doc().path
	pump := newPump(t, m)
	_, cmd := m.Update(tea.WindowSizeMsg{})
	pump(cmd, func() bool { return len(m.diags[path]) > 0 })
	m.doc().buf.SetCursor(textbuf.Pos{Line: 3, Col: 1}, false)
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt}) // aussitôt : gopls n'a pas fini
	pump(cmd, func() bool { return m.compl != nil })
	if got := m.compl.items[0].Label; !strings.Contains(got, "x") {
		t.Fatalf("correction : %q", got)
	}
	m.Update(key("enter"))
	if got := m.doc().buf.String(); strings.Contains(got, "x := 1") {
		t.Errorf("variable inutilisée retirée :\n%s", got)
	}
}
