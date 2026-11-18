// Package shell est le module Terminal : une ligne de commande exécutée par
// le shell dans le répertoire du projet, dont la sortie défile dans le
// module. Pas de programme interactif (l'entrée standard est vide) ; les
// emplacements « fichier.go:12 » s'ouvrent dans l'éditeur.
package shell

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/scroll"
	"github.com/laurentpoirierfr/gopit/internal/theme"
)

const (
	maxLines   = 5000 // lignes de sortie gardées
	maxHistory = 100  // commandes gardées dans l'historique
)

type lineKind int

const (
	kindOut  lineKind = iota // sortie de la commande
	kindCmd                  // « $ commande »
	kindOK                   // fin réussie
	kindFail                 // fin en échec ou arrêt
)

type line struct {
	text string
	kind lineKind
}

type outMsg struct {
	id     int
	lines  []string
	closed bool // plus de sortie : attendre la fin du processus
}

type doneMsg struct {
	id  int
	err error
	dur time.Duration
}

// runMsg lance une commande (palette).
type runMsg struct{ line string }

// actionMsg est une action de la palette.
type actionMsg string

const (
	actRerun actionMsg = "rerun"
	actStop  actionMsg = "stop"
	actClear actionMsg = "clear"
)

type Model struct {
	th   *theme.Theme
	proj *project.Project

	typing bool   // saisie de la commande (sinon : parcours de la sortie)
	input  []rune // commande en cours de saisie
	pos    int    // curseur dans input

	history []string // plus ancienne d'abord
	histPos int      // dans history pendant la navigation (len : saisie en cours)
	draft   string   // saisie mise de côté pendant la navigation

	lines  []line
	cursor int  // ligne sélectionnée (parcours)
	offset int  // première ligne affichée
	follow bool // suivre la fin de la sortie
	outH   int  // hauteur de la zone de sortie au dernier rendu

	// Commande en cours.
	id      int
	cmd     *exec.Cmd
	out     chan string
	done    chan error
	started time.Time
	stopped bool
	last    string // dernière commande lancée
}

func New(th *theme.Theme, p *project.Project) *Model {
	return &Model{th: th, proj: p, typing: true, follow: true, outH: 20}
}

func (m *Model) Title() string { return i18n.N("Terminal") }
func (m *Model) Icon() string  { return "\uf120" } // terminal (Font Awesome, comme les autres modules)
func (m *Model) Init() tea.Cmd { return nil }

// Capturing : en saisie, toutes les touches vont à la ligne de commande.
func (m *Model) Capturing() bool { return m.typing }

func (m *Model) running() bool { return m.cmd != nil }

// TakesCtrlC : Ctrl+C arrête la commande en cours, ou efface la saisie.
func (m *Model) TakesCtrlC() bool { return m.running() || (m.typing && len(m.input) > 0) }

func (m *Model) Hints() []panel.Hint {
	var h []panel.Hint
	if m.typing {
		h = []panel.Hint{{Key: "Entrée", Desc: i18n.T("exécuter")}, {Key: "↑↓", Desc: i18n.T("historique")},
			{Key: "Échap", Desc: i18n.T("parcourir la sortie")}, {Key: "Ctrl+L", Desc: i18n.T("effacer")}}
	} else {
		h = []panel.Hint{{Key: "i / Entrée", Desc: i18n.T("saisir")}, {Key: "↑↓", Desc: i18n.T("ligne")},
			{Key: "o", Desc: i18n.T("ouvrir fichier:ligne")}, {Key: "r", Desc: i18n.T("relancer")}, {Key: "c", Desc: i18n.T("effacer")}}
	}
	if m.running() {
		h = append(h, panel.Hint{Key: "Ctrl+C", Desc: i18n.T("arrêter")})
	}
	return h
}

// Commands expose les actions à la palette, ainsi que quelques commandes
// Go courantes.
func (m *Model) Commands() []panel.Command {
	c := []panel.Command{
		{Title: i18n.T("Relancer la dernière commande"), Key: "r", Msg: actRerun},
		{Title: i18n.T("Arrêter la commande"), Key: "Ctrl+C", Msg: actStop},
		{Title: i18n.T("Effacer la sortie"), Key: "Ctrl+L", Msg: actClear},
	}
	for _, line := range []string{"go build ./...", "go run .", "go generate ./...", "go mod tidy", "go vet ./..."} {
		c = append(c, panel.Command{Title: i18n.T("Exécuter : ") + line, Msg: runMsg{line}})
	}
	return c
}

// State garde l'historique d'une session à l'autre.
func (m *Model) State() map[string]string {
	return map[string]string{"history": strings.Join(m.history, "\n")}
}

func (m *Model) Restore(s map[string]string) {
	if h := s["history"]; h != "" {
		m.history = strings.Split(h, "\n")
	}
	m.histPos = len(m.history)
}

// ─── Exécution ───────────────────────────────────────────────────────────────

// run lance une commande ; sa sortie arrive par outMsg, sa fin par doneMsg.
func (m *Model) run(cmdline string) tea.Cmd {
	cmdline = strings.TrimSpace(cmdline)
	if cmdline == "" {
		return nil
	}
	if m.running() {
		return panel.Flash(i18n.T("Une commande est déjà en cours (Ctrl+C pour l'arrêter)"), true)
	}
	m.remember(cmdline)
	m.last = cmdline
	m.add(line{"$ " + cmdline, kindCmd})
	m.follow = true

	cmd := shellCommand(cmdline)
	cmd.Dir = m.proj.Root
	cmd.Env = append(cmd.Environ(), "TERM=dumb") // pas de séquences de contrôle
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	cmd.WaitDelay = time.Second // un descendant qui garde la sortie ouverte ne bloque pas
	if err := cmd.Start(); err != nil {
		m.add(line{err.Error(), kindFail})
		return nil
	}
	m.id++
	m.cmd, m.started, m.stopped = cmd, time.Now(), false
	m.out, m.done = make(chan string, 1024), make(chan error, 1)
	out, done := m.out, m.done
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			out <- sc.Text()
		}
		if err := sc.Err(); err != nil { // ligne démesurée : la signaler, puis vider la sortie
			out <- err.Error()
			_, _ = io.Copy(io.Discard, pr)
		}
		close(out)
	}()
	go func() {
		err := cmd.Wait()
		pw.Close()
		done <- err
	}()
	return m.next()
}

// next attend la sortie suivante (par paquets, pour ne pas redessiner à
// chaque ligne).
func (m *Model) next() tea.Cmd {
	id, out := m.id, m.out
	return func() tea.Msg {
		l, ok := <-out
		if !ok {
			return outMsg{id: id, closed: true}
		}
		lines := []string{l}
		for len(lines) < 500 {
			select {
			case l, ok := <-out:
				if !ok {
					return outMsg{id: id, lines: lines, closed: true}
				}
				lines = append(lines, l)
			default:
				return outMsg{id: id, lines: lines}
			}
		}
		return outMsg{id: id, lines: lines}
	}
}

func (m *Model) wait() tea.Cmd {
	id, done, started := m.id, m.done, m.started
	return func() tea.Msg {
		err := <-done
		return doneMsg{id: id, err: err, dur: time.Since(started)}
	}
}

func (m *Model) stop() {
	if m.running() {
		m.stopped = true
		stop(m.cmd)
	}
}

// add ajoute une ligne à la sortie (en oubliant les plus anciennes).
func (m *Model) add(l line) {
	m.lines = append(m.lines, l)
	if extra := len(m.lines) - maxLines; extra > 0 {
		m.lines = m.lines[extra:]
		m.cursor = max(0, m.cursor-extra)
		m.offset = max(0, m.offset-extra)
	}
	if m.follow {
		m.cursor = len(m.lines) - 1
	}
}

// clean prépare une ligne de sortie pour l'affichage : séquences de
// contrôle retirées, retour chariot (barres de progression) : dernière
// version de la ligne, tabulations en espaces.
func clean(s string) string {
	if i := strings.LastIndex(strings.TrimRight(s, "\r"), "\r"); i >= 0 {
		s = s[i+1:]
	}
	s = ansi.Strip(strings.TrimRight(s, "\r"))
	return strings.ReplaceAll(s, "\t", "    ")
}

func (m *Model) remember(cmdline string) {
	if n := len(m.history); n == 0 || m.history[n-1] != cmdline {
		m.history = append(m.history, cmdline)
	}
	if extra := len(m.history) - maxHistory; extra > 0 {
		m.history = m.history[extra:]
	}
	m.histPos = len(m.history)
}

// ─── Update ──────────────────────────────────────────────────────────────────

func (m *Model) Update(msg tea.Msg) (panel.Panel, tea.Cmd) {
	switch msg := msg.(type) {
	case outMsg:
		if msg.id != m.id {
			return m, nil
		}
		for _, l := range msg.lines {
			m.add(line{clean(l), kindOut})
		}
		if msg.closed {
			return m, m.wait()
		}
		return m, m.next()

	case doneMsg:
		if msg.id != m.id {
			return m, nil
		}
		m.cmd = nil
		dur := msg.dur.Round(10 * time.Millisecond)
		var text string
		ok := false
		switch {
		case m.stopped:
			text = fmt.Sprintf(i18n.T("■ arrêtée après %s"), dur)
		case msg.err == nil:
			text, ok = fmt.Sprintf(i18n.T("✓ terminée en %s"), dur), true
		default:
			code := -1
			if ee, isExit := msg.err.(*exec.ExitError); isExit {
				code = ee.ExitCode()
			}
			text = fmt.Sprintf(i18n.T("✗ code de sortie %d après %s"), code, dur)
		}
		kind := kindFail
		if ok {
			kind = kindOK
		}
		m.add(line{text, kind})
		return m, panel.Flash(i18n.T("Terminal : ")+m.last+" — "+text, !ok && !m.stopped)

	case runMsg:
		m.typing = true
		return m, m.run(msg.line)

	case actionMsg:
		return m, m.act(msg)

	case tea.PasteMsg:
		if m.typing {
			m.insert(strings.ReplaceAll(strings.TrimRight(msg.Content, "\n"), "\n", " "))
		}

	case tea.MouseWheelMsg:
		if tea.Mouse(msg).Button == tea.MouseWheelUp {
			m.scroll(-3)
		} else {
			m.scroll(3)
		}

	case tea.KeyPressMsg:
		if m.typing {
			return m, m.typingKey(msg)
		}
		return m, m.browseKey(msg.String())
	}
	return m, nil
}

func (m *Model) act(a actionMsg) tea.Cmd {
	switch a {
	case actRerun:
		if m.last == "" && len(m.history) > 0 {
			m.last = m.history[len(m.history)-1]
		}
		return m.run(m.last)
	case actStop:
		m.stop()
	case actClear:
		m.lines, m.cursor, m.offset, m.follow = nil, 0, 0, true
	}
	return nil
}

func (m *Model) insert(s string) {
	r := []rune(s)
	m.input = append(m.input[:m.pos], append(r, m.input[m.pos:]...)...)
	m.pos += len(r)
}

func (m *Model) typingKey(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "enter":
		line := string(m.input)
		m.input, m.pos, m.draft = nil, 0, ""
		return m.run(line)
	case "esc":
		m.typing = false
	case "ctrl+c":
		if m.running() {
			m.stop()
		} else {
			m.input, m.pos = nil, 0
		}
	case "ctrl+l":
		return m.act(actClear)
	case "ctrl+u":
		m.input, m.pos = m.input[m.pos:], 0
	case "up":
		m.recall(-1)
	case "down":
		m.recall(1)
	case "left":
		m.pos = max(0, m.pos-1)
	case "right":
		m.pos = min(len(m.input), m.pos+1)
	case "home", "ctrl+a":
		m.pos = 0
	case "end", "ctrl+e":
		m.pos = len(m.input)
	case "backspace":
		if m.pos > 0 {
			m.input = append(m.input[:m.pos-1], m.input[m.pos:]...)
			m.pos--
		}
	case "delete":
		if m.pos < len(m.input) {
			m.input = append(m.input[:m.pos], m.input[m.pos+1:]...)
		}
	case "pgup":
		m.scroll(-max(1, m.outH-1))
	case "pgdown":
		m.scroll(max(1, m.outH-1))
	default:
		if k.Text != "" && !k.Mod.Contains(tea.ModCtrl) && !k.Mod.Contains(tea.ModAlt) {
			m.insert(k.Text)
		}
	}
	return nil
}

// recall parcourt l'historique (d = -1 : plus ancienne).
func (m *Model) recall(d int) {
	if len(m.history) == 0 {
		return
	}
	if m.histPos == len(m.history) {
		m.draft = string(m.input)
	}
	m.histPos = min(len(m.history), max(0, m.histPos+d))
	s := m.draft
	if m.histPos < len(m.history) {
		s = m.history[m.histPos]
	}
	m.input = []rune(s)
	m.pos = len(m.input)
}

func (m *Model) browseKey(s string) tea.Cmd {
	switch s {
	case "i", "enter":
		m.typing = true
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
	case "pgup":
		m.moveCursor(-max(1, m.outH-1))
	case "pgdown":
		m.moveCursor(max(1, m.outH-1))
	case "home", "g":
		m.moveCursor(-len(m.lines))
	case "end", "G":
		m.moveCursor(len(m.lines))
	case "o":
		return m.openLocation()
	case "r":
		return m.act(actRerun)
	case "x", "ctrl+c":
		m.stop()
	case "c", "ctrl+l":
		return m.act(actClear)
	}
	return nil
}

func (m *Model) moveCursor(d int) {
	if len(m.lines) == 0 {
		return
	}
	m.cursor = min(len(m.lines)-1, max(0, m.cursor+d))
	m.follow = m.cursor == len(m.lines)-1
	if m.cursor < m.offset {
		m.offset = m.cursor
	} else if m.cursor >= m.offset+m.outH {
		m.offset = m.cursor - m.outH + 1
	}
}

func (m *Model) scroll(d int) {
	m.offset = scroll.Clamp(m.offset+d, m.outH, len(m.lines))
	m.follow = m.offset+m.outH >= len(m.lines)
	if !m.typing {
		m.cursor = min(max(m.cursor, m.offset), m.offset+m.outH-1, len(m.lines)-1)
	}
}

// locRe repère « fichier.go:12 » (ou fichier.go:12:5) dans la sortie.
var locRe = regexp.MustCompile(`([\w./\\-]+\.go):(\d+)`)

// openLocation ouvre dans l'éditeur l'emplacement de la ligne sélectionnée.
func (m *Model) openLocation() tea.Cmd {
	if m.cursor >= len(m.lines) {
		return nil
	}
	mt := locRe.FindStringSubmatch(m.lines[m.cursor].text)
	if mt == nil {
		return panel.Flash(i18n.T("Aucun emplacement fichier:ligne sur cette ligne"), true)
	}
	file := mt[1]
	if !filepath.IsAbs(file) {
		file = filepath.Join(m.proj.Root, file)
	}
	n, _ := strconv.Atoi(mt[2])
	return panel.Open(file, n)
}

// ─── Vue ─────────────────────────────────────────────────────────────────────

func (m *Model) View(width, height int) string {
	t := m.th
	if height < 3 {
		return ""
	}
	m.outH = height - 2
	if m.follow {
		m.offset = max(0, len(m.lines)-m.outH)
	}
	m.offset = scroll.Clamp(m.offset, m.outH, len(m.lines))
	textW := max(1, width-2)

	var rows []string
	if len(m.lines) == 0 {
		rows = append(rows, t.Dim.Render(i18n.T("Tape une commande (go build, make, go run .…) puis Entrée ; elle s'exécute dans le répertoire du projet.")))
	}
	for i := m.offset; i < len(m.lines) && i < m.offset+m.outH; i++ {
		l := m.lines[i]
		text := ansi.Truncate(l.text, textW, "…")
		var st lipgloss.Style
		switch l.kind {
		case kindCmd:
			st = lipgloss.NewStyle().Foreground(t.P.Accent).Bold(true)
		case kindOK:
			st = lipgloss.NewStyle().Foreground(t.P.Green)
		case kindFail:
			st = lipgloss.NewStyle().Foreground(t.P.Red)
		default:
			st = lipgloss.NewStyle().Foreground(t.P.Text)
			if locRe.MatchString(l.text) {
				st = st.Foreground(t.P.Blue)
			}
		}
		if !m.typing && i == m.cursor {
			st = st.Background(t.P.Surface1)
			text += strings.Repeat(" ", max(0, textW-ansi.StringWidth(text)))
		}
		rows = append(rows, st.Render(text))
	}
	out := scroll.Render(rows, textW, m.outH, len(m.lines), m.offset,
		lipgloss.NewStyle().Foreground(t.P.Surface1), lipgloss.NewStyle().Foreground(t.P.Accent))
	sep := lipgloss.NewStyle().Foreground(t.P.Surface1).Render(strings.Repeat("─", width))
	return lipgloss.NewStyle().Height(m.outH).MaxHeight(m.outH).Render(out) + "\n" + sep + "\n" + m.viewInput(width)
}

func (m *Model) viewInput(w int) string {
	t := m.th
	prompt := lipgloss.NewStyle().Foreground(t.P.Accent).Bold(true).Render("❯ ")
	if !m.typing {
		prompt = t.Dim.Render("❯ ")
	}
	right := ""
	if m.running() {
		right = t.Warn.Render(fmt.Sprintf(i18n.T(" en cours… %.0fs · Ctrl+C pour arrêter"), time.Since(m.started).Seconds()))
	}
	room := max(1, w-lipgloss.Width(prompt)-lipgloss.Width(right)-1)
	in := string(m.input)
	if m.typing {
		before, after := string(m.input[:m.pos]), string(m.input[m.pos:])
		cur := " "
		if after != "" {
			r := []rune(after)
			cur, after = string(r[0]), string(r[1:])
		}
		in = t.Text.Render(before) + t.Text.Reverse(true).Render(cur) + t.Text.Render(after)
	} else {
		in = t.Dim.Render(in)
	}
	in = ansi.Truncate(in, room, "…")
	gap := max(1, w-lipgloss.Width(prompt)-lipgloss.Width(in)-lipgloss.Width(right))
	return prompt + in + strings.Repeat(" ", gap) + right
}
