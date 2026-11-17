package cover

import (
	"math"
	"strings"
	"testing"
)

const profile = `mode: set
ex/a/a.go:3.20,5.2 2 1
ex/a/a.go:7.20,9.16 2 0
ex/a/a.go:9.16,11.3 1 0
ex/a/b.go:3.14,3.30 1 1
ex/c/c.go:5.10,8.2 4 1
ex/c/c.go:5.10,8.2 4 0
`

func near(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestParse(t *testing.T) {
	p, err := Parse(strings.NewReader(profile))
	if err != nil {
		t.Fatal(err)
	}
	if p.Mode != "set" || len(p.Files) != 3 {
		t.Fatalf("mode %q, %d fichiers", p.Mode, len(p.Files))
	}
	a := p.Files["ex/a/a.go"]
	if c, tot := a.Stmts(); c != 2 || tot != 5 || !near(a.Percent(), 40) {
		t.Errorf("a.go : %d/%d = %.1f%%", c, tot, a.Percent())
	}
	if u := a.Uncovered(); len(u) != 2 || u[0].StartLine != 7 || u[1].StartLine != 9 {
		t.Errorf("blocs non couverts : %+v", u)
	}
	// Bloc présent deux fois (deux paquets de test) : compteurs additionnés.
	if c := p.Files["ex/c/c.go"]; len(c.Blocks) != 1 || !near(c.Percent(), 100) {
		t.Errorf("c.go : %+v", c.Blocks)
	}
	if pct, ok := p.PackagePercent("ex/a"); !ok || !near(pct, 50) { // (2+1)/(5+1)
		t.Errorf("paquet ex/a : %.1f%% ok=%v", pct, ok)
	}
	if _, ok := p.PackagePercent("ex/z"); ok {
		t.Error("paquet absent : ok doit valoir false")
	}
	if files := p.PackageFiles("ex/a"); len(files) != 2 || files[0].Name != "ex/a/a.go" {
		t.Errorf("fichiers triés du moins couvert au plus couvert : %v", files)
	}
	if !near(p.Total(), 70) { // (3+4)/(6+4)
		t.Errorf("total : %.2f", p.Total())
	}
}

func TestMerge(t *testing.T) {
	p, _ := Parse(strings.NewReader(profile))
	q, _ := Parse(strings.NewReader("mode: set\nex/a/a.go:3.20,5.2 2 1\nex/a/a.go:7.20,9.16 2 1\n"))
	p.Merge(q)
	if _, ok := p.Files["ex/a/b.go"]; ok {
		t.Error("les anciens fichiers du paquet relancé doivent être remplacés")
	}
	if !near(p.Files["ex/a/a.go"].Percent(), 100) || p.Files["ex/c/c.go"] == nil {
		t.Errorf("fusion : %+v", p.Files)
	}
}

func TestParseErrors(t *testing.T) {
	for _, bad := range []string{"ex/a.go 3 1", "ex/a.go:3.1 2 1", "ex/a.go:x.1,4.2 2 1"} {
		if _, err := Parse(strings.NewReader("mode: set\n" + bad + "\n")); err == nil {
			t.Errorf("%q : erreur attendue", bad)
		}
	}
}

func TestDiskPath(t *testing.T) {
	if p, ok := DiskPath("/src/mod", "ex.com/mod", "ex.com/mod/internal/x/y.go"); !ok || p != "/src/mod/internal/x/y.go" {
		t.Errorf("DiskPath = %q, %v", p, ok)
	}
	if _, ok := DiskPath("/src/mod", "ex.com/mod", "autre.com/x/y.go"); ok {
		t.Error("un fichier hors du module ne doit pas être résolu")
	}
}
