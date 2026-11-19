package diagnostics

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/laurentpoirierfr/gopit/internal/diag"
	"github.com/laurentpoirierfr/gopit/internal/lsp"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/theme"
)

func TestPreview(t *testing.T) {
	x := fix{base: "a\nb\nc\nd\ne", text: "a\nb\nC1\nC2\nd\ne"}
	first, suffix := changed(x.base, x.text)
	if first != 3 || suffix != 2 {
		t.Fatalf("changed : %d %d", first, suffix)
	}
	var got []string
	for _, l := range x.preview(1, 10) {
		got = append(got, string(l.op)+l.text)
	}
	if strings.Join(got, "|") != " b|-c|+C1|+C2| d" {
		t.Errorf("aperçu : %q", got)
	}
	if p := x.preview(1, 3); len(p) != 3 || p[2].op != '…' {
		t.Errorf("aperçu limité : %v", p)
	}
	if l := newFixes(nil, fix{base: "x", text: "x"}); l != nil {
		t.Error("correction sans effet écartée")
	}
	if l := newFixes([]fix{x}, x); len(l) != 1 {
		t.Error("correction en double écartée")
	}
}

func TestSameProblem(t *testing.T) {
	d := diag.Diagnostic{Line: 10, Code: "S1003", Message: "should use strings.Contains"}
	g := lsp.Diagnostic{Start: lsp.Pos{Line: 9, Col: 3}, End: lsp.Pos{Line: 9, Col: 20}, Source: "S1003", Message: "autre texte"}
	if !sameProblem(d, g) {
		t.Error("même règle")
	}
	g.Source = "SA9003"
	if sameProblem(d, g) {
		t.Error("autre règle, autre message : un autre problème de la ligne")
	}
	g.Source, g.Start.Line, g.End.Line = "S1003", 4, 4
	if sameProblem(d, g) {
		t.Error("autre ligne")
	}
}

func TestGofmtFix(t *testing.T) {
	t.Setenv("GOPIT_GOPLS", "off")
	root := t.TempDir()
	path := filepath.Join(root, "a.go")
	os.WriteFile(path, []byte("package a\nvar   x=1\n"), 0o600)
	m := New(theme.New(), &project.Project{Root: root})
	m.results[diag.ToolGofmt] = []diag.Diagnostic{{File: path, Line: 1, Code: "gofmt", Message: "fichier mal formaté (gofmt -w pour corriger)", Severity: diag.Warning}}
	m.merge()
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyDown}) // le module est utilisé : recherche
	m.Update(run(t, cmd))
	x, n := m.currentFix()
	if n != 1 || x.text != "package a\n\nvar x = 1\n" {
		t.Fatalf("correction gofmt : %d %+v", n, x)
	}
	if !strings.Contains(m.View(100, 30), "f appliquer") {
		t.Error("bouton appliquer absent")
	}
	_, cmd = m.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	a, ok := cmd().(panel.ApplyMsg)
	if !ok || a.Path != path || a.Text != x.text || a.Base != "package a\nvar   x=1\n" {
		t.Errorf("f : %#v", cmd())
	}
}

// run exécute cmd et renvoie le message fixesMsg qu'il contient.
func run(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("aucune commande")
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		for _, c := range b {
			if c == nil {
				continue
			}
			if m, ok := c().(fixesMsg); ok {
				return m
			}
		}
		t.Fatal("pas de recherche de correction")
	}
	return msg
}

func TestGoplsFix(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil || testing.Short() {
		t.Skip("gopls absent, ou mode court")
	}
	t.Setenv("GOPIT_GOPLS", "")
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module m\n\ngo 1.22\n"), 0o600)
	path := filepath.Join(root, "m.go")
	src := "// Package m.\npackage m\n\nimport \"strings\"\n\n// F dit si s contient « a ».\nfunc F(s string) bool {\n\treturn strings.Index(s, \"a\") != -1\n}\n"
	os.WriteFile(path, []byte(src), 0o600)

	m := New(theme.New(), &project.Project{Root: root})
	m.results[diag.ToolStaticcheck] = []diag.Diagnostic{{File: path, Line: 8, Col: 9, Code: "S1003",
		Message: `should use strings.Contains(s, "a") instead`, Severity: diag.Warning}}
	m.merge()
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	msg := run(t, cmd)
	m.Update(msg)
	x, n := m.currentFix()
	if x == nil {
		t.Fatalf("aucune correction : %+v", msg)
	}
	if !strings.Contains(x.text, `return strings.Contains(s, "a")`) || x.line != 8 {
		t.Errorf("correction %d : %s (ligne %d)\n%s", n, x.title, x.line, x.text)
	}
	// Une autre règle sur la même ligne : pas de correction proposée.
	m.results[diag.ToolStaticcheck][0].Code, m.results[diag.ToolStaticcheck][0].Message = "SA0000", "autre chose"
	m.fixes = map[string]*fixState{}
	m.merge()
	_, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(run(t, cmd))
	if x, _ := m.currentFix(); x != nil {
		t.Errorf("correction d'un autre problème : %s", x.title)
	}
}
