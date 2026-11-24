package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseFullTarget(t *testing.T) {
	cfg, err := Parse([]byte(`
project: shop
targets:
  production:
    provider: ssh
    hosts: [a.example.com, b.example.com]
    path: /srv/shop
    output: dist
    retries: 2
    retry_delay: 1s
    healthcheck: {url: "https://shop.example.com/health", timeout: 30s}
    confirm: true
`))
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Targets["production"]
	if p.Provider != "ssh" || len(p.Hosts) != 2 || p.RetryDelay != time.Second || p.Healthcheck.Timeout != 30*time.Second || !p.Confirm {
		t.Fatalf("target mal lido: %+v", p)
	}
}

func TestParseRejectsUnknownFieldsAndMissingProvider(t *testing.T) {
	cases := map[string]string{
		"campo desconhecido": "targets:\n  prod:\n    provider: vercel\n    prodd: true\n",
		"sem provider":       "targets:\n  prod:\n    dir: web\n",
		"sem targets":        "project: x\n",
		"healthcheck vazio":  "targets:\n  prod:\n    provider: vercel\n    healthcheck: {expect: 200}\n",
	}
	for name, raw := range cases {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("%s: esperava erro", name)
		}
	}
}

func TestExpandEnvOnlyBracedWithDefault(t *testing.T) {
	t.Setenv("AD_TEST_HOST", "prod.example.com")
	got := ExpandEnv("${AD_TEST_HOST} ${AD_TEST_MISSING:-fallback} ${AD_TEST_MISSING} $HOME")
	want := "prod.example.com fallback  $HOME"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestLoadDefaultsProjectAndResolvesDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "arch-deploy.yaml")
	os.WriteFile(path, []byte("targets:\n  web:\n    provider: vercel\n    dir: apps/web\n"), 0o644)

	found, err := Find("", dir)
	if err != nil || found != path {
		t.Fatalf("Find = %q, %v", found, err)
	}
	cfg, err := Load(found)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Project != filepath.Base(dir) {
		t.Errorf("project = %q", cfg.Project)
	}
	if got := cfg.WorkDir(cfg.Targets["web"]); got != filepath.Join(dir, "apps", "web") {
		t.Errorf("WorkDir = %q", got)
	}
}

func TestFindWithoutConfigSuggestsInit(t *testing.T) {
	_, err := Find("", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "arch-deploy init") {
		t.Fatalf("err = %v", err)
	}
}
