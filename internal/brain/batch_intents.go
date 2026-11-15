package brain

import (
	"strings"

	"whatshelper/internal/pack"
)

// BatchIntentItem descreve a intenção extraída para um segmento/mensagem específica do lote (WIN-06).
type BatchIntentItem struct {
	MessageIndex int    `json:"message_index"`
	Intent       string `json:"intent"`
	Summary      string `json:"summary,omitempty"`
}

// AggregatedIntents consolida múltiplos intents de um lote em uma visão única determinística (WIN-06).
type AggregatedIntents struct {
	PrimaryIntent       string            `json:"primary_intent"`
	UniqueIntents       []string          `json:"unique_intents"`
	Items               []BatchIntentItem `json:"items,omitempty"`
	DominantAction      pack.IntentAction `json:"dominant_action"`
	MaxQuestionsAllowed int               `json:"max_questions_allowed"`
}

// AggregateBatchIntents consolida de forma pura os intents individuais do lote e resolve a ação dominante (WIN-06).
// Regra de precedência de ação: opt_out > handoff > escalate > create_task > reply.
// Invariante: MaxQuestionsAllowed é sempre 1 para evitar bombardear o cliente com múltiplas perguntas.
func AggregateBatchIntents(primary string, items []BatchIntentItem, defs []pack.IntentDef) AggregatedIntents {
	actionByIntent := make(map[string]pack.IntentAction, len(defs))
	for _, d := range defs {
		actionByIntent[d.Name] = d.Action
	}

	seen := make(map[string]bool)
	unique := make([]string, 0, len(items)+1)
	addIntent := func(name string) {
		clean := strings.TrimSpace(name)
		if clean == "" || seen[clean] {
			return
		}
		seen[clean] = true
		unique = append(unique, clean)
	}

	for _, it := range items {
		addIntent(it.Intent)
	}
	addIntent(primary)

	resolvedPrimary := strings.TrimSpace(primary)
	if resolvedPrimary == "" && len(unique) > 0 {
		resolvedPrimary = unique[0]
	}

	dominant := pack.IntentActionReply
	bestRank := intentActionRank(dominant)
	for _, in := range unique {
		if act, ok := actionByIntent[in]; ok {
			if r := intentActionRank(act); r > bestRank {
				bestRank = r
				dominant = act
			}
		}
	}

	return AggregatedIntents{
		PrimaryIntent:       resolvedPrimary,
		UniqueIntents:       unique,
		Items:               items,
		DominantAction:      dominant,
		MaxQuestionsAllowed: 1,
	}
}

func intentActionRank(a pack.IntentAction) int {
	switch a {
	case pack.IntentActionOptOut:
		return 5
	case pack.IntentActionHandoff:
		return 4
	case pack.IntentActionEscalate:
		return 3
	case pack.IntentActionCreateTask:
		return 2
	default:
		return 1
	}
}
