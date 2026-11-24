package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Kaua-KGzin/arch-deploy/internal/config"
	"github.com/Kaua-KGzin/arch-deploy/internal/runner"
)

func init() {
	register(&Provider{
		Name:     "gh-pages",
		Summary:  "Publica uma pasta estatica no branch gh-pages (GitHub Pages) com git",
		Requires: fixed("git"),
		Validate: func(t *config.Target) error { return require("output (pasta a publicar)", t.Output) },
		Deploy: func(e Env, _ string) ([]runner.Cmd, error) {
			branch := e.T.Branch
			if branch == "" {
				branch = "gh-pages"
			}
			remote := e.T.Remote
			if remote == "" {
				remote = "origin"
			}
			out := e.path(e.T.Output)
			// A separate git dir publishes the folder as-is, without touching
			// the project's own repository or leaving a .git inside the build.
			gitDir := filepath.Join(e.Cfg.Dir, ".arch-deploy", "gh-pages.git")
			git := func(args ...string) runner.Cmd {
				return e.cmd("git", append([]string{"--git-dir=" + gitDir, "--work-tree=" + out}, args...)...)
			}
			remoteURL := e.cmd("git", "remote", "get-url", remote)
			return []runner.Cmd{
				{
					Label: "garante " + filepath.Join(e.T.Output, ".nojekyll"),
					Func: func(ctx context.Context, w io.Writer) error {
						return touch(filepath.Join(out, ".nojekyll"))
					},
				},
				{
					Label: "prepara repositorio temporario " + gitDir,
					Func: func(ctx context.Context, w io.Writer) error {
						if err := os.RemoveAll(gitDir); err != nil {
							return err
						}
						return os.MkdirAll(filepath.Dir(gitDir), 0o755)
					},
				},
				git("init", "-q", "-b", branch),
				{
					// When the output is the project root, keep the temporary
					// repository and the tool's own state out of the site.
					Label: "ignora .arch-deploy/ no que sera publicado",
					Func: func(ctx context.Context, w io.Writer) error {
						if err := os.MkdirAll(filepath.Join(gitDir, "info"), 0o755); err != nil {
							return err
						}
						return os.WriteFile(filepath.Join(gitDir, "info", "exclude"), []byte(".arch-deploy/\n"), 0o644)
					},
				},
				git("add", "-A"),
				{
					Label: fmt.Sprintf("git commit -m %q", "deploy "+e.Vars["version"]+" via arch-deploy"),
					Func: func(ctx context.Context, w io.Writer) error {
						args := []string{"commit", "-q", "-m", "deploy " + e.Vars["version"] + " via arch-deploy"}
						// CI runners often have no git identity; only then use a
						// neutral one, so local deploys keep the author.
						if email, _ := output(ctx, e.cmd("git", "config", "user.email")); strings.TrimSpace(email) == "" {
							args = append([]string{"-c", "user.name=arch-deploy", "-c", "user.email=arch-deploy@users.noreply.github.com"}, args...)
						}
						return runner.Exec{}.Run(ctx, git(args...), w)
					},
				},
				{
					Label: fmt.Sprintf("git push --force <%s> %s", remote, branch),
					Func: func(ctx context.Context, w io.Writer) error {
						urlOut, err := output(ctx, remoteURL)
						if err != nil {
							return fmt.Errorf("lendo a URL do remote %q: %w", remote, err)
						}
						address := strings.TrimSpace(urlOut)
						push := git("push", "--force", "-q", address, "HEAD:"+branch)
						// The URL may embed a token (https://x-access-token:...@).
						push.Secrets = append(push.Secrets, address)
						return runner.Exec{}.Run(ctx, push, w)
					},
				},
			}, nil
		},
	})

	register(&Provider{
		Name:    "webhook",
		Summary: "Dispara um deploy hook HTTP (Render, Coolify, Dokploy, Netlify build hooks...)",
		Validate: func(t *config.Target) error {
			if err := require("hook (URL)", t.Hook); err != nil {
				return err
			}
			if !strings.HasPrefix(t.Hook, "https://") && !strings.HasPrefix(t.Hook, "http://") {
				return errors.New("`hook` precisa ser uma URL http(s)")
			}
			return nil
		},
		Deploy: func(e Env, _ string) ([]runner.Cmd, error) {
			hook := e.Vars.Expand(e.T.Hook)
			method := strings.ToUpper(e.T.Method)
			if method == "" {
				method = http.MethodPost
			}
			return []runner.Cmd{{
				Label: method + " " + redactURL(hook),
				Func: func(ctx context.Context, w io.Writer) error {
					ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
					defer cancel()
					req, err := http.NewRequestWithContext(ctx, method, hook, nil)
					if err != nil {
						return err
					}
					req.Header.Set("User-Agent", "arch-deploy")
					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						// *url.Error carries the full URL, and with it the
						// hook's secret: report only the cause.
						var urlErr *url.Error
						if errors.As(err, &urlErr) {
							return fmt.Errorf("webhook %s: %v", redactURL(hook), urlErr.Err)
						}
						return errors.New("webhook falhou")
					}
					defer resp.Body.Close()
					body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
					if resp.StatusCode >= 300 {
						return fmt.Errorf("webhook respondeu %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
					}
					fmt.Fprintf(w, "webhook respondeu %d\n", resp.StatusCode)
					return nil
				},
			}}, nil
		},
	})
}

func touch(path string) error {
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		return fmt.Errorf("pasta de output nao existe: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

func output(ctx context.Context, c runner.Cmd) (string, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	out, err := cmd.Output()
	return string(out), err
}

// redactURL hides the secret part of a deploy hook (path tokens and query).
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<hook>"
	}
	return u.Scheme + "://" + u.Host + "/****"
}
