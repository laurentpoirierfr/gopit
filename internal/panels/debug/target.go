package debug

// Cible automatique : quand aucune cible n'est choisie (c), F5 déduit quoi
// déboguer des paquets du module et du fichier affiché dans l'éditeur.

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
)

// errNoTarget : ni programme ni fichier de test pour choisir.
var errNoTarget = i18n.Error("Aucun programme (package main) dans le module : ouvre un fichier _test.go pour déboguer ses tests, ou indique une cible avec c (ex. « test ./pkg »)")

type pkgInfo struct {
	name, dir string
}

// listPackages renvoie le nom et le répertoire des paquets du module.
func listPackages(ctx context.Context, root string) ([]pkgInfo, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-e", "-f", "{{.Name}}\t{{.Dir}}", "./...")
	cmd.Dir = root
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errOut.String()); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, err
	}
	var pkgs []pkgInfo
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if name, dir, ok := strings.Cut(l, "\t"); ok && name != "" {
			pkgs = append(pkgs, pkgInfo{name, dir})
		}
	}
	return pkgs, nil
}

// relTarget écrit un répertoire de paquet comme une cible (« . », « ./cmd/x »).
func relTarget(root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." {
		return "."
	}
	return "./" + filepath.ToSlash(rel)
}

// resolveTarget choisit quoi déboguer : le programme ou les tests du
// fichier affiché, sinon le seul programme du module ; s'il y en a
// plusieurs, choices les propose.
func resolveTarget(pkgs []pkgInfo, root, current string) (target string, choices []string, err error) {
	if current != "" {
		dir := filepath.Dir(current)
		for _, p := range pkgs {
			if p.dir != dir {
				continue
			}
			switch {
			case strings.HasSuffix(current, "_test.go"):
				return "test " + relTarget(root, dir), nil, nil
			case p.name == "main":
				return relTarget(root, dir), nil, nil
			}
		}
	}
	var mains []string
	for _, p := range pkgs {
		if p.name == "main" {
			mains = append(mains, relTarget(root, p.dir))
		}
	}
	switch len(mains) {
	case 0:
		return "", nil, errNoTarget
	case 1:
		return mains[0], nil, nil
	}
	return "", mains, nil
}

// buildError extrait de la sortie de la compilation la ligne qui explique
// l'échec (« ./main.go:3:15: undefined: x », « no Go files in … »).
func buildError(lines []string) string {
	for _, l := range lines {
		l = strings.TrimSpace(l)
		switch {
		case l == "", strings.HasPrefix(l, "Building "), strings.HasPrefix(l, "Build Error: go build"), strings.HasPrefix(l, "# "):
			continue
		}
		return strings.TrimSuffix(l, " (exit status 1)")
	}
	return ""
}
