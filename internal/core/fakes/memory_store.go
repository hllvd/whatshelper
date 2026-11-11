package fakes

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"whatshelper/internal/core"
)

// MemoryStore é uma implementação concorrente e puramente em memória da interface core.Store.
type MemoryStore struct {
	mu           sync.RWMutex
	clock        core.Clock
	sources      map[string]*core.SourceMeta
	summaries    map[string]*core.Summary
	facts        map[string]map[string]string
	signals      map[string]map[string]bool
	contexts     map[string]*core.ConversationContext
	messages     map[string][]core.MessageMeta
	tasks        map[string]map[string]core.Task
	runs         map[string][]core.Run
	contactLinks map[string][]core.SourceLink // key: tenant#contact_id
}

func NewMemoryStore() *MemoryStore {
	return NewMemoryStoreWithClock(core.SystemClock{})
}

func NewMemoryStoreWithClock(clock core.Clock) *MemoryStore {
	if clock == nil {
		clock = core.SystemClock{}
	}
	return &MemoryStore{
		clock:        clock,
		sources:      make(map[string]*core.SourceMeta),
		summaries:    make(map[string]*core.Summary),
		facts:        make(map[string]map[string]string),
		signals:      make(map[string]map[string]bool),
		contexts:     make(map[string]*core.ConversationContext),
		messages:     make(map[string][]core.MessageMeta),
		tasks:        make(map[string]map[string]core.Task),
		runs:         make(map[string][]core.Run),
		contactLinks: make(map[string][]core.SourceLink),
	}
}

func sourceKeyID(tenant, channel, sourceKey string) string {
	return fmt.Sprintf("%s#%s#%s", tenant, channel, sourceKey)
}

func (m *MemoryStore) GetSourceState(ctx context.Context, tenant, channel, sourceKey string) (*core.SourceState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	id := sourceKeyID(tenant, channel, sourceKey)

	meta := core.SourceMeta{
		TenantID:  tenant,
		Channel:   channel,
		SourceKey: sourceKey,
		Status:    core.SourceStatusActive,
	}
	if existing, ok := m.sources[id]; ok {
		meta = *existing
	}

	summary := core.Summary{
		TenantID:  tenant,
		Channel:   channel,
		SourceKey: sourceKey,
	}
	if existing, ok := m.summaries[id]; ok {
		summary = *existing
	}

	copiedFacts := make(map[string]string)
	if existing, ok := m.facts[id]; ok {
		for k, v := range existing {
			copiedFacts[k] = v
		}
	}

	copiedSignals := make(map[string]bool)
	if existing, ok := m.signals[id]; ok {
		for k, v := range existing {
			copiedSignals[k] = v
		}
	}

	convCtx := core.ConversationContext{}
	if existing, ok := m.contexts[id]; ok {
		convCtx = *existing
		if existing.Details != nil {
			convCtx.Details = make(map[string]string)
			for k, v := range existing.Details {
				convCtx.Details[k] = v
			}
		}
	}

	return &core.SourceState{
		Meta:    meta,
		Summary: summary,
		Facts:   copiedFacts,
		Signals: copiedSignals,
		Context: convCtx,
	}, nil
}

func (m *MemoryStore) SaveSourceMeta(ctx context.Context, meta core.SourceMeta) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := sourceKeyID(meta.TenantID, meta.Channel, meta.SourceKey)
	copied := meta
	m.sources[id] = &copied
	return nil
}

func (m *MemoryStore) SaveSummary(ctx context.Context, summary core.Summary) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := sourceKeyID(summary.TenantID, summary.Channel, summary.SourceKey)
	copied := summary
	m.summaries[id] = &copied
	return nil
}

func (m *MemoryStore) SaveFacts(ctx context.Context, tenant, channel, sourceKey string, facts map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := sourceKeyID(tenant, channel, sourceKey)
	if _, ok := m.facts[id]; !ok {
		m.facts[id] = make(map[string]string)
	}
	for k, v := range facts {
		m.facts[id][k] = v
	}
	return nil
}

func (m *MemoryStore) SaveSignals(ctx context.Context, tenant, channel, sourceKey string, signals map[string]bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := sourceKeyID(tenant, channel, sourceKey)
	if _, ok := m.signals[id]; !ok {
		m.signals[id] = make(map[string]bool)
	}
	for k, v := range signals {
		m.signals[id][k] = v
	}
	return nil
}

func (m *MemoryStore) SaveContext(ctx context.Context, tenant, channel, sourceKey string, ctxData core.ConversationContext) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := sourceKeyID(tenant, channel, sourceKey)
	copied := ctxData
	if ctxData.Details != nil {
		copied.Details = make(map[string]string)
		for k, v := range ctxData.Details {
			copied.Details[k] = v
		}
	}
	m.contexts[id] = &copied
	return nil
}

func (m *MemoryStore) SaveMessageMeta(ctx context.Context, msg core.MessageMeta) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := sourceKeyID(msg.TenantID, msg.Channel, msg.SourceKey)
	// deduplicação por message_id
	for _, existing := range m.messages[id] {
		if existing.MessageID == msg.MessageID {
			return nil
		}
	}
	m.messages[id] = append(m.messages[id], msg)
	return nil
}

func (m *MemoryStore) ListRecentMessages(ctx context.Context, tenant, channel, sourceKey string, limit int) ([]core.MessageMeta, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	id := sourceKeyID(tenant, channel, sourceKey)
	msgs, ok := m.messages[id]
	if !ok || len(msgs) == 0 {
		return []core.MessageMeta{}, nil
	}

	result := make([]core.MessageMeta, len(msgs))
	copy(result, msgs)

	// Ordena por timestamp descendente
	sort.Slice(result, func(i, j int) bool {
		return result[i].Timestamp.After(result[j].Timestamp)
	})

	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}

	return result, nil
}

func (m *MemoryStore) SaveTask(ctx context.Context, task core.Task) error {
	if err := task.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.tasks[task.TenantID]; !ok {
		m.tasks[task.TenantID] = make(map[string]core.Task)
	}
	m.tasks[task.TenantID][task.ID] = task
	return nil
}

func (m *MemoryStore) ListDueTasks(ctx context.Context, tenant string, dueBefore time.Time) ([]core.Task, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []core.Task
	tenantTasks, ok := m.tasks[tenant]
	if !ok {
		return result, nil
	}

	for _, t := range tenantTasks {
		if t.Status == core.TaskStatusPending && t.DueAt != nil && !t.DueAt.After(dueBefore) {
			result = append(result, t)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].DueAt.Before(*result[j].DueAt)
	})

	return result, nil
}

func (m *MemoryStore) SaveRun(ctx context.Context, run core.Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := sourceKeyID(run.TenantID, run.Channel, run.SourceKey)
	m.runs[id] = append(m.runs[id], run)
	return nil
}

func (m *MemoryStore) LinkSourceToContact(ctx context.Context, tenant, channel, sourceKey, contactID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.clock.Now().UTC()
	srcID := sourceKeyID(tenant, channel, sourceKey)
	if existing, ok := m.sources[srcID]; ok {
		existing.ContactID = contactID
		existing.UpdatedAt = now
	} else {
		m.sources[srcID] = &core.SourceMeta{
			TenantID:  tenant,
			Channel:   channel,
			SourceKey: sourceKey,
			ContactID: contactID,
			Status:    core.SourceStatusActive,
			CreatedAt: now,
			UpdatedAt: now,
		}
	}

	linkKey := fmt.Sprintf("%s#%s", tenant, contactID)
	link := core.SourceLink{
		TenantID:  tenant,
		ContactID: contactID,
		Channel:   channel,
		SourceKey: sourceKey,
		LinkedAt:  now,
	}

	// Evita duplicatas na lista de links
	for _, l := range m.contactLinks[linkKey] {
		if l.Channel == channel && l.SourceKey == sourceKey {
			return nil
		}
	}
	m.contactLinks[linkKey] = append(m.contactLinks[linkKey], link)
	return nil
}

func (m *MemoryStore) ListContactSources(ctx context.Context, tenant, contactID string) ([]core.SourceLink, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	linkKey := fmt.Sprintf("%s#%s", tenant, contactID)
	links, ok := m.contactLinks[linkKey]
	if !ok {
		return []core.SourceLink{}, nil
	}

	result := make([]core.SourceLink, len(links))
	copy(result, links)
	return result, nil
}
