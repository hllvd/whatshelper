package brain_test

import (
	"testing"

	"whatshelper/internal/brain"
)

func TestParseAdminCommand(t *testing.T) {
	c1 := brain.ParseAdminCommand("/aprovar prop-123")
	if c1.Type != brain.AdminCmdApprove || c1.ProposalID != "prop-123" {
		t.Fatalf("unexpected approve command: %+v", c1)
	}

	c1Edit := brain.ParseAdminCommand("/aprovar prop-123 | Resposta editada")
	if c1Edit.Type != brain.AdminCmdApprove || c1Edit.ProposalID != "prop-123" || c1Edit.Correction != "Resposta editada" {
		t.Fatalf("unexpected approve-edit command: %+v", c1Edit)
	}

	c2 := brain.ParseAdminCommand("/rejeitar prop-999")
	if c2.Type != brain.AdminCmdReject || c2.ProposalID != "prop-999" {
		t.Fatalf("unexpected reject command: %+v", c2)
	}

	c3 := brain.ParseAdminCommand("/corrigir whatsapp:5511999 | Aceitamos pix apenas com sinal")
	if c3.Type != brain.AdminCmdCorrect || c3.Channel != "whatsapp" || c3.SourceKey != "5511999" || c3.Correction != "Aceitamos pix apenas com sinal" {
		t.Fatalf("unexpected correct command: %+v", c3)
	}

	c4 := brain.ParseAdminCommand("Sim, funcionamos sábado até 14h.")
	if c4.Type != brain.AdminCmdAnswerEscalation || c4.Correction != "Sim, funcionamos sábado até 14h." {
		t.Fatalf("unexpected free-text answer: %+v", c4)
	}
}
