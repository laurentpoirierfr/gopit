package code

// Création d'un fichier (Ctrl+N, ou « n » dans l'arbre) : chemin relatif au
// projet, sous-répertoires créés au besoin ; un fichier Go commence par sa
// clause package.

import (
	"errors"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/textbuf"
)

// startNewFile ouvre la saisie du chemin, préremplie avec le répertoire du
// moment : celui sélectionné dans l'arbre, sinon celui du fichier affiché.
func (m *Model) startNewFile() {
	dir := ""
	if r := m.tree.selected(); m.tree.focus && m.tree.visible && r != nil {
		dir = r.path
		if !r.dir {
			dir = filepath.Dir(r.path)
		}
	} else if d := m.doc(); d != nil {
		dir = filepath.Dir(d.path)
	}
	m.prompt, m.input = promptNew, ""
	if rel := m.relName(dir); dir != "" && rel != "." && !filepath.IsAbs(rel) {
		m.input = rel + "/"
	}
}

// createFile crée le fichier saisi (ou ouvre celui qui existe déjà).
func (m *Model) createFile(input string) tea.Cmd {
	input = strings.TrimSpace(input)
	if input == "" || strings.HasSuffix(input, "/") {
		return panel.Flash(i18n.T("Nom de fichier attendu"), true)
	}
	path := m.abs(input)
	if rel, err := filepath.Rel(m.proj.Root, path); err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
		return panel.Flash(i18n.T("Le fichier doit être dans le projet"), true)
	}
	switch st, err := os.Stat(path); {
	case err == nil && st.IsDir():
		return panel.Flash(i18n.T("C'est un répertoire : ")+m.relName(path), true)
	case err == nil:
		if cmd := m.OpenFile(path, 0); cmd != nil {
			return cmd
		}
		return panel.Flash(i18n.T("Existe déjà, ouvert : ")+m.relName(path), false)
	case !errors.Is(err, fs.ErrNotExist):
		return panel.Flash(i18n.T("Création : ")+err.Error(), true)
	}

	text := ""
	if strings.HasSuffix(path, ".go") {
		text = "package " + packageFor(filepath.Dir(path)) + "\n"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return panel.Flash(i18n.T("Création : ")+err.Error(), true)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return panel.Flash(i18n.T("Création : ")+err.Error(), true)
	}
	m.tree.refresh()
	for dir := filepath.Dir(path); strings.HasPrefix(dir, m.proj.Root); dir = filepath.Dir(dir) {
		m.tree.expanded[dir] = true // montrer le fichier dans l'arbre
		if dir == m.proj.Root {
			break
		}
	}
	if cmd := m.OpenFile(path, 0); cmd != nil {
		return cmd
	}
	m.doc().buf.MoveCursor(textbuf.DocEnd, 1, false)
	return panel.Flash(i18n.T("Créé : ")+m.relName(path), false)
}

// packageFor renvoie le nom de paquet d'un répertoire : celui de ses
// fichiers Go (hors paquet de test externe), sinon tiré du nom du
// répertoire.
func packageFor(dir string) string {
	files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
	fset := token.NewFileSet()
	for _, f := range files {
		af, err := parser.ParseFile(fset, f, nil, parser.PackageClauseOnly)
		if err == nil && !strings.HasSuffix(af.Name.Name, "_test") {
			return af.Name.Name
		}
	}
	name := strings.Map(func(r rune) rune {
		switch {
		case r == '_' || unicode.IsDigit(r):
			return r
		case unicode.IsLetter(r) && r < unicode.MaxASCII:
			return unicode.ToLower(r)
		}
		return -1
	}, filepath.Base(dir))
	if name == "" || unicode.IsDigit(rune(name[0])) {
		return "main"
	}
	return name
}
