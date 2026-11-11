package brain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"whatshelper/internal/core"
	"whatshelper/internal/pack"
)

// DraftPayload representa o conteúdo persistido no BlobStore para rascunhos ou aprovações pendentes.
type DraftPayload struct {
	RunID         string    `json:"run_id"`
	TenantID      string    `json:"tenant_id"`
	Channel       string    `json:"channel"`
	SourceKey     string    `json:"source_key"`
	Account       string    `json:"account,omitempty"`
	BusinessActor string    `json:"business_actor"`
	Text          string    `json:"text"`
	Intent        string    `json:"intent,omitempty"`
	Mode          string    `json:"mode"`
	CreatedAt     time.Time `json:"created_at"`
}

// DeriveTasksInput agrupa entradas puras para derivar tarefas configuradas no pack.
type DeriveTasksInput struct {
	Pack      *pack.Pack
	Analysis  *pack.AnalysisResult
	TenantID  string
	Channel   string
	SourceKey string
	MessageID string
	Now       time.Time
}

// DeriveTasks gera deterministicamente tarefas a partir das regras declaradas no pack.yaml.
func DeriveTasks(in DeriveTasksInput) []core.Task {
	if in.Pack == nil || in.Analysis == nil || len(in.Pack.TaskRules) == 0 {
		return nil
	}
	now := in.Now.UTC()
	var tasks []core.Task

	for _, rule := range in.Pack.TaskRules {
		signalMatched := rule.TriggerSignal != "" && in.Analysis.Signals[rule.TriggerSignal]
		intentMatched := rule.TriggerIntent != "" && in.Analysis.Intent == rule.TriggerIntent
		if !signalMatched && !intentMatched {
			continue
		}

		due := now
		if rule.DueInMinutes > 0 {
			due = now.Add(time.Duration(rule.DueInMinutes) * time.Minute)
		}

		meta := map[string]string{
			"message_id": in.MessageID,
		}
		if in.Analysis.Intent != "" {
			meta["intent"] = in.Analysis.Intent
		}
		if rule.ContextSection != "" {
			for k, v := range in.Analysis.Context[rule.ContextSection] {
				if strings.TrimSpace(v) != "" {
					meta[rule.ContextSection+"."+k] = v
				}
			}
		}

		tasks = append(tasks, core.Task{
			ID:        deterministicTaskID(in.TenantID, in.Channel, in.SourceKey, in.MessageID, rule.Type),
			TenantID:  in.TenantID,
			Channel:   in.Channel,
			SourceKey: in.SourceKey,
			Kind:      "task",
			Title:     rule.Title,
			Type:      rule.Type,
			Status:    core.TaskStatusPending,
			DueAt:     &due,
			Metadata:  meta,
			CreatedAt: now,
		})
	}
	return tasks
}

// FilterNewTasks descarta tarefas cujo Type já possua uma tarefa pendente na fonte.
func FilterNewTasks(derived, existing []core.Task) []core.Task {
	pendingTypes := make(map[string]bool)
	for _, t := range existing {
		if t.Status == core.TaskStatusPending {
			pendingTypes[t.Type] = true
		}
	}
	var out []core.Task
	for _, t := range derived {
		if pendingTypes[t.Type] {
			continue
		}
		pendingTypes[t.Type] = true
		out = append(out, t)
	}
	return out
}

func deterministicTaskID(tenant, channel, sourceKey, msgID, taskType string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{tenant, channel, sourceKey, msgID, taskType}, "|")))
	cleanType := strings.ReplaceAll(strings.TrimSpace(taskType), "#", "_")
	return fmt.Sprintf("rule_%s_%s", cleanType, hex.EncodeToString(sum[:8]))
}
