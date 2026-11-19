package code

// Complétion (Ctrl+Espace, ou après « . ») : gopls propose les symboles
// possibles au curseur ; la liste se filtre pendant la frappe.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/lsp"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/textbuf"
)

// actComplete ouvre la liste de complétion (action propre à ce module).
const actComplete panel.EditAction = "complete"

// complRows est le nombre de propositions visibles à la fois.
const complRows = 8

// completion est la liste de complétion ouverte.
type completion struct {
	path   string
	start  textbuf.Pos // début du mot complété
	items  []lsp.Item
	shown  []int // propositions retenues par le filtre (indices dans items)
	sel    int   // dans shown
	offset int
}

type complMsg struct {
	path     string
	at       textbuf.Pos // curseur au moment de la demande
	explicit bool        // Ctrl+Espace (sinon : après « . »)
	items    []lsp.Item
	err      error
}

// requestCompletion demande les propositions au curseur.
func (m *Model) requestCompletion(trigger string) tea.Cmd {
	d := m.doc()
	if d == nil || !isGo(d) {
		return nil
	}
	if m.lsp == nil {
		if trigger != "" {
			return nil // « . » tapé sans gopls : rien à proposer
		}
		return panel.Flash(i18n.T("Complétion indisponible : gopls n'est pas connecté"), true)
	}
	m.sendChange(d)
	c, path, cur := m.lsp, d.path, d.buf.Cursor()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		items, err := c.Completion(ctx, path, lsp.Pos{Line: cur.Line, Col: cur.Col}, trigger)
		return complMsg{path: path, at: cur, explicit: trigger == "", items: items, err: err}
	}
}

// onCompletion affiche les propositions reçues, si le curseur n'est pas
// parti ailleurs entre-temps.
func (m *Model) onCompletion(msg complMsg) tea.Cmd {
	if msg.err != nil {
		if errors.Is(msg.err, context.DeadlineExceeded) {
			return nil
		}
		return panel.Flash("gopls : "+msg.err.Error(), true)
	}
	d := m.doc()
	if d == nil || d.path != msg.path || !m.editing {
		return nil
	}
	cur := d.buf.Cursor()
	if cur.Line != msg.at.Line || cur.Col < msg.at.Col {
		return nil
	}
	if len(msg.items) == 0 {
		if msg.explicit {
			return panel.Flash(i18n.T("Aucune proposition"), false)
		}
		return nil
	}
	start := textbuf.Pos{Line: msg.at.Line, Col: wordStart([]rune(d.buf.Line(msg.at.Line)), msg.at.Col)}
	if it := msg.items[0]; it.HasStart && it.Start.Line == msg.at.Line && it.Start.Col <= msg.at.Col {
		start = textbuf.Pos{Line: it.Start.Line, Col: it.Start.Col}
	}
	m.compl = &completion{path: msg.path, start: start, items: msg.items}
	if !m.refilter() {
		m.compl = nil
	}
	return nil
}

func isIdent(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// wordStart renvoie le début de l'identifiant qui se termine en col.
func wordStart(line []rune, col int) int {
	col = min(col, len(line))
	for col > 0 && isIdent(line[col-1]) {
		col--
	}
	return col
}

// refilter garde les propositions qui commencent par le mot tapé (ou, à
// défaut, qui le contiennent), dans l'ordre de gopls. Il renvoie false s'il
// n'en reste aucune ou si le curseur a quitté le mot.
func (m *Model) refilter() bool {
	c, d := m.compl, m.doc()
	if c == nil || d == nil || d.path != c.path {
		return false
	}
	cur := d.buf.Cursor()
	line := []rune(d.buf.Line(cur.Line))
	if cur.Line != c.start.Line || cur.Col < c.start.Col || cur.Col > len(line) {
		return false
	}
	typed := line[c.start.Col:cur.Col]
	for _, r := range typed {
		if !isIdent(r) {
			return false
		}
	}
	word := strings.ToLower(string(typed))
	c.shown = c.shown[:0]
	for i, it := range c.items {
		if strings.HasPrefix(strings.ToLower(it.Filter), word) {
			c.shown = append(c.shown, i)
		}
	}
	if len(c.shown) == 0 {
		for i, it := range c.items {
			if strings.Contains(strings.ToLower(it.Filter), word) {
				c.shown = append(c.shown, i)
			}
		}
	}
	c.sel, c.offset = 0, 0
	return len(c.shown) > 0
}

// complKey traite les touches propres à la liste ouverte ; handled est faux
// pour les autres, qui modifient le texte normalement.
func (m *Model) complKey(s string) (handled bool, cmd tea.Cmd) {
	c := m.compl
	switch s {
	case "up":
		c.sel = (c.sel - 1 + len(c.shown)) % len(c.shown)
	case "down", "ctrl+n":
		c.sel = (c.sel + 1) % len(c.shown)
	case "pgup":
		c.sel = max(0, c.sel-complRows)
	case "pgdown":
		c.sel = min(len(c.shown)-1, c.sel+complRows)
	case "enter", "tab":
		return true, m.acceptCompletion()
	case "esc":
		m.compl = nil
	default:
		return false, nil
	}
	if m.compl != nil {
		if c.sel < c.offset {
			c.offset = c.sel
		} else if c.sel >= c.offset+complRows {
			c.offset = c.sel - complRows + 1
		}
	}
	return true, nil
}

// acceptCompletion remplace le mot commencé par la proposition choisie ;
// si elle vient d'un paquet pas encore importé, l'import est ajouté en même
// temps (une seule étape d'annulation).
func (m *Model) acceptCompletion() tea.Cmd {
	c, d := m.compl, m.doc()
	m.compl = nil
	if d == nil || len(c.shown) == 0 {
		return nil
	}
	it := c.items[c.shown[c.sel]]
	cur := d.buf.Cursor()
	m.follow = true
	if len(it.Extra) == 0 || strings.Contains(it.Text, "\n") {
		d.buf.SetCursor(c.start, false)
		d.buf.SetCursor(cur, true)
		d.buf.Insert(it.Text)
		return nil
	}
	edits := append([]lsp.TextEdit{{Path: d.path, Start: lsp.Pos(c.start), End: lsp.Pos(cur), NewText: it.Text}}, it.Extra...)
	// Lignes ajoutées au-dessus du mot (le bloc d'imports) : le curseur suit.
	delta := 0
	for _, e := range it.Extra {
		if e.End.Line < c.start.Line {
			delta += strings.Count(e.NewText, "\n") - (e.End.Line - e.Start.Line)
		}
	}
	d.buf.SetTextAt(lsp.Apply(d.buf.String(), d.path, edits),
		textbuf.Pos{Line: c.start.Line + delta, Col: c.start.Col + utf8.RuneCountInString(it.Text)})
	if pkg := importedPath(it.Extra); pkg != "" {
		return panel.Flash(i18n.T("Import ajouté : ")+pkg, false)
	}
	return nil
}

// importedPath renvoie le chemin entre guillemets ajouté par les
// modifications d'une proposition (l'import), ou "".
func importedPath(edits []lsp.TextEdit) string {
	for _, e := range edits {
		if i := strings.IndexByte(e.NewText, '"'); i >= 0 {
			if j := strings.IndexByte(e.NewText[i+1:], '"'); j >= 0 {
				return e.NewText[i : i+j+2]
			}
		}
	}
	return ""
}

// kindMark renvoie la lettre et la couleur qui résument la nature d'une
// proposition.
func (m *Model) kindMark(k lsp.Kind) string {
	p := m.th.P
	st := lipgloss.NewStyle().Bold(true)
	switch k {
	case lsp.KindFunction:
		return st.Foreground(p.Blue).Render("f")
	case lsp.KindMethod:
		return st.Foreground(p.Blue).Render("m")
	case lsp.KindVariable, lsp.KindField, lsp.KindProperty:
		return st.Foreground(p.Text).Render("v")
	case lsp.KindConstant:
		return st.Foreground(p.Peach).Render("c")
	case lsp.KindStruct, lsp.KindInterface, lsp.KindClass, lsp.KindTypeParam:
		return st.Foreground(p.Yellow).Render("t")
	case lsp.KindModule:
		return st.Foreground(p.Green).Render("p")
	case lsp.KindKeyword:
		return st.Foreground(p.Accent).Render("k")
	}
	return st.Foreground(p.Overlay0).Render("·")
}

// viewCompletion dessine la liste, au plus w colonnes de large.
func (m *Model) viewCompletion(w int) string {
	c, t := m.compl, m.th
	inner := max(10, min(w-2, 64))
	labelW := 0
	for _, i := range c.shown {
		labelW = max(labelW, ansi.StringWidth(c.items[i].Label))
	}
	labelW = min(labelW, inner-2)
	rows := make([]string, 0, complRows+1)
	for n := c.offset; n < len(c.shown) && n < c.offset+complRows; n++ {
		it := c.items[c.shown[n]]
		label := ansi.Truncate(it.Label, labelW, "…")
		label += strings.Repeat(" ", labelW-ansi.StringWidth(label))
		detail := ""
		if room := inner - 2 - labelW - 2; room > 3 && it.Detail != "" {
			detail = "  " + ansi.Truncate(strings.ReplaceAll(it.Detail, "\n", " "), room, "…")
		}
		line := label + detail
		line += strings.Repeat(" ", max(0, inner-2-ansi.StringWidth(line)))
		if n == c.sel {
			rows = append(rows, m.kindMark(it.Kind)+" "+lipgloss.NewStyle().Background(t.P.Accent).Foreground(t.P.Base).Render(line))
		} else {
			rows = append(rows, m.kindMark(it.Kind)+" "+t.Text.Render(label)+t.Dim.Render(strings.TrimPrefix(line, label)))
		}
	}
	if len(c.shown) > complRows {
		rows = append(rows, t.Dim.Render(fmt.Sprintf("  %d / %d", c.sel+1, len(c.shown))))
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(t.P.Accent).
		Render(strings.Join(rows, "\n"))
}
