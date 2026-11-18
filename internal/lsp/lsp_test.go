package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConversions(t *testing.T) {
	line := "a\té😀b" // 😀 : 2 unités UTF-16, 4 octets
	for _, c := range []struct {
		enc        string
		col, units int
	}{
		{"utf-16", 3, 3}, {"utf-16", 4, 5}, {"utf-16", 5, 6},
		{"utf-8", 3, 4}, {"utf-8", 4, 8},
		{"utf-32", 4, 4},
	} {
		if got := unitsOf(line, c.col, c.enc); got != c.units {
			t.Errorf("%s : colonne %d → %d unités, attendu %d", c.enc, c.col, got, c.units)
		}
		if got := runesOf(line, c.units, c.enc); got != c.col {
			t.Errorf("%s : %d unités → colonne %d, attendu %d", c.enc, c.units, got, c.col)
		}
	}
	if got := unitsOf("ab", 4, "utf-16"); got != 4 {
		t.Errorf("au-delà de la ligne : %d", got)
	}
}

func TestURI(t *testing.T) {
	u := fileURI("/home/moi/a b/é.go")
	if u != "file:///home/moi/a%20b/%C3%A9.go" {
		t.Errorf("uri : %s", u)
	}
	if p, err := uriPath(u); err != nil || p != "/home/moi/a b/é.go" {
		t.Errorf("chemin : %q %v", p, err)
	}
}

// fakeServer répond aux requêtes du client depuis l'autre bout d'un tube.
type fakeServer struct {
	t   *testing.T
	in  *bufio.Reader  // ce qu'envoie le client
	out io.WriteCloser // vers le client
}

func (s *fakeServer) recv() message {
	s.t.Helper()
	data, err := readFrame(s.in)
	if err != nil {
		s.t.Fatalf("lecture : %v", err)
	}
	var m message
	if err := json.Unmarshal(data, &m); err != nil {
		s.t.Fatal(err)
	}
	return m
}

func (s *fakeServer) send(v any) {
	data, _ := json.Marshal(v)
	fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n%s", len(data), data)
}

func newFake(t *testing.T, onDiag func(Diagnostics)) (*Client, *fakeServer) {
	cr, sw := io.Pipe() // serveur → client
	sr, cw := io.Pipe() // client → serveur
	t.Cleanup(func() { sw.Close(); cw.Close() })
	return newClient(cw, cr, onDiag), &fakeServer{t: t, in: bufio.NewReader(sr), out: sw}
}

func TestProtocol(t *testing.T) {
	diags := make(chan Diagnostics, 1)
	c, s := newFake(t, func(d Diagnostics) { diags <- d }) // encodage par défaut : UTF-16

	// Document ouvert : ses lignes servent aux conversions.
	go c.Open("/p/a.go", "package a\nvar 😀x = y\n", 1)
	if m := s.recv(); m.Method != "textDocument/didOpen" {
		t.Fatalf("didOpen attendu : %s", m.Method)
	}

	// Requête du serveur : le client répond (configuration vide).
	s.send(map[string]any{"jsonrpc": "2.0", "id": 7, "method": "workspace/configuration",
		"params": map[string]any{"items": []any{map[string]any{}, map[string]any{}}}})
	if m := s.recv(); string(*m.ID) != "7" || string(m.Result) != "[{},{}]" {
		t.Errorf("réponse à la configuration : %s %s", *m.ID, m.Result)
	}

	// Diagnostics : colonnes UTF-16 converties en runes.
	s.send(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": map[string]any{
		"uri": "file:///p/a.go", "diagnostics": []any{map[string]any{
			"range":    map[string]any{"start": map[string]int{"line": 1, "character": 10}, "end": map[string]int{"line": 1, "character": 11}},
			"severity": 1, "source": "compiler", "message": "undefined: y",
		}},
	}})
	select {
	case d := <-diags:
		if d.Path != "/p/a.go" || len(d.Items) != 1 || d.Items[0].Start != (Pos{1, 9}) || d.Items[0].Message != "undefined: y" {
			t.Errorf("diagnostics : %+v", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("diagnostics non reçus")
	}

	// Définition : la position envoyée est en UTF-16, la réponse convertie.
	done := make(chan *Location)
	go func() {
		loc, err := c.Definition(context.Background(), "/p/a.go", Pos{1, 5})
		if err != nil {
			t.Error(err)
		}
		done <- loc
	}()
	m := s.recv()
	var p struct {
		Position wirePos `json:"position"`
	}
	json.Unmarshal(m.Params, &p)
	if m.Method != "textDocument/definition" || p.Position != (wirePos{1, 6}) {
		t.Errorf("requête : %s %+v", m.Method, p.Position)
	}
	s.send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(*m.ID), "result": []any{map[string]any{
		"uri":   "file:///p/a.go",
		"range": map[string]any{"start": map[string]int{"line": 1, "character": 7}, "end": map[string]int{"line": 1, "character": 8}},
	}}})
	if loc := <-done; loc == nil || loc.Path != "/p/a.go" || loc.Pos != (Pos{1, 6}) {
		t.Errorf("définition : %+v", loc)
	}

	// Serveur arrêté : les appels échouent au lieu d'attendre.
	s.out.Close()
	<-c.done
	if _, err := c.Hover(context.Background(), "/p/a.go", Pos{}); err == nil {
		t.Error("serveur arrêté : erreur attendue")
	}
}

// TestGopls dialogue avec le vrai gopls, s'il est installé.
func TestGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls absent")
	}
	if testing.Short() {
		t.Skip("lent")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module m\n\ngo 1.22\n"), 0o600)
	path := filepath.Join(dir, "m.go")
	src := "package m\n\n// Deux renvoie 2.\nfunc Deux() int { return 2 }\n"
	os.WriteFile(path, []byte(src), 0o600)

	diags := make(chan Diagnostics, 16)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c, err := Start(ctx, dir, func(d Diagnostics) { diags <- d })
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Texte non enregistré, avec une erreur : signalée sans écrire le fichier.
	edited := src + "\nvar x = Trois()\n"
	if err := c.Open(path, edited, 1); err != nil {
		t.Fatal(err)
	}
	var got Diagnostics
	for got.Path == "" || len(got.Items) == 0 {
		select {
		case got = <-diags:
		case <-ctx.Done():
			t.Fatal("pas de diagnostic reçu")
		}
	}
	d := got.Items[0]
	if d.Severity != Error || !strings.Contains(d.Message, "Trois") || d.Start != (Pos{5, 8}) {
		t.Errorf("diagnostic : %+v", d)
	}

	// Définition et documentation dans le texte non enregistré.
	if err := c.Change(path, src+"\nvar x = Deux()\n", 2); err != nil {
		t.Fatal(err)
	}
	loc, err := c.Definition(ctx, path, Pos{5, 9})
	if err != nil || loc == nil || loc.Pos != (Pos{3, 5}) {
		t.Errorf("définition : %+v %v", loc, err)
	}
	doc, err := c.Hover(ctx, path, Pos{5, 9})
	if err != nil || !strings.Contains(doc, "Deux renvoie 2.") {
		t.Errorf("documentation : %q %v", doc, err)
	}

	// Complétion après « strings.Rep » : le mot commencé sera remplacé.
	withImport := "package m\n\nimport \"strings\"\n\nvar s = strings.Rep\n"
	if err := c.Change(path, withImport, 3); err != nil {
		t.Fatal(err)
	}
	items, err := c.Completion(ctx, path, Pos{4, 19}, "")
	if err != nil {
		t.Fatal(err)
	}
	var found *Item
	for i := range items {
		if items[i].Label == "Repeat" {
			found = &items[i]
		}
	}
	if found == nil || found.Kind != KindFunction || !found.HasStart || found.Start != (Pos{4, 16}) || !strings.HasPrefix(found.Text, "Repeat") {
		t.Errorf("complétion « Repeat » : %+v (%d propositions)", found, len(items))
	}
}
