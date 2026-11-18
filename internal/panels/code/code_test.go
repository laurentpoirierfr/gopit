package code

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/theme"
)

func TestHighlightGo(t *testing.T) {
	src := "package é\n\n// doc\nfunc f() int { return len(\"x\") + 42 }"
	cls := highlightGo(src)
	if len(cls) != 4 {
		t.Fatalf("%d lignes", len(cls))
	}
	if len(cls[0]) != len([]rune("package é")) {
		t.Errorf("une catégorie par rune : %d", len(cls[0]))
	}
	at := func(line int, word string) class {
		return cls[line][strings.Index(strings.Split(src, "\n")[line], word)]
	}
	for _, c := range []struct {
		line int
		word string
		want class
	}{
		{0, "package", clKeyword}, {2, "// doc", clComment}, {3, "f()", clFunc},
		{3, "int", clBuiltin}, {3, "len", clBuiltin}, {3, `"x"`, clString}, {3, "42", clNumber},
	} {
		if got := at(c.line, c.word); got != c.want {
			t.Errorf("%q : %d, attendu %d", c.word, got, c.want)
		}
	}
	// Source en cours de frappe : pas de panique, chaîne non fermée colorée.
	if cls := highlightGo("x := `brut\nsuite"); cls[1][0] != clString {
		t.Errorf("chaîne brute sur deux lignes : %v", cls[1])
	}
}

func TestFilterFiles(t *testing.T) {
	files := []string{"internal/app/app.go", "internal/panels/code/code.go", "README.md", "cmd/gopit/main.go"}
	got := filterFiles(files, "code")
	if len(got) != 1 || got[0] != "internal/panels/code/code.go" {
		t.Errorf("nom : %v", got)
	}
	if got := filterFiles(files, "pcode"); len(got) != 1 {
		t.Errorf("lettres dans l'ordre : %v", got)
	}
	if got := filterFiles(files, "app"); got[0] != "internal/app/app.go" {
		t.Errorf("nom du fichier d'abord : %v", got)
	}
}

func TestColumns(t *testing.T) {
	line := []rune("\tab\t日x")
	// \t → 4, a → 5, b → 6, \t → 8, 日 → 10, x → 11
	if got := dispCol(line, 5); got != 10 {
		t.Errorf("colonne d'affichage : %d", got)
	}
	for x, want := range map[int]int{0: 0, 3: 1, 4: 1, 5: 2, 9: 5, 10: 5, 30: 6} {
		if got := runeAtCol(line, x); got != want {
			t.Errorf("clic en %d : %d, attendu %d", x, got, want)
		}
	}
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "ctrl+s":
		return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	case "ctrl+w":
		return tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl}
	case "ctrl+f":
		return tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

func newModel(t *testing.T) (*Model, string) {
	dir := t.TempDir()
	m := New(theme.New(), &project.Project{Root: dir})
	return m, dir
}

func TestEditSaveFormatsGo(t *testing.T) {
	m, dir := newModel(t)
	path := filepath.Join(dir, "a.go")
	if err := os.WriteFile(path, []byte("package a\n\nfunc f() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cmd := m.OpenFile("a.go", 3); cmd != nil {
		t.Fatalf("ouverture : %v", cmd())
	}
	if !m.Capturing() || m.doc().buf.Cursor().Line != 2 {
		t.Fatalf("ouvert en édition à la ligne 3 : %v", m.doc().buf.Cursor())
	}
	m.Update(key("end"))
	for _, s := range []string{"\n", "v", "a", "r", " ", "x", "=", "1"} {
		if s == "\n" {
			m.Update(key("enter"))
		} else {
			m.Update(key(s))
		}
	}
	if !slices.Equal(m.Unsaved(), []string{"a.go"}) {
		t.Errorf("non enregistré : %v", m.Unsaved())
	}
	m.Update(key("ctrl+s"))
	data, _ := os.ReadFile(path)
	if want := "package a\n\nfunc f() {}\n\nvar x = 1\n"; string(data) != want {
		t.Errorf("enregistré avec gofmt :\n%q\nattendu\n%q", data, want)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Errorf("droits conservés : %v", st.Mode().Perm())
	}
	if len(m.Unsaved()) != 0 {
		t.Error("enregistré : plus rien à perdre")
	}

	// Échap : lecture ; les lettres ne modifient plus le texte.
	m.Update(key("esc"))
	m.Update(key("z"))
	if m.Capturing() || m.doc().buf.Dirty() {
		t.Error("en lecture, une lettre n'est pas insérée")
	}
}

func TestCloseNeedsConfirmation(t *testing.T) {
	m, dir := newModel(t)
	os.WriteFile(filepath.Join(dir, "n.txt"), []byte("x"), 0o644)
	m.OpenFile("n.txt", 0)
	m.Update(key("y"))
	m.Update(key("ctrl+w"))
	if len(m.docs) != 1 {
		t.Fatal("fichier modifié : confirmation demandée")
	}
	m.Update(key("ctrl+w"))
	if len(m.docs) != 0 || m.Capturing() {
		t.Error("deuxième Ctrl+W : fermé")
	}
}

func TestFindPromptAndRestore(t *testing.T) {
	m, dir := newModel(t)
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("un deux\ntrois deux"), 0o644)
	m.OpenFile("f.txt", 0)
	m.Update(key("ctrl+f"))
	for _, r := range "deux" {
		m.Update(key(string(r)))
	}
	if got := m.doc().buf.SelectedText(); got != "deux" {
		t.Fatalf("recherche au fil de la frappe : %q", got)
	}
	m.Update(key("enter"))
	m.Update(panel.EditMsg{Action: panel.ActFindNext})
	if c := m.doc().buf.Cursor(); c.Line != 1 {
		t.Errorf("occurrence suivante : %v", c)
	}
	// Échap annule la recherche et ramène le curseur.
	m.Update(key("ctrl+f"))
	m.Update(key("t"))
	m.Update(key("esc"))
	if c := m.doc().buf.Cursor(); c.Line != 1 || m.prompt != promptNone {
		t.Errorf("annulation : %v", c)
	}
}

func TestViewRenders(t *testing.T) {
	m, dir := newModel(t)
	var long strings.Builder
	for i := range 100 {
		long.WriteString("\tligne\t" + strings.Repeat("é", i) + "\n")
	}
	os.WriteFile(filepath.Join(dir, "l.go"), []byte(long.String()), 0o644)
	m.OpenFile("l.go", 80)
	out := m.View(60, 20)
	lines := strings.Split(out, "\n")
	if len(lines) != 20 {
		t.Errorf("hauteur : %d lignes", len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w > 60 {
			t.Errorf("ligne %d trop large : %d", i, w)
		}
	}
	if !strings.Contains(out, " 80 ") || !strings.Contains(out, "Ln 80") {
		t.Errorf("ligne 80 visible et indiquée :\n%s", ansi.Strip(out))
	}
	// Liste de fichiers.
	m.Update(panel.EditMsg{Action: panel.ActOpen})
	if out := ansi.Strip(m.View(60, 20)); !strings.Contains(out, "● l.go") {
		t.Errorf("liste des fichiers :\n%s", out)
	}
}

func TestPersistence(t *testing.T) {
	m, dir := newModel(t)
	for _, f := range []string{"a.txt", "b.txt"} {
		os.WriteFile(filepath.Join(dir, f), []byte(f), 0o644)
		m.OpenFile(f, 0)
	}
	st := m.State()
	m2 := New(theme.New(), &project.Project{Root: dir})
	m2.Restore(st)
	if len(m2.docs) != 2 || m2.doc().rel != "b.txt" {
		t.Errorf("fichiers rouverts : %d, courant %v", len(m2.docs), m2.doc())
	}
}

func TestTree(t *testing.T) {
	m, dir := newModel(t)
	for _, f := range []string{"b.go", "a/x.go", "a/y.go", ".git/HEAD", "Z.md"} {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o755)
		os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644)
	}
	m.Update(key("t")) // pas de fichier : lecture, t donne le focus à l'arbre
	m.focusTree()
	m.tree.flatten(dir)
	names := func() []string {
		var out []string
		for _, r := range m.tree.rows {
			out = append(out, strings.Repeat(">", r.depth)+r.name)
		}
		return out
	}
	if got := names(); !slices.Equal(got, []string{"a", "b.go", "Z.md"}) {
		t.Fatalf("dossiers d'abord, .git masqué : %v", got)
	}
	if m.Capturing() {
		t.Error("arbre : les touches du cockpit restent actives")
	}
	m.Update(key("right")) // déplier « a »
	m.Update(key("down"))
	m.Update(key("down"))  // a/y.go
	m.Update(key("enter")) // ouvrir
	if d := m.doc(); d == nil || d.rel != "a/y.go" || m.tree.focus || !m.Capturing() {
		t.Fatalf("ouverture depuis l'arbre : %v", d)
	}
	// Les lettres vont au texte, Ctrl+E revient à l'arbre où elles ne
	// modifient rien.
	m.Update(key("q"))
	m.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	m.Update(key("q"))
	if got := m.doc().buf.String(); got != "qx" || !m.tree.focus {
		t.Errorf("texte %q, focus arbre %v", got, m.tree.focus)
	}
	m.Update(key("left")) // sur un fichier : remonte au dossier
	if r := m.tree.selected(); r == nil || r.name != "a" {
		t.Errorf("remonter au dossier : %v", r)
	}
	m.Update(key("left")) // replier
	if got := names(); len(got) != 3 {
		t.Errorf("replié : %v", got)
	}
	// Persistance : arbre masqué gardé, fichier courant montré.
	m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m2 := New(theme.New(), &project.Project{Root: dir})
	m2.Restore(m.State())
	if m2.tree.visible {
		t.Error("arbre masqué conservé")
	}
	m2.tree.flatten(dir)
	if r := m2.tree.selected(); r == nil || r.name != "y.go" {
		t.Errorf("fichier courant montré dans l'arbre : %v", r)
	}
}

func TestTabCloseCross(t *testing.T) {
	m, dir := newModel(t)
	for _, f := range []string{"a.txt", "b.txt"} {
		os.WriteFile(filepath.Join(dir, f), []byte(f), 0o644)
		m.OpenFile(f, 0)
	}
	m.View(100, 20)
	sp := m.tabSpans[0]
	m.Update(panel.ClickMsg{X: m.treeW + sp.close, Y: 0})
	if len(m.docs) != 1 || m.doc().rel != "b.txt" {
		t.Errorf("croix de l'onglet : %d fichiers", len(m.docs))
	}
	if out := ansi.Strip(m.View(100, 20)); !strings.Contains(out, "b.txt ×") || !strings.Contains(out, "▸") && !strings.Contains(out, "b.txt") {
		t.Errorf("rendu :\n%s", out)
	}
}
