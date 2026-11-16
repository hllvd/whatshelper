package core_test

import (
	"testing"
	"time"

	"whatshelper/internal/core"
)

func TestNormalizePhone(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "clean 55 prefix with 11 digits mobile",
			input:    "+55 (11) 99999-8888",
			expected: "11999998888",
		},
		{
			name:     "55 prefix without plus",
			input:    "5511988887777",
			expected: "11988887777",
		},
		{
			name:     "55 prefix with 10 digits landline",
			input:    "+55 11 3333-4444",
			expected: "1133334444",
		},
		{
			name:     "already without 55",
			input:    "11999998888",
			expected: "11999998888",
		},
		{
			name:     "short or other digits",
			input:    "12345",
			expected: "12345",
		},
		{
			name:     "empty",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := core.NormalizePhone(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizePhone(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNormalizeEmail(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{" Teste@Empresa.COM  ", "teste@empresa.com"},
		{"user@domain.org", "user@domain.org"},
		{"", ""},
	}

	for _, tt := range tests {
		if got := core.NormalizeEmail(tt.input); got != tt.expected {
			t.Errorf("NormalizeEmail(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestContact_Validate(t *testing.T) {
	c := core.Contact{
		ID:        "c-1",
		TenantID:  "demo",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}

	cNoID := c
	cNoID.ID = "   "
	if err := cNoID.Validate(); err == nil {
		t.Fatal("expected error on empty ID")
	}

	cNoTenant := c
	cNoTenant.TenantID = ""
	if err := cNoTenant.Validate(); err == nil {
		t.Fatal("expected error on empty TenantID")
	}
}
