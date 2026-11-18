//go:build windows

package shell

import "os/exec"

// shellCommand prépare l'exécution d'une ligne par cmd.exe.
func shellCommand(line string) *exec.Cmd {
	return exec.Command("cmd", "/C", line)
}

// stop arrête la commande.
func stop(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
