// Package cli wires the arch-deploy commands.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Kaua-KGzin/arch-deploy/internal/config"
	"github.com/Kaua-KGzin/arch-deploy/internal/engine"
	"github.com/Kaua-KGzin/arch-deploy/internal/history"
	"github.com/Kaua-KGzin/arch-deploy/internal/hub"
	"github.com/Kaua-KGzin/arch-deploy/internal/provider"
)

// Version is set at build time with -ldflags "-X .../internal/cli.Version=x.y.z".
var Version = "dev"

type app struct {
	configPath string
	out        io.Writer
	in         io.Reader

	dryRun      bool
	yes         bool
	all         bool
	retries     int
	maxParallel int
	noHub       bool
}

func NewRootCommand() *cobra.Command {
	return newRoot(os.Stdout, os.Stdin)
}

func newRoot(out io.Writer, in io.Reader) *cobra.Command {
	a := &app{out: out, in: in}

	root := &cobra.Command{
		Use:   "arch-deploy",
		Short: "Deploy de qualquer projeto, em qualquer lugar, com um comando",
		Long: `arch-deploy faz o deploy de qualquer projeto: Vercel, Netlify, Cloudflare, Fly.io,
Railway, Firebase, GitHub Pages, releases do GitHub, Docker, docker compose,
Kubernetes, servidores proprios via SSH, deploy hooks ou qualquer comando seu.

Ele usa as ferramentas que voce ja tem (vercel, docker, gh, git, ssh, kubectl...)
e cuida do resto: plano antes de executar, hooks, varios servidores em paralelo,
retry, health check, rollback, historico e aviso ao Arch Hub.

Comece com:  arch-deploy init`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       Version,
	}
	root.SetOut(a.out)
	root.PersistentFlags().StringVarP(&a.configPath, "config", "c", "", "arquivo de configuracao (padrao: arch-deploy.yaml)")

	root.AddCommand(
		a.initCommand(),
		a.deployCommand(),
		a.planCommand(),
		a.rollbackCommand(),
		a.targetsCommand(),
		a.doctorCommand(),
		a.historyCommand(),
		a.validateCommand(),
		a.providersCommand(),
	)
	return root
}

func (a *app) load() (*config.Config, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	path, err := config.Find(a.configPath, cwd)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	if err := provider.ValidateConfig(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (a *app) deployCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy [target...]",
		Short: "Faz o deploy de um ou mais targets",
		Example: `  arch-deploy deploy production
  arch-deploy deploy staging production --yes
  arch-deploy deploy --all --dry-run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runDeploy(args, a.dryRun)
		},
	}
	cmd.Flags().BoolVar(&a.dryRun, "dry-run", false, "mostra os comandos sem executar")
	a.addRunFlags(cmd)
	cmd.Flags().BoolVar(&a.all, "all", false, "todos os targets")
	return cmd
}

func (a *app) planCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan [target...]",
		Short: "Mostra exatamente o que o deploy vai executar, sem executar",
		RunE: func(cmd *cobra.Command, args []string) error {
			a.yes = true
			return a.runDeploy(args, true)
		},
	}
	cmd.Flags().BoolVar(&a.all, "all", false, "todos os targets")
	return cmd
}

func (a *app) addRunFlags(cmd *cobra.Command) {
	cmd.Flags().BoolVarP(&a.yes, "yes", "y", false, "nao pede confirmacao (targets com confirm: true)")
	cmd.Flags().IntVar(&a.retries, "retries", 0, "tentativas extras por unidade (sobrescreve o target)")
	cmd.Flags().IntVar(&a.maxParallel, "max-parallel", 0, "unidades em paralelo (sobrescreve o target)")
	cmd.Flags().BoolVar(&a.noHub, "no-hub", false, "nao avisa o Arch Hub")
}

func (a *app) targetsFrom(cfg *config.Config, args []string) ([]string, error) {
	if a.all {
		return cfg.TargetNames(), nil
	}
	if len(args) > 0 {
		return args, nil
	}
	if len(cfg.Targets) == 1 {
		return cfg.TargetNames(), nil
	}
	return nil, fmt.Errorf("escolha um target: %s (ou --all)", strings.Join(cfg.TargetNames(), ", "))
}

func (a *app) runDeploy(args []string, dryRun bool) error {
	cfg, err := a.load()
	if err != nil {
		return err
	}
	names, err := a.targetsFrom(cfg, args)
	if err != nil {
		return err
	}
	for _, name := range names {
		if _, ok := cfg.Targets[name]; !ok {
			return fmt.Errorf("target %q nao existe (disponiveis: %s)", name, strings.Join(cfg.TargetNames(), ", "))
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	failed := 0
	for _, name := range names {
		t := cfg.Targets[name]
		if t.Confirm && !dryRun && !a.yes {
			if err := a.confirm(name); err != nil {
				return err
			}
		}
		res := engine.Deploy(ctx, cfg, name, a.options(dryRun))
		a.printResult(res)
		if !dryRun {
			a.record(cfg, res)
			if res.OK() {
				a.notifyHub(ctx, cfg)
			}
		}
		if !res.OK() {
			failed++
			if ctx.Err() != nil {
				break
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d de %d target(s) falharam", failed, len(names))
	}
	return nil
}

func (a *app) rollbackCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rollback <target>",
		Short: "Volta o target para a versao anterior",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := a.load()
			if err != nil {
				return err
			}
			name := args[0]
			t, ok := cfg.Targets[name]
			if !ok {
				return fmt.Errorf("target %q nao existe", name)
			}
			if t.Confirm && !a.dryRun && !a.yes {
				if err := a.confirm(name); err != nil {
					return err
				}
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			res := engine.Rollback(ctx, cfg, name, a.options(a.dryRun))
			a.printResult(res)
			if !a.dryRun {
				a.record(cfg, res)
				if res.OK() {
					a.notifyHub(ctx, cfg)
				}
			}
			if !res.OK() {
				return res.Err
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&a.dryRun, "dry-run", false, "mostra os comandos sem executar")
	a.addRunFlags(cmd)
	return cmd
}

func (a *app) options(dryRun bool) engine.Options {
	return engine.Options{
		DryRun:      dryRun,
		Out:         a.out,
		Retries:     a.retries,
		MaxParallel: a.maxParallel,
	}
}

func (a *app) confirm(name string) error {
	if f, ok := a.in.(*os.File); ok {
		if info, err := f.Stat(); err == nil && info.Mode()&os.ModeCharDevice == 0 {
			return fmt.Errorf("o target %q pede confirmacao: rode com --yes em scripts/CI", name)
		}
	}
	fmt.Fprintf(a.out, "Deploy em %q. Digite o nome do target para confirmar: ", name)
	line, err := bufio.NewReader(a.in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if strings.TrimSpace(line) != name {
		return errors.New("cancelado")
	}
	return nil
}

func (a *app) printResult(res engine.Result) {
	if res.DryRun && res.OK() {
		fmt.Fprintf(a.out, "<== plano de %s ok\n\n", res.Target)
		return
	}
	status := "OK"
	if !res.OK() {
		status = "FALHOU"
	}
	fmt.Fprintf(a.out, "<== %s %s %s em %s", res.Action, res.Target, status, res.Duration.Round(100*time.Millisecond))
	if res.RolledBack {
		fmt.Fprint(a.out, " (rollback automatico feito)")
	}
	fmt.Fprintln(a.out)
	if len(res.Units) > 1 {
		for _, u := range res.Units {
			state := "ok"
			switch {
			case u.Skipped:
				state = "pulado"
			case u.Err != nil:
				state = "falhou: " + u.Err.Error()
			}
			fmt.Fprintf(a.out, "    %-28s %s (tentativas: %d, %s)\n", u.Unit, state, u.Attempts, u.Duration.Round(100*time.Millisecond))
		}
	}
	if res.Err != nil {
		fmt.Fprintf(a.out, "    erro: %v\n", res.Err)
	}
	fmt.Fprintln(a.out)
}

func (a *app) record(cfg *config.Config, res engine.Result) {
	entry := history.Entry{
		Time:       res.Started,
		Target:     res.Target,
		Provider:   res.Provider,
		Action:     res.Action,
		Version:    res.Version,
		Commit:     res.Commit,
		OK:         res.OK(),
		DurationMS: res.Duration.Milliseconds(),
		RolledBack: res.RolledBack,
		User:       currentUser(),
	}
	if res.Err != nil {
		entry.Error = res.Err.Error()
	}
	for _, u := range res.Units {
		entry.Units = append(entry.Units, u.Unit)
	}
	if err := history.Append(cfg.Dir, entry); err != nil {
		fmt.Fprintf(a.out, "aviso: nao consegui gravar o historico: %v\n", err)
	}
}

func (a *app) notifyHub(ctx context.Context, cfg *config.Config) {
	if a.noHub {
		return
	}
	settings, ok := hub.Resolve(cfg)
	if !ok {
		return
	}
	if err := hub.Heartbeat(ctx, &http.Client{Timeout: 10 * time.Second}, settings); err != nil {
		fmt.Fprintf(a.out, "aviso: Arch Hub nao recebeu o aviso: %v\n", err)
		return
	}
	fmt.Fprintf(a.out, "Arch Hub avisado (%s)\n", settings.System)
}

func currentUser() string {
	for _, k := range []string{"USER", "USERNAME"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}
