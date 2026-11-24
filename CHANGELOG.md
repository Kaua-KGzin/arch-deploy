# Changelog

Todas as mudancas relevantes deste projeto serao documentadas neste arquivo.

## [2.0.0] - 2026-11-24

O `deploy-cli` virou **arch-deploy** e passou a fazer deploys de verdade
(a versao 1.x so simulava as etapas).

### Adicionado
- 15 providers: `vercel`, `netlify`, `cloudflare`, `fly`, `railway`,
  `firebase`, `gh-pages`, `github-release`, `docker`, `compose`, `kubernetes`,
  `ssh`, `git-push`, `webhook` e `exec` (que cobre qualquer outra coisa).
- `arch-deploy init`: detecta o tipo de projeto e gera o `arch-deploy.yaml`.
- `ssh` com releases versionadas, `current` por symlink, limpeza das antigas
  e rollback de verdade.
- Hooks `before` / `after` / `rollback`, `healthcheck` com espera e
  `auto_rollback` quando o deploy ou o health check falham.
- `plan` mostra exatamente os comandos que vao rodar.
- `doctor` confere ferramentas e configuracao; `history` e `targets` mostram
  os ultimos deploys (`.arch-deploy/history.jsonl`).
- `confirm: true` pede o nome do target antes de publicar (e exige `--yes`
  em CI).
- Variaveis `{{version}}`, `{{commit}}`, `{{release}}`... e `${VAR:-padrao}`.
- Aviso ao Arch Hub (heartbeat) depois de cada deploy.
- Workflow de release com binarios para Windows, macOS e Linux.

### Mudado
- Modulo Go: `github.com/Kaua-KGzin/arch-deploy`; binario `arch-deploy`.
- Configuracao: `deploy.yaml` (servicos x servidores) virou
  `arch-deploy.yaml` (targets com provider).

### Removido
- O motor simulado (`DeployToServer`) e os comandos `status` /
  `validate-config` (agora `targets` / `validate`).

## [1.0.0] - 2026-03-03

- Arquitetura modular, `plan`, `validate-config`, execucao paralela com
  retry e fail-fast, CI com gofmt/test/build. Deploy ainda simulado.
