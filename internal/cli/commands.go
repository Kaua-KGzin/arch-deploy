package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Kaua-KGzin/arch-deploy/internal/config"
	"github.com/Kaua-KGzin/arch-deploy/internal/detect"
	"github.com/Kaua-KGzin/arch-deploy/internal/history"
	"github.com/Kaua-KGzin/arch-deploy/internal/hub"
	"github.com/Kaua-KGzin/arch-deploy/internal/provider"
)

func (a *app) initCommand() *cobra.Command {
	var force, stdout bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Detecta o tipo de projeto e gera o arch-deploy.yaml",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			suggestions := detect.Detect(dir)
			cfg := &config.Config{Project: strings.ToLower(filepath.Base(dir)), Targets: map[string]*config.Target{}}
			for _, s := range suggestions {
				cfg.Targets[s.Name] = s.Target
			}
			body, err := config.Marshal(cfg)
			if err != nil {
				return err
			}
			var header strings.Builder
			header.WriteString("# arch-deploy: gerado por `arch-deploy init`. Detectado:\n")
			for _, s := range suggestions {
				fmt.Fprintf(&header, "#   %-12s %-15s %s\n", s.Name, s.Target.Provider, s.Reason)
			}
			header.WriteString(initFooter)
			content := header.String() + "\n" + string(body)

			if stdout {
				fmt.Fprint(a.out, content)
				return nil
			}
			path := filepath.Join(dir, config.DefaultFiles[0])
			if a.configPath != "" {
				path = a.configPath
			}
			if _, err := os.Stat(path); err == nil && !force {
				return fmt.Errorf("%s ja existe (use --force para sobrescrever ou --stdout para so ver)", path)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return err
			}
			ensureGitignore(dir)
			fmt.Fprintf(a.out, "%s criado com %d target(s):\n", filepath.Base(path), len(suggestions))
			for _, s := range suggestions {
				fmt.Fprintf(a.out, "  %-12s %-15s %s\n", s.Name, s.Target.Provider, s.Reason)
			}
			fmt.Fprintln(a.out, "\nProximos passos: arch-deploy doctor  ->  arch-deploy plan <target>  ->  arch-deploy deploy <target>")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "sobrescreve um arch-deploy.yaml existente")
	cmd.Flags().BoolVar(&stdout, "stdout", false, "so imprime a configuracao sugerida")
	return cmd
}

const initFooter = `#
# Campos comuns a todo target: dir, output, env, before, after, rollback,
# healthcheck {url, expect, timeout}, confirm, auto_rollback, retries,
# retry_delay, max_parallel, fail_fast, args.
# Variaveis: {{version}} {{commit}} {{branch}} {{release}} {{target}} {{host}}
# {{project}} {{exe}}; e ${VAR} / ${VAR:-padrao} vindas do ambiente.
# Lista de providers: arch-deploy providers
#
# Exemplo de servidor proprio (descomente e ajuste):
#   vps:
#     provider: ssh
#     hosts: [app-01.exemplo.com, app-02.exemplo.com]
#     user: deploy
#     path: /srv/app
#     output: dist
#     run: ["sudo systemctl restart app"]
#     healthcheck: {url: "https://app.exemplo.com/health"}
#     auto_rollback: true
#
# Aviso ao Arch Hub depois de cada deploy (ou defina ARCHHUB_URL):
#   hub: {url: "${ARCHHUB_URL}", system: "meu-sistema"}
`

func ensureGitignore(dir string) {
	path := filepath.Join(dir, ".gitignore")
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return
	}
	if strings.Contains(string(raw), ".arch-deploy") {
		return
	}
	prefix := ""
	if len(raw) > 0 && !strings.HasSuffix(string(raw), "\n") {
		prefix = "\n"
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s# arch-deploy (historico e estado local)\n.arch-deploy/\n", prefix)
}

func (a *app) targetsCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "targets",
		Aliases: []string{"status", "ls"},
		Short:   "Lista os targets, o provider e o ultimo deploy de cada um",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.load()
			if err != nil {
				return err
			}
			entries, _ := history.Read(cfg.Dir)
			last := map[string]history.Entry{}
			for _, e := range entries {
				last[e.Target] = e
			}
			w := tabwriter.NewWriter(a.out, 0, 2, 2, ' ', 0)
			fmt.Fprintf(w, "TARGET\tPROVIDER\tUNIDADES\tULTIMO DEPLOY\n")
			for _, name := range cfg.TargetNames() {
				t := cfg.Targets[name]
				p, _ := provider.Get(t.Provider)
				units := strings.Join(p.UnitsOf(t), ", ")
				lastDeploy := "-"
				if e, ok := last[name]; ok {
					state := "ok"
					if !e.OK {
						state = "falhou"
					}
					lastDeploy = fmt.Sprintf("%s %s %s (%s)", e.Action, e.Version, state, since(e.Time))
				}
				flags := ""
				if t.Confirm {
					flags = " [confirm]"
				}
				fmt.Fprintf(w, "%s%s\t%s\t%s\t%s\n", name, flags, t.Provider, units, lastDeploy)
			}
			return w.Flush()
		},
	}
}

func (a *app) doctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Confere se as ferramentas e a configuracao estao prontas",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.load()
			if err != nil {
				fmt.Fprintf(a.out, "x configuracao: %v\n", err)
				return errors.New("doctor encontrou problemas")
			}
			fmt.Fprintf(a.out, "ok configuracao %s (%d targets)\n", cfg.Path, len(cfg.Targets))
			problems := 0
			if _, err := exec.LookPath("git"); err != nil {
				fmt.Fprintln(a.out, "!  git nao encontrado: {{version}} e {{commit}} usam data/hora")
			}
			checked := map[string]bool{}
			for _, name := range cfg.TargetNames() {
				t := cfg.Targets[name]
				p, _ := provider.Get(t.Provider)
				for _, bin := range p.Requires(t) {
					if checked[bin] {
						continue
					}
					checked[bin] = true
					if path, err := exec.LookPath(bin); err == nil {
						fmt.Fprintf(a.out, "ok %-10s %s\n", bin, path)
					} else {
						problems++
						fmt.Fprintf(a.out, "x  %-10s nao encontrado no PATH (necessario para %q: %s)\n", bin, name, installHint(bin))
					}
				}
			}
			if settings, ok := hub.Resolve(cfg); ok {
				fmt.Fprintf(a.out, "ok Arch Hub %s (sistema %q)\n", settings.URL, settings.System)
			} else {
				fmt.Fprintln(a.out, "-  Arch Hub desligado (defina hub.url ou ARCHHUB_URL para avisar o Hub)")
			}
			if problems > 0 {
				return fmt.Errorf("doctor encontrou %d problema(s)", problems)
			}
			fmt.Fprintln(a.out, "tudo pronto")
			return nil
		},
	}
}

func installHint(bin string) string {
	hints := map[string]string{
		"vercel":   "npm i -g vercel",
		"netlify":  "npm i -g netlify-cli",
		"wrangler": "npm i -g wrangler",
		"firebase": "npm i -g firebase-tools",
		"railway":  "npm i -g @railway/cli",
		"fly":      "https://fly.io/docs/flyctl/install/",
		"gh":       "https://cli.github.com",
		"docker":   "https://docs.docker.com/get-docker/",
		"kubectl":  "https://kubernetes.io/docs/tasks/tools/",
		"ssh":      "instale o OpenSSH client",
		"scp":      "instale o OpenSSH client",
		"git":      "https://git-scm.com",
	}
	if h, ok := hints[bin]; ok {
		return h
	}
	return "instale e coloque no PATH"
}

func (a *app) historyCommand() *cobra.Command {
	var limit int
	var target string
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Mostra os ultimos deploys e rollbacks",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.load()
			if err != nil {
				return err
			}
			entries, err := history.Read(cfg.Dir)
			if err != nil {
				return err
			}
			var filtered []history.Entry
			for _, e := range entries {
				if target == "" || e.Target == target {
					filtered = append(filtered, e)
				}
			}
			if len(filtered) == 0 {
				fmt.Fprintln(a.out, "nenhum deploy registrado ainda")
				return nil
			}
			if limit > 0 && len(filtered) > limit {
				filtered = filtered[len(filtered)-limit:]
			}
			w := tabwriter.NewWriter(a.out, 0, 2, 2, ' ', 0)
			fmt.Fprintf(w, "QUANDO\tTARGET\tACAO\tVERSAO\tRESULTADO\tDURACAO\n")
			for i := len(filtered) - 1; i >= 0; i-- {
				e := filtered[i]
				result := "ok"
				if !e.OK {
					result = "falhou: " + e.Error
				}
				if e.RolledBack {
					result += " (rollback automatico)"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", e.Time.Local().Format("2006-01-02 15:04"), e.Target, e.Action, e.Version, result, (time.Duration(e.DurationMS) * time.Millisecond).Round(100*time.Millisecond))
			}
			return w.Flush()
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "quantos registros mostrar")
	cmd.Flags().StringVarP(&target, "target", "t", "", "filtra por target")
	return cmd
}

func (a *app) validateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Valida o arch-deploy.yaml",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.load()
			if err != nil {
				return err
			}
			fmt.Fprintf(a.out, "%s valido: projeto %q, targets %s\n", filepath.Base(cfg.Path), cfg.Project, strings.Join(cfg.TargetNames(), ", "))
			return nil
		},
	}
}

func (a *app) providersCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "providers",
		Short: "Lista os providers suportados",
		RunE: func(cmd *cobra.Command, args []string) error {
			w := tabwriter.NewWriter(a.out, 0, 2, 2, ' ', 0)
			for _, name := range provider.Names() {
				p, _ := provider.Get(name)
				fmt.Fprintf(w, "%s\t%s\n", name, p.Summary)
			}
			return w.Flush()
		},
	}
}

func since(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "agora"
	case d < time.Hour:
		return fmt.Sprintf("ha %dmin", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("ha %dh", int(d.Hours()))
	default:
		return fmt.Sprintf("ha %dd", int(d.Hours()/24))
	}
}
