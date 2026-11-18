package debug

import (
	"fmt"
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

func TestResolveTarget(t *testing.T) {
	root := "/p"
	lib := []pkgInfo{{"p", "/p"}, {"util", "/p/util"}}
	one := append(lib, pkgInfo{"main", "/p/cmd/outil"})
	two := append(one, pkgInfo{"main", "/p/cmd/autre"})
	for _, c := range []struct {
		name    string
		pkgs    []pkgInfo
		current string
		target  string
		choices int
		err     bool
	}{
		{"programme du fichier affiché", two, "/p/cmd/autre/main.go", "./cmd/autre", 0, false},
		{"tests du fichier affiché", two, "/p/util/x_test.go", "test ./util", 0, false},
		{"seul programme", one, "/p/util/x.go", "./cmd/outil", 0, false},
		{"programme à la racine", []pkgInfo{{"main", "/p"}}, "", ".", 0, false},
		{"plusieurs programmes", two, "", "", 2, false},
		{"aucun programme", lib, "/p/util/x.go", "", 0, true},
	} {
		target, choices, err := resolveTarget(c.pkgs, root, c.current)
		if target != c.target || len(choices) != c.choices || (err != nil) != c.err {
			t.Errorf("%s : %q %v %v", c.name, target, choices, err)
		}
	}
}

func TestBuildError(t *testing.T) {
	out := []string{"Building /p", "Build Error: go build -o /p/__debug_bin -gcflags all=-N -l /p", "# demo",
		"./main.go:3:15: undefined: indefini (exit status 1)"}
	if got := buildError(out); got != "./main.go:3:15: undefined: indefini" {
		t.Errorf("%q", got)
	}
	if got := buildError([]string{"Build Error: go build -o x .", "no Go files in /p (exit status 1)"}); got != "no Go files in /p" {
		t.Errorf("%q", got)
	}
}

// writeModule crée un module de test : files associe chemins et contenus.
func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("dlv"); err != nil {
		t.Skip("dlv absent")
	}
	if testing.Short() {
		t.Skip("lent")
	}
	root := t.TempDir()
	files["go.mod"] = "module demo\n\ngo 1.22\n"
	for name, src := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(src), 0o600)
	}
	return root
}

func newDebugger(t *testing.T, root string) *Model {
	m := New(theme.New(), &project.Project{Root: root})
	t.Cleanup(func() {
		if m.c != nil {
			m.c.Close()
		}
	})
	return m
}

func TestAutoTargetWithDelve(t *testing.T) {
	prog := "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"%s\") }\n"
	root := writeModule(t, map[string]string{
		"lib.go":           "package demo\n",
		"cmd/un/main.go":   fmt.Sprintf(prog, "un"),
		"cmd/deux/main.go": fmt.Sprintf(prog, "deux"),
	})
	m := newDebugger(t, root)
	pump := newPump(t, m)

	// Rien d'affiché dans l'éditeur, deux programmes : le choix est proposé.
	pump(m.DebugKey("f5"), func() bool { return m.choices != nil })
	if len(m.choices) != 2 || !m.Capturing() {
		t.Fatalf("choix : %v", m.choices)
	}
	for m.choices[m.choiceSel] != "./cmd/deux" {
		m.key("down")
	}
	pump(m.key("enter"), func() bool { return m.status == ended })
	if out := strings.Join(m.output, "\n"); !strings.Contains(out, "deux") || m.target != "./cmd/deux" {
		t.Errorf("cible %q, sortie :\n%s", m.target, out)
	}

	// Fichier d'un programme affiché : c'est lui qui est débogué.
	m.target = ""
	m.state.Current = filepath.Join(root, "cmd", "un", "main.go")
	pump(m.DebugKey("f5"), func() bool { return m.status == ended })
	if m.active != "./cmd/un" || !strings.Contains(strings.Join(m.output, "\n"), "un") {
		t.Errorf("cible automatique : %q", m.active)
	}
}

func TestBuildFailureIsExplained(t *testing.T) {
	root := writeModule(t, map[string]string{"main.go": "package main\n\nfunc main() { indefini() }\n"})
	m := newDebugger(t, root)
	var flash string
	msgs := make(chan tea.Msg, 64)
	run := func(c tea.Cmd) {
		if c != nil {
			go func() { msgs <- c() }()
		}
	}
	run(m.DebugKey("f5"))
	deadline := time.After(2 * time.Minute)
	for m.status != ended || flash == "" {
		select {
		case msg := <-msgs:
			switch msg := msg.(type) {
			case tea.BatchMsg:
				for _, c := range msg {
					run(c)
				}
			case panel.FlashMsg:
				flash = msg.Text
			case nil, panel.ShowMsg:
			default:
				_, c := m.Update(msg)
				run(c)
			}
		case <-deadline:
			t.Fatalf("délai dépassé (état %d)", m.status)
		}
	}
	if !strings.Contains(flash, "indefini") {
		t.Errorf("erreur de compilation non expliquée : %q", flash)
	}
	time.Sleep(200 * time.Millisecond) // un éventuel « terminated » tardif
	for len(msgs) > 0 {
		if msg, ok := (<-msgs).(eventMsg); ok {
			m.Update(msg)
		}
	}
	if out := strings.Join(m.output, "\n"); strings.Contains(out, "code 0") {
		t.Errorf("« programme terminé » après un échec :\n%s", out)
	}
}

func TestOutputView(t *testing.T) {
	m := New(theme.New(), &project.Project{Root: t.TempDir()})
	m.addOutput("Compilation…\n")
	for i := 1; i <= 100; i++ {
		m.addOutputKind(fmt.Sprintf("ligne %d\n", i), outProgram)
	}
	m.addOutputKind("oups\n", outStderr)
	m.addOutputKind("sans fin de ligne", outProgram)
	if m.kindOf(0) != outInfo || m.kindOf(101) != outStderr || len(m.allOutput()) != 103 {
		t.Fatalf("origines : %v… (%d lignes)", m.outKind[:3], len(m.allOutput()))
	}

	// Vue normale : seulement la fin, et l'invitation à agrandir.
	if out := ansi.Strip(m.View(80, 30)); strings.Contains(out, "ligne 50\n") || !strings.Contains(out, "s : plein écran") {
		t.Errorf("vue normale :\n%s", out)
	}

	// s : plein écran, qui suit la fin.
	m.key("s")
	out := ansi.Strip(m.View(80, 30))
	if !m.outView || !strings.Contains(out, "sans fin de ligne") || !strings.Contains(out, "oups") || !strings.Contains(out, "/ 103") {
		t.Fatalf("plein écran :\n%s", out)
	}
	// Remonter jusqu'au début : la première ligne réapparaît, la fin n'est plus suivie.
	m.key("home")
	out = ansi.Strip(m.View(80, 30))
	if !strings.Contains(out, "Compilation…") || m.outFollow {
		t.Errorf("début de la sortie :\n%s", out)
	}
	m.addOutputKind("nouvelle\n", outProgram)
	if out := ansi.Strip(m.View(80, 30)); strings.Contains(out, "nouvelle") {
		t.Error("en lecture du début, la nouvelle sortie ne fait pas défiler")
	}
	m.key("end")
	if out := ansi.Strip(m.View(80, 30)); !strings.Contains(out, "nouvelle") || !m.outFollow {
		t.Errorf("fin : de nouveau suivie :\n%s", out)
	}
	m.key("esc")
	if m.outView {
		t.Error("Échap revient à la pile et aux variables")
	}
}

func TestProgramOutputWithDelve(t *testing.T) {
	root := writeModule(t, map[string]string{"main.go": `package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("vers stdout")
	fmt.Fprintln(os.Stderr, "vers stderr")
}
`})
	m := newDebugger(t, root)
	var lines []panel.OutputMsg
	msgs := make(chan tea.Msg, 64)
	run := func(c tea.Cmd) {
		if c != nil {
			go func() { msgs <- c() }()
		}
	}
	run(m.DebugKey("f5"))
	deadline := time.After(2 * time.Minute)
	for m.status != ended {
		select {
		case msg := <-msgs:
			switch msg := msg.(type) {
			case tea.BatchMsg:
				for _, c := range msg {
					run(c)
				}
			case panel.OutputMsg:
				lines = append(lines, msg)
			case nil, panel.FlashMsg, panel.ShowMsg:
			default:
				_, c := m.Update(msg)
				run(c)
			}
		case <-deadline:
			t.Fatalf("délai dépassé (état %d)", m.status)
		}
	}
	// Chaque ligne du programme est annoncée (pour la barre d'état), avec son origine.
	got := map[string]bool{}
	for _, l := range lines {
		got[l.Line] = l.Err
	}
	if err, ok := got["vers stdout"]; !ok || err {
		t.Errorf("stdout : %+v", lines)
	}
	if err, ok := got["vers stderr"]; !ok || !err {
		t.Errorf("stderr : %+v", lines)
	}
	for i, l := range m.output {
		if l == "vers stderr" && m.kindOf(i) != outStderr {
			t.Error("ligne de stderr non marquée")
		}
	}
}
