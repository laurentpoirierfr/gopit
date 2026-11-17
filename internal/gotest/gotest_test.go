package gotest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Extrait réaliste de « go test -json » : un paquet qui passe (en cache), un
// paquet avec un sous-test en échec, un paquet sans tests et un paquet qui ne
// compile pas.
const sample = `
{"Action":"start","Package":"ex/ok"}
{"Action":"output","Package":"ex/ok","Output":"ok  \tex/ok\t(cached)\n"}
{"Action":"pass","Package":"ex/ok","Elapsed":0}
{"Action":"start","Package":"ex/calc"}
{"Action":"run","Package":"ex/calc","Test":"TestAdd"}
{"Action":"output","Package":"ex/calc","Test":"TestAdd","Output":"=== RUN   TestAdd\n"}
{"Action":"pass","Package":"ex/calc","Test":"TestAdd","Elapsed":0.01}
{"Action":"run","Package":"ex/calc","Test":"TestDiv"}
{"Action":"run","Package":"ex/calc","Test":"TestDiv/par_zéro"}
{"Action":"output","Package":"ex/calc","Test":"TestDiv/par_zéro","Output":"    calc_test.go:21: attendu une erreur\n"}
{"Action":"fail","Package":"ex/calc","Test":"TestDiv/par_zéro","Elapsed":0}
{"Action":"run","Package":"ex/calc","Test":"TestDiv/normal"}
{"Action":"pass","Package":"ex/calc","Test":"TestDiv/normal","Elapsed":0}
{"Action":"fail","Package":"ex/calc","Test":"TestDiv","Elapsed":0.02}
{"Action":"run","Package":"ex/calc","Test":"TestLent"}
{"Action":"skip","Package":"ex/calc","Test":"TestLent","Elapsed":0}
{"Action":"output","Package":"ex/calc","Output":"FAIL\n"}
{"Action":"fail","Package":"ex/calc","Elapsed":0.3}
{"Action":"start","Package":"ex/vide"}
{"Action":"output","Package":"ex/vide","Output":"?   \tex/vide\t[no test files]\n"}
{"Action":"skip","Package":"ex/vide","Elapsed":0}
{"ImportPath":"ex/casse [ex/casse.test]","Action":"build-output","Output":"casse.go:3:1: syntax error\n"}
{"ImportPath":"ex/casse [ex/casse.test]","Action":"build-fail"}
{"Action":"start","Package":"ex/casse"}
{"Action":"output","Package":"ex/casse","Output":"FAIL\tex/casse [build failed]\n"}
{"Action":"fail","Package":"ex/casse","Elapsed":0,"FailedBuild":"ex/casse [ex/casse.test]"}
pas du JSON
`

func load(t *testing.T) *Results {
	t.Helper()
	r := NewResults()
	for line := range strings.SplitSeq(strings.TrimSpace(sample), "\n") {
		var e Event
		if json.Unmarshal([]byte(line), &e) != nil {
			e = Event{Action: actionStderr, Output: line}
		}
		r.Apply(e)
	}
	return r
}

func TestApply(t *testing.T) {
	r := load(t)

	var paths []string
	for _, p := range r.Packages {
		paths = append(paths, p.Path)
	}
	if want := []string{"ex/calc", "ex/casse", "ex/ok", "ex/vide"}; !reflect.DeepEqual(paths, want) {
		t.Errorf("paquets = %v, attendu %v (triés)", paths, want)
	}

	ok := r.Package("ex/ok")
	if ok.Status != Pass || !ok.Cached {
		t.Errorf("ex/ok : statut %v, cache %v", ok.Status, ok.Cached)
	}
	if v := r.Package("ex/vide"); v.Status != Skip || !v.NoTests {
		t.Errorf("ex/vide : statut %v, sans tests %v", v.Status, v.NoTests)
	}
	casse := r.Package("ex/casse")
	if casse.Status != Fail || !casse.BuildFailed || len(casse.Output) == 0 {
		t.Errorf("ex/casse : statut %v, build %v, sortie %q", casse.Status, casse.BuildFailed, casse.Output)
	}

	calc := r.Package("ex/calc")
	if calc.Status != Fail {
		t.Errorf("ex/calc : statut %v", calc.Status)
	}
	sub := calc.Test("TestDiv/par_zéro")
	if sub == nil || sub.Status != Fail || sub.Depth() != 1 || sub.Short() != "par_zéro" {
		t.Fatalf("sous-test mal reconstitué : %+v", sub)
	}
	if !strings.Contains(strings.Join(sub.Output, ""), "calc_test.go:21") {
		t.Errorf("sortie du sous-test perdue : %q", sub.Output)
	}

	if c := r.Counts(); c != (Counts{Pass: 1, Fail: 2, Skip: 1}) {
		t.Errorf("compteurs = %+v", c) // TestAdd ; TestDiv + build casse ; TestLent
	}
	if len(r.Stderr) != 1 {
		t.Errorf("ligne hors JSON non conservée : %q", r.Stderr)
	}
}

func TestFailuresAndReset(t *testing.T) {
	r := load(t)
	want := map[string][]string{"ex/calc": {"TestDiv"}, "ex/casse": nil}
	if got := r.Failures(); !reflect.DeepEqual(got, want) {
		t.Errorf("Failures = %v, attendu %v", got, want)
	}

	r.ResetTest("ex/calc", "TestDiv")
	calc := r.Package("ex/calc")
	if div := calc.Test("TestDiv"); div == nil || div.Status != Running || len(div.Output) != 0 {
		t.Errorf("ResetTest doit remettre le test à zéro sur place : %+v", div)
	}
	if calc.Test("TestDiv/normal") != nil || calc.Test("TestAdd") == nil {
		t.Error("ResetTest doit retirer les sous-tests et garder les autres tests")
	}
	if calc.Tests[1].Name != "TestDiv" {
		t.Errorf("TestDiv doit garder sa position, ordre : %v", calc.Tests)
	}
	// Relance réussie de TestDiv : le paquet repasse au vert.
	r.Apply(Event{Action: "run", Package: "ex/calc", Test: "TestDiv"})
	r.Apply(Event{Action: "pass", Package: "ex/calc", Test: "TestDiv"})
	r.Apply(Event{Action: "pass", Package: "ex/calc"})
	if calc.Status != Pass {
		t.Errorf("après relance : statut %v", calc.Status)
	}

	r.ResetPackage("ex/casse")
	if p := r.Package("ex/casse"); p.BuildFailed || len(p.Output) != 0 || p.Status != Running {
		t.Errorf("ResetPackage incomplet : %+v", p)
	}
}

func TestRunPattern(t *testing.T) {
	cases := map[string]string{
		"TestA":         `^TestA$`,
		"TestA/cas_1":   `^TestA$/^cas_1$`,
		"TestB/a.b/(x)": `^TestB$/^a\.b$/^\(x\)$`,
	}
	for in, want := range cases {
		if got := RunPattern(in); got != want {
			t.Errorf("RunPattern(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestDependents(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":  "module ex\n\ngo 1.24\n",
		"a/a.go":  "package a\n\nfunc A() {}\n",
		"b/b.go":  "package b\n\nimport \"ex/a\"\n\nfunc B() { a.A() }\n",
		"c/c.go":  "package c\n\nimport \"ex/b\"\n\nfunc C() { b.B() }\n", // dépend de a via b
		"d/d.go":  "package d\n\nfunc D() {}\n",
		"main.go": "package main\n\nimport \"ex/d\"\n\nfunc main() { d.D() }\n",
	}
	for name, src := range files {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Dependents(context.Background(), root, []string{"ex/a"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ex/a", "ex/b", "ex/c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("dépendants de ex/a = %v, attendu %v", got, want)
	}
	got, _ = Dependents(context.Background(), root, []string{"ex/d"})
	if want := []string{"ex", "ex/d"}; !reflect.DeepEqual(got, want) {
		t.Errorf("dépendants de ex/d = %v, attendu %v", got, want)
	}
}
