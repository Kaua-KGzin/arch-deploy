// Package engine runs a deploy:
//
//	before hooks -> provider commands per unit (parallel, with retries)
//	-> health check -> after hooks
//
// and, when something fails and auto_rollback is on, the provider rollback
// plus the target's rollback hooks.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Kaua-KGzin/arch-deploy/internal/config"
	"github.com/Kaua-KGzin/arch-deploy/internal/provider"
	"github.com/Kaua-KGzin/arch-deploy/internal/runner"
	"github.com/Kaua-KGzin/arch-deploy/internal/vars"
)

type Options struct {
	DryRun bool
	Out    io.Writer
	Runner runner.Runner
	HTTP   *http.Client
	Now    func() time.Time
	// Overrides of the target settings; zero keeps the target's value.
	Retries     int
	MaxParallel int
	// Vars replaces the git-derived variables (tests).
	Vars vars.Vars
}

type UnitResult struct {
	Unit     string        `json:"unit"`
	Attempts int           `json:"attempts"`
	Duration time.Duration `json:"duration"`
	Err      error         `json:"-"`
	Skipped  bool          `json:"skipped,omitempty"`
}

type Result struct {
	Target     string        `json:"target"`
	Provider   string        `json:"provider"`
	Action     string        `json:"action"`
	Version    string        `json:"version"`
	Commit     string        `json:"commit,omitempty"`
	Started    time.Time     `json:"started"`
	Duration   time.Duration `json:"duration"`
	Units      []UnitResult  `json:"units"`
	Health     string        `json:"health,omitempty"`
	RolledBack bool          `json:"rolled_back,omitempty"`
	Err        error         `json:"-"`
	DryRun     bool          `json:"-"`
}

// OK reports whether the deploy succeeded.
func (r Result) OK() bool { return r.Err == nil }

type run struct {
	cfg  *config.Config
	name string
	t    *config.Target
	p    *provider.Provider
	env  provider.Env
	opts Options
	mu   sync.Mutex
	undo bool // rolling back this run's own release (auto_rollback)
}

func newRun(cfg *config.Config, name string, opts Options) (*run, error) {
	t, ok := cfg.Targets[name]
	if !ok {
		return nil, fmt.Errorf("target %q nao existe (disponiveis: %v)", name, cfg.TargetNames())
	}
	p, err := provider.Get(t.Provider)
	if err != nil {
		return nil, err
	}
	if err := p.Validate(t); err != nil {
		return nil, fmt.Errorf("target %q: %w", name, err)
	}
	if opts.Out == nil {
		opts.Out = io.Discard
	}
	if opts.Runner == nil {
		opts.Runner = runner.Exec{DryRun: opts.DryRun}
	}
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	v := opts.Vars
	if v == nil {
		v = vars.New(cfg.Project, name, cfg.WorkDir(t), opts.Now())
	}
	return &run{cfg: cfg, name: name, t: t, p: p, env: provider.NewEnv(cfg, name, v), opts: opts}, nil
}

// Deploy runs the full deploy of one target.
func Deploy(ctx context.Context, cfg *config.Config, name string, opts Options) Result {
	r, err := newRun(cfg, name, opts)
	if err != nil {
		return Result{Target: name, Action: "deploy", Err: err, Started: time.Now()}
	}
	return r.deploy(ctx)
}

// Rollback undoes the last deploy of one target.
func Rollback(ctx context.Context, cfg *config.Config, name string, opts Options) Result {
	r, err := newRun(cfg, name, opts)
	if err != nil {
		return Result{Target: name, Action: "rollback", Err: err, Started: time.Now()}
	}
	res := r.result("rollback")
	units := r.p.UnitsOf(r.t)
	if r.p.Rollback == nil && len(r.t.Rollback) == 0 {
		res.Err = fmt.Errorf("o provider %s nao tem rollback automatico; defina `rollback:` no target", r.t.Provider)
		return r.finish(res)
	}
	fmt.Fprintf(r.opts.Out, "==> rollback de %s (%s)\n", name, r.t.Provider)
	res.Units = r.rollbackUnits(ctx, units)
	if err := firstErr(res.Units); err != nil {
		res.Err = err
	} else if err := r.hooks(ctx, "rollback", r.t.Rollback); err != nil {
		res.Err = err
	}
	if res.Err == nil {
		res.Err = r.health(ctx, &res)
	}
	return r.finish(res)
}

func (r *run) result(action string) Result {
	return Result{
		Target:   r.name,
		Provider: r.t.Provider,
		Action:   action,
		Version:  r.env.Vars["version"],
		Commit:   r.env.Vars["commit"],
		Started:  r.opts.Now(),
		DryRun:   r.opts.DryRun,
	}
}

func (r *run) finish(res Result) Result {
	res.Duration = r.opts.Now().Sub(res.Started)
	return res
}

func (r *run) deploy(ctx context.Context) Result {
	res := r.result("deploy")
	out := r.opts.Out
	mode := ""
	if r.opts.DryRun {
		mode = " [plano: nada sera executado]"
	}
	fmt.Fprintf(out, "==> %s (%s) versao %s%s\n", r.name, r.t.Provider, res.Version, mode)

	if err := r.hooks(ctx, "before", r.t.Before); err != nil {
		res.Err = err
		return r.finish(res)
	}

	units := r.p.UnitsOf(r.t)
	res.Units = r.forEachUnit(ctx, units, r.retries(), config.Bool(r.t.FailFast, true), r.deployUnit)
	if err := firstErr(res.Units); err != nil {
		res.Err = err
		// A unit that failed half-way may already be switched; providers
		// whose rollback is safe on any unit get every unit rolled back.
		toUndo := succeeded(res.Units)
		if r.p.IdempotentRollback {
			toUndo = units
		}
		r.autoRollback(ctx, &res, toUndo)
		return r.finish(res)
	}

	if err := r.health(ctx, &res); err != nil {
		res.Err = err
		r.autoRollback(ctx, &res, units)
		return r.finish(res)
	}

	if err := r.hooks(ctx, "after", r.t.After); err != nil {
		res.Err = err
	}
	return r.finish(res)
}

func (r *run) deployUnit(ctx context.Context, unit string, out io.Writer) error {
	env := r.env
	env.Vars = env.Vars.With("host", unitHost(unit))
	cmds, err := r.p.Deploy(env, unit)
	if err != nil {
		return err
	}
	return r.runAll(ctx, cmds, out)
}

func (r *run) rollbackUnit(ctx context.Context, unit string, out io.Writer) error {
	if r.p.Rollback == nil {
		return nil
	}
	env := r.env
	env.Vars = env.Vars.With("host", unitHost(unit))
	if r.undo {
		// Undo exactly the release this run created, not whatever is live.
		env.Vars = env.Vars.With("undo", env.Vars["release"])
	}
	cmds, err := r.p.Rollback(env, unit)
	if err != nil {
		return err
	}
	return r.runAll(ctx, cmds, out)
}

// rollbackUnits never fails fast: one host failing to roll back must not
// leave the others on the broken version.
func (r *run) rollbackUnits(ctx context.Context, units []string) []UnitResult {
	return r.forEachUnit(ctx, units, 0, false, r.rollbackUnit)
}

func (r *run) autoRollback(ctx context.Context, res *Result, units []string) {
	if !r.t.AutoRollback || r.opts.DryRun {
		return
	}
	fmt.Fprintf(r.opts.Out, "==> deploy falhou: rollback automatico de %s\n", r.name)
	// The deploy context may be cancelled (fail-fast); the rollback must run.
	ctx = context.WithoutCancel(ctx)
	r.undo = true
	results := r.rollbackUnits(ctx, units)
	r.undo = false
	if err := firstErr(results); err != nil {
		fmt.Fprintf(r.opts.Out, "rollback falhou: %v\n", err)
		return
	}
	if err := r.hooks(ctx, "rollback", r.t.Rollback); err != nil {
		fmt.Fprintf(r.opts.Out, "rollback falhou: %v\n", err)
		return
	}
	res.RolledBack = true
}

func (r *run) runAll(ctx context.Context, cmds []runner.Cmd, out io.Writer) error {
	for _, c := range cmds {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := r.opts.Runner.Run(ctx, c, out); err != nil {
			return err
		}
	}
	return nil
}

func (r *run) hooks(ctx context.Context, stage string, scripts []string) error {
	for _, script := range scripts {
		if err := r.opts.Runner.Run(ctx, r.env.Shell(script), r.opts.Out); err != nil {
			return fmt.Errorf("%s: %w", stage, err)
		}
	}
	return nil
}

func (r *run) retries() int {
	if r.opts.Retries > 0 {
		return r.opts.Retries
	}
	return r.t.Retries
}

type unitFunc func(ctx context.Context, unit string, out io.Writer) error

// forEachUnit runs fn for every unit, up to max_parallel at a time, retrying
// each unit with exponential backoff. With failFast the first unit that gives
// up cancels the ones not finished yet; those are reported as skipped so the
// real failure is the one shown.
func (r *run) forEachUnit(ctx context.Context, units []string, retries int, failFast bool, fn unitFunc) []UnitResult {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	limit := r.opts.MaxParallel
	if limit <= 0 {
		limit = r.t.MaxParallel
	}
	if limit <= 0 {
		limit = 4
	}
	if r.opts.DryRun {
		limit = 1 // a plan reads top to bottom, one unit after the other
	}
	results := make([]UnitResult, len(units))
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i := range units {
		results[i].Unit = units[i]
	}
	// Taking the slot here, not inside the goroutine, starts units in the
	// order they are configured.
	for i, unit := range units {
		select {
		case sem <- struct{}{}:
			if ctx.Err() != nil { // both were ready: do not start after a cancel
				<-sem
			}
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			for j := i; j < len(units); j++ {
				results[j].Skipped = true
				results[j].Err = context.Canceled
			}
			break
		}
		wg.Add(1)
		go func(i int, unit string) {
			defer wg.Done()
			defer func() { <-sem }()

			out := r.opts.Out
			var pw *runner.PrefixWriter
			if len(units) > 1 {
				pw = runner.NewPrefixWriter(r.opts.Out, &r.mu, "["+unit+"] ")
				out = pw
			}
			start := r.opts.Now()
			var err error
			for attempt := 1; attempt <= retries+1; attempt++ {
				results[i].Attempts = attempt
				if err = fn(ctx, unit, out); err == nil || ctx.Err() != nil {
					break
				}
				if attempt <= retries {
					delay := retryDelay(r.t.RetryDelay, attempt)
					fmt.Fprintf(out, "falhou (%v); nova tentativa em %s\n", err, delay)
					if sleepErr := sleep(ctx, delay); sleepErr != nil {
						break
					}
				}
			}
			if pw != nil {
				pw.Flush()
			}
			results[i].Duration = r.opts.Now().Sub(start)
			results[i].Err = err
			if err != nil && ctx.Err() != nil {
				// Interrupted by another unit's failure (or Ctrl-C), not a
				// failure of its own.
				results[i].Skipped = true
			}
			if err != nil && failFast {
				cancel()
			}
		}(i, unit)
	}
	wg.Wait()
	return results
}

func (r *run) health(ctx context.Context, res *Result) error {
	hc := r.t.Healthcheck
	if hc == nil {
		return nil
	}
	url := r.env.Vars.Expand(hc.URL)
	if r.opts.DryRun {
		fmt.Fprintf(r.opts.Out, "> health check: GET %s\n", url)
		return nil
	}
	if err := WaitHealthy(ctx, r.opts.HTTP, url, hc, r.opts.Out); err != nil {
		res.Health = err.Error()
		return fmt.Errorf("health check: %w", err)
	}
	res.Health = "ok"
	return nil
}

func firstErr(units []UnitResult) error {
	wrap := func(u UnitResult) error {
		if u.Unit == provider.Local {
			return u.Err
		}
		return fmt.Errorf("%s: %w", u.Unit, u.Err)
	}
	for _, u := range units {
		if u.Err != nil && !u.Skipped {
			return wrap(u)
		}
	}
	for _, u := range units {
		if u.Err != nil {
			return wrap(u)
		}
	}
	return nil
}

func succeeded(units []UnitResult) []string {
	var out []string
	for _, u := range units {
		if u.Err == nil {
			out = append(out, u.Unit)
		}
	}
	return out
}

func unitHost(unit string) string {
	if unit == provider.Local {
		return ""
	}
	return unit
}

// retryDelay doubles base per attempt, capped at 16x and 30s.
func retryDelay(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = 2 * time.Second
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

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ErrUnhealthy is returned when the health check never reaches the expected status.
var ErrUnhealthy = errors.New("servico nao ficou saudavel")
