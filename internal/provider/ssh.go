package provider

import (
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/Kaua-KGzin/arch-deploy/internal/config"
	"github.com/Kaua-KGzin/arch-deploy/internal/runner"
)

// ssh deploys to your own servers with the system ssh/scp. With `output` it
// uploads a release per deploy and keeps the last `keep` of them:
//
//	<path>/releases/<release>/   one folder per deploy
//	<path>/current -> releases/<release>
//
// so a rollback just points `current` back at the previous release.
func init() {
	register(&Provider{
		Name:    "ssh",
		Summary: "Servidores proprios via ssh/scp: releases versionadas, `current` por symlink e rollback",
		Requires: func(t *config.Target) []string {
			if t.Output != "" {
				return []string{"ssh", "scp"}
			}
			return []string{"ssh"}
		},
		Validate: func(t *config.Target) error {
			if len(t.Hosts) == 0 {
				return errors.New("campo `hosts` e obrigatorio")
			}
			if t.Output == "" && len(t.Run) == 0 {
				return errors.New("defina `output` (o que enviar) e/ou `run` (comandos remotos)")
			}
			if t.Output != "" {
				if err := require("path (pasta remota)", t.Path); err != nil {
					return err
				}
				if !path.IsAbs(t.Path) && !strings.HasPrefix(t.Path, "~") {
					return errors.New("`path` precisa ser absoluto no servidor (ex.: /srv/app)")
				}
			}
			return nil
		},
		Units: func(t *config.Target) []string { return t.Hosts },
		Deploy: func(e Env, host string) ([]runner.Cmd, error) {
			s := sshTarget{e: e, host: host}
			var cmds []runner.Cmd
			if e.T.Output == "" {
				if len(e.T.Run) > 0 {
					cmds = append(cmds, s.remote(s.runScript(e.T.Path)))
				}
				return cmds, nil
			}
			root := strings.TrimRight(e.T.Path, "/")
			release := root + "/releases/" + e.Vars["release"]
			upload := root + "/releases/.upload-" + e.Vars["release"]
			cmds = append(cmds,
				s.remote("mkdir -p "+shq(root+"/releases")),
				s.copy(e.path(e.T.Output), upload),
				s.remote(fmt.Sprintf("mv %s %s && ln -sfn %s %s", shq(upload), shq(release), shq(release), shq(root+"/current"))),
			)
			if len(e.T.Run) > 0 {
				cmds = append(cmds, s.remote(s.runScript(root+"/current")))
			}
			keep := e.T.Keep
			if keep <= 0 {
				keep = 5
			}
			// Only folders named like a release are counted and pruned, so
			// anything else kept in releases/ by hand is left alone.
			cmds = append(cmds, s.remote(fmt.Sprintf("cd %s && %s | tail -n +%d | xargs -r rm -rf", shq(root+"/releases"), listReleases, keep+1)))
			return cmds, nil
		},
		IdempotentRollback: true,
		Rollback: func(e Env, host string) ([]runner.Cmd, error) {
			if e.T.Output == "" {
				return nil, nil
			}
			s := sshTarget{e: e, host: host}
			root := strings.TrimRight(e.T.Path, "/")
			releases := shq(root + "/releases")
			current := shq(root + "/current")
			// Manual rollback undoes whatever is live. An auto rollback undoes
			// only this run's release: on a host where it never went live,
			// the leftovers are removed and nothing is switched.
			target := `"$cur"`
			if undo := e.Vars["undo"]; undo != "" {
				target = shq(undo)
			}
			restart := ""
			if len(e.T.Run) > 0 {
				restart = " && " + s.runScript(root+"/current")
			}
			script := fmt.Sprintf(`cd %[1]s && cur=$(basename "$(readlink %[2]s)" 2>/dev/null) && target=%[3]s && `+
				`if [ -z "$target" ] || [ "$target" = "." ]; then echo "nenhuma release ativa em current" >&2; exit 1; fi && `+
				`if [ "$cur" != "$target" ]; then rm -rf "./$target" "./.upload-$target"; echo "$target nao estava ativa: nada a desfazer"; exit 0; fi && `+
				`prev=$(%[4]s | grep -vx "$target" | head -n 1) && `+
				`if [ -z "$prev" ]; then echo "sem release anterior para voltar" >&2; exit 1; fi && `+
				`ln -sfn %[1]s/"$prev" %[2]s && rm -rf "./$target" && echo "current -> $prev"%[5]s`,
				releases, current, target, listReleases, restart)
			return []runner.Cmd{s.remote(script)}, nil
		},
	})
}

// listReleases prints the release folders, newest first.
const listReleases = `ls -1 | grep -E '^[0-9]{8}-[0-9]{6}' | sort -r`

type sshTarget struct {
	e    Env
	host string
}

func (s sshTarget) dest() string {
	host := s.e.Vars.Expand(s.host)
	if s.e.T.User != "" && !strings.Contains(host, "@") {
		return s.e.T.User + "@" + host
	}
	return host
}

func (s sshTarget) options(portFlag string) []string {
	opts := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=15"}
	if s.e.T.Port > 0 {
		opts = append(opts, portFlag, strconv.Itoa(s.e.T.Port))
	}
	if s.e.T.Identity != "" {
		opts = append(opts, "-i", s.e.path(s.e.T.Identity))
	}
	return opts
}

func (s sshTarget) remote(script string) runner.Cmd {
	args := append(s.options("-p"), s.dest(), script)
	return s.e.cmd("ssh", args...)
}

func (s sshTarget) copy(local, remote string) runner.Cmd {
	args := append(s.options("-P"), "-r", local, s.dest()+":"+remote)
	return s.e.cmd("scp", args...)
}

func (s sshTarget) runScript(dir string) string {
	steps := s.e.Vars.With("host", s.host).ExpandAll(s.e.T.Run)
	script := strings.Join(steps, " && ")
	if dir != "" {
		script = "cd " + shq(strings.TrimRight(dir, "/")) + " && " + script
	}
	return script
}

// shq single-quotes s for a POSIX shell, leaving a leading ~/ expandable.
func shq(s string) string {
	if strings.HasPrefix(s, "~/") {
		return `~/` + shq(s[2:])
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
