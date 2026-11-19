// Package shell est le module Terminal : un vrai shell ($SHELL) dans un
// pseudo-terminal, affiché par un émulateur VT (charmbracelet/x/vt). Les
// programmes interactifs (vim, htop, git rebase -i…) y fonctionnent.
//
// Deux modes : en mode shell, toutes les touches vont au shell, sauf Ctrl+]
// qui rend la main à gopit ; en mode navigation, les touches du cockpit
// reviennent, on remonte dans l'historique de l'écran et « o » ouvre le
// fichier:ligne de la ligne choisie dans l'éditeur.
package shell

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/theme"
)

// leaveKey rend la main à gopit depuis le mode shell.
const leaveKey = "ctrl+]"

// redrawMsg signale que l'écran du terminal a changé.
type redrawMsg struct{}

// exitMsg signale la fin du shell.
type exitMsg struct {
	gen int
	err error
}

// runMsg tape une commande dans le shell (palette).
type runMsg struct{ line string }

type Model struct {
	th    *theme.Theme
	proj  *project.Project
	spawn bool // démarrer un shell (GOPIT_TERMINAL=off : non)

	// Session : emu protège ses propres méthodes, mais pas l'historique
	// qu'il renvoie (Scrollback) : emuMu entoure l'écriture de la sortie du
	// shell, le redimensionnement et la lecture de l'historique. Le reste
	// n'est touché que depuis la boucle de Bubble Tea.
	emu      *vt.SafeEmulator
	emuMu    *sync.Mutex
	ptmx     *os.File
	gen      int  // numéro de la session (un shell relancé change de numéro)
	exited   bool // le shell s'est arrêté
	exitCode int
	failed   string // impossible de démarrer (Windows…)

	redraw chan tea.Msg // redessins (un seul en attente) ; voir Init
	exits  chan tea.Msg // fins de shell
	w, h   int          // taille du terminal (zone du module moins la marge et la ligne d'état)

	raw    bool // mode shell : les touches vont au shell
	back   int  // lignes remontées dans l'historique (0 : écran courant)
	sel    int  // ligne choisie en navigation (index dans lines())
	selSet bool
}

func New(th *theme.Theme, p *project.Project) *Model {
	return &Model{th: th, proj: p, raw: true, redraw: make(chan tea.Msg, 1), exits: make(chan tea.Msg, 4),
		spawn: os.Getenv("GOPIT_TERMINAL") != "off"}
}

func (m *Model) Title() string { return i18n.N("Terminal") }
func (m *Model) Icon() string  { return "" } // terminal (Font Awesome, comme les autres modules)

// Init attend, pendant toute la vie de gopit, les messages du shell.
func (m *Model) Init() tea.Cmd { return m.wait() }

func (m *Model) wait() tea.Cmd {
	redraw, exits := m.redraw, m.exits
	return func() tea.Msg {
		select {
		case msg := <-redraw:
			return msg
		case msg := <-exits:
			return msg
		}
	}
}

// RawInput : en mode shell, le cadre transmet toutes les touches (sauf
// quand une fenêtre de gopit est ouverte).
func (m *Model) RawInput() bool { return m.raw && m.emu != nil && m.failed == "" && !m.exited }

// Capturing : en mode shell, rien n'échappe au terminal.
func (m *Model) Capturing() bool { return m.RawInput() }

func (m *Model) Hints() []panel.Hint {
	if m.RawInput() {
		return []panel.Hint{{Key: "Ctrl+]", Desc: i18n.T("revenir à gopit (les autres touches vont au shell)")}}
	}
	if m.exited {
		return []panel.Hint{{Key: "Entrée", Desc: i18n.T("relancer le shell")}}
	}
	return []panel.Hint{{Key: "i / Entrée", Desc: i18n.T("shell")}, {Key: "↑↓", Desc: i18n.T("ligne")},
		{Key: "PgUp/PgDn", Desc: i18n.T("défiler")}, {Key: "o", Desc: i18n.T("ouvrir fichier:ligne")}}
}

// Commands propose quelques commandes Go courantes, tapées dans le shell.
func (m *Model) Commands() []panel.Command {
	var c []panel.Command
	for _, line := range []string{"go build ./...", "go run .", "go generate ./...", "go mod tidy", "go vet ./..."} {
		c = append(c, panel.Command{Title: i18n.T("Exécuter : ") + line, Msg: runMsg{line}})
	}
	return c
}

// ─── Shell ───────────────────────────────────────────────────────────────────

// start lance le shell dans un pseudo-terminal de la taille du module.
func (m *Model) start() {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "sh"
	}
	cmd := exec.Command(sh)
	cmd.Dir = m.proj.Root
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "COLORTERM=truecolor")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: uint16(m.h), Cols: uint16(m.w)})
	if err != nil {
		m.failed = fmt.Sprintf(i18n.T("Impossible de démarrer le shell : %v"), err)
		return
	}
	emu, mu := vt.NewSafeEmulator(m.w, m.h), &sync.Mutex{}
	m.gen++
	m.emu, m.emuMu, m.ptmx, m.exited, m.back, m.selSet = emu, mu, ptmx, false, 0, false
	gen, redraw, exits := m.gen, m.redraw, m.exits

	// Sortie du shell → écran ; réponses de l'émulateur et touches → shell.
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				mu.Lock()
				_, _ = emu.Write(buf[:n])
				mu.Unlock()
				select { // un seul redessin en attente suffit
				case redraw <- redrawMsg{}:
				default:
				}
			}
			if err != nil {
				return
			}
		}
	}()
	go func() { _, _ = io.Copy(ptmx, emu) }()
	go func() {
		err := cmd.Wait()
		_ = ptmx.Close()
		exits <- exitMsg{gen: gen, err: err}
	}()
}

// resize suit la taille du module.
func (m *Model) resize(w, h int) {
	if w == m.w && h == m.h {
		return
	}
	m.w, m.h = w, h
	if m.emu != nil && !m.exited {
		m.emuMu.Lock()
		m.emu.Resize(w, h)
		m.emuMu.Unlock()
		_ = pty.Setsize(m.ptmx, &pty.Winsize{Rows: uint16(h), Cols: uint16(w)})
	}
}

// ─── Update ──────────────────────────────────────────────────────────────────

func (m *Model) Update(msg tea.Msg) (panel.Panel, tea.Cmd) {
	switch msg := msg.(type) {
	case redrawMsg:
		return m, m.wait() // le prochain rendu montrera l'écran à jour

	case exitMsg:
		if msg.gen == m.gen {
			m.exited, m.raw, m.exitCode = true, false, 0
			if ee, ok := msg.err.(*exec.ExitError); ok {
				m.exitCode = ee.ExitCode()
			}
			// Fermer l'entrée de l'émulateur arrête la copie vers le shell
			// (Close de l'émulateur n'est pas sûr pendant cette lecture).
			if c, ok := m.emu.InputPipe().(io.Closer); ok {
				_ = c.Close()
			}
		}
		return m, m.wait()

	case runMsg:
		if m.emu != nil && !m.exited {
			m.raw, m.back = true, 0
			m.emu.SendText(msg.line + "\r")
		}

	case tea.PasteMsg:
		if m.RawInput() {
			m.emu.Paste(msg.Content)
		}

	case panel.WheelMsg:
		m.scroll(-msg.Delta)

	case panel.ClickMsg: // un clic dans le terminal y rend la main au shell
		if !msg.Drag && m.emu != nil && !m.exited {
			m.raw, m.back = true, 0
		}

	case tea.KeyPressMsg:
		return m, m.key(msg)
	}
	return m, nil
}

func (m *Model) key(k tea.KeyPressMsg) tea.Cmd {
	s := k.String()
	if m.RawInput() {
		if s == leaveKey {
			m.raw = false
			m.sel, m.selSet = len(m.lines())-1-m.back, true
			return nil
		}
		m.back = 0 // taper ramène à l'écran courant
		m.emu.SendKey(vt.KeyPressEvent(k))
		return nil
	}
	if m.exited || m.failed != "" || m.emu == nil {
		if s == "enter" && m.exited {
			m.start()
			m.raw = true
		}
		return nil
	}
	lines := m.lines()
	page := max(1, m.h-1)
	switch s {
	case "i", "enter", "a":
		m.raw, m.back = true, 0
	case "up", "k":
		m.moveSel(-1, len(lines))
	case "down", "j":
		m.moveSel(1, len(lines))
	case "pgup":
		m.moveSel(-page, len(lines))
	case "pgdown":
		m.moveSel(page, len(lines))
	case "home", "g":
		m.moveSel(-len(lines), len(lines))
	case "end", "G":
		m.moveSel(len(lines), len(lines))
	case "o":
		return m.openLocation(lines)
	}
	return nil
}

// moveSel déplace la ligne choisie et fait défiler pour la garder visible.
func (m *Model) moveSel(d, total int) {
	if total == 0 {
		return
	}
	m.sel = min(max(0, m.sel+d), total-1)
	m.selSet = true
	// Fenêtre affichée : lignes [total-h-back, total-back).
	if top := total - m.h - m.back; m.sel < top {
		m.back = total - m.h - m.sel
	} else if m.sel >= total-m.back {
		m.back = total - 1 - m.sel
	}
	m.back = min(max(0, m.back), max(0, total-m.h))
}

// scroll remonte (n > 0) ou redescend dans l'historique.
func (m *Model) scroll(n int) {
	total := len(m.lines())
	m.back = min(max(0, m.back+n), max(0, total-m.h))
}

// lines renvoie l'historique (lignes sorties de l'écran) puis l'écran,
// avec leurs styles.
func (m *Model) lines() []string {
	if m.emu == nil {
		return nil
	}
	m.emuMu.Lock()
	defer m.emuMu.Unlock()
	var out []string
	if sb := m.emu.Scrollback(); sb != nil {
		for _, l := range sb.Lines() {
			out = append(out, l.Render())
		}
	}
	return append(out, strings.Split(m.emu.Render(), "\n")...)
}

// locRe repère « fichier.go:12 » (ou fichier.go:12:5).
var locRe = regexp.MustCompile(`([\w./\\-]+\.go):(\d+)`)

// openLocation ouvre dans l'éditeur l'emplacement de la ligne choisie.
func (m *Model) openLocation(lines []string) tea.Cmd {
	if !m.selSet || m.sel >= len(lines) {
		return nil
	}
	mt := locRe.FindStringSubmatch(ansi.Strip(lines[m.sel]))
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

// View dessine le terminal ; au premier affichage, le shell démarre à la
// taille du module.
func (m *Model) View(width, height int) string {
	t := m.th
	if width < 10 || height < 3 {
		return ""
	}
	m.resize(width-2, height-1) // marge de sélection à gauche, ligne d'état en bas
	if m.emu == nil && m.failed == "" && m.spawn {
		m.start()
	}
	var rows []string
	switch {
	case m.failed != "":
		rows = []string{t.Err.Render(m.failed)}
	case m.emu == nil:
		rows = []string{t.Dim.Render(i18n.T("Terminal désactivé (GOPIT_TERMINAL=off)."))}
	default:
		rows = m.viewScreen()
	}
	body := lipgloss.NewStyle().Height(height - 1).MaxHeight(height - 1).Render(strings.Join(rows, "\n"))
	return body + "\n" + m.viewStatus(width)
}

// viewScreen dessine les lignes visibles, avec le curseur en mode shell et
// la ligne choisie en navigation.
func (m *Model) viewScreen() []string {
	t := m.th
	lines := m.lines()
	total := len(lines)
	from := max(0, total-m.h-m.back)
	cur := m.emu.CursorPosition()
	curRow := total - m.h + cur.Y // ligne du curseur dans lines
	rows := make([]string, 0, m.h)
	for i := from; i < total && i < from+m.h; i++ {
		l := lines[i]
		if m.RawInput() && m.back == 0 && i == curRow {
			l = drawCursor(l, cur.X)
		}
		mark := "  "
		if !m.RawInput() && m.selSet && i == m.sel {
			mark = lipgloss.NewStyle().Foreground(t.P.Accent).Bold(true).Render("▶ ")
		}
		rows = append(rows, mark+l)
	}
	return rows
}

// drawCursor inverse la cellule x d'une ligne rendue.
func drawCursor(line string, x int) string {
	cell := ansi.Strip(ansi.Cut(line, x, x+1))
	if cell == "" {
		cell = " "
	}
	return ansi.Cut(line, 0, x) + "\x1b[7m" + cell + "\x1b[27m" + ansi.Cut(line, x+1, ansi.StringWidth(line))
}

func (m *Model) viewStatus(w int) string {
	t := m.th
	var s string
	switch {
	case m.failed != "" || m.emu == nil:
		return ""
	case m.exited:
		s = t.Warn.Render(fmt.Sprintf(i18n.T("Shell terminé (code %d) — Entrée pour le relancer"), m.exitCode))
	case m.RawInput():
		s = lipgloss.NewStyle().Foreground(t.P.Green).Render(i18n.T("● shell")) +
			t.Dim.Render(i18n.T(" — les touches vont au shell ; Ctrl+] revient à gopit"))
	default:
		s = lipgloss.NewStyle().Foreground(t.P.Accent).Render(i18n.T("◆ navigation")) +
			t.Dim.Render(i18n.T(" — i ou Entrée : shell · ↑↓ ligne · o : ouvrir fichier:ligne"))
		if m.back > 0 {
			s += t.Dim.Render(fmt.Sprintf(i18n.T(" · %d lignes plus haut"), m.back))
		}
	}
	return ansi.Truncate(s, w, "…")
}
