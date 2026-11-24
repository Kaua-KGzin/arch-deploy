# arch-deploy

Deploy de **qualquer projeto, em qualquer lugar, com um comando**.

```bash
arch-deploy init                 # detecta o projeto e gera o arch-deploy.yaml
arch-deploy plan production      # mostra exatamente o que vai rodar
arch-deploy deploy production    # faz o deploy
arch-deploy rollback production  # volta para a versao anterior
```

Vercel, Netlify, Cloudflare, Fly.io, Railway, Firebase, GitHub Pages, releases
do GitHub, Docker, docker compose, Kubernetes, servidores proprios via SSH,
deploy hooks (Render, Coolify, Dokploy...) ou qualquer comando seu — com o
mesmo fluxo, os mesmos hooks e o mesmo historico.

Faz parte da **linha Arch**: depois de cada deploy, avisa o
[Arch Hub](https://github.com/Kaua-KGzin/System-Arch).

## Por que

Cada plataforma tem a sua CLI, o seu jeito de fazer preview, rollback e
health check. O arch-deploy nao reimplementa nenhuma delas: ele **usa as
ferramentas que voce ja tem** (`vercel`, `docker`, `gh`, `git`, `ssh`,
`kubectl`...) e acrescenta o que falta em todas:

- **plano antes de executar** — `plan` lista os comandos exatos;
- **hooks** `before` / `after` / `rollback` em qualquer provider;
- **varios servidores em paralelo**, com retry (backoff exponencial) e fail-fast;
- **health check** depois do deploy e **rollback automatico** se falhar;
- **confirmacao** para producao (`confirm: true`);
- **historico** local de deploys e rollbacks;
- **nenhuma credencial guardada**: cada CLI usa o proprio login/token.

## Instalacao

Baixe o binario da sua plataforma em
[Releases](https://github.com/Kaua-KGzin/arch-deploy/releases), ou:

```bash
go install github.com/Kaua-KGzin/arch-deploy@latest
```

## Configuracao

`arch-deploy init` gera um ponto de partida. Um exemplo com varios targets:

```yaml
project: shop

hub:                          # opcional: avisa o Arch Hub (ou use ARCHHUB_URL)
  url: ${ARCHHUB_URL}
  system: shop

targets:
  preview:
    provider: vercel
    prod: false

  production:
    provider: vercel
    confirm: true             # pede o nome do target antes de publicar
    healthcheck:
      url: https://shop.example.com/api/health

  vps:
    provider: ssh
    hosts: [app-01.example.com, app-02.example.com]
    user: deploy
    path: /srv/shop
    output: dist              # pasta enviada a cada deploy
    before: ["npm ci", "npm run build"]
    run: ["sudo systemctl restart shop"]
    healthcheck: {url: "https://shop.example.com/health", timeout: 60s}
    auto_rollback: true
    retries: 2
    max_parallel: 2

  image:
    provider: docker
    image: ghcr.io/me/shop    # tags padrao: {{version}} e latest

  release:
    provider: github-release
    before: ["go build -o dist/shop{{exe}} ."]
    assets: ["dist/*"]
```

### Campos de todo target

| Campo | O que faz |
|---|---|
| `provider` | qual plataforma (veja abaixo) — obrigatorio |
| `dir` | pasta onde os comandos rodam (padrao: a do arquivo) |
| `output` | pasta gerada pelo build, quando o provider publica arquivos |
| `env` | variaveis de ambiente extras para os comandos |
| `before` / `after` | comandos locais antes/depois do deploy |
| `rollback` | comandos extras ao desfazer um deploy |
| `healthcheck` | `url`, `expect` (200), `timeout` (60s), `interval` (3s) |
| `auto_rollback` | desfaz sozinho se o deploy ou o health check falharem |
| `confirm` | exige confirmacao (ou `--yes`) |
| `retries`, `retry_delay` | novas tentativas por unidade, com backoff |
| `max_parallel`, `fail_fast` | paralelismo entre hosts e se a 1a falha para o resto |
| `args` | flags extras repassadas para a CLI da plataforma |

### Variaveis

- `{{version}}` (`git describe`), `{{commit}}`, `{{branch}}`, `{{release}}`
  (data-hora-commit), `{{target}}`, `{{host}}`, `{{project}}`, `{{exe}}`
  (`.exe` no Windows).
- `${VAR}` e `${VAR:-padrao}` vem do ambiente ao carregar o arquivo. `$VAR`
  sem chaves fica intacto, para o shell (local ou remoto) resolver.

## Providers

| Provider | Usa | Campos principais | Rollback |
|---|---|---|---|
| `vercel` | `vercel` | `prod` (true), `scope` | `vercel rollback` |
| `netlify` | `netlify` | `output`, `app` (site), `prod` | — |
| `cloudflare` | `wrangler` | Workers; Pages com `output` + `app` | — |
| `fly` | `fly` | `app` | — |
| `railway` | `railway` | `app` (service) | — |
| `firebase` | `firebase` | `app` (projeto) | — |
| `gh-pages` | `git` | `output`, `branch` (gh-pages), `remote` | — |
| `github-release` | `gh` | `tag` (v{{version}}), `assets`, `notes`, `draft` | apaga a release |
| `docker` | `docker` | `image`, `tags`, `dockerfile`, `platform`, `build_args`, `push` | — |
| `compose` | `docker` | `file`, `app` (projeto), `context` (remoto), `pull` | — |
| `kubernetes` | `kubectl` | `manifests`, `app` (deployment), `image`, `namespace`, `context` | `rollout undo` |
| `ssh` | `ssh`, `scp` | `hosts`, `user`, `port`, `identity`, `path`, `output`, `run`, `keep` | volta o `current` |
| `git-push` | `git` | `remote`, `branch` (main), `force` — Heroku, Dokku, Piku | — |
| `webhook` | HTTP | `hook`, `method` (POST) — Render, Coolify, Dokploy | — |
| `exec` | shell | `command` — qualquer outra coisa | via `rollback:` |

Sem rollback nativo, `arch-deploy rollback` roda os comandos de `rollback:`.

### SSH com releases

Com `output`, cada deploy vira uma pasta nova e `current` aponta para ela:

```
/srv/shop/releases/20261124-014056-abc1234/
/srv/shop/releases/20261123-180210-9f8e7d6/
/srv/shop/current -> releases/20261124-014056-abc1234
```

O `run` roda dentro de `current`; as `keep` (5) releases mais novas ficam e o
resto e apagado. `rollback` aponta `current` de volta para a anterior e roda
o `run` de novo.

## Comandos

| Comando | |
|---|---|
| `init [--stdout] [--force]` | detecta o projeto e gera o `arch-deploy.yaml` |
| `plan [target...]` | mostra os comandos sem executar |
| `deploy [target...] [--all] [--dry-run] [--yes]` | faz o deploy |
| `rollback <target>` | volta para a versao anterior |
| `targets` | lista targets, unidades e o ultimo deploy |
| `history [-n 20] [-t target]` | historico de deploys e rollbacks |
| `doctor` | confere ferramentas no PATH, configuracao e Arch Hub |
| `validate` | valida o arquivo |
| `providers` | lista os providers |

O `init` reconhece `vercel.json`, `netlify.toml`, `fly.toml`, `wrangler.toml`,
`firebase.json`, `railway.json`, `render.yaml`, compose, `Dockerfile`, pastas
de manifests Kubernetes, apps Next/Nuxt/SvelteKit/Astro, sites Vite/CRA,
`mkdocs.yml`, sites estaticos, Go, Rust, Maven e GoReleaser.

## Em CI

```yaml
- run: arch-deploy deploy production --yes
  env:
    VERCEL_TOKEN: ${{ secrets.VERCEL_TOKEN }}
    ARCHHUB_URL: ${{ vars.ARCHHUB_URL }}
```

`confirm: true` recusa rodar sem terminal, a menos que venha `--yes`.

## Arch Hub

Com `hub.url` (ou `ARCHHUB_URL`), cada deploy ou rollback bem-sucedido envia
`POST /api/v1/systems/{system}/heartbeat` (com `X-Hub-Token` quando houver
`ARCHHUB_TOKEN`). `system` e por padrao o `project`. Use `--no-hub` para pular.

## Desenvolvimento

```bash
go test ./...
go build -o arch-deploy .
```

```
main.go
internal/
  cli/       comandos (cobra)
  config/    arch-deploy.yaml: schema, ${VAR}, validacao
  provider/  cada provider vira uma lista de comandos
  engine/    hooks, paralelismo, retry, health check, rollback
  runner/    executa (ou so imprime) os comandos
  detect/    deteccao de projeto para o init
  hub/       heartbeat no Arch Hub
  history/   .arch-deploy/history.jsonl
  vars/      {{version}}, {{commit}}...
```

Releases: crie uma tag `vX.Y.Z`; o workflow `release.yml` publica os binarios
de Windows, macOS e Linux (amd64 e arm64).
