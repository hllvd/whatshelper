package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/channels/admin"
	"whatshelper/internal/channels/bridge"
	"whatshelper/internal/config"
	"whatshelper/internal/core"
	"whatshelper/internal/core/fakes"
	"whatshelper/internal/server"
)

func TestConnectorEndpoints_AuthDedupeMediaAndAdminRoute(t *testing.T) {
	ctx := context.Background()
	st := fakes.NewMemoryStore()
	blobs := fakes.NewMemoryBlobStore()
	bus := core.NewInMemoryEventBus(32)
	pipeline := brain.NewPipeline(ctx, brain.PipelineConfig{
		Store:    st,
		Blobs:    blobs,
		Bus:      bus,
		Debounce: time.Hour,
	})
	defer pipeline.Stop()

	rc := bridge.NewRemoteChannel("whatsapp", core.ChannelCapabilities{Media: true}, nil)
	defer func() { _ = rc.Stop(ctx) }()

	cfg := config.Config{
		HTTPHost:       "127.0.0.1",
		HTTPPort:       8080,
		APIToken:       "user-api-token",
		ConnectorToken: "conn-secret-token",
		LLMProvider:    "fake",
	}
	srv := server.NewWithDeps(cfg, nil, server.Dependencies{
		Store:          st,
		Blobs:          blobs,
		Bus:            bus,
		Pipeline:       pipeline,
		RemoteChannels: map[string]*bridge.RemoteChannel{"whatsapp": rc},
	})
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 1. Token de usuário NÃO deve autenticar endpoint exclusivo do connector quando CONNECTOR_TOKEN difere.
	reqBad, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/connectors/v1/inbound", bytes.NewBufferString("{}"))
	reqBad.Header.Set("Authorization", "Bearer user-api-token")
	respBad, err := http.DefaultClient.Do(reqBad)
	if err != nil {
		t.Fatal(err)
	}
	_ = respBad.Body.Close()
	if respBad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 when using wrong connector token, got %d", respBad.StatusCode)
	}

	// 2. Upload de mídia via bridge Client e envio inbound idempotente.
	client, err := bridge.NewClient(bridge.ClientConfig{
		BrainBaseURL: ts.URL,
		Token:        "conn-secret-token",
		Channel:      "whatsapp",
	})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 11, 15, 0, 0, 0, time.UTC)
	mediaRef, err := client.UploadMediaWithRetry(ctx, bridge.MediaUploadParams{
		TenantID:  "demo",
		Channel:   "whatsapp",
		SourceKey: "5511999998888",
		MediaID:   "att-1",
		Ext:       "ogg",
		MimeType:  "audio/ogg",
		Timestamp: now,
	}, []byte("ogg-audio-bytes"))
	if err != nil {
		t.Fatalf("UploadMediaWithRetry failed: %v", err)
	}
	if gotBytes, err := blobs.Get(ctx, mediaRef); err != nil || string(gotBytes) != "ogg-audio-bytes" {
		t.Fatalf("expected stored media bytes at %q, err=%v", mediaRef, err)
	}

	inReq := bridge.InboundRequest{
		TenantID:  "demo",
		Channel:   "whatsapp",
		SourceKey: "5511999998888",
		MessageID: "wamid-dedupe-1",
		Timestamp: now,
		Type:      core.MessageTypeAudio,
		Text:      "Audio transcrito",
		MediaRef:  mediaRef,
	}
	r1, err := client.SendInboundWithRetry(ctx, inReq)
	if err != nil || r1.Status != "accepted" {
		t.Fatalf("expected first inbound accepted, got %+v err=%v", r1, err)
	}
	r2, err := client.SendInboundWithRetry(ctx, inReq)
	if err != nil || r2.Status != "duplicate" {
		t.Fatalf("expected second inbound duplicate, got %+v err=%v", r2, err)
	}

	// 3. Versão de protocolo incompatível deve falhar com 400.
	badVerBody, _ := json.Marshal(inReq)
	reqVer, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/connectors/v1/inbound", bytes.NewReader(badVerBody))
	reqVer.Header.Set("Authorization", "Bearer conn-secret-token")
	reqVer.Header.Set(bridge.HeaderProtocolVersion, "99")
	respVer, err := http.DefaultClient.Do(reqVer)
	if err != nil {
		t.Fatal(err)
	}
	_ = respVer.Body.Close()
	if respVer.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for unsupported protocol version, got %d", respVer.StatusCode)
	}

	// 4. RouteToAdmin deve redirecionar para o canal admin.
	rAdmin, err := client.SendInboundWithRetry(ctx, bridge.InboundRequest{
		TenantID:     "demo",
		Channel:      "whatsapp",
		SourceKey:    "5511988887777",
		MessageID:    "wamid-admin-1",
		Timestamp:    now,
		Type:         core.MessageTypeText,
		Text:         "/ajuda",
		RouteToAdmin: true,
	})
	if err != nil || rAdmin.Status != "accepted" {
		t.Fatalf("expected admin routed message accepted, got %+v err=%v", rAdmin, err)
	}
	adminMsgs, err := st.ListRecentMessages(ctx, "demo", brain.AdminChannelID, admin.DefaultAdminSourceKey, 10)
	if err != nil || len(adminMsgs) < 1 {
		t.Fatalf("expected admin messages persisted, got %d err=%v", len(adminMsgs), err)
	}
	foundAdminInbound := false
	for _, m := range adminMsgs {
		if m.MessageID == "wamid-admin-1" && m.Direction == core.DirectionInbound {
			foundAdminInbound = true
			break
		}
	}
	if !foundAdminInbound {
		t.Fatalf("expected wamid-admin-1 routed to admin channel, got %+v", adminMsgs)
	}
}
