// Package newmod propose de créer un module Go quand gopit est lancé hors
// de tout module : chemin par défaut déduit du dépôt git ou du compte
// GitHub, saisie dans le terminal, puis « go mod init ».
package newmod

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"

	"golang.org/x/mod/module"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
)

// DefaultPath propose un chemin de module pour le répertoire dir : celui
// de son dépôt GitHub (ou autre) s'il en a un, sinon
// github.com/<compte>/<répertoire>, sinon example.com/<répertoire>.
func DefaultPath(dir string) string {
	if p := fromRemote(gitOutput(dir, "remote", "get-url", "origin")); p != "" {
		return p
	}
	base := cleanName(filepath.Base(dir))
	if u := githubUser(); u != "" {
		return "github.com/" + u + "/" + base
	}
	return "example.com/" + base
}

// fromRemote convertit l'adresse d'un dépôt distant en chemin de module
// (« git@github.com:moi/projet.git » → « github.com/moi/projet »).
func fromRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}
	var host, path string
	if u, err := url.Parse(remote); err == nil && u.Host != "" {
		host, path = u.Hostname(), u.Path
	} else if at := strings.Index(remote, "@"); at >= 0 && strings.Contains(remote[at:], ":") {
		host, path, _ = strings.Cut(remote[at+1:], ":") // syntaxe scp de ssh
	} else {
		return ""
	}
	p := strings.TrimSuffix(strings.Trim(host+"/"+strings.Trim(path, "/"), "/"), ".git")
	if module.CheckPath(p) != nil {
		return ""
	}
	return p
}

// githubUser renvoie le compte GitHub de l'utilisateur, d'après sa
// configuration locale (gh, git, variable d'environnement), sans réseau.
func githubUser() string {
	for _, f := range []func() string{
		func() string { return command("gh", "config", "get", "-h", "github.com", "user") },
		func() string { return gitOutput("", "config", "--get", "github.user") },
		func() string { return os.Getenv("GITHUB_USER") },
	} {
		if u := strings.TrimSpace(f()); u != "" && !strings.ContainsAny(u, " /") {
			return u
		}
	}
	return ""
}

// cleanName adapte un nom de répertoire à un élément de chemin de module.
func cleanName(name string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case r == ' ':
			return '-'
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-._~", r)):
			return unicode.ToLower(r)
		}
		return -1
	}, name)
	if name == "" || name == "." {
		return "app"
	}
	return name
}

func gitOutput(dir string, args ...string) string {
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	return command("git", args...)
}

func command(name string, args ...string) string {
	if _, err := exec.LookPath(name); err != nil {
		return ""
	}
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Check vérifie un chemin de module.
func Check(path string) error {
	if err := module.CheckPath(path); err != nil {
		return errors.New(i18n.T("chemin de module invalide : ") + path)
	}
	return nil
}

// Create crée le module dans dir (« go mod init path »).
func Create(ctx context.Context, dir, path string) error {
	if err := Check(path); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "go", "mod", "init", path)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(out.String()); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}
