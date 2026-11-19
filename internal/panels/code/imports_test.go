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
