// Package gitx interroge et modifie un dépôt git par la commande git :
// état (status --porcelain=v2), diff, historique, indexation et commit.
// Il ne dépend pas de l'interface et peut être testé seul.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
)

// ErrNotRepo est renvoyée hors d'un dépôt git.
var ErrNotRepo = i18n.Error("pas de dépôt git")

// Change est l'état d'un fichier.
type Change struct {
	Path     string
	OrigPath string // ancien nom (renommage)
	Staged   byte   // état dans l'index : 'M', 'A', 'D', 'R', 'C', '.' (aucun), '?' (non suivi)
	Worktree byte   // état dans l'arbre de travail
	Conflict bool
}

// IsStaged indique une modification indexée (prête à être commitée).
func (c Change) IsStaged() bool { return c.Staged != '.' && c.Staged != '?' && !c.Conflict }

// IsUnstaged indique une modification de l'arbre de travail non indexée.
func (c Change) IsUnstaged() bool { return c.Worktree != '.' && c.Staged != '?' }

// Untracked indique un fichier non suivi.
func (c Change) Untracked() bool { return c.Staged == '?' }

// Status est l'état du dépôt.
type Status struct {
	Branch   string // vide si HEAD est détaché
	Commit   string // commit courant (court), vide sans commit
	Upstream string
	Ahead    int
	Behind   int
	Changes  []Change
}

// Run lance git dans dir et renvoie sa sortie standard.
func Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	// Messages de git en anglais, quelle que soit la langue de l'utilisateur :
	// ils sont analysés (« not a git repository »…). Le contenu des fichiers et
	// des commits n'est pas concerné.
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANGUAGE=C")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if strings.Contains(msg, "not a git repository") {
			return nil, ErrNotRepo
		}
		if msg != "" {
			first, _, _ := strings.Cut(msg, "\n")
			return out, errors.New(first)
		}
		return out, err
	}
	return out, nil
}

// GetStatus lit l'état du dépôt.
func GetStatus(ctx context.Context, dir string) (*Status, error) {
	out, err := Run(ctx, dir, "status", "--porcelain=v2", "--branch", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	return ParseStatus(out)
}

// ParseStatus lit la sortie de « git status --porcelain=v2 --branch -z ».
func ParseStatus(out []byte) (*Status, error) {
	s := &Status{}
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if f == "" {
			continue
		}
		switch f[0] {
		case '#':
			switch {
			case strings.HasPrefix(f, "# branch.oid "):
				if oid := strings.TrimPrefix(f, "# branch.oid "); oid != "(initial)" {
					s.Commit = oid[:min(7, len(oid))]
				}
			case strings.HasPrefix(f, "# branch.head "):
				if h := strings.TrimPrefix(f, "# branch.head "); h != "(detached)" {
					s.Branch = h
				}
			case strings.HasPrefix(f, "# branch.upstream "):
				s.Upstream = strings.TrimPrefix(f, "# branch.upstream ")
			case strings.HasPrefix(f, "# branch.ab "):
				var a, b int
				if _, err := fmt.Sscanf(strings.TrimPrefix(f, "# branch.ab "), "+%d -%d", &a, &b); err == nil {
					s.Ahead, s.Behind = a, b
				}
			}
		case '1', '2', 'u':
			// 1 XY sub mH mI mW hH hI chemin
			// 2 XY sub mH mI mW hH hI Xscore chemin \0 ancien_chemin
			// u XY sub m1 m2 m3 mW h1 h2 h3 chemin
			n := map[byte]int{'1': 9, '2': 10, 'u': 11}[f[0]]
			parts := strings.SplitN(f, " ", n)
			if len(parts) < n || len(parts[1]) != 2 {
				return nil, fmt.Errorf(i18n.T("ligne d'état invalide : %q"), f)
			}
			c := Change{Path: parts[n-1], Staged: parts[1][0], Worktree: parts[1][1], Conflict: f[0] == 'u'}
			if f[0] == '2' && i+1 < len(fields) {
				i++
				c.OrigPath = fields[i]
			}
			s.Changes = append(s.Changes, c)
		case '?':
			s.Changes = append(s.Changes, Change{Path: strings.TrimPrefix(f, "? "), Staged: '?', Worktree: '?'})
		}
	}
	return s, nil
}

// Diff renvoie le diff d'un fichier : indexé (staged) ou non. Pour un
// fichier non suivi, tout son contenu apparaît comme ajouté.
func Diff(ctx context.Context, dir string, c Change, staged bool) (string, error) {
	var args []string
	switch {
	case c.Untracked():
		args = []string{"diff", "--no-color", "--no-index", "--", "/dev/null", c.Path}
	case staged:
		args = []string{"diff", "--no-color", "--cached", "--", c.Path}
	default:
		args = []string{"diff", "--no-color", "--", c.Path}
	}
	out, err := Run(ctx, dir, args...)
	// « diff --no-index » sort avec le code 1 quand les fichiers diffèrent.
	if err != nil && !(c.Untracked() && len(out) > 0) {
		return "", err
	}
	return string(out), nil
}

// Commit est un commit de l'historique.
type Commit struct {
	Hash    string
	Author  string
	When    time.Time
	Subject string
}

// Log renvoie les n derniers commits.
func Log(ctx context.Context, dir string, n int) ([]Commit, error) {
	out, err := Run(ctx, dir, "log", "-n", strconv.Itoa(n), "--format=%h%x1f%an%x1f%at%x1f%s%x1e")
	if err != nil {
		if strings.Contains(err.Error(), "does not have any commits") {
			return nil, nil
		}
		return nil, err
	}
	var commits []Commit
	for rec := range strings.SplitSeq(string(out), "\x1e") {
		f := strings.Split(strings.TrimSpace(rec), "\x1f")
		if len(f) != 4 {
			continue
		}
		ts, _ := strconv.ParseInt(f[2], 10, 64)
		commits = append(commits, Commit{Hash: f[0], Author: f[1], When: time.Unix(ts, 0), Subject: f[3]})
	}
	return commits, nil
}

// Show renvoie le message complet, les statistiques et le diff d'un commit.
func Show(ctx context.Context, dir, hash string) (string, error) {
	out, err := Run(ctx, dir, "show", "--no-color", "--stat", "--patch", "--format=%H%n%an <%ae>%n%ad%n%n%B", "--date=format:%d/%m/%Y %H:%M", hash)
	return string(out), err
}

// Stage indexe des fichiers (git add).
func Stage(ctx context.Context, dir string, paths ...string) error {
	_, err := Run(ctx, dir, append([]string{"add", "--"}, paths...)...)
	return err
}

// StageAll indexe toutes les modifications, fichiers non suivis compris.
func StageAll(ctx context.Context, dir string) error {
	_, err := Run(ctx, dir, "add", "--all")
	return err
}

// Unstage retire des fichiers de l'index sans toucher à l'arbre de travail.
func Unstage(ctx context.Context, dir string, hasCommit bool, paths ...string) error {
	args := append([]string{"restore", "--staged", "--"}, paths...)
	if !hasCommit {
		// Sans commit, il n'y a rien à restaurer : on vide l'entrée d'index.
		args = append([]string{"rm", "--cached", "--quiet", "--"}, paths...)
	}
	_, err := Run(ctx, dir, args...)
	return err
}

// CommitStaged commite les modifications indexées avec le message donné.
func CommitStaged(ctx context.Context, dir, message string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", errors.New(i18n.T("message de commit vide"))
	}
	if _, err := Run(ctx, dir, "commit", "-q", "-m", message); err != nil {
		return "", err
	}
	out, err := Run(ctx, dir, "rev-parse", "--short", "HEAD")
	return strings.TrimSpace(string(out)), err
}
