// Package diag rassemble les problèmes signalés par les outils d'analyse Go
// (go vet, staticcheck, gopls, gofmt) dans une liste commune, sans doublons.
// Il ne dépend pas de l'interface et peut être testé seul.
package diag

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Severity classe les diagnostics, du plus grave au moins grave.
type Severity int

const (
	Error   Severity = iota // ne compile pas
	Warning                 // bogue probable (vet, staticcheck SA…, gopls)
	Info                    // style, simplification, formatage
)

func (s Severity) String() string {
	switch s {
	case Error:
		return "erreur"
	case Warning:
		return "avertissement"
	}
	return "info"
}

// Diagnostic est un problème à un emplacement du code.
type Diagnostic struct {
	File     string // chemin absolu
	Line     int
	Col      int
	Message  string
	Code     string   // analyseur ou règle (printf, SA4000…), si connu
	Tools    []string // outils qui l'ont signalé
	Severity Severity

	// guessed indique une gravité supposée (gopls ne la donne pas) : à la
	// fusion, celle d'un outil qui la connaît l'emporte.
	guessed bool
}

// Noms des outils.
const (
	ToolVet         = "vet"
	ToolStaticcheck = "staticcheck"
	ToolGopls       = "gopls"
	ToolGofmt       = "gofmt"
)

// Tool décrit un outil d'analyse.
type Tool struct {
	Name    string
	Binary  string // exécutable recherché dans le PATH
	Install string // commande d'installation, si l'outil est optionnel
}

// Tools liste les outils dans l'ordre d'affichage.
var Tools = []Tool{
	{Name: ToolVet, Binary: "go"},
	{Name: ToolStaticcheck, Binary: "staticcheck", Install: "go install honnef.co/go/tools/cmd/staticcheck@latest"},
	{Name: ToolGopls, Binary: "gopls", Install: "go install golang.org/x/tools/gopls@latest"},
	{Name: ToolGofmt, Binary: "gofmt"},
}

// Available indique si l'outil est installé.
func (t Tool) Available() bool {
	_, err := exec.LookPath(t.Binary)
	return err == nil
}

// ErrMissing est renvoyée par Run quand l'outil n'est pas installé.
var ErrMissing = errors.New("non installé")

// Run lance l'outil sur le module situé dans root.
func (t Tool) Run(ctx context.Context, root string) ([]Diagnostic, error) {
	if !t.Available() {
		return nil, ErrMissing
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	switch t.Name {
	case ToolVet:
		out, errOut, err := run(ctx, root, "go", "vet", "-json", "./...")
		diags, perr := ParseVet(out, errOut, root)
		return diags, firstErr(perr, exitErr(err, errOut, len(diags) > 0))
	case ToolStaticcheck:
		out, errOut, err := run(ctx, root, "staticcheck", "-f", "json", "./...")
		diags, perr := ParseStaticcheck(out, root)
		return diags, firstErr(perr, exitErr(err, errOut, len(diags) > 0))
	case ToolGopls:
		files, err := GoFiles(root)
		if err != nil || len(files) == 0 {
			return nil, err
		}
		out, errOut, err := run(ctx, root, "gopls", append([]string{"check", "-severity=info"}, files...)...)
		diags := ParseGopls(out, root)
		return diags, exitErr(err, errOut, len(diags) > 0)
	case ToolGofmt:
		files, err := GoFiles(root)
		if err != nil || len(files) == 0 {
			return nil, err
		}
		out, errOut, err := run(ctx, root, "gofmt", append([]string{"-l"}, files...)...)
		diags := ParseGofmt(out, root)
		return diags, exitErr(err, errOut, len(diags) > 0)
	}
	return nil, fmt.Errorf("outil inconnu : %s", t.Name)
}

func run(ctx context.Context, dir, name string, args ...string) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	err = cmd.Run()
	return o.Bytes(), e.Bytes(), err
}

// exitErr transforme l'échec de la commande en erreur lisible. Un code de
// sortie non nul accompagné de diagnostics est normal (problèmes trouvés).
func exitErr(err error, stderr []byte, found bool) error {
	if err == nil {
		return nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && found {
		return nil
	}
	if msg := strings.TrimSpace(string(stderr)); msg != "" {
		first, _, _ := strings.Cut(msg, "\n")
		return errors.New(first)
	}
	return err
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// GoFiles liste les fichiers .go du module (chemins relatifs à root), hors
// vendor, testdata et répertoires cachés.
func GoFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "vendor" || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".go") {
			rel, _ := filepath.Rel(root, p)
			files = append(files, rel)
		}
		return nil
	})
	return files, err
}

// ─── Analyse des sorties ─────────────────────────────────────────────────────

// locRe repère « fichier.go:ligne:colonne: message » (avec une éventuelle
// plage « -fin » après la colonne, comme dans la sortie de gopls).
var locRe = regexp.MustCompile(`^(?:vet: )?(.+?\.go):(\d+):(\d+)(?:-[\d:]+)?: (.*)$`)

func abs(root, file string) string {
	if file == "" || filepath.IsAbs(file) {
		return file
	}
	return filepath.Join(root, file)
}

// parseLoc analyse une ligne « fichier:l:c: message ».
func parseLoc(line, root string) (Diagnostic, bool) {
	m := locRe.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return Diagnostic{}, false
	}
	l, _ := strconv.Atoi(m[2])
	c, _ := strconv.Atoi(m[3])
	return Diagnostic{File: abs(root, m[1]), Line: l, Col: c, Message: m[4]}, true
}

// parsePosn analyse « fichier:l:c » (champ posn de go vet).
func parsePosn(posn, root string) (file string, line, col int) {
	parts := strings.Split(posn, ":")
	if n := len(parts); n >= 3 {
		line, _ = strconv.Atoi(parts[n-2])
		col, _ = strconv.Atoi(parts[n-1])
		file = strings.Join(parts[:n-2], ":")
	} else {
		file = posn
	}
	return abs(root, file), line, col
}

// compileErrors extrait les erreurs de compilation d'un texte.
func compileErrors(text []byte, root, tool string) []Diagnostic {
	var out []Diagnostic
	for line := range strings.SplitSeq(string(text), "\n") {
		if d, ok := parseLoc(line, root); ok {
			d.Severity, d.Code, d.Tools = Error, "compilation", []string{tool}
			out = append(out, d)
		}
	}
	return out
}

// ParseVet lit la sortie de « go vet -json » : un objet JSON par paquet sur la
// sortie standard, les erreurs de compilation en texte sur la sortie d'erreur.
func ParseVet(stdout, stderr []byte, root string) ([]Diagnostic, error) {
	type finding struct {
		Posn    string `json:"posn"`
		Message string `json:"message"`
	}
	var out []Diagnostic
	dec := json.NewDecoder(bytes.NewReader(stdout))
	for {
		var pkgs map[string]map[string]json.RawMessage
		if err := dec.Decode(&pkgs); err != nil {
			if err.Error() == "EOF" {
				break
			}
			return out, fmt.Errorf("sortie de go vet illisible : %w", err)
		}
		for _, analyzers := range pkgs {
			for name, raw := range analyzers {
				var fs []finding
				if json.Unmarshal(raw, &fs) != nil {
					continue // {"error": …} : analyseur en échec, ignoré
				}
				for _, f := range fs {
					file, line, col := parsePosn(f.Posn, root)
					out = append(out, Diagnostic{File: file, Line: line, Col: col, Message: f.Message,
						Code: name, Tools: []string{ToolVet}, Severity: Warning})
				}
			}
		}
	}
	return append(out, compileErrors(stderr, root, ToolVet)...), nil
}

// ParseStaticcheck lit la sortie de « staticcheck -f json » (une ligne JSON
// par diagnostic).
func ParseStaticcheck(stdout []byte, root string) ([]Diagnostic, error) {
	type pos struct {
		File   string `json:"file"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
	}
	type item struct {
		Code     string `json:"code"`
		Location pos    `json:"location"`
		Message  string `json:"message"`
	}
	var out []Diagnostic
	sc := bufio.NewScanner(bytes.NewReader(stdout))
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var it item
		if err := json.Unmarshal(line, &it); err != nil {
			return out, fmt.Errorf("sortie de staticcheck illisible : %w", err)
		}
		if it.Code == "compile" {
			out = append(out, compileErrors([]byte(it.Message), root, ToolStaticcheck)...)
			continue
		}
		sev := Info // S… (simplifications), ST… (style), QF… (corrections rapides)
		if strings.HasPrefix(it.Code, "SA") {
			sev = Warning // SA… : bogues probables
		}
		out = append(out, Diagnostic{File: abs(root, it.Location.File), Line: it.Location.Line,
			Col: it.Location.Column, Message: it.Message, Code: it.Code,
			Tools: []string{ToolStaticcheck}, Severity: sev})
	}
	return out, sc.Err()
}

// ParseGopls lit la sortie de « gopls check » (une ligne par diagnostic,
// sans nom d'analyseur).
func ParseGopls(stdout []byte, root string) []Diagnostic {
	var out []Diagnostic
	for line := range strings.SplitSeq(string(stdout), "\n") {
		if d, ok := parseLoc(line, root); ok {
			d.Tools, d.Severity, d.guessed = []string{ToolGopls}, Warning, true
			out = append(out, d)
		}
	}
	return out
}

// ParseGofmt lit la sortie de « gofmt -l » (un fichier mal formaté par ligne).
func ParseGofmt(stdout []byte, root string) []Diagnostic {
	var out []Diagnostic
	for line := range strings.SplitSeq(string(stdout), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, Diagnostic{File: abs(root, line), Line: 1, Col: 1,
				Message: "fichier mal formaté (gofmt -w pour corriger)", Code: "gofmt",
				Tools: []string{ToolGofmt}, Severity: Info})
		}
	}
	return out
}

// ─── Fusion ──────────────────────────────────────────────────────────────────

// Merge fusionne les diagnostics de plusieurs outils : un même problème
// (fichier, ligne, message) n'apparaît qu'une fois, avec la liste des outils
// qui l'ont signalé et la gravité la plus haute. Le résultat est trié par
// fichier puis par ligne.
func Merge(lists ...[]Diagnostic) []Diagnostic {
	byKey := map[string]int{}
	var out []Diagnostic
	for _, list := range lists {
		for _, d := range list {
			key := fmt.Sprintf("%s:%d:%s", d.File, d.Line, d.Message)
			i, ok := byKey[key]
			if !ok {
				byKey[key] = len(out)
				d.Tools = append([]string(nil), d.Tools...)
				out = append(out, d)
				continue
			}
			m := &out[i]
			for _, t := range d.Tools {
				if !slices.Contains(m.Tools, t) {
					m.Tools = append(m.Tools, t)
				}
			}
			switch {
			case m.guessed && !d.guessed:
				m.Severity, m.guessed = d.Severity, false
			case d.guessed && !m.guessed:
				// garder la gravité connue
			default:
				m.Severity = min(m.Severity, d.Severity)
			}
			if m.Code == "" {
				m.Code = d.Code
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})
	return out
}

// Counts compte les diagnostics par gravité.
func Counts(ds []Diagnostic) (errs, warns, infos int) {
	for _, d := range ds {
		switch d.Severity {
		case Error:
			errs++
		case Warning:
			warns++
		default:
			infos++
		}
	}
	return
}
