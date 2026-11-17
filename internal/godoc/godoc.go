// Package godoc produit la documentation d'un paquet Go en Markdown, à partir
// de ses sources (go/parser, go/doc), comme « go doc » mais dans un format
// que Glamour sait afficher. Il ne dépend pas de l'interface.
package godoc

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/doc"
	"go/parser"
	"go/printer"
	"go/token"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Pkg est un paquet listé par « go list ».
type Pkg struct {
	ImportPath string
	Name       string
	Dir        string
	Synopsis   string // première phrase de la documentation
	Std        bool
}

// ListModule renvoie les paquets du module situé dans root.
func ListModule(ctx context.Context, root string) ([]Pkg, error) {
	return list(ctx, root, false, "./...")
}

// ListStd renvoie les paquets publics de la bibliothèque standard
// (sans internal/ ni vendor/).
func ListStd(ctx context.Context, root string) ([]Pkg, error) {
	pkgs, err := list(ctx, root, true, "std")
	out := pkgs[:0]
	for _, p := range pkgs {
		if !strings.Contains("/"+p.ImportPath+"/", "/internal/") && !strings.HasPrefix(p.ImportPath, "vendor/") {
			out = append(out, p)
		}
	}
	return out, err
}

func list(ctx context.Context, root string, std bool, pattern string) ([]Pkg, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "list", "-e", "-f", "{{.ImportPath}}\t{{.Name}}\t{{.Dir}}\t{{.Doc}}", pattern)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var pkgs []Pkg
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.SplitN(sc.Text(), "\t", 4)
		if len(f) < 4 || f[1] == "" {
			continue
		}
		pkgs = append(pkgs, Pkg{ImportPath: f[0], Name: f[1], Dir: f[2], Synopsis: f[3], Std: std})
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].ImportPath < pkgs[j].ImportPath })
	if err != nil && len(pkgs) == 0 {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			first, _, _ := strings.Cut(msg, "\n")
			return nil, errors.New(first)
		}
		return nil, err
	}
	return pkgs, nil
}

// ─── Documentation d'un paquet ───────────────────────────────────────────────

// Kind est la nature d'un symbole.
type Kind string

const (
	Const  Kind = "const"
	Var    Kind = "var"
	Func   Kind = "func"
	Type   Kind = "type"
	Method Kind = "method"
)

// Symbol est un élément exporté du paquet.
type Symbol struct {
	Name   string // « Fonction », « Type » ou « Type.Méthode »
	Kind   Kind
	Parent string // type de rattachement (constructeurs et méthodes)
	File   string // emplacement de la déclaration
	Line   int
}

// Doc est la documentation d'un paquet.
type Doc struct {
	Pkg     Pkg
	Symbols []Symbol // dans l'ordre de go doc : constantes, variables, fonctions, types
	d       *doc.Package
	fset    *token.FileSet
}

// Load lit les sources du paquet (fichiers retenus par les contraintes de
// compilation de la plateforme courante, hors tests).
func Load(p Pkg) (*Doc, error) {
	bp, err := build.Default.ImportDir(p.Dir, build.ImportComment)
	if err != nil {
		if _, noGo := errors.AsType[*build.NoGoError](err); !noGo {
			return nil, err
		}
	}
	fset := token.NewFileSet()
	var files []*ast.File
	if bp != nil {
		for _, name := range append(append([]string{}, bp.GoFiles...), bp.CgoFiles...) {
			f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, parser.ParseComments)
			if err != nil {
				return nil, err
			}
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s : aucun fichier Go pour cette plateforme", p.ImportPath)
	}
	dp, err := doc.NewFromFiles(fset, files, p.ImportPath)
	if err != nil {
		return nil, err
	}

	d := &Doc{Pkg: p, d: dp, fset: fset}
	add := func(name string, k Kind, parent string, pos token.Pos) {
		position := fset.Position(pos)
		d.Symbols = append(d.Symbols, Symbol{Name: name, Kind: k, Parent: parent, File: position.Filename, Line: position.Line})
	}
	for _, v := range dp.Consts {
		add(strings.Join(v.Names, ", "), Const, "", v.Decl.Pos())
	}
	for _, v := range dp.Vars {
		add(strings.Join(v.Names, ", "), Var, "", v.Decl.Pos())
	}
	for _, f := range dp.Funcs {
		add(f.Name, Func, "", f.Decl.Pos())
	}
	for _, t := range dp.Types {
		add(t.Name, Type, "", t.Decl.Pos())
		for _, f := range t.Funcs {
			add(f.Name, Func, t.Name, f.Decl.Pos())
		}
		for _, f := range t.Methods {
			add(t.Name+"."+f.Name, Method, t.Name, f.Decl.Pos())
		}
	}
	return d, nil
}

// Find renvoie le symbole nommé, ou nil.
func (d *Doc) Find(name string) *Symbol {
	for i := range d.Symbols {
		if d.Symbols[i].Name == name {
			return &d.Symbols[i]
		}
	}
	return nil
}

// ─── Markdown ────────────────────────────────────────────────────────────────

// Overview renvoie la vue d'ensemble du paquet : documentation et index.
func (d *Doc) Overview() string {
	var b strings.Builder
	p := d.d
	fmt.Fprintf(&b, "# package %s\n\n`import %q`\n\n", p.Name, d.Pkg.ImportPath)
	if doc := strings.TrimSpace(p.Doc); doc != "" {
		b.Write(p.Markdown(doc))
		b.WriteString("\n")
	} else {
		b.WriteString("*Paquet non documenté.*\n\n")
	}

	section := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		fmt.Fprintf(&b, "## %s\n\n```go\n%s\n```\n\n", title, strings.Join(lines, "\n"))
	}
	var consts, vars, funcs, types []string
	for _, v := range p.Consts {
		consts = append(consts, firstLine(d.node(v.Decl)))
	}
	for _, v := range p.Vars {
		vars = append(vars, firstLine(d.node(v.Decl)))
	}
	for _, f := range p.Funcs {
		funcs = append(funcs, d.node(f.Decl))
	}
	for _, t := range p.Types {
		types = append(types, firstLine(d.node(t.Decl)))
		for _, f := range t.Funcs {
			types = append(types, "    "+d.node(f.Decl))
		}
		for _, f := range t.Methods {
			types = append(types, "    "+d.node(f.Decl))
		}
	}
	section("Constantes", consts)
	section("Variables", vars)
	section("Fonctions", funcs)
	section("Types", types)
	return b.String()
}

// Symbol renvoie la documentation d'un symbole : déclaration, commentaire
// et, pour un type, ses constructeurs et méthodes.
func (d *Doc) Symbol(name string) string {
	p := d.d
	var b strings.Builder
	entry := func(title string, decl ast.Node, comment string, level int) {
		fmt.Fprintf(&b, "%s %s\n\n```go\n%s\n```\n\n", strings.Repeat("#", level), title, d.node(decl))
		if c := strings.TrimSpace(comment); c != "" {
			b.Write(p.Markdown(c))
			b.WriteString("\n")
		}
	}

	for _, v := range append(append([]*doc.Value{}, p.Consts...), p.Vars...) {
		if strings.Join(v.Names, ", ") == name {
			entry(name, v.Decl, v.Doc, 1)
			return b.String()
		}
	}
	for _, f := range p.Funcs {
		if f.Name == name {
			entry("func "+name, f.Decl, f.Doc, 1)
			return b.String()
		}
	}
	for _, t := range p.Types {
		for _, f := range t.Funcs {
			if f.Name == name {
				entry("func "+name, f.Decl, f.Doc, 1)
				return b.String()
			}
		}
		for _, f := range t.Methods {
			if t.Name+"."+f.Name == name {
				entry("func ("+t.Name+") "+f.Name, f.Decl, f.Doc, 1)
				return b.String()
			}
		}
		if t.Name != name {
			continue
		}
		entry("type "+name, t.Decl, t.Doc, 1)
		for _, v := range append(append([]*doc.Value{}, t.Consts...), t.Vars...) {
			fmt.Fprintf(&b, "```go\n%s\n```\n\n", d.node(v.Decl))
			if c := strings.TrimSpace(v.Doc); c != "" {
				b.Write(p.Markdown(c))
				b.WriteString("\n")
			}
		}
		for _, f := range t.Funcs {
			entry("func "+f.Name, f.Decl, f.Doc, 2)
		}
		for _, f := range t.Methods {
			entry("func ("+t.Name+") "+f.Name, f.Decl, f.Doc, 2)
		}
		return b.String()
	}
	return fmt.Sprintf("*Symbole %s introuvable.*\n", name)
}

// node imprime une déclaration (sans corps de fonction : go/doc les retire).
func (d *Doc) node(n ast.Node) string {
	var buf bytes.Buffer
	cfg := printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 4}
	if err := cfg.Fprint(&buf, d.fset, n); err != nil {
		return fmt.Sprintf("/* %v */", err)
	}
	return buf.String()
}

// firstLine résume une déclaration de plusieurs lignes
// (« type T struct { … } », « const ( … ) »).
func firstLine(s string) string {
	first, rest, ok := strings.Cut(s, "\n")
	if !ok {
		return s
	}
	first = strings.TrimRight(first, " ")
	switch {
	case strings.HasSuffix(first, "{"):
		return first + " … }"
	case strings.HasSuffix(first, "("):
		// Bloc de constantes ou de variables : lister les noms.
		var names []string
		for line := range strings.SplitSeq(rest, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || line == ")" || strings.HasPrefix(line, "//") {
				continue
			}
			name, _, _ := strings.Cut(line, " ")
			names = append(names, strings.TrimSuffix(name, ","))
		}
		return first + " " + strings.Join(names, ", ") + " )"
	}
	return first + " …"
}
