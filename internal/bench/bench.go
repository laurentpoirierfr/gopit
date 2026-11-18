// Package bench lance les benchmarks d'un module (go test -bench), lit leurs
// résultats, les enregistre dans un historique et compare deux exécutions.
// Il ne dépend pas de l'interface et peut être testé seul.
package bench

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
)

// Unités principales de « go test -bench -benchmem ».
const (
	NsOp     = "ns/op"
	BytesOp  = "B/op"
	AllocsOp = "allocs/op"
)

// Stat résume les échantillons d'une métrique (une valeur par -count).
type Stat struct {
	Mean, Min, Max float64
	N              int
}

// Spread renvoie l'écart relatif maximal à la moyenne (0,05 = ±5 %).
func (s Stat) Spread() float64 {
	if s.Mean == 0 {
		return 0
	}
	return math.Max(s.Max-s.Mean, s.Mean-s.Min) / s.Mean
}

func newStat(vals []float64) Stat {
	s := Stat{Min: math.Inf(1), Max: math.Inf(-1), N: len(vals)}
	for _, v := range vals {
		s.Mean += v
		s.Min = math.Min(s.Min, v)
		s.Max = math.Max(s.Max, v)
	}
	if s.N > 0 {
		s.Mean /= float64(s.N)
	}
	return s
}

// Result est un benchmark d'une exécution.
type Result struct {
	Pkg     string
	Name    string // sans le préfixe « Benchmark » ni le suffixe « -GOMAXPROCS »
	Procs   int
	Iters   int             // itérations du dernier échantillon
	Metrics map[string]Stat // par unité (ns/op, B/op, allocs/op, unités personnalisées)
	samples map[string][]float64
}

// Key identifie un benchmark d'une exécution à l'autre.
func (r *Result) Key() string { return r.Pkg + " " + r.Name }

// Run est une exécution de benchmarks.
type Run struct {
	ID      string // horodatage, sert de nom de fichier
	Time    time.Time
	Label   string // ce qui a été lancé (« tous », « Fib20 »…)
	Count   int
	CPU     string
	Results []*Result
}

// Result renvoie le benchmark de clé key, ou nil.
func (r *Run) Result(key string) *Result {
	for _, res := range r.Results {
		if res.Key() == key {
			return res
		}
	}
	return nil
}

// ─── Lecture de la sortie ────────────────────────────────────────────────────

// lineRe repère « BenchmarkNom-16   1000   123.4 ns/op   56 B/op … ».
var lineRe = regexp.MustCompile(`^Benchmark(\S+?)(?:-(\d+))?\s+(\d+)\s+(.+)$`)

// Parser lit la sortie de go test -bench ligne par ligne.
type Parser struct {
	pkg, cpu string
	byKey    map[string]*Result
	order    []*Result
}

func NewParser() *Parser { return &Parser{byKey: map[string]*Result{}} }

// Line analyse une ligne ; elle renvoie le benchmark mis à jour, s'il y en a un.
func (p *Parser) Line(line string) *Result {
	line = strings.TrimSpace(line)
	if v, ok := strings.CutPrefix(line, "pkg: "); ok {
		p.pkg = v
		return nil
	}
	if v, ok := strings.CutPrefix(line, "cpu: "); ok {
		p.cpu = v
		return nil
	}
	m := lineRe.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	fields := strings.Fields(m[4])
	if len(fields) < 2 || len(fields)%2 != 0 {
		return nil
	}
	key := p.pkg + " " + m[1]
	r := p.byKey[key]
	if r == nil {
		r = &Result{Pkg: p.pkg, Name: m[1], samples: map[string][]float64{}}
		p.byKey[key] = r
		p.order = append(p.order, r)
	}
	r.Procs, _ = strconv.Atoi(m[2])
	r.Iters, _ = strconv.Atoi(m[3])
	for i := 0; i+1 < len(fields); i += 2 {
		v, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			continue
		}
		r.samples[fields[i+1]] = append(r.samples[fields[i+1]], v)
	}
	r.Metrics = map[string]Stat{}
	for unit, vals := range r.samples {
		r.Metrics[unit] = newStat(vals)
	}
	return r
}

// Results renvoie les benchmarks lus, dans l'ordre d'apparition.
func (p *Parser) Results() []*Result { return p.order }

// CPU renvoie le processeur annoncé par go test.
func (p *Parser) CPU() string { return p.cpu }

// ─── Exécution ───────────────────────────────────────────────────────────────

// Options d'une exécution.
type Options struct {
	Pattern   string   // expression de -bench (« . » par défaut)
	Pkgs      []string // paquets (« ./... » par défaut)
	Count     int      // -count
	BenchTime string   // -benchtime (vide : valeur de go test)
	Extra     []string // arguments supplémentaires (-cpuprofile…)
}

// Args renvoie les arguments de go test.
func (o Options) Args() []string {
	pattern := o.Pattern
	if pattern == "" {
		pattern = "."
	}
	args := []string{"test", "-run=^$", "-bench=" + pattern, "-benchmem"}
	if o.Count > 0 {
		args = append(args, "-count="+strconv.Itoa(o.Count))
	}
	if o.BenchTime != "" {
		args = append(args, "-benchtime="+o.BenchTime)
	}
	args = append(args, o.Extra...)
	pkgs := o.Pkgs
	if len(pkgs) == 0 {
		pkgs = []string{"./..."}
	}
	return append(args, pkgs...)
}

// Exec est une exécution en cours : Lines reçoit chaque ligne de sortie et
// est fermé à la fin ; Err est alors disponible.
type Exec struct {
	Lines <-chan string
	err   error
}

func (e *Exec) Err() error { return e.err }

// ErrInterrupted est renvoyée quand l'exécution a été arrêtée.
var ErrInterrupted = i18n.Error("interrompu")

// Start lance les benchmarks dans root.
func Start(ctx context.Context, root string, o Options) (*Exec, error) {
	cmd := exec.CommandContext(ctx, "go", o.Args()...)
	cmd.Dir = root
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	ch := make(chan string, 64)
	e := &Exec{Lines: ch}
	var wg sync.WaitGroup
	wg.Go(func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for sc.Scan() {
			ch <- sc.Text()
		}
		if err := sc.Err(); err != nil {
			ch <- i18n.T("gopit : lecture interrompue : ") + err.Error()
		}
		_, _ = io.Copy(io.Discard, pr) // vider le reste pour ne pas bloquer go test
	})
	go func() {
		err := cmd.Wait()
		pw.Close()
		wg.Wait()
		switch {
		case ctx.Err() != nil:
			e.err = ErrInterrupted
		case err != nil:
			e.err = fmt.Errorf("go test : %w", err)
		}
		close(ch)
	}()
	return e, nil
}

// ─── Comparaison ─────────────────────────────────────────────────────────────

// Delta compare une métrique entre deux exécutions.
type Delta struct {
	Old, New Stat
	Pct      float64 // variation relative (+0,12 = +12 %)
	Noise    bool    // écart dans le bruit : les plages de mesures se recouvrent
}

// Compare renvoie la variation de la métrique unit entre old et cur.
func Compare(old, cur *Result, unit string) (Delta, bool) {
	if old == nil || cur == nil {
		return Delta{}, false
	}
	o, ok1 := old.Metrics[unit]
	n, ok2 := cur.Metrics[unit]
	if !ok1 || !ok2 {
		return Delta{}, false
	}
	d := Delta{Old: o, New: n}
	switch {
	case o.Mean == 0 && n.Mean == 0:
		d.Pct = 0
	case o.Mean == 0:
		d.Pct = math.Inf(1)
	default:
		d.Pct = (n.Mean - o.Mean) / o.Mean
	}
	// Sans plusieurs échantillons, on ne peut pas juger du bruit : on retient
	// un seuil de 5 %. Sinon, des plages qui se recouvrent = pas de différence.
	if o.N > 1 && n.N > 1 {
		d.Noise = n.Min <= o.Max && o.Min <= n.Max
	} else {
		d.Noise = math.Abs(d.Pct) < 0.05
	}
	return d, true
}

// ─── Historique ──────────────────────────────────────────────────────────────

// History enregistre les exécutions d'un module dans un répertoire.
type History struct {
	Dir string
}

// HistoryFor renvoie l'historique d'un module, rangé dans le répertoire de
// cache de l'utilisateur (~/.cache/gopit/bench/<module>).
func HistoryFor(module string) (*History, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	safe := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(module)
	return &History{Dir: filepath.Join(base, "gopit", "bench", safe)}, nil
}

type storedResult struct {
	Pkg     string               `json:"pkg"`
	Name    string               `json:"name"`
	Procs   int                  `json:"procs"`
	Iters   int                  `json:"iters"`
	Samples map[string][]float64 `json:"samples"`
}

type storedRun struct {
	Time    time.Time      `json:"time"`
	Label   string         `json:"label"`
	Count   int            `json:"count"`
	CPU     string         `json:"cpu"`
	Results []storedResult `json:"results"`
}

// Save enregistre une exécution et lui attribue un identifiant.
func (h *History) Save(r *Run) error {
	if err := os.MkdirAll(h.Dir, 0o755); err != nil {
		return err
	}
	if r.ID == "" {
		r.ID = r.Time.Format(idLayout)
	}
	s := storedRun{Time: r.Time, Label: r.Label, Count: r.Count, CPU: r.CPU}
	for _, res := range r.Results {
		s.Results = append(s.Results, storedResult{Pkg: res.Pkg, Name: res.Name, Procs: res.Procs, Iters: res.Iters, Samples: res.samples})
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(h.Dir, r.ID+".json"), data, 0o644)
}

// idLayout est le format des identifiants (et noms de fichiers) d'exécution.
const idLayout = "20060102-150405.000"

// files renvoie les fichiers d'exécution, du plus ancien au plus récent ;
// les fichiers dont le nom n'est pas un horodatage sont ignorés.
func (h *History) files() ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(h.Dir, "*.json"))
	if err != nil {
		return nil, err
	}
	out := paths[:0]
	for _, p := range paths {
		if _, err := time.Parse(idLayout, strings.TrimSuffix(filepath.Base(p), ".json")); err == nil {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Load renvoie les exécutions enregistrées, de la plus ancienne à la plus
// récente.
func (h *History) Load() ([]*Run, error) {
	paths, err := h.files()
	if err != nil {
		return nil, err
	}
	var runs []*Run
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var s storedRun
		if json.Unmarshal(data, &s) != nil {
			continue // fichier abîmé : ignoré
		}
		r := &Run{ID: strings.TrimSuffix(filepath.Base(p), ".json"), Time: s.Time, Label: s.Label, Count: s.Count, CPU: s.CPU}
		for _, sr := range s.Results {
			res := &Result{Pkg: sr.Pkg, Name: sr.Name, Procs: sr.Procs, Iters: sr.Iters, samples: sr.Samples, Metrics: map[string]Stat{}}
			for unit, vals := range sr.Samples {
				res.Metrics[unit] = newStat(vals)
			}
			r.Results = append(r.Results, res)
		}
		runs = append(runs, r)
	}
	return runs, nil
}

// Prune ne garde que les keep exécutions les plus récentes.
func (h *History) Prune(keep int) error {
	paths, err := h.files()
	if err != nil || len(paths) <= keep {
		return err
	}
	for _, p := range paths[:len(paths)-keep] {
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	return nil
}
