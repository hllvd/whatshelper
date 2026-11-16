package core

import (
	"errors"
	"strings"
	"time"
)

var (
	ErrContactNotFound = errors.New("store: contato não encontrado")
	ErrEmptyContactID  = errors.New("contact id is required")
)

// Contact representa a entidade permanente de um contato/cliente com metadados unificados.
type Contact struct {
	ID        string            `json:"id"`
	TenantID  string            `json:"tenant_id"`
	Name      string            `json:"name,omitempty"`
	Email     string            `json:"email,omitempty"`
	Phone     string            `json:"phone,omitempty"`
	Listening bool              `json:"listening,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// Validate verifica a consistência dos campos obrigatórios do contato.
func (c Contact) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return ErrEmptyContactID
	}
	if strings.TrimSpace(c.TenantID) == "" {
		return ErrEmptyTenantID
	}
	return nil
}

// NormalizePhone remove pontuações e o DDI 55 do Brasil se presente,
// padronizando o número nacional (DDD + número).
func NormalizePhone(phone string) string {
	var sb strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			sb.WriteRune(r)
		}
	}
	cleaned := sb.String()
	// Se tiver 12 ou 13 dígitos e começar com 55 (ex: 5511999998888 ou 551188887777), remove o DDI 55
	if strings.HasPrefix(cleaned, "55") && (len(cleaned) == 12 || len(cleaned) == 13) {
		cleaned = cleaned[2:]
	}
	return cleaned
}

// NormalizeEmail converte o endereço para minúsculas e remove espaços vazios.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
