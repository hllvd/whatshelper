package core_test

import (
	"testing"
	"time"

	"whatshelper/internal/core"
)

func TestTask_Validate(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name    string
		task    core.Task
		wantErr bool
	}{
		{
			name: "valid pending task",
			task: core.Task{
				ID:        "task-1",
				TenantID:  "tenant-1",
				Channel:   "whatsapp",
				SourceKey: "5511999998888",
				Title:     "Agendar retorno telefônico",
				Type:      "call",
				Status:    core.TaskStatusPending,
				CreatedAt: now,
			},
			wantErr: false,
		},
		{
			name: "missing id",
			task: core.Task{
				TenantID:  "tenant-1",
				Channel:   "whatsapp",
				SourceKey: "5511999998888",
				Title:     "Agendar retorno telefônico",
			},
			wantErr: true,
		},
		{
			name: "missing tenant_id",
			task: core.Task{
				ID:        "task-1",
				Channel:   "whatsapp",
				SourceKey: "5511999998888",
				Title:     "Agendar retorno telefônico",
			},
			wantErr: true,
		},
		{
			name: "missing source_key",
			task: core.Task{
				ID:       "task-1",
				TenantID: "tenant-1",
				Channel:  "whatsapp",
				Title:    "Agendar retorno telefônico",
			},
			wantErr: true,
		},
		{
			name: "missing title",
			task: core.Task{
				ID:        "task-1",
				TenantID:  "tenant-1",
				Channel:   "whatsapp",
				SourceKey: "5511999998888",
				Title:     "   ",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.task.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
