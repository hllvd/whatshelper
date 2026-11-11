package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"whatshelper/internal/core"
	"whatshelper/internal/store"
)

type simMessageRequest struct {
	TenantID  string `json:"tenant_id"`
	SourceKey string `json:"source_key"`
	Text      string `json:"text"`
}

type chatMessageView struct {
	MessageID string           `json:"message_id"`
	Timestamp time.Time        `json:"timestamp"`
	Direction core.Direction   `json:"direction"`
	Type      core.MessageType `json:"type"`
	Text      string           `json:"text"`
	Sender    string           `json:"sender,omitempty"`
	RawRef    string           `json:"raw_ref,omitempty"`
}

func (s *Server) handlePostSimMessage(w http.ResponseWriter, r *http.Request) {
	if s.deps.Simulator == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "simulador não inicializado")
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

	msg, err := s.deps.Simulator.SubmitInbound(r.Context(), req.TenantID, req.SourceKey, req.Text)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, msg)
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

	metas, err := s.deps.Store.ListRecentMessages(r.Context(), tenant, channel, key, limit)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Retorna em ordem cronológica (mais antiga -> mais recente) para o chat
	views := make([]chatMessageView, 0, len(metas))
	for i := len(metas) - 1; i >= 0; i-- {
		m := metas[i]
		text := ""
		if m.RawRef != "" && s.deps.Blobs != nil {
			if raw, err := s.deps.Blobs.Get(r.Context(), m.RawRef); err == nil {
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
			Type:      m.Type,
			Text:      text,
			Sender:    m.Sender,
			RawRef:    m.RawRef,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"messages": views,
	})
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
