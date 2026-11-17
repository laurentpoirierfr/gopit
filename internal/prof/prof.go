// Package prof analyse un profil pprof (CPU ou mémoire) : coût propre (flat)
// et cumulé (cum) de chaque fonction, et coût ligne par ligne d'une fonction,
// comme « go tool pprof -top » et « -list ».
package prof

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/pprof/profile"
)

// Func est le coût d'une fonction.
type Func struct {
	Name string
	File string
	Line int   // première ligne de la fonction (si connue)
	Flat int64 // coût passé dans la fonction elle-même
	Cum  int64 // coût de la fonction et de tout ce qu'elle appelle
}

// LineCost est le coût d'une ligne de code d'une fonction.
type LineCost struct {
	Line      int
	Flat, Cum int64
}

// Report est l'analyse d'un profil pour un type de mesure.
type Report struct {
	Kind     string        // « cpu », « alloc_space »…
	Unit     string        // « nanoseconds », « bytes », « count »
	Total    int64         // somme des mesures
	Duration time.Duration // durée de collecte (profils CPU)
	Funcs    []Func        // triées par coût propre décroissant
	Types    []string      // types de mesure disponibles dans le profil

	p   *profile.Profile
	idx int
}

// Load lit un profil et l'analyse pour le type de mesure kind (vide : le
// plus pertinent — temps CPU, ou octets alloués pour un profil mémoire).
func Load(path, kind string) (*Report, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	p, err := profile.Parse(f)
	if err != nil {
		return nil, fmt.Errorf("profil illisible : %w", err)
	}
	return Analyze(p, kind)
}

// Analyze analyse un profil déjà lu.
func Analyze(p *profile.Profile, kind string) (*Report, error) {
	if len(p.SampleType) == 0 {
		return nil, fmt.Errorf("profil sans mesure")
	}
	idx := defaultIndex(p)
	var types []string
	for i, st := range p.SampleType {
		types = append(types, st.Type)
		if st.Type == kind {
			idx = i
		}
	}
	r := &Report{Kind: p.SampleType[idx].Type, Unit: p.SampleType[idx].Unit, Types: types,
		Duration: time.Duration(p.DurationNanos), p: p, idx: idx}

	flat := map[string]int64{}
	cum := map[string]int64{}
	where := map[string]*profile.Function{}
	for _, s := range p.Sample {
		v := s.Value[idx]
		if v == 0 {
			continue
		}
		r.Total += v
		seen := map[string]bool{}
		for li, loc := range s.Location {
			for ln, line := range loc.Line {
				if line.Function == nil {
					continue
				}
				name := line.Function.Name
				where[name] = line.Function
				// La première ligne du premier emplacement est la fonction
				// réellement en cours d'exécution (inlining compris).
				if li == 0 && ln == 0 {
					flat[name] += v
				}
				if !seen[name] {
					seen[name] = true
					cum[name] += v
				}
			}
		}
	}
	for name, c := range cum {
		fn := where[name]
		r.Funcs = append(r.Funcs, Func{Name: name, File: fn.Filename, Line: int(fn.StartLine), Flat: flat[name], Cum: c})
	}
	r.Sort(false)
	return r, nil
}

// defaultIndex choisit la mesure à afficher : la dernière pour un profil
// CPU (cpu/nanoseconds), alloc_space pour un profil mémoire.
func defaultIndex(p *profile.Profile) int {
	for i, st := range p.SampleType {
		if st.Type == "alloc_space" {
			return i
		}
	}
	return len(p.SampleType) - 1
}

// Sort trie les fonctions par coût propre (ou cumulé si byCum).
func (r *Report) Sort(byCum bool) {
	sort.SliceStable(r.Funcs, func(i, j int) bool {
		a, b := r.Funcs[i], r.Funcs[j]
		ka, kb := a.Flat, b.Flat
		if byCum {
			ka, kb = a.Cum, b.Cum
		}
		if ka != kb {
			return ka > kb
		}
		return a.Name < b.Name
	})
}

// Lines renvoie le coût de chaque ligne de la fonction, triées par numéro.
func (r *Report) Lines(fn string) []LineCost {
	costs := map[int]*LineCost{}
	for _, s := range r.p.Sample {
		v := s.Value[r.idx]
		if v == 0 {
			continue
		}
		seen := map[int]bool{}
		for li, loc := range s.Location {
			for ln, line := range loc.Line {
				if line.Function == nil || line.Function.Name != fn {
					continue
				}
				n := int(line.Line)
				c := costs[n]
				if c == nil {
					c = &LineCost{Line: n}
					costs[n] = c
				}
				if li == 0 && ln == 0 {
					c.Flat += v
				}
				if !seen[n] {
					seen[n] = true
					c.Cum += v
				}
			}
		}
	}
	out := make([]LineCost, 0, len(costs))
	for _, c := range costs {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

// Format présente une mesure dans l'unité du profil.
func (r *Report) Format(v int64) string {
	switch r.Unit {
	case "nanoseconds":
		d := time.Duration(v)
		switch {
		case d >= time.Second:
			return fmt.Sprintf("%.2fs", d.Seconds())
		case d >= time.Millisecond:
			return fmt.Sprintf("%.0fms", float64(d)/float64(time.Millisecond))
		}
		return d.String()
	case "bytes":
		f := float64(v)
		switch {
		case f >= 1<<30:
			return fmt.Sprintf("%.1fGio", f/(1<<30))
		case f >= 1<<20:
			return fmt.Sprintf("%.1fMio", f/(1<<20))
		case f >= 1<<10:
			return fmt.Sprintf("%.1fKio", f/(1<<10))
		}
		return fmt.Sprintf("%do", v)
	}
	return fmt.Sprint(v)
}

// Percent renvoie v en pourcentage du total.
func (r *Report) Percent(v int64) float64 {
	if r.Total == 0 {
		return 0
	}
	return 100 * float64(v) / float64(r.Total)
}

// ShortName raccourcit un nom de fonction : « github.com/x/y/pkg.(*T).M »
// devient « pkg.(*T).M ».
func ShortName(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}

// As renvoie l'analyse du même profil pour un autre type de mesure.
func (r *Report) As(kind string) (*Report, error) { return Analyze(r.p, kind) }
