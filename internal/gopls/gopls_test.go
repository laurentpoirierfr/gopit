package gopls

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDefinition(t *testing.T) {
	out := `{
	"span": {
		"uri": "file:///home/moi/proj/a%20b/x.go",
		"start": {"line": 3, "column": 6, "offset": 42},
		"end": {"line": 3, "column": 9, "offset": 45}
	},
	"description": "func F(x int)\nF fait quelque chose.\n"
}`
	d, err := ParseDefinition([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if d.File != "/home/moi/proj/a b/x.go" || d.Offset != 42 || d.Doc != "func F(x int)\nF fait quelque chose." {
		t.Errorf("%+v", d)
	}
	if _, err := ParseDefinition([]byte("pas du json")); err == nil {
		t.Error("sortie illisible : erreur attendue")
	}
}

func TestOffsets(t *testing.T) {
	src := "package é\n\nvar ü, x = 1, 2\n"
	lines := strings.Split(strings.TrimSuffix(src, "\n"), "\n")
	for _, c := range []struct{ line, col int }{{0, 0}, {0, 8}, {1, 0}, {2, 4}, {2, 7}} {
		off := Offset(lines, c.line, c.col)
		if l, col := LineCol([]byte(src), off); l != c.line || col != c.col {
			t.Errorf("%d:%d → %d → %d:%d", c.line, c.col, off, l, col)
		}
	}
	if off := Offset(lines, 2, 7); src[off:off+1] != "x" {
		t.Errorf("octet %d : %q", off, src[off:off+1])
	}
}

// TestDefine interroge le vrai gopls, s'il est installé.
func TestDefine(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls absent")
	}
	if testing.Short() {
		t.Skip("lent")
	}
	dir := t.TempDir()
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module m\n\ngo 1.22\n")
	src := "package m\n\n// Deux renvoie 2.\nfunc Deux() int { return 2 }\n\nvar x = Deux()\n"
	write("m.go", src)
	ctx := context.Background()
	d, err := Define(ctx, dir, filepath.Join(dir, "m.go"), strings.LastIndex(src, "Deux"))
	if err != nil {
		t.Fatal(err)
	}
	if l, c := LineCol([]byte(src), d.Offset); filepath.Base(d.File) != "m.go" || l != 3 || c != 5 {
		t.Errorf("%s %d:%d", d.File, l, c)
	}
	if !strings.Contains(d.Doc, "Deux renvoie 2.") {
		t.Errorf("doc : %q", d.Doc)
	}
	if _, err := Define(ctx, dir, filepath.Join(dir, "m.go"), 2); err != ErrNoIdent {
		t.Errorf("mot-clé : %v", err)
	}
}
