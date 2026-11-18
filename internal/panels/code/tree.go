package code

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
)

// Arborescence des fichiers du projet, à gauche de l'éditeur. Les
// répertoires sont lus à la demande (à l'ouverture d'un dossier) et relus
// quand l'arbre reprend le focus ou avec « r ».

type treeEntry struct {
	name string
	dir  bool
}

// treeRow est une ligne affichée de l'arbre.
type treeRow struct {
	path  string // chemin absolu
	name  string
	dir   bool
	depth int
}

type tree struct {
	visible  bool
	focus    bool
	expanded map[string]bool        // répertoires ouverts (chemins absolus)
	cache    map[string][]treeEntry // contenu lu des répertoires
	rows     []treeRow
	cursor   int
	offset   int
	reveal   string // fichier à montrer au prochain calcul (vide : aucun)
}

func newTree() tree {
	return tree{visible: true, expanded: map[string]bool{}, cache: map[string][]treeEntry{}}
}

// readDir renvoie le contenu d'un répertoire : dossiers d'abord, puis
// fichiers, par ordre alphabétique ; les dossiers ignorés (.git, vendor…)
// sont omis.
func (t *tree) readDir(dir string) []treeEntry {
	if e, ok := t.cache[dir]; ok {
		return e
	}
	des, _ := os.ReadDir(dir) // répertoire illisible : vide
	var out []treeEntry
	for _, de := range des {
		isDir := de.IsDir()
		if de.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(dir, de.Name())); err == nil {
				isDir = st.IsDir()
			}
		}
		if isDir && skipDir(de.Name()) {
			continue
		}
		out = append(out, treeEntry{name: de.Name(), dir: isDir})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].dir != out[j].dir {
			return out[i].dir
		}
		return strings.ToLower(out[i].name) < strings.ToLower(out[j].name)
	})
	t.cache[dir] = out
	return out
}

// refresh oublie le contenu lu : les répertoires seront relus.
func (t *tree) refresh() { t.cache = map[string][]treeEntry{} }

// flatten recalcule les lignes affichées à partir de root, en gardant le
// curseur sur la même entrée (ou sur le fichier à montrer).
func (t *tree) flatten(root string) {
	keep := ""
	if t.cursor >= 0 && t.cursor < len(t.rows) {
		keep = t.rows[t.cursor].path
	}
	if t.reveal != "" {
		// Ouvrir les dossiers parents du fichier à montrer.
		if rel, err := filepath.Rel(root, t.reveal); err == nil && !strings.HasPrefix(rel, "..") {
			for d := filepath.Dir(t.reveal); len(d) > len(root); d = filepath.Dir(d) {
				t.expanded[d] = true
			}
			keep = t.reveal
		}
		t.reveal = ""
	}
	t.rows = t.rows[:0]
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		for _, e := range t.readDir(dir) {
			p := filepath.Join(dir, e.name)
			t.rows = append(t.rows, treeRow{path: p, name: e.name, dir: e.dir, depth: depth})
			if e.dir && t.expanded[p] {
				walk(p, depth+1)
			}
		}
	}
	walk(root, 0)
	for i, r := range t.rows {
		if r.path == keep {
			t.cursor = i
			return
		}
	}
	t.cursor = min(max(t.cursor, 0), max(0, len(t.rows)-1))
}

func (t *tree) selected() *treeRow {
	if t.cursor < 0 || t.cursor >= len(t.rows) {
		return nil
	}
	return &t.rows[t.cursor]
}

// parentRow renvoie l'indice de la ligne du dossier parent de la ligne i.
func (t *tree) parentRow(i int) int {
	for j := i - 1; j >= 0; j-- {
		if t.rows[j].depth < t.rows[i].depth {
			return j
		}
	}
	return i
}

// treeWidth renvoie la largeur de l'arbre (0 : masqué ou pas la place).
func (m *Model) treeWidth(width int) int {
	if !m.tree.visible || width < 60 {
		return 0
	}
	return min(max(width/4, 22), 36)
}

// focusTree donne le focus à l'arbre (en l'affichant) et y montre le
// fichier courant.
func (m *Model) focusTree() {
	m.tree.visible, m.tree.focus = true, true
	m.tree.refresh()
	if d := m.doc(); d != nil {
		m.tree.reveal = d.path
	}
}

// treeKey traite une touche quand l'arbre a le focus ; il renvoie false si
// la touche ne le concerne pas.
func (m *Model) treeKey(s string) (handled bool, open string) {
	t := &m.tree
	t.flatten(m.proj.Root)
	r := t.selected()
	page := max(1, m.textH-1)
	switch s {
	case "up", "k":
		t.cursor = max(0, t.cursor-1)
	case "down", "j":
		t.cursor = min(len(t.rows)-1, t.cursor+1)
	case "pgup":
		t.cursor = max(0, t.cursor-page)
	case "pgdown":
		t.cursor = min(len(t.rows)-1, t.cursor+page)
	case "home", "g":
		t.cursor = 0
	case "end", "G":
		t.cursor = len(t.rows) - 1
	case "left", "h":
		if r != nil && r.dir && t.expanded[r.path] {
			delete(t.expanded, r.path)
		} else if r != nil {
			t.cursor = t.parentRow(t.cursor)
		}
	case "right", "l":
		if r != nil && r.dir {
			if t.expanded[r.path] {
				t.cursor = min(len(t.rows)-1, t.cursor+1)
			} else {
				t.expanded[r.path] = true
			}
		}
	case "enter", "space", " ":
		if r == nil {
			break
		}
		if r.dir {
			t.expanded[r.path] = !t.expanded[r.path]
			break
		}
		return true, r.path
	case "r":
		t.refresh()
	case "n":
		m.startNewFile()
		return true, ""
	case "esc", "tab", "ctrl+e":
		if m.doc() != nil {
			t.focus = false
		}
	default:
		return false, ""
	}
	t.flatten(m.proj.Root)
	return true, ""
}

// treeClick traite un clic sur la ligne y de l'arbre (0 : en-tête).
func (m *Model) treeClick(y int, drag bool) string {
	t := &m.tree
	if drag {
		return ""
	}
	t.focus = true
	i := t.offset + y - 1
	if y < 1 || i >= len(t.rows) {
		return ""
	}
	r := t.rows[i]
	if r.dir {
		t.expanded[r.path] = !t.expanded[r.path]
		t.cursor = i
		return ""
	}
	t.cursor = i
	return r.path
}

func (m *Model) viewTree(w, h int) string {
	th := m.th
	t := &m.tree
	t.flatten(m.proj.Root)
	rows := max(1, h-1)
	if t.cursor < t.offset {
		t.offset = t.cursor
	} else if t.cursor >= t.offset+rows {
		t.offset = t.cursor - rows + 1
	}
	t.offset = min(t.offset, max(0, len(t.rows)-rows))

	open := map[string]*doc{}
	for _, d := range m.docs {
		open[d.path] = d
	}
	var cur string
	if d := m.doc(); d != nil {
		cur = d.path
	}

	head := th.Label.Bold(true).Render(ansi.Truncate(filepath.Base(m.proj.Root)+"/", w, "…"))
	lines := []string{head}
	for i := t.offset; i < min(len(t.rows), t.offset+rows); i++ {
		r := t.rows[i]
		icon := "  "
		if r.dir {
			icon = "▸ "
			if t.expanded[r.path] {
				icon = "▾ "
			}
		}
		label := strings.Repeat("  ", r.depth) + icon + r.name
		if r.dir {
			label += "/"
		}
		mark := ""
		if d := open[r.path]; d != nil && d.buf.Dirty() {
			mark = " ●"
		}
		label = ansi.Truncate(label, w-lipgloss.Width(mark)-1, "…") + mark

		var st lipgloss.Style
		switch {
		case i == t.cursor && t.focus:
			st = th.NavItemSel
		case i == t.cursor:
			st = th.NavItemBlur
		case r.path == cur:
			st = th.NavItem.Foreground(th.P.Accent).Bold(true)
		case r.dir:
			st = th.NavItem.Foreground(th.P.Blue)
		case open[r.path] != nil:
			st = th.NavItem
		default:
			st = th.NavItem.Foreground(th.P.Subtext)
		}
		lines = append(lines, st.Padding(0).Width(w).Render(label))
	}
	if len(t.rows) == 0 {
		lines = append(lines, th.Dim.Render(i18n.T("(vide)")))
	}
	return lipgloss.NewStyle().Width(w).Height(h).MaxHeight(h).Render(strings.Join(lines, "\n"))
}
