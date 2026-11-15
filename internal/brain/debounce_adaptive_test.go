package brain_test

import (
	"math/rand"
	"testing"
	"time"

	"whatshelper/internal/brain"
)

func TestNextDebounceDecision_Table(t *testing.T) {
	t0 := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	cfg := brain.AdaptiveDebounceConfig{
		BaseDebounce:    8 * time.Second,
		IncompleteGrace: 4 * time.Second,
		MaxWait:         16 * time.Second,
		Lexicon:         brain.DefaultPTBRLexicon(),
	}

	tests := []struct {
		name       string
		state      brain.DebounceState
		evt        brain.DebounceEvent
		now        time.Time
		wantFlush  bool
		wantReason string
		wantWait   time.Time
	}{
		{
			name:  "normal_first_message_waits_base_debounce_8s",
			state: brain.DebounceState{},
			evt: brain.DebounceEvent{
				Type: brain.DebounceEventMessage,
				Text: "Bom dia, tudo bem?",
			},
			now:        t0,
			wantFlush:  false,
			wantReason: "base_debounce",
			wantWait:   t0.Add(8 * time.Second),
		},
		{
			name:  "incomplete_message_ending_in_conjunction_extends_wait_within_cap",
			state: brain.DebounceState{},
			evt: brain.DebounceEvent{
				Type: brain.DebounceEventMessage,
				Text: "quero comprar um apê e",
			},
			now:        t0,
			wantFlush:  false,
			wantReason: "incomplete_fragment",
			wantWait:   t0.Add(12 * time.Second),
		},
		{
			name: "continuation_prefix_e_tb_resets_sliding_window_capped_at_max_wait",
			state: brain.DebounceState{
				PendingCount: 2,
				CycleStart:   t0,
			},
			evt: brain.DebounceEvent{
				Type: brain.DebounceEventMessage,
				Text: "E TB tenho outro para alugar em março",
			},
			now:        t0.Add(11 * time.Second),
			wantFlush:  false,
			wantReason: "capped_at_max_wait",
			wantWait:   t0.Add(16 * time.Second),
		},
		{
			name: "timer_expired_while_typing_holds_until_max_wait",
			state: brain.DebounceState{
				PendingCount: 1,
				CycleStart:   t0,
				Typing:       true,
			},
			evt: brain.DebounceEvent{
				Type: brain.DebounceEventTimerExpired,
			},
			now:        t0.Add(8 * time.Second),
			wantFlush:  false,
			wantReason: "typing_standby",
			wantWait:   t0.Add(16 * time.Second),
		},
		{
			name: "timer_expired_at_max_wait_flushes_even_if_typing",
			state: brain.DebounceState{
				PendingCount:  1,
				CycleStart:    t0,
				Typing:        true,
				WaitingTyping: true,
			},
			evt: brain.DebounceEvent{
				Type: brain.DebounceEventTimerExpired,
			},
			now:        t0.Add(16 * time.Second),
			wantFlush:  true,
			wantReason: "max_wait_reached",
		},
		{
			name: "typing_stopped_after_debounce_flushes_immediately",
			state: brain.DebounceState{
				PendingCount:  2,
				CycleStart:    t0,
				Typing:        true,
				WaitingTyping: true,
			},
			evt: brain.DebounceEvent{
				Type:   brain.DebounceEventTypingChanged,
				Typing: false,
			},
			now:        t0.Add(10 * time.Second),
			wantFlush:  true,
			wantReason: "typing_stopped",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec := brain.NextDebounceDecision(tc.state, tc.evt, cfg, tc.now)
			if dec.Flush != tc.wantFlush {
				t.Fatalf("Flush: got %v, want %v (reason=%s)", dec.Flush, tc.wantFlush, dec.Reason)
			}
			if dec.Reason != tc.wantReason {
				t.Fatalf("Reason: got %q, want %q", dec.Reason, tc.wantReason)
			}
			if !tc.wantFlush && !dec.WaitUntil.Equal(tc.wantWait) {
				t.Fatalf("WaitUntil: got %v, want %v", dec.WaitUntil, tc.wantWait)
			}
		})
	}
}

func TestNextDebounceDecision_Properties_NeverExceedsMaxWait(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

	for iter := 0; iter < 200; iter++ {
		baseMs := 1000 + rng.Intn(9000)
		maxMs := baseMs + rng.Intn(15000)
		cfg := brain.AdaptiveDebounceConfig{
			BaseDebounce:    time.Duration(baseMs) * time.Millisecond,
			IncompleteGrace: 4 * time.Second,
			MaxWait:         time.Duration(maxMs) * time.Millisecond,
			Lexicon:         brain.DefaultPTBRLexicon(),
		}

		state := brain.DebounceState{
			PendingCount: 1,
			CycleStart:   t0,
			Typing:       rng.Intn(2) == 1,
		}
		now := t0.Add(time.Duration(rng.Intn(maxMs+5000)) * time.Millisecond)
		evt := brain.DebounceEvent{
			Type: brain.DebounceEventMessage,
			Text: "quero contratar o plano e",
		}

		dec := brain.NextDebounceDecision(state, evt, cfg, now)
		capTime := t0.Add(cfg.MaxWait)
		if !dec.Flush && dec.WaitUntil.After(capTime) {
			t.Fatalf("iter %d: WaitUntil %v exceeded capTime %v", iter, dec.WaitUntil, capTime)
		}
	}
}

func TestIsIncompleteFragment_EdgeCases(t *testing.T) {
	lex := brain.DefaultPTBRLexicon()
	if brain.IsIncompleteFragment("   ", lex) {
		t.Fatal("empty string should not be incomplete")
	}
	if !brain.IsIncompleteFragment("Quero comprar,", lex) {
		t.Fatal("trailing comma should be incomplete")
	}
	if brain.IsIncompleteFragment("Tudo certo!", lex) {
		t.Fatal("sentence ending in exclamation should not be incomplete")
	}
}
