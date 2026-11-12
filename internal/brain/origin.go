package brain

import (
	"strings"

	"whatshelper/internal/core"
)

// OriginResolutionInput reúne os sinais determinísticos usados para classificar a origem de uma mensagem.
// Nunca utiliza comparação de texto ou heurística semântica.
type OriginResolutionInput struct {
	Direction          core.Direction
	Channel            string
	FromMe             bool
	MessageID          string
	ReportsOwnMessages bool
	OutboundRecord     *core.OutboundMessageRecord
	IsOldHistory       bool
	IsAmbiguous        bool
}

// OriginResolutionResult descreve a origem classificada, o motivo e a confiança da decisão.
type OriginResolutionResult struct {
	Origin     core.MessageOrigin
	Reason     string
	Confidence string
}

// ResolveMessageOrigin classifica a origem de uma mensagem de forma 100% pura e determinística.
// Regras:
// 1. Outbound do Brain -> OriginBot.
// 2. Canal admin -> OriginHumanAdmin (nunca dispara pausa).
// 3. Histórico antigo -> OriginUnknown (nunca dispara pausa).
// 4. Inbound com from_me=false -> OriginCustomer.
// 5. Inbound com from_me=true:
//    - Ambíguo ou sem message_id -> OriginUnknown (na dúvida, não pausa).
//    - message_id pré-registrado em OUTMSG# -> OriginBot (eco próprio).
//    - Canal não reporta mensagens próprias -> OriginUnknown.
//    - Caso contrário -> OriginHumanPhone (dono respondeu pelo aparelho).
func ResolveMessageOrigin(in OriginResolutionInput) OriginResolutionResult {
	if in.Direction == core.DirectionOutbound {
		return OriginResolutionResult{
			Origin:     core.OriginBot,
			Reason:     "outbound_from_brain",
			Confidence: "high",
		}
	}

	if strings.TrimSpace(in.Channel) == AdminChannelID {
		return OriginResolutionResult{
			Origin:     core.OriginHumanAdmin,
			Reason:     "admin_channel",
			Confidence: "high",
		}
	}

	if in.IsOldHistory {
		return OriginResolutionResult{
			Origin:     core.OriginUnknown,
			Reason:     "old_history_sync",
			Confidence: "low",
		}
	}

	if !in.FromMe {
		return OriginResolutionResult{
			Origin:     core.OriginCustomer,
			Reason:     "inbound_not_from_me",
			Confidence: "high",
		}
	}

	if in.IsAmbiguous || strings.TrimSpace(in.MessageID) == "" {
		return OriginResolutionResult{
			Origin:     core.OriginUnknown,
			Reason:     "ambiguous_or_missing_id",
			Confidence: "low",
		}
	}

	if in.OutboundRecord != nil {
		return OriginResolutionResult{
			Origin:     core.OriginBot,
			Reason:     "matched_preregistered_outbound_id",
			Confidence: "high",
		}
	}

	if !in.ReportsOwnMessages {
		return OriginResolutionResult{
			Origin:     core.OriginUnknown,
			Reason:     "channel_does_not_report_own_messages",
			Confidence: "low",
		}
	}

	return OriginResolutionResult{
		Origin:     core.OriginHumanPhone,
		Reason:     "from_me_unregistered_id",
		Confidence: "high",
	}
}
