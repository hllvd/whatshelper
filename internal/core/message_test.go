package core_test

import (
	"testing"
	"time"

	"whatshelper/internal/core"
)

func TestNormalizedMessage_Validate(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name    string
		msg     core.NormalizedMessage
		wantErr bool
	}{
		{
			name: "valid text inbound message",
			msg: core.NormalizedMessage{
				TenantID:  "tenant-1",
				Channel:   "simulator",
				Account:   "acc-1",
				SourceKey: "user-123",
				MessageID: "msg-001",
				Timestamp: now,
				Direction: core.DirectionInbound,
				Type:      core.MessageTypeText,
				Text:      "Olá, gostaria de saber mais",
			},
			wantErr: false,
		},
		{
			name: "valid outbound audio message with media ref",
			msg: core.NormalizedMessage{
				TenantID:  "tenant-1",
				Channel:   "whatsapp",
				Account:   "acc-1",
				SourceKey: "5511999998888",
				MessageID: "msg-002",
				Timestamp: now,
				Direction: core.DirectionOutbound,
				Type:      core.MessageTypeAudio,
				MediaRef:  "s3://bucket/audio.ogg",
			},
			wantErr: false,
		},
		{
			name: "missing tenant_id",
			msg: core.NormalizedMessage{
				Channel:   "simulator",
				SourceKey: "user-123",
				MessageID: "msg-001",
				Timestamp: now,
				Direction: core.DirectionInbound,
				Type:      core.MessageTypeText,
				Text:      "Olá",
			},
			wantErr: true,
		},
		{
			name: "missing channel",
			msg: core.NormalizedMessage{
				TenantID:  "tenant-1",
				SourceKey: "user-123",
				MessageID: "msg-001",
				Timestamp: now,
				Direction: core.DirectionInbound,
				Type:      core.MessageTypeText,
				Text:      "Olá",
			},
			wantErr: true,
		},
		{
			name: "missing source_key",
			msg: core.NormalizedMessage{
				TenantID:  "tenant-1",
				Channel:   "simulator",
				MessageID: "msg-001",
				Timestamp: now,
				Direction: core.DirectionInbound,
				Type:      core.MessageTypeText,
				Text:      "Olá",
			},
			wantErr: true,
		},
		{
			name: "missing message_id",
			msg: core.NormalizedMessage{
				TenantID:  "tenant-1",
				Channel:   "simulator",
				SourceKey: "user-123",
				Timestamp: now,
				Direction: core.DirectionInbound,
				Type:      core.MessageTypeText,
				Text:      "Olá",
			},
			wantErr: true,
		},
		{
			name: "zero timestamp",
			msg: core.NormalizedMessage{
				TenantID:  "tenant-1",
				Channel:   "simulator",
				SourceKey: "user-123",
				MessageID: "msg-001",
				Direction: core.DirectionInbound,
				Type:      core.MessageTypeText,
				Text:      "Olá",
			},
			wantErr: true,
		},
		{
			name: "invalid direction",
			msg: core.NormalizedMessage{
				TenantID:  "tenant-1",
				Channel:   "simulator",
				SourceKey: "user-123",
				MessageID: "msg-001",
				Timestamp: now,
				Direction: "sideways",
				Type:      core.MessageTypeText,
				Text:      "Olá",
			},
			wantErr: true,
		},
		{
			name: "invalid message type",
			msg: core.NormalizedMessage{
				TenantID:  "tenant-1",
				Channel:   "simulator",
				SourceKey: "user-123",
				MessageID: "msg-001",
				Timestamp: now,
				Direction: core.DirectionInbound,
				Type:      "video",
				Text:      "Olá",
			},
			wantErr: true,
		},
		{
			name: "empty text content for text type",
			msg: core.NormalizedMessage{
				TenantID:  "tenant-1",
				Channel:   "simulator",
				SourceKey: "user-123",
				MessageID: "msg-001",
				Timestamp: now,
				Direction: core.DirectionInbound,
				Type:      core.MessageTypeText,
				Text:      "   ",
			},
			wantErr: true,
		},
		{
			name: "missing media ref for audio type without text",
			msg: core.NormalizedMessage{
				TenantID:  "tenant-1",
				Channel:   "simulator",
				SourceKey: "user-123",
				MessageID: "msg-001",
				Timestamp: now,
				Direction: core.DirectionInbound,
				Type:      core.MessageTypeAudio,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.msg.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
