package whatsapp_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatshelper/internal/channels/whatsapp"
	"whatshelper/internal/core"
)

func TestNormalizeE164AndAdminCheck(t *testing.T) {
	cases := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{raw: "+55 11 99999-8888", want: "5511999998888"},
		{raw: "5511999998888@s.whatsapp.net", want: "5511999998888"},
		{raw: "5511999998888:12@s.whatsapp.net", want: "5511999998888"},
		{raw: "005511999998888", want: "5511999998888"},
		{raw: "123", wantErr: true},
		{raw: "", wantErr: true},
	}

	for _, tc := range cases {
		got, err := whatsapp.NormalizeE164(tc.raw)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("NormalizeE164(%q) expected error, got %q", tc.raw, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("NormalizeE164(%q) = %q, %v (want %q)", tc.raw, got, err, tc.want)
		}
	}

	if !whatsapp.IsAdminPhone("5511988887777", "+55 (11) 98888-7777") {
		t.Fatalf("expected matching admin phone")
	}
	if whatsapp.IsAdminPhone("5511999998888", "+5511988887777") {
		t.Fatalf("expected non-matching phone")
	}
}

func TestShouldProcessInboundAndExtractContent(t *testing.T) {
	if whatsapp.ShouldProcessInbound(true, false, false, false) {
		t.Fatalf("from-me messages must be ignored")
	}
	if whatsapp.ShouldProcessInbound(false, true, false, false) {
		t.Fatalf("group messages must be ignored when allowGroups=false")
	}
	if whatsapp.ShouldProcessInbound(false, false, true, false) {
		t.Fatalf("broadcast messages must be ignored")
	}
	if !whatsapp.ShouldProcessInbound(false, false, false, false) {
		t.Fatalf("direct inbound messages must be processed")
	}

	ephemeral := &waE2E.Message{
		EphemeralMessage: &waE2E.FutureProofMessage{
			Message: &waE2E.Message{
				ExtendedTextMessage: &waE2E.ExtendedTextMessage{
					Text: proto.String("  Olá pelo efêmero  "),
				},
			},
		},
	}
	parsed, ok := whatsapp.ExtractContent(ephemeral)
	if !ok || parsed.Type != core.MessageTypeText || parsed.Text != "Olá pelo efêmero" {
		t.Fatalf("unexpected extracted ephemeral text: ok=%v parsed=%+v", ok, parsed)
	}

	audParsed, ok := whatsapp.ExtractContent(&waE2E.Message{
		AudioMessage: &waE2E.AudioMessage{
			Mimetype: proto.String("audio/ogg; codecs=opus"),
		},
	})
	if !ok || audParsed.Type != core.MessageTypeAudio || audParsed.Ext != "ogg" {
		t.Fatalf("unexpected extracted audio: ok=%v parsed=%+v", ok, audParsed)
	}
}

func TestBuildInboundRequest_LIDFallbackToSenderAltAndAdminRoute(t *testing.T) {
	evt := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				Chat:      types.NewJID("1234567890", types.HiddenUserServer),
				Sender:    types.NewJID("1234567890", types.HiddenUserServer),
				SenderAlt: types.NewJID("5511988887777", types.DefaultUserServer),
			},
			ID:        "WAMID-1",
			PushName:  "Admin Teste",
			Timestamp: time.Date(2026, 11, 11, 19, 0, 0, 0, time.UTC),
		},
	}
	req, err := whatsapp.BuildInboundRequest("demo", "main", "+55 11 98888-7777", evt, whatsapp.ParsedContent{
		Type: core.MessageTypeText,
		Text: "/aprovar prop-1",
	}, "")
	if err != nil {
		t.Fatalf("BuildInboundRequest failed: %v", err)
	}
	if req.SourceKey != "5511988887777" || !req.RouteToAdmin || req.RawContact != "+5511988887777" || req.DisplayName != "Admin Teste" {
		t.Fatalf("unexpected InboundRequest: %+v", req)
	}
}

func TestOpenSQLiteContainer_PureGoNoCGO(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wa.db")
	container, db, err := whatsapp.OpenSQLiteContainer(context.Background(), dbPath, nil)
	if err != nil {
		t.Fatalf("OpenSQLiteContainer failed: %v", err)
	}
	defer func() { _ = db.Close() }()

	dev, err := container.GetFirstDevice(context.Background())
	if err != nil {
		t.Fatalf("GetFirstDevice failed: %v", err)
	}
	if dev == nil {
		t.Fatalf("expected non-nil device")
	}
}
