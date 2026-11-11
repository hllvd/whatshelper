package fakes_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
)

func TestMemoryStore_SourceLifecycle(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	store := fakes.NewMemoryStoreWithClock(fakes.NewFakeClock(now))

	// 1. Initial State
	state, err := store.GetSourceState(ctx, "tenant-1", "simulator", "user-1")
	if err != nil {
		t.Fatalf("unexpected error getting initial state: %v", err)
	}
	if state.Meta.Status != core.SourceStatusActive || state.Version != 0 {
		t.Fatalf("expected active status and version 0, got %s / %d", state.Meta.Status, state.Version)
	}

	// 2. CommitAnalysis (versão 0 -> 1)
	err = store.CommitAnalysis(ctx, core.CommitAnalysisInput{
		TenantID:        "tenant-1",
		Channel:         "simulator",
		SourceKey:       "user-1",
		ExpectedVersion: 0,
		Summary: core.Summary{
			TenantID:  "tenant-1",
			Channel:   "simulator",
			SourceKey: "user-1",
			Text:      "Cliente procura plano para 2 usuários até 5000.",
			TurnCount: 1,
			UpdatedAt: now,
		},
		Facts:     map[string]string{"Budget": "5000", "Seats": "2"},
		Signals:   map[string]bool{"has_new_question": true},
		Context:   core.ConversationContext{LastActionType: "question", UpdatedAt: now},
		Run:       core.Run{ID: "run-1", TenantID: "tenant-1", Channel: "simulator", SourceKey: "user-1", Stage: "analysis", Status: "ok", CreatedAt: now},
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CommitAnalysis failed: %v", err)
	}

	// Conflito de versão ao tentar comitar com ExpectedVersion=0 novamente
	err = store.CommitAnalysis(ctx, core.CommitAnalysisInput{
		TenantID:        "tenant-1",
		Channel:         "simulator",
		SourceKey:       "user-1",
		ExpectedVersion: 0,
	})
	if !errors.Is(err, core.ErrVersionConflict) {
		t.Fatalf("expected ErrVersionConflict, got %v", err)
	}

	// 3. Verify Updated State
	state, err = store.GetSourceState(ctx, "tenant-1", "simulator", "user-1")
	if err != nil {
		t.Fatalf("GetSourceState failed: %v", err)
	}
	if state.Version != 1 {
		t.Fatalf("expected version 1, got %d", state.Version)
	}
	if state.Facts["Budget"] != "5000" || state.Facts["Seats"] != "2" {
		t.Fatalf("facts mismatch: %v", state.Facts)
	}
	if !state.Signals["has_new_question"] {
		t.Fatalf("expected has_new_question to be true")
	}
	if state.Summary.Text != "Cliente procura plano para 2 usuários até 5000." {
		t.Fatalf("unexpected summary: %q", state.Summary.Text)
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

	if err := store.PutMessageMeta(ctx, msg1); err != nil {
		t.Fatalf("PutMessageMeta msg1 failed: %v", err)
	}
	if err := store.PutMessageMeta(ctx, msg2); err != nil {
		t.Fatalf("PutMessageMeta msg2 failed: %v", err)
	}
	// Duplicata deve retornar ErrDuplicateMessage
	if err := store.PutMessageMeta(ctx, msg1); !errors.Is(err, core.ErrDuplicateMessage) {
		t.Fatalf("expected ErrDuplicateMessage, got %v", err)
	}

	msgs, err := store.ListRecentMessages(ctx, "tenant-1", "simulator", "user-1", 10)
	if err != nil {
		t.Fatalf("ListRecentMessages failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 unique messages, got %d", len(msgs))
	}
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
		Title:     "Agendar reunião",
		Type:      "meeting",
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
