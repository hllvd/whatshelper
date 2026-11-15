package brain_test

import (
	"testing"

	"whatshelper/internal/brain"
)

func TestShouldDiscardReply_Table(t *testing.T) {
	policy := brain.StaleReplyPolicy{
		Enabled:        true,
		MaxReprocesses: 2,
	}

	tests := []struct {
		name           string
		initialSeq     uint64
		currentSeq     uint64
		reprocessCount int
		policy         brain.StaleReplyPolicy
		wantDiscard    bool
		wantReason     string
	}{
		{
			name:           "same_seq_does_not_discard",
			initialSeq:     5,
			currentSeq:     5,
			reprocessCount: 0,
			policy:         policy,
			wantDiscard:    false,
			wantReason:     "up_to_date",
		},
		{
			name:           "newer_seq_within_limit_discards_and_reprocesses",
			initialSeq:     5,
			currentSeq:     6,
			reprocessCount: 0,
			policy:         policy,
			wantDiscard:    true,
			wantReason:     "new_message_arrived",
		},
		{
			name:           "newer_seq_at_max_reprocesses_does_not_discard_to_prevent_loop",
			initialSeq:     5,
			currentSeq:     7,
			reprocessCount: 2,
			policy:         policy,
			wantDiscard:    false,
			wantReason:     "max_reprocesses_reached",
		},
		{
			name:           "disabled_policy_never_discards",
			initialSeq:     1,
			currentSeq:     2,
			reprocessCount: 0,
			policy:         brain.StaleReplyPolicy{Enabled: false, MaxReprocesses: 2},
			wantDiscard:    false,
			wantReason:     "guard_disabled",
		},
		{
			name:           "zero_max_reprocesses_uses_default_limit",
			initialSeq:     1,
			currentSeq:     2,
			reprocessCount: 0,
			policy:         brain.StaleReplyPolicy{Enabled: true, MaxReprocesses: 0},
			wantDiscard:    true,
			wantReason:     "new_message_arrived",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec := brain.ShouldDiscardReply(tc.initialSeq, tc.currentSeq, tc.reprocessCount, tc.policy)
			if dec.Discard != tc.wantDiscard {
				t.Fatalf("Discard: got %v, want %v", dec.Discard, tc.wantDiscard)
			}
			if dec.Reason != tc.wantReason {
				t.Fatalf("Reason: got %q, want %q", dec.Reason, tc.wantReason)
			}
		})
	}
}
