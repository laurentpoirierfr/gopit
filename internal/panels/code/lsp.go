package code

// Connexion à gopls (« gopls serve ») : les fichiers Go ouverts lui sont
// envoyés au fil de la frappe, et ses diagnostics sont soulignés dans le
// texte. La connexion démarre au premier fichier Go ouvert ; sans gopls (ou
// avec GOPIT_GOPLS=off), l'éditeur fonctionne sans.

import (
	"context"
	"os"
	"os/exec"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/laurentpoirierfr/gopit/internal/lsp"
)

type lspState int

const (
	lspOff      lspState = iota // pas encore démarrée
	lspStarting                 // démarrage en cours
	lspReady
	lspDisabled // gopls absent, en échec ou désactivé
)

// syncDelay regroupe les frappes rapprochées avant d'envoyer le texte.
const syncDelay = 150 * time.Millisecond

// startFunc démarre le serveur ; remplacée dans les tests.
type startFunc func(ctx context.Context, root string, onDiag func(lsp.Diagnostics)) (*lsp.Client, error)

type lspReadyMsg struct {
	c   *lsp.Client
	err error
}

type lspDiagMsg lsp.Diagnostics

type lspSyncMsg struct{ path string }

// lspEnabled indique si la connexion à gopls est possible.
func lspEnabled() bool {
	if os.Getenv("GOPIT_GOPLS") == "off" {
		return false
	}
	_, err := exec.LookPath("gopls")
	return err == nil
}

// lspSync démarre la connexion si besoin, puis envoie à gopls les fichiers
// Go ouverts ou modifiés. Elle est appelée après chaque message.
func (m *Model) lspSync() tea.Cmd {
	switch m.lspState {
	case lspOff:
		if !m.hasGoDoc() {
			return nil
		}
		if m.start == nil {
			m.lspState = lspDisabled
			return nil
		}
		m.lspState = lspStarting
		events := make(chan tea.Msg, 256)
		m.events = events
		start, root := m.start, m.proj.Root
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			c, err := start(ctx, root, func(d lsp.Diagnostics) { events <- lspDiagMsg(d) })
			return lspReadyMsg{c: c, err: err}
		}
	case lspReady:
		var cmds []tea.Cmd
		for _, d := range m.docs {
			if !isGo(d) {
				continue
			}
			switch {
			case !d.lspOpen:
				d.lspOpen, d.sentVer = true, d.buf.Version()
				_ = m.lsp.Open(d.path, d.buf.String(), d.sentVer)
			case d.buf.Version() != d.sentVer && !d.syncPending:
				d.syncPending = true
				path := d.path
				cmds = append(cmds, tea.Tick(syncDelay, func(time.Time) tea.Msg { return lspSyncMsg{path} }))
			}
		}
		return tea.Batch(cmds...)
	}
	return nil
}

// onLSP traite les messages de la connexion ; ok est faux pour les autres.
func (m *Model) onLSP(msg tea.Msg) (cmd tea.Cmd, ok bool) {
	switch msg := msg.(type) {
	case lspReadyMsg:
		if msg.err != nil {
			m.lspState = lspDisabled
			return nil, true
		}
		m.lsp, m.lspState = msg.c, lspReady
		return m.waitEvent(), true
	case lspDiagMsg:
		if len(msg.Items) == 0 {
			delete(m.diags, msg.Path)
		} else {
			items := msg.Items
			sort.SliceStable(items, func(i, j int) bool { return items[i].Start.Line < items[j].Start.Line })
			m.diags[msg.Path] = items
		}
		return m.waitEvent(), true
	case lspSyncMsg:
		for _, d := range m.docs {
			if d.path == msg.path {
				d.syncPending = false
				m.sendChange(d)
			}
		}
		return nil, true
	}
	return nil, false
}

// sendChange envoie le texte d'un document s'il a changé depuis le dernier
// envoi.
func (m *Model) sendChange(d *doc) {
	if m.lsp == nil || !d.lspOpen || d.buf.Version() == d.sentVer {
		return
	}
	d.sentVer = d.buf.Version()
	_ = m.lsp.Change(d.path, d.buf.String(), d.sentVer)
}

func (m *Model) waitEvent() tea.Cmd {
	ch := m.events
	return func() tea.Msg { return <-ch }
}

func (m *Model) hasGoDoc() bool {
	for _, d := range m.docs {
		if isGo(d) {
			return true
		}
	}
	return false
}

// lspClosed signale la fermeture d'un document.
func (m *Model) lspClosed(d *doc) {
	if m.lsp != nil && d.lspOpen {
		_ = m.lsp.CloseDoc(d.path)
	}
	delete(m.diags, d.path)
}

// lspSaved signale l'enregistrement d'un document.
func (m *Model) lspSaved(d *doc) {
	if m.lsp != nil && d.lspOpen {
		m.sendChange(d) // gofmt a pu modifier le texte
		_ = m.lsp.Save(d.path)
	}
}

// lineDiags renvoie les diagnostics qui touchent la ligne l du document.
func (m *Model) lineDiags(d *doc, l int) []lsp.Diagnostic {
	var out []lsp.Diagnostic
	for _, dg := range m.diags[d.path] {
		if dg.Start.Line <= l && l <= max(dg.Start.Line, dg.End.Line) {
			out = append(out, dg)
		}
	}
	return out
}

// diagSpans renvoie, pour une ligne de n runes, la gravité de chaque colonne
// (0 : aucun diagnostic) et la plus forte de la ligne (pour la marge).
func diagSpans(ds []lsp.Diagnostic, l, n int) (cols []lsp.Severity, worst lsp.Severity) {
	for _, dg := range ds {
		from, to := 0, n
		if dg.Start.Line == l {
			from = dg.Start.Col
		}
		if dg.End.Line == l {
			to = dg.End.Col
		}
		if to <= from { // position seule : souligner un caractère
			to = from + 1
		}
		if cols == nil {
			cols = make([]lsp.Severity, n+1)
		}
		for c := max(0, from); c < to && c <= n; c++ {
			if cols[c] == 0 || dg.Severity < cols[c] {
				cols[c] = dg.Severity
			}
		}
		if worst == 0 || dg.Severity < worst {
			worst = dg.Severity
		}
	}
	return cols, worst
}
