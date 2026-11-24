// Package runner executes the commands a deploy is made of.
//
// Every provider turns a target into a list of Cmd values; the engine hands
// them to a Runner. Keeping the commands as data is what makes `plan`, dry
// runs and the tests possible: the same list is either printed or executed.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Cmd is one step of a deploy: an external program, or an internal step
// (Func) such as an HTTP call.
type Cmd struct {
	Name string
	Args []string
	Dir  string
	Env  []string // extra KEY=VALUE pairs on top of the current environment

	// Func, when set, replaces the external program; Label describes it.
	Func  func(ctx context.Context, out io.Writer) error
	Label string

	// Secrets are masked whenever the command is printed.
	Secrets []string

	// rawCmdLine passes Args to the program unquoted (needed for cmd /C).
	rawCmdLine bool
}

// String renders the command the way it is shown to the user.
func (c Cmd) String() string {
	var s string
	if c.Func != nil {
		s = c.Label
	} else if c.rawCmdLine {
		s = c.Name + " " + strings.Join(c.Args, " ")
	} else {
		parts := make([]string, 0, len(c.Args)+1)
		parts = append(parts, quote(c.Name))
		for _, a := range c.Args {
			parts = append(parts, quote(a))
		}
		s = strings.Join(parts, " ")
	}
	for _, secret := range c.Secrets {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "****")
		}
	}
	return s
}

func quote(s string) string {
	if s == "" {
		return "''"
	}
	// Backslashes are left alone: the rendering is for people, and quoting
	// every Windows path would make plans unreadable.
	if strings.ContainsAny(s, " \t\n\"'$&|;<>()*?`!#") {
		// Prefer double quotes for strings that only contain single quotes
		// (typical remote scripts): same meaning, far easier to read.
		if strings.Contains(s, "'") && !strings.ContainsAny(s, "\"$`\\!") {
			return `"` + s + `"`
		}
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
	return s
}

// Shell builds a command that runs script through a shell. shell overrides
// the default ("sh -c", or "cmd /C" on Windows), e.g. "bash -c" or
// "pwsh -NoProfile -Command".
func Shell(shell, script string) Cmd {
	if fields := strings.Fields(shell); len(fields) > 0 {
		return Cmd{Name: fields[0], Args: append(fields[1:], script)}
	}
	if runtime.GOOS == "windows" {
		// /S plus outer quotes makes cmd strip exactly those quotes, so a
		// script that starts with a quoted path keeps its own quotes.
		return Cmd{Name: "cmd", Args: []string{"/S", "/C", `"` + script + `"`}, rawCmdLine: true}
	}
	return Cmd{Name: "sh", Args: []string{"-c", script}}
}

// Runner executes commands, writing their output to out.
type Runner interface {
	Run(ctx context.Context, c Cmd, out io.Writer) error
}

// Exec runs commands for real; with DryRun it only prints them.
type Exec struct {
	DryRun bool
}

func (e Exec) Run(ctx context.Context, c Cmd, out io.Writer) error {
	prefix := "$ "
	if c.Func != nil {
		prefix = "> "
	}
	fmt.Fprintf(out, "%s%s\n", prefix, c.String())
	if e.DryRun {
		return nil
	}
	if c.Func != nil {
		return c.Func(ctx, out)
	}
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.Stdout = out
	cmd.Stderr = out
	// On cancel only the direct child is killed; a grandchild holding the
	// output pipe must not keep Wait (and Ctrl-C / fail-fast) hanging.
	cmd.WaitDelay = 5 * time.Second
	if c.rawCmdLine {
		setRawCmdLine(cmd, c.Name+" "+strings.Join(c.Args, " "))
	}
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("%s saiu com codigo %d", c.Name, exitErr.ExitCode())
		}
		return fmt.Errorf("%s: %w", c.Name, err)
	}
	return nil
}

// Recorder is a Runner for tests: it records every command and fails the
// ones whose rendered form contains a key of Fail, the given number of times
// (a negative count fails forever).
type Recorder struct {
	mu   sync.Mutex
	Cmds []string
	Fail map[string]int
}

func (r *Recorder) Run(ctx context.Context, c Cmd, out io.Writer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := c.String()
	r.Cmds = append(r.Cmds, s)
	for key, times := range r.Fail {
		if strings.Contains(s, key) && times != 0 {
			r.Fail[key] = times - 1
			return fmt.Errorf("falha simulada em %q", key)
		}
	}
	return ctx.Err()
}

// Joined returns the recorded commands, one per line.
func (r *Recorder) Joined() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.Cmds, "\n")
}
