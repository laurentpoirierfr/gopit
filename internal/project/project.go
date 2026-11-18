// Package project détecte le module Go sur lequel gopit travaille.
package project

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/mod/modfile"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
)

// Project décrit un module Go.
type Project struct {
	Root      string // répertoire qui contient go.mod
	GoMod     string // chemin de go.mod
	Module    string // chemin du module (ex. github.com/moi/projet)
	GoVersion string // directive « go » de go.mod
	Toolchain string // directive « toolchain » (vide si absente)
	Requires  int    // nombre de dépendances directes
	Indirect  int    // nombre de dépendances indirectes
}

// Name renvoie le nom court du projet (dernier élément du chemin du module).
func (p *Project) Name() string {
	return filepath.Base(p.Module)
}

// ErrNoModule est renvoyée quand aucun go.mod n'est trouvé.
var ErrNoModule = i18n.Error("aucun go.mod trouvé (gopit doit être lancé dans un module Go)")

// Detect remonte depuis dir jusqu'au premier go.mod et le lit.
func Detect(dir string) (*Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(abs); err != nil {
		return nil, err
	} else if !st.IsDir() {
		return nil, fmt.Errorf(i18n.T("%s : ce n'est pas un répertoire"), dir)
	}

	for d := abs; ; d = filepath.Dir(d) {
		gomod := filepath.Join(d, "go.mod")
		if _, err := os.Stat(gomod); err == nil {
			return Load(gomod)
		}
		if filepath.Dir(d) == d {
			return nil, ErrNoModule
		}
	}
}

// Load lit un fichier go.mod.
func Load(gomod string) (*Project, error) {
	data, err := os.ReadFile(gomod)
	if err != nil {
		return nil, err
	}
	// Parse lit toutes les directives (dont toolchain) ; ParseLax, plus
	// tolérant mais qui en ignore certaines, sert de repli.
	f, err := modfile.Parse(gomod, data, nil)
	if err != nil {
		if f, err = modfile.ParseLax(gomod, data, nil); err != nil {
			return nil, fmt.Errorf("%s : %w", gomod, err)
		}
	}
	if f.Module == nil {
		return nil, fmt.Errorf(i18n.T("%s : directive « module » absente"), gomod)
	}

	p := &Project{Root: filepath.Dir(gomod), GoMod: gomod, Module: f.Module.Mod.Path}
	if f.Go != nil {
		p.GoVersion = f.Go.Version
	}
	if f.Toolchain != nil {
		p.Toolchain = f.Toolchain.Name
	}
	for _, r := range f.Require {
		if r.Indirect {
			p.Indirect++
		} else {
			p.Requires++
		}
	}
	return p, nil
}
