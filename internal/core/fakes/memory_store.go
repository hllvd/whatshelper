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
	msgIndex     map[string]map[string]bool // sourceID -> messageID -> seen
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
		msgIndex:     make(map[string]map[string]bool),
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
		Version:   0,
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

	convCtx := copyContext(m.contexts[id])

	return &core.SourceState{
		Version: meta.Version,
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
	if existing, ok := m.sources[id]; ok && copied.Version == 0 {
		copied.Version = existing.Version
	}
	m.sources[id] = &copied
	return nil
}

// CommitAnalysis grava atomicamente SUMMARY, FACTS, SIGNALS, CONTEXT e RUN, validando ExpectedVersion em META.
func (m *MemoryStore) CommitAnalysis(ctx context.Context, input core.CommitAnalysisInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := sourceKeyID(input.TenantID, input.Channel, input.SourceKey)
	now := input.UpdatedAt
	if now.IsZero() {
		now = m.clock.Now().UTC()
	}

	var currentVersion int64
	var meta core.SourceMeta
	if existing, ok := m.sources[id]; ok {
		currentVersion = existing.Version
		meta = *existing
	} else {
		meta = core.SourceMeta{
			TenantID:  input.TenantID,
			Channel:   input.Channel,
			SourceKey: input.SourceKey,
			Status:    core.SourceStatusActive,
			CreatedAt: now,
		}
	}

	if currentVersion != input.ExpectedVersion {
		return fmt.Errorf("%w: esperado %d, atual %d", core.ErrVersionConflict, input.ExpectedVersion, currentVersion)
	}

	meta.Version = currentVersion + 1
	meta.UpdatedAt = now
	m.sources[id] = &meta

	summaryCopy := input.Summary
	m.summaries[id] = &summaryCopy

	factsCopy := make(map[string]string, len(input.Facts))
	for k, v := range input.Facts {
		factsCopy[k] = v
	}
	m.facts[id] = factsCopy

	signalsCopy := make(map[string]bool, len(input.Signals))
	for k, v := range input.Signals {
		signalsCopy[k] = v
	}
	m.signals[id] = signalsCopy

	ctxCopy := copyContext(&input.Context)
	m.contexts[id] = &ctxCopy

	if input.Run.ID != "" {
		m.runs[id] = append(m.runs[id], input.Run)
	}

	return nil
}

func (m *MemoryStore) PutMessageMeta(ctx context.Context, msg core.MessageMeta) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := sourceKeyID(msg.TenantID, msg.Channel, msg.SourceKey)
	if _, ok := m.msgIndex[id]; !ok {
		m.msgIndex[id] = make(map[string]bool)
	}
	if m.msgIndex[id][msg.MessageID] {
		return fmt.Errorf("%w: %s", core.ErrDuplicateMessage, msg.MessageID)
	}
	m.msgIndex[id][msg.MessageID] = true
	m.messages[id] = append(m.messages[id], msg)

	// Garante existência de META inicial quando a primeira mensagem entra
	if _, ok := m.sources[id]; !ok {
		now := msg.Timestamp
		if now.IsZero() {
			now = m.clock.Now().UTC()
		}
		m.sources[id] = &core.SourceMeta{
			TenantID:  msg.TenantID,
			Channel:   msg.Channel,
			SourceKey: msg.SourceKey,
			Status:    core.SourceStatusActive,
			Version:   0,
			CreatedAt: now,
			UpdatedAt: now,
		}
	}
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

	// Ordena por timestamp descendente (desempate por message_id)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Timestamp.Equal(result[j].Timestamp) {
			return result[i].MessageID > result[j].MessageID
		}
		return result[i].Timestamp.After(result[j].Timestamp)
	})

	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}

	return result, nil
}

func (m *MemoryStore) ListSources(ctx context.Context, tenant string) ([]core.SourceMeta, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]core.SourceMeta, 0)
	for _, src := range m.sources {
		if src.TenantID == tenant {
			result = append(result, *src)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].UpdatedAt.After(result[j].UpdatedAt)
	})
	return result, nil
}

func (m *MemoryStore) ListRuns(ctx context.Context, tenant, channel, sourceKey string, limit int) ([]core.Run, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	id := sourceKeyID(tenant, channel, sourceKey)
	runs, ok := m.runs[id]
	if !ok || len(runs) == 0 {
		return []core.Run{}, nil
	}

	result := make([]core.Run, len(runs))
	copy(result, runs)

	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID > result[j].ID
		}
		return result[i].CreatedAt.After(result[j].CreatedAt)
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

func copyContext(src *core.ConversationContext) core.ConversationContext {
	if src == nil {
		return core.ConversationContext{}
	}
	copied := *src
	if src.Details != nil {
		copied.Details = make(map[string]string, len(src.Details))
		for k, v := range src.Details {
			copied.Details[k] = v
		}
	}
	if src.Sections != nil {
		copied.Sections = make(map[string]map[string]string, len(src.Sections))
		for sec, fields := range src.Sections {
			secCopy := make(map[string]string, len(fields))
			for k, v := range fields {
				secCopy[k] = v
			}
			copied.Sections[sec] = secCopy
		}
	}
	return copied
}
