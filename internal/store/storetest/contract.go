package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
)

// RunStoreContractTests executa a suíte de contrato compartilhada contra qualquer implementação de core.Store.
func RunStoreContractTests(t *testing.T, makeStore func(t *testing.T, clock core.Clock) core.Store) {
	t.Helper()

	t.Run("InitialStateAndCommitAnalysisOptimisticLocking", func(t *testing.T) {
		ctx := context.Background()
		t0 := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
		clock := fakes.NewFakeClock(t0)
		s := makeStore(t, clock)

		state, err := s.GetSourceState(ctx, "tenant-a", "simulator", "sess-1")
		if err != nil {
			t.Fatalf("GetSourceState initial failed: %v", err)
		}
		if state.Version != 0 {
			t.Fatalf("expected version 0, got %d", state.Version)
		}

		// Commit v0 -> v1
		err = s.CommitAnalysis(ctx, core.CommitAnalysisInput{
			TenantID:        "tenant-a",
			Channel:         "simulator",
			SourceKey:       "sess-1",
			ExpectedVersion: 0,
			Summary: core.Summary{
				TenantID:  "tenant-a",
				Channel:   "simulator",
				SourceKey: "sess-1",
				Text:      "Resumo inicial",
				TurnCount: 1,
				UpdatedAt: t0,
			},
			Facts:   map[string]string{"Budget": "1000"},
			Signals: map[string]bool{"has_new_question": true},
			Context: core.ConversationContext{
				LastActionType: "question",
				LastActionBy:   "customer",
				Description:    "Qual o valor?",
				Sections: map[string]map[string]string{
					"last_action": {"type": "question", "actor": "customer"},
				},
				UpdatedAt: t0,
			},
			Run: core.Run{
				ID:        "run-1",
				TenantID:  "tenant-a",
				Channel:   "simulator",
				SourceKey: "sess-1",
				MessageID: "msg-1",
				Stage:     "analysis",
				Status:    "ok",
				Model:     "fake",
				CreatedAt: t0,
			},
			UpdatedAt: t0,
		})
		if err != nil {
			t.Fatalf("CommitAnalysis v0->v1 failed: %v", err)
		}

		// Conflito com ExpectedVersion = 0
		err = s.CommitAnalysis(ctx, core.CommitAnalysisInput{
			TenantID:        "tenant-a",
			Channel:         "simulator",
			SourceKey:       "sess-1",
			ExpectedVersion: 0,
			Summary:         core.Summary{TenantID: "tenant-a", Channel: "simulator", SourceKey: "sess-1", Text: "conflito"},
			UpdatedAt:       t0,
		})
		if !errors.Is(err, core.ErrVersionConflict) {
			t.Fatalf("expected ErrVersionConflict, got %v", err)
		}

		// Leitura pós-commit
		after, err := s.GetSourceState(ctx, "tenant-a", "simulator", "sess-1")
		if err != nil {
			t.Fatalf("GetSourceState after commit failed: %v", err)
		}
		if after.Version != 1 {
			t.Fatalf("expected version 1, got %d", after.Version)
		}
		if after.Summary.Text != "Resumo inicial" || after.Facts["Budget"] != "1000" || !after.Signals["has_new_question"] {
			t.Fatalf("unexpected state after commit: %+v", after)
		}
		if after.Context.Sections["last_action"]["type"] != "question" {
			t.Fatalf("expected context sections preserved, got %+v", after.Context)
		}

		runs, err := s.ListRuns(ctx, "tenant-a", "simulator", "sess-1", 10)
		if err != nil {
			t.Fatalf("ListRuns failed: %v", err)
		}
		if len(runs) != 1 || runs[0].ID != "run-1" {
			t.Fatalf("expected 1 run (run-1), got %+v", runs)
		}
	})

	t.Run("PutMessageMetaDedupeOrderAndListSources", func(t *testing.T) {
		ctx := context.Background()
		t1 := time.Date(2026, 11, 11, 11, 0, 0, 0, time.UTC)
		t2 := t1.Add(time.Second)
		s := makeStore(t, fakes.NewFakeClock(t1))

		m1 := core.MessageMeta{
			TenantID:  "tenant-b",
			Channel:   "simulator",
			SourceKey: "sess-2",
			MessageID: "m1",
			Timestamp: t1,
			Direction: core.DirectionInbound,
			Type:      core.MessageTypeText,
			RawRef:    "tenant-b/simulator/sess-2/raw1.json",
		}
		m2 := core.MessageMeta{
			TenantID:  "tenant-b",
			Channel:   "simulator",
			SourceKey: "sess-2",
			MessageID: "m2",
			Timestamp: t2,
			Direction: core.DirectionOutbound,
			Type:      core.MessageTypeText,
			RawRef:    "tenant-b/simulator/sess-2/raw2.json",
		}

		if err := s.PutMessageMeta(ctx, m1); err != nil {
			t.Fatalf("PutMessageMeta m1 failed: %v", err)
		}
		if err := s.PutMessageMeta(ctx, m2); err != nil {
			t.Fatalf("PutMessageMeta m2 failed: %v", err)
		}
		// Tentativa duplicada (mesmo message_id, mesmo com timestamp diferente)
		dup := m1
		dup.Timestamp = t2.Add(time.Minute)
		if err := s.PutMessageMeta(ctx, dup); !errors.Is(err, core.ErrDuplicateMessage) {
			t.Fatalf("expected ErrDuplicateMessage, got %v", err)
		}

		recent, err := s.ListRecentMessages(ctx, "tenant-b", "simulator", "sess-2", 10)
		if err != nil {
			t.Fatalf("ListRecentMessages failed: %v", err)
		}
		if len(recent) != 2 || recent[0].MessageID != "m2" || recent[1].MessageID != "m1" {
			t.Fatalf("unexpected recent messages: %+v", recent)
		}

		sources, err := s.ListSources(ctx, "tenant-b")
		if err != nil {
			t.Fatalf("ListSources failed: %v", err)
		}
		if len(sources) != 1 || sources[0].SourceKey != "sess-2" {
			t.Fatalf("unexpected sources: %+v", sources)
		}
	})

	t.Run("TasksAndContactLinking", func(t *testing.T) {
		ctx := context.Background()
		now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
		s := makeStore(t, fakes.NewFakeClock(now))

		past := now.Add(-5 * time.Minute)
		future := now.Add(30 * time.Minute)

		if err := s.SaveTask(ctx, core.Task{
			ID: "t-due", TenantID: "tenant-c", Channel: "simulator", SourceKey: "s1",
			Title: "Retornar contato", Type: "call", Status: core.TaskStatusPending, DueAt: &past, CreatedAt: now,
		}); err != nil {
			t.Fatalf("SaveTask t-due failed: %v", err)
		}
		if err := s.SaveTask(ctx, core.Task{
			ID: "t-later", TenantID: "tenant-c", Channel: "simulator", SourceKey: "s1",
			Title: "Followup futuro", Type: "followup", Status: core.TaskStatusPending, DueAt: &future, CreatedAt: now,
		}); err != nil {
			t.Fatalf("SaveTask t-later failed: %v", err)
		}

		due, err := s.ListDueTasks(ctx, "tenant-c", now)
		if err != nil {
			t.Fatalf("ListDueTasks failed: %v", err)
		}
		if len(due) != 1 || due[0].ID != "t-due" {
			t.Fatalf("expected only t-due, got %+v", due)
		}

		if err := s.LinkSourceToContact(ctx, "tenant-c", "simulator", "s1", "contact-99"); err != nil {
			t.Fatalf("LinkSourceToContact failed: %v", err)
		}
		links, err := s.ListContactSources(ctx, "tenant-c", "contact-99")
		if err != nil {
			t.Fatalf("ListContactSources failed: %v", err)
		}
		if len(links) != 1 || links[0].SourceKey != "s1" {
			t.Fatalf("expected 1 contact link, got %+v", links)
		}
	})
}

// RunBlobStoreContractTests executa testes de contrato em qualquer implementação de core.BlobStore.
func RunBlobStoreContractTests(t *testing.T, makeBlob func(t *testing.T) core.BlobStore) {
	t.Helper()

	ctx := context.Background()
	b := makeBlob(t)

	k1 := "demo/simulator/u1/2026/11/11/1000_m1.json"
	k2 := "demo/traces/simulator/u1/run1.json"

	if err := b.Put(ctx, k1, []byte(`{"ok":true}`), "application/json"); err != nil {
		t.Fatalf("Put k1 failed: %v", err)
	}
	if err := b.Put(ctx, k2, []byte(`{"trace":1}`), "application/json"); err != nil {
		t.Fatalf("Put k2 failed: %v", err)
	}

	got, err := b.Get(ctx, k1)
	if err != nil {
		t.Fatalf("Get k1 failed: %v", err)
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("unexpected blob data: %s", got)
	}

	listed, err := b.List(ctx, "demo/simulator/")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(listed) != 1 || listed[0] != k1 {
		t.Fatalf("unexpected listed keys: %v", listed)
	}

	if err := b.Delete(ctx, k1); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if _, err := b.Get(ctx, k1); err == nil {
		t.Fatalf("expected error after deleting k1")
	}
}
