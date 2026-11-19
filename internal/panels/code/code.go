// Package code est le module « Éditeur » : un éditeur de texte intégré,
// pensé pour Go (coloration, indentation, gofmt à l'enregistrement), avec
// plusieurs fichiers ouverts à la fois.
//
// Deux modes : LECTURE (les touches du cockpit restent actives, les flèches
// déplacent le curseur) et ÉDITION (toutes les touches vont au texte ;
// Échap revient en lecture).
package code

import (
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/laurentpoirierfr/imgview/pkg/imgrender"

	"github.com/laurentpoirierfr/gopit/internal/gopls"
	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/lsp"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/termimg"
	"github.com/laurentpoirierfr/gopit/internal/textbuf"
	"github.com/laurentpoirierfr/gopit/internal/theme"
)

const (
	tabWidth  = 4
	wheelStep = 3
)

// doc est un fichier ouvert.
type doc struct {
	path  string // chemin absolu
	rel   string // chemin affiché (relatif au projet si possible)
	buf   *textbuf.Buffer
	perm  fs.FileMode
	mtime int64 // date de modification au chargement (ns)

	top, left int // défilement (ligne, colonne d'affichage)

	hlVer int // version du tampon colorée (-1 : à calculer)
	hl    [][]class
	lang  *language // coloration choisie (syntax.go), à la première demande

	image *imageView // image (image.go) : onglet en lecture seule ; nil : texte

	lspOpen     bool // signalé à gopls (lsp.go)
	sentVer     int  // version du tampon envoyée à gopls
	syncPending bool // envoi programmé
}

type promptKind int

const (
	promptNone promptKind = iota
	promptFind
	promptGoto
	promptReplace // texte à remplacer (replace.go)
	promptWith    // texte de remplacement
	promptConfirm // revue des occurrences
	promptNew     // chemin d'un nouveau fichier (newfile.go)
)

// picker est la liste de choix d'un fichier à ouvrir.
type picker struct {
	all     []string
	query   string
	matches []string
	cursor  int
	offset  int
}

type Model struct {
	th   *theme.Theme
	proj *project.Project

	docs      []*doc
	cur       int
	editing   bool
	clip      string // presse-papiers interne
	clipLines bool   // … copié sans sélection : des lignes entières

	autoPair bool // fermeture automatique des parenthèses et guillemets
	imports  bool // imports organisés par gopls à l'enregistrement (imports.go)

	lastClick  time.Time // double et triple clic
	clickAt    textbuf.Pos
	clickCount int

	follow bool // garder le curseur visible au prochain rendu
	center bool // … et le placer au tiers de la hauteur

	prompt   promptKind
	input    string
	origin   textbuf.Pos // position avant la recherche (Échap y revient)
	notFound bool
	lastFind string
	repl     replace // remplacement en cours (replace.go)

	picker     *picker
	tree       tree // arborescence des fichiers, à gauche (tree.go)
	closeArmed bool // Ctrl+W sur un fichier modifié : encore une fois pour fermer

	define defineFunc // gopls en ligne de commande, sans connexion (goto.go)

	dbg *panel.DebugState // points d'arrêt et ligne d'arrêt (partagés avec Débogage)

	// Images (image.go).
	hd           bool   // haute définition (protocole Kitty)
	cellW, cellH int    // taille d'une cellule en pixels (0 : inconnue)
	hdNextID     uint32 // numéro de la prochaine image envoyée au terminal
	textW        int    // largeur de la zone de texte au dernier rendu

	watchFS bool        // surveiller le disque (fswatch.go)
	watcher *dirWatcher // nil : pas encore démarrée

	// Connexion à gopls (lsp.go).
	start    startFunc // nil : désactivée
	lsp      *lsp.Client
	lspState lspState
	events   chan tea.Msg                // messages de la connexion
	diags    map[string][]lsp.Diagnostic // par chemin

	back  []mark      // positions d'avant les sauts vers une définition (Ctrl+T)
	info  string      // documentation affichée sur le texte (vide : aucune)
	compl *completion // liste de complétion ouverte (complete.go)

	// Géométrie du dernier rendu (souris, pages).
	textH, gutterW int
	treeW          int // largeur de l'arbre, séparateur compris (0 : masqué)
	tabSpans       []span
}

// span est la place d'un onglet ; close est la colonne de sa croix.
type span struct{ x, w, close int }

func New(th *theme.Theme, p *project.Project) *Model {
	m := &Model{th: th, proj: p, textH: 20, tree: newTree(), define: gopls.Define, diags: map[string][]lsp.Diagnostic{},
		dbg: panel.NewDebugState(), watchFS: os.Getenv("GOPIT_WATCH") != "off", hd: termimg.Supported()}
	m.cellW, m.cellH = termimg.CellSize()
	if lspEnabled() {
		m.start = lsp.Start
	}
	m.autoPair, m.imports = true, true
	m.tree.focus = true // aucun fichier ouvert au départ
	return m
}

func (m *Model) Title() string { return i18n.N("Éditeur") }
func (m *Model) Icon() string  { return "" }
func (m *Model) Init() tea.Cmd { return nil }
func (m *Model) CanEdit() bool { return m.doc() != nil }
func (m *Model) Capturing() bool {
	return (m.editing && !m.tree.focus) || m.picker != nil || m.prompt != promptNone
}

// TakesCtrlC : Ctrl+C copie dès qu'un fichier est affiché et que le texte
// a le focus (en lecture comme en édition) ; ailleurs, il quitte.
func (m *Model) TakesCtrlC() bool {
	return m.doc() != nil && !m.tree.focus && m.picker == nil && m.prompt == promptNone
}

// doc renvoie le fichier affiché (nil : aucun).
func (m *Model) doc() *doc {
	if m.cur < 0 || m.cur >= len(m.docs) {
		return nil
	}
	return m.docs[m.cur]
}

// Unsaved renvoie les fichiers modifiés et non enregistrés.
func (m *Model) Unsaved() []string {
	var out []string
	for _, d := range m.docs {
		if d.buf.Dirty() {
			out = append(out, d.rel)
		}
	}
	return out
}

// ─── Fichiers ────────────────────────────────────────────────────────────────

func (m *Model) abs(file string) string {
	if !filepath.IsAbs(file) {
		file = filepath.Join(m.proj.Root, file)
	}
	return filepath.Clean(file)
}

func (m *Model) relName(path string) string {
	if rel, err := filepath.Rel(m.proj.Root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return path
}

// load ouvre un fichier (ou l'active s'il est déjà ouvert) sans changer de
// mode ; un fichier non modifié qui a changé sur le disque est relu.
func (m *Model) load(file string) (*doc, error) {
	path := m.abs(file)
	for i, d := range m.docs {
		if d.path != path {
			continue
		}
		m.cur = i
		if st, err := os.Stat(path); err == nil && !d.buf.Dirty() && st.ModTime().UnixNano() != d.mtime {
			if text, perm, err := readText(path); err == nil {
				cur := d.buf.Cursor()
				d.buf = textbuf.New(text)
				d.buf.SetCursor(cur, false)
				d.perm, d.mtime, d.hlVer = perm, st.ModTime().UnixNano(), -1
				d.sentVer = -1 // nouveau tampon : à renvoyer à gopls
			}
		}
		return d, nil
	}
	if isImage(path) {
		if _, err := os.Stat(path); err != nil {
			return nil, err
		}
		d := m.newImageDoc(path)
		m.docs = append(m.docs, d)
		m.cur = len(m.docs) - 1
		return d, nil
	}
	text, perm, err := readText(path)
	if err != nil {
		return nil, err
	}
	d := &doc{path: path, rel: m.relName(path), buf: textbuf.New(text), perm: perm, hlVer: -1}
	if st, err := os.Stat(path); err == nil {
		d.mtime = st.ModTime().UnixNano()
	}
	m.docs = append(m.docs, d)
	m.cur = len(m.docs) - 1
	return d, nil
}

// OpenFile ouvre file à la ligne line (1 = première ; 0 : position gardée)
// et passe en édition.
func (m *Model) OpenFile(file string, line int) tea.Cmd {
	d, err := m.load(file)
	if err != nil {
		return panel.Flash(i18n.T("Ouverture : ")+err.Error(), true)
	}
	m.picker, m.prompt = nil, promptNone
	m.tree.focus, m.tree.reveal = false, d.path
	if line > 0 {
		d.buf.SetCursor(textbuf.Pos{Line: line - 1}, false)
		if d.buf.Cursor().Col == 0 {
			d.buf.MoveCursor(textbuf.LineStart, 1, false) // après l'indentation
		}
		m.center = true
	}
	m.editing, m.follow = d.image == nil, true // une image ne se modifie pas
	return nil
}

// closeDoc ferme le fichier courant (après confirmation s'il est modifié).
func (m *Model) closeDoc() tea.Cmd {
	d := m.doc()
	if d == nil {
		return nil
	}
	if d.buf.Dirty() && !m.closeArmed {
		m.closeArmed = true
		return panel.Flash(d.rel+i18n.T(" n'est pas enregistré : Ctrl+W encore pour fermer sans enregistrer"), true)
	}
	m.closeArmed = false
	m.lspClosed(d)
	freed := closeImage(d)
	m.docs = append(m.docs[:m.cur], m.docs[m.cur+1:]...)
	m.cur = min(m.cur, len(m.docs)-1)
	if len(m.docs) == 0 {
		m.cur, m.editing = 0, false
		if m.tree.visible {
			m.tree.focus = true // plus de texte : retour à l'arbre
		}
	} else {
		m.tree.reveal = m.doc().path
	}
	return freed
}

// formatDoc passe le fichier Go courant à gofmt (une étape d'annulation).
func formatDoc(d *doc) error {
	src, err := format.Source([]byte(d.buf.String()))
	if err != nil {
		return err
	}
	d.buf.SetText(string(src))
	return nil
}

func isGo(d *doc) bool { return strings.HasSuffix(d.path, ".go") }

// save enregistre le fichier courant ; un fichier Go a d'abord ses imports
// organisés par gopls (imports.go), puis il est formaté.
func (m *Model) save() tea.Cmd {
	d := m.doc()
	if d == nil {
		return nil
	}
	if cmd := m.organize(d, true); cmd != nil {
		return cmd // l'enregistrement suit la réponse de gopls
	}
	return m.write(d, "")
}

// write enregistre d ; un fichier Go est d'abord formaté (s'il ne se compile
// pas, il est enregistré tel quel avec un avertissement). organized, s'il
// n'est pas vide, est le texte aux imports organisés : il remplace celui du
// tampon, avec le formatage, en une seule étape d'annulation.
func (m *Model) write(d *doc, organized string) tea.Cmd {
	var fmtErr error
	importsChanged := false
	if isGo(d) {
		text, cur := d.buf.String(), d.buf.Cursor()
		final := text
		if organized != "" && organized != text {
			cur = textbuf.MapPos(text, organized, cur)
			final, importsChanged = organized, true
		}
		if src, err := format.Source([]byte(final)); err != nil {
			fmtErr = err
		} else {
			cur = textbuf.MapPos(final, string(src), cur)
			final = string(src)
		}
		d.buf.SetTextAt(final, cur)
	}
	perm := d.perm
	if perm == 0 {
		perm = 0o644
	}
	if err := os.WriteFile(d.path, []byte(d.buf.String()), perm); err != nil {
		return panel.Flash(i18n.T("Enregistrement : ")+err.Error(), true)
	}
	d.buf.MarkSaved()
	m.lspSaved(d)
	if st, err := os.Stat(d.path); err == nil {
		d.mtime = st.ModTime().UnixNano()
	}
	m.follow = true
	if fmtErr != nil {
		return tea.Batch(panel.Saved(d.path), panel.Flash(i18n.T("Enregistré sans gofmt : ")+firstLine(fmtErr.Error()), true))
	}
	if importsChanged {
		return tea.Batch(panel.Saved(d.path), panel.Flash(i18n.T("Enregistré : ")+d.rel+i18n.T(" (imports organisés)"), false))
	}
	return tea.Batch(panel.Saved(d.path), panel.Flash(i18n.T("Enregistré : ")+d.rel, false))
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}

// ─── Préférences ─────────────────────────────────────────────────────────────

// State garde la liste des fichiers ouverts et le fichier affiché.
func (m *Model) State() map[string]string {
	files := make([]string, len(m.docs))
	for i, d := range m.docs {
		files[i] = d.path
	}
	st := map[string]string{"files": strings.Join(files, "\n"), "tree": "1"}
	if d := m.doc(); d != nil {
		st["current"] = d.path
	}
	if !m.tree.visible {
		st["tree"] = "0"
	}
	if !m.autoPair {
		st["pairs"] = "0"
	}
	if !m.imports {
		st["imports"] = "0"
	}
	var dirs []string
	for d, open := range m.tree.expanded {
		if open {
			dirs = append(dirs, d)
		}
	}
	slices.Sort(dirs)
	st["expanded"] = strings.Join(dirs, "\n")
	return st
}

func (m *Model) Restore(st map[string]string) {
	for f := range strings.SplitSeq(st["files"], "\n") {
		if f != "" {
			_, _ = m.load(f) // fichier disparu depuis : ignoré
		}
	}
	m.cur = 0
	for i, d := range m.docs {
		if d.path == st["current"] {
			m.cur = i
		}
	}
	m.tree.visible = st["tree"] != "0"
	m.autoPair = st["pairs"] != "0"
	m.imports = st["imports"] != "0"
	for d := range strings.SplitSeq(st["expanded"], "\n") {
		if d != "" {
			m.tree.expanded[d] = true
		}
	}
	if d := m.doc(); d != nil {
		m.tree.reveal = d.path
	} else {
		m.tree.focus = m.tree.visible
	}
}

// ─── Update ──────────────────────────────────────────────────────────────────

func (m *Model) Update(msg tea.Msg) (panel.Panel, tea.Cmd) {
	cmd := m.update(msg)
	if d := m.doc(); d != nil {
		m.dbg.Current = d.path // le débogueur s'en sert pour choisir sa cible
	} else {
		m.dbg.Current = ""
	}
	return m, tea.Batch(cmd, m.lspSync(), m.watchSync(), m.imageSync())
}

func (m *Model) update(msg tea.Msg) tea.Cmd {
	if cmd, ok := m.onLSP(msg); ok {
		return cmd
	}
	switch msg := msg.(type) {
	case panel.EditMsg:
		return m.action(msg.Action)
	case defMsg:
		return m.onDefinition(msg)
	case complMsg:
		return m.onCompletion(msg)
	case imageMsg:
		return m.onImage(msg)
	case organizedMsg:
		return m.onOrganized(msg)
	case panel.RepoChangedMsg:
		m.reloadClean()
		return nil
	case fsMsg:
		return m.onFS(msg)
	case tea.PasteMsg:
		m.compl = nil
		if d := m.doc(); d != nil && d.image != nil {
			return nil // une image ne se modifie pas
		}
		m.paste(msg.Content)
		return nil
	case panel.WheelMsg:
		m.wheel(msg.Delta < 0, msg.X < m.treeW)
	case panel.ClickMsg:
		m.compl = nil
		m.click(msg)
	case tea.KeyPressMsg:
		return m.keyWithCompletion(msg)
	}
	return nil
}

// keyWithCompletion traite une touche, puis tient la liste de complétion à
// jour : filtrée si le mot a changé, fermée si le curseur en est sorti, et
// ouverte après un « . ».
func (m *Model) keyWithCompletion(k tea.KeyPressMsg) tea.Cmd {
	var ver int
	var cur textbuf.Pos
	if d := m.doc(); d != nil {
		ver, cur = d.buf.Version(), d.buf.Cursor()
	}
	cmd := m.key(k)
	d := m.doc()
	if m.compl != nil && (d == nil || d.buf.Version() != ver || d.buf.Cursor() != cur) && !m.refilter() {
		m.compl = nil
	}
	if d != nil && m.editing && isGo(d) && k.Text == "." && d.buf.Version() != ver {
		cmd = tea.Batch(cmd, m.requestCompletion("."))
	}
	return cmd
}

// keyActions associe les raccourcis aux actions, en lecture comme en édition.
var keyActions = map[string]panel.EditAction{
	"ctrl+s": panel.ActSave, "ctrl+o": panel.ActOpen, "ctrl+n": panel.ActNew, "ctrl+w": panel.ActClose, "ctrl+f4": panel.ActClose,
	"ctrl+z": panel.ActUndo, "ctrl+y": panel.ActRedo, "ctrl+shift+z": panel.ActRedo,
	"ctrl+x": panel.ActCut, "ctrl+c": panel.ActCopy, "ctrl+insert": panel.ActCopy, "alt+c": panel.ActCopy,
	"ctrl+v": panel.ActPaste, "shift+insert": panel.ActPaste,
	"ctrl+a": panel.ActSelectAll, "ctrl+f": panel.ActFind, "f3": panel.ActFindNext,
	"ctrl+h": panel.ActReplace, "ctrl+r": panel.ActReplace,
	"ctrl+g": panel.ActGoto, "ctrl+/": panel.ActComment, "ctrl+_": panel.ActComment,
	"alt+shift+f": panel.ActFormat, "alt+F": panel.ActFormat, "alt+shift+o": actOrganize, "alt+O": actOrganize,
	"f12": panel.ActDefinition, "ctrl+]": panel.ActDefinition, "ctrl+t": panel.ActBack, "ctrl+k": panel.ActDoc,
	"ctrl+space": actComplete, "ctrl+@": actComplete, "f9": actBreakpoint,
	"alt+up": actMoveUp, "alt+down": actMoveDown,
	"alt+shift+up": actDupUp, "alt+shift+down": actDupDown,
	"ctrl+shift+k": actDeleteLine, "ctrl+l": actSelectLine,
	"ctrl+enter": actLineBelow, "ctrl+shift+enter": actLineAbove,
	"ctrl+m": actMatch, "ctrl+shift+\\": actMatch, "ctrl+|": actMatch,
}

// moves associe les touches de déplacement (sans Maj) aux mouvements.
var moves = map[string]textbuf.Move{
	"left": textbuf.Left, "right": textbuf.Right, "up": textbuf.Up, "down": textbuf.Down,
	"home": textbuf.LineStart, "end": textbuf.LineEnd,
	"ctrl+left": textbuf.WordLeft, "ctrl+right": textbuf.WordRight,
	"alt+left": textbuf.WordLeft, "alt+right": textbuf.WordRight,
	"ctrl+home": textbuf.DocStart, "ctrl+end": textbuf.DocEnd,
	"pgup": textbuf.Up, "pgdown": textbuf.Down,
}

// moveFor reconnaît une touche de déplacement, avec ou sans Maj.
func moveFor(s string) (mv textbuf.Move, extend, page, ok bool) {
	plain := strings.Replace(s, "shift+", "", 1)
	mv, ok = moves[plain]
	return mv, plain != s, strings.HasPrefix(plain, "pg"), ok
}

func (m *Model) key(k tea.KeyPressMsg) tea.Cmd {
	if m.picker != nil {
		return m.pickerKey(k)
	}
	if m.prompt != promptNone {
		return m.promptKey(k)
	}
	s := k.String()
	if s != "ctrl+w" && s != "ctrl+f4" {
		m.closeArmed = false
	}
	if m.compl != nil {
		if handled, cmd := m.complKey(s); handled {
			return cmd
		}
	}
	if m.info != "" { // la documentation se ferme à la touche suivante
		m.info = ""
		if s == "esc" {
			return nil
		}
	}
	switch s {
	case "ctrl+b": // afficher / masquer l'arbre
		return m.action(actTree)
	case "ctrl+e": // passer du texte à l'arbre et inversement
		if m.tree.focus && m.doc() != nil {
			m.tree.focus = false
		} else {
			m.focusTree()
		}
		return nil
	}
	if m.doc() == nil && m.tree.visible {
		m.tree.focus = true // rien à éditer : l'arbre a la main
	}
	if m.tree.focus && m.tree.visible {
		if handled, file := m.treeKey(s); handled {
			if file != "" {
				return m.OpenFile(file, 0)
			}
			return nil
		}
		// Les autres touches de lettre ne doivent pas modifier le texte.
		if _, ok := keyActions[s]; !ok && !strings.HasPrefix(s, "ctrl+") {
			return nil
		}
	}
	if a, ok := keyActions[s]; ok {
		return m.action(a)
	}
	switch s {
	case "shift+f3":
		return m.findNext(false)
	case "ctrl+pgdown", "ctrl+tab":
		m.switchDoc(1)
		return nil
	case "ctrl+pgup", "ctrl+shift+tab":
		m.switchDoc(-1)
		return nil
	}
	d := m.doc()
	if d == nil {
		if s == "enter" || s == "o" {
			return m.action(panel.ActOpen)
		}
		return nil
	}
	if d.image != nil { // image : en lecture seule
		switch s {
		case "w":
			return m.action(panel.ActClose)
		case "o":
			return m.action(panel.ActOpen)
		case "t":
			m.focusTree()
		}
		return nil
	}
	if mv, extend, page, ok := moveFor(s); ok {
		n := 1
		if page {
			n = max(1, m.textH-1)
		}
		d.buf.MoveCursor(mv, n, extend)
		m.follow = true
		return nil
	}

	if !m.editing {
		switch s {
		case "enter", "i":
			m.editing, m.follow = true, true
		case "o":
			return m.action(panel.ActOpen)
		case "s":
			return m.action(panel.ActSave)
		case "/":
			return m.action(panel.ActFind)
		case "n":
			return m.findNext(true)
		case "N":
			return m.findNext(false)
		case "g":
			return m.action(panel.ActGoto)
		case "w":
			return m.action(panel.ActClose)
		case "t":
			m.focusTree()
		case "d":
			return m.action(panel.ActDefinition)
		case "K":
			return m.action(panel.ActDoc)
		}
		return nil
	}

	b := d.buf
	switch s {
	case "esc":
		if _, _, ok := b.Selection(); ok {
			b.ClearSelection()
		} else {
			m.editing = false
		}
	case "enter":
		b.Newline()
	case "tab":
		if st, e, ok := b.Selection(); ok && st.Line != e.Line {
			b.Indent()
		} else {
			b.Insert("\t")
		}
	case "shift+tab":
		b.Dedent()
	case "backspace", "shift+backspace":
		if m.autoPair && m.inEmptyPair(b) {
			b.DeletePair()
		} else {
			b.Backspace()
		}
	case "ctrl+backspace", "alt+backspace":
		b.DeleteWordLeft()
	case "delete":
		b.Delete()
	case "ctrl+delete", "alt+delete":
		b.DeleteWordRight()
	default:
		if k.Text == "" || k.Mod.Contains(tea.ModCtrl) || k.Mod.Contains(tea.ModAlt) || k.Mod.Contains(tea.ModSuper) {
			return nil
		}
		m.typeText(b, k.Text)
	}
	m.follow = true
	return nil
}

func (m *Model) switchDoc(d int) {
	if n := len(m.docs); n > 1 {
		m.cur = ((m.cur+d)%n + n) % n
		m.follow = true
	}
}

// action exécute une action du menu Édition, de la palette ou d'un raccourci.
func (m *Model) action(a panel.EditAction) tea.Cmd {
	if a == actTree {
		if m.tree.visible {
			m.tree.visible, m.tree.focus = false, false
		} else {
			m.focusTree()
		}
		return nil
	}
	if a == panel.ActNew {
		m.startNewFile()
		return nil
	}
	if a == panel.ActOpen {
		all := listFiles(m.proj.Root)
		m.picker = &picker{all: all, matches: all}
		return nil
	}
	if a == panel.ActBack {
		return m.goBack()
	}
	d := m.doc()
	if d == nil {
		return nil
	}
	if d.image != nil && a != panel.ActClose {
		return nil // image : rien à modifier, chercher ou enregistrer
	}
	b := d.buf
	m.follow = true
	switch a {
	case actBreakpoint:
		return m.toggleBreakpoint()
	case actComplete:
		m.editing = true
		return m.requestCompletion("")
	case panel.ActDefinition:
		return m.lookup(true)
	case panel.ActDoc:
		return m.lookup(false)
	case panel.ActSave:
		return m.save()
	case panel.ActClose:
		cmd := m.closeDoc()
		if len(m.docs) == 0 {
			m.closeArmed = false
		}
		return cmd
	case panel.ActUndo:
		if !b.Undo() {
			return panel.Flash(i18n.T("Rien à annuler"), false)
		}
	case panel.ActRedo:
		if !b.Redo() {
			return panel.Flash(i18n.T("Rien à rétablir"), false)
		}
	case panel.ActCut:
		_, _, sel := b.Selection()
		m.clip, m.clipLines = b.CutLine(), !sel
		if m.clipLines { // dernière ligne : « \ntexte » → « texte\n »
			m.clip = strings.TrimPrefix(m.clip, "\n")
			if !strings.HasSuffix(m.clip, "\n") {
				m.clip += "\n"
			}
		}
		return tea.SetClipboard(m.clip)
	case panel.ActCopy:
		_, _, sel := b.Selection()
		m.clip, m.clipLines = b.CopyText(), !sel
		return tea.SetClipboard(m.clip)
	case panel.ActPaste:
		m.pasteText(b, m.clip)
	case panel.ActSelectAll:
		b.SelectAll()
	case panel.ActComment:
		b.ToggleComment()
	case panel.ActFormat:
		if !isGo(d) {
			return panel.Flash(i18n.T("gofmt ne s'applique qu'aux fichiers Go"), true)
		}
		if err := formatDoc(d); err != nil {
			return panel.Flash(i18n.T("gofmt : ")+firstLine(err.Error()), true)
		}
	case panel.ActFind, panel.ActReplace:
		m.prompt, m.input, m.notFound = promptFind, "", false
		if a == panel.ActReplace {
			m.prompt = promptReplace
		}
		m.origin = b.Cursor()
		if t := b.SelectedText(); t != "" && !strings.Contains(t, "\n") {
			m.input = t // chercher le texte sélectionné
			s, _, _ := b.Selection()
			m.origin = s
		}
	case actMoveUp, actMoveDown:
		b.MoveLines(map[bool]int{true: -1, false: 1}[a == actMoveUp])
	case actDupUp, actDupDown:
		b.DuplicateLines(map[bool]int{true: 0, false: 1}[a == actDupUp])
	case actDeleteLine:
		b.DeleteLines()
	case actSelectLine:
		m.editing = true
		b.SelectLine()
	case actLineBelow:
		m.editing = true
		b.InsertLineBelow()
	case actLineAbove:
		m.editing = true
		b.InsertLineAbove()
	case actMatch:
		if p, ok := m.matchingBracket(d); ok {
			b.SetCursor(p, false)
		}
	case actOrganize:
		if !isGo(d) {
			return panel.Flash(i18n.T("Les imports ne concernent que les fichiers Go"), true)
		}
		if cmd := m.organize(d, false); cmd != nil {
			return cmd
		}
		return panel.Flash(i18n.T("gopls n'est pas disponible"), true)
	case actImports:
		m.imports = !m.imports
		if m.imports {
			return panel.Flash(i18n.T("Imports organisés à l'enregistrement"), false)
		}
		return panel.Flash(i18n.T("Imports laissés tels quels à l'enregistrement"), false)
	case actAutoPair:
		m.autoPair = !m.autoPair
		if m.autoPair {
			return panel.Flash(i18n.T("Fermeture automatique des parenthèses activée"), false)
		}
		return panel.Flash(i18n.T("Fermeture automatique des parenthèses désactivée"), false)
	case panel.ActFindNext:
		return m.findNext(true)
	case panel.ActGoto:
		m.prompt, m.input = promptGoto, ""
	}
	return nil
}

func (m *Model) findNext(forward bool) tea.Cmd {
	d := m.doc()
	if d == nil {
		return nil
	}
	if m.lastFind == "" {
		return m.action(panel.ActFind)
	}
	m.follow = true
	if !d.buf.Find(m.lastFind, forward) {
		return panel.Flash(i18n.T("Introuvable : ")+m.lastFind, true)
	}
	return nil
}

// paste insère un collage du terminal (au texte, dans la saisie en cours…).
func (m *Model) paste(text string) {
	switch {
	case m.picker != nil:
		m.picker.query += firstLine(text)
		m.filterPicker()
	case m.prompt != promptNone:
		m.input += firstLine(text)
		m.searchAsYouType()
	case m.doc() != nil && m.editing:
		m.pasteText(m.doc().buf, text)
		m.follow = true
	}
}

// ─── Saisie : recherche, aller à la ligne ────────────────────────────────────

// editInput applique une touche à une saisie d'une ligne ; il renvoie false
// si la touche n'est pas une modification de la saisie.
func editInput(s *string, k tea.KeyPressMsg) bool {
	switch k.String() {
	case "backspace":
		if r := []rune(*s); len(r) > 0 {
			*s = string(r[:len(r)-1])
		}
		return true
	case "ctrl+u":
		*s = ""
		return true
	}
	if k.Text != "" && !k.Mod.Contains(tea.ModCtrl) && !k.Mod.Contains(tea.ModAlt) {
		*s += k.Text
		return true
	}
	return false
}

func (m *Model) searchAsYouType() {
	d := m.doc()
	if d == nil || (m.prompt != promptFind && m.prompt != promptReplace) {
		return
	}
	d.buf.SetCursor(m.origin, false)
	m.notFound = m.input != "" && !d.buf.Find(m.input, true)
	m.follow = true
}

func (m *Model) promptKey(k tea.KeyPressMsg) tea.Cmd {
	if m.prompt == promptNew {
		switch k.String() {
		case "esc":
			m.prompt = promptNone
		case "enter":
			m.prompt = promptNone
			return m.createFile(m.input)
		default:
			editInput(&m.input, k)
		}
		return nil
	}
	d := m.doc()
	if d == nil {
		m.prompt = promptNone
		return nil
	}
	if m.prompt == promptConfirm {
		return m.confirmKey(k.String())
	}
	searching := m.prompt == promptFind || m.prompt == promptReplace
	switch k.String() {
	case "esc":
		if searching || m.prompt == promptWith {
			d.buf.SetCursor(m.origin, false)
		}
		m.prompt = promptNone
		m.follow = true
		return nil
	case "enter":
		switch m.prompt {
		case promptReplace:
			if m.input == "" {
				m.prompt = promptNone
				return nil
			}
			m.repl.find, m.prompt, m.input = m.input, promptWith, m.repl.with
			return nil
		case promptWith:
			m.repl.with = m.input
			return m.startReview()
		}
		kind := m.prompt
		m.prompt = promptNone
		m.follow = true
		if kind == promptFind {
			if m.input != "" {
				m.lastFind = m.input
			}
			if m.notFound {
				return panel.Flash(i18n.T("Introuvable : ")+m.input, true)
			}
			return nil
		}
		n, err := strconv.Atoi(strings.TrimSpace(m.input))
		if err != nil || n < 1 {
			return panel.Flash(i18n.T("Numéro de ligne attendu"), true)
		}
		d.buf.SetCursor(textbuf.Pos{Line: n - 1}, false)
		d.buf.MoveCursor(textbuf.LineStart, 1, false)
		m.center = true
		return nil
	case "f3", "down", "ctrl+g":
		if searching && m.input != "" {
			m.notFound = !d.buf.Find(m.input, true)
			m.follow = true
		}
		return nil
	case "shift+f3", "up":
		if searching && m.input != "" {
			m.notFound = !d.buf.Find(m.input, false)
			m.follow = true
		}
		return nil
	}
	if editInput(&m.input, k) {
		m.searchAsYouType()
	}
	return nil
}

// ─── Choix d'un fichier ──────────────────────────────────────────────────────

func (m *Model) filterPicker() {
	p := m.picker
	p.matches = filterFiles(p.all, p.query)
	p.cursor, p.offset = 0, 0
}

func (m *Model) pickerKey(k tea.KeyPressMsg) tea.Cmd {
	p := m.picker
	switch k.String() {
	case "esc", "ctrl+o":
		m.picker = nil
	case "up", "ctrl+k":
		p.cursor = max(0, p.cursor-1)
	case "down", "ctrl+j":
		p.cursor = min(len(p.matches)-1, p.cursor+1)
	case "pgup":
		p.cursor = max(0, p.cursor-m.pickerRows())
	case "pgdown":
		p.cursor = min(len(p.matches)-1, p.cursor+m.pickerRows())
	case "enter":
		if p.cursor >= 0 && p.cursor < len(p.matches) {
			return m.OpenFile(p.matches[p.cursor], 0)
		}
	default:
		if editInput(&p.query, k) {
			m.filterPicker()
		}
	}
	return nil
}

// pickerRows est le nombre de fichiers affichés à la fois.
func (m *Model) pickerRows() int { return max(1, m.textH-1) }

// ─── Souris ──────────────────────────────────────────────────────────────────

func (m *Model) wheel(up, overTree bool) {
	if overTree {
		t := &m.tree
		if up {
			t.offset = max(0, t.offset-wheelStep)
		} else {
			t.offset += wheelStep
		}
		t.cursor = min(max(t.cursor, t.offset), t.offset+max(1, m.textH)-1)
		return
	}
	if p := m.picker; p != nil {
		if up {
			p.cursor = max(0, p.cursor-wheelStep)
		} else {
			p.cursor = min(len(p.matches)-1, p.cursor+wheelStep)
		}
		return
	}
	d := m.doc()
	if d == nil {
		return
	}
	if up {
		d.top -= wheelStep
	} else {
		d.top += wheelStep
	}
	m.follow = false
}

func (m *Model) click(c panel.ClickMsg) {
	if c.X < m.treeW && !c.Drag {
		if f := m.treeClick(c.Y, c.Drag); f != "" {
			m.OpenFile(f, 0)
		}
		return
	}
	c.X -= m.treeW
	m.tree.focus = false
	if c.Y == 0 && !c.Drag { // onglets
		for i, sp := range m.tabSpans {
			if c.X >= sp.x && c.X < sp.x+sp.w {
				m.cur, m.picker, m.prompt = i, nil, promptNone
				m.follow = true
				if c.X == sp.close {
					m.closeArmed = false
					m.closeDoc() // fichier modifié : un deuxième clic confirme
				}
				m.tree.reveal = ""
				if d := m.doc(); d != nil {
					m.tree.reveal = d.path
				}
				return
			}
		}
		return
	}
	if p := m.picker; p != nil {
		if i := p.offset + c.Y - 2; !c.Drag && c.Y >= 2 && i < len(p.matches) {
			if i == p.cursor {
				m.OpenFile(p.matches[i], 0)
			} else {
				p.cursor = i
			}
		}
		return
	}
	d := m.doc()
	if d == nil || m.prompt != promptNone {
		return
	}
	row := min(max(c.Y-1, 0), m.textH-1)
	line := min(d.top+row, d.buf.LineCount()-1)
	x := max(0, c.X-m.gutterW) + d.left
	col := runeAtCol([]rune(d.buf.Line(line)), x)
	p := textbuf.Pos{Line: line, Col: col}
	m.editing, m.follow = true, true
	if c.Drag || c.Shift {
		d.buf.SetCursor(p, true)
		return
	}
	// Double clic : le mot ; triple clic : la ligne.
	now := time.Now()
	if now.Sub(m.lastClick) < 400*time.Millisecond && p.Line == m.clickAt.Line && abs(p.Col-m.clickAt.Col) <= 1 {
		m.clickCount++
	} else {
		m.clickCount = 1
	}
	m.lastClick, m.clickAt = now, p
	switch m.clickCount {
	case 1:
		d.buf.SetCursor(p, false)
	case 2:
		d.buf.SelectWord(p)
	default:
		d.buf.SetCursor(p, false)
		d.buf.SelectLine()
	}
}

func abs(n int) int { return max(n, -n) }

// ─── Hints, commandes ────────────────────────────────────────────────────────

func (m *Model) Hints() []panel.Hint {
	switch {
	case m.picker != nil:
		return []panel.Hint{{Key: "↑↓", Desc: i18n.T("choisir")}, {Key: "Entrée", Desc: i18n.T("ouvrir")}, {Key: "Échap", Desc: i18n.T("annuler")}}
	case m.prompt == promptFind:
		return []panel.Hint{{Key: "Entrée", Desc: i18n.T("valider")}, {Key: "F3 / ↓", Desc: i18n.T("suivante")}, {Key: "Maj+F3 / ↑", Desc: i18n.T("précédente")}, {Key: "Échap", Desc: i18n.T("annuler")}}
	case m.compl != nil:
		return []panel.Hint{{Key: "↑↓", Desc: i18n.T("choisir")}, {Key: "Entrée / Tab", Desc: i18n.T("insérer")}, {Key: "Échap", Desc: i18n.T("fermer")}}
	case m.prompt == promptGoto:
		return []panel.Hint{{Key: "Entrée", Desc: i18n.T("aller")}, {Key: "Échap", Desc: i18n.T("annuler")}}
	case m.prompt == promptReplace || m.prompt == promptWith || m.prompt == promptNew:
		return []panel.Hint{{Key: "Entrée", Desc: i18n.T("valider")}, {Key: "Échap", Desc: i18n.T("annuler")}}
	case m.prompt == promptConfirm:
		return []panel.Hint{{Key: "Entrée", Desc: i18n.T("remplacer")}, {Key: "Espace / F3", Desc: i18n.T("passer")},
			{Key: "a", Desc: i18n.T("tout remplacer")}, {Key: "Échap", Desc: i18n.T("terminer")}}
	case m.tree.focus && m.tree.visible:
		return []panel.Hint{{Key: "↑↓", Desc: i18n.T("naviguer")}, {Key: "←→", Desc: i18n.T("replier / déplier")}, {Key: "Entrée", Desc: i18n.T("ouvrir")},
			{Key: "n", Desc: i18n.T("nouveau fichier")}, {Key: "Ctrl+E / Échap", Desc: i18n.T("texte")}, {Key: "Ctrl+B", Desc: i18n.T("masquer l'arbre")}, {Key: "r", Desc: i18n.T("relire")}}
	case m.doc() != nil && m.doc().image != nil:
		return []panel.Hint{{Key: "w / Ctrl+W", Desc: i18n.T("fermer")}, {Key: "o / Ctrl+O", Desc: i18n.T("ouvrir")},
			{Key: "t", Desc: i18n.T("arbre")}, {Key: "Ctrl+PgUp/PgDn", Desc: i18n.T("autre fichier")}}
	case m.doc() == nil:
		return []panel.Hint{{Key: "Entrée / Ctrl+O", Desc: i18n.T("ouvrir un fichier")}, {Key: "Ctrl+N", Desc: i18n.T("nouveau fichier")}, {Key: "Ctrl+B", Desc: i18n.T("arbre")}}
	case m.editing:
		return []panel.Hint{{Key: "Ctrl+S", Desc: i18n.T("enregistrer")}, {Key: "Ctrl+W", Desc: i18n.T("fermer")}, {Key: "Ctrl+O", Desc: i18n.T("ouvrir")},
			{Key: "Ctrl+Espace", Desc: i18n.T("compléter")}, {Key: "Ctrl+F", Desc: i18n.T("chercher")}, {Key: "F12", Desc: i18n.T("définition")}, {Key: "Ctrl+E", Desc: i18n.T("arbre")}, {Key: "Échap", Desc: i18n.T("lecture")}}
	}
	return []panel.Hint{{Key: "Entrée", Desc: i18n.T("modifier")}, {Key: "w / Ctrl+W", Desc: i18n.T("fermer")}, {Key: "o", Desc: i18n.T("ouvrir")},
		{Key: "s", Desc: i18n.T("enregistrer")}, {Key: "/", Desc: i18n.T("chercher")}, {Key: "d / F12", Desc: i18n.T("définition")},
		{Key: "K", Desc: i18n.T("doc")}, {Key: "Ctrl+T", Desc: i18n.T("retour")}, {Key: "t", Desc: i18n.T("arbre")}}
}

// actBreakpoint pose ou retire un point d'arrêt (F9).
const actBreakpoint panel.EditAction = "breakpoint"

// UseDebug partage l'état de débogage avec le module Débogage.
func (m *Model) UseDebug(s *panel.DebugState) { m.dbg = s }

// toggleBreakpoint pose ou retire un point d'arrêt sur la ligne du curseur.
func (m *Model) toggleBreakpoint() tea.Cmd {
	d := m.doc()
	if d == nil || !isGo(d) {
		return panel.Flash(i18n.T("Les points d'arrêt se posent dans un fichier Go"), true)
	}
	line := d.buf.Cursor().Line + 1
	set := m.dbg.Toggle(d.path, line)
	msg := fmt.Sprintf(i18n.T("Point d'arrêt retiré : %s:%d"), d.rel, line)
	if set {
		msg = fmt.Sprintf(i18n.T("Point d'arrêt : %s:%d"), d.rel, line)
	}
	path := d.path
	return tea.Batch(panel.Flash(msg, false), func() tea.Msg { return panel.BreakpointsMsg{Path: path} })
}

// ShowLine affiche une position en lecture (ligne où le programme débogué
// est arrêté).
func (m *Model) ShowLine(file string, line int) tea.Cmd {
	d, err := m.load(file)
	if err != nil {
		return panel.Flash(i18n.T("Ouverture : ")+err.Error(), true)
	}
	m.picker, m.prompt, m.compl, m.info = nil, promptNone, nil, ""
	m.tree.focus, m.tree.reveal = false, d.path
	d.buf.SetCursor(textbuf.Pos{Line: max(0, line-1)}, false)
	d.buf.MoveCursor(textbuf.LineStart, 1, false)
	m.editing, m.follow, m.center = false, true, true
	return nil
}

// reloadClean relit les fichiers ouverts non modifiés qui ont changé sur le
// disque (après un changement de branche) ; un fichier modifié dans
// l'éditeur est laissé tel quel.
func (m *Model) reloadClean() {
	for _, d := range m.docs {
		st, err := os.Stat(d.path)
		if err != nil || d.buf.Dirty() || st.ModTime().UnixNano() == d.mtime {
			continue
		}
		if d.image != nil { // image réécrite : nouveau rendu
			d.mtime = st.ModTime().UnixNano()
			d.image.info, d.image.key = imgrender.ReadInfo(d.path), ""
			continue
		}
		text, perm, err := readText(d.path)
		if err != nil {
			continue
		}
		cur := d.buf.Cursor()
		d.buf = textbuf.New(text)
		d.buf.SetCursor(cur, false)
		d.perm, d.mtime, d.hlVer, d.lang = perm, st.ModTime().UnixNano(), -1, nil
		d.sentVer = -1 // nouveau tampon : à renvoyer à gopls
	}
}
