package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"whatshelper/internal/brain"
	"whatshelper/internal/channels/admin"
	"whatshelper/internal/channels/bridge"
	"whatshelper/internal/store"
)

const maxConnectorMediaBytes = 25 << 20 // 25 MiB limite por upload local de mídia

func (s *Server) handleConnectorInbound(w http.ResponseWriter, r *http.Request) {
	if s.deps.Pipeline == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "pipeline não inicializado")
		return
	}
	if err := bridge.ValidateProtocolHeader(r.Header.Get(bridge.HeaderProtocolVersion)); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	var req bridge.InboundRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "JSON inválido no inbound do connector")
		return
	}

	msg, err := req.ToNormalizedMessage()
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.RouteToAdmin {
		msg.Channel = brain.AdminChannelID
		msg.SourceKey = admin.DefaultAdminSourceKey
	}

	duplicate, err := s.deps.Pipeline.IngestWithStatus(r.Context(), msg)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	statusText := "accepted"
	statusCode := http.StatusAccepted
	if duplicate {
		statusText = "duplicate"
		statusCode = http.StatusOK
	}

	writeJSON(w, statusCode, bridge.InboundResponse{
		Status:    statusText,
		MessageID: msg.MessageID,
	})
}

func (s *Server) handleConnectorMedia(w http.ResponseWriter, r *http.Request) {
	if s.deps.Blobs == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "blob store não inicializado")
		return
	}
	if err := bridge.ValidateProtocolHeader(r.Header.Get(bridge.HeaderProtocolVersion)); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxConnectorMediaBytes+1<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeJSONError(w, http.StatusBadRequest, "multipart/form-data inválido")
		return
	}

	tenantID := strings.TrimSpace(r.FormValue("tenant_id"))
	channel := strings.TrimSpace(r.FormValue("channel"))
	sourceKey := strings.TrimSpace(r.FormValue("source_key"))
	mediaID := strings.TrimSpace(r.FormValue("media_id"))
	ext := strings.TrimPrefix(strings.TrimSpace(r.FormValue("ext")), ".")
	mimeType := strings.TrimSpace(r.FormValue("mime_type"))
	if tenantID == "" || channel == "" || sourceKey == "" || mediaID == "" {
		writeJSONError(w, http.StatusBadRequest, "tenant_id, channel, source_key e media_id são obrigatórios")
		return
	}
	if ext == "" {
		ext = "bin"
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	ts := time.Now().UTC()
	if rawTS := strings.TrimSpace(r.FormValue("timestamp")); rawTS != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, rawTS); err == nil {
			ts = parsed.UTC()
		}
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "campo 'file' ausente")
		return
	}
	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, maxConnectorMediaBytes))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "falha ao ler arquivo de mídia")
		return
	}

	key := store.S3MediaKey(tenantID, channel, sourceKey, ts, mediaID, ext)
	if err := s.deps.Blobs.Put(r.Context(), key, data, mimeType); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "falha ao armazenar mídia")
		return
	}

	writeJSON(w, http.StatusCreated, bridge.MediaUploadResponse{MediaRef: key})
}

func (s *Server) handleConnectorWS(w http.ResponseWriter, r *http.Request) {
	channelID := strings.TrimSpace(r.URL.Query().Get("channel"))
	if channelID == "" {
		channelID = "whatsapp"
	}
	rc, ok := s.deps.RemoteChannels[channelID]
	if !ok || rc == nil {
		writeJSONError(w, http.StatusNotFound, "canal remoto não registrado")
		return
	}
	rc.HandleWebSocket(w, r)
}
