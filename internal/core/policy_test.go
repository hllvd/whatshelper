package core_test

import (
	"errors"
	"testing"
	"time"

	"whatshelper/internal/core"
)

func TestTenantPolicy_ValidateAndDefaults(t *testing.T) {
	p := core.TenantPolicy{
		ID:   "demo",
		Name: "Demo",
		Pack: "realestate",
		BusinessHours: core.BusinessHours{
			Enabled: true,
			Days:    []string{"mon", "tue", "wed", "thu", "fri", "sat"},
			Start:   "09:00",
			End:     "18:00",
		},
	}.WithDefaults()

	if p.Mode != core.ModeAuto || p.Timezone != "America/Sao_Paulo" || p.ProactiveEnabled {
		t.Fatalf("unexpected defaults: %+v", p)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("expected valid policy, got %v", err)
	}

	invalid := []core.TenantPolicy{
		{},
		{ID: "demo", Pack: "p", Mode: "unknown", Timezone: "America/Sao_Paulo"},
		{ID: "demo", Pack: "p", Mode: core.ModeAuto, Timezone: "Mars/Olympus"},
		{ID: "demo", Pack: "p", Mode: core.ModeAuto, Timezone: "UTC", BusinessHours: core.BusinessHours{Enabled: true, Days: []string{"mon"}, Start: "18:00", End: "09:00"}},
		{ID: "demo", Pack: "p", Mode: core.ModeAuto, Timezone: "UTC", BusinessHours: core.BusinessHours{Enabled: true, Days: []string{"funday"}, Start: "09:00", End: "18:00"}},
	}
	for i, tc := range invalid {
		if err := tc.Validate(); !errors.Is(err, core.ErrInvalidPolicy) {
			t.Fatalf("case %d: expected ErrInvalidPolicy, got %v", i, err)
		}
	}
}

func TestIsWithinBusinessHours(t *testing.T) {
	p := core.TenantPolicy{
		ID:       "demo",
		Pack:     "realestate",
		Mode:     core.ModeAuto,
		Timezone: "America/Sao_Paulo",
		BusinessHours: core.BusinessHours{
			Enabled: true,
			Days:    []string{"mon", "tue", "wed", "thu", "fri", "sat"},
			Start:   "09:00",
			End:     "18:00",
		},
	}

	loc, _ := time.LoadLocation("America/Sao_Paulo")
	// Quarta-feira 10:00 local -> dentro
	wed10 := time.Date(2026, 11, 11, 10, 0, 0, 0, loc)
	if !core.IsWithinBusinessHours(p, wed10) {
		t.Fatalf("expected wednesday 10:00 within business hours")
	}
	// Quarta-feira 18:00 local -> fora (end exclusivo)
	wed18 := time.Date(2026, 11, 11, 18, 0, 0, 0, loc)
	if core.IsWithinBusinessHours(p, wed18) {
		t.Fatalf("expected wednesday 18:00 outside business hours")
	}
	// Domingo 11:00 local -> fora
	sun11 := time.Date(2026, 11, 15, 11, 0, 0, 0, loc)
	if core.IsWithinBusinessHours(p, sun11) {
		t.Fatalf("expected sunday 11:00 outside business hours")
	}
}
