//go:build !windows

package xray

import (
	"os"
	"os/exec"
	"syscall"
)

func configureHiddenProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	if err := process.Signal(syscall.SIGTERM); err == nil {
		return nil
	}
	return process.Kill()
}

// adoptProcess на не-Windows не нужен: процесс и так живёт в своей группе.
func adoptProcess(*exec.Cmd) {}

// KillOrphans реализован только для Windows.
func KillOrphans(string) int { return 0 }
