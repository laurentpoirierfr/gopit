package code

import (
	"go/scanner"
	"go/token"
)

// class est la catégorie syntaxique d'un caractère (couleur à l'affichage).
type class uint8

const (
	clText class = iota
	clKeyword
	clString
	clNumber
	clComment
	clFunc
	clBuiltin
	clHeading // titre Markdown
	clAdded   // ligne ajoutée (diff)
	clRemoved // ligne retirée (diff)
)

// builtins regroupe les identificateurs prédéclarés de Go (types, constantes
// et fonctions).
var builtins = map[string]bool{
	"any": true, "bool": true, "byte": true, "comparable": true, "complex64": true,
	"complex128": true, "error": true, "float32": true, "float64": true, "int": true,
	"int8": true, "int16": true, "int32": true, "int64": true, "rune": true,
	"string": true, "uint": true, "uint8": true, "uint16": true, "uint32": true,
	"uint64": true, "uintptr": true,
	"true": true, "false": true, "nil": true, "iota": true,
	"append": true, "cap": true, "clear": true, "close": true, "complex": true,
	"copy": true, "delete": true, "imag": true, "len": true, "make": true,
	"max": true, "min": true, "new": true, "panic": true, "print": true,
	"println": true, "real": true, "recover": true,
}

// highlightGo renvoie la catégorie de chaque rune, ligne par ligne, d'un
// source Go (lignes séparées par « \n »). Le source peut être incomplet ou
// invalide : c'est le cas pendant la frappe.
func highlightGo(src string) [][]class {
	cls := make([]class, len(src)) // par octet
	fset := token.NewFileSet()
	f := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(f, []byte(src), func(token.Position, string) {}, scanner.ScanComments)

	prevIdent, prevLen := -1, 0 // identificateur précédent (appel de fonction)
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.SEMICOLON && lit == "\n" { // point-virgule implicite
			prevIdent = -1
			continue
		}
		off := f.Offset(pos)
		n := len(lit)
		if n == 0 {
			n = len(tok.String())
		}
		n = min(n, len(src)-off)

		c := clText
		switch {
		case tok.IsKeyword():
			c = clKeyword
		case tok == token.STRING || tok == token.CHAR:
			c = clString
		case tok == token.INT || tok == token.FLOAT || tok == token.IMAG:
			c = clNumber
		case tok == token.COMMENT:
			c = clComment
		case tok == token.IDENT && builtins[lit]:
			c = clBuiltin
		case tok == token.LPAREN && prevIdent >= 0:
			// « nom( » : appel ou déclaration de fonction.
			if cls[prevIdent] != clBuiltin {
				for i := prevIdent; i < prevIdent+prevLen; i++ {
					cls[i] = clFunc
				}
			}
		}
		for i := off; i < off+n; i++ {
			cls[i] = c
		}
		if tok == token.IDENT {
			prevIdent, prevLen = off, n
		} else {
			prevIdent = -1
		}
	}

	// Passage des octets aux runes, ligne par ligne.
	var out [][]class
	var line []class
	for i, r := range src {
		if r == '\n' {
			out = append(out, line)
			line = nil
			continue
		}
		line = append(line, cls[i])
	}
	return append(out, line)
}
