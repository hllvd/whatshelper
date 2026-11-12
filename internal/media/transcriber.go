package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// HTTPTranscriber implementa core.AudioTranscriber usando endpoint HTTP multipart compatível com Whisper/OpenAI.
type HTTPTranscriber struct {
	endpoint string
	apiKey   string
	model    string
	client   *http.Client
}

// NewHTTPTranscriber cria um HTTPTranscriber ou retorna nil se endpoint estiver vazio (transcrição desabilitada).
func NewHTTPTranscriber(endpoint, apiKey, model string, client *http.Client) *HTTPTranscriber {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return nil
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	m := strings.TrimSpace(model)
	if m == "" {
		m = "whisper-1"
	}
	return &HTTPTranscriber{
		endpoint: trimmed,
		apiKey:   strings.TrimSpace(apiKey),
		model:    m,
		client:   client,
	}
}

func (t *HTTPTranscriber) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
	if len(audio) == 0 {
		return "", errors.New("transcrição: áudio vazio")
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", t.model)
	ext := extFromMime(mimeType)
	part, err := mw.CreateFormFile("file", "audio."+ext)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(audio); err != nil {
		return "", err
	}
	_ = mw.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if t.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+t.apiKey)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("serviço de transcrição retornou HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("resposta inválida da transcrição: %w", err)
	}
	return strings.TrimSpace(parsed.Text), nil
}

func extFromMime(mimeType string) string {
	m := strings.ToLower(strings.TrimSpace(mimeType))
	switch {
	case strings.Contains(m, "ogg"):
		return "ogg"
	case strings.Contains(m, "mp3") || strings.Contains(m, "mpeg"):
		return "mp3"
	case strings.Contains(m, "mp4") || strings.Contains(m, "m4a"):
		return "m4a"
	case strings.Contains(m, "wav"):
		return "wav"
	default:
		return "ogg"
	}
}
