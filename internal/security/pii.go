package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const piiCipherPrefix = "enc:v1:"

var (
	ErrInvalidPIIKey      = errors.New("chave de criptografia PII deve resultar em 32 bytes (hex 64 chars, base64 de 32 bytes ou texto de 32 bytes)")
	ErrMissingPIIKey      = errors.New("PII_ENCRYPTION_KEY é obrigatória em staging/production")
	ErrInvalidCiphertext  = errors.New("ciphertext PII inválido ou corrompido")
	ErrUnsupportedVersion = errors.New("versão de cifragem PII não suportada")
)

// PIICipher define o contrato de cifragem simétrica autenticada para dados de contato brutos.
type PIICipher interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(encoded string) (string, error)
}

// ContactInfo representa dados brutos de identificação do contato protegidos em EncryptedContactInfo.
type ContactInfo struct {
	Channel       string `json:"channel,omitempty"`
	RawIdentifier string `json:"raw_identifier,omitempty"`
	DisplayName   string `json:"display_name,omitempty"`
}

// AESGCMCipher implementa cifragem AES-256-GCM com prefixo versionado ("enc:v1:").
type AESGCMCipher struct {
	aead cipher.AEAD
	rng  io.Reader
}

// DevDefaultPIIKey retorna uma chave determinística exclusiva para desenvolvimento e testes locais.
func DevDefaultPIIKey() []byte {
	sum := sha256.Sum256([]byte("whatshelper-local-dev-pii-key-v1"))
	return sum[:]
}

// ParsePIIKey valida e converte uma chave configurada (hex, base64 ou 32 bytes literais) em exatamente 32 bytes.
func ParsePIIKey(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: chave vazia", ErrInvalidPIIKey)
	}
	if len(trimmed) == 64 {
		if decoded, err := hex.DecodeString(trimmed); err == nil && len(decoded) == 32 {
			return decoded, nil
		}
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if decoded, err := enc.DecodeString(trimmed); err == nil && len(decoded) == 32 {
			return decoded, nil
		}
	}
	if len(trimmed) == 32 {
		return []byte(trimmed), nil
	}
	return nil, ErrInvalidPIIKey
}

// NewAESGCMCipher constrói um AESGCMCipher a partir de uma chave de 32 bytes.
func NewAESGCMCipher(key []byte) (*AESGCMCipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: esperado 32 bytes, recebido %d", ErrInvalidPIIKey, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("erro ao inicializar AES: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("erro ao inicializar GCM: %w", err)
	}
	return &AESGCMCipher{
		aead: aead,
		rng:  rand.Reader,
	}, nil
}

// NewPIICipherFromEnv resolve a chave conforme APP_ENV, bloqueando chave ausente em staging/production.
func NewPIICipherFromEnv(rawKey, environment string) (*AESGCMCipher, error) {
	env := strings.ToLower(strings.TrimSpace(environment))
	trimmed := strings.TrimSpace(rawKey)
	if trimmed == "" {
		if env == "staging" || env == "production" {
			return nil, ErrMissingPIIKey
		}
		return NewAESGCMCipher(DevDefaultPIIKey())
	}
	key, err := ParsePIIKey(trimmed)
	if err != nil {
		return nil, err
	}
	return NewAESGCMCipher(key)
}

// Encrypt cifra o texto claro em "enc:v1:<base64(nonce||ciphertext)>". Strings vazias retornam vazio.
func (c *AESGCMCipher) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(c.rng, nonce); err != nil {
		return "", fmt.Errorf("falha ao gerar nonce PII: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return piiCipherPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt valida versão, autenticidade e decifra um payload produzido por Encrypt.
func (c *AESGCMCipher) Decrypt(encoded string) (string, error) {
	if strings.TrimSpace(encoded) == "" {
		return "", nil
	}
	if !strings.HasPrefix(encoded, piiCipherPrefix) {
		return "", ErrUnsupportedVersion
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encoded, piiCipherPrefix))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidCiphertext, err)
	}
	nonceSize := c.aead.NonceSize()
	if len(raw) < nonceSize {
		return "", ErrInvalidCiphertext
	}
	nonce, ciphertext := raw[:nonceSize], raw[nonceSize:]
	plain, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidCiphertext, err)
	}
	return string(plain), nil
}

// EncryptContactInfo serializa e cifra os metadados brutos do contato.
func EncryptContactInfo(c PIICipher, info ContactInfo) (string, error) {
	if c == nil {
		return "", errors.New("PIICipher não configurado")
	}
	if strings.TrimSpace(info.RawIdentifier) == "" && strings.TrimSpace(info.DisplayName) == "" {
		return "", nil
	}
	payload, err := json.Marshal(info)
	if err != nil {
		return "", err
	}
	return c.Encrypt(string(payload))
}

// DecryptContactInfo decifra e desserializa os metadados brutos do contato.
func DecryptContactInfo(c PIICipher, encoded string) (ContactInfo, error) {
	if c == nil {
		return ContactInfo{}, errors.New("PIICipher não configurado")
	}
	if strings.TrimSpace(encoded) == "" {
		return ContactInfo{}, nil
	}
	plain, err := c.Decrypt(encoded)
	if err != nil {
		return ContactInfo{}, err
	}
	var info ContactInfo
	if err := json.Unmarshal([]byte(plain), &info); err != nil {
		return ContactInfo{}, fmt.Errorf("%w: JSON inválido: %v", ErrInvalidCiphertext, err)
	}
	return info, nil
}
