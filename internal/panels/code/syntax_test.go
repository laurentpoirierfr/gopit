package code

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestDetectLanguage(t *testing.T) {
	for _, c := range []struct{ path, first, want string }{
		{"/p/main.go", "", "Go"},
		{"/p/go.mod", "", "go.mod"},
		{"/p/go.work", "", "go.work"},
		{"/p/go.sum", "", "go.sum"},
		{"/p/README.md", "", "markdown"},
		{"/p/Makefile", "", "Makefile"},
		{"/p/Dockerfile", "", "Docker"},
		{"/p/.github/workflows/ci.yml", "", "YAML"},
		{"/p/.goreleaser.yaml", "", "YAML"},
		{"/p/config.json", "", "JSON"},
		{"/p/schema.sql", "", "SQL"},
		{"/p/api.proto", "", "Protocol Buffer"},
		{"/p/scripts/release", "#!/usr/bin/env bash", "Bash"},
		{"/p/scripts/check", "#!/bin/sh -e", "Bash"},
		{"/p/outil", "#!/usr/bin/python3", "Python"},
		{"/p/LICENSE", "MIT License", ""},
		{"/p/donnees", "SELECT * FROM t", ""}, // le contenu seul ne suffit pas
	} {
		if got := detectLanguage(c.path, c.first).name; got != c.want {
			t.Errorf("%s (%q) : %q, attendu %q", c.path, c.first, got, c.want)
		}
	}
}

// classAt renvoie la catégorie du premier caractère de word dans la ligne l.
func classAt(t *testing.T, src string, cls [][]class, l int, word string) class {
	t.Helper()
	line := strings.Split(src, "\n")[l]
	i := strings.Index(line, word)
	if i < 0 {
		t.Fatalf("%q absent de la ligne %d", word, l)
	}
	col := len([]rune(line[:i]))
	if l >= len(cls) || col >= len(cls[l]) {
		t.Fatalf("ligne %d : %d catégories, colonne %d", l, len(cls[l]), col)
	}
	return cls[l][col]
}

func TestHighlightChroma(t *testing.T) {
	for _, c := range []struct {
		path, src string
		checks    []struct {
			line int
			word string
			want class
		}
	}{
		{"ci.yml", "# Intégration\nname: CI\non:\n  push:\n    branches: [\"main\"]\n", []struct {
			line int
			word string
			want class
		}{{0, "# Int", clComment}, {1, "name", clKeyword}, {3, "push", clKeyword}, {4, `"main"`, clString}}},
		{"README.md", "# gopit\n\nLe cockpit.\n\n## Utilisation\n", []struct {
			line int
			word string
			want class
		}{{0, "# gopit", clHeading}, {4, "## Util", clHeading}, {2, "Le", clText}}},
		{"Makefile", "# cibles\nbuild: ## compile\n\tgo build $(PKG)\n", []struct {
			line int
			word string
			want class
		}{{0, "# cibles", clComment}, {1, "build", clFunc}, {2, "$(", clKeyword}}},
		{"x.diff", "--- a\n+++ b\n-retiré\n+ajouté\n", []struct {
			line int
			word string
			want class
		}{{2, "-retiré", clRemoved}, {3, "+ajouté", clAdded}}},
	} {
		lang := detectLanguage(c.path, "")
		cls := lang.color(c.src)
		// Une catégorie par rune, ligne par ligne (accents compris).
		for i, l := range strings.Split(c.src, "\n") {
			if i < len(cls) && len(cls[i]) != len([]rune(l)) {
				t.Errorf("%s ligne %d : %d catégories pour %d runes", c.path, i, len(cls[i]), len([]rune(l)))
			}
		}
		for _, ck := range c.checks {
			if got := classAt(t, c.src, cls, ck.line, ck.word); got != ck.want {
				t.Errorf("%s : %q → %d, attendu %d", c.path, ck.word, got, ck.want)
			}
		}
	}
}

func TestHighlightGoMod(t *testing.T) {
	src := "module example.com/m // commentaire\n\ngo 1.22\n\nrequire (\n\tgolang.org/x/mod v0.20.0 // indirect\n)\nreplace a => ../a\n"
	cls := highlightGoMod(src)
	for _, c := range []struct {
		line int
		word string
		want class
	}{
		{0, "module", clKeyword}, {0, "example", clText}, {0, "// comm", clComment},
		{2, "go", clKeyword}, {2, "1.22", clNumber}, {4, "require", clKeyword},
		{5, "golang", clText}, {5, "v0.20.0", clNumber}, {5, "// indirect", clComment},
		{7, "replace", clKeyword},
	} {
		if got := classAt(t, src, cls, c.line, c.word); got != c.want {
			t.Errorf("go.mod : %q → %d, attendu %d", c.word, got, c.want)
		}
	}
	sum := "golang.org/x/mod v0.20.0 h1:abc+/=\ngolang.org/x/mod v0.20.0/go.mod h1:def=\n"
	cls = highlightGoMod(sum)
	if classAt(t, sum, cls, 0, "v0.20.0") != clNumber || classAt(t, sum, cls, 0, "h1:") != clString ||
		classAt(t, sum, cls, 1, "v0.20.0/go.mod") != clNumber {
		t.Errorf("go.sum : %v", cls)
	}
}

func TestHighlightLimit(t *testing.T) {
	big := strings.Repeat("clé: valeur\n", maxHighlightLines+1)
	if cls := detectLanguage("gros.yaml", "").color(big); cls != nil {
		t.Error("fichier trop long : pas de coloration")
	}
}

func TestStatusShowsLanguage(t *testing.T) {
	m, dir := newModel(t)
	for name, want := range map[string]string{"ci.yml": "YAML", "notes": "Texte", "go.mod": "go.mod"} {
		os.WriteFile(filepath.Join(dir, name), []byte("x\n"), 0o600)
		m.OpenFile(name, 1)
		if out := ansi.Strip(m.View(100, 10)); !strings.Contains(out, "· "+want) {
			t.Errorf("%s : langue %q absente de la barre d'état :\n%s", name, want, out)
		}
	}
}
