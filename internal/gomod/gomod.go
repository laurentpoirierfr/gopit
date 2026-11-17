// Package gomod interroge la chaîne d'outils Go sur les dépendances d'un
// module : liste (go list -m), mises à jour (-u), raison de leur présence
// (go mod why) et vulnérabilités connues (govulncheck). Il ne dépend pas de
// l'interface et peut être testé seul.
package gomod

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// StdlibPath est le nom donné par govulncheck à la bibliothèque standard.
const StdlibPath = "stdlib"

// Version est une version de module publiée.
type Version struct {
	Path    string
	Version string
	Time    *time.Time
}

// Module est une dépendance (sortie de « go list -m -json »).
type Module struct {
	Path       string
	Version    string
	Time       *time.Time
	Main       bool
	Indirect   bool
	Dir        string
	GoVersion  string
	Update     *Version // avec -u : version plus récente disponible
	Replace    *Module
	Deprecated string
	Retracted  []string
	Error      *struct{ Err string }
}

// Direct indique une dépendance déclarée sans « // indirect » dans go.mod.
func (m *Module) Direct() bool { return !m.Main && !m.Indirect }

// ─── go list -m ──────────────────────────────────────────────────────────────

// List renvoie le module principal et ses dépendances. Avec updates, la
// commande consulte le proxy de modules pour trouver les mises à jour
// (accès réseau, selon GOPROXY / GOPRIVATE).
func List(ctx context.Context, root string, updates bool) ([]Module, error) {
	args := []string{"list", "-m", "-json"}
	if updates {
		args = append(args, "-u")
	}
	out, err := goCmd(ctx, root, append(args, "all")...)
	mods, perr := ParseList(out)
	if err != nil && len(mods) == 0 {
		return nil, err
	}
	return mods, perr
}

// ParseList lit une suite d'objets JSON de « go list -m -json ». Le module
// principal vient en premier, puis les dépendances triées par chemin.
func ParseList(data []byte) ([]Module, error) {
	var mods []Module
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		var m Module
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return mods, fmt.Errorf("sortie de go list illisible : %w", err)
		}
		mods = append(mods, m)
	}
	sort.SliceStable(mods, func(i, j int) bool {
		if mods[i].Main != mods[j].Main {
			return mods[i].Main
		}
		return mods[i].Path < mods[j].Path
	})
	return mods, nil
}

// UpdateKind décrit l'ampleur d'une mise à jour.
type UpdateKind int

const (
	NoUpdate UpdateKind = iota
	Patch               // v1.2.3 → v1.2.4
	Minor               // v1.2.3 → v1.3.0
	Major               // v0.x → v1, ou changement de version majeure
)

func (k UpdateKind) String() string {
	switch k {
	case Patch:
		return "correctif"
	case Minor:
		return "mineure"
	case Major:
		return "majeure"
	}
	return ""
}

// Kind compare deux versions sémantiques.
func Kind(from, to string) UpdateKind {
	if !semver.IsValid(from) || !semver.IsValid(to) || semver.Compare(to, from) <= 0 {
		return NoUpdate
	}
	switch {
	case semver.Major(from) != semver.Major(to):
		return Major
	case semver.MajorMinor(from) != semver.MajorMinor(to):
		if semver.Major(from) == "v0" {
			return Major // en v0, une version mineure peut tout casser
		}
		return Minor
	}
	return Patch
}

// ─── go mod why / go get ─────────────────────────────────────────────────────

// Why explique pourquoi le module est nécessaire (go mod why -m) : la chaîne
// d'imports la plus courte depuis le module principal.
func Why(ctx context.Context, root, path string) (string, error) {
	out, err := goCmd(ctx, root, "mod", "why", "-m", path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > 0 && strings.HasPrefix(lines[0], "# ") {
		lines = lines[1:] // l'en-tête répète le chemin du module
	}
	return strings.Join(lines, "\n"), nil
}

// Get met le module à la version demandée (go get chemin@version), ce qui
// modifie go.mod et go.sum.
func Get(ctx context.Context, root, path, version string) error {
	_, err := goCmd(ctx, root, "get", path+"@"+version)
	return err
}

// GoVersion renvoie la version de la chaîne d'outils (ex. « go1.27.3 »).
func GoVersion(ctx context.Context, root string) (string, error) {
	out, err := goCmd(ctx, root, "env", "GOVERSION")
	return strings.TrimSpace(string(out)), err
}

func goCmd(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			first, _, _ := strings.Cut(msg, "\n")
			return out, errors.New(first)
		}
		return out, err
	}
	return out, nil
}

// ─── govulncheck ─────────────────────────────────────────────────────────────

// VulnInstall est la commande d'installation de govulncheck.
const VulnInstall = "go install golang.org/x/vuln/cmd/govulncheck@latest"

// VulnAvailable indique si govulncheck est installé.
func VulnAvailable() bool {
	_, err := exec.LookPath("govulncheck")
	return err == nil
}

// Level indique à quel point une vulnérabilité concerne le code.
type Level int

const (
	LevelModule  Level = iota // le module est requis, le paquet vulnérable n'est pas importé
	LevelPackage              // le paquet vulnérable est importé, la fonction n'est pas appelée
	LevelSymbol               // la fonction vulnérable est appelée par le code
)

func (l Level) String() string {
	switch l {
	case LevelSymbol:
		return "fonction vulnérable appelée"
	case LevelPackage:
		return "paquet importé, fonction non appelée"
	}
	return "module requis, paquet non importé"
}

// Frame est une étape de la chaîne d'appels qui mène à la fonction vulnérable.
type Frame struct {
	Module   string
	Package  string
	Function string
	Receiver string
	File     string
	Line     int
}

// Name renvoie « paquet.Type.Fonction ».
func (f Frame) Name() string {
	s := f.Function
	if f.Receiver != "" {
		s = strings.TrimPrefix(f.Receiver, "*") + "." + s
	}
	if f.Package != "" {
		s = f.Package + "." + s
	}
	return s
}

// Vuln est une vulnérabilité qui touche un module.
type Vuln struct {
	ID      string
	Summary string
	Details string
	Aliases []string // CVE, GHSA…
	Module  string   // « stdlib » pour la bibliothèque standard
	Version string   // version utilisée
	Fixed   string   // première version corrigée (vide si aucune)
	Level   Level
	Trace   []Frame // chaîne d'appels (niveau LevelSymbol), de la fonction vulnérable vers le code
}

// URL renvoie la page de la vulnérabilité sur pkg.go.dev.
func (v Vuln) URL() string { return "https://pkg.go.dev/vuln/" + v.ID }

// Vulns lance govulncheck sur le module (accès réseau à vuln.go.dev).
func Vulns(ctx context.Context, root string) ([]Vuln, error) {
	if !VulnAvailable() {
		return nil, errors.New("govulncheck n'est pas installé")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "govulncheck", "-format", "json", "./...")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	vulns, perr := ParseVulns(out)
	if err != nil && len(vulns) == 0 {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			first, _, _ := strings.Cut(msg, "\n")
			return nil, errors.New(first)
		}
		return nil, err
	}
	return vulns, perr
}

// ParseVulns lit la sortie de « govulncheck -format json » : des messages
// config, progress, osv (fiches) et finding (constats), un par objet JSON.
// Les constats sont regroupés par vulnérabilité et par module, au niveau le
// plus grave rencontré.
func ParseVulns(data []byte) ([]Vuln, error) {
	type position struct {
		Filename string `json:"filename"`
		Line     int    `json:"line"`
	}
	type frame struct {
		Module   string    `json:"module"`
		Version  string    `json:"version"`
		Package  string    `json:"package"`
		Function string    `json:"function"`
		Receiver string    `json:"receiver"`
		Position *position `json:"position"`
	}
	type message struct {
		OSV *struct {
			ID      string   `json:"id"`
			Summary string   `json:"summary"`
			Details string   `json:"details"`
			Aliases []string `json:"aliases"`
		} `json:"osv"`
		Finding *struct {
			OSV          string  `json:"osv"`
			FixedVersion string  `json:"fixed_version"`
			Trace        []frame `json:"trace"`
		} `json:"finding"`
	}

	type osvInfo struct {
		summary, details string
		aliases          []string
	}
	osvs := map[string]osvInfo{}
	byKey := map[string]*Vuln{}
	var order []string

	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		var msg message
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("sortie de govulncheck illisible : %w", err)
		}
		if o := msg.OSV; o != nil {
			osvs[o.ID] = osvInfo{o.Summary, o.Details, o.Aliases}
		}
		f := msg.Finding
		if f == nil || len(f.Trace) == 0 {
			continue
		}
		top := f.Trace[0]
		level := LevelModule
		switch {
		case top.Function != "":
			level = LevelSymbol
		case top.Package != "":
			level = LevelPackage
		}
		key := f.OSV + "|" + top.Module
		v := byKey[key]
		if v == nil {
			v = &Vuln{ID: f.OSV, Module: top.Module, Version: top.Version, Fixed: f.FixedVersion, Level: -1}
			byKey[key] = v
			order = append(order, key)
		}
		if level > v.Level {
			v.Level = level
			if level == LevelSymbol {
				v.Trace = v.Trace[:0]
				for _, fr := range f.Trace {
					fm := Frame{Module: fr.Module, Package: fr.Package, Function: fr.Function, Receiver: fr.Receiver}
					if fr.Position != nil {
						fm.File, fm.Line = fr.Position.Filename, fr.Position.Line
					}
					v.Trace = append(v.Trace, fm)
				}
			}
		}
	}

	out := make([]Vuln, 0, len(order))
	for _, k := range order {
		v := *byKey[k]
		info := osvs[v.ID]
		v.Summary, v.Details, v.Aliases = info.summary, info.details, info.aliases
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Level != out[j].Level {
			return out[i].Level > out[j].Level // les plus graves d'abord
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}
