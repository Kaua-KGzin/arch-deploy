// Package provider turns a target into the commands that deploy it.
//
// arch-deploy does not reimplement any platform API: each provider drives the
// tool that platform already ships (vercel, docker, gh, git, ssh, kubectl...).
// That keeps credentials where the user already has them (each CLI's own
// login or token variable) and makes every deploy reproducible by hand: the
// plan shows exactly the commands that will run.
package provider

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Kaua-KGzin/arch-deploy/internal/config"
	"github.com/Kaua-KGzin/arch-deploy/internal/runner"
	"github.com/Kaua-KGzin/arch-deploy/internal/vars"
)

// Local is the unit name of providers that deploy once, from this machine.
const Local = "local"

// Env is everything a provider needs to build commands for one unit.
type Env struct {
	Cfg    *config.Config
	Name   string // target name
	T      *config.Target
	Vars   vars.Vars
	Dir    string // absolute working dir of the target
	Secret []string
}

type Provider struct {
	Name    string
	Summary string

	// Requires lists the executables the target needs on PATH.
	Requires func(t *config.Target) []string
	// Validate checks the provider-specific fields.
	Validate func(t *config.Target) error
	// Units splits a target into independently deployed parts (one per ssh
	// host). Nil means a single Local unit.
	Units func(t *config.Target) []string
	// Deploy returns the commands that deploy one unit.
	Deploy func(e Env, unit string) ([]runner.Cmd, error)
	// Rollback returns the commands that undo the last deploy of one unit.
	// Nil means the platform has no rollback; the target's `rollback` hooks
	// still run.
	Rollback func(e Env, unit string) ([]runner.Cmd, error)
	// IdempotentRollback means Rollback is safe on a unit whose deploy failed
	// part-way (or never started), so auto_rollback can run it everywhere.
	// During an auto rollback Vars["undo"] names the release to undo.
	IdempotentRollback bool
}

var registry = map[string]*Provider{}

func register(p *Provider) {
	if p.Requires == nil {
		p.Requires = func(*config.Target) []string { return nil }
	}
	if p.Validate == nil {
		p.Validate = func(*config.Target) error { return nil }
	}
	registry[p.Name] = p
}

// Get returns a provider by name.
func Get(name string) (*Provider, error) {
	if p, ok := registry[name]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("provider desconhecido %q (disponiveis: %s)", name, strings.Join(Names(), ", "))
}

// Names lists the registered providers, sorted.
func Names() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// UnitsOf returns the units of a target.
func (p *Provider) UnitsOf(t *config.Target) []string {
	if p.Units == nil {
		return []string{Local}
	}
	return p.Units(t)
}

// ValidateConfig checks every target against its provider.
func ValidateConfig(cfg *config.Config) error {
	var problems []string
	for _, name := range cfg.TargetNames() {
		t := cfg.Targets[name]
		p, err := Get(t.Provider)
		if err != nil {
			problems = append(problems, fmt.Sprintf("target %q: %v", name, err))
			continue
		}
		if err := p.Validate(t); err != nil {
			problems = append(problems, fmt.Sprintf("target %q (%s): %v", name, t.Provider, err))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("configuracao invalida:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

// NewEnv builds the Env of one target.
func NewEnv(cfg *config.Config, name string, v vars.Vars) Env {
	t := cfg.Targets[name]
	secrets := append([]string(nil), cfg.Secrets...)
	return Env{Cfg: cfg, Name: name, T: t, Vars: v, Dir: cfg.WorkDir(t), Secret: secrets}
}

// cmd builds a local command that runs in the target dir with its env.
func (e Env) cmd(name string, args ...string) runner.Cmd {
	return runner.Cmd{Name: name, Args: args, Dir: e.Dir, Env: e.envList(), Secrets: e.Secret}
}

// Shell builds a local shell command for a hook.
func (e Env) Shell(script string) runner.Cmd {
	c := runner.Shell(e.Cfg.Shell, e.Vars.Expand(script))
	c.Dir = e.Dir
	c.Env = e.envList()
	c.Secrets = e.Secret
	return c
}

func (e Env) envList() []string {
	if len(e.T.Env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(e.T.Env))
	for k := range e.T.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+e.Vars.Expand(e.T.Env[k]))
	}
	return out
}

// path resolves a path from the config relative to the target dir.
func (e Env) path(p string) string {
	p = e.Vars.Expand(p)
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(e.Dir, p)
}

// extra returns the target's `args`, expanded.
func (e Env) extra() []string {
	return e.Vars.ExpandAll(e.T.Args)
}

func require(field string, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("campo `%s` e obrigatorio", field)
	}
	return nil
}

func fixed(names ...string) func(*config.Target) []string {
	return func(*config.Target) []string { return names }
}
