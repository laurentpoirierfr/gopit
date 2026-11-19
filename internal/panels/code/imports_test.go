package code

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/laurentpoirierfr/gopit/internal/lsp"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/textbuf"
)

func TestOrganizeImportsOnSave(t *testing.T) {
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
	path := filepath.Join(dir, "m.go")
	// os importé sans servir ; strings utilisé sans import.
	os.WriteFile(path, []byte("package m\n\nimport \"os\"\n\nfunc F(s string) string {\n\treturn strings.ToUpper(s)\n}\n"), 0o600)
	m.OpenFile("m.go", 6)
	m.doc().buf.SetCursor(textbuf.Pos{Line: 5, Col: 8}, false) // sur « strings »
	pump := newPump(t, m)
	_, cmd := m.Update(tea.WindowSizeMsg{})
	pump(cmd, func() bool { return len(m.diags[m.doc().path]) > 0 }) // gopls a chargé le fichier

	_, cmd = m.Update(key("ctrl+s"))
	pump(cmd, func() bool {
		data, _ := os.ReadFile(path)
		return strings.Contains(string(data), `"strings"`)
	})
	want := "package m\n\nimport \"strings\"\n\nfunc F(s string) string {\n\treturn strings.ToUpper(s)\n}\n"
	if data, _ := os.ReadFile(path); string(data) != want {
		t.Fatalf("enregistré :\n%s", data)
	}
	if c := m.doc().buf.Cursor(); c != (textbuf.Pos{Line: 5, Col: 8}) {
		t.Errorf("le curseur suit son texte : %v", c)
	}
	m.doc().buf.Undo()
	if !strings.Contains(m.doc().buf.String(), `"os"`) {
		t.Error("imports et formatage : une seule annulation")
	}

	// Désactivé : enregistré tel quel (avec gofmt).
	m.Update(panel.EditMsg{Action: actImports})
	_, cmd = m.Update(key("ctrl+s"))
	if f, ok := findFlash(cmd); !ok || strings.Contains(f, "imports") {
		t.Errorf("enregistrement direct : %q", f)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), `"os"`) {
		t.Error("imports laissés tels quels")
	}
}

// findFlash renvoie le texte du message temporaire contenu dans cmd.
func findFlash(cmd tea.Cmd) (string, bool) {
	if cmd == nil {
		return "", false
	}
	switch msg := cmd().(type) {
	case panel.FlashMsg:
		return msg.Text, true
	case tea.BatchMsg:
		for _, c := range msg {
			if f, ok := findFlash(c); ok {
				return f, true
			}
		}
	}
	return "", false
}

func TestOnOrganized(t *testing.T) {
	m, dir := newModel(t)
	path := filepath.Join(dir, "a.go")
	src := "package a\n\nvar x = strings.ToUpper(\"a\")\n"
	os.WriteFile(path, []byte(src), 0o600)
	m.OpenFile("a.go", 3)
	b := m.doc().buf
	organized := "package a\n\nimport \"strings\"\n\nvar x = strings.ToUpper(\"a\")\n"

	// À la demande : appliqué sans enregistrer, curseur suivi.
	b.SetCursor(textbuf.Pos{Line: 2, Col: 4}, false)
	m.Update(organizedMsg{path: m.doc().path, ver: b.Version(), text: organized})
	if b.String() != organized || !b.Dirty() || b.Cursor() != (textbuf.Pos{Line: 4, Col: 4}) {
		t.Fatalf("à la demande : %q %v", b.String(), b.Cursor())
	}

	// Texte modifié pendant la demande : enregistré tel quel, sans les imports.
	b.Undo()
	ver := b.Version()
	m.Update(key("end"))
	m.Update(key("x"))
	m.Update(organizedMsg{path: m.doc().path, ver: ver, text: organized, save: true})
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "import") || !strings.HasSuffix(string(data), "x\n") {
		t.Errorf("enregistré : %q", data)
	}
}

func TestCompletionAddsImport(t *testing.T) {
	m, dir := newModel(t)
	path := filepath.Join(dir, "c.go")
	src := "package c\n\nimport \"fmt\"\n\nfunc F() {\n\tfmt.Println(strings.Rep)\n}\n"
	os.WriteFile(path, []byte(src), 0o600)
	m.OpenFile("c.go", 6)
	b := m.doc().buf
	b.SetCursor(textbuf.Pos{Line: 5, Col: 24}, false) // après « strings.Rep »
	// Réponse de gopls (telle qu'il l'envoie) : l'import transforme
	// « import "fmt" » en bloc.
	extra := []lsp.TextEdit{
		{Path: m.doc().path, Start: lsp.Pos{Line: 2, Col: 7}, End: lsp.Pos{Line: 2, Col: 7}, NewText: "(\n\t"},
		{Path: m.doc().path, Start: lsp.Pos{Line: 3}, End: lsp.Pos{Line: 3}, NewText: "\t\"strings\"\n)\n"},
	}
	items := []lsp.Item{{Label: "Repeat", Filter: "Repeat", Text: "Repeat", Kind: lsp.KindFunction,
		Detail: `func (from "strings")`, Start: lsp.Pos{Line: 5, Col: 21}, HasStart: true, Extra: extra}}
	m.Update(complMsg{path: m.doc().path, at: b.Cursor(), explicit: true, items: items})
	if m.compl == nil {
		t.Fatal("liste attendue")
	}
	_, cmd := m.Update(key("enter"))
	want := "package c\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n\nfunc F() {\n\tfmt.Println(strings.Repeat)\n}\n"
	if b.String() != want {
		t.Fatalf("texte :\n%s", b.String())
	}
	if c := b.Cursor(); c != (textbuf.Pos{Line: 8, Col: 27}) {
		t.Errorf("curseur après « Repeat » : %v", c)
	}
	if f, ok := findFlash(cmd); !ok || !strings.Contains(f, `"strings"`) {
		t.Errorf("message : %q", f)
	}
	b.Undo()
	if b.String() != src {
		t.Errorf("une seule annulation :\n%s", b.String())
	}
}

func TestCompletionAddsImportGopls(t *testing.T) {
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
	os.WriteFile(filepath.Join(dir, "m.go"), []byte("package m\n\nvar s = strings\n"), 0o600)
	m.OpenFile("m.go", 3)
	path := m.doc().path
	pump := newPump(t, m)
	_, cmd := m.Update(tea.WindowSizeMsg{})
	pump(cmd, func() bool { return len(m.diags[path]) > 0 })
	m.doc().buf.SetCursor(textbuf.Pos{Line: 2, Col: 15}, false)
	_, cmd = m.Update(key("."))
	pump(cmd, func() bool { return m.compl != nil })
	for _, r := range "ToUpp" {
		m.Update(key(string(r)))
	}
	if m.compl == nil || m.compl.items[m.compl.shown[0]].Label != "ToUpper" {
		t.Fatalf("« ToUpper » attendu : %+v", m.compl)
	}
	m.Update(key("enter"))
	if got := m.doc().buf.String(); got != "package m\n\nimport \"strings\"\n\nvar s = strings.ToUpper\n" {
		t.Errorf("après complétion :\n%s", got)
	}
}
