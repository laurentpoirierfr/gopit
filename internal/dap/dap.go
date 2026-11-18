// Package dap est un petit client du Debug Adapter Protocol, pour piloter
// Delve (« dlv dap ») : lancer un programme ou des tests, points d'arrêt,
// pas à pas, pile d'appels et variables.
//
// Les lignes sont numérotées à partir de 1, comme dans le protocole.
package dap

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
)

// ErrMissing est renvoyée quand Delve n'est pas installé.
var ErrMissing = i18n.Error("Delve (dlv) n'est pas installé")

// ErrClosed est renvoyée quand la session s'est arrêtée.
var ErrClosed = i18n.Error("la session de débogage est terminée")

// Install est la commande qui installe Delve.
const Install = "go install github.com/go-delve/delve/cmd/dlv@latest"

// Event est un événement envoyé par Delve (stopped, output, terminated…).
type Event struct {
	Name string
	Body json.RawMessage
}

// Stopped est le corps d'un événement « stopped ».
type Stopped struct {
	Reason   string `json:"reason"` // breakpoint, step, pause, exception…
	ThreadID int    `json:"threadId"`
	Text     string `json:"text"`
}

// Output est le corps d'un événement « output ».
type Output struct {
	Category string `json:"category"` // stdout, stderr, console
	Output   string `json:"output"`
}

// Exited est le corps d'un événement « exited ».
type Exited struct {
	ExitCode int `json:"exitCode"`
}

// Breakpoint est un point d'arrêt tel que Delve l'a accepté.
type Breakpoint struct {
	Line     int    `json:"line"`
	Verified bool   `json:"verified"`
	Message  string `json:"message"`
}

// Frame est un niveau de la pile d'appels.
type Frame struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Line   int    `json:"line"`
	Source struct {
		Path string `json:"path"`
	} `json:"source"`
}

// Scope est un groupe de variables (Locals, Arguments…).
type Scope struct {
	Name string `json:"name"`
	Ref  int    `json:"variablesReference"`
}

// Variable est une variable ; Ref non nul : elle a des éléments à déplier.
type Variable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Type  string `json:"type"`
	Ref   int    `json:"variablesReference"`
}

// Launch décrit le programme à déboguer.
type Launch struct {
	Mode    string   // "debug" (programme) ou "test"
	Program string   // répertoire du paquet
	Dir     string   // répertoire de travail
	Args    []string // arguments du programme (ou de go test)
}

// Client est une session avec Delve.
type Client struct {
	conn net.Conn
	cmd  *exec.Cmd

	wmu   sync.Mutex
	mu    sync.Mutex
	seq   int
	calls map[int]chan response
	err   error
	done  chan struct{}

	onEvent func(Event)
}

type response struct {
	body json.RawMessage
	err  error
}

type message struct {
	Seq        int             `json:"seq"`
	Type       string          `json:"type"`
	Command    string          `json:"command,omitempty"`
	Event      string          `json:"event,omitempty"`
	RequestSeq int             `json:"request_seq,omitempty"`
	Success    bool            `json:"success,omitempty"`
	Message    string          `json:"message,omitempty"`
	Body       json.RawMessage `json:"body,omitempty"`
}

// Start lance « dlv dap » dans dir, s'y connecte et l'initialise. onEvent
// est appelée (depuis une autre goroutine) pour chaque événement.
func Start(ctx context.Context, dir string, onEvent func(Event)) (*Client, error) {
	if _, err := exec.LookPath("dlv"); err != nil {
		return nil, ErrMissing
	}
	cmd := exec.Command("dlv", "dap", "--listen", "127.0.0.1:0")
	cmd.Dir = dir
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() { _ = cmd.Wait() }()

	// Première ligne : « DAP server listening at: 127.0.0.1:34621 ».
	addr := make(chan string, 1)
	go func() {
		r := bufio.NewReader(out)
		line, _ := r.ReadString('\n')
		_, a, _ := strings.Cut(strings.TrimSpace(line), "listening at: ")
		addr <- a
		_, _ = io.Copy(io.Discard, r) // journal de Delve, inutile ici
	}()
	var a string
	select {
	case a = <-addr:
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		return nil, ctx.Err()
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		return nil, errors.New("dlv dap : pas d'adresse d'écoute")
	}
	if a == "" {
		_ = cmd.Process.Kill()
		return nil, errors.New("dlv dap : pas d'adresse d'écoute")
	}
	conn, err := net.DialTimeout("tcp", a, 5*time.Second)
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, err
	}
	c := newClient(conn, onEvent)
	c.cmd = cmd
	err = c.call(ctx, "initialize", map[string]any{
		"clientID": "gopit", "clientName": "gopit", "adapterID": "go",
		"linesStartAt1": true, "columnsStartAt1": true, "pathFormat": "path",
		"supportsVariableType": true,
	}, nil)
	if err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func newClient(conn net.Conn, onEvent func(Event)) *Client {
	c := &Client{conn: conn, calls: map[int]chan response{}, done: make(chan struct{}), onEvent: onEvent}
	go c.read(bufio.NewReader(conn))
	return c
}

// Launch construit et lance le programme ; il s'arrête aux points d'arrêt
// une fois ConfigurationDone appelé.
func (c *Client) Launch(ctx context.Context, l Launch) error {
	args := map[string]any{
		"request": "launch", "mode": l.Mode, "program": l.Program, "cwd": l.Dir,
		"outputMode": "remote", // sortie du programme en événements « output »
	}
	if len(l.Args) > 0 {
		args["args"] = l.Args
	}
	return c.call(ctx, "launch", args, nil)
}

// SetBreakpoints remplace les points d'arrêt d'un fichier.
func (c *Client) SetBreakpoints(ctx context.Context, path string, lines []int) ([]Breakpoint, error) {
	bps := make([]map[string]int, len(lines))
	for i, l := range lines {
		bps[i] = map[string]int{"line": l}
	}
	var res struct {
		Breakpoints []Breakpoint `json:"breakpoints"`
	}
	err := c.call(ctx, "setBreakpoints", map[string]any{
		"source": map[string]string{"path": path}, "breakpoints": bps,
	}, &res)
	return res.Breakpoints, err
}

// ConfigurationDone démarre l'exécution.
func (c *Client) ConfigurationDone(ctx context.Context) error {
	return c.call(ctx, "configurationDone", nil, nil)
}

// Continue, Next (pas à pas principal), StepIn et StepOut reprennent
// l'exécution de thread.
func (c *Client) Continue(ctx context.Context, thread int) error {
	return c.call(ctx, "continue", map[string]int{"threadId": thread}, nil)
}

func (c *Client) Next(ctx context.Context, thread int) error {
	return c.call(ctx, "next", map[string]int{"threadId": thread}, nil)
}

func (c *Client) StepIn(ctx context.Context, thread int) error {
	return c.call(ctx, "stepIn", map[string]int{"threadId": thread}, nil)
}

func (c *Client) StepOut(ctx context.Context, thread int) error {
	return c.call(ctx, "stepOut", map[string]int{"threadId": thread}, nil)
}

// Pause interrompt l'exécution de thread.
func (c *Client) Pause(ctx context.Context, thread int) error {
	return c.call(ctx, "pause", map[string]int{"threadId": thread}, nil)
}

// StackTrace renvoie la pile d'appels de thread (au plus levels niveaux).
func (c *Client) StackTrace(ctx context.Context, thread, levels int) ([]Frame, error) {
	var res struct {
		Frames []Frame `json:"stackFrames"`
	}
	err := c.call(ctx, "stackTrace", map[string]int{"threadId": thread, "levels": levels}, &res)
	return res.Frames, err
}

// Scopes renvoie les groupes de variables d'un niveau de la pile.
func (c *Client) Scopes(ctx context.Context, frame int) ([]Scope, error) {
	var res struct {
		Scopes []Scope `json:"scopes"`
	}
	err := c.call(ctx, "scopes", map[string]int{"frameId": frame}, &res)
	return res.Scopes, err
}

// Variables renvoie les variables d'un groupe, ou les éléments d'une
// variable.
func (c *Client) Variables(ctx context.Context, ref int) ([]Variable, error) {
	var res struct {
		Variables []Variable `json:"variables"`
	}
	err := c.call(ctx, "variables", map[string]int{"variablesReference": ref}, &res)
	return res.Variables, err
}

// Close termine la session : arrêt du programme, puis de Delve.
func (c *Client) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = c.call(ctx, "disconnect", map[string]bool{"terminateDebuggee": true}, nil)
	_ = c.conn.Close()
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
}

// Done est fermé quand la connexion avec Delve est perdue.
func (c *Client) Done() <-chan struct{} { return c.done }

// ─── Protocole ───────────────────────────────────────────────────────────────

func (c *Client) call(ctx context.Context, command string, args, result any) error {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.err
	}
	c.seq++
	seq := c.seq
	ch := make(chan response, 1)
	c.calls[seq] = ch
	c.mu.Unlock()

	msg := map[string]any{"seq": seq, "type": "request", "command": command}
	if args != nil {
		msg["arguments"] = args
	}
	if err := c.send(msg); err != nil {
		return err
	}
	select {
	case r := <-ch:
		if r.err != nil || result == nil || len(r.body) == 0 {
			return r.err
		}
		return json.Unmarshal(r.body, result)
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.calls, seq)
		c.mu.Unlock()
		return ctx.Err()
	case <-c.done:
		return ErrClosed
	}
}

func (c *Client) send(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if _, err := fmt.Fprintf(c.conn, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	_, err = c.conn.Write(data)
	return err
}

func (c *Client) read(r *bufio.Reader) {
	err := c.readLoop(r)
	c.mu.Lock()
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		err = ErrClosed
	}
	c.err = err
	for seq, ch := range c.calls {
		ch <- response{err: err}
		delete(c.calls, seq)
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
		var m message
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		switch m.Type {
		case "response":
			c.mu.Lock()
			ch := c.calls[m.RequestSeq]
			delete(c.calls, m.RequestSeq)
			c.mu.Unlock()
			if ch == nil {
				continue
			}
			resp := response{body: m.Body}
			if !m.Success {
				resp.err = errors.New(responseError(m))
			}
			ch <- resp
		case "event":
			if c.onEvent != nil {
				c.onEvent(Event{Name: m.Event, Body: m.Body})
			}
		case "request": // requête de Delve (runInTerminal…) : non prise en charge
			_ = c.send(map[string]any{"seq": 0, "type": "response", "request_seq": m.Seq,
				"command": m.Command, "success": false, "message": "non pris en charge"})
		}
	}
}

// responseError extrait le message d'une réponse en échec.
func responseError(m message) string {
	var body struct {
		Error struct {
			Format string `json:"format"`
		} `json:"error"`
	}
	if json.Unmarshal(m.Body, &body) == nil && body.Error.Format != "" {
		return body.Error.Format
	}
	if m.Message != "" {
		return m.Message
	}
	return m.Command
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
