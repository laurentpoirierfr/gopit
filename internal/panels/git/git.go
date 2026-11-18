// Package git est le panneau « Git » : état du dépôt, diff coloré, commits
// récents ; indexation, désindexation et commit. Aucune action destructive
// (pas d'annulation de modifications, pas de push).
package git

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/gitx"
	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/scroll"
	"github.com/laurentpoirierfr/gopit/internal/theme"
)

const (
	logSize = 30
	refresh = 3 * time.Second
)

type stateMsg struct {
	status  *gitx.Status
	commits []gitx.Commit
	err     error
}

type detailMsg struct {
	key, text string
	err       error
}

type actionMsg struct {
	done string // message de succès
	err  error
}

type tickMsg struct{}

// row est une ligne : en-tête de section, fichier (indexé ou non) ou commit.
type row struct {
	header string
	change *gitx.Change
	staged bool
	commit *gitx.Commit
}

func (r row) key() string {
	switch {
	case r.change != nil:
		return fmt.Sprintf("f|%v|%s", r.staged, r.change.Path)
	case r.commit != nil:
		return "c|" + r.commit.Hash
	}
	return ""
}

type Model struct {
	th   *theme.Theme
	proj *project.Project

	status  *gitx.Status
	commits []gitx.Commit
	err     error
	loaded  bool

	rows   []row
	cursor int
	offset int

	detailKey   string
	detail      string
	detailErr   error
	detailOff   int
	detailH     int // hauteur visible du détail (dernier rendu)
	detailTotal int // nombre de lignes du détail une fois replié

	typing  bool   // saisie du message de commit
	message string // message en cours de saisie
	busy    bool
}

func New(th *theme.Theme, p *project.Project) *Model {
	return &Model{th: th, proj: p}
}

func (m *Model) Title() string   { return i18n.N("Git") }
func (m *Model) Icon() string    { return "" }
func (m *Model) Capturing() bool { return m.typing }

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.load(), tea.Tick(refresh, func(time.Time) tea.Msg { return tickMsg{} }))
}

func (m *Model) Hints() []panel.Hint {
	if m.typing {
		return []panel.Hint{{Key: "Entrée", Desc: i18n.T("commiter")}, {Key: "Échap", Desc: i18n.T("annuler")}}
	}
	return []panel.Hint{{Key: "b", Desc: i18n.T("branche")}, {Key: "s", Desc: i18n.T("indexer")}, {Key: "u", Desc: i18n.T("désindexer")}, {Key: "A", Desc: i18n.T("tout indexer")},
		{Key: "c", Desc: "commit"}, {Key: "o", Desc: i18n.T("ouvrir")}, {Key: "PgUp/PgDn Maj+↑↓ molette", Desc: i18n.T("défiler")}}
}

func (m *Model) load() tea.Cmd {
	root := m.proj.Root
	return func() tea.Msg {
		ctx := context.Background()
		s, err := gitx.GetStatus(ctx, root)
		if err != nil {
			return stateMsg{err: err}
		}
		commits, err := gitx.Log(ctx, root, logSize)
		return stateMsg{status: s, commits: commits, err: err}
	}
}

// loadDetail charge le diff du fichier ou le contenu du commit sélectionné.
func (m *Model) loadDetail(force bool) tea.Cmd {
	r := m.selected()
	if r == nil {
		m.detailKey, m.detail = "", ""
		return nil
	}
	key := r.key()
	if key == m.detailKey && !force {
		return nil
	}
	if key != m.detailKey {
		m.detailOff = 0
	}
	m.detailKey = key
	root, row := m.proj.Root, *r
	return func() tea.Msg {
		ctx := context.Background()
		var text string
		var err error
		if row.commit != nil {
			text, err = gitx.Show(ctx, root, row.commit.Hash)
		} else {
			text, err = gitx.Diff(ctx, root, *row.change, row.staged)
		}
		return detailMsg{key: key, text: text, err: err}
	}
}

func (m *Model) act(done string, fn func(ctx context.Context, root string) error) tea.Cmd {
	m.busy = true
	root := m.proj.Root
	return func() tea.Msg {
		return actionMsg{done: done, err: fn(context.Background(), root)}
	}
}

// ─── Update ──────────────────────────────────────────────────────────────────

func (m *Model) Update(msg tea.Msg) (panel.Panel, tea.Cmd) {
	switch msg := msg.(type) {
	case panel.RepoChangedMsg:
		return m, m.load()
	case panel.BranchPickerMsg: // depuis la palette : c'est le cadre qui ouvre la liste
		return m, func() tea.Msg { return msg }
	case stateMsg:
		m.loaded, m.err = true, msg.err
		if msg.status != nil {
			m.status, m.commits = msg.status, msg.commits
		}
		m.rebuild()
		return m, m.loadDetail(true)

	case detailMsg:
		if msg.key == m.detailKey {
			m.detail, m.detailErr = msg.text, msg.err
		}

	case actionMsg:
		m.busy = false
		if msg.err != nil {
			return m, tea.Batch(m.load(), panel.Flash(msg.err.Error(), true))
		}
		return m, tea.Batch(m.load(), panel.Flash(msg.done, false))

	case tickMsg:
		next := tea.Tick(refresh, func(time.Time) tea.Msg { return tickMsg{} })
		if m.typing || m.busy || m.err == gitx.ErrNotRepo {
			return m, next
		}
		return m, tea.Batch(m.load(), next)

	case tea.KeyPressMsg:
		cmd := m.handleKey(msg)
		return m, tea.Batch(cmd, m.loadDetail(false))

	case tea.MouseWheelMsg:
		switch tea.Mouse(msg).Button {
		case tea.MouseWheelUp:
			m.scrollDetail(-3)
		case tea.MouseWheelDown:
			m.scrollDetail(3)
		}
	}
	return m, nil
}

func (m *Model) handleKey(k tea.KeyPressMsg) tea.Cmd {
	s := k.String()
	if m.typing {
		switch s {
		case "esc":
			m.typing, m.message = false, ""
		case "enter":
			msg := strings.TrimSpace(m.message)
			m.typing, m.message = false, ""
			if msg == "" {
				return panel.Flash(i18n.T("Commit annulé : message vide"), true)
			}
			return m.act(i18n.T("Commit créé"), func(ctx context.Context, root string) error {
				_, err := gitx.CommitStaged(ctx, root, msg)
				return err
			})
		case "backspace":
			if r := []rune(m.message); len(r) > 0 {
				m.message = string(r[:len(r)-1])
			}
		default:
			if k.Text != "" {
				m.message += k.Text
			}
		}
		return nil
	}

	r := m.selected()
	switch s {
	case "r":
		return m.load()
	case "b":
		return func() tea.Msg { return panel.BranchPickerMsg{} }
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "home", "g":
		m.move(-len(m.rows))
	case "end", "G":
		m.move(len(m.rows))
	case "pgup", "ctrl+u":
		m.scrollDetail(-max(1, m.detailH/2))
	case "pgdown", "ctrl+d":
		m.scrollDetail(max(1, m.detailH/2))
	case "shift+up":
		m.scrollDetail(-1)
	case "shift+down":
		m.scrollDetail(1)
	case "s":
		if r != nil && r.change != nil && !r.staged {
			path := r.change.Path
			return m.act(i18n.T("Indexé : ")+path, func(ctx context.Context, root string) error { return gitx.Stage(ctx, root, path) })
		}
	case "u":
		if r != nil && r.change != nil && r.staged {
			path, hasCommit := r.change.Path, m.status != nil && m.status.Commit != ""
			return m.act(i18n.T("Désindexé : ")+path, func(ctx context.Context, root string) error {
				return gitx.Unstage(ctx, root, hasCommit, path)
			})
		}
	case "A":
		return m.act(i18n.T("Toutes les modifications sont indexées"), gitx.StageAll)
	case "c":
		if m.stagedCount() == 0 {
			return panel.Flash(i18n.T("Rien à commiter : indexe d'abord des fichiers (s ou A)"), true)
		}
		m.typing, m.message = true, ""
	case "o", "enter":
		if r != nil && r.change != nil {
			return panel.Open(filepath.Join(m.proj.Root, r.change.Path), 0)
		}
	}
	return nil
}

// scrollDetail fait défiler le détail de d lignes, sans dépasser la fin.
func (m *Model) scrollDetail(d int) {
	m.detailOff = scroll.Clamp(m.detailOff+d, m.detailH, m.detailTotal)
}

func (m *Model) stagedCount() int {
	n := 0
	if m.status != nil {
		for _, c := range m.status.Changes {
			if c.IsStaged() {
				n++
			}
		}
	}
	return n
}

func (m *Model) selected() *row {
	if m.cursor >= 0 && m.cursor < len(m.rows) && m.rows[m.cursor].header == "" {
		return &m.rows[m.cursor]
	}
	return nil
}

func (m *Model) move(d int) {
	var idx []int
	for i, r := range m.rows {
		if r.header == "" {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		return
	}
	pos := 0
	for i, x := range idx {
		if x == m.cursor {
			pos = i
		}
	}
	m.cursor = idx[min(max(pos+d, 0), len(idx)-1)]
}

// rebuild regroupe les fichiers (indexés, modifiés, conflits, non suivis)
// puis les commits, en gardant la sélection.
func (m *Model) rebuild() {
	sel := ""
	if r := m.selected(); r != nil {
		sel = r.key()
	}
	m.rows = m.rows[:0]
	if m.status != nil {
		var staged, unstaged, conflicts, untracked []row
		for i := range m.status.Changes {
			c := &m.status.Changes[i]
			switch {
			case c.Conflict:
				conflicts = append(conflicts, row{change: c})
			case c.Untracked():
				untracked = append(untracked, row{change: c})
			default:
				if c.IsStaged() {
					staged = append(staged, row{change: c, staged: true})
				}
				if c.IsUnstaged() {
					unstaged = append(unstaged, row{change: c})
				}
			}
		}
		section := func(title string, rows []row) {
			if len(rows) > 0 {
				m.rows = append(m.rows, row{header: fmt.Sprintf("%s (%d)", title, len(rows))})
				m.rows = append(m.rows, rows...)
			}
		}
		section(i18n.T("Conflits"), conflicts)
		section(i18n.T("Indexés"), staged)
		section(i18n.T("Modifiés"), unstaged)
		section(i18n.T("Non suivis"), untracked)
	}
	if len(m.commits) > 0 {
		m.rows = append(m.rows, row{header: i18n.T("Commits récents")})
		for i := range m.commits {
			m.rows = append(m.rows, row{commit: &m.commits[i]})
		}
	}
	m.cursor = -1
	for i, r := range m.rows {
		if r.header != "" {
			continue
		}
		if m.cursor < 0 || r.key() == sel {
			m.cursor = i
		}
		if r.key() == sel {
			break
		}
	}
}

// ─── Vue ─────────────────────────────────────────────────────────────────────

func (m *Model) View(width, height int) string {
	t := m.th
	switch {
	case !m.loaded:
		return t.Dim.Render(i18n.T("Lecture du dépôt…"))
	case m.err == gitx.ErrNotRepo:
		return t.Dim.Render(i18n.T("Ce module n'est pas dans un dépôt git (git init pour en créer un)."))
	case m.status == nil:
		return t.Err.Render("git : " + m.err.Error())
	}

	top := m.viewSummary(width)
	bodyH := max(1, height-len(top)-1)
	listW := min(max(32, width*40/100), 60)
	detailW := max(10, width-listW-3)
	sep := t.Dim.Render(strings.TrimSuffix(strings.Repeat("│\n", bodyH), "\n"))
	return strings.Join(top, "\n") + "\n\n" +
		lipgloss.JoinHorizontal(lipgloss.Top, m.viewList(listW, bodyH), " ", sep, " ", m.viewDetail(detailW, bodyH))
}

func (m *Model) viewSummary(width int) []string {
	t := m.th
	s := m.status
	branch := s.Branch
	if branch == "" {
		branch = i18n.T("HEAD détaché (") + s.Commit + ")"
	}
	line := t.Dim.Render(i18n.T("branche ")) + t.Value.Render(branch)
	if s.Upstream != "" {
		line += t.Dim.Render(" → " + s.Upstream)
		switch {
		case s.Ahead == 0 && s.Behind == 0:
			line += t.OK.Render(i18n.T("  à jour"))
		default:
			if s.Ahead > 0 {
				line += t.Warn.Render(fmt.Sprintf(i18n.T("  ↑%d à pousser"), s.Ahead))
			}
			if s.Behind > 0 {
				line += t.Err.Render(fmt.Sprintf(i18n.T("  ↓%d à récupérer"), s.Behind))
			}
		}
	} else if s.Branch != "" {
		line += t.Dim.Render(i18n.T("  (pas de branche distante)"))
	}
	changes := len(s.Changes)
	if changes == 0 {
		line += t.Dim.Render("   │   ") + t.OK.Render(i18n.T("✓ arbre propre"))
	} else {
		line += t.Dim.Render("   │   ") + t.Text.Render(fmt.Sprintf(i18n.T("%d fichier(s) modifié(s), %d indexé(s)"), changes, m.stagedCount()))
	}
	lines := []string{ansi.Truncate(line, width, "…")}
	if m.typing {
		input := t.Label.Render(i18n.T("Message du commit : ")) + t.Value.Render(m.message) +
			lipgloss.NewStyle().Foreground(t.P.Accent).Render("▌")
		lines = append(lines, ansi.Truncate(input, width, "…"))
	}
	return lines
}

// mark renvoie le symbole et la couleur d'un état de fichier.
func (m *Model) mark(code byte) string {
	t := m.th
	switch code {
	case 'A':
		return t.OK.Render("A")
	case 'D':
		return t.Err.Render("D")
	case 'R', 'C':
		return lipgloss.NewStyle().Foreground(t.P.Blue).Render(string(code))
	case '?':
		return t.Dim.Render("?")
	case 'U':
		return t.Err.Render("U")
	}
	return t.Warn.Render(string(code))
}

func (m *Model) viewList(w, h int) string {
	t := m.th
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	if m.offset > 0 && m.cursor == m.offset && m.rows[m.offset-1].header != "" {
		m.offset--
	}
	m.offset = min(max(m.offset, 0), max(0, len(m.rows)-h))

	lines := make([]string, 0, h)
	for i := m.offset; i < len(m.rows) && len(lines) < h; i++ {
		r := m.rows[i]
		var line string
		switch {
		case r.header != "":
			line = t.Label.Bold(true).Render(r.header)
		case r.change != nil:
			code := r.change.Worktree
			if r.staged {
				code = r.change.Staged
			}
			if r.change.Conflict {
				code = 'U'
			}
			name := r.change.Path
			if r.change.OrigPath != "" {
				name = r.change.OrigPath + " → " + name
			}
			line = "  " + m.mark(code) + " " + t.Text.Render(name)
		case r.commit != nil:
			c := r.commit
			line = "  " + lipgloss.NewStyle().Foreground(t.P.Accent).Render(c.Hash) + " " +
				t.Text.Render(c.Subject) + t.Dim.Render("  "+ago(c.When))
		}
		line = ansi.Truncate(line, w, "…")
		if i == m.cursor {
			line = lipgloss.NewStyle().Background(t.P.Surface0).Width(w).Render(line)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = append(lines, t.Dim.Render(i18n.T("Aucun commit pour l'instant.")))
	}
	return lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(strings.Join(lines, "\n"))
}

// ago présente une date de façon relative (« il y a 5 min »).
func ago(tm time.Time) string {
	d := time.Since(tm)
	switch {
	case d < time.Minute:
		return i18n.T("à l'instant")
	case d < time.Hour:
		return fmt.Sprintf(i18n.T("il y a %d min"), int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf(i18n.T("il y a %d h"), int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf(i18n.T("il y a %d j"), int(d.Hours()/24))
	}
	return tm.Format("02/01/2006")
}

func (m *Model) viewDetail(w, h int) string {
	t := m.th
	box := lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h)
	r := m.selected()
	switch {
	case r == nil:
		return box.Render(t.Dim.Render(i18n.T("Aucune sélection")))
	case m.detailErr != nil:
		return box.Render(t.Err.Render(m.detailErr.Error()))
	case m.detail == "":
		return box.Render(t.Dim.Render(i18n.T("(pas de différence)")))
	}

	// Lignes repliées à la largeur disponible (moins la colonne de l'ascenseur),
	// en gardant la couleur de chaque ligne d'origine.
	textW := max(10, w-2)
	var lines []string
	for l := range strings.SplitSeq(strings.TrimRight(m.detail, "\n"), "\n") {
		st := m.lineStyle(l)
		plain := strings.ReplaceAll(l, "\t", "    ")
		for seg := range strings.SplitSeq(ansi.Hardwrap(plain, textW, true), "\n") {
			lines = append(lines, st.Render(seg))
		}
	}

	viewH := max(1, h-1) // une ligne d'en-tête
	m.detailH, m.detailTotal = viewH, len(lines)
	m.detailOff = scroll.Clamp(m.detailOff, viewH, len(lines))
	end := min(len(lines), m.detailOff+viewH)

	title := ""
	switch {
	case r.commit != nil:
		title = "commit " + r.commit.Hash
	case r.staged:
		title = r.change.Path + i18n.T(" (indexé)")
	default:
		title = r.change.Path
	}
	pos := ""
	if len(lines) > viewH {
		pos = fmt.Sprintf(i18n.T("lignes %d–%d / %d"), m.detailOff+1, end, len(lines))
	}
	head := ansi.Truncate(t.Value.Render(title), max(1, w-lipgloss.Width(pos)-2), "…")
	head += strings.Repeat(" ", max(1, w-lipgloss.Width(head)-lipgloss.Width(pos))) + t.Dim.Render(pos)

	body := scroll.Render(lines[m.detailOff:end], textW, viewH, len(lines), m.detailOff,
		lipgloss.NewStyle().Foreground(t.P.Surface1), lipgloss.NewStyle().Foreground(t.P.Accent))
	return box.Render(head + "\n" + body)
}

// lineStyle choisit la couleur d'une ligne de diff ou de commit.
func (m *Model) lineStyle(l string) lipgloss.Style {
	t := m.th
	switch {
	case strings.HasPrefix(l, "+++"), strings.HasPrefix(l, "---"),
		strings.HasPrefix(l, "diff "), strings.HasPrefix(l, "index "),
		strings.HasPrefix(l, "new file"), strings.HasPrefix(l, "deleted file"):
		return t.Dim
	case strings.HasPrefix(l, "@@"):
		return lipgloss.NewStyle().Foreground(t.P.Blue)
	case strings.HasPrefix(l, "+"):
		return t.OK
	case strings.HasPrefix(l, "-"):
		return t.Err
	}
	return t.Text
}
