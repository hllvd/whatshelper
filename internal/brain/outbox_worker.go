package brain

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"whatshelper/internal/core"
)

// OutboxActionHandler executa um efeito colateral idempotente de um item do outbox.
type OutboxActionHandler func(ctx context.Context, item core.OutboxItem) error

// OutboxWorker processa itens pendentes/vencidos do outbox transacional com backoff exponencial e DLQ.
type OutboxWorker struct {
	store       core.Store
	notifier    core.AdminNotifier
	bus         core.EventBus
	nowFn       func() time.Time
	baseBackoff time.Duration

	mu       sync.RWMutex
	handlers map[string]OutboxActionHandler
}

// NewOutboxWorker cria um worker de outbox com o registro padrão de ações agnósticas.
func NewOutboxWorker(st core.Store, notifier core.AdminNotifier, bus core.EventBus, nowFn func() time.Time) *OutboxWorker {
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	w := &OutboxWorker{
		store:       st,
		notifier:    notifier,
		bus:         bus,
		nowFn:       nowFn,
		baseBackoff: 5 * time.Second,
		handlers:    make(map[string]OutboxActionHandler),
	}
	w.registerDefaults()
	return w
}

// RegisterHandler registra ou sobrescreve o executor de um tipo de ação do outbox.
func (w *OutboxWorker) RegisterHandler(actionType string, fn OutboxActionHandler) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.handlers[actionType] = fn
}

func (w *OutboxWorker) registerDefaults() {
	w.RegisterHandler("reply_directive", func(context.Context, core.OutboxItem) error {
		return nil
	})
	w.RegisterHandler("schedule_timeout", func(context.Context, core.OutboxItem) error {
		return nil
	})
	w.RegisterHandler("create_task", func(ctx context.Context, item core.OutboxItem) error {
		if w.store == nil {
			return nil
		}
		now := w.nowFn()
		taskType := stringParam(item.Params, "type", "follow_up")
		title := stringParam(item.Params, "title", "Pendência gerada pela jornada")
		taskID := "obx-" + strings.ReplaceAll(item.ID, "#", "-")
		return w.store.SaveTask(ctx, core.Task{
			ID:        taskID,
			TenantID:  item.TenantID,
			Channel:   item.Channel,
			SourceKey: item.SourceKey,
			Type:      taskType,
			Title:     title,
			Status:    core.TaskStatusPending,
			CreatedAt: now,
		})
	})
	w.RegisterHandler("notify_admin", func(ctx context.Context, item core.OutboxItem) error {
		if w.notifier == nil {
			return nil
		}
		reason := stringParam(item.Params, "reason", "Notificação da jornada")
		return w.notifier.Notify(ctx, core.AdminAlert{
			TenantID:  item.TenantID,
			Channel:   item.Channel,
			SourceKey: item.SourceKey,
			Reason:    reason,
			Message:   fmt.Sprintf("[%s:%s] %s", item.Channel, item.SourceKey, reason),
			CreatedAt: w.nowFn(),
		})
	})
	w.RegisterHandler("handoff", func(ctx context.Context, item core.OutboxItem) error {
		if w.store == nil {
			return nil
		}
		return UpdateSourceStatus(ctx, w.store, w.nowFn(), item.TenantID, item.Channel, item.SourceKey, core.SourceStatusHuman)
	})
	w.RegisterHandler("set_status", func(ctx context.Context, item core.OutboxItem) error {
		if w.store == nil {
			return nil
		}
		status := core.SourceStatus(stringParam(item.Params, "status", string(core.SourceStatusActive)))
		return UpdateSourceStatus(ctx, w.store, w.nowFn(), item.TenantID, item.Channel, item.SourceKey, status)
	})
}

// ProcessDue busca e despacha itens vencidos do outbox de um tenant.
func (w *OutboxWorker) ProcessDue(ctx context.Context, tenantID string, limit int) (int, error) {
	if w.store == nil {
		return 0, nil
	}
	if limit <= 0 {
		limit = 50
	}
	items, err := w.store.ListDueOutboxItems(ctx, tenantID, w.nowFn(), limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, item := range items {
		if err := w.DispatchItem(ctx, item); err == nil {
			processed++
		}
	}
	return processed, nil
}

// DispatchItem executa um item do outbox de forma idempotente, atualiza seu status e publica evento.
func (w *OutboxWorker) DispatchItem(ctx context.Context, item core.OutboxItem) error {
	if item.Status == core.OutboxStatusDone || item.Status == core.OutboxStatusDead {
		return nil
	}
	now := w.nowFn()
	if !item.NextAttemptAt.IsZero() && now.Before(item.NextAttemptAt) {
		return nil
	}

	w.mu.RLock()
	handler, ok := w.handlers[item.ActionType]
	w.mu.RUnlock()
	if !ok {
		return w.markFailure(ctx, item, now, fmt.Errorf("handler de outbox não registrado para ação %q", item.ActionType))
	}

	if err := handler(ctx, item); err != nil {
		return w.markFailure(ctx, item, now, err)
	}

	item.Status = core.OutboxStatusDone
	item.LastError = ""
	item.UpdatedAt = now
	if w.store != nil {
		if err := w.store.UpdateOutboxItem(ctx, item); err != nil {
			return err
		}
	}
	w.publish(core.Event{
		ID:        newID("ev"),
		Type:      core.EventOutboxDispatched,
		TenantID:  item.TenantID,
		Channel:   item.Channel,
		SourceKey: item.SourceKey,
		RunID:     item.RunID,
		At:        now,
		Payload: map[string]any{
			"outbox_id":   item.ID,
			"action_type": item.ActionType,
			"attempts":    item.Attempts,
		},
	})
	return nil
}

// ReprocessItem reabilita manualmente um item failed/dead do outbox e tenta despachá-lo imediatamente.
func (w *OutboxWorker) ReprocessItem(ctx context.Context, tenantID, channel, sourceKey, outboxID string) error {
	if w.store == nil {
		return fmt.Errorf("store não configurado")
	}
	item, err := w.store.GetOutboxItem(ctx, tenantID, channel, sourceKey, outboxID)
	if err != nil {
		return err
	}
	if item == nil {
		return fmt.Errorf("item do outbox %q não encontrado", outboxID)
	}
	now := w.nowFn()
	item.Status = core.OutboxStatusPending
	item.LastError = ""
	item.NextAttemptAt = now
	item.UpdatedAt = now
	if err := w.store.UpdateOutboxItem(ctx, *item); err != nil {
		return err
	}
	return w.DispatchItem(ctx, *item)
}

func (w *OutboxWorker) markFailure(ctx context.Context, item core.OutboxItem, now time.Time, execErr error) error {
	item.Attempts++
	maxAttempts := item.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 5
	}
	item.LastError = execErr.Error()
	item.UpdatedAt = now
	if item.Attempts >= maxAttempts {
		item.Status = core.OutboxStatusDead
	} else {
		item.Status = core.OutboxStatusFailed
		shift := item.Attempts - 1
		if shift > 6 {
			shift = 6
		}
		item.NextAttemptAt = now.Add(w.baseBackoff * time.Duration(1<<shift))
	}
	if w.store != nil {
		_ = w.store.UpdateOutboxItem(ctx, item)
	}
	w.publish(core.Event{
		ID:        newID("ev"),
		Type:      core.EventOutboxFailed,
		TenantID:  item.TenantID,
		Channel:   item.Channel,
		SourceKey: item.SourceKey,
		RunID:     item.RunID,
		At:        now,
		Payload: map[string]any{
			"outbox_id":   item.ID,
			"action_type": item.ActionType,
			"status":      string(item.Status),
			"attempts":    item.Attempts,
			"error":       item.LastError,
		},
	})
	return execErr
}

func (w *OutboxWorker) publish(ev core.Event) {
	if w.bus != nil {
		w.bus.Publish(ev)
	}
}

func stringParam(params map[string]any, key, fallback string) string {
	if params == nil {
		return fallback
	}
	if v, ok := params[key].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return fallback
}
