# Changelog

Todas as mudancas relevantes deste projeto serao documentadas neste arquivo.

## [1.0.0] - 2026-03-03

### Adicionado
- Nova arquitetura modular com pacotes internos:
  - `internal/cli` para comandos e flags.
  - `internal/config` para carga e validacao do YAML.
  - `internal/deploy` para engine de deploy.
- Comandos operacionais:
  - `plan` para visualizar o plano de execucao sem aplicar alteracoes.
  - `validate-config` para validar a configuracao ativa.
- Pipeline CI em GitHub Actions (`.github/workflows/ci.yml`) com:
  - verificacao de formatacao (`gofmt -l .`),
  - testes (`go test ./...`),
  - build (`go build ./...`).
- Testes unitarios para validacao de configuracao, retry, fail-fast e utilitarios do engine.
- Arquivo `.gitignore` para evitar commit de binarios e arquivos de IDE.

### Melhorado
- Engine de deploy com:
  - execucao paralela com limite de concorrencia (`--max-parallel`),
  - retry com backoff exponencial (`--retries` e `--retry-delay`),
  - fail-fast opcional (`--fail-fast`),
  - resumo final por servidor (status, tentativas e duracao).
- Resolucao de servicos e ambientes sem diferenciar maiusculas/minusculas.
- README completamente reescrito com guia de uso, arquitetura, CI e checklist de publicacao.

### Observacoes
- O projeto permanece com simulacao de deploy (`DeployToServer`) e esta pronto para evoluir para execucao real via SSH.
