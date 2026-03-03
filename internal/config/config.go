package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Environments map[string][]string `yaml:"environments"`
	Services     []string            `yaml:"services"`
}

func Default() Config {
	return Config{
		Environments: map[string][]string{
			"staging":    {"staging-01.example.com", "staging-02.example.com"},
			"production": {"prod-01.example.com", "prod-02.example.com", "prod-03.example.com"},
		},
		Services: []string{"api-backend", "frontend", "worker", "database"},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	candidates := ResolveCandidates(path)

	filePath, found, err := FirstExistingPath(candidates)
	if err != nil {
		return Config{}, err
	}
	if !found {
		return cfg, nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return Config{}, fmt.Errorf("falha ao ler arquivo de configuracao %q: %w", filePath, err)
	}

	var parsed Config
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return Config{}, fmt.Errorf("falha ao interpretar yaml %q: %w", filePath, err)
	}

	if len(parsed.Services) > 0 {
		cfg.Services = NormalizeSlice(parsed.Services)
	}
	if len(parsed.Environments) > 0 {
		cfg.Environments = make(map[string][]string, len(parsed.Environments))
		for environment, servers := range parsed.Environments {
			trimmedEnvironment := strings.TrimSpace(environment)
			if trimmedEnvironment == "" {
				continue
			}
			trimmedServers := NormalizeSlice(servers)
			if len(trimmedServers) == 0 {
				continue
			}
			cfg.Environments[trimmedEnvironment] = trimmedServers
		}
	}

	if len(cfg.Services) == 0 {
		return Config{}, errors.New("nenhum servico disponivel na configuracao")
	}
	if len(cfg.Environments) == 0 {
		return Config{}, errors.New("nenhum ambiente disponivel na configuracao")
	}

	return cfg, nil
}

func ResolveCandidates(path string) []string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil
	}
	if strings.EqualFold(trimmed, "deploy.yaml") {
		return []string{"deploy.yaml", "deploy.yml"}
	}
	return []string{trimmed}
}

func FirstExistingPath(paths []string) (string, bool, error) {
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", false, fmt.Errorf("erro ao verificar %q: %w", path, err)
		}
		if info.IsDir() {
			continue
		}
		return path, true, nil
	}
	return "", false, nil
}

func NormalizeSlice(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func ResolveServiceName(service string, services []string) (string, error) {
	trimmedService := strings.TrimSpace(service)
	if trimmedService == "" {
		return "", errors.New("service nao pode ser vazio")
	}

	for _, configuredService := range services {
		if strings.EqualFold(trimmedService, configuredService) {
			return configuredService, nil
		}
	}

	available := make([]string, 0, len(services))
	available = append(available, services...)
	sort.Strings(available)
	return "", fmt.Errorf("service %q nao configurado. permitidos: %s", trimmedService, strings.Join(available, ", "))
}

func ResolveServers(environment string, environments map[string][]string) (string, []string, error) {
	trimmedEnvironment := strings.TrimSpace(environment)
	if trimmedEnvironment == "" {
		trimmedEnvironment = "staging"
	}

	for configuredEnvironment, servers := range environments {
		if strings.EqualFold(trimmedEnvironment, configuredEnvironment) {
			if len(servers) == 0 {
				break
			}
			return configuredEnvironment, servers, nil
		}
	}

	available := make([]string, 0, len(environments))
	for configuredEnvironment := range environments {
		available = append(available, configuredEnvironment)
	}
	sort.Strings(available)
	return "", nil, fmt.Errorf("ambiente %q nao encontrado. disponiveis: %s", trimmedEnvironment, strings.Join(available, ", "))
}

func FilterEnvironments(environment string, environments map[string][]string) (map[string][]string, error) {
	trimmedEnvironment := strings.TrimSpace(environment)
	if trimmedEnvironment == "" {
		return environments, nil
	}

	resolvedEnvironment, servers, err := ResolveServers(trimmedEnvironment, environments)
	if err != nil {
		return nil, err
	}

	return map[string][]string{resolvedEnvironment: servers}, nil
}
