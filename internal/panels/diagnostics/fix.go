package diagnostics

// Corrections proposées : pour le diagnostic choisi, gopls (lancé à la
// demande, avec ses analyses staticcheck) est interrogé sur ses corrections
// rapides (« quickfix ») ; pour gofmt, la correction est le fichier formaté.
// Une correction est un nouveau contenu du fichier, montré en aperçu et
// appliqué dans l'éditeur.

import (
	"context"
	"errors"
	"go/format"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/laurentpoirierfr/gopit/internal/diag"
	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/lsp"
	"github.com/laurentpoirierfr/gopit/internal/panel"
)

// fix est une correction : le contenu du fichier avant et après.
type fix struct {
	title      string
	path       string
	base, text string
	line       int // première ligne modifiée (à partir de 1)
}

// fixState est l'état des corrections d'un diagnostic.
type fixState struct {
	loading bool
	fixes   []fix
	rule    string // règle du diagnostic de gopls correspondant
	err     error
}

type fixesMsg struct {
	key   string
	fixes []fix
	rule  string
	err   error
}

// fixEnabled indique si gopls peut proposer des corrections.
func fixEnabled() bool {
	if os.Getenv("GOPIT_GOPLS") == "off" {
		return false
	}
	_, err := exec.LookPath("gopls")
	return err == nil
}

// quiet est le temps sans nouvelle publication de gopls après lequel ses
// diagnostics d'un fichier sont tenus pour complets (les analyses arrivent
// après la vérification des types).
var quiet = 2 * time.Second

// analysisWait borne l'attente d'un problème que gopls n'a pas encore
// signalé : sans cache (première utilisation), ses analyses staticcheck
// étudient d'abord la bibliothèque standard, bien après ses premiers
// diagnostics.
var analysisWait = 20 * time.Second

// fixer est la connexion à gopls dédiée aux corrections.
type fixer struct {
	root string

	mu      sync.Mutex // démarrage et documents envoyés
	c       *lsp.Client
	err     error
	sent    map[string]string // texte envoyé, par fichier
	version map[string]int
	waited  map[string]string // texte pour lequel l'attente des analyses a été faite

	dmu    sync.Mutex // diagnostics reçus
	got    map[string][]lsp.Diagnostic
	count  map[string]int
	signal chan struct{} // fermé à chaque publication
}

func newFixer(root string) *fixer {
	return &fixer{root: root, sent: map[string]string{}, version: map[string]int{}, waited: map[string]string{},
		got: map[string][]lsp.Diagnostic{}, count: map[string]int{}, signal: make(chan struct{})}
}

func (f *fixer) onDiag(d lsp.Diagnostics) {
	f.dmu.Lock()
	f.got[d.Path] = d.Items
	f.count[d.Path]++
	close(f.signal)
	f.signal = make(chan struct{})
	f.dmu.Unlock()
}

// client démarre gopls la première fois.
func (f *fixer) client(ctx context.Context) (*lsp.Client, error) {
	if f.c == nil && f.err == nil {
		f.c, f.err = lsp.StartWith(ctx, f.root, map[string]any{"staticcheck": true}, f.onDiag)
	}
	return f.c, f.err
}

// sync envoie à gopls le texte du fichier et attend ses diagnostics.
func (f *fixer) sync(ctx context.Context, path, text string) ([]lsp.Diagnostic, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, err := f.client(ctx)
	if err != nil {
		return nil, err
	}
	f.dmu.Lock()
	before := f.count[path]
	f.dmu.Unlock()
	if sent, ok := f.sent[path]; ok && sent == text && before > 0 {
		f.dmu.Lock()
		defer f.dmu.Unlock()
		return f.got[path], nil
	}
	f.version[path]++
	if f.version[path] == 1 {
		err = c.Open(path, text, 1)
	} else {
		err = c.Change(path, text, f.version[path])
	}
	if err != nil {
		return nil, err
	}
	f.sent[path] = text
	for {
		f.dmu.Lock()
		n, sig, got := f.count[path], f.signal, f.got[path]
		f.dmu.Unlock()
		var timeout <-chan time.Time
		if n > before {
			timeout = time.After(quiet)
		}
		select {
		case <-sig:
		case <-timeout:
			return got, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// fixes cherche les corrections du diagnostic d ; rule est la règle du
// diagnostic de gopls qui lui correspond.
func (f *fixer) fixes(ctx context.Context, d diag.Diagnostic) (out []fix, rule string, err error) {
	data, err := os.ReadFile(d.File)
	if err != nil {
		return nil, "", err
	}
	base := string(data)
	if d.Code == "gofmt" {
		out, err := format.Source(data)
		if err != nil {
			return nil, "gofmt", err
		}
		return newFixes(nil, fix{title: i18n.T("Formater le fichier (gofmt)"), path: d.File, base: base, text: string(out)}), "gofmt", nil
	}
	if f == nil {
		return nil, "", nil
	}
	diags, err := f.sync(ctx, d.File, base)
	if err != nil {
		return nil, "", err
	}
	if !slices.ContainsFunc(diags, func(g lsp.Diagnostic) bool { return sameProblem(d, g) }) {
		diags = f.awaitProblem(ctx, d, base)
	}
	for _, g := range diags {
		if !sameProblem(d, g) {
			continue
		}
		if rule == "" {
			rule = g.Source
		}
		acts, err := f.c.CodeActions(ctx, d.File, g.Start, g.End, []lsp.Diagnostic{g}, "quickfix")
		if err != nil {
			return out, rule, err
		}
	actions:
		for _, a := range acts {
			for _, e := range a.Edits {
				if e.Path != d.File {
					continue actions // plusieurs fichiers : pas d'aperçu simple
				}
			}
			out = newFixes(out, fix{title: a.Title, path: d.File, base: base, text: lsp.Apply(base, d.File, a.Edits)})
		}
	}
	return out, rule, nil
}

// awaitProblem attend que gopls signale le problème d (analyses encore en
// cours), au plus analysisWait, une seule fois par texte du fichier ; il
// renvoie ses derniers diagnostics du fichier.
func (f *fixer) awaitProblem(ctx context.Context, d diag.Diagnostic, text string) []lsp.Diagnostic {
	f.mu.Lock()
	first := f.waited[d.File] != text
	f.waited[d.File] = text
	f.mu.Unlock()
	timeout := time.After(analysisWait)
	for {
		f.dmu.Lock()
		got, sig := f.got[d.File], f.signal
		f.dmu.Unlock()
		if !first || slices.ContainsFunc(got, func(g lsp.Diagnostic) bool { return sameProblem(d, g) }) {
			return got
		}
		select {
		case <-sig:
		case <-timeout:
			return got
		case <-ctx.Done():
			return got
		}
	}
}

// sameProblem indique si le diagnostic g de gopls est le problème d : même
// ligne, et même règle ou même message (jamais la correction d'un autre
// problème de la ligne).
func sameProblem(d diag.Diagnostic, g lsp.Diagnostic) bool {
	line := d.Line - 1
	if line < g.Start.Line || line > g.End.Line {
		return false
	}
	return (d.Code != "" && g.Source == d.Code) || g.Message == d.Message ||
		strings.Contains(d.Message, g.Message) || strings.Contains(g.Message, d.Message)
}

// newFixes ajoute x à list s'il change le texte et n'y est pas déjà.
func newFixes(list []fix, x fix) []fix {
	if x.text == x.base {
		return list
	}
	for _, y := range list {
		if y.text == x.text {
			return list
		}
	}
	x.line, _ = changed(x.base, x.text)
	return append(list, x)
}

// changed renvoie la première ligne modifiée (à partir de 1) et le nombre de
// lignes communes à la fin.
func changed(a, b string) (first, suffix int) {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	p := 0
	for p < len(al) && p < len(bl) && al[p] == bl[p] {
		p++
	}
	s := 0
	for s < len(al)-p && s < len(bl)-p && al[len(al)-1-s] == bl[len(bl)-1-s] {
		s++
	}
	return p + 1, s
}

// previewLine est une ligne de l'aperçu : contexte (' '), retirée ('-') ou
// ajoutée ('+'), avec son numéro.
type previewLine struct {
	op   rune
	num  int
	text string
}

// preview renvoie l'aperçu de la correction : les lignes changées entourées
// de context lignes, au plus max lignes.
func (x fix) preview(context, max int) []previewLine {
	al, bl := strings.Split(x.base, "\n"), strings.Split(x.text, "\n")
	first, suffix := changed(x.base, x.text)
	p := first - 1
	var out []previewLine
	for i := max0(p - context); i < p; i++ {
		out = append(out, previewLine{' ', i + 1, al[i]})
	}
	for i := p; i < len(al)-suffix; i++ {
		out = append(out, previewLine{'-', i + 1, al[i]})
	}
	for i := p; i < len(bl)-suffix; i++ {
		out = append(out, previewLine{'+', i + 1, bl[i]})
	}
	for i := len(al) - suffix; i < len(al) && i < len(al)-suffix+context; i++ {
		out = append(out, previewLine{' ', i + 1, al[i]})
	}
	if len(out) > max {
		out = append(out[:max-1], previewLine{op: '…'})
	}
	return out
}

func max0(n int) int { return max(n, 0) }

// ensureFix lance la recherche des corrections du diagnostic choisi, s'il ne
// l'a pas déjà été.
func (m *Model) ensureFix() tea.Cmd {
	d := m.selected()
	if d == nil || !strings.HasSuffix(d.File, ".go") {
		return nil
	}
	k := key(d)
	if _, ok := m.fixes[k]; ok {
		return nil
	}
	if d.Code != "gofmt" && !m.canFix() {
		return nil
	}
	m.fixes[k] = &fixState{loading: true}
	f, dd := m.fixer, *d
	if dd.Code != "gofmt" && f == nil {
		f = newFixer(m.proj.Root)
		m.fixer = f
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		fixes, rule, err := f.fixes(ctx, dd)
		if errors.Is(err, context.DeadlineExceeded) {
			err = errors.New(i18n.T("gopls ne répond pas"))
		}
		return fixesMsg{key: k, fixes: fixes, rule: rule, err: err}
	}
}

// canFix indique si gopls est disponible pour les corrections (vérifié une
// fois).
func (m *Model) canFix() bool {
	if m.fixOK == 0 {
		m.fixOK = -1
		if fixEnabled() {
			m.fixOK = 1
		}
	}
	return m.fixOK > 0
}

// currentFix renvoie la correction affichée du diagnostic choisi.
func (m *Model) currentFix() (*fix, int) {
	d := m.selected()
	if d == nil {
		return nil, 0
	}
	st := m.fixes[key(d)]
	if st == nil || len(st.fixes) == 0 {
		return nil, 0
	}
	i := m.fixSel % len(st.fixes)
	return &st.fixes[i], len(st.fixes)
}

// applyFix envoie la correction affichée à l'éditeur.
func (m *Model) applyFix() tea.Cmd {
	x, _ := m.currentFix()
	if x == nil {
		return nil
	}
	msg := panel.ApplyMsg{Path: x.path, Base: x.base, Text: x.text, Line: x.line, Title: x.title}
	return func() tea.Msg { return msg }
}
