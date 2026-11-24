// Package vars holds the {{placeholders}} available in a config: version,
// commit, branch, release, target, host, project and exe.
package vars

import (
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type Vars map[string]string

// New collects the variables for one deploy of target, reading git metadata
// from dir when it is a repository.
func New(project, target, dir string, now time.Time) Vars {
	commit := git(dir, "rev-parse", "--short", "HEAD")
	branch := git(dir, "rev-parse", "--abbrev-ref", "HEAD")
	version := git(dir, "describe", "--tags", "--always", "--dirty")
	stamp := now.UTC().Format("20060102-150405")
	if version == "" {
		version = stamp
	}
	release := stamp
	if commit != "" {
		release += "-" + commit
	}
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	return Vars{
		"project": project,
		"target":  target,
		"version": strings.TrimPrefix(version, "v"),
		"commit":  commit,
		"branch":  branch,
		"release": release,
		"exe":     exe,
		"host":    "",
	}
}

func git(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// With returns a copy with key set to value.
func (v Vars) With(key, value string) Vars {
	out := make(Vars, len(v)+1)
	for k, val := range v {
		out[k] = val
	}
	out[key] = value
	return out
}

// Expand replaces every {{name}} in s.
func (v Vars) Expand(s string) string {
	if !strings.Contains(s, "{{") {
		return s
	}
	pairs := make([]string, 0, len(v)*2)
	for k, val := range v {
		pairs = append(pairs, "{{"+k+"}}", val)
	}
	return strings.NewReplacer(pairs...).Replace(s)
}

// ExpandAll expands every element of list.
func (v Vars) ExpandAll(list []string) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = v.Expand(s)
	}
	return out
}
