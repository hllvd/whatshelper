package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/core"
	"whatshelper/internal/pack"
)

func (s *Server) handleGetSourceJourney(w http.ResponseWriter, r *http.Request) {
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

	ctx := r.Context()
	js, err := s.deps.Store.GetJourneyState(ctx, tenant, channel, key)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	transitions, err := s.deps.Store.ListJourneyTransitions(ctx, tenant, channel, key, 50)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	outbox, err := s.deps.Store.ListOutboxItems(ctx, tenant, channel, key, 50)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var jDef *pack.JourneyDef
	validation := map[string]any{"valid": false, "message": "pack não carregado"}
	if s.deps.Pipeline != nil {
		if pk, pkErr := s.deps.Pipeline.LoadTenantPack(ctx, tenant); pkErr != nil {
			validation = map[string]any{"valid": false, "message": pkErr.Error()}
		} else if pk.Journey != nil {
			jDef = pk.Journey
			validation = map[string]any{
				"valid":         true,
				"pack":          pk.Name,
				"version":       pk.Journey.Version,
				"initial_state": pk.Journey.InitialState,
				"states_count":  len(pk.Journey.States),
				"global_rules":  len(pk.Journey.GlobalRules),
			}
		} else {
			validation = map[string]any{"valid": false, "message": "pack não possui journey.yaml"}
		}
	}

	curState := ""
	if js != nil {
		curState = js.State
	}
	mermaid := brain.RenderJourneyMermaid(jDef, curState)

	writeJSON(w, http.StatusOK, map[string]any{
		"tenant":      tenant,
		"channel":     channel,
		"source_key":  key,
		"state":       js,
		"transitions": transitions,
		"outbox":      outbox,
		"journey":     jDef,
		"mermaid":     mermaid,
		"validation":  validation,
	})
}

type whatIfRequest struct {
	TenantID        string            `json:"tenant_id"`
	CurrentState    string            `json:"current_state"`
	StagnationCount int               `json:"stagnation_count"`
	EventType       string            `json:"event_type"`
	Intent          string            `json:"intent"`
	Facts           map[string]string `json:"facts"`
	Signals         map[string]bool   `json:"signals"`
	Dimensions      map[string]string `json:"dimensions"`
}

func (s *Server) handleJourneyWhatIf(w http.ResponseWriter, r *http.Request) {
	if s.deps.Pipeline == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "pipeline não inicializado")
		return
	}
	var req whatIfRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	tenant := strings.TrimSpace(req.TenantID)
	if tenant == "" {
		tenant = "demo"
	}
	pk, err := s.deps.Pipeline.LoadTenantPack(r.Context(), tenant)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if pk.Journey == nil {
		writeJSONError(w, http.StatusNotFound, "pack não possui journey.yaml")
		return
	}

	res := brain.SimulateJourneyWhatIf(
		pk.Journey,
		req.CurrentState,
		req.StagnationCount,
		core.SystemEventType(strings.TrimSpace(req.EventType)),
		strings.TrimSpace(req.Intent),
		req.Facts,
		req.Signals,
		req.Dimensions,
		time.Now().UTC(),
	)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleListOutbox(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store não configurado")
		return
	}
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant == "" {
		tenant = "demo"
	}
	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))

	sources, err := s.deps.Store.ListSources(r.Context(), tenant)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var items []core.OutboxItem
	for _, src := range sources {
		list, err := s.deps.Store.ListOutboxItems(r.Context(), tenant, src.Channel, src.SourceKey, 50)
		if err != nil {
			continue
		}
		for _, it := range list {
			if statusFilter != "" && string(it.Status) != statusFilter {
				continue
			}
			items = append(items, it)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant": tenant,
		"items":  items,
	})
}

func (s *Server) handleReprocessOutbox(w http.ResponseWriter, r *http.Request) {
	if s.deps.Pipeline == nil || s.deps.Pipeline.OutboxWorker() == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "outbox worker não configurado")
		return
	}
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant == "" {
		tenant = "demo"
	}
	channel := r.PathValue("channel")
	key := r.PathValue("key")
	id := r.PathValue("id")
	if channel == "" || key == "" || id == "" {
		writeJSONError(w, http.StatusBadRequest, "channel, key e id são obrigatórios")
		return
	}
	if err := s.deps.Pipeline.OutboxWorker().ReprocessItem(r.Context(), tenant, channel, key, id); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "reprocessed", "id": id})
}

func (s *Server) handleGetFunnelMetrics(w http.ResponseWriter, r *http.Request) {
	if s.deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "store não configurado")
		return
	}
	tenant := strings.TrimSpace(r.URL.Query().Get("tenant"))
	if tenant == "" {
		tenant = "demo"
	}
	ctx := r.Context()
	sources, err := s.deps.Store.ListSources(ctx, tenant)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var journeys []core.JourneyState
	var transitions []core.JourneyTransitionRecord
	for _, src := range sources {
		if js, err := s.deps.Store.GetJourneyState(ctx, tenant, src.Channel, src.SourceKey); err == nil && js != nil {
			journeys = append(journeys, *js)
		}
		if trs, err := s.deps.Store.ListJourneyTransitions(ctx, tenant, src.Channel, src.SourceKey, 100); err == nil {
			transitions = append(transitions, trs...)
		}
	}

	var jDef *pack.JourneyDef
	if s.deps.Pipeline != nil {
		if pk, err := s.deps.Pipeline.LoadTenantPack(ctx, tenant); err == nil && pk != nil {
			jDef = pk.Journey
		}
	}

	metrics := brain.ComputeFunnelMetrics(journeys, transitions, jDef, time.Now().UTC())
	writeJSON(w, http.StatusOK, metrics)
}

type simOriginRequest struct {
	TenantID  string `json:"tenant_id"`
	Channel   string `json:"channel"`
	SourceKey string `json:"source_key"`
	Mode      string `json:"mode"` // "human_phone" | "bot_echo"
	Text      string `json:"text"`
}

func (s *Server) handlePostSimOrigin(w http.ResponseWriter, r *http.Request) {
	if s.deps.Pipeline == nil || s.deps.Store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "pipeline ou store não configurado")
		return
	}
	var req simOriginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "JSON inválido")
		return
	}
	tenant := strings.TrimSpace(req.TenantID)
	if tenant == "" {
		tenant = "demo"
	}
	channel := strings.TrimSpace(req.Channel)
	if channel == "" {
		channel = "simulator"
	}
	key := strings.TrimSpace(req.SourceKey)
	if key == "" {
		writeJSONError(w, http.StatusBadRequest, "source_key é obrigatório")
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		text = "(mensagem simulada)"
	}

	now := time.Now().UTC()
	msgID := fmt.Sprintf("sim-%d", now.UnixNano())
	ctx := r.Context()

	switch req.Mode {
	case "bot_echo":
		if err := s.deps.Store.PreRegisterOutbound(ctx, core.OutboundMessageRecord{
			TenantID:  tenant,
			Channel:   channel,
			SourceKey: key,
			MessageID: msgID,
			Status:    core.OutboundStatusSent,
			CreatedAt: now,
			UpdatedAt: now,
		}); err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	case "human_phone":
		// ID não pré-registrado + from_me=true -> OriginHumanPhone
	default:
		writeJSONError(w, http.StatusBadRequest, "mode deve ser 'human_phone' ou 'bot_echo'")
		return
	}

	dup, err := s.deps.Pipeline.IngestWithStatus(ctx, core.NormalizedMessage{
		TenantID:  tenant,
		Channel:   channel,
		SourceKey: key,
		MessageID: msgID,
		Timestamp: now,
		Direction: core.DirectionInbound,
		Type:      core.MessageTypeText,
		Text:      text,
		FromMe:    true,
	})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"message_id": msgID,
		"mode":       req.Mode,
		"duplicate":  dup,
	})
}

func (s *Server) handleListUnknownOriginMessages(w http.ResponseWriter, r *http.Request) {
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

	type unknownItem struct {
		Channel   string             `json:"channel"`
		SourceKey string             `json:"source_key"`
		MessageID string             `json:"message_id"`
		Timestamp time.Time          `json:"timestamp"`
		Origin    core.MessageOrigin `json:"origin"`
		Text      string             `json:"text"`
	}
	var out []unknownItem
	for _, src := range sources {
		msgs, err := s.loadChatMessages(r.Context(), tenant, src.Channel, src.SourceKey, 50)
		if err != nil {
			continue
		}
		for _, m := range msgs {
			if m.Origin == core.OriginUnknown {
				out = append(out, unknownItem{
					Channel:   src.Channel,
					SourceKey: src.SourceKey,
					MessageID: m.MessageID,
					Timestamp: m.Timestamp,
					Origin:    m.Origin,
					Text:      m.Text,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Timestamp.After(out[j].Timestamp)
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant":   tenant,
		"messages": out,
	})
}
