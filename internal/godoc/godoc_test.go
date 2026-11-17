package godoc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const src = `// Package geo fournit des formes géométriques.
//
// # Exemple
//
//	c := geo.NewCercle(2)
//	_ = c.Aire()
package geo

import "math"

// Pi est une approximation de π.
const Pi = math.Pi

// Unités de mesure.
const (
	Metre = iota
	Centimetre
)

// Forme est une surface.
type Forme interface {
	Aire() float64
}

// Cercle est un disque de rayon R.
type Cercle struct {
	R float64
}

// NewCercle crée un cercle.
func NewCercle(r float64) *Cercle { return &Cercle{R: r} }

// Aire renvoie la surface du cercle.
func (c *Cercle) Aire() float64 { return Pi * c.R * c.R }

// Somme additionne les aires.
func Somme(fs ...Forme) float64 {
	var s float64
	for _, f := range fs {
		s += f.Aire()
	}
	return s
}

func prive() {}
`

func load(t *testing.T) *Doc {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "geo.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	// Un fichier de test ne doit pas apparaître dans la documentation.
	os.WriteFile(filepath.Join(dir, "geo_test.go"), []byte("package geo\n\nfunc TestX() {}\n"), 0o644)
	d, err := Load(Pkg{ImportPath: "example.com/geo", Name: "geo", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestSymbols(t *testing.T) {
	d := load(t)
	var got []string
	for _, s := range d.Symbols {
		got = append(got, string(s.Kind)+":"+s.Name)
	}
	want := "const:Metre, Centimetre const:Pi func:Somme type:Cercle func:NewCercle method:Cercle.Aire type:Forme"
	if strings.Join(got, " ") != want {
		t.Errorf("symboles :\n %s\nattendu\n %s", strings.Join(got, " "), want)
	}
	s := d.Find("Cercle.Aire")
	if s == nil || filepath.Base(s.File) != "geo.go" || s.Line != 34 || s.Parent != "Cercle" {
		t.Errorf("emplacement de Cercle.Aire : %+v", s)
	}
}

func TestOverview(t *testing.T) {
	md := load(t).Overview()
	for _, want := range []string{
		"# package geo",
		"`import \"example.com/geo\"`",
		"Package geo fournit des formes géométriques.",
		"### Exemple",           // titre de commentaire → Markdown
		"c := geo.NewCercle(2)", // bloc de code du commentaire
		"## Fonctions",
		"func Somme(fs ...Forme) float64",
		"type Cercle struct { … }",
		"    func (c *Cercle) Aire() float64",
		"const ( Metre, Centimetre )",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("vue d'ensemble : %q absent\n%s", want, md)
		}
	}
	if strings.Contains(md, "prive") || strings.Contains(md, "TestX") {
		t.Error("les symboles non exportés et les tests ne doivent pas apparaître")
	}
}

func TestSymbolDoc(t *testing.T) {
	d := load(t)
	md := d.Symbol("Cercle")
	for _, want := range []string{"# type Cercle", "R float64", "Cercle est un disque", "## func NewCercle", "## func (Cercle) Aire", "Aire renvoie la surface"} {
		if !strings.Contains(md, want) {
			t.Errorf("type Cercle : %q absent\n%s", want, md)
		}
	}
	if md := d.Symbol("Cercle.Aire"); !strings.Contains(md, "func (c *Cercle) Aire() float64") || strings.Contains(md, "return") {
		t.Errorf("méthode : déclaration sans corps attendue\n%s", md)
	}
	if md := d.Symbol("Inconnu"); !strings.Contains(md, "introuvable") {
		t.Errorf("symbole inconnu : %s", md)
	}
}

func TestListStd(t *testing.T) {
	pkgs, err := ListStd(context.Background(), ".")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]Pkg{}
	for _, p := range pkgs {
		seen[p.ImportPath] = p
		if strings.Contains(p.ImportPath, "internal") || strings.HasPrefix(p.ImportPath, "vendor/") {
			t.Errorf("paquet non public listé : %s", p.ImportPath)
		}
	}
	if p, ok := seen["net/http"]; !ok || !p.Std || p.Synopsis == "" {
		t.Errorf("net/http absent ou incomplet : %+v", p)
	}
}
