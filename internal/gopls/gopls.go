// Package gopls interroge gopls en ligne de commande pour l'éditeur :
// définition d'un symbole et sa documentation (gopls definition -json).
//
// gopls lit les fichiers sur le disque : la position demandée doit
// correspondre au fichier enregistré.
package gopls

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"unicode/utf8"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
)

// Install est la commande qui installe gopls.
const Install = "go install golang.org/x/tools/gopls@latest"

// ErrMissing est renvoyée quand gopls n'est pas installé.
var ErrMissing = i18n.Error("gopls n'est pas installé")

// ErrNoIdent est renvoyée quand aucun identifiant ne se trouve à la
// position demandée (espace, mot-clé, commentaire…).
var ErrNoIdent = i18n.Error("Aucun symbole à cet endroit")

// Definition est l'emplacement où un symbole est déclaré.
type Definition struct {
	File   string // chemin absolu
	Offset int    // position en octets dans File
	Doc    string // signature, puis commentaire de documentation
}

// Define renvoie la définition du symbole situé à offset (en octets) dans
// file, en lançant gopls depuis root.
func Define(ctx context.Context, root, file string, offset int) (*Definition, error) {
	if _, err := exec.LookPath("gopls"); err != nil {
		return nil, ErrMissing
	}
	cmd := exec.CommandContext(ctx, "gopls", "definition", "-json", fmt.Sprintf("%s:#%d", file, offset))
	cmd.Dir = root
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if strings.Contains(msg, "no identifier found") || strings.Contains(msg, "not an identifier") {
			return nil, ErrNoIdent
		}
		if msg == "" {
			msg = err.Error()
		}
		if i := strings.LastIndex(msg, "\n"); i >= 0 { // la dernière ligne porte la cause
			msg = msg[i+1:]
		}
		return nil, errors.New(msg)
	}
	return ParseDefinition(out.Bytes())
}

// ParseDefinition lit la sortie de « gopls definition -json ».
func ParseDefinition(data []byte) (*Definition, error) {
	var v struct {
		Span struct {
			URI   string `json:"uri"`
			Start struct {
				Offset int `json:"offset"`
			} `json:"start"`
		} `json:"span"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf(i18n.T("sortie de gopls illisible : %w"), err)
	}
	u, err := url.Parse(v.Span.URI)
	if err != nil || u.Scheme != "file" {
		return nil, fmt.Errorf(i18n.T("sortie de gopls illisible : %w"), fmt.Errorf("uri %q", v.Span.URI))
	}
	return &Definition{File: u.Path, Offset: v.Span.Start.Offset, Doc: strings.TrimSpace(v.Description)}, nil
}

// LineCol convertit une position en octets en ligne et colonne (en runes),
// à partir de 0, comme le tampon de l'éditeur.
func LineCol(src []byte, offset int) (line, col int) {
	offset = min(max(offset, 0), len(src))
	before := src[:offset]
	line = bytes.Count(before, []byte("\n"))
	start := bytes.LastIndexByte(before, '\n') + 1
	return line, utf8.RuneCount(before[start:])
}

// Offset fait l'inverse de LineCol pour un texte en lignes (sans les fins
// de ligne) : position en octets de la colonne col (en runes) de la ligne.
func Offset(lines []string, line, col int) int {
	off := 0
	for i := 0; i < line && i < len(lines); i++ {
		off += len(lines[i]) + 1
	}
	if line < len(lines) {
		l := lines[line]
		for i := 0; i < col && l != ""; i++ {
			_, size := utf8.DecodeRuneInString(l)
			off += size
			l = l[size:]
		}
	}
	return off
}
