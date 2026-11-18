// Package home est le panneau d'accueil : un résumé du module Go ouvert.
package home

import (
	"context"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/theme"
)

// Informations chargées en arrière-plan.
const (
	factToolchain = "toolchain"
	factPackages  = "packages"
	factFiles     = "files"
	factGit       = "git"
)

type factMsg struct {
	key string
	val string
	err error
}

type fact struct {
	val  string
	err  error
	done bool
}

type Model struct {
	th    *theme.Theme
	proj  *project.Project
	facts map[string]fact
	gen   int // incrémenté à chaque actualisation pour ignorer les vieux résultats
}

func New(th *theme.Theme, p *project.Project) *Model {
	return &Model{th: th, proj: p, facts: map[string]fact{}}
}

func (m *Model) Title() string { return i18n.N("Accueil") }
func (m *Model) Icon() string  { return "" }

func (m *Model) Hints() []panel.Hint {
	return []panel.Hint{{Key: "r", Desc: i18n.T("actualiser")}}
}

func (m *Model) Init() tea.Cmd { return m.load() }

func (m *Model) load() tea.Cmd {
	m.gen++
	m.facts = map[string]fact{}
	root := m.proj.Root
	return tea.Batch(
		fetch(factToolchain, func() (string, error) { return run(root, "go", "env", "GOVERSION") }),
		fetch(factPackages, func() (string, error) { return countPackages(root) }),
		fetch(factFiles, func() (string, error) { return countFiles(root) }),
		fetch(factGit, func() (string, error) { return gitState(root) }),
	)
}

func (m *Model) Update(msg tea.Msg) (panel.Panel, tea.Cmd) {
	switch msg := msg.(type) {
	case factMsg:
		m.facts[msg.key] = fact{val: msg.val, err: msg.err, done: true}
	case panel.LangMsg: // le résumé contient du texte traduit
		return m, m.load()
	case tea.KeyPressMsg:
		if msg.String() == "r" {
			return m, tea.Batch(m.load(), panel.Flash(i18n.T("Accueil actualisé"), false))
		}
	}
	return m, nil
}

func (m *Model) View(width, height int) string {
	t, p := m.th, m.proj
	labelW := 22

	row := func(label, value string) string {
		return t.Label.Render(fmt.Sprintf("%-*s", labelW, label)) + value
	}
	val := func(key string) string {
		f := m.facts[key]
		switch {
		case !f.done:
			return t.Dim.Render("…")
		case f.err != nil:
			return t.Err.Render(f.err.Error())
		}
		return t.Value.Render(f.val)
	}

	goVer := t.Value.Render(p.GoVersion)
	if p.Toolchain != "" {
		goVer += t.Dim.Render("  (toolchain " + p.Toolchain + ")")
	}

	lines := []string{
		t.Title.Render(p.Name()),
		t.Dim.Render(p.Module),
		"",
		row(i18n.T("Répertoire"), t.Text.Render(p.Root)),
		row(i18n.T("Version Go (go.mod)"), goVer),
		row(i18n.T("Go installé"), val(factToolchain)),
		row(i18n.T("Dépendances"), t.Value.Render(fmt.Sprintf(i18n.T("%d directes"), p.Requires))+
			t.Dim.Render(fmt.Sprintf(i18n.T(" · %d indirectes"), p.Indirect))),
		row(i18n.T("Paquets"), val(factPackages)),
		row(i18n.T("Fichiers"), val(factFiles)),
		row("Git", val(factGit)),
		"",
		t.Label.Render(i18n.T("Modules du cockpit")),
		t.Dim.Render(i18n.T("Choisis un module dans la colonne de gauche (↑↓ ou son numéro).")),
	}
	return lipgloss.NewStyle().MaxWidth(width).MaxHeight(height).Render(strings.Join(lines, "\n"))
}

// ─── Chargement en arrière-plan ──────────────────────────────────────────────

func fetch(key string, fn func() (string, error)) tea.Cmd {
	return func() tea.Msg {
		v, err := fn()
		return factMsg{key: key, val: v, err: err}
	}
}

// run lance une commande dans dir et renvoie sa sortie, sans espaces autour.
func run(dir, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			first, _, _ := strings.Cut(strings.TrimSpace(string(ee.Stderr)), "\n")
			return "", fmt.Errorf("%s : %s", name, first)
		}
		return "", fmt.Errorf("%s : %w", name, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func countPackages(root string) (string, error) {
	out, err := run(root, "go", "list", "./...")
	if err != nil {
		return "", err
	}
	n := 0
	if out != "" {
		n = strings.Count(out, "\n") + 1
	}
	return plural(n, i18n.T("paquet"), i18n.T("paquets")), nil
}

// countFiles compte les fichiers .go (hors vendor, testdata et dossiers cachés).
func countFiles(root string) (string, error) {
	var src, tests int
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case strings.HasSuffix(name, "_test.go"):
			tests++
		case strings.HasSuffix(name, ".go"):
			src++
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s · %s", plural(src, i18n.T("fichier source"), i18n.T("fichiers sources")),
		plural(tests, i18n.T("fichier de test"), i18n.T("fichiers de test"))), nil
}

func gitState(root string) (string, error) {
	if _, err := run(root, "git", "rev-parse", "--git-dir"); err != nil {
		return i18n.T("pas de dépôt git"), nil
	}
	// symbolic-ref fonctionne aussi sans commit ; rev-parse couvre le HEAD détaché.
	branch, err := run(root, "git", "symbolic-ref", "--short", "HEAD")
	if err != nil {
		if branch, err = run(root, "git", "rev-parse", "--short", "HEAD"); err != nil {
			branch = "?"
		}
	}
	status, err := run(root, "git", "status", "--porcelain")
	if err != nil {
		return branch, nil
	}
	if status == "" {
		return branch + i18n.T(" · propre"), nil
	}
	n := strings.Count(status, "\n") + 1
	return fmt.Sprintf("%s · %s", branch, plural(n, i18n.T("fichier modifié"), i18n.T("fichiers modifiés"))), nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
