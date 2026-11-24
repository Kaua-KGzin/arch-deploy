//go:build !windows

package runner

import "os/exec"

func setRawCmdLine(cmd *exec.Cmd, line string) {}
