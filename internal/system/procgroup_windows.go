package system

import "os/exec"

// killGroupOnCancel is a no-op on Windows, where the agent only gets built
// for development; it runs on Linux.
func killGroupOnCancel(*exec.Cmd) {}
