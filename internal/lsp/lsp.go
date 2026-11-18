// Package lsp est un petit client du protocole Language Server, pour
// dialoguer avec « gopls serve » : documents ouverts (avec leur texte non
// enregistré), diagnostics publiés au fil de la frappe, définition et
// documentation d'un symbole.
//
// Les positions échangées avec l'appelant sont en lignes et colonnes de
// runes, à partir de 0, comme dans le tampon de l'éditeur ; la conversion
// vers l'encodage choisi par le serveur (UTF-16 par défaut) est faite ici.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
)

// ErrClosed est renvoyée quand le serveur s'est arrêté.
var ErrClosed = i18n.Error("gopls s'est arrêté")

// Severity est la gravité d'un diagnostic (valeurs du protocole).
type Severity int

const (
	Error   Severity = 1
	Warning Severity = 2
	Info    Severity = 3
	Hint    Severity = 4
)

// Pos est une position en runes, à partir de 0.
type Pos struct{ Line, Col int }

// Diagnostic est un problème signalé par le serveur.
type Diagnostic struct {
	Start, End Pos
	Severity   Severity
	Source     string // compiler, vet, staticcheck…
	Message    string
}

// Diagnostics est la liste complète des problèmes d'un fichier (elle
// remplace la précédente).
type Diagnostics struct {
	Path  string
	Items []Diagnostic
}

// Location est un emplacement dans un fichier.
type Location struct {
	Path string
	Pos  Pos
}

// Client est une connexion à un serveur.
type Client struct {
	cmd *exec.Cmd
	in  io.WriteCloser

	wmu    sync.Mutex // écriture des messages
	mu     sync.Mutex // champs ci-dessous
	nextID int64
	calls  map[int64]chan response
	texts  map[string][]string // lignes des documents ouverts, par chemin
	err    error               // arrêt du serveur
	done   chan struct{}

	encoding string // utf-8, utf-16 ou utf-32
	onDiag   func(Diagnostics)
}

type response struct {
	result json.RawMessage
	err    error
}

// message est un message JSON-RPC (requête, réponse ou notification).
type message struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Start lance « gopls serve » pour le module situé dans root et l'initialise.
// onDiag est appelée (depuis une autre goroutine) à chaque publication de
// diagnostics.
func Start(ctx context.Context, root string, onDiag func(Diagnostics)) (*Client, error) {
	cmd := exec.Command("gopls", "serve")
	cmd.Dir = root
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := newClient(in, out, onDiag)
	c.cmd = cmd
	go func() { _ = cmd.Wait() }()
	if err := c.initialize(ctx, root); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// newClient crée un client sur une connexion déjà ouverte (tests).
func newClient(in io.WriteCloser, out io.Reader, onDiag func(Diagnostics)) *Client {
	c := &Client{in: in, calls: map[int64]chan response{}, texts: map[string][]string{},
		done: make(chan struct{}), encoding: "utf-16", onDiag: onDiag}
	go c.read(bufio.NewReader(out))
	return c
}

func (c *Client) initialize(ctx context.Context, root string) error {
	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   fileURI(root),
		"workspaceFolders": []map[string]string{
			{"uri": fileURI(root), "name": filepath.Base(root)},
		},
		"capabilities": map[string]any{
			"general": map[string]any{"positionEncodings": []string{"utf-32", "utf-8", "utf-16"}},
			"textDocument": map[string]any{
				"synchronization":    map[string]any{"didSave": true},
				"publishDiagnostics": map[string]any{},
				"hover":              map[string]any{"contentFormat": []string{"plaintext"}},
				"definition":         map[string]any{},
				"completion": map[string]any{
					"completionItem": map[string]any{"snippetSupport": false},
				},
			},
		},
	}
	var res struct {
		Capabilities struct {
			PositionEncoding string `json:"positionEncoding"`
		} `json:"capabilities"`
	}
	if err := c.call(ctx, "initialize", params, &res); err != nil {
		return err
	}
	if e := res.Capabilities.PositionEncoding; e != "" {
		c.encoding = e
	}
	return c.notify("initialized", struct{}{})
}

// Close arrête le serveur (sans attendre sa réponse au-delà du nécessaire).
func (c *Client) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.call(ctx, "shutdown", nil, nil)
	_ = c.notify("exit", nil)
	_ = c.in.Close()
	if c.cmd != nil && c.cmd.Process != nil {
		select {
		case <-c.done:
		case <-ctx.Done():
			_ = c.cmd.Process.Kill()
		}
	}
}

// ─── Documents ───────────────────────────────────────────────────────────────

// Open signale l'ouverture d'un document et son texte.
func (c *Client) Open(path, text string, version int) error {
	c.setText(path, text)
	return c.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": fileURI(path), "languageId": "go", "version": version, "text": text},
	})
}

// Change envoie le nouveau texte complet d'un document ouvert.
func (c *Client) Change(path, text string, version int) error {
	c.setText(path, text)
	return c.notify("textDocument/didChange", map[string]any{
		"textDocument":   map[string]any{"uri": fileURI(path), "version": version},
		"contentChanges": []map[string]string{{"text": text}},
	})
}

// Save signale l'enregistrement d'un document.
func (c *Client) Save(path string) error {
	return c.notify("textDocument/didSave", map[string]any{"textDocument": map[string]string{"uri": fileURI(path)}})
}

// CloseDoc signale la fermeture d'un document.
func (c *Client) CloseDoc(path string) error {
	c.mu.Lock()
	delete(c.texts, path)
	c.mu.Unlock()
	return c.notify("textDocument/didClose", map[string]any{"textDocument": map[string]string{"uri": fileURI(path)}})
}

func (c *Client) setText(path, text string) {
	c.mu.Lock()
	c.texts[path] = strings.Split(text, "\n")
	c.mu.Unlock()
}

// ─── Requêtes ────────────────────────────────────────────────────────────────

type wirePos struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type wireRange struct {
	Start wirePos `json:"start"`
	End   wirePos `json:"end"`
}

func (c *Client) docPos(path string, p Pos) map[string]any {
	return map[string]any{
		"textDocument": map[string]string{"uri": fileURI(path)},
		"position":     c.toWire(path, p),
	}
}

// Definition renvoie l'emplacement où est déclaré le symbole situé en p.
func (c *Client) Definition(ctx context.Context, path string, p Pos) (*Location, error) {
	var raw json.RawMessage
	if err := c.call(ctx, "textDocument/definition", c.docPos(path, p), &raw); err != nil {
		return nil, err
	}
	var locs []struct {
		URI   string    `json:"uri"`
		Range wireRange `json:"range"`
	}
	if len(raw) > 0 && raw[0] == '{' {
		raw = append(append(json.RawMessage{'['}, raw...), ']')
	}
	if err := json.Unmarshal(raw, &locs); err != nil || len(locs) == 0 {
		return nil, nil
	}
	target, err := uriPath(locs[0].URI)
	if err != nil {
		return nil, err
	}
	return &Location{Path: target, Pos: c.fromWire(target, locs[0].Range.Start)}, nil
}

// Hover renvoie la documentation du symbole situé en p (vide : aucune).
func (c *Client) Hover(ctx context.Context, path string, p Pos) (string, error) {
	var res *struct {
		Contents json.RawMessage `json:"contents"`
	}
	if err := c.call(ctx, "textDocument/hover", c.docPos(path, p), &res); err != nil || res == nil {
		return "", err
	}
	var mc struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(res.Contents, &mc) == nil && mc.Value != "" {
		return strings.TrimSpace(mc.Value), nil
	}
	var s string
	if json.Unmarshal(res.Contents, &s) == nil {
		return strings.TrimSpace(s), nil
	}
	return "", nil
}

// Kind est la nature d'une proposition de complétion (valeurs du protocole).
type Kind int

const (
	KindMethod    Kind = 2
	KindFunction  Kind = 3
	KindField     Kind = 5
	KindVariable  Kind = 6
	KindClass     Kind = 7
	KindInterface Kind = 8
	KindModule    Kind = 9
	KindProperty  Kind = 10
	KindKeyword   Kind = 14
	KindSnippet   Kind = 15
	KindConstant  Kind = 21
	KindStruct    Kind = 22
	KindTypeParam Kind = 25
)

// Item est une proposition de complétion.
type Item struct {
	Label  string // texte affiché
	Detail string // type ou signature
	Kind   Kind
	Filter string // texte servant au filtrage
	Text   string // texte à insérer
	// Start est le début du texte que remplace Text (le mot en cours) ;
	// HasStart est faux si le serveur ne l'a pas précisé.
	Start    Pos
	HasStart bool
}

// Completion renvoie les propositions de complétion en p. trigger est le
// caractère qui a déclenché la demande (« . »), ou vide pour une demande
// explicite.
func (c *Client) Completion(ctx context.Context, path string, p Pos, trigger string) ([]Item, error) {
	params := c.docPos(path, p)
	params["context"] = map[string]any{"triggerKind": 1}
	if trigger != "" {
		params["context"] = map[string]any{"triggerKind": 2, "triggerCharacter": trigger}
	}
	var raw json.RawMessage
	if err := c.call(ctx, "textDocument/completion", params, &raw); err != nil {
		return nil, err
	}
	type wireItem struct {
		Label      string `json:"label"`
		Detail     string `json:"detail"`
		Kind       Kind   `json:"kind"`
		FilterText string `json:"filterText"`
		InsertText string `json:"insertText"`
		TextEdit   *struct {
			Range   *wireRange `json:"range"`
			Insert  *wireRange `json:"insert"` // InsertReplaceEdit
			NewText string     `json:"newText"`
		} `json:"textEdit"`
	}
	var items []wireItem
	if len(raw) > 0 && raw[0] == '{' {
		var list struct {
			Items []wireItem `json:"items"`
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, err
		}
		items = list.Items
	} else if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, err
		}
	}
	out := make([]Item, 0, len(items))
	for _, w := range items {
		it := Item{Label: w.Label, Detail: w.Detail, Kind: w.Kind, Filter: w.FilterText, Text: w.InsertText}
		if it.Filter == "" {
			it.Filter = w.Label
		}
		if e := w.TextEdit; e != nil {
			it.Text = e.NewText
			r := e.Range
			if r == nil {
				r = e.Insert
			}
			if r != nil {
				it.Start, it.HasStart = c.fromWire(path, r.Start), true
			}
		}
		if it.Text == "" {
			it.Text = w.Label
		}
		out = append(out, it)
	}
	return out, nil
}

// ─── Positions ───────────────────────────────────────────────────────────────

// lineOf renvoie la ligne d'un document ouvert, ou lue sur le disque.
func (c *Client) lineOf(path string, line int) string {
	c.mu.Lock()
	lines, ok := c.texts[path]
	c.mu.Unlock()
	if !ok {
		data, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		lines = strings.Split(string(data), "\n")
	}
	if line < 0 || line >= len(lines) {
		return ""
	}
	return lines[line]
}

func (c *Client) toWire(path string, p Pos) wirePos {
	return wirePos{Line: p.Line, Character: unitsOf(c.lineOf(path, p.Line), p.Col, c.encoding)}
}

func (c *Client) fromWire(path string, w wirePos) Pos {
	return Pos{Line: w.Line, Col: runesOf(c.lineOf(path, w.Line), w.Character, c.encoding)}
}

// unitsOf convertit une colonne en runes en unités de l'encodage.
func unitsOf(line string, col int, enc string) int {
	n, i := 0, 0
	for _, r := range line {
		if i >= col {
			break
		}
		n += width(r, enc)
		i++
	}
	return n + (col - i) // au-delà de la fin de ligne : une unité par colonne
}

// runesOf convertit une position en unités de l'encodage en colonne de runes.
func runesOf(line string, units int, enc string) int {
	n, col := 0, 0
	for _, r := range line {
		if n >= units {
			return col
		}
		n += width(r, enc)
		col++
	}
	return col
}

func width(r rune, enc string) int {
	switch enc {
	case "utf-8":
		return len(string(r))
	case "utf-32":
		return 1
	}
	if r >= 0x10000 {
		return 2 // paire de substitution UTF-16
	}
	return 1
}

// ─── JSON-RPC ────────────────────────────────────────────────────────────────

func (c *Client) call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.err
	}
	c.nextID++
	id := c.nextID
	ch := make(chan response, 1)
	c.calls[id] = ch
	c.mu.Unlock()

	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return err
	}
	select {
	case r := <-ch:
		if r.err != nil || result == nil || len(r.result) == 0 {
			return r.err
		}
		return json.Unmarshal(r.result, result)
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.calls, id)
		c.mu.Unlock()
		return ctx.Err()
	case <-c.done:
		return ErrClosed
	}
}

func (c *Client) notify(method string, params any) error {
	return c.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *Client) send(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := fmt.Fprintf(c.in, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	_, err = c.in.Write(data)
	return err
}

// read reçoit les messages du serveur jusqu'à la fin de la connexion.
func (c *Client) read(r *bufio.Reader) {
	err := c.readLoop(r)
	c.mu.Lock()
	if err == nil || errors.Is(err, io.EOF) {
		err = ErrClosed
	}
	c.err = err
	for id, ch := range c.calls {
		ch <- response{err: err}
		delete(c.calls, id)
	}
	c.mu.Unlock()
	close(c.done)
}

func (c *Client) readLoop(r *bufio.Reader) error {
	for {
		data, err := readFrame(r)
		if err != nil {
			return err
		}
		var msg message
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch {
		case msg.ID != nil && msg.Method != "": // requête du serveur
			c.answer(*msg.ID, msg.Method, msg.Params)
		case msg.ID != nil: // réponse
			id, err := strconv.ParseInt(string(*msg.ID), 10, 64)
			if err != nil {
				continue
			}
			c.mu.Lock()
			ch := c.calls[id]
			delete(c.calls, id)
			c.mu.Unlock()
			if ch == nil {
				continue
			}
			resp := response{result: msg.Result}
			if msg.Error != nil {
				resp.err = errors.New(msg.Error.Message)
			}
			ch <- resp
		case msg.Method == "textDocument/publishDiagnostics":
			c.publish(msg.Params)
		}
	}
}

// answer répond aux requêtes du serveur : configuration vide, le reste
// accepté sans suite.
func (c *Client) answer(id json.RawMessage, method string, params json.RawMessage) {
	var result any
	if method == "workspace/configuration" {
		var p struct {
			Items []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(params, &p)
		items := make([]any, len(p.Items))
		for i := range items {
			items[i] = map[string]any{}
		}
		result = items
	}
	_ = c.send(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (c *Client) publish(params json.RawMessage) {
	var p struct {
		URI         string `json:"uri"`
		Diagnostics []struct {
			Range    wireRange `json:"range"`
			Severity Severity  `json:"severity"`
			Source   string    `json:"source"`
			Message  string    `json:"message"`
		} `json:"diagnostics"`
	}
	if json.Unmarshal(params, &p) != nil || c.onDiag == nil {
		return
	}
	path, err := uriPath(p.URI)
	if err != nil {
		return
	}
	ds := Diagnostics{Path: path, Items: make([]Diagnostic, 0, len(p.Diagnostics))}
	for _, d := range p.Diagnostics {
		sev := d.Severity
		if sev == 0 {
			sev = Error
		}
		ds.Items = append(ds.Items, Diagnostic{
			Start: c.fromWire(path, d.Range.Start), End: c.fromWire(path, d.Range.End),
			Severity: sev, Source: d.Source, Message: d.Message,
		})
	}
	c.onDiag(ds)
}

// readFrame lit un message : en-têtes (dont Content-Length), ligne vide,
// puis le contenu.
func readFrame(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Content-Length:"); ok {
			if length, err = strconv.Atoi(strings.TrimSpace(v)); err != nil {
				return nil, fmt.Errorf("Content-Length : %w", err)
			}
		}
	}
	if length < 0 {
		return nil, errors.New("Content-Length absent")
	}
	data := make([]byte, length)
	_, err := io.ReadFull(r, data)
	return data, err
}

// ─── URI ─────────────────────────────────────────────────────────────────────

func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

func uriPath(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return "", fmt.Errorf("uri %q", uri)
	}
	return filepath.FromSlash(u.Path), nil
}
