package shell

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/theme"
)

func newModel(t *testing.T) *Model {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("tests écrits pour sh")
	}
	t.Setenv("SHELL", "/bin/sh")
	m := New(theme.New(), &project.Project{Root: t.TempDir()})
	t.Cleanup(m.stop)
	return m
}

func typeLine(m *Model, s string) tea.Cmd {
	for _, r := range s {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return cmd
}

// pump exécute les commandes et repasse leurs messages au module jusqu'à
// la fin de la commande lancée.
func pump(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	deadline := time.After(20 * time.Second)
	for cmd != nil {
		ch := make(chan tea.Msg, 1)
		go func(c tea.Cmd) { ch <- c() }(cmd)
		select {
		case msg := <-ch:
			if _, ok := msg.(panel.FlashMsg); ok {
				return // bilan de fin
			}
			_, cmd = m.Update(msg)
		case <-deadline:
			t.Fatal("délai dépassé")
		}
	}
}

// texts renvoie toutes les lignes affichées ; output, seulement la sortie
// des commandes (sans les lignes « $ … » ni les bilans).
func texts(m *Model) string { return join(m, func(line) bool { return true }) }

func output(m *Model) string { return join(m, func(l line) bool { return l.kind == kindOut }) }

func join(m *Model, keep func(line) bool) string {
	var b strings.Builder
	for _, l := range m.lines {
		if keep(l) {
			b.WriteString(l.text)
			b.WriteString("\n")
		}
	}
	return b.String()
}

func TestRunCommand(t *testing.T) {
	m := newModel(t)
	pump(t, m, typeLine(m, "echo bonjour; echo oups >&2; printf 'a\\tb\\r50%%\\r100%%\\n'; pwd; exit 3"))
	out := texts(m)
	for _, want := range []string{"$ echo bonjour", "bonjour\n", "oups\n", "100%\n", m.proj.Root, "code de sortie 3"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q absent de :\n%s", want, out)
		}
	}
	if strings.Contains(output(m), "50%") {
		t.Error("retour chariot : seule la dernière version de la ligne est gardée")
	}
	if m.running() || m.lines[len(m.lines)-1].kind != kindFail {
		t.Error("fin en échec attendue")
	}
	if v := ansi.Strip(m.View(80, 10)); !strings.Contains(v, "bonjour") || !strings.Contains(v, "❯") {
		t.Errorf("vue :\n%s", v)
	}

	// Réussite, et relance de la dernière commande.
	pump(t, m, typeLine(m, "true"))
	if l := m.lines[len(m.lines)-1]; l.kind != kindOK {
		t.Errorf("réussite : %+v", l)
	}
	n := len(m.lines)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	pump(t, m, cmd)
	if len(m.lines) != n+2 || m.lines[n].text != "$ true" {
		t.Errorf("relance : %q", texts(m))
	}
}

func TestStopCommand(t *testing.T) {
	m := newModel(t)
	cmd := typeLine(m, "echo début; sleep 30; echo jamais")
	if !m.running() || !m.TakesCtrlC() {
		t.Fatal("commande en cours : Ctrl+C l'arrête au lieu de quitter")
	}
	m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	start := time.Now()
	pump(t, m, cmd)
	if time.Since(start) > 10*time.Second || m.running() {
		t.Fatal("la commande ne s'est pas arrêtée")
	}
	if out := output(m); strings.Contains(out, "jamais") {
		t.Errorf("sortie après l'arrêt :\n%s", out)
	}
	if l := m.lines[len(m.lines)-1]; l.kind != kindFail || !strings.Contains(l.text, "arrêtée") {
		t.Errorf("bilan : %+v", l)
	}
	// Sans commande en cours ni saisie, Ctrl+C quitte gopit.
	if m.TakesCtrlC() {
		t.Error("rien à arrêter : Ctrl+C quitte")
	}
}

func TestHistory(t *testing.T) {
	m := newModel(t)
	pump(t, m, typeLine(m, "echo un"))
	pump(t, m, typeLine(m, "echo deux"))
	pump(t, m, typeLine(m, "echo deux")) // doublon consécutif : gardé une fois
	for _, r := range "ech" {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	up := tea.KeyPressMsg{Code: tea.KeyUp}
	m.Update(up)
	if string(m.input) != "echo deux" {
		t.Errorf("↑ : %q", string(m.input))
	}
	m.Update(up)
	m.Update(up) // déjà la plus ancienne
	if string(m.input) != "echo un" {
		t.Errorf("↑↑ : %q", string(m.input))
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if string(m.input) != "ech" {
		t.Errorf("retour à la saisie en cours : %q", string(m.input))
	}

	// Gardé d'une session à l'autre.
	m2 := New(theme.New(), m.proj)
	m2.Restore(m.State())
	m2.Update(up)
	if string(m2.input) != "echo deux" {
		t.Errorf("historique restauré : %q", string(m2.input))
	}
}

func TestOpenLocation(t *testing.T) {
	m := newModel(t)
	pump(t, m, typeLine(m, "echo './pkg/a.go:12:5: undefined: x'; echo rien"))
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	// Curseur sur la dernière ligne (fin) : remonter jusqu'à l'emplacement.
	for m.cursor > 0 && !strings.Contains(m.lines[m.cursor].text, "a.go") {
		m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	if cmd == nil {
		t.Fatal("ouverture attendue")
	}
	msg, ok := cmd().(panel.OpenMsg)
	if !ok || msg.File != filepath.Join(m.proj.Root, "pkg", "a.go") || msg.Line != 12 {
		t.Errorf("ouverture : %#v", cmd())
	}
}
