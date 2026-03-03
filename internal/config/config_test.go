package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadReturnsDefaultWhenFileMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if len(cfg.Services) == 0 {
		t.Fatal("expected default services")
	}
	if len(cfg.Environments["staging"]) == 0 {
		t.Fatal("expected default staging environment")
	}
}

func TestLoadOverridesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deploy.yaml")
	content := `services:
  - api
  - worker
environments:
  preview:
    - preview-01.example.com
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if got, want := strings.Join(cfg.Services, ","), "api,worker"; got != want {
		t.Fatalf("unexpected services. got=%q want=%q", got, want)
	}
	if _, ok := cfg.Environments["preview"]; !ok {
		t.Fatal("expected preview environment from yaml")
	}
	if _, ok := cfg.Environments["staging"]; ok {
		t.Fatal("did not expect default staging once custom env is provided")
	}
}

func TestResolveServersUnknownEnvironment(t *testing.T) {
	_, _, err := ResolveServers("qa", Default().Environments)
	if err == nil {
		t.Fatal("expected error for unknown environment")
	}
	if !strings.Contains(err.Error(), "disponiveis") {
		t.Fatalf("expected available environments in error, got: %v", err)
	}
}

func TestResolveServiceNameCaseInsensitive(t *testing.T) {
	service, err := ResolveServiceName("API-BACKEND", Default().Services)
	if err != nil {
		t.Fatalf("expected service resolution, got error: %v", err)
	}
	if service != "api-backend" {
		t.Fatalf("unexpected service resolution: %q", service)
	}
}
