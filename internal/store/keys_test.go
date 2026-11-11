package store_test

import (
	"testing"
	"time"

	"whatshelper/internal/store"
)

func TestDynamoKeys(t *testing.T) {
	ts := time.Date(2026, 11, 11, 14, 30, 5, 123000000, time.UTC)

	if got := store.SourcePK("demo", "whatsapp", "5511999998888"); got != "T#demo#S#whatsapp#5511999998888" {
		t.Fatalf("unexpected SourcePK: %s", got)
	}
	if got := store.TenantPK("demo"); got != "T#demo" {
		t.Fatalf("unexpected TenantPK: %s", got)
	}
	if got := store.TenantSourceSK("simulator", "sess-1"); got != "SRC#simulator#sess-1" {
		t.Fatalf("unexpected TenantSourceSK: %s", got)
	}
	if got := store.MessageSK(ts, "msg-1"); got != "MSG#2026-11-11T14:30:05.123000000Z#msg-1" {
		t.Fatalf("unexpected MessageSK: %s", got)
	}
	if got := store.RunSK(ts, "run-1"); got != "RUN#2026-11-11T14:30:05.123000000Z#run-1" {
		t.Fatalf("unexpected RunSK: %s", got)
	}
	if got := store.ContactPK("demo", "c-1"); got != "T#demo#CONTACT#c-1" {
		t.Fatalf("unexpected ContactPK: %s", got)
	}
	if got := store.DueGSI2PK("demo"); got != "T#demo#DUE" {
		t.Fatalf("unexpected DueGSI2PK: %s", got)
	}
	if got := store.DueGSI2SK(ts, "whatsapp", "5511999998888"); got != "2026-11-11T14:30:05.123000000Z#whatsapp#5511999998888" {
		t.Fatalf("unexpected DueGSI2SK: %s", got)
	}
}

func TestS3Keys(t *testing.T) {
	ts := time.Date(2026, 11, 5, 9, 15, 0, 0, time.UTC)
	expectedRaw := "demo/simulator/sess-1/2026/11/05/1793870100000_msg-1.json"
	if got := store.S3RawMessageKey("demo", "simulator", "sess-1", ts, "msg-1"); got != expectedRaw {
		t.Fatalf("want %s, got %s", expectedRaw, got)
	}
	if got := store.S3MediaKey("demo", "whatsapp", "5511", ts, "m1", "ogg"); got != "demo/media/whatsapp/5511/2026/11/m1.ogg" {
		t.Fatalf("unexpected S3MediaKey: %s", got)
	}
	if got := store.S3TraceKey("demo", "simulator", "sess-1", "run-1"); got != "demo/traces/simulator/sess-1/run-1.json" {
		t.Fatalf("unexpected S3TraceKey: %s", got)
	}
	if got := store.S3KnowledgeKey("demo", "QA.md"); got != "demo/knowledge/QA.md" {
		t.Fatalf("unexpected S3KnowledgeKey: %s", got)
	}
}
