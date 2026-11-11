package brain_test

import (
	"testing"

	"whatshelper/internal/brain"
)

func TestParseAndFormatQA_RoundTripAndFormats(t *testing.T) {
	rawHeading := `# Perguntas e Respostas Aprovadas (QA)

## O espaço aceita animais de pequeno porte?
Sim, animais de pequeno e médio porte são permitidos.
`
	e1 := brain.ParseQA(rawHeading)
	if len(e1) != 1 || e1[0].Question != "O espaço aceita animais de pequeno porte?" {
		t.Fatalf("unexpected heading parse: %+v", e1)
	}

	rawPR := `# Perguntas e Respostas Aprendidas

P: Qual o limite da exportação síncrona?
R: Até 50000 linhas; acima disso use /v2/exports.

P: Posso cancelar a qualquer momento?
R: Sim, sem multa rescisória.
`
	e2 := brain.ParseQA(rawPR)
	if len(e2) != 2 || e2[1].Answer != "Sim, sem multa rescisória." {
		t.Fatalf("unexpected P:/R: parse: %+v", e2)
	}

	formatted := brain.FormatQA(e2)
	reparsed := brain.ParseQA(formatted)
	if len(reparsed) != 2 || reparsed[0] != e2[0] || reparsed[1] != e2[1] {
		t.Fatalf("round-trip mismatch: %+v vs %+v", reparsed, e2)
	}
}

func TestMergeQAEntry_DedupeAndMerge(t *testing.T) {
	initial := []brain.QAEntry{
		{Question: "Qual o horário de atendimento nos sábados?", Answer: "Das 9h às 13h."},
		{Question: "Aceitam pagamento via PIX?", Answer: "Sim, com aprovação imediata."},
	}

	// Pergunta semanticamente equivalente -> deve fazer merge sem duplicar
	merged, outcome, idx := brain.MergeQAEntry(initial, brain.QAEntry{
		Question: "Qual o horário de atendimento aos sábados?",
		Answer:   "Atendemos aos sábados das 9h às 14h.",
	}, "")
	if outcome != brain.QAMergeMerged || idx != 0 || len(merged) != 2 {
		t.Fatalf("expected merge at idx 0 keeping len 2, got outcome=%s idx=%d len=%d", outcome, idx, len(merged))
	}
	if merged[0].Answer != "Atendemos aos sábados das 9h às 14h." {
		t.Fatalf("unexpected merged answer: %q", merged[0].Answer)
	}
	// Não pode mutar o slice original
	if initial[0].Answer != "Das 9h às 13h." {
		t.Fatalf("original slice was mutated!")
	}

	// Entrada idêntica -> unchanged
	unchanged, outcome2, _ := brain.MergeQAEntry(merged, merged[1], "")
	if outcome2 != brain.QAMergeUnchanged || len(unchanged) != 2 {
		t.Fatalf("expected unchanged, got %s len=%d", outcome2, len(unchanged))
	}

	// Nova pergunta distinta -> added
	added, outcome3, _ := brain.MergeQAEntry(merged, brain.QAEntry{
		Question: "Existe estacionamento no local?",
		Answer:   "Sim, gratuito para clientes por 2 horas.",
	}, "")
	if outcome3 != brain.QAMergeAdded || len(added) != 3 {
		t.Fatalf("expected added with len 3, got %s len=%d", outcome3, len(added))
	}
}
