package brain

const DefaultMaxStaleReprocesses = 2

// StaleReplyPolicy configura a guarda contra resposta defasada (WIN-04).
type StaleReplyPolicy struct {
	Enabled        bool
	MaxReprocesses int
}

// DefaultStaleReplyPolicy retorna a política padrão ativa contra respostas defasadas.
func DefaultStaleReplyPolicy() StaleReplyPolicy {
	return StaleReplyPolicy{
		Enabled:        true,
		MaxReprocesses: DefaultMaxStaleReprocesses,
	}
}

// StaleReplyDecision é o resultado puro de ShouldDiscardReply.
type StaleReplyDecision struct {
	Discard bool
	Reason  string
}

// ShouldDiscardReply decide de forma pura se a resposta gerada para um lote deve ser descartada
// e reprocessada porque novas mensagens chegaram durante o processamento (WIN-04).
func ShouldDiscardReply(initialSeq, currentSeq uint64, reprocessCount int, policy StaleReplyPolicy) StaleReplyDecision {
	if !policy.Enabled {
		return StaleReplyDecision{
			Discard: false,
			Reason:  "guard_disabled",
		}
	}
	if currentSeq <= initialSeq {
		return StaleReplyDecision{
			Discard: false,
			Reason:  "up_to_date",
		}
	}
	maxRep := policy.MaxReprocesses
	if maxRep <= 0 {
		maxRep = DefaultMaxStaleReprocesses
	}
	if reprocessCount >= maxRep {
		return StaleReplyDecision{
			Discard: false,
			Reason:  "max_reprocesses_reached",
		}
	}
	return StaleReplyDecision{
		Discard: true,
		Reason:  "new_message_arrived",
	}
}
