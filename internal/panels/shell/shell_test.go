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

// newShell démarre un vrai /bin/sh dans un pseudo-terminal de 80×24.
func newShell(t *testing.T) *Model {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("pas de pseudo-terminal")
	}
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("PS1", "$ ")
	t.Setenv("GOPIT_TERMINAL", "on")
	m := New(theme.New(), &project.Project{Root: t.TempDir()})
	m.View(82, 25) // 80 colonnes (marge de 2), 24 lignes (ligne d'état en plus)
	if m.emu == nil {
		t.Fatalf("shell non démarré : %s", m.failed)
	}
	t.Cleanup(func() {
		if !m.exited && m.ptmx != nil {
			m.emu.SendText("exit\r")
			waitFor(t, m, func(string) bool { return m.exited })
		}
	})
	return m
}

// screen renvoie le texte de l'historique et de l'écran (sans les espaces
// qui complètent chaque ligne à la largeur du terminal).
func screen(m *Model) string {
	lines := m.lines()
	for i, l := range lines {
		lines[i] = strings.TrimRight(ansi.Strip(l), " ")
	}
	return strings.Join(lines, "\n")
}

// waitFor traite les messages du shell jusqu'à ce que cond soit vraie.
func waitFor(t *testing.T, m *Model, cond func(string) bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond(screen(m)) {
		select {
		case msg := <-m.redraw:
			m.Update(msg)
		case msg := <-m.exits:
			m.Update(msg)
		case <-time.After(20 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("délai dépassé ; écran :\n%s", screen(m))
		}
	}
}

func typeKeys(m *Model, s string) {
	for _, r := range s {
		if r == '\r' {
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			continue
		}
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func ctrl(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl} }

func TestShellRunsCommands(t *testing.T) {
	m := newShell(t)
	if !m.RawInput() || !m.Capturing() {
		t.Fatal("mode shell attendu au démarrage")
	}
	typeKeys(m, "echo bon$((1+1))jour\r")
	waitFor(t, m, func(s string) bool { return strings.Contains(s, "bon2jour") })
	// Rendu : marge de 2 colonnes, ligne d'état, aucune ligne trop large.
	out := m.View(82, 25)
	lines := strings.Split(out, "\n")
	if len(lines) != 25 || !strings.Contains(ansi.Strip(out), "Ctrl+]") {
		t.Errorf("vue : %d lignes\n%s", len(lines), ansi.Strip(out))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w > 82 {
			t.Errorf("ligne %d : largeur %d", i, w)
		}
	}
	// Redimensionnement : le terminal suit la taille du module.
	m.View(102, 31)
	if m.emu.Width() != 100 || m.emu.Height() != 30 {
		t.Errorf("taille : %d×%d", m.emu.Width(), m.emu.Height())
	}
	typeKeys(m, "stty size\r")
	waitFor(t, m, func(s string) bool { return strings.Contains(s, "30 100") })
}

func TestShellInterruptAndInteractive(t *testing.T) {
	m := newShell(t)
	// Ctrl+C arrête la commande (pas gopit).
	typeKeys(m, "sleep 30; echo jamais\r")
	time.Sleep(200 * time.Millisecond)
	m.Update(ctrl('c'))
	typeKeys(m, "echo apres\r")
	// « apres » : la commande tapée, puis sa sortie (après l'invite réaffichée).
	waitFor(t, m, func(s string) bool { return strings.Count(s, "apres") >= 2 })
	if strings.Contains(screen(m), "\njamais") {
		t.Error("la commande n'a pas été interrompue")
	}

	// Programme qui lit le clavier : impossible avec l'ancien lanceur.
	typeKeys(m, "cat\r")
	time.Sleep(200 * time.Millisecond)
	typeKeys(m, "saisie interactive\r")
	waitFor(t, m, func(s string) bool { return strings.Count(s, "saisie interactive") >= 2 })
	m.Update(ctrl('d'))
	typeKeys(m, "echo fini\r")
	waitFor(t, m, func(s string) bool { return strings.Count(s, "fini") >= 2 })
}

func TestNavigationAndOpen(t *testing.T) {
	m := newShell(t)
	typeKeys(m, "i=1; while [ $i -le 60 ]; do echo ligne $i; i=$((i+1)); done; echo ./pkg/a.go:12:3: erreur\r")
	waitFor(t, m, func(s string) bool { return strings.Contains(s, "\n./pkg/a.go:12:3: erreur") })

	// Ctrl+] : navigation, les touches du cockpit reviennent.
	m.Update(ctrl(']'))
	if m.RawInput() || m.Capturing() {
		t.Fatal("Ctrl+] doit rendre la main à gopit")
	}
	// Remonter jusqu'à la ligne de l'emplacement, puis l'ouvrir.
	for i := 0; i < 10 && !strings.Contains(ansi.Strip(m.lines()[m.sel]), "a.go"); i++ {
		m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'o', Text: "o"})
	if cmd == nil {
		t.Fatalf("ouverture attendue (ligne %q)", ansi.Strip(m.lines()[m.sel]))
	}
	if msg, ok := cmd().(panel.OpenMsg); !ok || msg.File != filepath.Join(m.proj.Root, "pkg", "a.go") || msg.Line != 12 {
		t.Errorf("ouverture : %#v", cmd())
	}

	// Historique : le début de la sortie est sorti de l'écran, on y remonte.
	m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	if m.back == 0 || !strings.Contains(ansi.Strip(m.View(82, 25)), "ligne 1 ") {
		t.Errorf("historique (remonté de %d) :\n%s", m.back, ansi.Strip(m.View(82, 25)))
	}

	// i : retour au shell, sur l'écran courant.
	m.Update(tea.KeyPressMsg{Code: 'i', Text: "i"})
	if !m.RawInput() || m.back != 0 {
		t.Error("i : retour au shell attendu")
	}
}

func TestShellExitAndRestart(t *testing.T) {
	m := newShell(t)
	typeKeys(m, "exit 3\r")
	waitFor(t, m, func(string) bool { return m.exited })
	if m.exitCode != 3 || m.RawInput() || !strings.Contains(ansi.Strip(m.View(82, 25)), "code 3") {
		t.Errorf("fin : code %d", m.exitCode)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.exited || !m.RawInput() {
		t.Fatal("Entrée relance le shell")
	}
	typeKeys(m, "echo relance\r")
	waitFor(t, m, func(s string) bool { return strings.Contains(s, "\nrelance") })
}

func TestDisabled(t *testing.T) {
	t.Setenv("GOPIT_TERMINAL", "off")
	m := New(theme.New(), &project.Project{Root: t.TempDir()})
	if out := ansi.Strip(m.View(80, 10)); m.emu != nil || !strings.Contains(out, "GOPIT_TERMINAL=off") || m.RawInput() {
		t.Errorf("désactivé :\n%s", out)
	}
}
