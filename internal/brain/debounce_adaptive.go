package brain

import (
	"strings"
	"time"
	"unicode"
)

// DebounceEventType classifica o estímulo que alimenta a decisão de debounce.
type DebounceEventType string

const (
	DebounceEventMessage      DebounceEventType = "message"
	DebounceEventTimerExpired DebounceEventType = "timer_expired"
	DebounceEventTypingChanged DebounceEventType = "typing_changed"
)

// IncompleteLexicon define sufixos e prefixos por idioma que indicam pensamento fragmentado.
type IncompleteLexicon struct {
	TrailingPunctuation []string
	TrailingWords       []string
	LeadingPrefixes     []string
}

// DefaultPTBRLexicon retorna a heurística padrão para português brasileiro (WIN-03).
func DefaultPTBRLexicon() IncompleteLexicon {
	return IncompleteLexicon{
		TrailingPunctuation: []string{",", ":", ";", "...", "-"},
		TrailingWords: []string{
			"e", "ou", "mas", "porém", "que", "porque", "pq",
			"para", "pra", "pro", "com", "sem", "de", "do", "da", "dos", "das",
			"em", "no", "na", "nos", "nas", "por", "como", "quando", "onde",
		},
		LeadingPrefixes: []string{
			"e ", "tb ", "tbm ", "também ", "tambem ", "inclusive ", "além disso", "alem disso", "outra coisa",
		},
	}
}

// AdaptiveDebounceConfig reúne os parâmetros temporais e léxicos do debounce adaptativo.
type AdaptiveDebounceConfig struct {
	BaseDebounce    time.Duration
	IncompleteGrace time.Duration
	MinWait         time.Duration
	MaxWait         time.Duration
	Lexicon         IncompleteLexicon
}

// DebounceState descreve o estado imutável atual da fila de debounce para uma conversa.
type DebounceState struct {
	PendingCount  int
	CycleStart    time.Time
	Typing        bool
	WaitingTyping bool
}

// DebounceEvent descreve o evento recebido para cálculo puro da próxima decisão.
type DebounceEvent struct {
	Type   DebounceEventType
	Text   string
	Typing bool
}

// DebounceDecision é a saída pura de NextDebounceDecision.
type DebounceDecision struct {
	Flush         bool
	WaitUntil     time.Time
	WaitDuration  time.Duration
	Reason        string
	NextState     DebounceState
}

// IsIncompleteFragment avalia de forma pura se o texto parece terminar incompleto
// (ex.: conjunção, preposição ou vírgula pendente).
func IsIncompleteFragment(text string, lex IncompleteLexicon) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, p := range lex.TrailingPunctuation {
		if p != "" && strings.HasSuffix(lower, p) {
			return true
		}
	}
	// Extrai a última palavra caso não termine em pontuação final (. ! ?)
	lastRune := rune(lower[len(lower)-1])
	if unicode.IsPunct(lastRune) {
		return false
	}
	fields := strings.Fields(lower)
	if len(fields) == 0 {
		return false
	}
	lastWord := fields[len(fields)-1]
	for _, w := range lex.TrailingWords {
		if lastWord == strings.ToLower(w) {
			return true
		}
	}
	return false
}

// NextDebounceDecision calcula de forma pura se o lote deve dar flush agora ou aguardar até WaitUntil (WIN-03).
// Invariante: quando cfg.MaxWait > 0, WaitUntil nunca excede CycleStart + cfg.MaxWait.
func NextDebounceDecision(state DebounceState, evt DebounceEvent, cfg AdaptiveDebounceConfig, now time.Time) DebounceDecision {
	next := state
	lex := cfg.Lexicon
	if len(lex.TrailingWords) == 0 && len(lex.TrailingPunctuation) == 0 {
		lex = DefaultPTBRLexicon()
	}

	switch evt.Type {
	case DebounceEventMessage:
		if next.PendingCount == 0 || next.CycleStart.IsZero() {
			next.CycleStart = now
		}
		next.PendingCount++
		next.WaitingTyping = false

		if cfg.BaseDebounce <= 0 {
			return DebounceDecision{
				Flush:     true,
				Reason:    "zero_debounce",
				NextState: next,
			}
		}

		wait := cfg.BaseDebounce
		reason := "base_debounce"
		if cfg.IncompleteGrace > 0 && IsIncompleteFragment(evt.Text, lex) {
			wait += cfg.IncompleteGrace
			reason = "incomplete_fragment"
		}
		if cfg.MinWait > 0 && wait < cfg.MinWait {
			wait = cfg.MinWait
		}

		waitUntil := now.Add(wait)
		if cfg.MaxWait > 0 {
			capTime := next.CycleStart.Add(cfg.MaxWait)
			if waitUntil.After(capTime) {
				waitUntil = capTime
				reason = "capped_at_max_wait"
			}
		}
		if !waitUntil.After(now) {
			return DebounceDecision{
				Flush:     true,
				Reason:    "max_wait_reached",
				NextState: next,
			}
		}
		return DebounceDecision{
			Flush:        false,
			WaitUntil:    waitUntil,
			WaitDuration: waitUntil.Sub(now),
			Reason:       reason,
			NextState:    next,
		}

	case DebounceEventTimerExpired:
		if next.PendingCount == 0 {
			return DebounceDecision{
				Flush:     false,
				Reason:    "empty_queue",
				NextState: next,
			}
		}
		if next.Typing && cfg.MaxWait > 0 && !next.CycleStart.IsZero() {
			capTime := next.CycleStart.Add(cfg.MaxWait)
			if capTime.After(now) {
				next.WaitingTyping = true
				return DebounceDecision{
					Flush:        false,
					WaitUntil:    capTime,
					WaitDuration: capTime.Sub(now),
					Reason:       "typing_standby",
					NextState:    next,
				}
			}
			return DebounceDecision{
				Flush:     true,
				Reason:    "max_wait_reached",
				NextState: next,
			}
		}
		if next.WaitingTyping {
			return DebounceDecision{
				Flush:     true,
				Reason:    "max_wait_reached",
				NextState: next,
			}
		}
		return DebounceDecision{
			Flush:     true,
			Reason:    "debounce_expired",
			NextState: next,
		}

	case DebounceEventTypingChanged:
		next.Typing = evt.Typing
		if !evt.Typing && next.WaitingTyping && next.PendingCount > 0 {
			next.WaitingTyping = false
			return DebounceDecision{
				Flush:     true,
				Reason:    "typing_stopped",
				NextState: next,
			}
		}
		return DebounceDecision{
			Flush:     false,
			Reason:    "typing_updated",
			NextState: next,
		}
	}

	return DebounceDecision{
		Flush:     false,
		Reason:    "noop",
		NextState: next,
	}
}
