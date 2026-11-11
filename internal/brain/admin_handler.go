package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"whatshelper/internal/core"
	"whatshelper/internal/store"
)

// AdminCommandType identifica um comando explícito enviado pelo dono/operador no canal admin.
type AdminCommandType string

const (
	AdminCmdAnswerEscalation AdminCommandType = "answer_escalation"
	AdminCmdApprove          AdminCommandType = "approve"
	AdminCmdReject           AdminCommandType = "reject"
	AdminCmdCorrect          AdminCommandType = "correct"
)

// AdminCommand representa o resultado determinístico do parsing de uma mensagem no canal admin.
type AdminCommand struct {
	Type       AdminCommandType
	ProposalID string
	Channel    string
	SourceKey  string
	Correction string
	RawText    string
}

// ParseAdminCommand interpreta /aprovar, /rejeitar, /corrigir ou resposta livre ao escalonamento.
func ParseAdminCommand(text string) AdminCommand {
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)

	switch {
	case lower == "/aprovar" || strings.HasPrefix(lower, "/aprovar "):
		rest := strings.TrimSpace(trimmed[len("/aprovar"):])
		id := rest
		editedAnswer := ""
		if idx := strings.Index(rest, "|"); idx >= 0 {
			id = strings.TrimSpace(rest[:idx])
			editedAnswer = strings.TrimSpace(rest[idx+1:])
		}
		return AdminCommand{
			Type:       AdminCmdApprove,
			ProposalID: id,
			Correction: editedAnswer,
			RawText:    trimmed,
		}

	case lower == "/rejeitar" || strings.HasPrefix(lower, "/rejeitar "):
		id := strings.TrimSpace(trimmed[len("/rejeitar"):])
		return AdminCommand{
			Type:       AdminCmdReject,
			ProposalID: id,
			RawText:    trimmed,
		}

	case lower == "/corrigir" || strings.HasPrefix(lower, "/corrigir "):
		rest := strings.TrimSpace(trimmed[len("/corrigir"):])
		cmd := AdminCommand{
			Type:    AdminCmdCorrect,
			Channel: "simulator",
			RawText: trimmed,
		}
		if idx := strings.Index(rest, "|"); idx >= 0 {
			target := strings.TrimSpace(rest[:idx])
			cmd.Correction = strings.TrimSpace(rest[idx+1:])
			parseAdminTarget(target, &cmd)
			return cmd
		}
		parts := strings.Fields(rest)
		if len(parts) >= 2 && strings.Contains(parts[0], ":") {
			parseAdminTarget(parts[0], &cmd)
			cmd.Correction = strings.TrimSpace(strings.TrimPrefix(rest, parts[0]))
			return cmd
		}
		cmd.Correction = rest
		return cmd

	default:
		return AdminCommand{
			Type:       AdminCmdAnswerEscalation,
			Correction: trimmed,
			RawText:    trimmed,
		}
	}
}

func parseAdminTarget(target string, cmd *AdminCommand) {
	if ch, sk, ok := strings.Cut(target, ":"); ok {
		if strings.TrimSpace(ch) != "" {
			cmd.Channel = strings.TrimSpace(ch)
		}
		cmd.SourceKey = strings.TrimSpace(sk)
		return
	}
	cmd.SourceKey = strings.TrimSpace(target)
}

// HandleAdminInbound processa mensagens recebidas pelo canal admin sem passar pelo pipeline de atendimento ao cliente.
func (p *Pipeline) HandleAdminInbound(ctx context.Context, msg core.NormalizedMessage) error {
	cmd := ParseAdminCommand(msg.Text)
	p.logger.Info("mensagem recebida no canal admin",
		"tenant", msg.TenantID,
		"source_key", msg.SourceKey,
		"command", string(cmd.Type),
	)
	return p.deliverAdminMessage(ctx, msg.TenantID, msg.SourceKey, fmt.Sprintf("Comando recebido (%s). Processamento de aprendizado ativo.", cmd.Type))
}

func (p *Pipeline) deliverAdminMessage(ctx context.Context, tenantID, sourceKey, text string) error {
	if sourceKey == "" {
		sourceKey = AdminOwnerKey
	}
	now := p.now()
	outMsg := core.NormalizedMessage{
		TenantID:  tenantID,
		Channel:   AdminChannelID,
		SourceKey: sourceKey,
		MessageID: newID("adm"),
		Timestamp: now,
		Direction: core.DirectionOutbound,
		Type:      core.MessageTypeText,
		Text:      text,
	}
	if p.cfg.Blobs != nil {
		outMsg.RawRef = store.S3RawMessageKey(tenantID, AdminChannelID, sourceKey, now, outMsg.MessageID)
		if rawBytes, err := json.Marshal(outMsg); err == nil {
			_ = p.cfg.Blobs.Put(ctx, outMsg.RawRef, rawBytes, "application/json")
		}
	}
	if p.cfg.Store != nil {
		_ = p.cfg.Store.PutMessageMeta(ctx, core.MessageMeta{
			TenantID:  tenantID,
			Channel:   AdminChannelID,
			SourceKey: sourceKey,
			MessageID: outMsg.MessageID,
			Timestamp: now,
			Direction: core.DirectionOutbound,
			Type:      core.MessageTypeText,
			RawRef:    outMsg.RawRef,
			Sender:    "brain",
		})
	}
	if ch := p.getChannel(AdminChannelID); ch != nil {
		_ = ch.Send(ctx, outMsg)
	}
	p.publish(core.Event{
		ID:        newID("ev"),
		Type:      core.EventAdminMessage,
		TenantID:  tenantID,
		Channel:   AdminChannelID,
		SourceKey: sourceKey,
		MessageID: outMsg.MessageID,
		At:        now,
		Payload: map[string]any{
			"direction": string(core.DirectionOutbound),
			"text":      text,
		},
	})
	return nil
}
