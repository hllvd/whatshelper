package contextmod

import (
	"testing"
	"time"

	"whatshelper/internal/core"
)

func TestSelectRecentMessages_PreservesLast5InboundWithin10Total(t *testing.T) {
	now := time.Now().UTC()
	mk := func(id string, dir core.Direction) MessageView {
		return MessageView{
			MessageID: id,
			Timestamp: now,
			Direction: string(dir),
			Text:      id,
		}
	}

	// 13 mensagens: 5 do cliente e 8 do robô onde as últimas 10 teriam apenas 2 do cliente.
	all := []MessageView{
		mk("c1", core.DirectionInbound),
		mk("c2", core.DirectionInbound),
		mk("c3", core.DirectionInbound),
		mk("b1", core.DirectionOutbound),
		mk("b2", core.DirectionOutbound),
		mk("b3", core.DirectionOutbound),
		mk("b4", core.DirectionOutbound),
		mk("b5", core.DirectionOutbound),
		mk("b6", core.DirectionOutbound),
		mk("b7", core.DirectionOutbound),
		mk("b8", core.DirectionOutbound),
		mk("c4", core.DirectionInbound),
		mk("c5", core.DirectionInbound),
	}

	got := selectRecentMessages(all, 10, 5)
	if len(got) != 10 {
		t.Fatalf("expected 10 total messages, got %d", len(got))
	}

	inboundCount := 0
	for _, m := range got {
		if m.Direction == string(core.DirectionInbound) {
			inboundCount++
		}
	}
	if inboundCount != 5 {
		t.Fatalf("expected all 5 inbound messages preserved, got %d", inboundCount)
	}

	// Ordem cronológica preservada: c1..c3 antes de b4..b8 e c4..c5 no final
	wantIDs := []string{"c1", "c2", "c3", "b4", "b5", "b6", "b7", "b8", "c4", "c5"}
	for i, w := range wantIDs {
		if got[i].MessageID != w {
			t.Fatalf("index %d: expected %s, got %s", i, w, got[i].MessageID)
		}
	}
}
