package fakes

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"whatshelper/internal/core"
)

// MemoryStore é uma implementação concorrente e puramente em memória da interface core.Store.
type MemoryStore struct {
	mu            sync.RWMutex
	clock         core.Clock
	sources       map[string]*core.SourceMeta
	summaries     map[string]*core.Summary
	facts         map[string]map[string]string
	signals       map[string]map[string]bool
	contexts      map[string]*core.ConversationContext
	messages      map[string][]core.MessageMeta
	msgIndex      map[string]map[string]bool // sourceID -> messageID -> seen
	tasks         map[string]map[string]core.Task
	runs          map[string][]core.Run
	configs       map[string][]byte            // key: tenant#name
	contacts      map[string]core.Contact      // key: tenant#contact_id
	contactLinks  map[string][]core.SourceLink // key: tenant#contact_id
	outbound      map[string]core.OutboundMessageRecord
	journeys      map[string]*core.JourneyState
	jtransitions  map[string][]core.JourneyTransitionRecord
	outbox        map[string]map[string]core.OutboxItem // sourceID -> outboxID -> item
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
		configs:      make(map[string][]byte),
		contacts:     make(map[string]core.Contact),
		contactLinks: make(map[string][]core.SourceLink),
		outbound:     make(map[string]core.OutboundMessageRecord),
		journeys:     make(map[string]*core.JourneyState),
		jtransitions: make(map[string][]core.JourneyTransitionRecord),
		outbox:       make(map[string]map[string]core.OutboxItem),
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
	if err := core.CheckCommitAnalysisSizes(input); err != nil {
		return err
	}

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
	if input.JourneyStage != "" {
		meta.JourneyStage = input.JourneyStage
	}
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
		upsertRun(m.runs, id, input.Run)
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

func (m *MemoryStore) MarkMessagesAnswered(ctx context.Context, tenant, channel, sourceKey string, messageIDs []string) error {
	if len(messageIDs) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	id := sourceKeyID(tenant, channel, sourceKey)
	msgs, ok := m.messages[id]
	if !ok || len(msgs) == 0 {
		return nil
	}
	idSet := make(map[string]bool, len(messageIDs))
	markAllInbound := false
	for _, mid := range messageIDs {
		if mid == "*" {
			markAllInbound = true
		}
		idSet[mid] = true
	}
	for i := range msgs {
		if (markAllInbound && msgs[i].Direction == core.DirectionInbound) || idSet[msgs[i].MessageID] {
			msgs[i].Answered = true
		}
	}
	m.messages[id] = msgs
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

func (m *MemoryStore) GetTask(ctx context.Context, tenant, channel, sourceKey, taskID string) (*core.Task, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tenantTasks, ok := m.tasks[tenant]
	if !ok {
		return nil, core.ErrTaskNotFound
	}
	t, ok := tenantTasks[taskID]
	if !ok || t.Channel != channel || t.SourceKey != sourceKey {
		return nil, core.ErrTaskNotFound
	}
	copied := t
	return &copied, nil
}

func (m *MemoryStore) ListSourceTasks(ctx context.Context, tenant, channel, sourceKey string) ([]core.Task, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []core.Task
	for _, t := range m.tasks[tenant] {
		if t.Channel == channel && t.SourceKey == sourceKey {
			result = append(result, t)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result, nil
}

func (m *MemoryStore) SaveTenantConfig(ctx context.Context, tenantID, name string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	copied := make([]byte, len(data))
	copy(copied, data)
	m.configs[fmt.Sprintf("%s#%s", tenantID, name)] = copied
	return nil
}

func (m *MemoryStore) GetTenantConfig(ctx context.Context, tenantID, name string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	raw, ok := m.configs[fmt.Sprintf("%s#%s", tenantID, name)]
	if !ok {
		return nil, core.ErrConfigNotFound
	}
	copied := make([]byte, len(raw))
	copy(copied, raw)
	return copied, nil
}

func (m *MemoryStore) ListSourcesByContactGSI1(ctx context.Context, tenant, contactID string) ([]core.SourceMeta, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []core.SourceMeta
	for _, src := range m.sources {
		if src.TenantID == tenant && src.ContactID == contactID {
			result = append(result, *src)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Channel < result[j].Channel || (result[i].Channel == result[j].Channel && result[i].SourceKey < result[j].SourceKey)
	})
	return result, nil
}

func (m *MemoryStore) SaveRun(ctx context.Context, run core.Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := sourceKeyID(run.TenantID, run.Channel, run.SourceKey)
	upsertRun(m.runs, id, run)
	return nil
}

func upsertRun(runsMap map[string][]core.Run, sourceID string, run core.Run) {
	list := runsMap[sourceID]
	for i := range list {
		if list[i].ID == run.ID {
			list[i] = run
			return
		}
	}
	runsMap[sourceID] = append(list, run)
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

func (m *MemoryStore) SaveContact(ctx context.Context, contact core.Contact) error {
	if err := contact.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.clock.Now().UTC()
	if contact.CreatedAt.IsZero() {
		contact.CreatedAt = now
	}
	contact.UpdatedAt = now

	// normaliza email e telefone
	if contact.Email != "" {
		contact.Email = core.NormalizeEmail(contact.Email)
	}
	if contact.Phone != "" {
		contact.Phone = core.NormalizePhone(contact.Phone)
	}

	key := fmt.Sprintf("%s#%s", contact.TenantID, contact.ID)
	m.contacts[key] = contact
	return nil
}

func (m *MemoryStore) GetContact(ctx context.Context, tenant, contactID string) (*core.Contact, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := fmt.Sprintf("%s#%s", tenant, contactID)
	c, ok := m.contacts[key]
	if !ok {
		return nil, core.ErrContactNotFound
	}
	copied := c
	return &copied, nil
}

func (m *MemoryStore) FindContactByEmailOrPhone(ctx context.Context, tenant, email, phone string) (*core.Contact, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	normEmail := core.NormalizeEmail(email)
	normPhone := core.NormalizePhone(phone)

	for _, c := range m.contacts {
		if c.TenantID != tenant {
			continue
		}
		if normEmail != "" && core.NormalizeEmail(c.Email) == normEmail {
			copied := c
			return &copied, nil
		}
		if normPhone != "" && core.NormalizePhone(c.Phone) == normPhone {
			copied := c
			return &copied, nil
		}
	}
	return nil, nil
}

func (m *MemoryStore) SearchContacts(ctx context.Context, tenant, query string, limit int) ([]core.Contact, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	q := strings.ToLower(strings.TrimSpace(query))
	normQPhone := core.NormalizePhone(query)

	var list []core.Contact
	for _, c := range m.contacts {
		if c.TenantID != tenant {
			continue
		}
		if q == "" {
			list = append(list, c)
			continue
		}
		if strings.Contains(strings.ToLower(c.ID), q) ||
			strings.Contains(strings.ToLower(c.Name), q) ||
			strings.Contains(strings.ToLower(c.Email), q) ||
			strings.Contains(core.NormalizePhone(c.Phone), normQPhone) ||
			strings.Contains(strings.ToLower(c.Phone), q) {
			list = append(list, c)
		}
	}

	// Ordena por UpdatedAt decrescente
	for i := 0; i < len(list); i++ {
		for j := i + 1; j < len(list); j++ {
			if list[i].UpdatedAt.Before(list[j].UpdatedAt) {
				list[i], list[j] = list[j], list[i]
			}
		}
	}

	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	return list, nil
}

func (m *MemoryStore) DeleteContactAndHistory(ctx context.Context, tenant, contactID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	contactKey := fmt.Sprintf("%s#%s", tenant, contactID)
	delete(m.contacts, contactKey)

	// Localiza todas as fontes vinculadas
	links := m.contactLinks[contactKey]
	delete(m.contactLinks, contactKey)

	sourcesToDelete := make(map[string]bool)
	for _, l := range links {
		sourcesToDelete[sourceKeyID(tenant, l.Channel, l.SourceKey)] = true
	}
	// Também verifica fontes que tenham ContactID igual
	for sid, src := range m.sources {
		if src != nil && src.TenantID == tenant && src.ContactID == contactID {
			sourcesToDelete[sid] = true
		}
	}

	// Apaga todo o histórico e memória LLM de cada fonte
	for sid := range sourcesToDelete {
		delete(m.sources, sid)
		delete(m.summaries, sid)
		delete(m.facts, sid)
		delete(m.signals, sid)
		delete(m.contexts, sid)
		delete(m.messages, sid)
		delete(m.msgIndex, sid)
		delete(m.tasks, sid)
		delete(m.runs, sid)
		delete(m.journeys, sid)
		delete(m.jtransitions, sid)
		delete(m.outbox, sid)
	}

	return nil
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

func outboundKey(tenant, channel, messageID string) string {
	return fmt.Sprintf("%s#%s#%s", tenant, channel, messageID)
}

func (m *MemoryStore) PreRegisterOutbound(ctx context.Context, rec core.OutboundMessageRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := rec.CreatedAt
	if now.IsZero() {
		now = m.clock.Now().UTC()
	}
	rec.CreatedAt = now
	if rec.UpdatedAt.IsZero() {
		rec.UpdatedAt = now
	}
	if rec.Status == "" {
		rec.Status = core.OutboundStatusReserved
	}
	m.outbound[outboundKey(rec.TenantID, rec.Channel, rec.MessageID)] = rec
	return nil
}

func (m *MemoryStore) GetOutboundRecord(ctx context.Context, tenant, channel, messageID string) (*core.OutboundMessageRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, ok := m.outbound[outboundKey(tenant, channel, messageID)]
	if !ok {
		return nil, nil
	}
	// Verifica TTL se definido
	if rec.TTL > 0 && m.clock.Now().UTC().Unix() > rec.TTL {
		return nil, nil
	}
	copied := rec
	return &copied, nil
}

func (m *MemoryStore) UpdateOutboundStatus(ctx context.Context, tenant, channel, messageID string, status core.OutboundRecordStatus, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := outboundKey(tenant, channel, messageID)
	rec, ok := m.outbound[k]
	if !ok {
		return nil
	}
	rec.Status = status
	if !now.IsZero() {
		rec.UpdatedAt = now.UTC()
	} else {
		rec.UpdatedAt = m.clock.Now().UTC()
	}
	m.outbound[k] = rec
	return nil
}

func (m *MemoryStore) GetJourneyState(ctx context.Context, tenant, channel, sourceKey string) (*core.JourneyState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id := sourceKeyID(tenant, channel, sourceKey)
	st, ok := m.journeys[id]
	if !ok {
		return nil, nil
	}
	copied := *st
	if st.Dimensions != nil {
		copied.Dimensions = make(map[string]string, len(st.Dimensions))
		for k, v := range st.Dimensions {
			copied.Dimensions[k] = v
		}
	}
	return &copied, nil
}

func (m *MemoryStore) ListJourneyTransitions(ctx context.Context, tenant, channel, sourceKey string, limit int) ([]core.JourneyTransitionRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id := sourceKeyID(tenant, channel, sourceKey)
	items := m.jtransitions[id]
	if len(items) == 0 {
		return []core.JourneyTransitionRecord{}, nil
	}
	res := make([]core.JourneyTransitionRecord, len(items))
	copy(res, items)
	sort.Slice(res, func(i, j int) bool {
		return res[i].CreatedAt.After(res[j].CreatedAt)
	})
	if limit > 0 && len(res) > limit {
		res = res[:limit]
	}
	return res, nil
}

func (m *MemoryStore) CommitJourneyTurn(ctx context.Context, input core.CommitJourneyTurnInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := sourceKeyID(input.TenantID, input.Channel, input.SourceKey)
	var currentVersion int64
	if existing, ok := m.journeys[id]; ok {
		currentVersion = existing.Version
	}
	if currentVersion != input.ExpectedVersion {
		return fmt.Errorf("%w: journey esperado %d, atual %d", core.ErrVersionConflict, input.ExpectedVersion, currentVersion)
	}

	now := input.UpdatedAt
	if now.IsZero() {
		now = m.clock.Now().UTC()
	}

	j := input.Journey
	j.TenantID = input.TenantID
	j.Channel = input.Channel
	j.SourceKey = input.SourceKey
	j.Version = currentVersion + 1
	j.UpdatedAt = now
	if j.Dimensions != nil {
		dimsCopy := make(map[string]string, len(j.Dimensions))
		for k, v := range j.Dimensions {
			dimsCopy[k] = v
		}
		j.Dimensions = dimsCopy
	}
	m.journeys[id] = &j

	if input.Transition != nil {
		tr := *input.Transition
		tr.TenantID = input.TenantID
		tr.Channel = input.Channel
		tr.SourceKey = input.SourceKey
		if tr.CreatedAt.IsZero() {
			tr.CreatedAt = now
		}
		m.jtransitions[id] = append(m.jtransitions[id], tr)
	}

	if m.outbox[id] == nil {
		m.outbox[id] = make(map[string]core.OutboxItem)
	}
	for _, ob := range input.OutboxItems {
		if _, exists := m.outbox[id][ob.ID]; exists {
			continue // idempotência por <run_id>#<index>
		}
		if ob.CreatedAt.IsZero() {
			ob.CreatedAt = now
		}
		if ob.UpdatedAt.IsZero() {
			ob.UpdatedAt = now
		}
		m.outbox[id][ob.ID] = ob
	}

	for _, rec := range input.OutboundRecords {
		if rec.CreatedAt.IsZero() {
			rec.CreatedAt = now
		}
		if rec.UpdatedAt.IsZero() {
			rec.UpdatedAt = now
		}
		if rec.Status == "" {
			rec.Status = core.OutboundStatusReserved
		}
		m.outbound[outboundKey(rec.TenantID, rec.Channel, rec.MessageID)] = rec
	}

	return nil
}

func (m *MemoryStore) ListDueOutboxItems(ctx context.Context, tenant string, dueBefore time.Time, limit int) ([]core.OutboxItem, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []core.OutboxItem
	for _, srcItems := range m.outbox {
		for _, item := range srcItems {
			if tenant != "" && item.TenantID != tenant {
				continue
			}
			if item.Status != core.OutboxStatusPending && item.Status != core.OutboxStatusFailed {
				continue
			}
			if !item.NextAttemptAt.After(dueBefore) {
				result = append(result, item)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].NextAttemptAt.Before(result[j].NextAttemptAt)
	})
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (m *MemoryStore) ListOutboxItems(ctx context.Context, tenant, channel, sourceKey string, limit int) ([]core.OutboxItem, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []core.OutboxItem
	if sourceKey != "" {
		id := sourceKeyID(tenant, channel, sourceKey)
		for _, item := range m.outbox[id] {
			result = append(result, item)
		}
	} else {
		for _, srcItems := range m.outbox {
			for _, item := range srcItems {
				if tenant != "" && item.TenantID != tenant {
					continue
				}
				if channel != "" && item.Channel != channel {
					continue
				}
				result = append(result, item)
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (m *MemoryStore) GetOutboxItem(ctx context.Context, tenant, channel, sourceKey, outboxID string) (*core.OutboxItem, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	id := sourceKeyID(tenant, channel, sourceKey)
	item, ok := m.outbox[id][outboxID]
	if !ok {
		return nil, nil
	}
	copied := item
	return &copied, nil
}

func (m *MemoryStore) UpdateOutboxItem(ctx context.Context, item core.OutboxItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := sourceKeyID(item.TenantID, item.Channel, item.SourceKey)
	if m.outbox[id] == nil {
		m.outbox[id] = make(map[string]core.OutboxItem)
	}
	m.outbox[id][item.ID] = item
	return nil
}
