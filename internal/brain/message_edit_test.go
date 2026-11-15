package brain_test

import (
	"testing"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/core"
)

func TestApplyMessageEdit_Table(t *testing.T) {
	t0 := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	batch := []core.NormalizedMessage{
		{MessageID: "m1", Timestamp: t0, Text: "Quero comprar"},
		{MessageID: "m2", Timestamp: t0.Add(time.Second), Text: "Em abril"},
	}
	policy := brain.EditPolicy{AllowEditInPending: true, AllowDeleteInPending: true, ReopenOnEditAfterFlush: true}

	// 1. Edição no lote pendente substitui o texto in-place sem alterar a ordem
	resEdit := brain.ApplyMessageEdit(batch, brain.MessageMutationEvent{
		Type:            brain.MutationEdit,
		TargetMessageID: "m2",
		NewText:         "Em março",
		OccurredAt:      t0.Add(2 * time.Second),
	}, policy)
	if !resEdit.AppliedInBatch || len(resEdit.UpdatedBatch) != 2 || resEdit.UpdatedBatch[1].Text != "Em março" {
		t.Fatalf("expected in-batch edit to 'Em março', got %+v", resEdit)
	}

	// 2. Exclusão no lote pendente remove a mensagem do lote enviado ao LLM
	resDel := brain.ApplyMessageEdit(resEdit.UpdatedBatch, brain.MessageMutationEvent{
		Type:            brain.MutationDelete,
		TargetMessageID: "m1",
		OccurredAt:      t0.Add(3 * time.Second),
	}, policy)
	if !resDel.AppliedInBatch || len(resDel.UpdatedBatch) != 1 || resDel.UpdatedBatch[0].MessageID != "m2" {
		t.Fatalf("expected m1 removed from pending batch, got %+v", resDel)
	}

	// 3. Edição de mensagem que já foi processada (fora do lote pendente) sinaliza ReopenTurn se política permitir
	resAfter := brain.ApplyMessageEdit(nil, brain.MessageMutationEvent{
		Type:            brain.MutationEdit,
		TargetMessageID: "m-old",
		NewText:         "Texto corrigido",
		OccurredAt:      t0.Add(20 * time.Second),
	}, policy)
	if resAfter.AppliedInBatch || !resAfter.ReopenTurn {
		t.Fatalf("expected post-flush edit to set ReopenTurn=true, got %+v", resAfter)
	}
}
