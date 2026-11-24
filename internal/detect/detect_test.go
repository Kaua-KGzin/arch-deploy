package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func project(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(path), 0o755)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func byName(s []Suggestion) map[string]Suggestion {
	m := map[string]Suggestion{}
	for _, x := range s {
		m[x.Name] = x
	}
	return m
}

func TestNextAppGoesToVercelWithPreview(t *testing.T) {
	got := byName(Detect(project(t, map[string]string{"package.json": `{"dependencies":{"next":"15"}}`})))
	if got["production"].Target.Provider != "vercel" || !got["production"].Target.Confirm {
		t.Fatalf("%+v", got)
	}
	if got["preview"].Target.Prod == nil || *got["preview"].Target.Prod {
		t.Fatal("preview deveria ter prod: false")
	}
}

func TestViteSiteGoesToGhPagesWithBuild(t *testing.T) {
	got := byName(Detect(project(t, map[string]string{
		"package.json":   `{"devDependencies":{"vite":"6"}}`,
		"pnpm-lock.yaml": "",
	})))
	pages := got["pages"].Target
	if pages == nil || pages.Provider != "gh-pages" || pages.Output != "dist" || pages.Before[1] != "pnpm run build" {
		t.Fatalf("%+v", got)
	}
}

func TestDockerComposeAndGo(t *testing.T) {
	got := byName(Detect(project(t, map[string]string{
		"Dockerfile": "FROM scratch", "compose.yaml": "services: {}", "go.mod": "module x",
		".git/config": "[remote \"origin\"]\n\turl = git@github.com:Kaua-KGzin/thing.git\n",
	})))
	if got["server"].Target.Provider != "compose" {
		t.Errorf("compose: %+v", got["server"])
	}
	if img := got["image"].Target.Image; img == "" || img[:len("ghcr.io/kaua-kgzin/")] != "ghcr.io/kaua-kgzin/" {
		t.Errorf("imagem: %q", img)
	}
	if got["release"].Target.Provider != "github-release" {
		t.Errorf("release: %+v", got["release"])
	}
}

func TestFlyReadsAppName(t *testing.T) {
	got := byName(Detect(project(t, map[string]string{"fly.toml": "app = \"shop-api\"\n"})))
	if got["production"].Target.App != "shop-api" {
		t.Fatalf("%+v", got["production"].Target)
	}
}

func TestUnknownProjectGetsExecTemplate(t *testing.T) {
	got := Detect(project(t, map[string]string{"notes.txt": "hi"}))
	if len(got) != 1 || got[0].Target.Provider != "exec" {
		t.Fatalf("%+v", got)
	}
}
