package panel

import (
	"os/exec"
	"runtime"

	tea "charm.land/bubbletea/v2"

	"github.com/laurentpoirierfr/gopit/internal/i18n"
)

// Browse ouvre url dans le navigateur et renvoie le message à afficher.
func Browse(url string) tea.Cmd {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return Flash(i18n.T("Impossible d'ouvrir le navigateur : ")+err.Error(), true)
	}
	go func() { _ = cmd.Wait() }()
	return Flash(i18n.T("Ouverture de ")+url, false)
}
