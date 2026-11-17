package gotest

import (
	"encoding/json"
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
