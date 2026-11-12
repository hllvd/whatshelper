package security_test

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"whatshelper/internal/security"
)

func TestParsePIIKey(t *testing.T) {
	raw32 := strings.Repeat("a", 32)
	hexKey := hex.EncodeToString([]byte(raw32))
	b64Key := base64.StdEncoding.EncodeToString([]byte(raw32))

	for _, tc := range []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "hex 64 chars", input: hexKey},
		{name: "base64 32 bytes", input: b64Key},
		{name: "literal 32 bytes", input: raw32},
		{name: "empty rejected", input: "", wantErr: true},
		{name: "short rejected", input: "abc", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := security.ParsePIIKey(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got key len=%d", len(got))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytes.Equal(got, []byte(raw32)) {
				t.Fatalf("key mismatch")
			}
		})
	}
}

func TestAESGCMCipher_RoundTripAndTamper(t *testing.T) {
	cipherA, err := security.NewAESGCMCipher(security.DevDefaultPIIKey())
	if err != nil {
		t.Fatalf("NewAESGCMCipher failed: %v", err)
	}
	otherKey := make([]byte, 32)
	otherKey[0] = 99
	cipherB, err := security.NewAESGCMCipher(otherKey)
	if err != nil {
		t.Fatalf("NewAESGCMCipher B failed: %v", err)
	}

	plain := "+55 (11) 99999-8888"
	enc1, err := cipherA.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	enc2, err := cipherA.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt 2 failed: %v", err)
	}
	if !strings.HasPrefix(enc1, "enc:v1:") {
		t.Fatalf("expected enc:v1 prefix, got %q", enc1)
	}
	if enc1 == enc2 {
		t.Fatalf("expected distinct nonces for identical plaintexts")
	}

	got, err := cipherA.Decrypt(enc1)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if got != plain {
		t.Fatalf("expected %q, got %q", plain, got)
	}

	if _, err := cipherB.Decrypt(enc1); !errors.Is(err, security.ErrInvalidCiphertext) {
		t.Fatalf("expected ErrInvalidCiphertext with wrong key, got %v", err)
	}

	tampered := enc1[:len(enc1)-2] + "AA"
	if _, err := cipherA.Decrypt(tampered); !errors.Is(err, security.ErrInvalidCiphertext) {
		t.Fatalf("expected ErrInvalidCiphertext on tamper, got %v", err)
	}

	if _, err := cipherA.Decrypt("plaintext-without-prefix"); !errors.Is(err, security.ErrUnsupportedVersion) {
		t.Fatalf("expected ErrUnsupportedVersion, got %v", err)
	}
}

func TestEncryptDecryptContactInfo(t *testing.T) {
	cipher, err := security.NewPIICipherFromEnv("", "development")
	if err != nil {
		t.Fatalf("expected dev fallback cipher, got %v", err)
	}

	want := security.ContactInfo{
		Channel:       "whatsapp",
		RawIdentifier: "+55 11 98765-4321",
		DisplayName:   "Maria Silva",
	}
	encoded, err := security.EncryptContactInfo(cipher, want)
	if err != nil {
		t.Fatalf("EncryptContactInfo failed: %v", err)
	}
	if strings.Contains(encoded, "Maria") || strings.Contains(encoded, "98765") {
		t.Fatalf("encrypted payload leaked plaintext: %s", encoded)
	}

	got, err := security.DecryptContactInfo(cipher, encoded)
	if err != nil {
		t.Fatalf("DecryptContactInfo failed: %v", err)
	}
	if got != want {
		t.Fatalf("expected %+v, got %+v", want, got)
	}
}

func TestNewPIICipherFromEnv_ProductionRequiresKey(t *testing.T) {
	if _, err := security.NewPIICipherFromEnv("", "production"); !errors.Is(err, security.ErrMissingPIIKey) {
		t.Fatalf("expected ErrMissingPIIKey in production, got %v", err)
	}
	if _, err := security.NewPIICipherFromEnv("", "staging"); !errors.Is(err, security.ErrMissingPIIKey) {
		t.Fatalf("expected ErrMissingPIIKey in staging, got %v", err)
	}
}
