package fakes_test

import (
	"context"
	"testing"
	"time"

	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
)

func TestMemoryStore_SourceLifecycle(t *testing.T) {
	ctx := context.Background()
	store := fakes.NewMemoryStore()
	now := time.Now().UTC()

	// 1. Initial State
	state, err := store.GetSourceState(ctx, "tenant-1", "simulator", "user-1")
	if err != nil {
		t.Fatalf("unexpected error getting initial state: %v", err)
	}
	if state.Meta.Status != core.SourceStatusActive {
		t.Fatalf("expected default status active, got %s", state.Meta.Status)
	}
	if len(state.Facts) != 0 {
		t.Fatalf("expected 0 facts, got %d", len(state.Facts))
	}

	// 2. Save Meta
	meta := core.SourceMeta{
		TenantID:  "tenant-1",
		Channel:   "simulator",
		SourceKey: "user-1",
		Status:    core.SourceStatusPaused,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.SaveSourceMeta(ctx, meta); err != nil {
		t.Fatalf("SaveSourceMeta failed: %v", err)
	}

	// 3. Save Facts & Signals & Summary
	facts := map[string]string{"Budget": "500000", "Bedrooms": "2"}
	if err := store.SaveFacts(ctx, "tenant-1", "simulator", "user-1", facts); err != nil {
		t.Fatalf("SaveFacts failed: %v", err)
	}

	signals := map[string]bool{"has_new_question": true}
	if err := store.SaveSignals(ctx, "tenant-1", "simulator", "user-1", signals); err != nil {
		t.Fatalf("SaveSignals failed: %v", err)
	}

	summary := core.Summary{
		TenantID:  "tenant-1",
		Channel:   "simulator",
		SourceKey: "user-1",
		Text:      "Cliente procura apartamento de 2 quartos até 500k.",
		TurnCount: 1,
		UpdatedAt: now,
	}
	if err := store.SaveSummary(ctx, summary); err != nil {
		t.Fatalf("SaveSummary failed: %v", err)
	}

	// 4. Verify Updated State
	state, err = store.GetSourceState(ctx, "tenant-1", "simulator", "user-1")
	if err != nil {
		t.Fatalf("GetSourceState failed: %v", err)
	}
	if state.Meta.Status != core.SourceStatusPaused {
		t.Fatalf("expected status paused, got %s", state.Meta.Status)
	}
	if state.Facts["Budget"] != "500000" || state.Facts["Bedrooms"] != "2" {
		t.Fatalf("facts mismatch: %v", state.Facts)
	}
	if !state.Signals["has_new_question"] {
		t.Fatalf("expected has_new_question to be true")
	}
	if state.Summary.Text != summary.Text {
		t.Fatalf("expected summary %q, got %q", summary.Text, state.Summary.Text)
	}
}

func TestMemoryStore_MessagesDeduplicationAndOrder(t *testing.T) {
	ctx := context.Background()
	store := fakes.NewMemoryStore()
	t1 := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 11, 5, 10, 1, 0, 0, time.UTC)

	msg1 := core.MessageMeta{
		TenantID:  "tenant-1",
		Channel:   "simulator",
		SourceKey: "user-1",
		MessageID: "msg-1",
		Timestamp: t1,
		Direction: core.DirectionInbound,
		Type:      core.MessageTypeText,
	}
	msg2 := core.MessageMeta{
		TenantID:  "tenant-1",
		Channel:   "simulator",
		SourceKey: "user-1",
		MessageID: "msg-2",
		Timestamp: t2,
		Direction: core.DirectionOutbound,
		Type:      core.MessageTypeText,
	}

	_ = store.SaveMessageMeta(ctx, msg1)
	_ = store.SaveMessageMeta(ctx, msg2)
	// Salva msg1 novamente para testar deduplicação
	_ = store.SaveMessageMeta(ctx, msg1)

	msgs, err := store.ListRecentMessages(ctx, "tenant-1", "simulator", "user-1", 10)
	if err != nil {
		t.Fatalf("ListRecentMessages failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 unique messages, got %d", len(msgs))
	}
	// A mais recente deve vir primeiro
	if msgs[0].MessageID != "msg-2" || msgs[1].MessageID != "msg-1" {
		t.Fatalf("unexpected message order: [0]=%s, [1]=%s", msgs[0].MessageID, msgs[1].MessageID)
	}
}

func TestMemoryStore_DueTasks(t *testing.T) {
	ctx := context.Background()
	store := fakes.NewMemoryStore()
	now := time.Now().UTC()

	past := now.Add(-10 * time.Minute)
	future := now.Add(10 * time.Minute)

	task1 := core.Task{
		ID:        "task-past",
		TenantID:  "tenant-1",
		Channel:   "simulator",
		SourceKey: "user-1",
		Title:     "Ligar de volta",
		Type:      "call",
		Status:    core.TaskStatusPending,
		DueAt:     &past,
		CreatedAt: now,
	}
	task2 := core.Task{
		ID:        "task-future",
		TenantID:  "tenant-1",
		Channel:   "simulator",
		SourceKey: "user-1",
		Title:     "Agendar visita",
		Type:      "visit",
		Status:    core.TaskStatusPending,
		DueAt:     &future,
		CreatedAt: now,
	}
	task3 := core.Task{
		ID:          "task-completed",
		TenantID:    "tenant-1",
		Channel:     "simulator",
		SourceKey:   "user-1",
		Title:       "Confirmar proposta",
		Type:        "other",
		Status:      core.TaskStatusCompleted,
		DueAt:       &past,
		CreatedAt:   now,
		CompletedAt: &now,
	}

	_ = store.SaveTask(ctx, task1)
	_ = store.SaveTask(ctx, task2)
	_ = store.SaveTask(ctx, task3)

	due, err := store.ListDueTasks(ctx, "tenant-1", now)
	if err != nil {
		t.Fatalf("ListDueTasks failed: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("expected 1 due task, got %d", len(due))
	}
	if due[0].ID != "task-past" {
		t.Fatalf("expected task-past, got %s", due[0].ID)
	}
}

func TestMemoryStore_ContactLinking(t *testing.T) {
	ctx := context.Background()
	store := fakes.NewMemoryStore()

	err := store.LinkSourceToContact(ctx, "tenant-1", "whatsapp", "5511999998888", "contact-uuid-1")
	if err != nil {
		t.Fatalf("LinkSourceToContact failed: %v", err)
	}
	err = store.LinkSourceToContact(ctx, "tenant-1", "email", "hash-email-1", "contact-uuid-1")
	if err != nil {
		t.Fatalf("LinkSourceToContact failed: %v", err)
	}

	sources, err := store.ListContactSources(ctx, "tenant-1", "contact-uuid-1")
	if err != nil {
		t.Fatalf("ListContactSources failed: %v", err)
	}
	if len(sources) != 2 {
		t.Fatalf("expected 2 linked sources, got %d", len(sources))
	}
}

func TestMemoryBlobStore(t *testing.T) {
	ctx := context.Background()
	b := fakes.NewMemoryBlobStore()

	key := "tenant-1/raw/msg-1.json"
	data := []byte(`{"text": "olá"}`)

	if err := b.Put(ctx, key, data, "application/json"); err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	got, err := b.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("expected %s, got %s", string(data), string(got))
	}

	list, err := b.List(ctx, "tenant-1/raw")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(list) != 1 || list[0] != key {
		t.Fatalf("expected list [key], got %v", list)
	}

	if err := b.Delete(ctx, key); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = b.Get(ctx, key)
	if err == nil {
		t.Fatalf("expected error after delete, got nil")
	}
}

func TestFakeLLM_And_FakeClock(t *testing.T) {
	ctx := context.Background()
	llm := fakes.NewFakeLLM("resposta simulada")

	req := core.LLMRequest{
		Stage:  "response",
		Model:  "claude-3-7-sonnet",
		Prompt: "Olá",
	}
	resp, err := llm.Generate(ctx, req)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if resp.Content != "resposta simulada" {
		t.Fatalf("expected 'resposta simulada', got %s", resp.Content)
	}
	if len(llm.Calls()) != 1 {
		t.Fatalf("expected 1 call, got %d", len(llm.Calls()))
	}

	initial := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	clock := fakes.NewFakeClock(initial)
	if !clock.Now().Equal(initial) {
		t.Fatalf("expected %v, got %v", initial, clock.Now())
	}
	clock.Advance(5 * time.Minute)
	expectedAfter := initial.Add(5 * time.Minute)
	if !clock.Now().Equal(expectedAfter) {
		t.Fatalf("expected %v, got %v", expectedAfter, clock.Now())
	}
}
