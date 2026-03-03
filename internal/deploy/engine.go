package deploy

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"deploy-cli/internal/config"
)

type Options struct {
	Parallel    bool
	MaxParallel int
	Timeout     time.Duration
	DryRun      bool
	Retries     int
	RetryDelay  time.Duration
	FailFast    bool
	Verbose     bool
}

type Plan struct {
	Service     string
	Environment string
	Servers     []string
	Parallel    bool
	MaxParallel int
	Timeout     time.Duration
	DryRun      bool
	Retries     int
	RetryDelay  time.Duration
	FailFast    bool
}

type Result struct {
	Index    int
	Server   string
	Attempts int
	Duration time.Duration
	Err      error
}

type Summary struct {
	Service     string
	Environment string
	Total       int
	Succeeded   int
	Failed      int
	Skipped     int
	Duration    time.Duration
	Parallel    bool
	MaxParallel int
	DryRun      bool
	Results     []Result
}

type Executor func(ctx context.Context, service, server string, dryRun bool, verbose bool) error

type deployJob struct {
	index  int
	server string
}

var (
	executor Executor = DeployToServer

	ErrSkippedByFailFast = errors.New("servidor nao executado devido a fail-fast")
)

func (o Options) Validate() error {
	if o.Timeout <= 0 {
		return errors.New("timeout deve ser maior que zero")
	}
	if o.MaxParallel <= 0 {
		return errors.New("max-parallel deve ser maior que zero")
	}
	if o.Retries < 0 {
		return errors.New("retries nao pode ser negativo")
	}
	if o.RetryDelay < 0 {
		return errors.New("retry-delay nao pode ser negativo")
	}
	return nil
}

func BuildPlan(service, environment string, environments map[string][]string, opts Options) (Plan, error) {
	resolvedEnvironment, servers, err := config.ResolveServers(environment, environments)
	if err != nil {
		return Plan{}, err
	}

	workerCount := calculateWorkerCount(opts.MaxParallel, len(servers))

	return Plan{
		Service:     service,
		Environment: resolvedEnvironment,
		Servers:     servers,
		Parallel:    opts.Parallel,
		MaxParallel: workerCount,
		Timeout:     opts.Timeout,
		DryRun:      opts.DryRun,
		Retries:     opts.Retries,
		RetryDelay:  opts.RetryDelay,
		FailFast:    opts.FailFast,
	}, nil
}

func PrintPlan(plan Plan) {
	fmt.Println("Plano de deploy")
	fmt.Println("---------------")
	fmt.Printf("Servico: %s\n", plan.Service)
	fmt.Printf("Ambiente: %s\n", plan.Environment)
	fmt.Printf("Servidores (%d): %s\n", len(plan.Servers), strings.Join(plan.Servers, ", "))
	fmt.Printf("Execucao paralela: %t\n", plan.Parallel)
	fmt.Printf("Max paralelo: %d\n", plan.MaxParallel)
	fmt.Printf("Timeout: %s\n", plan.Timeout)
	fmt.Printf("Retries: %d\n", plan.Retries)
	fmt.Printf("Retry delay: %s\n", plan.RetryDelay)
	fmt.Printf("Fail fast: %t\n", plan.FailFast)
	fmt.Printf("Dry run: %t\n", plan.DryRun)
}

func Execute(ctx context.Context, plan Plan, opts Options) (Summary, error) {
	if err := opts.Validate(); err != nil {
		return Summary{}, err
	}

	started := time.Now()

	var (
		results []Result
		err     error
	)

	if plan.Parallel {
		results, err = deployParallel(ctx, plan, opts)
	} else {
		results, err = deploySequential(ctx, plan, opts)
	}

	summary := buildSummary(plan, time.Since(started), results)
	if err != nil {
		return summary, err
	}
	if summary.Failed > 0 {
		return summary, fmt.Errorf("deploy finalizado com %d falha(s) e %d skip(s)", summary.Failed, summary.Skipped)
	}

	return summary, nil
}

func PrintSummary(summary Summary) {
	fmt.Println()
	fmt.Println("Resumo do deploy")
	fmt.Println("----------------")
	fmt.Printf("Servico: %s\n", summary.Service)
	fmt.Printf("Ambiente: %s\n", summary.Environment)
	fmt.Printf("Modo paralelo: %t\n", summary.Parallel)
	fmt.Printf("Max paralelo: %d\n", summary.MaxParallel)
	fmt.Printf("Dry run: %t\n", summary.DryRun)
	fmt.Printf("Total: %d | Sucesso: %d | Falha: %d | Skip: %d\n", summary.Total, summary.Succeeded, summary.Failed, summary.Skipped)
	fmt.Printf("Duracao total: %s\n", summary.Duration.Round(time.Millisecond))
	fmt.Println()
	fmt.Println("Detalhes por servidor")
	fmt.Println("---------------------")
	for _, result := range summary.Results {
		fmt.Printf("- %-24s %-8s tentativas=%d duracao=%s", result.Server, statusLabel(result.Err, result.Attempts), result.Attempts, result.Duration.Round(time.Millisecond))
		if result.Err != nil && !isSkippedError(result.Err) {
			fmt.Printf(" erro=%v", result.Err)
		}
		fmt.Println()
	}
}

func deploySequential(ctx context.Context, plan Plan, opts Options) ([]Result, error) {
	results := make([]Result, 0, len(plan.Servers))

	for index, server := range plan.Servers {
		if ctx.Err() != nil {
			break
		}
		if opts.Verbose {
			fmt.Printf("[%d/%d] Deploying em %s...\n", index+1, len(plan.Servers), server)
		}

		result := deployWithRetry(ctx, plan.Service, server, index, opts)
		results = append(results, result)

		if opts.Verbose {
			printLiveResult(index+1, len(plan.Servers), result)
		}
		if result.Err != nil && opts.FailFast {
			break
		}
	}

	results = appendSkippedResults(results, plan)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return results, fmt.Errorf("timeout de deploy excedido: %w", ctx.Err())
	}

	return results, nil
}

func deployParallel(ctx context.Context, plan Plan, opts Options) ([]Result, error) {
	workerCount := calculateWorkerCount(plan.MaxParallel, len(plan.Servers))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan deployJob)
	resultsCh := make(chan Result, len(plan.Servers))

	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case job, ok := <-jobs:
					if !ok {
						return
					}
					result := deployWithRetry(ctx, plan.Service, job.server, job.index, opts)
					resultsCh <- result
					if opts.FailFast && result.Err != nil {
						cancel()
					}
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for index, server := range plan.Servers {
			select {
			case <-ctx.Done():
				return
			case jobs <- deployJob{index: index, server: server}:
			}
		}
	}()

	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	results := make([]Result, 0, len(plan.Servers))
	completed := 0
	for result := range resultsCh {
		completed++
		results = append(results, result)
		if opts.Verbose {
			printLiveResult(completed, len(plan.Servers), result)
		}
	}

	results = appendSkippedResults(results, plan)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return results, fmt.Errorf("timeout de deploy excedido: %w", ctx.Err())
	}

	return results, nil
}

func deployWithRetry(ctx context.Context, service, server string, index int, opts Options) Result {
	started := time.Now()
	maxAttempts := opts.Retries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	attempts := 0
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			lastErr = ctx.Err()
			break
		}

		attempts = attempt
		lastErr = executor(ctx, service, server, opts.DryRun, opts.Verbose)
		if lastErr == nil {
			break
		}

		if attempt < maxAttempts {
			delay := computeRetryDelay(opts.RetryDelay, attempt)
			if opts.Verbose {
				fmt.Printf("  -> [%s] tentativa %d falhou: %v. retry em %s\n", server, attempt, lastErr, delay)
			}
			if sleepErr := sleepWithContext(ctx, delay); sleepErr != nil {
				lastErr = sleepErr
				break
			}
		}
	}

	return Result{
		Index:    index,
		Server:   server,
		Attempts: attempts,
		Duration: time.Since(started),
		Err:      lastErr,
	}
}

func appendSkippedResults(results []Result, plan Plan) []Result {
	seen := make(map[int]struct{}, len(results))
	for _, result := range results {
		seen[result.Index] = struct{}{}
	}

	for index, server := range plan.Servers {
		if _, ok := seen[index]; ok {
			continue
		}
		results = append(results, Result{
			Index:    index,
			Server:   server,
			Attempts: 0,
			Duration: 0,
			Err:      ErrSkippedByFailFast,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Index < results[j].Index
	})

	return results
}

func buildSummary(plan Plan, duration time.Duration, results []Result) Summary {
	summary := Summary{
		Service:     plan.Service,
		Environment: plan.Environment,
		Total:       len(plan.Servers),
		Duration:    duration,
		Parallel:    plan.Parallel,
		MaxParallel: plan.MaxParallel,
		DryRun:      plan.DryRun,
		Results:     results,
	}

	for _, result := range results {
		switch {
		case result.Err == nil:
			summary.Succeeded++
		case isSkippedError(result.Err) || (errors.Is(result.Err, context.Canceled) && result.Attempts == 0):
			summary.Skipped++
		default:
			summary.Failed++
		}
	}

	return summary
}

func printLiveResult(current, total int, result Result) {
	status := statusLabel(result.Err, result.Attempts)
	message := fmt.Sprintf("[%d/%d] %s %s (tentativas=%d, duracao=%s)", current, total, status, result.Server, result.Attempts, result.Duration.Round(time.Millisecond))
	if result.Err != nil && !isSkippedError(result.Err) {
		message = fmt.Sprintf("%s erro=%v", message, result.Err)
	}
	fmt.Println(message)
}

func statusLabel(err error, attempts int) string {
	switch {
	case err == nil:
		return "[OK]"
	case isSkippedError(err) || (errors.Is(err, context.Canceled) && attempts == 0):
		return "[SKIP]"
	default:
		return "[ERROR]"
	}
}

func isSkippedError(err error) bool {
	return errors.Is(err, ErrSkippedByFailFast)
}

func calculateWorkerCount(requested, totalServers int) int {
	if totalServers <= 0 {
		return 1
	}
	if requested <= 0 {
		return 1
	}
	if requested > totalServers {
		return totalServers
	}
	return requested
}

func computeRetryDelay(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = 500 * time.Millisecond
	}
	if attempt < 1 {
		attempt = 1
	}
	factor := 1 << (attempt - 1)
	if factor > 16 {
		factor = 16
	}
	delay := time.Duration(factor) * base
	if delay > 30*time.Second {
		return 30 * time.Second
	}
	return delay
}

func DeployToServer(ctx context.Context, service, server string, dryRun bool, verbose bool) error {
	steps := []struct {
		message string
		delay   time.Duration
	}{
		{message: "Conectando", delay: 450 * time.Millisecond},
		{message: "Fazendo backup da versao atual", delay: 300 * time.Millisecond},
		{message: fmt.Sprintf("Atualizando %s", service), delay: 950 * time.Millisecond},
		{message: "Reiniciando servico", delay: 250 * time.Millisecond},
	}

	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if verbose {
			fmt.Printf("  -> [%s] %s\n", server, step.message)
		}
		if dryRun {
			continue
		}
		if err := sleepWithContext(ctx, step.delay); err != nil {
			return err
		}
	}

	return nil
}

func sleepWithContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
