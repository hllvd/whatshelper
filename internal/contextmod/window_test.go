package contextmod

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

func TestBuildWindow_Table(t *testing.T) {
	t0 := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name            string
		in              WindowInput
		wantHistIDs     []string
		wantBatchIDs    []string
		wantRoles       map[string]string
		wantDeltas      map[string]string
		wantDroppedIDs  []string
	}{
		{
			name: "4_fragmented_messages_in_batch_with_history_and_deltas",
			in: WindowInput{
				History: []WindowMessage{
					{MessageID: "h1", Timestamp: t0, Direction: "inbound", Origin: "customer", Text: "Oi"},
					{MessageID: "h2", Timestamp: t0.Add(5 * time.Second), Direction: "outbound", Origin: "bot", Text: "Olá! Como posso ajudar?"},
					{MessageID: "h3", Timestamp: t0.Add(20 * time.Second), Direction: "outbound", Origin: "human_phone", Text: "Sou o atendente João"},
				},
				Batch: []WindowMessage{
					{MessageID: "b1", Timestamp: t0.Add(30 * time.Second), Direction: "inbound", Origin: "customer", Text: "Eu quero comprar um item"},
					{MessageID: "b2", Timestamp: t0.Add(33 * time.Second), Direction: "inbound", Origin: "customer", Text: "E vender o meu"},
					{MessageID: "b3", Timestamp: t0.Add(35 * time.Second), Direction: "inbound", Origin: "customer", Text: "Bom dia primeiramente"},
					{MessageID: "b4", Timestamp: t0.Add(39 * time.Second), Direction: "inbound", Origin: "customer", Text: "E TB tenho outro para alugar em março"},
				},
				HistoryMaxMessages: 10,
				HistoryMinCustomer: 5,
			},
			wantHistIDs:  []string{"h1", "h2", "h3"},
			wantBatchIDs: []string{"b1", "b2", "b3", "b4"},
			wantRoles: map[string]string{
				"h1": "customer",
				"h2": "bot",
				"h3": "human",
				"b1": "customer",
			},
			wantDeltas: map[string]string{
				"h1": "0s",
				"h2": "+5s",
				"h3": "+15s",
				"b1": "+10s",
				"b2": "+3s",
				"b3": "+2s",
				"b4": "+4s",
			},
		},
		{
			name: "batch_larger_than_N_is_never_cut_and_deleted_excluded",
			in: WindowInput{
				History: []WindowMessage{
					{MessageID: "h1", Timestamp: t0, Direction: "inbound", Text: "msg 1"},
					{MessageID: "hdel", Timestamp: t0.Add(time.Second), Direction: "inbound", Text: "apagada", Deleted: true},
				},
				Batch: []WindowMessage{
					{MessageID: "b1", Timestamp: t0.Add(2 * time.Second), Direction: "inbound", Text: "p1"},
					{MessageID: "b2", Timestamp: t0.Add(3 * time.Second), Direction: "inbound", Text: "p2"},
					{MessageID: "b3", Timestamp: t0.Add(4 * time.Second), Direction: "inbound", Text: "p3"},
					{MessageID: "b4", Timestamp: t0.Add(5 * time.Second), Direction: "inbound", Text: "p4"},
				},
				HistoryMaxMessages: 2,
				HistoryMinCustomer: 1,
			},
			wantHistIDs:  []string{"h1"},
			wantBatchIDs: []string{"b1", "b2", "b3", "b4"},
		},
		{
			name: "token_budget_cuts_old_history_never_batch",
			in: WindowInput{
				History: []WindowMessage{
					{MessageID: "h1", Timestamp: t0, Direction: "outbound", Origin: "bot", Text: "Histórico muito antigo longo que deve cair primeiro no orçamento"},
					{MessageID: "h2", Timestamp: t0.Add(time.Second), Direction: "inbound", Origin: "customer", Text: "Pergunta recente do cliente"},
				},
				Batch: []WindowMessage{
					{MessageID: "b1", Timestamp: t0.Add(2 * time.Second), Direction: "inbound", Origin: "customer", Text: "Mensagem nova do lote atual que jamais pode ser cortada"},
				},
				HistoryMaxMessages: 10,
				HistoryMinCustomer: 5,
				TokenBudget:        35,
			},
			wantHistIDs:    []string{"h2"},
			wantBatchIDs:   []string{"b1"},
			wantDroppedIDs: []string{"h1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := BuildWindow(tc.in)
			if len(out.History) != len(tc.wantHistIDs) {
				t.Fatalf("history len: got %d, want %d (%+v)", len(out.History), len(tc.wantHistIDs), out.History)
			}
			for i, wantID := range tc.wantHistIDs {
				if out.History[i].MessageID != wantID {
					t.Fatalf("history[%d]: got %s, want %s", i, out.History[i].MessageID, wantID)
				}
			}
			if len(out.Batch) != len(tc.wantBatchIDs) {
				t.Fatalf("batch len: got %d, want %d", len(out.Batch), len(tc.wantBatchIDs))
			}
			for i, wantID := range tc.wantBatchIDs {
				if out.Batch[i].MessageID != wantID {
					t.Fatalf("batch[%d]: got %s, want %s", i, out.Batch[i].MessageID, wantID)
				}
			}
			all := append(append([]LabeledWindowMessage{}, out.History...), out.Batch...)
			for _, m := range all {
				if wantRole, ok := tc.wantRoles[m.MessageID]; ok && m.Role != wantRole {
					t.Fatalf("msg %s role: got %q, want %q", m.MessageID, m.Role, wantRole)
				}
				if wantDelta, ok := tc.wantDeltas[m.MessageID]; ok && m.DeltaFromPrev != wantDelta {
					t.Fatalf("msg %s delta: got %q, want %q", m.MessageID, m.DeltaFromPrev, wantDelta)
				}
			}
			if len(tc.wantDroppedIDs) > 0 {
				if len(out.Dropped) != len(tc.wantDroppedIDs) {
					t.Fatalf("dropped len: got %d, want %d (%+v)", len(out.Dropped), len(tc.wantDroppedIDs), out.Dropped)
				}
				for i, id := range tc.wantDroppedIDs {
					if out.Dropped[i].ItemID != id {
						t.Fatalf("dropped[%d]: got %s, want %s", i, out.Dropped[i].ItemID, id)
					}
				}
			}
		})
	}
}

func TestBuildWindow_Properties(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	baseTime := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

	for iter := 0; iter < 100; iter++ {
		histCount := rng.Intn(25)
		batchCount := 1 + rng.Intn(12)
		maxHist := 1 + rng.Intn(15)
		minCust := 1 + rng.Intn(6)
		budget := rng.Intn(200) // 0 = ilimitado, >0 = orçamento restrito

		var hist []WindowMessage
		cur := baseTime
		for i := 0; i < histCount; i++ {
			cur = cur.Add(time.Duration(rng.Intn(120)) * time.Second)
			dir := "inbound"
			origin := "customer"
			if rng.Intn(2) == 1 {
				dir = "outbound"
				origin = "bot"
			}
			hist = append(hist, WindowMessage{
				MessageID: fmt.Sprintf("h-%d", i),
				Timestamp: cur,
				Direction: dir,
				Origin:    origin,
				Text:      fmt.Sprintf("hist message %d content text", i),
			})
		}

		var batch []WindowMessage
		for i := 0; i < batchCount; i++ {
			cur = cur.Add(time.Duration(rng.Intn(10)) * time.Second)
			batch = append(batch, WindowMessage{
				MessageID: fmt.Sprintf("b-%d", i),
				Timestamp: cur,
				Direction: "inbound",
				Origin:    "customer",
				Text:      fmt.Sprintf("batch fragment %d text", i),
			})
		}

		in := WindowInput{
			History:            hist,
			Batch:              batch,
			HistoryMaxMessages: maxHist,
			HistoryMinCustomer: minCust,
			TokenBudget:        budget,
		}

		out1 := BuildWindow(in)
		out2 := BuildWindow(in)

		// Invariante 1: Determinismo
		if len(out1.History) != len(out2.History) || len(out1.Batch) != len(out2.Batch) || out1.UsedTokens != out2.UsedTokens {
			t.Fatalf("iter %d: non-deterministic output", iter)
		}

		// Invariante 2: Lote NUNCA é cortado
		if len(out1.Batch) != len(batch) {
			t.Fatalf("iter %d: batch was cut! got %d, want %d", iter, len(out1.Batch), len(batch))
		}

		// Invariante 3: Ordem cronológica preservada em History e Batch
		combined := append(append([]LabeledWindowMessage{}, out1.History...), out1.Batch...)
		for i := 1; i < len(combined); i++ {
			if combined[i].Timestamp.Before(combined[i-1].Timestamp) {
				t.Fatalf("iter %d: chronological order violated at %d", iter, i)
			}
		}

		// Invariante 4: Orçamento de tokens respeitado sempre que o lote sozinho couber
		if budget > 0 && out1.UsedTokens > budget && len(out1.History) > 0 {
			t.Fatalf("iter %d: budget %d exceeded (%d) while history still had %d items", iter, budget, out1.UsedTokens, len(out1.History))
		}
	}
}

func TestFormatTimeDeltaAndRole_EdgeCases(t *testing.T) {
	if got := FormatTimeDelta(-5 * time.Second); got != "+0s" {
		t.Fatalf("expected +0s, got %s", got)
	}
	if got := FormatTimeDelta(2 * time.Minute); got != "+2m" {
		t.Fatalf("expected +2m, got %s", got)
	}
	if got := FormatTimeDelta(2*time.Minute + 15*time.Second); got != "+2m15s" {
		t.Fatalf("expected +2m15s, got %s", got)
	}
	if got := FormatTimeDelta(3 * time.Hour); got != "+3h" {
		t.Fatalf("expected +3h, got %s", got)
	}
	if got := FormatTimeDelta(3*time.Hour + 20*time.Minute); got != "+3h20m" {
		t.Fatalf("expected +3h20m, got %s", got)
	}
	if got := ResolveMessageRole("outbound", ""); got != "bot" {
		t.Fatalf("expected bot for outbound default, got %s", got)
	}
	if got := ResolveMessageRole("inbound", ""); got != "customer" {
		t.Fatalf("expected customer for inbound default, got %s", got)
	}
}
