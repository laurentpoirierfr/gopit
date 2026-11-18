package debug

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/dap"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/theme"
)

func TestParseTarget(t *testing.T) {
	root := "/p"
	for target, want := range map[string]dap.Launch{
		".":                       {Mode: "debug", Program: "/p", Dir: "/p"},
		"./cmd/outil -- -v a":     {Mode: "debug", Program: "/p/cmd/outil", Dir: "/p", Args: []string{"-v", "a"}},
		"-- a":                    {Mode: "debug", Program: "/p", Dir: "/p", Args: []string{"a"}},
		"test ./pkg -run TestX":   {Mode: "test", Program: "/p/pkg", Dir: "/p/pkg", Args: []string{"-test.run", "TestX"}},
		"test . -test.v":          {Mode: "test", Program: "/p", Dir: "/p", Args: []string{"-test.v"}},
		"test ./a --count=1 -v=1": {Mode: "test", Program: "/p/a", Dir: "/p/a", Args: []string{"-test.count=1", "-test.v=1"}},
	} {
		if got := parseTarget(root, target); !reflect.DeepEqual(got, want) {
			t.Errorf("%q : %+v, attendu %+v", target, got, want)
		}
	}
}

func TestStateKeepsBreakpoints(t *testing.T) {
	root := t.TempDir()
	m := New(theme.New(), &project.Project{Root: root})
	m.state.Toggle(filepath.Join(root, "main.go"), 12)
	m.state.Toggle(filepath.Join(root, "pkg", "a.go"), 3)
	m.target = "./cmd/x -- a"

	m2 := New(theme.New(), &project.Project{Root: root})
	m2.Restore(m.State())
	if !m2.state.Has(filepath.Join(root, "main.go"), 12) || !m2.state.Has(filepath.Join(root, "pkg", "a.go"), 3) ||
		len(m2.state.Files()) != 2 || m2.target != "./cmd/x -- a" {
		t.Errorf("restauré : %v, cible %q", m2.state.Files(), m2.target)
	}
}

const demo = `package main

import "fmt"

type point struct{ X, Y int }

func double(n int) int {
	r := n * 2
	return r
}

func main() {
	p := point{1, 2}
	fmt.Println("début", p)
	v := double(21)
	fmt.Println("fin", v)
}
`

// pump exécute les commandes et repasse leurs messages au module jusqu'à
// ce que done soit vrai ; les commandes en attente restent actives d'un
// appel à l'autre.
func newPump(t *testing.T, m *Model) func(cmd tea.Cmd, done func() bool) {
	msgs := make(chan tea.Msg, 256)
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c != nil {
			go func() { msgs <- c() }()
		}
	}
	return func(cmd tea.Cmd, done func() bool) {
		t.Helper()
		run(cmd)
		deadline := time.After(2 * time.Minute)
		for !done() {
			select {
			case msg := <-msgs:
				switch msg := msg.(type) {
				case tea.BatchMsg:
					for _, c := range msg {
						run(c)
					}
				case nil, panel.FlashMsg, panel.ShowMsg:
				default:
					_, c := m.Update(msg)
					run(c)
				}
			case <-deadline:
				t.Fatalf("délai dépassé (état %d)", m.status)
			}
		}
	}
}

func TestDebugWithDelve(t *testing.T) {
	if _, err := exec.LookPath("dlv"); err != nil {
		t.Skip("dlv absent")
	}
	if testing.Short() {
		t.Skip("lent")
	}
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o600)
	main := filepath.Join(root, "main.go")
	os.WriteFile(main, []byte(demo), 0o600)

	m := New(theme.New(), &project.Project{Root: root})
	t.Cleanup(func() {
		if m.c != nil {
			m.c.Close()
		}
	})
	m.state.Toggle(main, 8) // r := n * 2
	pump := newPump(t, m)

	// F5 : arrêt au point d'arrêt, montré à l'éditeur.
	pump(m.DebugKey("f5"), func() bool { return m.status == paused && len(m.vars) > 0 })
	if m.state.StopPath != main || m.state.StopLine != 8 || !strings.HasSuffix(m.frames[0].Name, "double") {
		t.Fatalf("arrêt : %s:%d %+v", m.state.StopPath, m.state.StopLine, m.frames)
	}
	if out := ansi.Strip(m.View(100, 30)); !strings.Contains(out, "n = 21") || !strings.Contains(out, "main.go:8") {
		t.Errorf("vue :\n%s", out)
	}

	// F6 : ligne suivante ; F8 : retour dans main.
	pump(m.DebugKey("f6"), func() bool { return m.status == paused && m.state.StopLine == 9 && len(m.vars) > 0 })
	pump(m.DebugKey("f8"), func() bool {
		return m.status == paused && len(m.frames) > 0 && strings.HasSuffix(m.frames[0].Name, "main") && len(m.vars) > 0
	})

	// Déplier p.
	for i, n := range m.rows() {
		if n.v.Name == "p" {
			m.cursor = i
		}
	}
	p := m.rows()[m.cursor]
	pump(m.key("right"), func() bool { return p.loaded })
	if out := ansi.Strip(m.View(100, 30)); !strings.Contains(out, "X = 1") {
		t.Errorf("p déplié :\n%s", out)
	}

	// F5 : jusqu'à la fin ; la sortie du programme est affichée.
	pump(m.DebugKey("f5"), func() bool { return m.status == ended })
	if out := strings.Join(m.output, "\n"); !strings.Contains(out, "début {1 2}") || !strings.Contains(out, "fin 42") ||
		!strings.Contains(out, "code 0") {
		t.Errorf("sortie :\n%s", out)
	}
	if m.state.StopLine != 0 || len(m.frames) != 0 || len(m.vars) != 0 {
		t.Error("plus rien à inspecter après la fin")
	}
	if strings.Contains(strings.Join(m.output, "\n"), "dlv help") {
		t.Error("messages de Delve affichés")
	}
}
