// Package cover lit les profils de couverture de « go test -coverprofile »
// et calcule la couverture par fichier et par paquet.
//
// Format d'un profil :
//
//	mode: set
//	exemple.com/mod/pkg/fichier.go:12.34,15.2 3 1
//	(fichier:ligne.colonne,ligne.colonne nombreInstructions compteur)
package cover

import (
	"bufio"
	"fmt"
	"io"
	"maps"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Block est un bloc de code du profil.
type Block struct {
	StartLine, EndLine int
	NumStmt            int
	Count              int
}

// File est la couverture d'un fichier source.
type File struct {
	Name   string // chemin dans le profil : « module/paquet/fichier.go »
	Blocks []Block
}

// Package renvoie le chemin d'import du paquet du fichier.
func (f *File) Package() string { return path.Dir(f.Name) }

// Stmts renvoie le nombre d'instructions couvertes et le total.
func (f *File) Stmts() (covered, total int) {
	for _, b := range f.Blocks {
		total += b.NumStmt
		if b.Count > 0 {
			covered += b.NumStmt
		}
	}
	return covered, total
}

// Percent renvoie la couverture du fichier (100 s'il n'a aucune instruction).
func (f *File) Percent() float64 { return percent(f.Stmts()) }

// Uncovered renvoie les blocs non couverts, triés par ligne.
func (f *File) Uncovered() []Block {
	var out []Block
	for _, b := range f.Blocks {
		if b.Count == 0 && b.NumStmt > 0 {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartLine < out[j].StartLine })
	return out
}

func percent(covered, total int) float64 {
	if total == 0 {
		return 100
	}
	return 100 * float64(covered) / float64(total)
}

// Profile regroupe la couverture de plusieurs fichiers.
type Profile struct {
	Mode  string
	Files map[string]*File
}

// New renvoie un profil vide.
func New() *Profile { return &Profile{Files: map[string]*File{}} }

// Parse lit un profil. Les blocs répétés (plusieurs paquets de test qui
// couvrent le même code) sont fusionnés en additionnant les compteurs.
func Parse(r io.Reader) (*Profile, error) {
	p := New()
	type key struct {
		file       string
		start, end string
	}
	index := map[key]int{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if mode, ok := strings.CutPrefix(line, "mode: "); ok {
			p.Mode = mode
			continue
		}
		// « fichier:12.34,15.2 3 1 » : le nom peut contenir « : », on coupe au dernier.
		name, after, found := strings.CutLast(line, ":")
		if !found {
			return nil, fmt.Errorf("ligne %d : format invalide", n)
		}
		rest := strings.Fields(after)
		if len(rest) != 3 {
			return nil, fmt.Errorf("ligne %d : format invalide", n)
		}
		span := strings.SplitN(rest[0], ",", 2)
		if len(span) != 2 {
			return nil, fmt.Errorf("ligne %d : position invalide", n)
		}
		startLine, err1 := lineOf(span[0])
		endLine, err2 := lineOf(span[1])
		stmts, err3 := strconv.Atoi(rest[1])
		count, err4 := strconv.Atoi(rest[2])
		if err := firstErr(err1, err2, err3, err4); err != nil {
			return nil, fmt.Errorf("ligne %d : %w", n, err)
		}

		f := p.Files[name]
		if f == nil {
			f = &File{Name: name}
			p.Files[name] = f
		}
		k := key{name, span[0], span[1]}
		if j, ok := index[k]; ok {
			f.Blocks[j].Count += count
			continue
		}
		index[k] = len(f.Blocks)
		f.Blocks = append(f.Blocks, Block{StartLine: startLine, EndLine: endLine, NumStmt: stmts, Count: count})
	}
	return p, sc.Err()
}

func lineOf(pos string) (int, error) {
	l, _, _ := strings.Cut(pos, ".")
	return strconv.Atoi(l)
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// Merge remplace, dans p, les paquets présents dans q par leur nouvelle
// couverture (relance d'un paquet sans effacer les autres).
func (p *Profile) Merge(q *Profile) {
	pkgs := map[string]bool{}
	for _, f := range q.Files {
		pkgs[f.Package()] = true
	}
	for name, f := range p.Files {
		if pkgs[f.Package()] {
			delete(p.Files, name)
		}
	}
	maps.Copy(p.Files, q.Files)
	if q.Mode != "" {
		p.Mode = q.Mode
	}
}

// PackageFiles renvoie les fichiers d'un paquet, du moins couvert au plus
// couvert.
func (p *Profile) PackageFiles(pkg string) []*File {
	var out []*File
	for _, f := range p.Files {
		if f.Package() == pkg {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].Percent(), out[j].Percent(); a != b {
			return a < b
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// PackagePercent renvoie la couverture d'un paquet ; ok vaut false si le
// profil ne le contient pas.
func (p *Profile) PackagePercent(pkg string) (pct float64, ok bool) {
	var covered, total int
	for _, f := range p.Files {
		if f.Package() == pkg {
			c, t := f.Stmts()
			covered, total, ok = covered+c, total+t, true
		}
	}
	return percent(covered, total), ok
}

// Total renvoie la couverture de tout le profil.
func (p *Profile) Total() float64 {
	var covered, total int
	for _, f := range p.Files {
		c, t := f.Stmts()
		covered, total = covered+c, total+t
	}
	return percent(covered, total)
}

// DiskPath traduit le nom d'un fichier du profil en chemin sur le disque,
// pour un fichier du module (chemin d'import commençant par module).
func DiskPath(root, module, name string) (string, bool) {
	rel, ok := strings.CutPrefix(name, module+"/")
	if !ok {
		return "", false
	}
	return filepath.Join(root, filepath.FromSlash(rel)), true
}
