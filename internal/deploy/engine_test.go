package deploy

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDeployWithRetryRetriesTransientFailure(t *testing.T) {
	originalExecutor := executor
	t.Cleanup(func() {
		executor = originalExecutor
	})

	calls := 0
	executor = func(ctx context.Context, service, server string, dryRun bool, verbose bool) error {
		calls++
		if calls < 3 {
			return errors.New("temporary error")
		}
		return nil
	}

	opts := Options{
		Retries:    3,
		RetryDelay: time.Millisecond,
		DryRun:     true,
		Verbose:    false,
	}

	result := deployWithRetry(context.Background(), "api", "server-1", 0, opts)
	if result.Err != nil {
		t.Fatalf("expected success after retries, got error: %v", result.Err)
	}
	if result.Attempts != 3 {
		t.Fatalf("unexpected attempts: got=%d want=3", result.Attempts)
	}
}

func TestDeploySequentialFailFastMarksSkipped(t *testing.T) {
	originalExecutor := executor
	t.Cleanup(func() {
		executor = originalExecutor
	})

	calls := 0
	executor = func(ctx context.Context, service, server string, dryRun bool, verbose bool) error {
		calls++
		if calls == 1 {
			return errors.New("boom")
		}
		return nil
	}

	plan := Plan{
		Service: "api",
		Servers: []string{"srv-1", "srv-2", "srv-3"},
	}
	opts := Options{
		Retries:     0,
		RetryDelay:  time.Millisecond,
		FailFast:    true,
		DryRun:      true,
		Verbose:     false,
		Timeout:     time.Second,
		MaxParallel: 1,
	}

	results, err := deploySequential(context.Background(), plan, opts)
	if err != nil {
		t.Fatalf("deploySequential returned error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("unexpected results length: got=%d want=3", len(results))
	}
	if results[0].Err == nil {
		t.Fatal("expected first server to fail")
	}
	if !errors.Is(results[1].Err, ErrSkippedByFailFast) || !errors.Is(results[2].Err, ErrSkippedByFailFast) {
		t.Fatal("expected remaining servers to be skipped")
	}
	if calls != 1 {
		t.Fatalf("expected a single deploy attempt due to fail-fast, got=%d", calls)
	}
}

func TestCalculateWorkerCount(t *testing.T) {
	if got := calculateWorkerCount(10, 3); got != 3 {
		t.Fatalf("unexpected worker count. got=%d want=3", got)
	}
	if got := calculateWorkerCount(0, 5); got != 1 {
		t.Fatalf("unexpected worker count. got=%d want=1", got)
	}
	if got := calculateWorkerCount(2, 0); got != 1 {
		t.Fatalf("unexpected worker count for empty servers. got=%d want=1", got)
	}
}

func TestComputeRetryDelayCap(t *testing.T) {
	delay := computeRetryDelay(10*time.Second, 5)
	if delay != 30*time.Second {
		t.Fatalf("unexpected capped delay. got=%s want=30s", delay)
	}
}

func TestSleepWithContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := sleepWithContext(ctx, 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}
