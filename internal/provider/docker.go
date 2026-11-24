package provider

import (
	"sort"

	"github.com/Kaua-KGzin/arch-deploy/internal/config"
	"github.com/Kaua-KGzin/arch-deploy/internal/runner"
)

func init() {
	register(&Provider{
		Name:     "docker",
		Summary:  "Builda a imagem Docker, aplica as tags e faz push para o registry",
		Requires: fixed("docker"),
		Deploy: func(e Env, _ string) ([]runner.Cmd, error) {
			image := e.Vars.Expand(e.T.Image)
			if image == "" {
				image = e.Cfg.Project
			}
			tags := e.Vars.ExpandAll(e.T.Tags)
			if len(tags) == 0 {
				tags = []string{e.Vars["version"], "latest"}
			}
			refs := make([]string, len(tags))
			for i, tag := range tags {
				refs[i] = image + ":" + tag
			}

			args := []string{"build"}
			for _, ref := range refs {
				args = append(args, "-t", ref)
			}
			if e.T.Dockerfile != "" {
				args = append(args, "-f", e.path(e.T.Dockerfile))
			}
			if e.T.Platform != "" {
				args = append(args, "--platform", e.T.Platform)
			}
			keys := make([]string, 0, len(e.T.BuildArgs))
			for k := range e.T.BuildArgs {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				args = append(args, "--build-arg", k+"="+e.Vars.Expand(e.T.BuildArgs[k]))
			}
			args = append(args, e.extra()...)
			args = append(args, ".")

			cmds := []runner.Cmd{e.cmd("docker", args...)}
			if config.Bool(e.T.Push, true) {
				for _, ref := range refs {
					cmds = append(cmds, e.cmd("docker", "push", ref))
				}
			}
			return cmds, nil
		},
	})

	register(&Provider{
		Name:     "compose",
		Summary:  "docker compose up -d --build, local ou remoto via `context` (docker context / ssh)",
		Requires: fixed("docker"),
		Deploy: func(e Env, _ string) ([]runner.Cmd, error) {
			base := []string{}
			if e.T.Context != "" {
				base = append(base, "--context", e.T.Context)
			}
			base = append(base, "compose")
			if e.T.File != "" {
				base = append(base, "-f", e.path(e.T.File))
			}
			if e.T.App != "" {
				base = append(base, "-p", e.T.App)
			}
			var cmds []runner.Cmd
			if e.T.Pull {
				cmds = append(cmds, e.cmd("docker", append(append([]string{}, base...), "pull")...))
			}
			up := append(append([]string{}, base...), "up", "-d", "--build", "--remove-orphans")
			cmds = append(cmds, e.cmd("docker", append(up, e.extra()...)...))
			return cmds, nil
		},
	})
}
