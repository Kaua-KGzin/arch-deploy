package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kaua-KGzin/arch-deploy/internal/history"
)

func run(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	wd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	// A pipe is what CI gives: not a terminal, so confirmations refuse.
	stdin, w, _ := os.Pipe()
	w.Close()
	defer stdin.Close()
	var out bytes.Buffer
	cmd := newRoot(&out, stdin)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestInitWritesConfigThatValidates(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"dependencies":{"next":"15"}}`), 0o644)

	out, err := run(t, dir, "init")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "production") {
		t.Fatalf("saida: %s", out)
	}
	if _, err := run(t, dir, "init"); err == nil {
		t.Fatal("init de novo sem --force deveria falhar")
	}
	if out, err := run(t, dir, "validate"); err != nil {
		t.Fatalf("validate: %v\n%s", err, out)
	}
	gitignore, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(gitignore), ".arch-deploy/") {
		t.Fatal(".gitignore sem .arch-deploy/")
	}
}

func TestExecDeployRunsForRealAndRecordsHistory(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "arch-deploy.yaml"), []byte(`
project: demo
targets:
  local:
    provider: exec
    before: ["echo building {{target}}"]
    command: ["echo shipping {{project}}"]
`), 0o644)

	out, err := run(t, dir, "plan", "local")
	if err != nil || !strings.Contains(out, "echo shipping demo") {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	if entries, _ := history.Read(dir); len(entries) != 0 {
		t.Fatal("plan nao deveria gravar historico")
	}

	out, err = run(t, dir, "deploy", "--no-hub")
	if err != nil {
		t.Fatalf("deploy: %v\n%s", err, out)
	}
	if !strings.Contains(out, "shipping demo") || !strings.Contains(out, "deploy local OK") {
		t.Fatalf("saida: %s", out)
	}
	entries, _ := history.Read(dir)
	if len(entries) != 1 || !entries[0].OK || entries[0].Target != "local" {
		t.Fatalf("historico: %+v", entries)
	}
}

func TestFailingDeployReturnsError(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "arch-deploy.yaml"), []byte(`
targets:
  broken:
    provider: exec
    command: ["exit 3"]
`), 0o644)
	out, err := run(t, dir, "deploy", "broken", "--no-hub")
	if err == nil || !strings.Contains(out, "FALHOU") {
		t.Fatalf("err=%v\n%s", err, out)
	}
}

func TestConfirmRefusesWithoutTTY(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "arch-deploy.yaml"), []byte(`
targets:
  production:
    provider: exec
    confirm: true
    command: ["echo nope"]
`), 0o644)
	_, err := run(t, dir, "deploy", "production")
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err = %v", err)
	}
}
