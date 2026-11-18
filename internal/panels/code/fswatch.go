package code

// Surveillance du disque : les répertoires affichés dans l'arbre (racine,
// dossiers dépliés) et ceux des fichiers ouverts sont surveillés, pour que
// l'arbre montre aussitôt un fichier créé, supprimé ou renommé ailleurs
// (terminal, autre éditeur, git), et qu'un fichier ouvert non modifié soit
// relu s'il change sur le disque.

import (
	"path/filepath"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/fsnotify/fsnotify"
)

// fsDelay regroupe les rafales d'événements (une copie, un git checkout…).
const fsDelay = 150 * time.Millisecond

// fsMsg signale des répertoires dont le contenu a changé.
type fsMsg struct{ dirs []string }

// dirWatcher surveille un ensemble de répertoires (sans récursion).
type dirWatcher struct {
	fw    *fsnotify.Watcher
	mu    sync.Mutex
	dirs  map[string]bool
	out   chan fsMsg
	close sync.Once
}

func newDirWatcher() (*dirWatcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &dirWatcher{fw: fw, dirs: map[string]bool{}, out: make(chan fsMsg, 8)}
	go w.loop()
	return w, nil
}

// set fait correspondre les répertoires surveillés à want.
func (w *dirWatcher) set(want map[string]bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for d := range w.dirs {
		if !want[d] {
			_ = w.fw.Remove(d)
			delete(w.dirs, d)
		}
	}
	for d := range want {
		if !w.dirs[d] && w.fw.Add(d) == nil {
			w.dirs[d] = true
		}
	}
}

func (w *dirWatcher) Close() { w.close.Do(func() { _ = w.fw.Close() }) }

func (w *dirWatcher) loop() {
	defer close(w.out)
	pending := map[string]bool{}
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	for {
		select {
		case ev, ok := <-w.fw.Events:
			if !ok {
				return
			}
			if !ev.Has(fsnotify.Create | fsnotify.Remove | fsnotify.Rename | fsnotify.Write) {
				continue // changement de droits…
			}
			pending[filepath.Dir(ev.Name)] = true
			timer.Reset(fsDelay)
		case <-timer.C:
			if len(pending) == 0 {
				continue
			}
			var msg fsMsg
			for d := range pending {
				msg.dirs = append(msg.dirs, d)
			}
			pending = map[string]bool{}
			w.out <- msg
		case _, ok := <-w.fw.Errors:
			if !ok {
				return
			}
		}
	}
}

// watchSync démarre la surveillance au premier appel, puis la tient à jour
// avec l'arbre et les fichiers ouverts. Elle est appelée après chaque
// message.
func (m *Model) watchSync() tea.Cmd {
	if !m.watchFS {
		return nil
	}
	var cmd tea.Cmd
	if m.watcher == nil {
		w, err := newDirWatcher()
		if err != nil {
			m.watchFS = false // pas de surveillance possible : « r » relit l'arbre
			return nil
		}
		m.watcher = w
		cmd = m.waitFS()
	}
	want := map[string]bool{m.proj.Root: true}
	if m.tree.visible {
		for d := range m.tree.expanded {
			want[d] = true
		}
	}
	for _, d := range m.docs {
		want[filepath.Dir(d.path)] = true
	}
	m.watcher.set(want)
	return cmd
}

func (m *Model) waitFS() tea.Cmd {
	ch := m.watcher.out
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

// onFS relit ce qui a changé sur le disque.
func (m *Model) onFS(msg fsMsg) tea.Cmd {
	for _, d := range msg.dirs {
		delete(m.tree.cache, d) // relu au prochain affichage
	}
	m.reloadClean()
	return m.waitFS()
}
