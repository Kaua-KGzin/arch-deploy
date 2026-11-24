package engine

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Kaua-KGzin/arch-deploy/internal/config"
	"github.com/Kaua-KGzin/arch-deploy/internal/runner"
	"github.com/Kaua-KGzin/arch-deploy/internal/vars"
)

func fixture(t *config.Target) *config.Config {
	return &config.Config{Project: "shop", Dir: t.Dir, Targets: map[string]*config.Target{"prod": t}}
}

func opts(rec *runner.Recorder) Options {
	return Options{
		Runner: rec,
		Out:    &bytes.Buffer{},
		Vars:   vars.Vars{"project": "shop", "version": "1.0.0", "release": "r1", "commit": "abc"},
	}
}

func sshTarget() *config.Target {
	return &config.Target{
		Provider: "ssh", Hosts: []string{"a", "b", "c"}, Path: "/srv/shop", Output: "dist",
		Before: []string{"npm run build"}, After: []string{"echo done"},
		RetryDelay: time.Millisecond,
	}
}

func TestDeployRunsHooksAroundEveryHost(t *testing.T) {
	rec := &runner.Recorder{}
	res := Deploy(context.Background(), fixture(sshTarget()), "prod", opts(rec))
	if !res.OK() {
		t.Fatal(res.Err)
	}
	log := rec.Joined()
	if !strings.HasPrefix(rec.Cmds[0], "") || !strings.Contains(rec.Cmds[0], "npm run build") {
		t.Errorf("before deveria ser o primeiro: %s", rec.Cmds[0])
	}
	if !strings.Contains(rec.Cmds[len(rec.Cmds)-1], "echo done") {
		t.Errorf("after deveria ser o ultimo: %s", rec.Cmds[len(rec.Cmds)-1])
	}
	for _, host := range []string{"a:", "b:", "c:"} {
		if !strings.Contains(log, host+"/srv/shop/releases/.upload-r1") {
			t.Errorf("host %s nao recebeu upload", host)
		}
	}
	if len(res.Units) != 3 {
		t.Errorf("units = %d", len(res.Units))
	}
}

func TestRetrySucceedsAfterTransientFailure(t *testing.T) {
	target := sshTarget()
	target.Hosts = []string{"a"}
	target.Retries = 2
	rec := &runner.Recorder{Fail: map[string]int{"scp": 2}}
	res := Deploy(context.Background(), fixture(target), "prod", opts(rec))
	if !res.OK() {
		t.Fatal(res.Err)
	}
	if res.Units[0].Attempts != 3 {
		t.Fatalf("attempts = %d", res.Units[0].Attempts)
	}
}

func TestFailureTriggersAutoRollbackOfDeployedUnits(t *testing.T) {
	target := sshTarget()
	target.Hosts = []string{"a"}
	target.AutoRollback = true
	target.Rollback = []string{"notify-team"}
	rec := &runner.Recorder{Fail: map[string]int{"echo done": -1}}
	// The failing step is the `after` hook, so the deploy itself succeeded
	// and must not be rolled back...
	res := Deploy(context.Background(), fixture(target), "prod", opts(rec))
	if res.OK() || res.RolledBack {
		t.Fatalf("after falhando nao deveria fazer rollback: ok=%v rolledBack=%v", res.OK(), res.RolledBack)
	}

	// ...but a failing host does.
	rec = &runner.Recorder{Fail: map[string]int{`"cd '/srv/shop/current' && systemctl`: -1}}
	target.Run = []string{"systemctl restart shop"}
	target.Hosts = []string{"a", "b"}
	target.MaxParallel = 1
	res = Deploy(context.Background(), fixture(target), "prod", opts(rec))
	if res.OK() {
		t.Fatal("deveria falhar")
	}
	if !strings.Contains(rec.Joined(), "notify-team") {
		t.Fatalf("hooks de rollback nao rodaram:\n%s", rec.Joined())
	}
}

func TestFailFastSkipsPendingHosts(t *testing.T) {
	target := sshTarget()
	target.Hosts = []string{"a", "b", "c"}
	target.MaxParallel = 1
	rec := &runner.Recorder{Fail: map[string]int{"mkdir": -1}}
	res := Deploy(context.Background(), fixture(target), "prod", opts(rec))
	if res.OK() {
		t.Fatal("deveria falhar")
	}
	skipped := 0
	for _, u := range res.Units {
		if u.Skipped {
			skipped++
		}
	}
	if skipped == 0 {
		t.Fatalf("fail-fast deveria pular hosts: %+v", res.Units)
	}
}

func TestHealthCheckFailureRollsBack(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	target := sshTarget()
	target.Hosts = []string{"a"}
	target.AutoRollback = true
	target.Healthcheck = &config.Healthcheck{URL: srv.URL, Timeout: 200 * time.Millisecond, Interval: 20 * time.Millisecond}
	rec := &runner.Recorder{}
	res := Deploy(context.Background(), fixture(target), "prod", opts(rec))
	if res.OK() || !res.RolledBack {
		t.Fatalf("ok=%v rolledBack=%v err=%v", res.OK(), res.RolledBack, res.Err)
	}
	if calls.Load() < 2 {
		t.Errorf("health check deveria tentar de novo, chamadas=%d", calls.Load())
	}
	if !strings.Contains(rec.Joined(), "sem release anterior") {
		t.Errorf("rollback ssh nao rodou:\n%s", rec.Joined())
	}
}

func TestAutoRollbackReachesEveryHostAndShowsRealCause(t *testing.T) {
	target := sshTarget()
	target.Hosts = []string{"a", "b", "c"}
	target.Run = []string{"systemctl restart shop"}
	target.AutoRollback = true
	// Host b fails after switching `current`; its rollback also fails once.
	rec := &runner.Recorder{Fail: map[string]int{"b \"cd '/srv/shop/current' && systemctl": -1, "b 'cd '\\''/srv/shop/releases'\\'' && cur=": -1}}
	res := Deploy(context.Background(), fixture(target), "prod", opts(rec))
	if res.OK() {
		t.Fatal("deveria falhar")
	}
	if !strings.HasPrefix(res.Err.Error(), "b:") || strings.Contains(res.Err.Error(), "canceled") {
		t.Fatalf("erro deveria apontar o host b, nao um cancelamento: %v", res.Err)
	}
	log := rec.Joined()
	for _, host := range []string{"a", "b", "c"} {
		if !strings.Contains(log, host+" 'cd '\\''/srv/shop/releases'\\'' && cur=") {
			t.Errorf("rollback nao rodou em %s (mesmo com falha em b):\n%s", host, log)
		}
	}
	if !strings.Contains(log, "target='\\''r1'\\''") {
		t.Errorf("auto rollback deveria desfazer so a release r1:\n%s", log)
	}
}

func TestHealthCheckPasses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	target := &config.Target{Provider: "exec", Command: []string{"deploy.sh"}, Healthcheck: &config.Healthcheck{URL: srv.URL}}
	res := Deploy(context.Background(), fixture(target), "prod", opts(&runner.Recorder{}))
	if !res.OK() || res.Health != "ok" {
		t.Fatalf("ok=%v health=%q err=%v", res.OK(), res.Health, res.Err)
	}
}

func TestRollbackWithoutSupportExplainsWhatToDo(t *testing.T) {
	target := &config.Target{Provider: "netlify"}
	res := Rollback(context.Background(), fixture(target), "prod", opts(&runner.Recorder{}))
	if res.OK() || !strings.Contains(res.Err.Error(), "rollback:") {
		t.Fatalf("err = %v", res.Err)
	}
}

func TestUnknownTarget(t *testing.T) {
	res := Deploy(context.Background(), fixture(&config.Target{Provider: "vercel"}), "staging", opts(&runner.Recorder{}))
	if res.OK() {
		t.Fatal("deveria falhar")
	}
}

func TestRetryDelayIsCapped(t *testing.T) {
	if d := retryDelay(time.Second, 1); d != time.Second {
		t.Errorf("1: %s", d)
	}
	if d := retryDelay(time.Second, 3); d != 4*time.Second {
		t.Errorf("3: %s", d)
	}
	if d := retryDelay(10*time.Second, 10); d != 30*time.Second {
		t.Errorf("cap: %s", d)
	}
}
