// Package detect looks at a project folder and proposes deploy targets, so
// `arch-deploy init` starts from a config that already fits the project.
package detect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Kaua-KGzin/arch-deploy/internal/config"
)

// Suggestion is a target proposed for the project, with why it was chosen.
type Suggestion struct {
	Name   string
	Target *config.Target
	Reason string
}

// Detect returns the suggested targets for dir, most specific first. It
// never returns an empty list: unknown projects get an `exec` template.
func Detect(dir string) []Suggestion {
	has := func(names ...string) string {
		for _, n := range names {
			if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
				return n
			}
		}
		return ""
	}
	project := strings.ToLower(filepath.Base(dir))
	var out []Suggestion
	add := func(name, reason string, t *config.Target) {
		out = append(out, Suggestion{Name: uniqueName(out, name), Target: t, Reason: reason})
	}
	// The first platform found owns "production"; any other one is named
	// after itself so both stay obvious (e.g. vercel + render).
	platformTarget := func(alias, reason string, t *config.Target) {
		name := "production"
		for _, s := range out {
			if s.Name == "production" {
				name = alias
			}
		}
		t.Confirm = true // these all publish to production
		add(name, reason, t)
	}

	platform := false
	if f := has("vercel.json", ".vercel/project.json"); f != "" {
		add("production", f+" encontrado", &config.Target{Provider: "vercel"})
		add("preview", "deploys de preview na Vercel", &config.Target{Provider: "vercel", Prod: boolPtr(false)})
		platform = true
	}
	if f := has("netlify.toml"); f != "" {
		platformTarget("netlify", f+" encontrado", &config.Target{Provider: "netlify"})
		platform = true
	}
	if f := has("fly.toml"); f != "" {
		platformTarget("fly", f+" encontrado", &config.Target{Provider: "fly", App: tomlString(filepath.Join(dir, f), "app")})
		platform = true
	}
	if f := has("wrangler.toml", "wrangler.json", "wrangler.jsonc"); f != "" {
		platformTarget("cloudflare", f+" encontrado", &config.Target{Provider: "cloudflare"})
		platform = true
	}
	if f := has("firebase.json"); f != "" {
		platformTarget("firebase", f+" encontrado", &config.Target{Provider: "firebase"})
		platform = true
	}
	if f := has("railway.json", "railway.toml"); f != "" {
		platformTarget("railway", f+" encontrado", &config.Target{Provider: "railway"})
		platform = true
	}
	if f := has("render.yaml"); f != "" {
		platformTarget("render", f+" encontrado (deploy hook do Render)", &config.Target{Provider: "webhook", Hook: "${RENDER_DEPLOY_HOOK}"})
		platform = true
	}

	if f := has("compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"); f != "" {
		add("server", f+" encontrado (use `context` para um servidor remoto)", &config.Target{
			Provider: "compose", File: f, Pull: true,
		})
	}
	if f := has("Dockerfile"); f != "" {
		image := project
		if owner := githubOwner(dir); owner != "" {
			image = "ghcr.io/" + strings.ToLower(owner) + "/" + project
		}
		add("image", f+" encontrado", &config.Target{Provider: "docker", Image: image})
	}
	if f := has("k8s", "kubernetes", "deploy/k8s", "manifests"); f != "" {
		add("cluster", "pasta "+f+"/ com manifests", &config.Target{Provider: "kubernetes", Manifests: []string{f}, App: project})
	}

	pkg := readPackageJSON(filepath.Join(dir, "package.json"))
	switch {
	case pkg != nil && !platform && pkg.uses("next", "nuxt", "@sveltejs/kit", "astro", "@remix-run/react"):
		add("production", "app "+pkg.framework()+" (package.json)", &config.Target{Provider: "vercel"})
		add("preview", "deploys de preview na Vercel", &config.Target{Provider: "vercel", Prod: boolPtr(false)})
	case pkg != nil && !platform && pkg.uses("vite", "react-scripts", "@vue/cli-service", "parcel"):
		add("pages", "site estatico "+pkg.framework()+" (package.json)", &config.Target{
			Provider: "gh-pages", Output: staticOutput(pkg), Before: []string{pkg.installCmd(dir), pkg.runCmd(dir, "build")},
		})
	}

	switch {
	case has(".goreleaser.yaml", ".goreleaser.yml") != "":
		add("release", "goreleaser configurado", &config.Target{Provider: "exec", Command: []string{"goreleaser release --clean"}})
	case has("go.mod") != "":
		add("release", "modulo Go (binario na release do GitHub)", &config.Target{
			Provider: "github-release",
			Before:   []string{"go test ./...", "go build -o dist/" + project + "{{exe}} ."},
			Assets:   []string{"dist/*"},
			Confirm:  true,
		})
	case has("Cargo.toml") != "":
		name := tomlString(filepath.Join(dir, "Cargo.toml"), "name")
		if name == "" {
			name = project
		}
		add("release", "crate Rust (binario na release do GitHub)", &config.Target{
			Provider: "github-release",
			Before:   []string{"cargo test", "cargo build --release"},
			Assets:   []string{"target/release/" + name + "{{exe}}"},
			Confirm:  true,
		})
	case has("pom.xml") != "" && has("Dockerfile") == "":
		add("release", "projeto Maven (jar na release do GitHub)", &config.Target{
			Provider: "github-release", Before: []string{"mvn -B package"}, Assets: []string{"target/*.jar"}, Confirm: true,
		})
	}

	if f := has("mkdocs.yml"); f != "" {
		add("docs", f+" encontrado", &config.Target{Provider: "gh-pages", Output: "site", Before: []string{"mkdocs build"}})
	} else if pkg == nil && has("index.html") != "" && !platform {
		add("pages", "site estatico (index.html na raiz)", &config.Target{Provider: "gh-pages", Output: "."})
	}

	if len(out) == 0 {
		add("production", "nenhum tipo de projeto reconhecido: ajuste os comandos", &config.Target{
			Provider: "exec", Command: []string{"echo \"troque por seus comandos de deploy\""},
		})
	}
	// Anything that ends in production asks before running.
	for _, s := range out {
		if s.Name == "production" {
			s.Target.Confirm = true
		}
	}
	return out
}

func uniqueName(existing []Suggestion, name string) string {
	taken := func(n string) bool {
		for _, s := range existing {
			if s.Name == n {
				return true
			}
		}
		return false
	}
	if !taken(name) {
		return name
	}
	for i := 2; ; i++ {
		candidate := name + "-" + strconv.Itoa(i)
		if !taken(candidate) {
			return candidate
		}
	}
}

func boolPtr(b bool) *bool { return &b }

type packageJSON struct {
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

func readPackageJSON(path string) *packageJSON {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var p packageJSON
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	return &p
}

func (p *packageJSON) uses(names ...string) bool {
	for _, n := range names {
		if _, ok := p.Dependencies[n]; ok {
			return true
		}
		if _, ok := p.DevDependencies[n]; ok {
			return true
		}
	}
	return false
}

func (p *packageJSON) framework() string {
	for _, n := range []string{"next", "nuxt", "@sveltejs/kit", "astro", "@remix-run/react", "vite", "react-scripts", "@vue/cli-service", "parcel"} {
		if p.uses(n) {
			return n
		}
	}
	return "node"
}

func staticOutput(p *packageJSON) string {
	if p.uses("react-scripts") {
		return "build"
	}
	return "dist"
}

func packageManager(dir string) string {
	for file, pm := range map[string]string{"pnpm-lock.yaml": "pnpm", "yarn.lock": "yarn", "bun.lockb": "bun", "bun.lock": "bun"} {
		if _, err := os.Stat(filepath.Join(dir, file)); err == nil {
			return pm
		}
	}
	return "npm"
}

func (p *packageJSON) installCmd(dir string) string {
	switch pm := packageManager(dir); pm {
	case "npm":
		return "npm ci"
	case "yarn":
		return "yarn install --frozen-lockfile"
	default:
		return pm + " install --frozen-lockfile"
	}
}

func (p *packageJSON) runCmd(dir, script string) string {
	return packageManager(dir) + " run " + script
}

// tomlString reads a top-level `key = "value"` from a TOML file (enough for
// fly.toml's app and Cargo.toml's name).
func tomlString(path, key string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	re := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(key) + `\s*=\s*["']([^"']+)["']`)
	if m := re.FindStringSubmatch(string(raw)); m != nil {
		return m[1]
	}
	return ""
}

var remotePattern = regexp.MustCompile(`github\.com[:/]([^/]+)/`)

// githubOwner reads the owner of the origin remote from .git/config.
func githubOwner(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, ".git", "config"))
	if err != nil {
		return ""
	}
	if m := remotePattern.FindStringSubmatch(string(raw)); m != nil {
		return m[1]
	}
	return ""
}
