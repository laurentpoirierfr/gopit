package newmod

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestFromRemote(t *testing.T) {
	for remote, want := range map[string]string{
		"git@github.com:moi/projet.git":          "github.com/moi/projet",
		"https://github.com/moi/projet.git":      "github.com/moi/projet",
		"https://github.com/moi/projet":          "github.com/moi/projet",
		"ssh://git@gitlab.example.org/a/b/c.git": "gitlab.example.org/a/b/c",
		"":                                       "",
		"/chemin/local/depot":                    "",
	} {
		if got := fromRemote(remote); got != want {
			t.Errorf("%q : %q, attendu %q", remote, got, want)
		}
	}
}

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{"gopit-demo": "gopit-demo", "Mon Projet": "mon-projet", "été!": "t", "": "app"} {
		if got := cleanName(in); got != want {
			t.Errorf("%q : %q, attendu %q", in, got, want)
		}
	}
}

func TestDefaultPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "gopit-demo")
	os.Mkdir(dir, 0o755)
	// Sans dépôt : compte GitHub de la configuration locale, ou example.com.
	if p := DefaultPath(dir); !strings.HasSuffix(p, "/gopit-demo") ||
		(!strings.HasPrefix(p, "github.com/") && !strings.HasPrefix(p, "example.com/")) {
		t.Errorf("sans dépôt : %q", p)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git absent")
	}
	// Dépôt avec une adresse distante : elle fait foi.
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "git@github.com:quelquun/autre-nom.git"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v : %v %s", args, err, out)
		}
	}
	if p := DefaultPath(dir); p != "github.com/quelquun/autre-nom" {
		t.Errorf("avec dépôt : %q", p)
	}
}

func TestCreate(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go absent")
	}
	dir := t.TempDir()
	if err := Create(context.Background(), dir, "pas un chemin"); err == nil {
		t.Error("chemin invalide accepté")
	}
	if err := Create(context.Background(), dir, "github.com/moi/demo"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	if !strings.HasPrefix(string(data), "module github.com/moi/demo\n") {
		t.Errorf("go.mod : %q", data)
	}
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

func TestAsker(t *testing.T) {
	def := "github.com/moi/demo"

	// Proposition grisée ; Entrée l'accepte.
	a := &asker{dir: "/tmp/demo", def: def}
	if v := ansi.Strip(a.View().Content); !strings.Contains(v, def) || !strings.Contains(v, "/tmp/demo") {
		t.Errorf("vue :\n%s", v)
	}
	a.Update(key("enter"))
	if !a.done || a.value() != def {
		t.Errorf("proposition : %v %q", a.done, a.value())
	}

	// Saisie : remplace la proposition ; un chemin invalide est refusé.
	a = &asker{def: def}
	for _, r := range "pas valide" {
		a.Update(key(string(r)))
	}
	a.Update(key("enter"))
	if a.done || a.err == "" {
		t.Error("chemin invalide accepté")
	}
	for range "pas valide" {
		a.Update(key("backspace"))
	}
	// Tab reprend la proposition pour la modifier.
	a.Update(key("tab"))
	a.Update(key("2"))
	a.Update(key("enter"))
	if !a.done || a.value() != def+"2" {
		t.Errorf("proposition modifiée : %v %q", a.done, a.value())
	}

	// Échap : renoncer.
	a = &asker{def: def}
	a.Update(key("esc"))
	if !a.quit || a.done {
		t.Error("Échap : abandon attendu")
	}
}
