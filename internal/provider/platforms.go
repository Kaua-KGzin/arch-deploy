package provider

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Kaua-KGzin/arch-deploy/internal/config"
	"github.com/Kaua-KGzin/arch-deploy/internal/runner"
)

// Platform providers: one local command each, driving the platform's CLI.

func init() {
	register(&Provider{
		Name:     "vercel",
		Summary:  "Vercel (CLI `vercel`; token em VERCEL_TOKEN ou `vercel login`)",
		Requires: fixed("vercel"),
		Deploy: func(e Env, _ string) ([]runner.Cmd, error) {
			args := []string{"deploy", "--yes"}
			if config.Bool(e.T.Prod, true) {
				args = append(args, "--prod")
			}
			args = append(args, vercelAuth(&e)...)
			return []runner.Cmd{e.cmd("vercel", append(args, e.extra()...)...)}, nil
		},
		Rollback: func(e Env, _ string) ([]runner.Cmd, error) {
			args := append([]string{"rollback", "--yes"}, vercelAuth(&e)...)
			return []runner.Cmd{e.cmd("vercel", args...)}, nil
		},
	})

	register(&Provider{
		Name:     "netlify",
		Summary:  "Netlify (CLI `netlify`; token em NETLIFY_AUTH_TOKEN)",
		Requires: fixed("netlify"),
		Deploy: func(e Env, _ string) ([]runner.Cmd, error) {
			args := []string{"deploy", "--message", "arch-deploy " + e.Vars["version"]}
			if e.T.Output != "" {
				args = append(args, "--dir", e.path(e.T.Output))
			}
			if config.Bool(e.T.Prod, true) {
				args = append(args, "--prod")
			}
			if e.T.App != "" {
				args = append(args, "--site", e.T.App)
			}
			return []runner.Cmd{e.cmd("netlify", append(args, e.extra()...)...)}, nil
		},
	})

	register(&Provider{
		Name:     "cloudflare",
		Summary:  "Cloudflare Workers, ou Pages quando `output` e definido (CLI `wrangler`)",
		Requires: fixed("wrangler"),
		Validate: func(t *config.Target) error {
			if t.Output != "" {
				return require("app (nome do projeto Pages)", t.App)
			}
			return nil
		},
		Deploy: func(e Env, _ string) ([]runner.Cmd, error) {
			if e.T.Output == "" {
				return []runner.Cmd{e.cmd("wrangler", append([]string{"deploy"}, e.extra()...)...)}, nil
			}
			args := []string{"pages", "deploy", e.path(e.T.Output), "--project-name", e.T.App}
			if e.T.Branch != "" {
				args = append(args, "--branch", e.T.Branch)
			}
			return []runner.Cmd{e.cmd("wrangler", append(args, e.extra()...)...)}, nil
		},
	})

	register(&Provider{
		Name:     "fly",
		Summary:  "Fly.io (CLI `fly`; token em FLY_API_TOKEN)",
		Requires: fixed("fly"),
		Deploy: func(e Env, _ string) ([]runner.Cmd, error) {
			args := []string{"deploy"}
			if e.T.App != "" {
				args = append(args, "--app", e.T.App)
			}
			return []runner.Cmd{e.cmd("fly", append(args, e.extra()...)...)}, nil
		},
	})

	register(&Provider{
		Name:     "railway",
		Summary:  "Railway (CLI `railway`; token em RAILWAY_TOKEN)",
		Requires: fixed("railway"),
		Deploy: func(e Env, _ string) ([]runner.Cmd, error) {
			args := []string{"up", "--detach"}
			if e.T.App != "" {
				args = append(args, "--service", e.T.App)
			}
			return []runner.Cmd{e.cmd("railway", append(args, e.extra()...)...)}, nil
		},
	})

	register(&Provider{
		Name:     "firebase",
		Summary:  "Firebase Hosting/Functions (CLI `firebase`)",
		Requires: fixed("firebase"),
		Deploy: func(e Env, _ string) ([]runner.Cmd, error) {
			args := []string{"deploy", "--non-interactive", "--message", "arch-deploy " + e.Vars["version"]}
			if e.T.App != "" {
				args = append(args, "--project", e.T.App)
			}
			return []runner.Cmd{e.cmd("firebase", append(args, e.extra()...)...)}, nil
		},
	})

	register(&Provider{
		Name:     "kubernetes",
		Summary:  "Kubernetes: aplica manifests e/ou troca a imagem de um deployment (CLI `kubectl`)",
		Requires: fixed("kubectl"),
		Validate: func(t *config.Target) error {
			if len(t.Manifests) == 0 && t.App == "" {
				return errors.New("defina `manifests` e/ou `app` (nome do deployment)")
			}
			if t.Image != "" && t.App == "" {
				return errors.New("`image` precisa de `app` (nome do deployment)")
			}
			return nil
		},
		Deploy: func(e Env, _ string) ([]runner.Cmd, error) {
			base := kubeBase(e.T)
			var cmds []runner.Cmd
			for _, m := range e.T.Manifests {
				cmds = append(cmds, e.cmd("kubectl", append(base, "apply", "-f", e.path(m))...))
			}
			if e.T.Image != "" {
				cmds = append(cmds, e.cmd("kubectl", append(base, "set", "image", "deployment/"+e.T.App, "*="+e.Vars.Expand(e.T.Image))...))
			}
			if e.T.App != "" {
				cmds = append(cmds, e.cmd("kubectl", append(base, "rollout", "status", "deployment/"+e.T.App, "--timeout=5m")...))
			}
			return cmds, nil
		},
		Rollback: func(e Env, _ string) ([]runner.Cmd, error) {
			if e.T.App == "" {
				return nil, nil
			}
			base := kubeBase(e.T)
			return []runner.Cmd{
				e.cmd("kubectl", append(base, "rollout", "undo", "deployment/"+e.T.App)...),
				e.cmd("kubectl", append(base, "rollout", "status", "deployment/"+e.T.App, "--timeout=5m")...),
			}, nil
		},
	})

	register(&Provider{
		Name:     "git-push",
		Summary:  "git push para um remote que faz o deploy (Heroku, Dokku, Piku...)",
		Requires: fixed("git"),
		Validate: func(t *config.Target) error { return require("remote", t.Remote) },
		Deploy: func(e Env, _ string) ([]runner.Cmd, error) {
			branch := e.T.Branch
			if branch == "" {
				branch = "main"
			}
			args := []string{"push"}
			if e.T.Force {
				args = append(args, "--force")
			}
			args = append(args, e.T.Remote, "HEAD:"+branch)
			return []runner.Cmd{e.cmd("git", append(args, e.extra()...)...)}, nil
		},
	})

	register(&Provider{
		Name:     "github-release",
		Summary:  "Cria uma release no GitHub com os binarios/pacotes (CLI `gh`)",
		Requires: fixed("gh"),
		Deploy: func(e Env, _ string) ([]runner.Cmd, error) {
			tag := releaseTag(e)
			args := []string{"release", "create", tag}
			assets, err := expandAssets(e)
			if err != nil {
				return nil, err
			}
			args = append(args, assets...)
			title := e.Vars.Expand(e.T.Title)
			if title == "" {
				title = tag
			}
			args = append(args, "--title", title)
			if e.T.Notes != "" {
				args = append(args, "--notes", e.Vars.Expand(e.T.Notes))
			} else {
				args = append(args, "--generate-notes")
			}
			if e.T.Draft {
				args = append(args, "--draft")
			}
			return []runner.Cmd{e.cmd("gh", append(args, e.extra()...)...)}, nil
		},
		Rollback: func(e Env, _ string) ([]runner.Cmd, error) {
			return []runner.Cmd{e.cmd("gh", "release", "delete", releaseTag(e), "--yes", "--cleanup-tag")}, nil
		},
	})

	register(&Provider{
		Name:    "exec",
		Summary: "Qualquer coisa: roda os comandos de `command` em sequencia (shell local)",
		Validate: func(t *config.Target) error {
			if len(t.Command) == 0 {
				return errors.New("campo `command` (lista de comandos) e obrigatorio")
			}
			return nil
		},
		Deploy: func(e Env, _ string) ([]runner.Cmd, error) {
			cmds := make([]runner.Cmd, 0, len(e.T.Command))
			for _, c := range e.T.Command {
				cmds = append(cmds, e.Shell(c))
			}
			return cmds, nil
		},
	})
}

func vercelAuth(e *Env) []string {
	var args []string
	if e.T.Scope != "" {
		args = append(args, "--scope", e.T.Scope)
	}
	if token := os.Getenv("VERCEL_TOKEN"); token != "" {
		args = append(args, "--token", token)
		e.Secret = append(e.Secret, token)
	}
	return args
}

func kubeBase(t *config.Target) []string {
	var base []string
	if t.Context != "" {
		base = append(base, "--context", t.Context)
	}
	if t.Namespace != "" {
		base = append(base, "--namespace", t.Namespace)
	}
	return base
}

func releaseTag(e Env) string {
	if e.T.Tag != "" {
		return e.Vars.Expand(e.T.Tag)
	}
	return "v" + e.Vars["version"]
}

// expandAssets resolves asset globs here, since Windows shells do not.
func expandAssets(e Env) ([]string, error) {
	var out []string
	for _, pattern := range e.T.Assets {
		full := e.path(pattern)
		matches, err := filepath.Glob(full)
		if err != nil {
			return nil, fmt.Errorf("asset %q: %w", pattern, err)
		}
		if len(matches) == 0 {
			// Assets are usually built by `before`, so in a plan they may not
			// exist yet: keep the pattern visible and let gh report it.
			out = append(out, full)
			continue
		}
		out = append(out, matches...)
	}
	return out, nil
}
