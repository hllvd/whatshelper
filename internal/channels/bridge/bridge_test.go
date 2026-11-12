package bridge_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"whatshelper/internal/channels/bridge"
	"whatshelper/internal/core"
)

type fakeOutboundHandler struct {
	mu       sync.Mutex
	sent     []core.NormalizedMessage
	typings  []bool
	notifyCh chan core.NormalizedMessage
	health   core.ChannelHealth
}

func newFakeOutboundHandler() *fakeOutboundHandler {
	return &fakeOutboundHandler{
		notifyCh: make(chan core.NormalizedMessage, 16),
		health:   core.ChannelHealth{Healthy: true, Detail: "online"},
	}
}

func (f *fakeOutboundHandler) Send(ctx context.Context, msg core.NormalizedMessage) error {
	f.mu.Lock()
	f.sent = append(f.sent, msg)
	f.mu.Unlock()
	select {
	case f.notifyCh <- msg:
	default:
	}
	return nil
}

func (f *fakeOutboundHandler) SendTyping(ctx context.Context, sourceKey string, typing bool) error {
	f.mu.Lock()
	f.typings = append(f.typings, typing)
	f.mu.Unlock()
	return nil
}

func (f *fakeOutboundHandler) Health() core.ChannelHealth {
	return f.health
}

func TestBackoffDelay(t *testing.T) {
	if got := bridge.BackoffDelay(0, 200*time.Millisecond, 2*time.Second); got != 200*time.Millisecond {
		t.Fatalf("attempt 0: expected 200ms, got %v", got)
	}
	if got := bridge.BackoffDelay(2, 200*time.Millisecond, 2*time.Second); got != 800*time.Millisecond {
		t.Fatalf("attempt 2: expected 800ms, got %v", got)
	}
	if got := bridge.BackoffDelay(10, 200*time.Millisecond, 2*time.Second); got != 2*time.Second {
		t.Fatalf("attempt 10: expected capped 2s, got %v", got)
	}
}

func TestClient_InboundRetryAndPermanentFailure(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := attempts.Add(1)
		if r.Header.Get(bridge.HeaderProtocolVersion) != bridge.ProtocolVersion {
			http.Error(w, "bad version", http.StatusBadRequest)
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok-1" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if cur < 3 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(bridge.InboundResponse{Status: "accepted", MessageID: "m1"})
	}))
	defer srv.Close()

	client, err := bridge.NewClient(bridge.ClientConfig{
		BrainBaseURL:   srv.URL,
		Token:          "tok-1",
		BackoffBase:    time.Millisecond,
		BackoffMax:     2 * time.Millisecond,
		MaxHTTPRetries: 4,
		SleepFunc:      func(ctx context.Context, d time.Duration) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.SendInboundWithRetry(context.Background(), bridge.InboundRequest{
		TenantID:  "demo",
		Channel:   "whatsapp",
		SourceKey: "5511999998888",
		MessageID: "m1",
		Timestamp: time.Now().UTC(),
		Type:      core.MessageTypeText,
		Text:      "Oi",
	})
	if err != nil {
		t.Fatalf("expected retry success, got %v", err)
	}
	if resp.Status != "accepted" || attempts.Load() != 3 {
		t.Fatalf("expected 3 attempts and accepted status, got %d %+v", attempts.Load(), resp)
	}

	// Falha 401 permanente não deve ser retentada.
	attempts.Store(0)
	badClient, _ := bridge.NewClient(bridge.ClientConfig{
		BrainBaseURL:   srv.URL,
		Token:          "wrong",
		MaxHTTPRetries: 4,
		SleepFunc:      func(ctx context.Context, d time.Duration) error { return nil },
	})
	if _, err := badClient.SendInboundWithRetry(context.Background(), bridge.InboundRequest{
		TenantID: "demo", Channel: "whatsapp", SourceKey: "5511999998888", MessageID: "m2", Type: core.MessageTypeText, Text: "Oi",
	}); err == nil {
		t.Fatalf("expected 401 failure")
	}
	if attempts.Load() != 1 {
		t.Fatalf("expected no retry on 401, got %d attempts", attempts.Load())
	}
}

func TestRemoteChannel_OutboundAckTypingAndAutoReconnect(t *testing.T) {
	rc := bridge.NewRemoteChannel("whatsapp", core.ChannelCapabilities{Typing: true}, nil)
	defer func() { _ = rc.Stop(context.Background()) }()

	var wsHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wsHits.Add(1)
		rc.HandleWebSocket(w, r)
	}))
	defer srv.Close()

	client, err := bridge.NewClient(bridge.ClientConfig{
		BrainBaseURL: srv.URL,
		Channel:      "whatsapp",
		BackoffBase:  5 * time.Millisecond,
		BackoffMax:   10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	handler := newFakeOutboundHandler()
	go func() {
		_ = client.RunWS(ctx, handler)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for !rc.Health().Healthy && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !rc.Health().Healthy {
		t.Fatalf("expected remote channel connected")
	}

	if err := rc.SendTyping(ctx, "5511999998888", true); err != nil {
		t.Fatalf("SendTyping failed: %v", err)
	}
	if err := rc.Send(ctx, core.NormalizedMessage{
		TenantID:  "demo",
		Channel:   "whatsapp",
		SourceKey: "5511999998888",
		MessageID: "out-1",
		Timestamp: time.Now().UTC(),
		Direction: core.DirectionOutbound,
		Type:      core.MessageTypeText,
		Text:      "Primeira resposta",
	}); err != nil {
		t.Fatalf("Send 1 failed: %v", err)
	}

	// Derruba a conexão WebSocket ativa para simular queda de rede e validar reconexão automática.
	rc.Disconnect()

	// Aguarda segunda conexão WS estabelecer.
	deadline = time.Now().Add(2 * time.Second)
	for (wsHits.Load() < 2 || !rc.Health().Healthy) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if wsHits.Load() < 2 || !rc.Health().Healthy {
		t.Fatalf("expected automatic WS reconnect (hits=%d healthy=%v)", wsHits.Load(), rc.Health().Healthy)
	}

	if err := rc.Send(ctx, core.NormalizedMessage{
		TenantID:  "demo",
		Channel:   "whatsapp",
		SourceKey: "5511999998888",
		MessageID: "out-2",
		Timestamp: time.Now().UTC(),
		Direction: core.DirectionOutbound,
		Type:      core.MessageTypeText,
		Text:      "Segunda após reconexão",
	}); err != nil {
		t.Fatalf("Send 2 after reconnect failed: %v", err)
	}

	handler.mu.Lock()
	defer handler.mu.Unlock()
	if len(handler.sent) != 2 || len(handler.typings) == 0 {
		t.Fatalf("expected 2 sent messages and typing indicator, got sent=%d typings=%d", len(handler.sent), len(handler.typings))
	}
}
