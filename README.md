# Deploy CLI

CLI em Go para orquestrar deploys em multiplos ambientes com foco em desempenho, resiliencia e operacao segura.

## O que foi melhorado
- Arquitetura modular em pacotes internos (`cli`, `config`, `deploy`).
- Execucao paralela com limite de concorrencia (`worker pool`).
- Retry com backoff exponencial e limite de atraso.
- Fail-fast opcional para interromper no primeiro erro.
- Comando `plan` para revisar execucao antes do deploy.
- Comando `validate-config` para validar configuracao carregada.
- Status com filtro por ambiente e inventario de hosts.
- Pipeline CI com validacao de `gofmt`, `go test` e `go build`.

## Arquitetura
- `main.go`: entrypoint da aplicacao.
- `internal/cli/root.go`: definicao dos comandos Cobra e parsing de flags.
- `internal/config/config.go`: carregamento e validacao de configuracao YAML.
- `internal/deploy/engine.go`: motor de deploy, retries, concorrencia e sumarios.

## Requisitos
- Go 1.21+

## Instalacao
Linux/macOS:
```bash
go mod tidy
go build -o deploy-cli
```

Windows:
```powershell
go mod tidy
go build -o deploy-cli.exe
```

## Configuracao
Por padrao, a CLI tenta carregar:
1. `deploy.yaml`
2. `deploy.yml`

Se nenhum arquivo for encontrado, usa defaults internos.

Use `deploy.example.yaml` como base:

```yaml
services:
  - api-backend
  - frontend
  - worker
  - database

environments:
  staging:
    - staging-01.example.com
    - staging-02.example.com
  production:
    - prod-01.example.com
    - prod-02.example.com
    - prod-03.example.com
```

## Comandos

### `deploy [service]`
Executa deploy real (ou simulacao com `--dry-run`).

Exemplo:
```bash
./deploy-cli deploy api-backend -e production -p --max-parallel 3 --retries 2 --retry-delay 1s
```

### `plan [service]`
Mostra plano completo sem executar alteracoes.

Exemplo:
```bash
./deploy-cli plan api-backend -e production -p --max-parallel 3
```

### `status`
Mostra servicos cadastrados e inventario dos ambientes.

Exemplo:
```bash
./deploy-cli status
./deploy-cli status -e production --show-servers=true
```

### `validate-config`
Valida a configuracao carregada e exibe resumo.

Exemplo:
```bash
./deploy-cli validate-config
./deploy-cli validate-config -c deploy.example.yaml
```

## Flags

### Flag global
| Flag | Descricao | Padrao |
|---|---|---|
| `-c, --config` | Caminho do YAML de configuracao | `deploy.yaml` |

### Flags de `deploy` e `plan`
| Flag | Descricao | Padrao |
|---|---|---|
| `-e, --env` | Ambiente alvo | `staging` |
| `-p, --parallel` | Ativa execucao paralela | `false` |
| `--max-parallel` | Limite de deploys simultaneos | `4` |
| `-t, --timeout` | Timeout total em segundos | `300` |
| `--dry-run` | Simula deploy sem executar | `false` |
| `--retries` | Tentativas adicionais por host | `1` |
| `--retry-delay` | Delay base entre retries | `2s` |
| `--fail-fast` | Para no primeiro erro | `false` |

### Flags de `status`
| Flag | Descricao | Padrao |
|---|---|---|
| `-e, --env` | Filtra por ambiente | vazio |
| `--show-servers` | Exibe hosts de cada ambiente | `true` |

## Desempenho e operacao
- Use `--parallel` em ambientes com mais de 1 host.
- Ajuste `--max-parallel` de acordo com capacidade de rede e SSH.
- Em ambientes instaveis, aumente `--retries` e ajuste `--retry-delay`.
- Para rollout mais conservador, use `--fail-fast`.
- Sempre rode `plan` antes de deploy em producao.

## CI (GitHub Actions)
Workflow em `.github/workflows/ci.yml` com:
- verificacao de formatacao (`gofmt -l .`),
- execucao de testes (`go test ./...`),
- build (`go build ./...`).

Disparos:
- `push` em `main` e `master`;
- `pull_request`;
- execucao manual (`workflow_dispatch`).

## Desenvolvimento local
Executar testes:
```bash
go test ./...
```

Executar build:
```bash
go build ./...
```

Executar status:
```bash
go run . status
```

## Checklist para subir no Git
1. `go test ./...` sem falhas.
2. `go build ./...` sem falhas.
3. `go run . validate-config` validando o arquivo esperado.
4. Conferir se `deploy.yaml` local nao foi commitado (ignorado no `.gitignore`).
5. Conferir se nao ha binarios (`*.exe`, `deploy-cli`) no commit.
