package store

import (
	"fmt"
	"time"
)

const (
	// TimestampFormat garante ordenação lexicográfica idêntica à cronológica (UTC com nanossegundos fixos).
	TimestampFormat = "2006-01-02T15:04:05.000000000Z"

	SKMeta    = "META"
	SKSummary = "SUMMARY"
	SKFacts   = "FACTS"
	SKSignals = "SIGNALS"
	SKContext = "CONTEXT"
)

// SourcePK retorna a chave de partição da conversa: T#<tenant>#S#<canal>#<source_key>.
func SourcePK(tenant, channel, sourceKey string) string {
	return fmt.Sprintf("T#%s#S#%s#%s", tenant, channel, sourceKey)
}

// TenantPK retorna a chave de partição do tenant: T#<tenant>.
func TenantPK(tenant string) string {
	return fmt.Sprintf("T#%s", tenant)
}

// TenantSourceSK retorna a sort key do catálogo de fontes do tenant: SRC#<canal>#<source_key>.
func TenantSourceSK(channel, sourceKey string) string {
	return fmt.Sprintf("SRC#%s#%s", channel, sourceKey)
}

// TenantConfigSK retorna a sort key de configuração do tenant: CONFIG#<name>.
func TenantConfigSK(name string) string {
	return fmt.Sprintf("CONFIG#%s", name)
}

// MessageSK retorna a sort key cronológica de mensagem: MSG#<ts>#<message_id>.
func MessageSK(ts time.Time, messageID string) string {
	return fmt.Sprintf("MSG#%s#%s", ts.UTC().Format(TimestampFormat), messageID)
}

// MessageDedupSK retorna a sort key auxiliar de deduplicação por message_id.
func MessageDedupSK(messageID string) string {
	return fmt.Sprintf("DEDUP#MSG#%s", messageID)
}

// TaskSK retorna a sort key de uma tarefa: TASK#<id>.
func TaskSK(taskID string) string {
	return fmt.Sprintf("TASK#%s", taskID)
}

// RunSK retorna a sort key de um trace de execução: RUN#<ts>#<run_id>.
func RunSK(ts time.Time, runID string) string {
	return fmt.Sprintf("RUN#%s#%s", ts.UTC().Format(TimestampFormat), runID)
}

// ContactPK retorna a chave de partição de um contato unificado: T#<tenant>#CONTACT#<contact_id>.
func ContactPK(tenant, contactID string) string {
	return fmt.Sprintf("T#%s#CONTACT#%s", tenant, contactID)
}

// DueGSI2PK retorna a partition key do GSI2 para trabalhos vencidos: T#<tenant>#DUE.
func DueGSI2PK(tenant string) string {
	return fmt.Sprintf("T#%s#DUE", tenant)
}

// DueGSI2SK retorna a sort key do GSI2: <due_at>#<canal>#<source_key>.
func DueGSI2SK(dueAt time.Time, channel, sourceKey string) string {
	return fmt.Sprintf("%s#%s#%s", dueAt.UTC().Format(TimestampFormat), channel, sourceKey)
}

// S3RawMessageKey gera a chave S3 de mensagem crua: <tenant>/<canal>/<source_key>/<yyyy>/<mm>/<dd>/<ts>_<message_id>.json.
func S3RawMessageKey(tenant, channel, sourceKey string, ts time.Time, messageID string) string {
	u := ts.UTC()
	return fmt.Sprintf("%s/%s/%s/%04d/%02d/%02d/%d_%s.json",
		tenant, channel, sourceKey, u.Year(), int(u.Month()), u.Day(), u.UnixMilli(), messageID)
}

// S3MediaKey gera a chave S3 para arquivos de mídia: <tenant>/media/<canal>/<source_key>/<yyyy>/<mm>/<media_id>.<ext>.
func S3MediaKey(tenant, channel, sourceKey string, ts time.Time, mediaID, ext string) string {
	u := ts.UTC()
	return fmt.Sprintf("%s/media/%s/%s/%04d/%02d/%s.%s",
		tenant, channel, sourceKey, u.Year(), int(u.Month()), mediaID, ext)
}

// S3TraceKey gera a chave S3 para trace completo de execução: <tenant>/traces/<canal>/<source_key>/<run_id>.json.
func S3TraceKey(tenant, channel, sourceKey, runID string) string {
	return fmt.Sprintf("%s/traces/%s/%s/%s.json", tenant, channel, sourceKey, runID)
}

// S3DraftKey gera a chave S3 para rascunhos de resposta aguardando aprovação/revisão.
func S3DraftKey(tenant, channel, sourceKey, runID string) string {
	return fmt.Sprintf("%s/drafts/%s/%s/%s.json", tenant, channel, sourceKey, runID)
}

// S3KnowledgeKey gera a chave S3 para documentos de conhecimento: <tenant>/knowledge/<filename>.
func S3KnowledgeKey(tenant, filename string) string {
	return fmt.Sprintf("%s/knowledge/%s", tenant, filename)
}

// S3LearningKey gera a chave S3 para propostas de aprendizado pendentes: <tenant>/learning/<proposal_id>.json.
func S3LearningKey(tenant, proposalID string) string {
	return fmt.Sprintf("%s/learning/%s.json", tenant, proposalID)
}
