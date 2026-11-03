package xray

import (
	"context"
	"os/exec"
)

// PrepareChild and AdoptChild share the application's hidden-process/job-object
// lifecycle with the second core. A crashed UI must not leave a VPN running.
func PrepareChild(cmd *exec.Cmd) { configureHiddenProcess(cmd) }
func AdoptChild(cmd *exec.Cmd)   { adoptProcess(cmd) }

func DownloadVerified(ctx context.Context, url, path, sha string, progress func(int)) error {
	return downloadVerified(ctx, url, path, sha, progress)
}
