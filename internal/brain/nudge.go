package brain

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"whatshelper/internal/core"
	"whatshelper/internal/pack"
)

// ScheduleNudge agenda um follow-up proativo somente quando policy.ProactiveEnabled=true.
func (p *Pipeline) ScheduleNudge(ctx context.Context, tenantID, channelID, sourceKey, account string) error {
	tenantCfg, err := p.cfg.Tenants.LoadTenant(ctx, tenantID)
	if err != nil || !tenantCfg.ProactiveEnabled || !tenantCfg.BotEnabled {
		return nil
	}

	state, err := p.cfg.Store.GetSourceState(ctx, tenantID, channelID, sourceKey)
	if err != nil {
		return err
	}
	if state.Meta.Status != "" && state.Meta.Status != core.SourceStatusActive {
		return nil
	}

	delay := time.Duration(tenantCfg.Nudge.DelayMinutes) * time.Minute
	if delay <= 0 {
		delay = 60 * time.Minute
	}
	maxAttempts := tenantCfg.Nudge.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 2
	}

	now := p.now()
	due := now.Add(delay)
	task := core.Task{
		ID:          deterministicNudgeID(tenantID, channelID, sourceKey),
		TenantID:    tenantID,
		Channel:     channelID,
		SourceKey:   sourceKey,
		Kind:        "nudge",
		Title:       "Follow-up proativo (nudge)",
		Type:        "proactive_nudge",
		Status:      core.TaskStatusPending,
		DueAt:       &due,
		Attempt:     1,
		MaxAttempts: maxAttempts,
		Metadata:    map[string]string{"account": account},
		CreatedAt:   now,
	}
	return p.cfg.Store.SaveTask(ctx, task)
}

// CancelPendingNudges cancela qualquer nudge pendente quando o cliente volta a enviar mensagem.
func (p *Pipeline) CancelPendingNudges(ctx context.Context, tenantID, channelID, sourceKey string) error {
	tasks, err := p.cfg.Store.ListSourceTasks(ctx, tenantID, channelID, sourceKey)
	if err != nil {
		return err
	}
	now := p.now()
	for _, t := range tasks {
		if t.Kind != "nudge" || t.Status != core.TaskStatusPending {
			continue
		}
		t.Status = core.TaskStatusCancelled
		t.CompletedAt = &now
		if err := p.cfg.Store.SaveTask(ctx, t); err != nil {
			return err
		}
	}
	return nil
}

// ProcessDueNudges processa nudges vencidos do tenant via GSI2 respeitando opt-out, pausa, rate limit e horário comercial.
func (p *Pipeline) ProcessDueNudges(ctx context.Context, tenantID string) (int, error) {
	now := p.now()
	dueTasks, err := p.cfg.Store.ListDueTasks(ctx, tenantID, now)
	if err != nil {
		return 0, err
	}

	sentCount := 0
	for _, task := range dueTasks {
		if task.Kind != "nudge" || task.Status != core.TaskStatusPending {
			continue
		}
		if task.DueAt == nil || task.DueAt.After(now) {
			continue
		}

		tenantCfg, err := p.cfg.Tenants.LoadTenant(ctx, tenantID)
		if err != nil || !tenantCfg.ProactiveEnabled || !tenantCfg.BotEnabled {
			p.cancelNudgeTask(ctx, task, now)
			continue
		}

		state, err := p.cfg.Store.GetSourceState(ctx, tenantID, task.Channel, task.SourceKey)
		if err != nil {
			continue
		}
		if state.Meta.Status != "" && state.Meta.Status != core.SourceStatusActive {
			p.cancelNudgeTask(ctx, task, now)
			continue
		}

		if !core.IsWithinBusinessHours(tenantCfg, now) {
			continue
		}

		if ok, _ := p.limiter.Allow(tenantID, task.Channel, task.SourceKey, tenantCfg.RateLimit); !ok {
			continue
		}

		pk, err := p.cfg.PackLoader(tenantCfg.Pack)
		if err != nil || pk.NudgeTmpl == nil {
			continue
		}

		recent, err := p.loadRecentPromptMessages(ctx, pk, tenantID, task.Channel, task.SourceKey, nil)
		if err != nil {
			continue
		}

		rendered, err := pack.RenderNudge(pk, pack.PromptInput{
			Pack:           pk,
			Summary:        state.Summary.Text,
			Facts:          state.Facts,
			Signals:        state.Signals,
			Context:        state.Context.Sections,
			RecentMessages: recent,
		})
		if err != nil {
			continue
		}

		model := p.cfg.NudgeModel
		if model == "" {
			model = p.cfg.ResponseModel
		}
		resp, err := p.cfg.LLM.Generate(ctx, core.LLMRequest{
			Stage:       "nudge",
			Model:       model,
			System:      rendered.System,
			Prompt:      rendered.User,
			Temperature: 0.4,
			MaxTokens:   256,
		})
		if err != nil {
			continue
		}

		nudgeText := strings.TrimSpace(resp.Content)
		if ch := p.getChannel(task.Channel); ch != nil && ch.Capabilities().PlainTextOnly {
			nudgeText = SanitizePlainText(nudgeText)
		}
		if nudgeText == "" {
			continue
		}

		runID := newID("nudge")
		account := task.Metadata["account"]
		msgID, err := p.DeliverTextOutbound(ctx, tenantID, task.Channel, account, task.SourceKey, pk.Actors.Business, nudgeText, runID, "")
		if err != nil {
			p.logger.Warn("falha ao enviar nudge", slog.String("task_id", task.ID), slog.Any("error", err))
			continue
		}

		_ = p.cfg.Store.SaveRun(ctx, core.Run{
			ID:               runID,
			TenantID:         tenantID,
			Channel:          task.Channel,
			SourceKey:        task.SourceKey,
			MessageID:        msgID,
			Stage:            "nudge",
			Status:           "ok",
			Model:            model,
			PromptTokens:     resp.PromptTokens,
			CompletionTokens: resp.CompletionTokens,
			DurationMs:       resp.DurationMs,
			TTL:              p.runTTL(),
			CreatedAt:        now,
		})

		p.publish(core.Event{
			ID:        newID("ev"),
			Type:      core.EventNudgeSent,
			TenantID:  tenantID,
			Channel:   task.Channel,
			SourceKey: task.SourceKey,
			MessageID: msgID,
			RunID:     runID,
			At:        now,
			Payload: map[string]any{
				"task_id": task.ID,
				"attempt": task.Attempt,
				"text":    nudgeText,
			},
		})

		delay := time.Duration(tenantCfg.Nudge.DelayMinutes) * time.Minute
		if delay <= 0 {
			delay = 60 * time.Minute
		}
		if task.Attempt < task.MaxAttempts {
			task.Attempt++
			nextDue := now.Add(delay)
			task.DueAt = &nextDue
		} else {
			task.Status = core.TaskStatusCompleted
			task.CompletedAt = &now
		}
		_ = p.cfg.Store.SaveTask(ctx, task)
		sentCount++
	}
	return sentCount, nil
}

// StartNudgeWorker inicia um ticker periódico em background para despachar nudges vencidos dos tenants configurados.
func (p *Pipeline) StartNudgeWorker(ctx context.Context, interval time.Duration, tenantsFn func() []string) {
	if interval <= 0 || tenantsFn == nil {
		return
	}
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				for _, tid := range tenantsFn() {
					if _, err := p.ProcessDueNudges(ctx, tid); err != nil {
						p.logger.Warn("erro ao processar nudges", slog.String("tenant", tid), slog.Any("error", err))
					}
				}
			}
		}
	}()
}

func (p *Pipeline) cancelNudgeTask(ctx context.Context, task core.Task, now time.Time) {
	task.Status = core.TaskStatusCancelled
	task.CompletedAt = &now
	_ = p.cfg.Store.SaveTask(ctx, task)
}

func deterministicNudgeID(tenant, channel, sourceKey string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{tenant, channel, sourceKey, "nudge"}, "|")))
	return fmt.Sprintf("nudge_%s", hex.EncodeToString(sum[:8]))
}
