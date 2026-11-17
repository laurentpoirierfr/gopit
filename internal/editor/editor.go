// Package editor ouvre l'éditeur de l'utilisateur ($VISUAL, $EDITOR, sinon
// vi), éventuellement sur un fichier à une ligne donnée, en suspendant
// l'interface le temps de l'édition.
package editor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// DoneMsg arrive quand l'éditeur est refermé.
type DoneMsg struct{ Err error }

// Command renvoie la commande de l'éditeur (peut contenir des options,
// ex. « code --wait »).
func Command() string {
	for _, v := range []string{"VISUAL", "EDITOR"} {
		if ed := strings.TrimSpace(os.Getenv(v)); ed != "" {
			return ed
		}
	}
	return "vi"
}

// Args renvoie les arguments pour ouvrir file à la ligne line, selon la
// convention de l'éditeur. Avec file vide, ouvre target (un répertoire).
func Args(ed, target, file string, line int) []string {
	if file == "" {
		return []string{target}
	}
	if line <= 0 {
		return []string{file}
	}
	fields := strings.Fields(ed)
	switch filepath.Base(fields[0]) {
	case "code", "codium", "code-insiders", "cursor", "windsurf":
		return []string{"-g", fmt.Sprintf("%s:%d", file, line)}
	case "hx", "helix", "zed", "subl", "kak":
		return []string{fmt.Sprintf("%s:%d", file, line)}
	default: // vi, vim, nvim, nano, micro, emacs, kate…
		return []string{fmt.Sprintf("+%d", line), file}
	}
}

// Open ouvre l'éditeur dans dir, sur file:line si file n'est pas vide, sinon
// sur le répertoire dir.
func Open(dir, file string, line int) tea.Cmd {
	ed := Command()
	// Passer par le shell : $EDITOR peut contenir des options.
	args := append([]string{"-c", ed + ` "$@"`, "gopit"}, Args(ed, dir, file, line)...)
	c := exec.Command("sh", args...)
	c.Dir = dir
	return tea.ExecProcess(c, func(err error) tea.Msg { return DoneMsg{err} })
}
