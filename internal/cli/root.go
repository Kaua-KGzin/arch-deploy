package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"deploy-cli/internal/config"
	"deploy-cli/internal/deploy"
	"github.com/spf13/cobra"
)

type app struct {
	configPath string
	cfg        config.Config

	environment string
	parallel    bool
	timeout     int
	dryRun      bool
	maxParallel int
	retries     int
	retryDelay  time.Duration
	failFast    bool

	statusEnvironment string
	statusShowServers bool
}

func NewRootCommand() *cobra.Command {
	a := &app{}

	rootCmd := &cobra.Command{
		Use:          "deploy-cli",
		Short:        "CLI de automacao de deploys",
		Long:         "Ferramenta de linha de comando para automatizar deploys em multiplos ambientes.",
		SilenceUsage: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(a.configPath)
			if err != nil {
				return err
			}
			a.cfg = cfg
			return nil
		},
	}

	rootCmd.PersistentFlags().StringVarP(&a.configPath, "config", "c", "deploy.yaml", "Caminho do arquivo de configuracao")

	deployCmd := &cobra.Command{
		Use:   "deploy [service]",
		Short: "Executa o deploy de um servico",
		Args:  cobra.ExactArgs(1),
		RunE:  a.runDeploy,
	}
	a.addDeployFlags(deployCmd)

	planCmd := &cobra.Command{
		Use:   "plan [service]",
		Short: "Mostra o plano de deploy sem executar alteracoes",
		Args:  cobra.ExactArgs(1),
		RunE:  a.runPlan,
	}
	a.addDeployFlags(planCmd)

	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Mostra servicos cadastrados e inventario de ambientes",
		RunE:  a.runStatus,
	}
	statusCmd.Flags().StringVarP(&a.statusEnvironment, "env", "e", "", "Filtra o status por ambiente")
	statusCmd.Flags().BoolVar(&a.statusShowServers, "show-servers", true, "Mostra a lista de servidores em cada ambiente")

	validateConfigCmd := &cobra.Command{
		Use:   "validate-config",
		Short: "Valida o arquivo de configuracao carregado",
		RunE:  a.runValidateConfig,
	}

	rootCmd.AddCommand(deployCmd, planCmd, statusCmd, validateConfigCmd)
	return rootCmd
}

func (a *app) addDeployFlags(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&a.environment, "env", "e", "staging", "Ambiente de destino")
	cmd.Flags().BoolVarP(&a.parallel, "parallel", "p", false, "Executa em paralelo")
	cmd.Flags().IntVarP(&a.timeout, "timeout", "t", 300, "Timeout total em segundos")
	cmd.Flags().BoolVar(&a.dryRun, "dry-run", false, "Mostra etapas sem executar alteracoes")
	cmd.Flags().IntVar(&a.maxParallel, "max-parallel", 4, "Numero maximo de deploys simultaneos no modo paralelo")
	cmd.Flags().IntVar(&a.retries, "retries", 1, "Numero de tentativas adicionais por servidor")
	cmd.Flags().DurationVar(&a.retryDelay, "retry-delay", 2*time.Second, "Intervalo base entre tentativas (ex.: 2s, 500ms)")
	cmd.Flags().BoolVar(&a.failFast, "fail-fast", false, "Interrompe o deploy ao primeiro erro")
}

func (a *app) runDeploy(cmd *cobra.Command, args []string) error {
	service, err := config.ResolveServiceName(args[0], a.cfg.Services)
	if err != nil {
		return err
	}

	opts, err := a.buildDeployOptions()
	if err != nil {
		return err
	}

	plan, err := deploy.BuildPlan(service, a.environment, a.cfg.Environments, opts)
	if err != nil {
		return err
	}

	fmt.Printf("Iniciando deploy de %s no ambiente %s\n", plan.Service, plan.Environment)
	fmt.Printf("Alvos (%d): %s\n", len(plan.Servers), strings.Join(plan.Servers, ", "))
	if plan.DryRun {
		fmt.Println("Modo dry-run ativo: nenhuma alteracao sera aplicada.")
	}
	if plan.Parallel {
		fmt.Printf("Modo paralelo ativo (max-parallel=%d)\n", plan.MaxParallel)
	} else {
		fmt.Println("Modo sequencial ativo")
	}

	ctx, cancel := context.WithTimeout(context.Background(), plan.Timeout)
	defer cancel()

	summary, execErr := deploy.Execute(ctx, plan, opts)
	deploy.PrintSummary(summary)
	return execErr
}

func (a *app) runPlan(cmd *cobra.Command, args []string) error {
	service, err := config.ResolveServiceName(args[0], a.cfg.Services)
	if err != nil {
		return err
	}

	opts, err := a.buildDeployOptions()
	if err != nil {
		return err
	}

	plan, err := deploy.BuildPlan(service, a.environment, a.cfg.Environments, opts)
	if err != nil {
		return err
	}

	deploy.PrintPlan(plan)
	return nil
}

func (a *app) runStatus(cmd *cobra.Command, args []string) error {
	fmt.Println("Servicos cadastrados")
	fmt.Println("--------------------")
	services := make([]string, 0, len(a.cfg.Services))
	services = append(services, a.cfg.Services...)
	sort.Strings(services)
	for _, service := range services {
		fmt.Printf("- %s\n", service)
	}

	environments, err := config.FilterEnvironments(a.statusEnvironment, a.cfg.Environments)
	if err != nil {
		return err
	}

	envNames := make([]string, 0, len(environments))
	for envName := range environments {
		envNames = append(envNames, envName)
	}
	sort.Strings(envNames)

	fmt.Println()
	fmt.Println("Inventario de ambientes")
	fmt.Println("-----------------------")
	for _, envName := range envNames {
		servers := environments[envName]
		fmt.Printf("- %s (%d servidor(es))\n", envName, len(servers))
		if a.statusShowServers {
			for _, server := range servers {
				fmt.Printf("  - %s\n", server)
			}
		}
	}

	return nil
}

func (a *app) runValidateConfig(cmd *cobra.Command, args []string) error {
	source := "defaults internos"
	if path, ok, err := config.FirstExistingPath(config.ResolveCandidates(a.configPath)); err != nil {
		return err
	} else if ok {
		source = path
	}

	envNames := make([]string, 0, len(a.cfg.Environments))
	for envName := range a.cfg.Environments {
		envNames = append(envNames, envName)
	}
	sort.Strings(envNames)

	fmt.Println("Configuracao valida")
	fmt.Println("-------------------")
	fmt.Printf("Fonte: %s\n", source)
	fmt.Printf("Servicos: %d\n", len(a.cfg.Services))
	fmt.Printf("Ambientes: %d\n", len(a.cfg.Environments))
	for _, envName := range envNames {
		fmt.Printf("- %s: %d servidor(es)\n", envName, len(a.cfg.Environments[envName]))
	}

	return nil
}

func (a *app) buildDeployOptions() (deploy.Options, error) {
	opts := deploy.Options{
		Parallel:    a.parallel,
		MaxParallel: a.maxParallel,
		Timeout:     time.Duration(a.timeout) * time.Second,
		DryRun:      a.dryRun,
		Retries:     a.retries,
		RetryDelay:  a.retryDelay,
		FailFast:    a.failFast,
		Verbose:     true,
	}
	if err := opts.Validate(); err != nil {
		return deploy.Options{}, err
	}
	return opts, nil
}
