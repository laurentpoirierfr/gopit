//go:build !windows

package shell

import (
	"os"
	"os/exec"
	"syscall"
)

// shellCommand prépare l'exécution d'une ligne par le shell de l'utilisateur
// ($SHELL, sinon sh), dans son propre groupe de processus : l'arrêter
// arrête aussi ce qu'il a lancé (go run, make…).
func shellCommand(line string) *exec.Cmd {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "sh"
	}
	cmd := exec.Command(sh, "-c", line)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

// stop arrête la commande et ses descendants.
func stop(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
