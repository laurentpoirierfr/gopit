package app

// Changement de branche git : liste des branches dans la palette (clic sur
// la branche de la barre d'état, « b » dans le module Git, ou la palette),
// création d'une branche si le nom tapé n'existe pas.

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/laurentpoirierfr/gopit/internal/gitx"
	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/panel"
)

type branchesMsg struct {
	list []gitx.Branch
	err  error
}

type switchedMsg struct {
	name string
	err  error
}

// openBranches lit les branches, puis ouvre leur liste.
func (m *Model) openBranches() tea.Cmd {
	root := m.proj.Root
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		list, err := gitx.Branches(ctx, root)
		return branchesMsg{list: list, err: err}
	}
}

// showBranches ouvre la liste des branches dans la palette.
func (m *Model) showBranches(list []gitx.Branch) {
	items := make([]paletteItem, 0, len(list))
	for _, b := range list {
		key := ""
		switch {
		case b.Current:
			key = i18n.T("actuelle")
		case b.Remote:
			key = i18n.T("distante")
		}
		title := "  " + b.Name
		if b.Current {
			title = "✓ " + b.Name
		}
		items = append(items, paletteItem{title: title, key: key, run: func(m *Model) tea.Cmd {
			if b.Current {
				return nil
			}
			return m.switchBranch(b, false)
		}})
	}
	exists := map[string]bool{}
	for _, b := range list {
		exists[b.Name] = true
	}
	extra := func(q string) []paletteItem {
		name := strings.TrimSpace(q)
		if name == "" || exists[name] || strings.ContainsAny(name, " \t~^:?*[\\") {
			return nil
		}
		return []paletteItem{{title: fmt.Sprintf(i18n.T("+ Créer la branche « %s »"), name), run: func(m *Model) tea.Cmd {
			return m.switchBranch(gitx.Branch{Name: name}, true)
		}}}
	}
	m.menu.open, m.modal = -1, modalNone
	m.pal = palette{open: true, base: items, extra: extra, prompt: " ",
		footer: i18n.T(" %d branche(s) · tape un nom pour filtrer ou créer · Entrée changer · Échap fermer")}
	m.filterPalette()
}

// switchBranch change de branche (git switch) en arrière-plan.
func (m *Model) switchBranch(b gitx.Branch, create bool) tea.Cmd {
	root := m.proj.Root
	name := b.Name
	if b.Remote {
		_, name, _ = strings.Cut(b.Name, "/")
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		return switchedMsg{name: name, err: gitx.Switch(ctx, root, b, create)}
	}
}

// onSwitched annonce le changement de branche : barre d'état, et modules
// dont les fichiers ou l'analyse ont pu changer.
func (m *Model) onSwitched(msg switchedMsg) tea.Cmd {
	if msg.err != nil {
		return m.setFlash(i18n.T("Changement de branche impossible : ")+msg.err.Error(), true)
	}
	return tea.Batch(m.setFlash(i18n.T("Branche : ")+msg.name, false), m.refreshGit(),
		m.broadcast(panel.RepoChangedMsg{}))
}
