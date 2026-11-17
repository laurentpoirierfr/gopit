// Package watch surveille les fichiers Go d'un module et signale, par lots,
// les répertoires modifiés. Les rafales d'écritures (un enregistrement de
// l'éditeur produit souvent plusieurs événements) sont regroupées.
package watch

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// AllDirs est renvoyé dans un lot quand go.mod ou go.sum change : tout le
// module est concerné.
const AllDirs = "*"

// Watcher surveille récursivement les répertoires d'un module.
type Watcher struct {
	// Changes reçoit, après un temps de calme, la liste triée des
	// répertoires dont un fichier .go a changé (ou AllDirs).
	Changes <-chan []string

	fw       *fsnotify.Watcher
	root     string
	debounce time.Duration
	changes  chan []string
	done     chan struct{}
	once     sync.Once
}

// New commence à surveiller root (hors vendor, testdata et répertoires
// cachés). Les lots sont envoyés après debounce sans nouvel événement.
func New(root string, debounce time.Duration) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	ch := make(chan []string, 4)
	w := &Watcher{Changes: ch, fw: fw, root: root, debounce: debounce, changes: ch, done: make(chan struct{})}
	if err := w.addTree(root); err != nil {
		fw.Close()
		return nil, err
	}
	go w.loop()
	return w, nil
}

// Close arrête la surveillance ; Changes est alors fermé.
func (w *Watcher) Close() error {
	var err error
	w.once.Do(func() {
		close(w.done)
		err = w.fw.Close()
	})
	return err
}

func skipDir(root, path, name string) bool {
	return path != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") ||
		name == "vendor" || name == "testdata" || name == "node_modules")
}

// addTree ajoute dir et ses sous-répertoires (fsnotify n'est pas récursif).
func (w *Watcher) addTree(dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if skipDir(w.root, p, d.Name()) {
			return filepath.SkipDir
		}
		return w.fw.Add(p)
	})
}

func (w *Watcher) loop() {
	defer close(w.changes)
	pending := map[string]bool{}
	timer := time.NewTimer(time.Hour)
	timer.Stop()

	for {
		select {
		case <-w.done:
			return

		case ev, ok := <-w.fw.Events:
			if !ok {
				return
			}
			name := filepath.Base(ev.Name)
			switch {
			case ev.Has(fsnotify.Create) && isDir(ev.Name):
				// Nouveau répertoire : le surveiller aussi.
				if !skipDir(w.root, ev.Name, name) {
					_ = w.addTree(ev.Name)
				}
				continue
			case name == "go.mod" || name == "go.sum":
				pending[AllDirs] = true
			case strings.HasSuffix(name, ".go") && !strings.HasPrefix(name, ".") &&
				ev.Has(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename):
				pending[filepath.Dir(ev.Name)] = true
			default:
				continue // fichiers temporaires d'éditeur, chmod…
			}
			timer.Reset(w.debounce)

		case <-timer.C:
			if len(pending) == 0 {
				continue
			}
			dirs := make([]string, 0, len(pending))
			for d := range pending {
				dirs = append(dirs, d)
			}
			sort.Strings(dirs)
			pending = map[string]bool{}
			select {
			case w.changes <- dirs:
			case <-w.done:
				return
			}

		case <-w.fw.Errors:
			// Erreur de surveillance (trop de fichiers…) : on continue.
		}
	}
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
