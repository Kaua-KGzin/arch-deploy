// Package config loads arch-deploy.yaml.
//
// A config names a project and a set of targets (production, staging,
// preview...). Each target picks a provider (vercel, ssh, docker...) and the
// options that provider needs, plus hooks, health check and retry policy that
// work the same for every provider.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultFiles are looked up, in order, when no --config is given.
var DefaultFiles = []string{"arch-deploy.yaml", "arch-deploy.yml"}

type Config struct {
	Project string             `yaml:"project"`
	Shell   string             `yaml:"shell,omitempty"`
	Hub     Hub                `yaml:"hub,omitempty"`
	Targets map[string]*Target `yaml:"targets"`

	// Path is the file the config came from; Dir is its folder, which every
	// relative path in the config is resolved against.
	Path string `yaml:"-"`
	Dir  string `yaml:"-"`
	// Secrets are the values of secret-looking ${VARS} (TOKEN, SECRET, KEY,
	// PASSWORD, HOOK, AUTH...) that were expanded; they are masked in output.
	Secrets []string `yaml:"-"`
}

// Hub points at an Arch Hub that gets a heartbeat after each successful
// deploy. Empty fields fall back to ARCHHUB_URL / ARCHHUB_TOKEN and the
// project name.
type Hub struct {
	URL    string `yaml:"url,omitempty"`
	System string `yaml:"system,omitempty"`
	Token  string `yaml:"token,omitempty"`
}

type Healthcheck struct {
	URL      string        `yaml:"url"`
	Expect   int           `yaml:"expect,omitempty"`
	Timeout  time.Duration `yaml:"timeout,omitempty"`
	Interval time.Duration `yaml:"interval,omitempty"`
}

type Target struct {
	Provider    string `yaml:"provider"`
	Description string `yaml:"description,omitempty"`

	// Common to every provider.
	Dir          string            `yaml:"dir,omitempty"`
	Output       string            `yaml:"output,omitempty"`
	Env          map[string]string `yaml:"env,omitempty"`
	Before       []string          `yaml:"before,omitempty"`
	After        []string          `yaml:"after,omitempty"`
	Rollback     []string          `yaml:"rollback,omitempty"`
	Healthcheck  *Healthcheck      `yaml:"healthcheck,omitempty"`
	Confirm      bool              `yaml:"confirm,omitempty"`
	AutoRollback bool              `yaml:"auto_rollback,omitempty"`
	Retries      int               `yaml:"retries,omitempty"`
	RetryDelay   time.Duration     `yaml:"retry_delay,omitempty"`
	MaxParallel  int               `yaml:"max_parallel,omitempty"`
	FailFast     *bool             `yaml:"fail_fast,omitempty"`
	Args         []string          `yaml:"args,omitempty"`

	// Platform CLIs (vercel, netlify, cloudflare, fly, railway, firebase, kubernetes).
	Prod      *bool    `yaml:"prod,omitempty"`
	App       string   `yaml:"app,omitempty"`
	Scope     string   `yaml:"scope,omitempty"`
	Context   string   `yaml:"context,omitempty"`
	Namespace string   `yaml:"namespace,omitempty"`
	Manifests []string `yaml:"manifests,omitempty"`

	// compose
	File string `yaml:"file,omitempty"`
	Pull bool   `yaml:"pull,omitempty"`

	// webhook
	Hook   string `yaml:"hook,omitempty"`
	Method string `yaml:"method,omitempty"`

	// git-push, gh-pages
	Remote string `yaml:"remote,omitempty"`
	Branch string `yaml:"branch,omitempty"`
	Force  bool   `yaml:"force,omitempty"`

	// docker
	Image      string            `yaml:"image,omitempty"`
	Tags       []string          `yaml:"tags,omitempty"`
	Dockerfile string            `yaml:"dockerfile,omitempty"`
	Platform   string            `yaml:"platform,omitempty"`
	Push       *bool             `yaml:"push,omitempty"`
	BuildArgs  map[string]string `yaml:"build_args,omitempty"`

	// github-release
	Tag    string   `yaml:"tag,omitempty"`
	Assets []string `yaml:"assets,omitempty"`
	Title  string   `yaml:"title,omitempty"`
	Notes  string   `yaml:"notes,omitempty"`
	Draft  bool     `yaml:"draft,omitempty"`

	// ssh
	Hosts    []string `yaml:"hosts,omitempty"`
	User     string   `yaml:"user,omitempty"`
	Port     int      `yaml:"port,omitempty"`
	Identity string   `yaml:"identity,omitempty"`
	Path     string   `yaml:"path,omitempty"`
	Keep     int      `yaml:"keep,omitempty"`
	Run      []string `yaml:"run,omitempty"`

	// exec
	Command []string `yaml:"command,omitempty"`
}

// Bool returns *p, or def when p is unset.
func Bool(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// Find returns the config file to use: path itself, or the first default
// file that exists in dir.
func Find(path, dir string) (string, error) {
	if path != "" {
		return path, nil
	}
	for _, name := range DefaultFiles {
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", errors.New("nenhum arch-deploy.yaml encontrado (rode `arch-deploy init` para gerar um)")
}

// Load reads and parses a config file.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("lendo %s: %w", path, err)
	}
	cfg, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	cfg.Path = abs
	cfg.Dir = filepath.Dir(abs)
	if cfg.Project == "" {
		cfg.Project = filepath.Base(cfg.Dir)
	}
	return cfg, nil
}

// Parse decodes a config, expanding ${VAR} and ${VAR:-default} from the
// environment first. Plain $VAR is left alone so shell commands (local or
// remote) keep their own variables.
func Parse(raw []byte) (*Config, error) {
	expanded, secrets := expandEnv(string(raw))
	dec := yaml.NewDecoder(bytes.NewReader([]byte(expanded)))
	dec.KnownFields(true)
	var cfg Config
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("yaml invalido: %w", err)
	}
	cfg.Secrets = secrets
	if len(cfg.Targets) == 0 {
		return nil, errors.New("nenhum target definido em `targets`")
	}
	for name, t := range cfg.Targets {
		if t == nil {
			return nil, fmt.Errorf("target %q esta vazio", name)
		}
		if t.Provider == "" {
			return nil, fmt.Errorf("target %q: campo `provider` e obrigatorio", name)
		}
		if t.Retries < 0 {
			return nil, fmt.Errorf("target %q: `retries` nao pode ser negativo", name)
		}
		if t.Healthcheck != nil && t.Healthcheck.URL == "" {
			return nil, fmt.Errorf("target %q: `healthcheck.url` e obrigatorio", name)
		}
	}
	return &cfg, nil
}

var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}`)

var secretName = regexp.MustCompile(`(?i)token|secret|key|pass|hook|auth|credential|private`)

// ExpandEnv replaces ${VAR} and ${VAR:-default}.
func ExpandEnv(s string) string {
	out, _ := expandEnv(s)
	return out
}

func expandEnv(s string) (string, []string) {
	var secrets []string
	out := envPattern.ReplaceAllStringFunc(s, func(match string) string {
		m := envPattern.FindStringSubmatch(match)
		value := m[3]
		if v, ok := os.LookupEnv(m[1]); ok && v != "" {
			value = v
		}
		if secretName.MatchString(m[1]) && len(value) >= 4 {
			secrets = append(secrets, value)
		}
		return value
	})
	return out, secrets
}

// TargetNames returns the target names in a stable order.
func (c *Config) TargetNames() []string {
	names := make([]string, 0, len(c.Targets))
	for name := range c.Targets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// WorkDir is the absolute folder a target's commands run in.
func (c *Config) WorkDir(t *Target) string {
	if t.Dir == "" {
		return c.Dir
	}
	if filepath.IsAbs(t.Dir) {
		return t.Dir
	}
	return filepath.Join(c.Dir, t.Dir)
}

// Marshal renders a config as YAML.
func Marshal(c *Config) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
