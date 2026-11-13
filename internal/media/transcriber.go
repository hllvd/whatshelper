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
	return t.TranscribeWithModel(ctx, audio, mimeType, t.model)
}

// TranscribeWithModel executa a transcrição usando um modelo específico (override em tempo real).
func (t *HTTPTranscriber) TranscribeWithModel(ctx context.Context, audio []byte, mimeType, model string) (string, error) {
	if len(audio) == 0 {
		return "", errors.New("transcrição: áudio vazio")
	}
	targetModel := strings.TrimSpace(model)
	if targetModel == "" {
		targetModel = t.model
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", targetModel)
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

// DynamicAudioTranscriber permite selecionar provedor e modelo de áudio em tempo real,
// suportando tanto endpoint customizado (TRANSCRIPTION_URL) quanto OpenAI direto (/audio/transcriptions).
type DynamicAudioTranscriber struct {
	custom          *HTTPTranscriber
	openAI          *HTTPTranscriber
	defaultProvider string
	defaultModel    string
}

// NewDynamicAudioTranscriber cria um transcritor dinâmico; retorna nil se nenhum endpoint/chave estiver disponível.
func NewDynamicAudioTranscriber(customURL, customKey, customModel, openAIKey, openAIBaseURL, defaultProvider, defaultModel string, client *http.Client) *DynamicAudioTranscriber {
	custom := NewHTTPTranscriber(customURL, customKey, customModel, client)
	var openAI *HTTPTranscriber
	if strings.TrimSpace(openAIKey) != "" {
		base := strings.TrimRight(strings.TrimSpace(openAIBaseURL), "/")
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		openAI = NewHTTPTranscriber(base+"/audio/transcriptions", openAIKey, defaultModel, client)
	}
	if custom == nil && openAI == nil {
		return nil
	}
	return &DynamicAudioTranscriber{
		custom:          custom,
		openAI:          openAI,
		defaultProvider: strings.TrimSpace(defaultProvider),
		defaultModel:    strings.TrimSpace(defaultModel),
	}
}

func (d *DynamicAudioTranscriber) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
	return d.TranscribeWithConfig(ctx, audio, mimeType, d.defaultProvider, d.defaultModel)
}

// TranscribeWithConfig executa a transcrição usando provedor/modelo resolvidos em tempo real.
func (d *DynamicAudioTranscriber) TranscribeWithConfig(ctx context.Context, audio []byte, mimeType, provider, model string) (string, error) {
	p := strings.TrimSpace(provider)
	if p == "" {
		p = d.defaultProvider
	}
	m := strings.TrimSpace(model)
	if m == "" {
		m = d.defaultModel
	}
	if p == "openai" && d.openAI != nil {
		return d.openAI.TranscribeWithModel(ctx, audio, mimeType, m)
	}
	if d.custom != nil {
		return d.custom.TranscribeWithModel(ctx, audio, mimeType, m)
	}
	if d.openAI != nil {
		return d.openAI.TranscribeWithModel(ctx, audio, mimeType, m)
	}
	return "", errors.New("transcrição indisponível")
}
