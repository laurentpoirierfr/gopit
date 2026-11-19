package code

import (
	"fmt"
	"image/color"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/lsp"
	"github.com/laurentpoirierfr/gopit/internal/scroll"
)

// runeWidth renvoie la largeur d'affichage d'une rune (hors tabulation).
func runeWidth(r rune) int {
	if r < ' ' || r == 0x7f {
		return 1 // caractère de contrôle, affiché « · »
	}
	return ansi.StringWidth(string(r))
}

// advance renvoie la colonne d'affichage après la rune r placée en col.
func advance(r rune, col int) int {
	if r == '\t' {
		return col + tabWidth - col%tabWidth
	}
	return col + runeWidth(r)
}

// dispCol convertit une colonne en runes en colonne d'affichage.
func dispCol(line []rune, col int) int {
	x := 0
	for _, r := range line[:min(col, len(line))] {
		x = advance(r, x)
	}
	return x
}

// runeAtCol renvoie la colonne en runes la plus proche de la colonne
// d'affichage x (clic de souris).
func runeAtCol(line []rune, x int) int {
	col := 0
	for i, r := range line {
		next := advance(r, col)
		if x < next {
			if x-col > next-x-1 { // plus près de la fin du caractère
				return i + 1
			}
			return i
		}
		col = next
	}
	return len(line)
}

// classes renvoie la coloration du fichier, recalculée à chaque modification.
func (d *doc) classes() [][]class {
	lang := d.language()
	if lang.color == nil {
		return nil
	}
	if v := d.buf.Version(); v != d.hlVer || d.hl == nil {
		d.hl, d.hlVer = lang.color(d.buf.String()), v
	}
	return d.hl
}

// language renvoie la coloration du fichier (choisie une fois pour toutes).
func (d *doc) language() language {
	if d.lang == nil {
		first := ""
		if d.buf.LineCount() > 0 {
			first = d.buf.Line(0)
		}
		l := detectLanguage(d.path, first)
		d.lang = &l
	}
	return *d.lang
}

// cellStyle est l'apparence d'un caractère.
type cellStyle struct {
	cls    class
	sel    bool // sélectionné
	cursor bool
	line   bool         // ligne du curseur
	diag   lsp.Severity // diagnostic de gopls (0 : aucun)
	stop   bool         // ligne où le programme débogué est arrêté
	match  bool         // parenthèse du curseur ou sa correspondante
}

// sevColor renvoie la couleur d'une gravité de diagnostic.
func (m *Model) sevColor(s lsp.Severity) color.Color {
	switch s {
	case lsp.Error:
		return m.th.P.Red
	case lsp.Warning:
		return m.th.P.Yellow
	}
	return m.th.P.Blue
}

func (m *Model) style(cache map[cellStyle]lipgloss.Style, cs cellStyle) lipgloss.Style {
	if st, ok := cache[cs]; ok {
		return st
	}
	p := m.th.P
	st := lipgloss.NewStyle()
	switch cs.cls {
	case clKeyword:
		st = st.Foreground(p.Accent)
	case clString:
		st = st.Foreground(p.Green)
	case clNumber:
		st = st.Foreground(p.Peach)
	case clComment:
		st = st.Foreground(p.Overlay0).Italic(true)
	case clFunc:
		st = st.Foreground(p.Blue)
	case clBuiltin:
		st = st.Foreground(p.Yellow)
	case clHeading:
		st = st.Foreground(p.Accent).Bold(true)
	case clAdded:
		st = st.Foreground(p.Green)
	case clRemoved:
		st = st.Foreground(p.Red)
	default:
		st = st.Foreground(p.Text)
	}
	switch {
	case cs.sel:
		st = st.Background(p.Surface1)
	case cs.stop:
		st = st.Background(p.Surface1)
	case cs.line:
		st = st.Background(p.Surface0)
	}
	if cs.match {
		st = st.Background(p.Surface1).Bold(true)
	}
	if cs.diag != 0 {
		st = st.UnderlineStyle(lipgloss.UnderlineCurly).UnderlineColor(m.sevColor(cs.diag))
	}
	if cs.cursor {
		st = st.Reverse(true)
	}
	cache[cs] = st
	return st
}

// ─── Vue ─────────────────────────────────────────────────────────────────────

func (m *Model) View(width, height int) string {
	if height < 3 || width < 10 {
		return ""
	}
	m.textH = height - 2
	// Arbre à gauche (toute la hauteur), séparé du texte par un trait.
	tw := m.treeWidth(width)
	m.treeW = 0
	if tw > 0 {
		m.treeW = tw + 1
		width -= m.treeW
	}
	body := ""
	switch d := m.doc(); {
	case m.picker != nil:
		body = m.viewPicker(width, m.textH)
	case d == nil:
		body = m.viewWelcome()
	case d.image != nil:
		m.textW = width
		body = m.viewImage(d, width, m.textH)
	default:
		body = m.viewText(d, width, m.textH)
		if m.info != "" { // documentation du symbole, en bas du texte
			box := strings.Split(m.viewInfo(width, m.textH*2/3), "\n")
			lines := strings.Split(body, "\n")
			keep := max(0, min(len(lines), m.textH-len(box)))
			body = strings.Join(append(lines[:keep], box...), "\n")
		}
	}
	body = lipgloss.NewStyle().Height(m.textH).MaxHeight(m.textH).Render(body)
	if d := m.doc(); m.compl != nil && d != nil && m.picker == nil {
		body = m.overlayCompletion(d, body, width)
	}
	right := m.viewTabs(width) + "\n" + body + "\n" + m.viewStatus(width)
	if tw == 0 {
		return right
	}
	sepColor := m.th.P.Surface1
	if m.tree.focus {
		sepColor = m.th.P.Accent
	}
	sep := lipgloss.NewStyle().Foreground(sepColor).Render(strings.TrimSuffix(strings.Repeat("│\n", height), "\n"))
	return lipgloss.JoinHorizontal(lipgloss.Top, m.viewTree(tw, height), sep,
		lipgloss.NewStyle().Height(height).MaxHeight(height).Render(right))
}

func (m *Model) viewWelcome() string {
	t := m.th
	return strings.Join([]string{
		"",
		t.Value.Render(i18n.T("Aucun fichier ouvert.")),
		"",
		t.HintKey.Render(i18n.T("Entrée")) + t.Dim.Render(i18n.T(" ou ")) + t.HintKey.Render("Ctrl+O") + t.Text.Render(i18n.T("  ouvrir un fichier du projet")),
		t.Dim.Render(i18n.T("Ou choisis-le dans l'arbre à gauche (Ctrl+E pour y aller, Ctrl+B pour l'afficher).")),
		t.Dim.Render(i18n.T("Depuis les modules Tests, Diagnostics, Git…, ouvrir un fichier l'affiche ici.")),
		"",
		t.Label.Render(i18n.T("En édition : Ctrl+S enregistre (avec gofmt), Échap revient en lecture.")),
	}, "\n")
}

func (m *Model) viewTabs(w int) string {
	t := m.th
	m.tabSpans = m.tabSpans[:0]
	if len(m.docs) == 0 {
		return ""
	}
	var b strings.Builder
	x := 0
	for i, d := range m.docs {
		// La croix ferme l'onglet ; un point la remplace si le fichier est
		// modifié (un clic dessus demande alors confirmation).
		mark := "×"
		if d.buf.Dirty() {
			mark = "●"
		}
		label := " " + filepath.Base(d.path) + " " + mark + " "
		st := t.NavItemBlur.Padding(0)
		if i == m.cur {
			st = t.NavItemSel.Padding(0)
			if !m.editing || m.tree.focus {
				st = st.Background(t.P.Blue)
			}
		}
		lw := lipgloss.Width(label)
		m.tabSpans = append(m.tabSpans, span{x, lw, x + lw - 2})
		b.WriteString(st.Render(label))
		b.WriteByte(' ')
		x += lw + 1
	}
	return ansi.Truncate(b.String(), w, "…")
}

func (m *Model) viewStatus(w int) string {
	t := m.th
	d := m.doc()
	switch m.prompt {
	case promptFind:
		s := t.Label.Render(i18n.T("Rechercher : ")) + t.Value.Render(m.input) + t.Text.Reverse(true).Render(" ")
		if m.notFound {
			s += t.Err.Render(i18n.T("  introuvable"))
		}
		return s
	case promptGoto:
		return t.Label.Render(fmt.Sprintf(i18n.T("Aller à la ligne (1-%d) : "), d.buf.LineCount())) +
			t.Value.Render(m.input) + t.Text.Reverse(true).Render(" ")
	case promptNew:
		return t.Label.Render(i18n.T("Nouveau fichier : ")) + t.Value.Render(m.input) + t.Text.Reverse(true).Render(" ")
	case promptReplace:
		s := t.Label.Render(i18n.T("Remplacer : ")) + t.Value.Render(m.input) + t.Text.Reverse(true).Render(" ")
		if m.notFound {
			s += t.Err.Render(i18n.T("  introuvable"))
		}
		return s
	case promptWith:
		return t.Label.Render(fmt.Sprintf(i18n.T("Remplacer « %s » par : "), m.repl.find)) +
			t.Value.Render(m.input) + t.Text.Reverse(true).Render(" ")
	case promptConfirm:
		return t.Label.Render(fmt.Sprintf(i18n.T("Remplacer par « %s » ?"), m.repl.with)) +
			t.Dim.Render(fmt.Sprintf(i18n.T("  %d remplacement(s)"), m.repl.count))
	}
	if d == nil {
		return ""
	}
	mode := lipgloss.NewStyle().Bold(true).Foreground(t.P.Base).Padding(0, 1)
	if d.image != nil { // image : format, dimensions et taille
		left := mode.Background(t.P.Peach).Render(i18n.T("IMAGE")) + " " + t.Text.Render(d.rel)
		right := t.Dim.Render(imageStatus(d.image.info))
		if m.hd {
			right += t.Dim.Render(" · HD")
		}
		gap := w - lipgloss.Width(left) - lipgloss.Width(right)
		if gap < 1 {
			return ansi.Truncate(left, w, "…")
		}
		return left + strings.Repeat(" ", gap) + right
	}
	left := mode.Background(t.P.Blue).Render(i18n.T("LECTURE"))
	if m.editing {
		left = mode.Background(t.P.Green).Render(i18n.T("ÉDITION"))
	}
	left += " " + t.Text.Render(d.rel)
	if d.buf.Dirty() {
		left += t.Warn.Render(" ●")
	}
	c := d.buf.Cursor()
	right := fmt.Sprintf(i18n.T("Ln %d, Col %d"), c.Line+1, dispCol([]rune(d.buf.Line(c.Line)), c.Col)+1)
	if sel := d.buf.SelectedText(); sel != "" {
		right = fmt.Sprintf(i18n.T("%d sélectionnés · "), len([]rune(sel))) + right
	}
	lang := d.language().name
	if lang == "" {
		lang = i18n.T("Texte")
	}
	right = t.Dim.Render(right + " · " + lang)
	if ds := m.lineDiags(d, c.Line); len(ds) > 0 { // problème sur la ligne du curseur
		msg := strings.ReplaceAll(ds[0].Message, "\n", " ")
		if ds[0].Source != "" {
			msg = fmt.Sprintf(i18n.T("%s : %s"), ds[0].Source, msg)
		}
		room := w - lipgloss.Width(left) - lipgloss.Width(right) - 3
		if room > 10 {
			left += "  " + lipgloss.NewStyle().Foreground(m.sevColor(ds[0].Severity)).Render(ansi.Truncate(msg, room, "…"))
		}
	}
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return ansi.Truncate(left, w, "…")
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *Model) viewPicker(w, h int) string {
	t := m.th
	p := m.picker
	open := map[string]bool{}
	for _, d := range m.docs {
		open[d.rel] = true
	}
	head := t.Label.Render(i18n.T("Ouvrir : ")) + t.Value.Render(p.query) + t.Text.Reverse(true).Render(" ") +
		t.Dim.Render(fmt.Sprintf(i18n.T("   %d / %d fichiers"), len(p.matches), len(p.all)))
	rows := max(1, h-1)
	if p.cursor < p.offset {
		p.offset = p.cursor
	} else if p.cursor >= p.offset+rows {
		p.offset = p.cursor - rows + 1
	}
	lines := []string{head}
	for i := p.offset; i < min(len(p.matches), p.offset+rows); i++ {
		f := p.matches[i]
		mark := "  "
		if open[f] {
			mark = "● "
		}
		label := ansi.Truncate(mark+f, w-2, "…")
		if i == p.cursor {
			lines = append(lines, t.NavItemSel.Width(w).Render(label))
		} else {
			lines = append(lines, t.NavItem.Render(label))
		}
	}
	if len(p.matches) == 0 {
		lines = append(lines, t.Dim.Render(i18n.T("  Aucun fichier ne correspond.")))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) viewText(d *doc, w, h int) string {
	t := m.th
	b := d.buf
	n := b.LineCount()
	digits := len(strconv.Itoa(n))
	m.gutterW = digits + 2
	textW := max(1, w-m.gutterW-2) // 2 : ascenseur

	c := b.Cursor()
	cline := []rune(b.Line(c.Line))
	switch {
	case m.center:
		d.top = c.Line - h/3
	case m.follow:
		if c.Line < d.top {
			d.top = c.Line
		} else if c.Line >= d.top+h {
			d.top = c.Line - h + 1
		}
	}
	if m.center || m.follow {
		x := dispCol(cline, c.Col)
		if x < d.left {
			d.left = max(0, x-textW/4)
		} else if x >= d.left+textW {
			d.left = x - textW + textW/4
		}
	}
	m.center, m.follow = false, false
	d.top = scroll.Clamp(d.top, h, n)

	cls := d.classes()
	ss, se, hasSel := b.Selection()
	marks := m.bracketMarks(d)
	cache := map[cellStyle]lipgloss.Style{}
	gutter := t.Dim
	gutterCur := t.Title

	lines := make([]string, 0, h)
	for row := range h {
		l := d.top + row
		if l >= n {
			break
		}
		var lcls []class
		if l < len(cls) {
			lcls = cls[l]
		}
		// Sélection sur cette ligne : [from, to) en runes, eol si elle
		// continue sur la ligne suivante.
		from, to, eol := -1, -1, false
		if hasSel && l >= ss.Line && l <= se.Line {
			from, to = 0, len(b.Line(l))
			if l == ss.Line {
				from = ss.Col
			}
			if l == se.Line {
				to = se.Col
			} else {
				eol = true
			}
		}
		cur := -1
		if m.editing && l == c.Line {
			cur = c.Col
		}
		text := []rune(b.Line(l))
		dcols, worst := diagSpans(m.lineDiags(d, l), l, len(text))
		gst := gutter
		if l == c.Line {
			gst = gutterCur
		}
		num := gst.Render(fmt.Sprintf("%*d ", digits+1, l+1))
		// Marque dans la marge, à la place de l'espace : ligne d'arrêt du
		// débogueur, point d'arrêt, puis diagnostic.
		stopped := m.dbg.StopLine == l+1 && m.dbg.StopPath == d.path
		switch {
		case stopped:
			num = gst.Render(fmt.Sprintf("%*d", digits+1, l+1)) + lipgloss.NewStyle().Foreground(t.P.Yellow).Bold(true).Render("▶")
		case m.dbg.Has(d.path, l+1):
			num = gst.Render(fmt.Sprintf("%*d", digits+1, l+1)) + lipgloss.NewStyle().Foreground(t.P.Red).Render("●")
		case worst != 0:
			num = gst.Render(fmt.Sprintf("%*d", digits+1, l+1)) + lipgloss.NewStyle().Foreground(m.sevColor(worst)).Render("●")
		}
		lines = append(lines, num+m.renderLine(text, lcls, dcols, marks[l], from, to, eol, cur, l == c.Line && m.editing, stopped, d.left, textW, cache))
	}
	return scroll.Render(lines, m.gutterW+textW, h, n, d.top, lipgloss.NewStyle().Foreground(t.P.Surface1),
		lipgloss.NewStyle().Foreground(t.P.Accent))
}

// renderLine dessine une ligne de texte entre les colonnes d'affichage left
// et left+w.
func (m *Model) renderLine(line []rune, cls []class, dcols []lsp.Severity, marks []int, from, to int, eol bool, cursor int, curLine, stop bool,
	left, w int, cache map[cellStyle]lipgloss.Style) string {
	var out strings.Builder
	var run strings.Builder
	var runSt cellStyle
	used := 0
	emit := func(text string, cs cellStyle) {
		if run.Len() > 0 && cs != runSt {
			out.WriteString(m.style(cache, runSt).Render(run.String()))
			run.Reset()
		}
		runSt = cs
		run.WriteString(text)
		used += ansi.StringWidth(text)
	}

	col := 0
	for i, r := range line {
		next := advance(r, col)
		cs := cellStyle{line: curLine, sel: i >= from && i < to, stop: stop}
		if i < len(cls) {
			cs.cls = cls[i]
		}
		if i < len(dcols) {
			cs.diag = dcols[i]
		}
		cs.match = slices.Contains(marks, i)
		// Partie visible du caractère.
		vs, ve := max(col, left), min(next, left+w)
		if vs < ve {
			switch {
			case r == '\t' || vs != col || ve != next: // tabulation ou caractère coupé
				first := cs
				first.cursor = i == cursor && vs == col
				emit(" ", first)
				if ve-vs > 1 {
					emit(strings.Repeat(" ", ve-vs-1), cs)
				}
			default:
				cs.cursor = i == cursor
				txt := string(r)
				if r < ' ' || r == 0x7f {
					txt = "·"
				}
				emit(txt, cs)
			}
		}
		col = next
		if col >= left+w {
			break
		}
	}
	// Après la fin de la ligne : curseur, sélection qui continue, fond.
	if col >= left && col < left+w {
		switch {
		case cursor == len(line):
			emit(" ", cellStyle{line: curLine, cursor: true, sel: eol})
		case eol:
			emit(" ", cellStyle{line: curLine, sel: true})
		}
	}
	if (curLine || stop) && used < w {
		emit(strings.Repeat(" ", w-used), cellStyle{line: curLine, stop: stop})
	}
	if run.Len() > 0 {
		out.WriteString(m.style(cache, runSt).Render(run.String()))
	}
	return out.String()
}

// overlayCompletion pose la liste de complétion sous le mot complété (ou
// au-dessus, faute de place).
func (m *Model) overlayCompletion(d *doc, body string, width int) string {
	box := m.viewCompletion(width)
	bw, bh := lipgloss.Width(box), lipgloss.Height(box)
	start := m.compl.start
	row := start.Line - d.top
	x := m.gutterW + dispCol([]rune(d.buf.Line(start.Line)), start.Col) - d.left
	y := row + 1
	if y+bh > m.textH {
		y = row - bh
	}
	x, y = max(0, min(x, width-bw)), max(0, y)
	return lipgloss.NewCompositor(lipgloss.NewLayer(body), lipgloss.NewLayer(box).X(x).Y(y).Z(1)).Render()
}
