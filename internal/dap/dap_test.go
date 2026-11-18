package dap

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const demo = `package main

import "fmt"

type point struct{ X, Y int }

func double(n int) int {
	r := n * 2
	return r
}

func main() {
	p := point{1, 2}
	s := []string{"a", "b"}
	fmt.Println("début", p, s)
	v := double(21)
	fmt.Println("fin", v)
}
`

// session démarre Delve sur le programme de démonstration.
func session(t *testing.T) (*Client, chan Event, string) {
	t.Helper()
	if _, err := exec.LookPath("dlv"); err != nil {
		t.Skip("dlv absent")
	}
	if testing.Short() {
		t.Skip("lent")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(demo), 0o600)
	events := make(chan Event, 256)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := Start(ctx, dir, func(e Event) { events <- e })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c, events, dir
}

// waitEvent attend l'événement name (en gardant la sortie du programme).
func waitEvent(t *testing.T, events chan Event, name string, out *strings.Builder) Event {
	t.Helper()
	deadline := time.After(60 * time.Second)
	for {
		select {
		case e := <-events:
			if e.Name == "output" {
				var o Output
				json.Unmarshal(e.Body, &o)
				if o.Category != "console" {
					out.WriteString(o.Output)
				}
			}
			if e.Name == name {
				return e
			}
		case <-deadline:
			t.Fatalf("événement %q attendu", name)
		}
	}
}

func TestDebugSession(t *testing.T) {
	c, events, dir := session(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	main := filepath.Join(dir, "main.go")

	if err := c.Launch(ctx, Launch{Mode: "debug", Program: dir, Dir: dir}); err != nil {
		t.Fatal(err)
	}
	bps, err := c.SetBreakpoints(ctx, main, []int{8})
	if err != nil || len(bps) != 1 || !bps[0].Verified {
		t.Fatalf("point d'arrêt : %+v %v", bps, err)
	}
	if err := c.ConfigurationDone(ctx); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	var st Stopped
	json.Unmarshal(waitEvent(t, events, "stopped", &out).Body, &st)
	if st.Reason != "breakpoint" {
		t.Errorf("arrêt : %+v", st)
	}
	frames, err := c.StackTrace(ctx, st.ThreadID, 20)
	if err != nil || len(frames) < 2 || frames[0].Line != 8 || !strings.HasSuffix(frames[0].Name, "double") ||
		frames[0].Source.Path != main || frames[1].Line != 16 {
		t.Fatalf("pile : %+v %v", frames, err)
	}
	vars := locals(t, c, ctx, frames[0].ID)
	if vars["n"].Value != "21" {
		t.Errorf("n : %+v", vars["n"])
	}

	// Pas à pas : r vaut 42 à la ligne suivante.
	if err := c.Next(ctx, st.ThreadID); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(waitEvent(t, events, "stopped", &out).Body, &st)
	frames, _ = c.StackTrace(ctx, st.ThreadID, 20)
	if frames[0].Line != 9 || locals(t, c, ctx, frames[0].ID)["r"].Value != "42" {
		t.Errorf("après next : ligne %d", frames[0].Line)
	}

	// Sortie de double : retour dans main, p se déplie.
	if err := c.StepOut(ctx, st.ThreadID); err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(waitEvent(t, events, "stopped", &out).Body, &st)
	frames, _ = c.StackTrace(ctx, st.ThreadID, 20)
	if !strings.HasSuffix(frames[0].Name, "main") {
		t.Errorf("après stepOut : %+v", frames[0])
	}
	p := locals(t, c, ctx, frames[0].ID)["p"]
	if p.Ref == 0 {
		t.Fatalf("p doit se déplier : %+v", p)
	}
	fields, err := c.Variables(ctx, p.Ref)
	if err != nil || len(fields) != 2 || fields[0].Name != "X" || fields[0].Value != "1" {
		t.Errorf("champs de p : %+v %v", fields, err)
	}

	// Fin du programme : sa sortie est arrivée en événements.
	if err := c.Continue(ctx, st.ThreadID); err != nil {
		t.Fatal(err)
	}
	waitEvent(t, events, "terminated", &out)
	if o := out.String(); !strings.Contains(o, "début {1 2} [a b]") || !strings.Contains(o, "fin 42") {
		t.Errorf("sortie du programme : %q", o)
	}
}

func locals(t *testing.T, c *Client, ctx context.Context, frame int) map[string]Variable {
	t.Helper()
	scopes, err := c.Scopes(ctx, frame)
	if err != nil || len(scopes) == 0 {
		t.Fatalf("portées : %+v %v", scopes, err)
	}
	vars, err := c.Variables(ctx, scopes[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]Variable{}
	for _, v := range vars {
		m[v.Name] = v
	}
	return m
}

func TestLaunchError(t *testing.T) {
	c, events, dir := session(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() { indefini }\n"), 0o600)
	err := c.Launch(ctx, Launch{Mode: "debug", Program: dir, Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "Build error") {
		t.Fatalf("erreur de compilation attendue : %v", err)
	}
	// Le détail du compilateur arrive en sortie (catégorie console ou stderr).
	var details strings.Builder
	deadline := time.After(5 * time.Second)
	for !strings.Contains(details.String(), "indefini") {
		select {
		case e := <-events:
			if e.Name == "output" {
				var o Output
				json.Unmarshal(e.Body, &o)
				details.WriteString(o.Output)
			}
		case <-deadline:
			t.Fatalf("détail de l'erreur de compilation absent : %q", details.String())
		}
	}
}
