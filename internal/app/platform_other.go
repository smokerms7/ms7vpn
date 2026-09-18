//go:build !windows

package app

import (
	"fmt"
	"os/exec"
	"runtime"
)

func IsAdministrator() bool                      { return true }
func RelaunchElevated(_ string) error            { return fmt.Errorf("elevation is implemented only on Windows") }
func EnableSystemProxy(_ string, _, _ int) error { return nil }
func RestoreSystemProxy(_ string) error          { return nil }
func RecoverSystemProxy(_ string) error          { return nil }

func OpenAppWindow(rawURL, _ string) (<-chan struct{}, error) { return nil, OpenExternalURL(rawURL) }
func OpenFolder(path string) error {
	return exec.Command("xdg-open", path).Start()
}

func OpenExternalURL(rawURL string) error {
	var command *exec.Cmd
	if runtime.GOOS == "darwin" {
		command = exec.Command("open", rawURL)
	} else {
		command = exec.Command("xdg-open", rawURL)
	}
	return command.Start()
}
