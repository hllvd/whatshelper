package brain

import (
	"strings"
	"time"

	"whatshelper/internal/core"
)

// MutationType classifica o evento de mutação sobre uma mensagem anterior (WIN-07).
type MutationType string

const (
	MutationEdit   MutationType = "message_edited"
	MutationDelete MutationType = "message_deleted"
)

// MessageMutationEvent descreve uma edição ou exclusão recebida do canal (ex.: whatsmeow ProtocolMessage).
type MessageMutationEvent struct {
	Type            MutationType `json:"type"`
	TargetMessageID string       `json:"target_message_id"`
	NewText         string       `json:"new_text,omitempty"`
	OccurredAt      time.Time    `json:"occurred_at"`
}

// EditPolicy controla como edições e exclusões afetam o lote pendente e turnos já processados.
type EditPolicy struct {
	AllowEditInPending     bool
	AllowDeleteInPending   bool
	ReopenOnEditAfterFlush bool
}

// DefaultEditPolicy retorna a política padrão de edição/exclusão.
func DefaultEditPolicy() EditPolicy {
	return EditPolicy{
		AllowEditInPending:     true,
		AllowDeleteInPending:   true,
		ReopenOnEditAfterFlush: false,
	}
}

// EditApplicationResult é o resultado puro de ApplyMessageEdit.
type EditApplicationResult struct {
	UpdatedBatch   []core.NormalizedMessage
	AppliedInBatch bool
	MarkDeleted    bool
	RecordEdit     bool
	ReopenTurn     bool
}

// ApplyMessageEdit aplica de forma pura uma edição ou exclusão sobre o lote pendente ou sinaliza ação pós-flush (WIN-07).
func ApplyMessageEdit(batch []core.NormalizedMessage, evt MessageMutationEvent, policy EditPolicy) EditApplicationResult {
	targetID := strings.TrimSpace(evt.TargetMessageID)
	out := make([]core.NormalizedMessage, 0, len(batch))
	foundInBatch := false

	for _, m := range batch {
		if targetID != "" && m.MessageID == targetID {
			switch evt.Type {
			case MutationEdit:
				if policy.AllowEditInPending {
					edited := m
					edited.Text = strings.TrimSpace(evt.NewText)
					out = append(out, edited)
					foundInBatch = true
					continue
				}
			case MutationDelete:
				if policy.AllowDeleteInPending {
					foundInBatch = true
					continue // omite mensagem deletada do lote
				}
			}
		}
		out = append(out, m)
	}

	if foundInBatch {
		return EditApplicationResult{
			UpdatedBatch:   out,
			AppliedInBatch: true,
			MarkDeleted:    evt.Type == MutationDelete,
			RecordEdit:     evt.Type == MutationEdit,
		}
	}

	// Mensagem já saiu da fila pendente (já deu flush)
	if evt.Type == MutationDelete {
		return EditApplicationResult{
			UpdatedBatch: out,
			MarkDeleted:  true,
		}
	}
	if evt.Type == MutationEdit {
		return EditApplicationResult{
			UpdatedBatch: out,
			RecordEdit:   true,
			ReopenTurn:   policy.ReopenOnEditAfterFlush && strings.TrimSpace(evt.NewText) != "",
		}
	}
	return EditApplicationResult{UpdatedBatch: out}
}
