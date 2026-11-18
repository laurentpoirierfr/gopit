package code

// Coloration des fichiers autres que Go : la langue se déduit du nom du
// fichier (extension, Makefile, Dockerfile…) ou de sa ligne #!, jamais du
// contenu seul (trop peu fiable) ; chroma (déjà embarqué par Glamour)
// découpe le texte. go.mod, go.sum et go.work ont leur propre coloration
// (chroma y verrait du Modula-2).

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// maxHighlightLines : au-delà, un fichier non Go n'est pas coloré (la
// frappe resterait fluide de toute façon, mais un gros fichier de données
// n'y gagne rien).
const maxHighlightLines = 20000

// language est la manière de colorer un fichier.
type language struct {
	name  string                     // affiché dans la barre d'état ("" : texte brut)
	color func(src string) [][]class // nil : pas de coloration
}

// detectLanguage choisit la coloration d'un fichier d'après son nom, et
// sinon d'après sa première ligne (#!/bin/sh…).
func detectLanguage(path, firstLine string) language {
	base := filepath.Base(path)
	switch {
	case strings.HasSuffix(base, ".go"):
		return language{"Go", highlightGo}
	case base == "go.mod" || base == "go.work":
		return language{base, highlightGoMod}
	case base == "go.sum" || base == "go.work.sum":
		return language{"go.sum", highlightGoMod}
	}
	lexer := lexers.Match(base)
	if strings.HasSuffix(base, ".sql") {
		lexer = lexers.Get("sql") // plutôt que le dialecte MySQL choisi par défaut
	}
	if lexer == nil {
		lexer = shebangLexer(firstLine)
	}
	if lexer == nil {
		return language{}
	}
	lexer = chroma.Coalesce(lexer)
	return language{lexer.Config().Name, func(src string) [][]class { return highlightChroma(lexer, src) }}
}

// shebangLexer reconnaît l'interpréteur d'une ligne #! (« #!/bin/sh -e »,
// « #!/usr/bin/env bash », « #!/usr/bin/env -S python3 -u »).
func shebangLexer(first string) chroma.Lexer {
	rest, ok := strings.CutPrefix(first, "#!")
	if !ok {
		return nil
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return nil
	}
	prog := filepath.Base(fields[0])
	if prog == "env" {
		prog = ""
		for _, f := range fields[1:] {
			if !strings.HasPrefix(f, "-") {
				prog = filepath.Base(f)
				break
			}
		}
	}
	prog = strings.TrimRight(prog, "0123456789.") // python3 → python
	switch prog {
	case "sh", "dash", "ksh":
		prog = "bash"
	case "node":
		prog = "javascript"
	}
	return lexers.Get(prog)
}

// highlightChroma découpe src avec chroma et renvoie la catégorie de chaque
// rune, ligne par ligne.
func highlightChroma(lexer chroma.Lexer, src string) [][]class {
	if strings.Count(src, "\n") >= maxHighlightLines {
		return nil
	}
	it, err := lexer.Tokenise(nil, src)
	if err != nil {
		return nil
	}
	out := [][]class{nil}
	for tok := it(); tok != chroma.EOF; tok = it() {
		c := chromaClass(tok.Type)
		for _, r := range tok.Value {
			if r == '\n' {
				out = append(out, nil)
				continue
			}
			out[len(out)-1] = append(out[len(out)-1], c)
		}
	}
	return out
}

// chromaClass ramène un type de jeton de chroma aux catégories de
// l'éditeur (mêmes couleurs que pour Go).
func chromaClass(t chroma.TokenType) class {
	switch {
	case t == chroma.GenericHeading || t == chroma.GenericSubheading:
		return clHeading
	case t == chroma.GenericInserted:
		return clAdded
	case t == chroma.GenericDeleted:
		return clRemoved
	case t == chroma.CommentPreproc || t == chroma.CommentPreprocFile:
		return clKeyword
	case t.InCategory(chroma.Comment):
		return clComment
	case t == chroma.KeywordType:
		return clBuiltin
	case t.InCategory(chroma.Keyword), t == chroma.NameTag:
		return clKeyword // mots-clés, balises HTML/XML, clés YAML/JSON
	case t.InSubCategory(chroma.LiteralString):
		return clString
	case t.InSubCategory(chroma.LiteralNumber), t == chroma.NameConstant:
		return clNumber
	case t == chroma.NameFunction || t == chroma.NameFunctionMagic:
		return clFunc
	case t == chroma.NameBuiltin || t == chroma.NameBuiltinPseudo || t == chroma.NameClass ||
		t == chroma.NameAttribute || t.InSubCategory(chroma.NameVariable):
		return clBuiltin // types, attributs, variables ($HOME, $(CC)…)
	}
	return clText
}

// goModDirectives sont les mots-clés de go.mod et go.work.
var goModDirectives = map[string]bool{
	"module": true, "go": true, "toolchain": true, "require": true, "replace": true,
	"exclude": true, "retract": true, "use": true, "godebug": true, "tool": true, "ignore": true,
}

var (
	goModVersion = regexp.MustCompile(`^v\d+\.\d+\.\d+\S*$|^\d+\.\d+(\.\d+)?\S*$`)
	goSumHash    = regexp.MustCompile(`^h1:\S+$`)
)

// highlightGoMod colore go.mod, go.work et go.sum : directives, versions,
// sommes de contrôle, chaînes et commentaires.
func highlightGoMod(src string) [][]class {
	lines := strings.Split(src, "\n")
	out := make([][]class, len(lines))
	for i, l := range lines {
		rs := []rune(l)
		cls := make([]class, len(rs))
		code := rs
		if c := strings.Index(l, "//"); c >= 0 {
			cc := len([]rune(l[:c]))
			for j := cc; j < len(rs); j++ {
				cls[j] = clComment
			}
			code = rs[:cc]
		}
		first := true
		for j := 0; j < len(code); {
			if code[j] == ' ' || code[j] == '\t' {
				j++
				continue
			}
			k := j
			if code[j] == '"' || code[j] == '`' {
				q := code[j]
				for k++; k < len(code) && code[k] != q; k++ {
				}
				k = min(k+1, len(code))
				fill(cls, j, k, clString)
			} else {
				for k < len(code) && code[k] != ' ' && code[k] != '\t' {
					k++
				}
				word := string(code[j:k])
				switch {
				case first && goModDirectives[word]:
					fill(cls, j, k, clKeyword)
				case goModVersion.MatchString(strings.TrimSuffix(word, "/go.mod")):
					fill(cls, j, k, clNumber)
				case goSumHash.MatchString(word):
					fill(cls, j, k, clString)
				}
			}
			first = false
			j = k
		}
		out[i] = cls
	}
	return out
}

func fill(cls []class, from, to int, c class) {
	for i := from; i < to; i++ {
		cls[i] = c
	}
}
