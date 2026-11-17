package bench

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Sortie réelle de go test -bench (Go 1.27) sur deux paquets, -count=2.
const output = `goos: linux
goarch: amd64
pkg: exb/fib
cpu: Intel(R) Core(TM) i9-9880H CPU @ 2.30GHz
BenchmarkFib10-16    	  219180	       250.3 ns/op	       0 B/op	       0 allocs/op
BenchmarkFib10-16    	  243517	       241.9 ns/op	       0 B/op	       0 allocs/op
BenchmarkFib20-16    	    2130	     28839 ns/op	       0 B/op	       0 allocs/op
BenchmarkFib20-16    	    2151	     30588 ns/op	       0 B/op	       0 allocs/op
PASS
ok  	exb/fib	0.246s
goos: linux
goarch: amd64
pkg: exb/str
cpu: Intel(R) Core(TM) i9-9880H CPU @ 2.30GHz
BenchmarkConcat-16     	   91545	       605.4 ns/op	     256 B/op	      19 allocs/op
BenchmarkConcat-16     	  103384	       535.3 ns/op	     256 B/op	      19 allocs/op
BenchmarkBuilder-16    	  606472	       105.7 ns/op	      56 B/op	       3 allocs/op	        42.00 items/op
PASS
ok  	exb/str	0.238s
`

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6*math.Max(1, math.Abs(b)) }

func parse(t *testing.T, s string) *Parser {
	t.Helper()
	p := NewParser()
	for line := range strings.SplitSeq(s, "\n") {
		p.Line(line)
	}
	return p
}

func TestParser(t *testing.T) {
	p := parse(t, output)
	var keys []string
	for _, r := range p.Results() {
		keys = append(keys, r.Key())
	}
	if want := []string{"exb/fib Fib10", "exb/fib Fib20", "exb/str Concat", "exb/str Builder"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("benchmarks = %v", keys)
	}
	if p.CPU() != "Intel(R) Core(TM) i9-9880H CPU @ 2.30GHz" {
		t.Errorf("cpu = %q", p.CPU())
	}
	fib := p.Results()[0]
	ns := fib.Metrics[NsOp]
	if fib.Procs != 16 || fib.Iters != 243517 || ns.N != 2 || !near(ns.Mean, 246.1) || ns.Min != 241.9 || ns.Max != 250.3 {
		t.Errorf("Fib10 : %+v %+v", fib, ns)
	}
	if !near(ns.Spread(), (250.3-246.1)/246.1) {
		t.Errorf("écart : %v", ns.Spread())
	}
	b := p.Results()[3]
	if b.Metrics[AllocsOp].Mean != 3 || b.Metrics["items/op"].Mean != 42 {
		t.Errorf("métriques de Builder : %+v", b.Metrics)
	}
	// Lignes sans rapport ignorées.
	if p.Line("ok  \texb/fib\t0.246s") != nil || p.Line("--- FAIL: BenchmarkX") != nil {
		t.Error("ligne non benchmark acceptée")
	}
}

func TestCompare(t *testing.T) {
	old := parse(t, output).Results()
	faster := parse(t, strings.ReplaceAll(strings.ReplaceAll(output, "250.3 ns/op", "125.0 ns/op"), "241.9 ns/op", "121.0 ns/op")).Results()

	d, ok := Compare(old[0], faster[0], NsOp)
	if !ok || d.Noise || !near(d.Pct, (123.0-246.1)/246.1) {
		t.Errorf("Fib10 deux fois plus rapide : %+v", d)
	}
	// Même mesures : plages qui se recouvrent → bruit.
	if d, _ := Compare(old[1], faster[1], NsOp); !d.Noise || d.Pct != 0 {
		t.Errorf("Fib20 inchangé : %+v", d)
	}
	// Un seul échantillon : seuil de 5 %.
	if d, _ := Compare(old[3], old[3], NsOp); !d.Noise {
		t.Errorf("Builder identique : %+v", d)
	}
	if _, ok := Compare(old[0], nil, NsOp); ok {
		t.Error("comparaison avec un benchmark absent")
	}
}

func TestHistory(t *testing.T) {
	h := &History{Dir: filepath.Join(t.TempDir(), "bench")}
	p := parse(t, output)
	for i, label := range []string{"un", "deux", "trois"} {
		r := &Run{Time: time.Date(2026, 11, 17, 10, i, 0, 0, time.UTC), Label: label, Count: 2, CPU: p.CPU(), Results: p.Results()}
		if err := h.Save(r); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(h.Dir, "zz-abime.json"), []byte("{"), 0o644)

	runs, err := h.Load()
	if err != nil || len(runs) != 3 || runs[0].Label != "un" || runs[2].Label != "trois" {
		t.Fatalf("Load : %d exécutions, err=%v", len(runs), err)
	}
	r := runs[2].Result("exb/fib Fib10")
	if r == nil || !near(r.Metrics[NsOp].Mean, 246.1) || r.Metrics[NsOp].N != 2 {
		t.Errorf("benchmark relu : %+v", r)
	}
	if err := h.Prune(2); err != nil {
		t.Fatal(err)
	}
	runs, _ = h.Load()
	if len(runs) != 2 || runs[0].Label != "deux" {
		t.Errorf("Prune : %d exécutions, première %q", len(runs), runs[0].Label)
	}
}

func TestArgs(t *testing.T) {
	got := Options{Pattern: "^BenchmarkFib20$", Pkgs: []string{"exb/fib"}, Count: 5, BenchTime: "100ms"}.Args()
	want := []string{"test", "-run=^$", "-bench=^BenchmarkFib20$", "-benchmem", "-count=5", "-benchtime=100ms", "exb/fib"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Args = %v", got)
	}
}

func TestStartLive(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module exl\n\ngo 1.24\n"), 0o644)
	os.WriteFile(filepath.Join(root, "x_test.go"), []byte("package x\n\nimport \"testing\"\n\nfunc BenchmarkVide(b *testing.B) { for b.Loop() {} }\n"), 0o644)
	e, err := Start(context.Background(), root, Options{Count: 2, BenchTime: "10ms"})
	if err != nil {
		t.Fatal(err)
	}
	p := NewParser()
	for line := range e.Lines {
		p.Line(line)
	}
	if e.Err() != nil || len(p.Results()) != 1 || p.Results()[0].Metrics[NsOp].N != 2 {
		t.Errorf("exécution réelle : err=%v résultats=%v", e.Err(), p.Results())
	}
}
