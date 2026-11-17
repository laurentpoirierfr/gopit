package diag

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Sorties réelles (Go 1.27, staticcheck 2026.2.2, gopls v0.24.0) sur un
// paquet contenant des erreurs volontaires ; chemins raccourcis en /p.
const (
	vetStdout = `{
	"example.com/fixture/vetbug": {
		"printf": [
			{
				"posn": "/p/vetbug/vetbug.go:6:23",
				"end": "/p/vetbug/vetbug.go:6:25",
				"message": "fmt.Sprintf format %s reads arg #2, but call has 1 arg"
			}
		]
	}
}
{}
`
	vetStderr = `# example.com/fixture/typeerr
vet: typeerr/t.go:3:23: cannot use "x" (untyped string constant) as int value in return statement
`
	staticcheckOut = `{"code":"compile","severity":"error","location":{"file":"","line":0,"column":0},"end":{"file":"","line":0,"column":0},"message":"# example.com/fixture/typeerr\ntypeerr/t.go:3:23: cannot use \"x\" (untyped string constant) as int value in return statement"}
{"code":"SA5009","severity":"error","location":{"file":"/p/vetbug/vetbug.go","line":6,"column":19},"end":{"file":"/p/vetbug/vetbug.go","line":6,"column":26},"message":"Printf format %s reads arg #2, but call has only 1 args"}
{"code":"SA4000","severity":"error","location":{"file":"/p/vetbug/vetbug.go","line":10,"column":5},"end":{"file":"/p/vetbug/vetbug.go","line":10,"column":11},"message":"identical expressions on the left and right side of the '==' operator"}
{"code":"S1039","severity":"error","location":{"file":"/p/vetbug/vetbug.go","line":13,"column":9},"end":{"file":"/p/vetbug/vetbug.go","line":13,"column":24},"message":"unnecessary use of fmt.Sprint"}
`
	goplsOut = `/p/typeerr/t.go:3:23-26: cannot use "x" (untyped string constant) as int value in return statement
/p/vetbug/vetbug.go:6:23-25: fmt.Sprintf format %s reads arg #2, but call has 1 arg
/p/vetbug/vetbug.go:10:5-11: identical expressions on the left and right side of the '==' operator
/p/vetbug/vetbug.go:13:9-24: unnecessary use of fmt.Sprint
`
)

func TestParseVet(t *testing.T) {
	ds, err := ParseVet([]byte(vetStdout), []byte(vetStderr), "/p")
	if err != nil {
		t.Fatal(err)
	}
	want := []Diagnostic{
		{File: "/p/vetbug/vetbug.go", Line: 6, Col: 23, Message: "fmt.Sprintf format %s reads arg #2, but call has 1 arg",
			Code: "printf", Tools: []string{"vet"}, Severity: Warning},
		{File: "/p/typeerr/t.go", Line: 3, Col: 23, Message: `cannot use "x" (untyped string constant) as int value in return statement`,
			Code: "compilation", Tools: []string{"vet"}, Severity: Error},
	}
	if !reflect.DeepEqual(ds, want) {
		t.Errorf("ParseVet =\n%+v\nattendu\n%+v", ds, want)
	}
}

func TestParseStaticcheck(t *testing.T) {
	ds, err := ParseStaticcheck([]byte(staticcheckOut), "/p")
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 4 {
		t.Fatalf("%d diagnostics, attendu 4 : %+v", len(ds), ds)
	}
	if ds[0].File != "/p/typeerr/t.go" || ds[0].Severity != Error || ds[0].Line != 3 {
		t.Errorf("erreur de compilation mal lue : %+v", ds[0])
	}
	if ds[2].Code != "SA4000" || ds[2].Severity != Warning {
		t.Errorf("SA4000 : %+v", ds[2])
	}
	if ds[3].Code != "S1039" || ds[3].Severity != Info {
		t.Errorf("S1039 doit être une info : %+v", ds[3])
	}
}

func TestParseGopls(t *testing.T) {
	ds := ParseGopls([]byte(goplsOut), "/p")
	if len(ds) != 4 || ds[2].Line != 10 || ds[2].Col != 5 || !strings.HasPrefix(ds[2].Message, "identical") {
		t.Errorf("ParseGopls = %+v", ds)
	}
}

func TestMerge(t *testing.T) {
	vet, _ := ParseVet([]byte(vetStdout), []byte(vetStderr), "/p")
	sc, _ := ParseStaticcheck([]byte(staticcheckOut), "/p")
	gp := ParseGopls([]byte(goplsOut), "/p")
	fmtd := ParseGofmt([]byte("vetbug/ugly.go\n"), "/p")

	ds := Merge(vet, sc, gp, fmtd)
	// Erreur de compilation : identique chez vet, staticcheck et gopls.
	if ds[0].File != "/p/typeerr/t.go" || ds[0].Severity != Error ||
		!reflect.DeepEqual(ds[0].Tools, []string{"vet", "staticcheck", "gopls"}) {
		t.Errorf("fusion de l'erreur de compilation : %+v", ds[0])
	}
	// printf : message identique chez vet et gopls, différent chez staticcheck.
	var printf []Diagnostic
	for _, d := range ds {
		if d.Line == 6 {
			printf = append(printf, d)
		}
	}
	if len(printf) != 2 {
		t.Errorf("ligne 6 : %d diagnostics, attendu 2 (vet+gopls fusionnés, staticcheck à part)", len(printf))
	}
	// SA4000 garde son code même s'il vient aussi de gopls.
	for _, d := range ds {
		if d.Line == 10 && (d.Code != "SA4000" || len(d.Tools) != 2) {
			t.Errorf("ligne 10 : %+v", d)
		}
	}
	// S1039 reste une info (staticcheck) malgré la gravité supposée de gopls.
	if e, w, i := Counts(ds); e != 1 || w != 3 || i != 2 {
		t.Errorf("Counts = %d erreurs, %d avertissements, %d infos", e, w, i)
	}
	// Tri par fichier puis ligne.
	for i := 1; i < len(ds); i++ {
		if ds[i-1].File > ds[i].File || (ds[i-1].File == ds[i].File && ds[i-1].Line > ds[i].Line) {
			t.Errorf("non trié : %s:%d avant %s:%d", ds[i-1].File, ds[i-1].Line, ds[i].File, ds[i].Line)
		}
	}
}

func TestGoFiles(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"a.go", "x/b.go", "x/b_test.go", "vendor/v.go", "testdata/t.go", ".git/g.go", "notes.txt"} {
		full := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, nil, 0o644)
	}
	got, err := GoFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a.go", "x/b.go", "x/b_test.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("GoFiles = %v, attendu %v", got, want)
	}
}
