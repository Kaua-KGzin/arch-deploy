package provider

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kaua-KGzin/arch-deploy/internal/config"
	"github.com/Kaua-KGzin/arch-deploy/internal/vars"
)

func testVars() vars.Vars {
	return vars.Vars{"project": "shop", "target": "prod", "version": "1.2.3", "commit": "abc1234", "branch": "main", "release": "20260101-000000-abc1234", "exe": "", "host": ""}
}

func render(t *testing.T, target *config.Target, unit string) string {
	t.Helper()
	cfg := &config.Config{Project: "shop", Dir: "/work", Targets: map[string]*config.Target{"prod": target}}
	p, err := Get(target.Provider)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(target); err != nil {
		t.Fatalf("validate: %v", err)
	}
	cmds, err := p.Deploy(NewEnv(cfg, "prod", testVars()), unit)
	if err != nil {
		t.Fatal(err)
	}
	lines := make([]string, len(cmds))
	for i, c := range cmds {
		lines[i] = c.String()
	}
	return filepath.ToSlash(strings.Join(lines, "\n"))
}

func TestVercel(t *testing.T) {
	t.Setenv("VERCEL_TOKEN", "sekret")
	got := render(t, &config.Target{Provider: "vercel", Scope: "team"}, Local)
	if got != "vercel deploy --yes --prod --scope team --token ****" {
		t.Fatalf("got %q", got)
	}
	no := false
	if got := render(t, &config.Target{Provider: "vercel", Prod: &no}, Local); strings.Contains(got, "--prod") {
		t.Fatalf("preview nao deveria ter --prod: %q", got)
	}
}

func TestSSHReleasesLayout(t *testing.T) {
	got := render(t, &config.Target{
		Provider: "ssh", Hosts: []string{"a.example.com"}, User: "deploy", Port: 2222,
		Path: "/srv/shop", Output: "dist", Run: []string{"sudo systemctl restart shop"}, Keep: 3,
	}, "a.example.com")
	for _, want := range []string{
		`ssh -o BatchMode=yes -o ConnectTimeout=15 -p 2222 deploy@a.example.com "mkdir -p '/srv/shop/releases'"`,
		"scp -o BatchMode=yes -o ConnectTimeout=15 -P 2222 -r /work/dist deploy@a.example.com:/srv/shop/releases/.upload-20260101-000000-abc1234",
		"ln -sfn",
		"sudo systemctl restart shop",
		"tail -n +4",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("faltou %q em:\n%s", want, got)
		}
	}
}

func TestSSHValidation(t *testing.T) {
	p, _ := Get("ssh")
	bad := []*config.Target{
		{Provider: "ssh", Run: []string{"x"}},
		{Provider: "ssh", Hosts: []string{"h"}},
		{Provider: "ssh", Hosts: []string{"h"}, Output: "dist"},
		{Provider: "ssh", Hosts: []string{"h"}, Output: "dist", Path: "relative"},
	}
	for i, target := range bad {
		if err := p.Validate(target); err == nil {
			t.Errorf("caso %d deveria falhar", i)
		}
	}
}

func TestDockerDefaultsToVersionAndLatest(t *testing.T) {
	got := render(t, &config.Target{Provider: "docker", Image: "ghcr.io/me/{{project}}", BuildArgs: map[string]string{"V": "{{version}}"}}, Local)
	want := strings.Join([]string{
		"docker build -t ghcr.io/me/shop:1.2.3 -t ghcr.io/me/shop:latest --build-arg V=1.2.3 .",
		"docker push ghcr.io/me/shop:1.2.3",
		"docker push ghcr.io/me/shop:latest",
	}, "\n")
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestGithubReleaseAndKubernetes(t *testing.T) {
	got := render(t, &config.Target{Provider: "github-release", Draft: true}, Local)
	if got != "gh release create v1.2.3 --title v1.2.3 --generate-notes --draft" {
		t.Fatalf("release: %q", got)
	}
	got = render(t, &config.Target{Provider: "kubernetes", App: "shop", Namespace: "prod", Image: "img:{{version}}"}, Local)
	if !strings.Contains(got, "kubectl --namespace prod set image deployment/shop '*=img:1.2.3'") || !strings.Contains(got, "rollout status") {
		t.Fatalf("kubernetes: %q", got)
	}
}

func TestGhPagesUsesSeparateGitDir(t *testing.T) {
	got := render(t, &config.Target{Provider: "gh-pages", Output: "dist"}, Local)
	if !strings.Contains(got, "--git-dir=/work/.arch-deploy/gh-pages.git --work-tree=/work/dist init -q -b gh-pages") {
		t.Fatalf("got:\n%s", got)
	}
	if !strings.Contains(got, "git push --force <origin> gh-pages") {
		t.Fatalf("push faltando:\n%s", got)
	}
}

func TestWebhookRedactsSecretURL(t *testing.T) {
	got := render(t, &config.Target{Provider: "webhook", Hook: "https://api.render.com/deploy/srv-123?key=abc"}, Local)
	if got != "POST https://api.render.com/****" {
		t.Fatalf("got %q", got)
	}
}

func TestSecretEnvValuesAreMaskedInHooks(t *testing.T) {
	t.Setenv("AD_DEPLOY_TOKEN", "super-secret-value")
	cfg, err := config.Parse([]byte("targets:\n  prod:\n    provider: exec\n    command: [\"curl -H 'auth: ${AD_DEPLOY_TOKEN}' https://x\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Dir = "/work"
	p, _ := Get("exec")
	cmds, _ := p.Deploy(NewEnv(cfg, "prod", testVars()), Local)
	if shown := cmds[0].String(); strings.Contains(shown, "super-secret-value") || !strings.Contains(shown, "****") {
		t.Fatalf("segredo apareceu: %s", shown)
	}
}

func TestValidateConfigListsEveryProblem(t *testing.T) {
	cfg := &config.Config{Targets: map[string]*config.Target{
		"a": {Provider: "nope"},
		"b": {Provider: "exec"},
	}}
	err := ValidateConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), `"a"`) || !strings.Contains(err.Error(), `"b"`) {
		t.Fatalf("err = %v", err)
	}
}
