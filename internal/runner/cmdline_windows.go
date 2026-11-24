//go:build windows

package runner

import (
	"os/exec"
	"syscall"
)

// cmd.exe parses its own command line, so Go's argument quoting would mangle
// scripts that contain quotes; hand it the line verbatim instead.
func setRawCmdLine(cmd *exec.Cmd, line string) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line}
}
