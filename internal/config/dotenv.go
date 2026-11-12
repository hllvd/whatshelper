package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// ParseDotEnv interpreta o conteúdo de um arquivo .env de forma pura e determinística.
// Suporta comentários (#), prefixo opcional "export ", aspas simples e duplas.
func ParseDotEnv(content string) (map[string]string, error) {
	out := make(map[string]string)
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")

	for i, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}

		eqIdx := strings.IndexByte(line, '=')
		if eqIdx <= 0 {
			return nil, fmt.Errorf("linha %d inválida no .env: esperado CHAVE=VALOR", i+1)
		}

		key := strings.TrimSpace(line[:eqIdx])
		if key == "" {
			return nil, fmt.Errorf("linha %d inválida no .env: chave vazia", i+1)
		}
		val, err := parseDotEnvValue(strings.TrimSpace(line[eqIdx+1:]))
		if err != nil {
			return nil, fmt.Errorf("linha %d (%s): %w", i+1, key, err)
		}
		out[key] = val
	}

	return out, nil
}

func parseDotEnvValue(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if strings.HasPrefix(raw, `"`) {
		if len(raw) < 2 || !strings.HasSuffix(raw, `"`) {
			return "", errors.New("aspas duplas não fechadas")
		}
		inner := raw[1 : len(raw)-1]
		replacer := strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t", `\"`, `"`, `\\`, `\`)
		return replacer.Replace(inner), nil
	}
	if strings.HasPrefix(raw, `'`) {
		if len(raw) < 2 || !strings.HasSuffix(raw, `'`) {
			return "", errors.New("aspas simples não fechadas")
		}
		return raw[1 : len(raw)-1], nil
	}

	// Em valores sem aspas, remove comentário inline precedido de espaço ("valor # comentário").
	if idx := strings.Index(raw, " #"); idx >= 0 {
		raw = strings.TrimSpace(raw[:idx])
	}
	return raw, nil
}

// LoadDotEnv carrega variáveis de ENV_FILE (padrão ".env") sem sobrescrever variáveis já definidas no ambiente.
// Se o arquivo não existir, retorna nil graciosamente.
func LoadDotEnv(path string) error {
	target := strings.TrimSpace(path)
	if target == "" {
		target = strings.TrimSpace(os.Getenv("ENV_FILE"))
	}
	if target == "" {
		target = ".env"
	}

	data, err := os.ReadFile(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("erro ao ler %q: %w", target, err)
	}

	vars, err := ParseDotEnv(string(data))
	if err != nil {
		return fmt.Errorf("erro ao interpretar %q: %w", target, err)
	}

	for k, v := range vars {
		if _, exists := os.LookupEnv(k); !exists {
			if err := os.Setenv(k, v); err != nil {
				return err
			}
		}
	}
	return nil
}
