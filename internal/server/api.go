package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/channels/admin"
	"whatshelper/internal/core"
	"whatshelper/internal/store"
)

type simMessageRequest struct {
	TenantID    string `json:"tenant_id"`
	SourceKey   string `json:"source_key"`
	Text        string `json:"text"`
	Type        string `json:"type,omitempty"`
	AudioBase64 string `json:"audio_base64,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
	FileName    string `json:"file_name,omitempty"`
}

type chatMessageView struct {
	MessageID string             `json:"message_id"`
	Timestamp time.Time          `json:"timestamp"`
	Direction core.Direction     `json:"direction"`
	Origin    core.MessageOrigin `json:"origin,omitempty"`
	Type      core.MessageType   `json:"type"`
	Text      string             `json:"text"`
	Sender    string             `json:"sender,omitempty"`
	RawRef    string             `json:"raw_ref,omitempty"`
	Answered  bool               `json:"answered"`
}

func (s *Server) handlePostSimMessage(w http.ResponseWriter, r *http.Request) {
	if s.deps.Simulator == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "simulador não inicializado")
		return
	}

	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(16 << 20); err != nil {
			writeJSONError(w, http.StatusBadRequest, "falha ao ler multipart form")
			return
		}
		tenantID := strings.TrimSpace(r.FormValue("tenant_id"))
		if tenantID == "" {
			tenantID = "demo"
		}
		sourceKey := strings.TrimSpace(r.FormValue("source_key"))
		text := strings.TrimSpace(r.FormValue("text"))

		file, handler, fileErr := r.FormFile("file")
		if fileErr == nil && file != nil {
			defer file.Close()
			audioBytes, err := io.ReadAll(io.LimitReader(file, 16<<20))
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "falha ao ler arquivo de áudio")
				return
			}
			mimeType := handler.Header.Get("Content-Type")
			if mimeType == "" || mimeType == "application/octet-stream" {
				mimeType = inferMimeFromName(handler.Filename)
			}
			msg, err := s.ingestSimAudio(r.Context(), tenantID, sourceKey, text, mimeType, audioBytes)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusAccepted, msg)
			return
		}

		msg, err := s.deps.Simulator.SubmitInbound(r.Context(), tenantID, sourceKey, text)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, msg)
		return
	}

	var req simMessageRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	if strings.TrimSpace(req.TenantID) == "" {
		req.TenantID = "demo"
	}

	if (req.Type == "audio" || req.AudioBase64 != "") && len(req.AudioBase64) > 0 {
		rawB64 := req.AudioBase64
		if idx := strings.Index(rawB64, ","); idx != -1 {
			rawB64 = rawB64[idx+1:]
		}
		audioBytes, err := base64.StdEncoding.DecodeString(rawB64)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "áudio em base64 inválido")
			return
		}
		mimeType := strings.TrimSpace(req.MimeType)
		if mimeType == "" {
			mimeType = inferMimeFromName(req.FileName)
		}
		msg, err := s.ingestSimAudio(r.Context(), req.TenantID, req.SourceKey, req.Text, mimeType, audioBytes)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, msg)
		return
	}

	msg, err := s.deps.Simulator.SubmitInbound(r.Context(), req.TenantID, req.SourceKey, req.Text)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, msg)
}

func (s *Server) ingestSimAudio(ctx context.Context, tenantID, sourceKey, text, mimeType string, audioBytes []byte) (core.NormalizedMessage, error) {
	if len(audioBytes) == 0 {
		return core.NormalizedMessage{}, errors.New("áudio vazio")
	}
	if s.deps.Blobs == nil {
		return core.NormalizedMessage{}, errors.New("armazenamento de arquivos (BlobStore) não configurado")
	}
	normMime := strings.TrimSpace(mimeType)
	if normMime == "" {
		normMime = "audio/webm"
	}
	ext := extFromAudioMime(normMime)
	now := time.Now().UTC()
	mediaKey := store.S3MediaKey(tenantID, "simulator", sourceKey, now, fmt.Sprintf("%d", now.UnixNano()), ext)
	if err := s.deps.Blobs.Put(ctx, mediaKey, audioBytes, normMime); err != nil {
		return core.NormalizedMessage{}, fmt.Errorf("falha ao salvar áudio no BlobStore: %w", err)
	}
	caption := text
	if strings.TrimSpace(caption) == "" {
		caption = "[Áudio enviado]"
	}
	return s.deps.Simulator.SubmitInboundMedia(ctx, tenantID, sourceKey, core.MessageTypeAudio, mediaKey, caption)
}

func extFromAudioMime(mimeType string) string {
	lower := strings.ToLower(mimeType)
	switch {
	case strings.Contains(lower, "webm"):
		return "webm"
	case strings.Contains(lower, "ogg"):
		return "ogg"
	case strings.Contains(lower, "mp3") || strings.Contains(lower, "mpeg"):
		return "mp3"
	case strings.Contains(lower, "mp4") || strings.Contains(lower, "m4a"):
		return "m4a"
	case strings.Contains(lower, "wav"):
		return "wav"
	default:
		return "ogg"
	}
}

func inferMimeFromName(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".webm"):
		return "audio/webm"
	case strings.HasSuffix(lower, ".ogg"):
		return "audio/ogg"
	case strings.HasSuffix(lower, ".mp3"):
		return "audio/mpeg"
	case strings.HasSuffix(lower, ".m4a") || strings.HasSuffix(lower, ".mp4"):
		return "audio/mp4"
	case strings.HasSuffix(lower, ".wav"):
		return "audio/wav"
	default:
		return "audio/webm"
	}
}

type simTypingRequest struct {
	TenantID  string `json:"tenant_id"`
	SourceKey string `json:"source_key"`
	Typing    bool   `json:"typing"`
}

func (s *Server) handlePostSimTyping(w http.ResponseWriter, r *http.Request) {
	if s.deps.Pipeline == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "pipeline não inicializado")
		return
	}
	var req simTypingRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	tenant := strings.TrimSpace(req.TenantID)
	if tenant == "" {
		tenant = "demo"
	}
	sourceKey := strings.TrimSpace(req.SourceKey)
	if sourceKey == "" {
		writeJSONError(w, http.StatusBadRequest, "source_key é obrigatório")
		return
	}
	s.deps.Pipeline.NotifyTyping(tenant, "simulator", sourceKey, req.Typing)
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "accepted"})
}

func (s *Server) handleListSources(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store não configurado")
		return
	}

	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant == "" {
		tenant = "demo"
	}

	sources, err := s.deps.Store.ListSources(r.Context(), tenant)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant":  tenant,
		"sources": sources,
	})
}

func (s *Server) handleGetSourceState(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store não configurado")
		return
	}

	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant == "" {
		tenant = "demo"
	}
	channel := r.PathValue("channel")
	key := r.PathValue("key")
	if channel == "" || key == "" {
		writeJSONError(w, http.StatusBadRequest, "channel e key são obrigatórios")
		return
	}

	state, err := s.deps.Store.GetSourceState(r.Context(), tenant, channel, key)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleGetSourceMessages(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store não configurado")
		return
	}

	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant == "" {
		tenant = "demo"
	}
	channel := r.PathValue("channel")
	key := r.PathValue("key")
	limit := parseIntQuery(r, "limit", 50)

	views, err := s.loadChatMessages(r.Context(), tenant, channel, key, limit)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"messages": views,
	})
}

func (s *Server) loadChatMessages(ctx context.Context, tenant, channel, key string, limit int) ([]chatMessageView, error) {
	metas, err := s.deps.Store.ListRecentMessages(ctx, tenant, channel, key, limit)
	if err != nil {
		return nil, err
	}

	// Retorna em ordem cronológica (mais antiga -> mais recente) para o chat
	views := make([]chatMessageView, 0, len(metas))
	for i := len(metas) - 1; i >= 0; i-- {
		m := metas[i]
		text := ""
		if m.RawRef != "" && s.deps.Blobs != nil {
			if raw, err := s.deps.Blobs.Get(ctx, m.RawRef); err == nil {
				var norm core.NormalizedMessage
				if json.Unmarshal(raw, &norm) == nil {
					text = norm.Text
				}
			}
		}
		views = append(views, chatMessageView{
			MessageID: m.MessageID,
			Timestamp: m.Timestamp,
			Direction: m.Direction,
			Origin:    m.Origin,
			Type:      m.Type,
			Text:      text,
			Sender:    m.Sender,
			RawRef:    m.RawRef,
			Answered:  m.Answered,
		})
	}
	return views, nil
}

func (s *Server) handleGetAdminMessages(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store não configurado")
		return
	}
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant == "" {
		tenant = "demo"
	}
	sourceKey := strings.TrimSpace(r.URL.Query().Get("source"))
	if sourceKey == "" {
		sourceKey = admin.DefaultAdminSourceKey
	}
	limit := parseIntQuery(r, "limit", 50)

	views, err := s.loadChatMessages(r.Context(), tenant, admin.ChannelID, sourceKey, limit)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant":   tenant,
		"channel":  admin.ChannelID,
		"messages": views,
	})
}

func (s *Server) handlePostAdminMessage(w http.ResponseWriter, r *http.Request) {
	if s.deps.AdminChannel == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "canal admin não inicializado")
		return
	}
	var req simMessageRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	if strings.TrimSpace(req.TenantID) == "" {
		req.TenantID = "demo"
	}
	if strings.TrimSpace(req.SourceKey) == "" {
		req.SourceKey = admin.DefaultAdminSourceKey
	}
	msg, err := s.deps.AdminChannel.SubmitInbound(r.Context(), req.TenantID, req.SourceKey, req.Text)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, msg)
}

func (s *Server) handleGetSourceRuns(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store não configurado")
		return
	}

	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant == "" {
		tenant = "demo"
	}
	channel := r.PathValue("channel")
	key := r.PathValue("key")
	limit := parseIntQuery(r, "limit", 30)

	runs, err := s.deps.Store.ListRuns(r.Context(), tenant, channel, key, limit)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"runs": runs,
	})
}

func (s *Server) handleGetRunTrace(w http.ResponseWriter, r *http.Request) {
	if s.deps.Blobs == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "blob store não configurado")
		return
	}

	runID := r.PathValue("id")
	q := r.URL.Query()
	tenant := strings.TrimSpace(q.Get("tenant"))
	if tenant == "" {
		tenant = "demo"
	}
	channel := strings.TrimSpace(q.Get("channel"))
	sourceKey := strings.TrimSpace(q.Get("source"))
	traceRef := strings.TrimSpace(q.Get("ref"))

	if traceRef == "" {
		if channel == "" || sourceKey == "" || runID == "" {
			writeJSONError(w, http.StatusBadRequest, "informe ?channel=&source= ou ?ref=")
			return
		}
		traceRef = store.S3TraceKey(tenant, channel, sourceKey, runID)
	}

	raw, err := s.deps.Blobs.Get(r.Context(), traceRef)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "trace não encontrado")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

type updateSourceStatusRequest struct {
	TenantID string            `json:"tenant_id"`
	Status   core.SourceStatus `json:"status"`
	Reason   string            `json:"reason"`
}

func isValidSourceStatus(s core.SourceStatus) bool {
	switch s {
	case core.SourceStatusActive, core.SourceStatusPaused, core.SourceStatusHuman, core.SourceStatusOptedOut, core.SourceStatusArchived:
		return true
	default:
		return false
	}
}

func (s *Server) handleUpdateSourceStatus(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store não configurado")
		return
	}

	channel := r.PathValue("channel")
	key := r.PathValue("key")
	if channel == "" || key == "" {
		writeJSONError(w, http.StatusBadRequest, "channel e key são obrigatórios")
		return
	}

	var req updateSourceStatusRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	tenant := strings.TrimSpace(req.TenantID)
	if tenant == "" {
		tenant = strings.TrimSpace(r.URL.Query().Get("tenant"))
	}
	if tenant == "" {
		tenant = "demo"
	}

	if !isValidSourceStatus(req.Status) {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("status inválido: %q", req.Status))
		return
	}

	if err := brain.UpdateSourceStatus(r.Context(), s.deps.Store, time.Now().UTC(), tenant, channel, key, req.Status); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if s.deps.Bus != nil {
		s.deps.Bus.Publish(core.Event{
			ID:        fmt.Sprintf("ev-%d", time.Now().UnixNano()),
			Type:      core.EventSourceStatusUpdated,
			TenantID:  tenant,
			Channel:   channel,
			SourceKey: key,
			At:        time.Now().UTC(),
			Payload: map[string]any{
				"status": string(req.Status),
				"reason": req.Reason,
			},
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tenant_id":  tenant,
		"channel":    channel,
		"source_key": key,
		"status":     req.Status,
	})
}

func (s *Server) handleGetTenantPolicy(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store não configurado")
		return
	}
	tenantID := strings.TrimSpace(r.PathValue("id"))
	if tenantID == "" {
		writeJSONError(w, http.StatusBadRequest, "tenant id obrigatório")
		return
	}
	var fallback brain.TenantConfigLoader
	if s.cfg.TenantsDir != "" {
		fallback = brain.NewLocalTenantLoader(s.cfg.TenantsDir)
	}
	loader := brain.NewStoreTenantPolicyLoader(s.deps.Store, fallback)
	policy, err := loader.LoadTenant(r.Context(), tenantID)
	if err != nil {
		if errors.Is(err, core.ErrConfigNotFound) {
			writeJSONError(w, http.StatusNotFound, "política do tenant não encontrada")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

func (s *Server) handlePutTenantPolicy(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store não configurado")
		return
	}
	tenantID := strings.TrimSpace(r.PathValue("id"))
	if tenantID == "" {
		writeJSONError(w, http.StatusBadRequest, "tenant id obrigatório")
		return
	}
	var policy core.TenantPolicy
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&policy); err != nil {
		writeJSONError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	policy.ID = tenantID
	policy = policy.WithDefaults()
	if err := policy.Validate(); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := brain.SeedTenantPolicy(r.Context(), s.deps.Store, policy); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

func (s *Server) handleGetSourceTasks(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store não configurado")
		return
	}
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant == "" {
		tenant = "demo"
	}
	channel := r.PathValue("channel")
	key := r.PathValue("key")
	if channel == "" || key == "" {
		writeJSONError(w, http.StatusBadRequest, "channel e key são obrigatórios")
		return
	}
	tasks, err := s.deps.Store.ListSourceTasks(r.Context(), tenant, channel, key)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tasks": tasks,
	})
}

func (s *Server) handleListPendingTasks(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store não configurado")
		return
	}
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant == "" {
		tenant = "demo"
	}
	farFuture := time.Now().UTC().Add(365 * 24 * time.Hour)
	due, err := s.deps.Store.ListDueTasks(r.Context(), tenant, farFuture)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pending := make([]core.Task, 0, len(due))
	for _, t := range due {
		if t.Status == core.TaskStatusPending {
			pending = append(pending, t)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant": tenant,
		"tasks":  pending,
	})
}

type taskActionRequest struct {
	TenantID  string `json:"tenant_id"`
	Channel   string `json:"channel"`
	SourceKey string `json:"source_key"`
	Text      string `json:"text,omitempty"`
}

func (s *Server) resolveTaskLocator(r *http.Request, taskID string, req taskActionRequest) (tenant, channel, sourceKey string, err error) {
	tenant = strings.TrimSpace(req.TenantID)
	if tenant == "" {
		tenant = strings.TrimSpace(r.URL.Query().Get("tenant"))
	}
	if tenant == "" {
		tenant = "demo"
	}
	channel = strings.TrimSpace(req.Channel)
	if channel == "" {
		channel = strings.TrimSpace(r.URL.Query().Get("channel"))
	}
	sourceKey = strings.TrimSpace(req.SourceKey)
	if sourceKey == "" {
		sourceKey = strings.TrimSpace(r.URL.Query().Get("source"))
	}
	if channel != "" && sourceKey != "" {
		return tenant, channel, sourceKey, nil
	}
	due, err := s.deps.Store.ListDueTasks(r.Context(), tenant, time.Now().UTC().Add(365*24*time.Hour))
	if err != nil {
		return "", "", "", err
	}
	for _, t := range due {
		if t.ID == taskID {
			return tenant, t.Channel, t.SourceKey, nil
		}
	}
	return "", "", "", core.ErrTaskNotFound
}

func (s *Server) handleCompleteTask(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store não configurado")
		return
	}
	taskID := r.PathValue("id")
	var req taskActionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeJSONError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	tenant, channel, sourceKey, err := s.resolveTaskLocator(r, taskID, req)
	if err != nil {
		if errors.Is(err, core.ErrTaskNotFound) {
			writeJSONError(w, http.StatusNotFound, "tarefa não encontrada")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	task, err := s.deps.Store.GetTask(r.Context(), tenant, channel, sourceKey, taskID)
	if err != nil {
		if errors.Is(err, core.ErrTaskNotFound) {
			writeJSONError(w, http.StatusNotFound, "tarefa não encontrada")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	now := time.Now().UTC()
	task.Status = core.TaskStatusCompleted
	task.CompletedAt = &now
	if err := s.deps.Store.SaveTask(r.Context(), *task); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleApproveTask(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil || s.deps.Pipeline == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "pipeline ou store não configurado")
		return
	}
	taskID := r.PathValue("id")
	var req taskActionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeJSONError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	tenant, channel, sourceKey, err := s.resolveTaskLocator(r, taskID, req)
	if err != nil {
		if errors.Is(err, core.ErrTaskNotFound) {
			writeJSONError(w, http.StatusNotFound, "tarefa de aprovação não encontrada")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	task, err := s.deps.Pipeline.ApproveTask(r.Context(), tenant, channel, sourceKey, taskID, req.Text)
	if err != nil {
		if errors.Is(err, core.ErrTaskNotFound) {
			writeJSONError(w, http.StatusNotFound, "tarefa não encontrada")
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleListContactSources(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store não configurado")
		return
	}
	contactID := strings.TrimSpace(r.PathValue("contact_id"))
	if contactID == "" {
		writeJSONError(w, http.StatusBadRequest, "contact_id obrigatório")
		return
	}
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant == "" {
		tenant = "demo"
	}
	sources, err := s.deps.Store.ListSourcesByContactGSI1(r.Context(), tenant, contactID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant_id":  tenant,
		"contact_id": contactID,
		"sources":    sources,
	})
}

type ContactSummaryView struct {
	core.Contact
	SourcesCount int               `json:"sources_count"`
	Sources      []core.SourceLink `json:"sources,omitempty"`
	LastSummary  string            `json:"last_summary,omitempty"`
}

type ContactDetailView struct {
	core.Contact
	Sources      []core.SourceLink            `json:"sources"`
	SourceStates map[string]*core.SourceState `json:"source_states,omitempty"`
}

func (s *Server) handleListContacts(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store não configurado")
		return
	}
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant == "" {
		tenant = "demo"
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))

	contacts, err := s.deps.Store.SearchContacts(r.Context(), tenant, query, 100)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	views := make([]ContactSummaryView, 0, len(contacts))
	for _, c := range contacts {
		links, _ := s.deps.Store.ListContactSources(r.Context(), tenant, c.ID)
		lastSummary := ""
		if len(links) > 0 {
			if st, stErr := s.deps.Store.GetSourceState(r.Context(), tenant, links[0].Channel, links[0].SourceKey); stErr == nil && st != nil {
				lastSummary = st.Summary.Text
			}
		}
		views = append(views, ContactSummaryView{
			Contact:      c,
			SourcesCount: len(links),
			Sources:      links,
			LastSummary:  lastSummary,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tenant":   tenant,
		"contacts": views,
		"total":    len(views),
	})
}

func (s *Server) handleGetContact(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store não configurado")
		return
	}
	contactID := strings.TrimSpace(r.PathValue("id"))
	if contactID == "" {
		writeJSONError(w, http.StatusBadRequest, "id do contato é obrigatório")
		return
	}
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant == "" {
		tenant = "demo"
	}

	contact, err := s.deps.Store.GetContact(r.Context(), tenant, contactID)
	if err != nil {
		if errors.Is(err, core.ErrContactNotFound) {
			writeJSONError(w, http.StatusNotFound, "contato não encontrado")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	sources, _ := s.deps.Store.ListContactSources(r.Context(), tenant, contactID)
	states := make(map[string]*core.SourceState)
	for _, src := range sources {
		key := fmt.Sprintf("%s/%s", src.Channel, src.SourceKey)
		if st, stErr := s.deps.Store.GetSourceState(r.Context(), tenant, src.Channel, src.SourceKey); stErr == nil && st != nil {
			states[key] = st
		}
	}

	writeJSON(w, http.StatusOK, ContactDetailView{
		Contact:      *contact,
		Sources:      sources,
		SourceStates: states,
	})
}

func (s *Server) handleDeleteContact(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store não configurado")
		return
	}
	contactID := strings.TrimSpace(r.PathValue("id"))
	if contactID == "" {
		writeJSONError(w, http.StatusBadRequest, "id do contato é obrigatório")
		return
	}
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant == "" {
		tenant = "demo"
	}

	if err := s.deps.Store.DeleteContactAndHistory(r.Context(), tenant, contactID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"deleted":    true,
		"contact_id": contactID,
		"tenant":     tenant,
	})
}

func (s *Server) handleToggleContactListening(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store não configurado")
		return
	}
	contactID := strings.TrimSpace(r.PathValue("id"))
	if contactID == "" {
		writeJSONError(w, http.StatusBadRequest, "id do contato é obrigatório")
		return
	}
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant == "" {
		tenant = "demo"
	}

	var req struct {
		Listening bool `json:"listening"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	contact, err := s.deps.Store.GetContact(r.Context(), tenant, contactID)
	if err != nil {
		if errors.Is(err, core.ErrContactNotFound) {
			writeJSONError(w, http.StatusNotFound, "contato não encontrado")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	contact.Listening = req.Listening
	if err := s.deps.Store.SaveContact(r.Context(), *contact); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, contact)
}

func (s *Server) handleGetListeningConfig(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store não configurado")
		return
	}
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant == "" {
		tenant = "demo"
	}

	data, err := s.deps.Store.GetTenantConfig(r.Context(), tenant, "listening")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"tenant":    tenant,
			"global":    false,
			"whitelist": []string{},
		})
		return
	}

	var cfg struct {
		Global    bool     `json:"global"`
		Whitelist []string `json:"whitelist"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"tenant":    tenant,
			"global":    false,
			"whitelist": []string{},
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tenant":    tenant,
		"global":    cfg.Global,
		"whitelist": cfg.Whitelist,
	})
}

func (s *Server) handlePutListeningConfig(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store não configurado")
		return
	}
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant == "" {
		tenant = "demo"
	}

	var req struct {
		Global    bool     `json:"global"`
		Whitelist []string `json:"whitelist"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "JSON inválido")
		return
	}

	data, err := json.Marshal(req)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if err := s.deps.Store.SaveTenantConfig(r.Context(), tenant, "listening", data); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tenant":    tenant,
		"global":    req.Global,
		"whitelist": req.Whitelist,
	})
}

type knowledgeFileView struct {
	Name    string `json:"name"`
	Key     string `json:"key"`
	Content string `json:"content"`
	IsQA    bool   `json:"is_qa"`
}

func validateKnowledgeFilename(raw string) (string, bool) {
	name := strings.TrimSpace(raw)
	if name == "" || strings.Contains(name, "..") || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") {
		return "", false
	}
	clean := filepath.Clean(filepath.ToSlash(name))
	if clean == "." || strings.HasPrefix(clean, "../") || !strings.HasSuffix(strings.ToLower(clean), ".md") {
		return "", false
	}
	return clean, true
}

func (s *Server) handleListKnowledge(w http.ResponseWriter, r *http.Request) {
	if s.deps.Blobs == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "blob store não configurado")
		return
	}
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant == "" {
		tenant = "demo"
	}
	prefix := store.S3KnowledgeKey(tenant, "")
	keys, err := s.deps.Blobs.List(r.Context(), prefix)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sort.Strings(keys)

	files := make([]knowledgeFileView, 0, len(keys))
	for _, k := range keys {
		if !strings.HasSuffix(strings.ToLower(k), ".md") {
			continue
		}
		raw, err := s.deps.Blobs.Get(r.Context(), k)
		if err != nil {
			continue
		}
		relName := strings.TrimPrefix(k, prefix)
		relName = strings.TrimPrefix(relName, "/")
		if relName == "" {
			relName = filepath.Base(k)
		}
		files = append(files, knowledgeFileView{
			Name:    relName,
			Key:     k,
			Content: string(raw),
			IsQA:    strings.EqualFold(filepath.Base(relName), "QA.md") || strings.EqualFold(filepath.Base(relName), "QA-listed.md"),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant": tenant,
		"files":  files,
	})
}

func (s *Server) handleGetKnowledgeFile(w http.ResponseWriter, r *http.Request) {
	if s.deps.Blobs == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "blob store não configurado")
		return
	}
	file, ok := validateKnowledgeFilename(r.PathValue("file"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "nome de arquivo .md inválido")
		return
	}
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant == "" {
		tenant = "demo"
	}
	key := store.S3KnowledgeKey(tenant, file)
	versionID := strings.TrimSpace(r.URL.Query().Get("version_id"))

	var raw []byte
	var err error
	if versionID != "" {
		vbs, ok := s.deps.Blobs.(core.VersionedBlobStore)
		if !ok {
			writeJSONError(w, http.StatusNotImplemented, "versionamento não suportado pelo blob store")
			return
		}
		raw, err = vbs.GetVersion(r.Context(), key, versionID)
	} else {
		raw, err = s.deps.Blobs.Get(r.Context(), key)
	}
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "arquivo não encontrado")
		return
	}
	writeJSON(w, http.StatusOK, knowledgeFileView{
		Name:    file,
		Key:     key,
		Content: string(raw),
		IsQA:    strings.EqualFold(file, "QA.md"),
	})
}

type putKnowledgeRequest struct {
	TenantID string `json:"tenant_id"`
	Content  string `json:"content"`
}

func (s *Server) handlePutKnowledgeFile(w http.ResponseWriter, r *http.Request) {
	if s.deps.Blobs == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "blob store não configurado")
		return
	}
	file, ok := validateKnowledgeFilename(r.PathValue("file"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "nome de arquivo .md inválido")
		return
	}
	var req putKnowledgeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	tenant := strings.TrimSpace(req.TenantID)
	if tenant == "" {
		tenant = strings.TrimSpace(r.URL.Query().Get("tenant"))
	}
	if tenant == "" {
		tenant = "demo"
	}

	key := store.S3KnowledgeKey(tenant, file)
	if err := s.deps.Blobs.Put(r.Context(), key, []byte(req.Content), "text/markdown; charset=utf-8"); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.deps.Knowledge != nil {
		s.deps.Knowledge.Invalidate(tenant)
	}
	writeJSON(w, http.StatusOK, knowledgeFileView{
		Name:    file,
		Key:     key,
		Content: req.Content,
		IsQA:    strings.EqualFold(file, "QA.md"),
	})
}

func (s *Server) handleListKnowledgeVersions(w http.ResponseWriter, r *http.Request) {
	if s.deps.Blobs == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "blob store não configurado")
		return
	}
	file, ok := validateKnowledgeFilename(r.PathValue("file"))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "nome de arquivo .md inválido")
		return
	}
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant == "" {
		tenant = "demo"
	}
	vbs, ok := s.deps.Blobs.(core.VersionedBlobStore)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"versions": []core.BlobVersion{}})
		return
	}
	key := store.S3KnowledgeKey(tenant, file)
	versions, err := vbs.ListVersions(r.Context(), key)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"file":     file,
		"key":      key,
		"versions": versions,
	})
}

func (s *Server) handlePostReplay(w http.ResponseWriter, r *http.Request) {
	if s.deps.Pipeline == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "pipeline não configurado")
		return
	}
	var req brain.ReplayRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	res, err := s.deps.Pipeline.ReplayIsolated(r.Context(), req)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleListPendingLearning(w http.ResponseWriter, r *http.Request) {
	if s.deps.Pipeline == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "pipeline não configurado")
		return
	}
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant == "" {
		tenant = "demo"
	}
	proposals, err := s.deps.Pipeline.ListPendingLearningProposals(r.Context(), tenant)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant":    tenant,
		"proposals": proposals,
	})
}

func (s *Server) handleApproveLearning(w http.ResponseWriter, r *http.Request) {
	if s.deps.Pipeline == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "pipeline não configurado")
		return
	}
	var req brain.ApproveLearningRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeJSONError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	req.ProposalID = r.PathValue("id")
	if strings.TrimSpace(req.TenantID) == "" {
		req.TenantID = strings.TrimSpace(r.URL.Query().Get("tenant"))
	}
	if strings.TrimSpace(req.TenantID) == "" {
		req.TenantID = "demo"
	}
	prop, err := s.deps.Pipeline.ApproveLearningProposal(r.Context(), req)
	if err != nil {
		if errors.Is(err, brain.ErrProposalNotFound) {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, prop)
}

func (s *Server) handleRejectLearning(w http.ResponseWriter, r *http.Request) {
	if s.deps.Pipeline == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "pipeline não configurado")
		return
	}
	var body struct {
		TenantID string `json:"tenant_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSONError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	tenant := strings.TrimSpace(body.TenantID)
	if tenant == "" {
		tenant = strings.TrimSpace(r.URL.Query().Get("tenant"))
	}
	if tenant == "" {
		tenant = "demo"
	}
	prop, err := s.deps.Pipeline.RejectLearningProposal(r.Context(), tenant, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, brain.ErrProposalNotFound) {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, prop)
}

func (s *Server) handleCorrectLearning(w http.ResponseWriter, r *http.Request) {
	if s.deps.Pipeline == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "pipeline não configurado")
		return
	}
	var req brain.CorrectReplyRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	prop, err := s.deps.Pipeline.ProposeLearningCorrection(r.Context(), req)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, prop)
}

func parseIntQuery(r *http.Request, name string, fallback int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
