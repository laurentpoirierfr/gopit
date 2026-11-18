// Package gotest lance « go test -json » et reconstitue, événement par
// événement, l'état des paquets et des tests. Il ne dépend pas de l'interface
// et peut être testé seul.
//
// Format des événements : go doc cmd/test2json
package gotest

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
)

// Event est une ligne de « go test -json ».
type Event struct {
	Time        time.Time
	Action      string
	Package     string
	ImportPath  string // événements build-output / build-fail
	Test        string
	Elapsed     float64 // secondes
	Output      string
	FailedBuild string
}

// Actions ajoutées par ce paquet (absentes de test2json).
const actionStderr = "stderr"

// Status est l'état d'un paquet ou d'un test.
type Status int

const (
	Pending Status = iota
	Running
	Pass
	Fail
	Skip
)

// Test est un test ou un sous-test (« TestA/cas_1 »).
type Test struct {
	Name    string
	Status  Status
	Elapsed float64
	Output  []string
}

// Depth vaut 0 pour un test, 1 pour un sous-test, etc.
func (t *Test) Depth() int { return strings.Count(t.Name, "/") }

// Short renvoie le dernier segment du nom (« cas_1 » pour « TestA/cas_1 »).
func (t *Test) Short() string {
	return t.Name[strings.LastIndex(t.Name, "/")+1:]
}

// Package regroupe les tests d'un paquet.
type Package struct {
	Path        string
	Status      Status
	Elapsed     float64
	Cached      bool
	NoTests     bool
	BuildFailed bool
	Output      []string // sortie du paquet (dont les erreurs de compilation)
	Tests       []*Test  // dans l'ordre d'apparition
	byName      map[string]*Test
}

// Test renvoie le test nommé, ou nil.
func (p *Package) Test(name string) *Test { return p.byName[name] }

func (p *Package) test(name string) *Test {
	if t := p.byName[name]; t != nil {
		return t
	}
	t := &Test{Name: name}
	p.byName[name] = t
	p.Tests = append(p.Tests, t)
	return t
}

// hasFailure indique si un test de premier niveau a échoué.
func (p *Package) hasFailure() bool {
	for _, t := range p.Tests {
		if t.Depth() == 0 && t.Status == Fail {
			return true
		}
	}
	return false
}

// Results est l'état complet des tests.
type Results struct {
	Packages []*Package // triés par chemin
	Stderr   []string   // lignes hors JSON (erreurs de la commande go)
	byPath   map[string]*Package
}

func NewResults() *Results {
	return &Results{byPath: map[string]*Package{}}
}

// Package renvoie le paquet, ou nil.
func (r *Results) Package(path string) *Package { return r.byPath[path] }

func (r *Results) pkg(path string) *Package {
	if p := r.byPath[path]; p != nil {
		return p
	}
	p := &Package{Path: path, byName: map[string]*Test{}}
	r.byPath[path] = p
	i := sort.Search(len(r.Packages), func(i int) bool { return r.Packages[i].Path >= path })
	r.Packages = append(r.Packages, nil)
	copy(r.Packages[i+1:], r.Packages[i:])
	r.Packages[i] = p
	return p
}

// Apply met à jour l'état avec un événement.
func (r *Results) Apply(e Event) {
	switch e.Action {
	case actionStderr:
		r.Stderr = append(r.Stderr, e.Output)
		return
	case "build-output", "build-fail":
		// ImportPath vaut « chemin » ou « chemin [chemin.test] ».
		path, _, _ := strings.Cut(e.ImportPath, " ")
		p := r.pkg(path)
		if e.Action == "build-output" {
			p.Output = append(p.Output, e.Output)
		} else {
			p.BuildFailed, p.Status = true, Fail
		}
		return
	}
	if e.Package == "" {
		return
	}

	p := r.pkg(e.Package)
	if e.Test == "" {
		switch e.Action {
		case "start":
			p.Status = Running
		case "output":
			p.Output = append(p.Output, e.Output)
			if strings.Contains(e.Output, "(cached)") {
				p.Cached = true
			}
			if strings.Contains(e.Output, "[no test files]") {
				p.NoTests = true
			}
		case "pass", "fail", "skip":
			p.Elapsed = e.Elapsed
			switch {
			case e.Action == "fail" || e.FailedBuild != "" || p.BuildFailed || p.hasFailure():
				p.Status = Fail
			case e.Action == "skip":
				p.Status = Skip
			default:
				p.Status = Pass
			}
		}
		return
	}

	t := p.test(e.Test)
	switch e.Action {
	case "run":
		t.Status = Running
		if p.Status == Pending {
			p.Status = Running
		}
	case "output":
		t.Output = append(t.Output, e.Output)
	case "pass":
		t.Status, t.Elapsed = Pass, e.Elapsed
	case "fail":
		t.Status, t.Elapsed = Fail, e.Elapsed
	case "skip":
		t.Status, t.Elapsed = Skip, e.Elapsed
	}
}

// ResetPackage efface les résultats d'un paquet avant de le relancer.
func (r *Results) ResetPackage(path string) {
	p := r.pkg(path)
	*p = Package{Path: path, Status: Running, byName: map[string]*Test{}}
}

// ResetTest prépare la relance d'un test : il est remis à zéro sur place
// (pour garder sa position dans la liste) et ses sous-tests sont retirés.
func (r *Results) ResetTest(path, name string) {
	p := r.pkg(path)
	kept := p.Tests[:0]
	for _, t := range p.Tests {
		switch {
		case t.Name == name:
			*t = Test{Name: name, Status: Running}
		case strings.HasPrefix(t.Name, name+"/"):
			delete(p.byName, t.Name)
			continue
		}
		kept = append(kept, t)
	}
	p.Tests = kept
	p.Status, p.Cached = Running, false
}

// Counts compte les tests de premier niveau par état.
type Counts struct{ Pass, Fail, Skip, Running int }

func (r *Results) Counts() Counts {
	var c Counts
	for _, p := range r.Packages {
		for _, t := range p.Tests {
			if t.Depth() > 0 {
				continue
			}
			switch t.Status {
			case Pass:
				c.Pass++
			case Fail:
				c.Fail++
			case Skip:
				c.Skip++
			case Running:
				c.Running++
			}
		}
		if p.BuildFailed {
			c.Fail++
		}
	}
	return c
}

// Failures renvoie, pour chaque paquet en échec, les tests de premier niveau
// échoués (nil si le paquet ne compile pas : il faut le relancer en entier).
func (r *Results) Failures() map[string][]string {
	out := map[string][]string{}
	for _, p := range r.Packages {
		if p.BuildFailed {
			out[p.Path] = nil
			continue
		}
		for _, t := range p.Tests {
			if t.Depth() == 0 && t.Status == Fail {
				out[p.Path] = append(out[p.Path], t.Name)
			}
		}
		if p.Status == Fail && len(out[p.Path]) == 0 {
			if _, ok := out[p.Path]; !ok {
				out[p.Path] = nil // échec hors test (TestMain, panic…)
			}
		}
	}
	return out
}

// RunPattern construit l'argument de -run qui sélectionne exactement un test
// ou un sous-test : « TestA/cas 1 » → « ^TestA$/^cas_1$ ».
func RunPattern(name string) string {
	segs := strings.Split(name, "/")
	for i, s := range segs {
		segs[i] = "^" + regexp.QuoteMeta(s) + "$"
	}
	return strings.Join(segs, "/")
}

// ─── Exécution ───────────────────────────────────────────────────────────────

// Run est une exécution de « go test -json » en cours.
type Run struct {
	Events <-chan Event // fermé à la fin de l'exécution
	err    error
}

// ErrInterrupted est renvoyée quand l'exécution a été arrêtée.
var ErrInterrupted = i18n.Error("interrompu")

// Err renvoie l'erreur de la commande ; à lire après la fermeture de Events.
// Un code de sortie 1 (tests en échec) n'est pas une erreur : les échecs sont
// déjà dans les événements.
func (r *Run) Err() error { return r.err }

// Start lance « go test -json args… » dans dir.
func Start(ctx context.Context, dir string, args ...string) (*Run, error) {
	cmd := exec.CommandContext(ctx, "go", append([]string{"test", "-json"}, args...)...)
	cmd.Dir = dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	ch := make(chan Event, 256)
	r := &Run{Events: ch}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for sc.Scan() {
			var e Event
			if json.Unmarshal(sc.Bytes(), &e) != nil || e.Action == "" {
				e = Event{Action: actionStderr, Output: sc.Text() + "\n"}
			}
			ch <- e
		}
		drain(sc, stdout, ch)
	}()
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			ch <- Event{Action: actionStderr, Output: sc.Text() + "\n"}
		}
		drain(sc, stderr, ch)
	}()
	go func() {
		wg.Wait()
		err := cmd.Wait()
		var ee *exec.ExitError
		switch {
		case ctx.Err() != nil:
			r.err = ErrInterrupted
		case errors.As(err, &ee) && ee.ExitCode() == 1:
			r.err = nil
		default:
			r.err = err
		}
		close(ch)
	}()
	return r, nil
}

// drain signale une erreur de lecture (ligne trop longue…) puis vide le reste
// du flux : sinon « go test » resterait bloqué en écriture.
func drain(sc *bufio.Scanner, r io.Reader, ch chan<- Event) {
	if err := sc.Err(); err != nil {
		ch <- Event{Action: actionStderr, Output: i18n.T("gopit : lecture interrompue : ") + err.Error() + "\n"}
		_, _ = io.Copy(io.Discard, r)
	}
}

// ─── Paquets à relancer après une modification ───────────────────────────────

// Dependents renvoie, parmi les paquets du module (./...), ceux qui sont dans
// pkgs ou qui en dépendent, directement ou non. Le résultat est trié.
func Dependents(ctx context.Context, root string, pkgs []string) ([]string, error) {
	want := map[string]bool{}
	for _, p := range pkgs {
		want[p] = true
	}
	cmd := exec.CommandContext(ctx, "go", "list", "-e", "-f", "{{.ImportPath}}{{range .Deps}} {{.}}{{end}}", "./...")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil && len(out) == 0 {
		return nil, err
	}
	var res []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		hit := want[fields[0]]
		for _, dep := range fields[1:] {
			if hit {
				break
			}
			hit = want[dep]
		}
		if hit {
			res = append(res, fields[0])
		}
	}
	sort.Strings(res)
	return res, nil
}
