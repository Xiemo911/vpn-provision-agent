//go:build !windows

package system

import (
	"os/exec"
	"syscall"
)

// killGroupOnCancel runs cmd in its own process group and kills the whole
// group when cmd's context ends. Killing only the shell would leave curl,
// apt or make running (and holding the dpkg lock) after a timeout.
func killGroupOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
