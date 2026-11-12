package core

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// OperationMode define o modo de atuação do bot ao responder.
type OperationMode string

const (
	ModeAuto     OperationMode = "auto"
	ModeApproval OperationMode = "approval"
	ModeDraft    OperationMode = "draft"
)

func (m OperationMode) IsValid() bool {
	switch m {
	case ModeAuto, ModeApproval, ModeDraft:
		return true
	default:
		return false
	}
}

// BusinessHours configura a janela de atendimento do tenant.
type BusinessHours struct {
	Enabled bool     `yaml:"enabled" json:"enabled"`
	Days    []string `yaml:"days" json:"days"`   // mon, tue, wed, thu, fri, sat, sun
	Start   string   `yaml:"start" json:"start"` // HH:MM
	End     string   `yaml:"end" json:"end"`     // HH:MM
}

// RateLimitPolicy define limites de envio global e por conversa.
type RateLimitPolicy struct {
	GlobalPerMinute    int `yaml:"global_per_minute" json:"global_per_minute"`
	PerSourcePerMinute int `yaml:"per_source_per_minute" json:"per_source_per_minute"`
}

// NudgePolicy define parâmetros de agendamento de follow-up proativo.
type NudgePolicy struct {
	DelayMinutes int `yaml:"delay_minutes" json:"delay_minutes"`
	MaxAttempts  int `yaml:"max_attempts" json:"max_attempts"`
}

// TakeoverMode define a reação do bot quando o dono responde diretamente pelo celular (origin=human_phone).
type TakeoverMode string

const (
	TakeoverModePause      TakeoverMode = "pause"
	TakeoverModeRecordOnly TakeoverMode = "record_only"
	TakeoverModeIgnore     TakeoverMode = "ignore"
)

func (m TakeoverMode) IsValid() bool {
	return m == "" || m == TakeoverModePause || m == TakeoverModeRecordOnly || m == TakeoverModeIgnore
}

// TakeoverPolicy configura a política de pausa automática quando um humano assume pelo aparelho (EVT-04).
type TakeoverPolicy struct {
	Mode                 TakeoverMode `yaml:"mode" json:"mode"`
	PauseDurationMinutes int          `yaml:"pause_duration_minutes" json:"pause_duration_minutes"`
}

// TenantPolicy reúne toda a política operacional e de segurança de um tenant.
type TenantPolicy struct {
	ID               string          `yaml:"id" json:"id"`
	Name             string          `yaml:"name" json:"name"`
	Pack             string          `yaml:"pack" json:"pack"`
	Mode             OperationMode   `yaml:"mode" json:"mode"`
	Timezone         string          `yaml:"timezone" json:"timezone"`
	BusinessHours    BusinessHours   `yaml:"business_hours" json:"business_hours"`
	OutOfHoursReply  string          `yaml:"out_of_hours_reply" json:"out_of_hours_reply"`
	ProactiveEnabled bool            `yaml:"proactive_enabled" json:"proactive_enabled"`
	BotEnabled       bool            `yaml:"bot_enabled" json:"bot_enabled"`
	RateLimit        RateLimitPolicy `yaml:"rate_limit" json:"rate_limit"`
	Nudge            NudgePolicy     `yaml:"nudge" json:"nudge"`
	Takeover         TakeoverPolicy  `yaml:"takeover" json:"takeover"`
}

var (
	ErrInvalidPolicy = errors.New("política do tenant inválida")
)

// WithDefaults preenche campos omitidos com valores padrão seguros sem mutar o original.
func (p TenantPolicy) WithDefaults() TenantPolicy {
	out := p
	if out.Mode == "" {
		out.Mode = ModeAuto
	}
	if strings.TrimSpace(out.Timezone) == "" {
		out.Timezone = "America/Sao_Paulo"
	}
	if out.RateLimit.GlobalPerMinute == 0 {
		out.RateLimit.GlobalPerMinute = 60
	}
	if out.RateLimit.PerSourcePerMinute == 0 {
		out.RateLimit.PerSourcePerMinute = 10
	}
	if out.Nudge.DelayMinutes == 0 {
		out.Nudge.DelayMinutes = 60
	}
	if out.Nudge.MaxAttempts == 0 {
		out.Nudge.MaxAttempts = 2
	}
	if out.Takeover.Mode == "" {
		out.Takeover.Mode = TakeoverModePause
	}
	if out.Takeover.PauseDurationMinutes == 0 {
		out.Takeover.PauseDurationMinutes = 60
	}
	return out
}

// Validate valida todos os campos da política na fronteira de entrada.
func (p TenantPolicy) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("%w: id é obrigatório", ErrInvalidPolicy)
	}
	if strings.TrimSpace(p.Pack) == "" {
		return fmt.Errorf("%w: pack é obrigatório", ErrInvalidPolicy)
	}
	if !p.Mode.IsValid() {
		return fmt.Errorf("%w: mode %q inválido (use auto, approval ou draft)", ErrInvalidPolicy, p.Mode)
	}
	if _, err := time.LoadLocation(p.Timezone); err != nil {
		return fmt.Errorf("%w: timezone %q inválido: %v", ErrInvalidPolicy, p.Timezone, err)
	}
	if p.BusinessHours.Enabled {
		if len(p.BusinessHours.Days) == 0 {
			return fmt.Errorf("%w: business_hours.days obrigatório quando habilitado", ErrInvalidPolicy)
		}
		for _, d := range p.BusinessHours.Days {
			if _, ok := parseWeekday(d); !ok {
				return fmt.Errorf("%w: dia inválido em business_hours: %q", ErrInvalidPolicy, d)
			}
		}
		startMin, err := parseHHMM(p.BusinessHours.Start)
		if err != nil {
			return fmt.Errorf("%w: business_hours.start inválido: %v", ErrInvalidPolicy, err)
		}
		endMin, err := parseHHMM(p.BusinessHours.End)
		if err != nil {
			return fmt.Errorf("%w: business_hours.end inválido: %v", ErrInvalidPolicy, err)
		}
		if endMin <= startMin {
			return fmt.Errorf("%w: business_hours.end (%s) deve ser após start (%s)", ErrInvalidPolicy, p.BusinessHours.End, p.BusinessHours.Start)
		}
	}
	if p.RateLimit.GlobalPerMinute < 0 || p.RateLimit.PerSourcePerMinute < 0 {
		return fmt.Errorf("%w: limites de taxa não podem ser negativos", ErrInvalidPolicy)
	}
	if p.Nudge.DelayMinutes < 0 || p.Nudge.MaxAttempts < 0 {
		return fmt.Errorf("%w: configurações de nudge não podem ser negativas", ErrInvalidPolicy)
	}
	return nil
}

// IsWithinBusinessHours verifica de forma pura se um instante está dentro da janela configurada.
func IsWithinBusinessHours(p TenantPolicy, at time.Time) bool {
	if !p.BusinessHours.Enabled {
		return true
	}
	loc, err := time.LoadLocation(p.Timezone)
	if err != nil {
		return false
	}
	local := at.In(loc)
	wd := local.Weekday()

	dayAllowed := false
	for _, d := range p.BusinessHours.Days {
		parsed, ok := parseWeekday(d)
		if ok && parsed == wd {
			dayAllowed = true
			break
		}
	}
	if !dayAllowed {
		return false
	}

	startMin, err1 := parseHHMM(p.BusinessHours.Start)
	endMin, err2 := parseHHMM(p.BusinessHours.End)
	if err1 != nil || err2 != nil {
		return false
	}

	curMin := local.Hour()*60 + local.Minute()
	return curMin >= startMin && curMin < endMin
}

func parseWeekday(s string) (time.Weekday, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "sun", "dom":
		return time.Sunday, true
	case "mon", "seg":
		return time.Monday, true
	case "tue", "ter":
		return time.Tuesday, true
	case "wed", "qua":
		return time.Wednesday, true
	case "thu", "qui":
		return time.Thursday, true
	case "fri", "sex":
		return time.Friday, true
	case "sat", "sab":
		return time.Saturday, true
	default:
		return 0, false
	}
}

func parseHHMM(s string) (int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("esperado HH:MM, recebido %q", s)
	}
	return t.Hour()*60 + t.Minute(), nil
}
