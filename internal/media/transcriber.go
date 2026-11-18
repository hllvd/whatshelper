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

	"whatshelper/internal/core"
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
// suportando endpoint customizado (TRANSCRIPTION_URL), OpenAI direto (/audio/transcriptions)
// e transcrição nativa via LLM configurado (ex.: Gemini multimodal).
type DynamicAudioTranscriber struct {
	custom          *HTTPTranscriber
	openAI          *HTTPTranscriber
	llm             core.LLM
	defaultProvider string
	defaultModel    string
}

// NewDynamicAudioTranscriber cria um transcritor dinâmico; retorna nil se nenhum endpoint/chave estiver disponível.
func NewDynamicAudioTranscriber(customURL, customKey, customModel, openAIKey, openAIBaseURL, defaultProvider, defaultModel string, client *http.Client) *DynamicAudioTranscriber {
	return NewDynamicAudioTranscriberWithLLM(customURL, customKey, customModel, openAIKey, openAIBaseURL, defaultProvider, defaultModel, nil, client)
}

// NewDynamicAudioTranscriberWithLLM cria um transcritor dinâmico incluindo fallback/integração com LLM configurado.
func NewDynamicAudioTranscriberWithLLM(customURL, customKey, customModel, openAIKey, openAIBaseURL, defaultProvider, defaultModel string, llm core.LLM, client *http.Client) *DynamicAudioTranscriber {
	custom := NewHTTPTranscriber(customURL, customKey, customModel, client)
	var openAI *HTTPTranscriber
	if strings.TrimSpace(openAIKey) != "" {
		base := strings.TrimRight(strings.TrimSpace(openAIBaseURL), "/")
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		openAI = NewHTTPTranscriber(base+"/audio/transcriptions", openAIKey, defaultModel, client)
	}
	if custom == nil && openAI == nil && llm == nil {
		return nil
	}
	return &DynamicAudioTranscriber{
		custom:          custom,
		openAI:          openAI,
		llm:             llm,
		defaultProvider: strings.TrimSpace(defaultProvider),
		defaultModel:    strings.TrimSpace(defaultModel),
	}
}

// WithLLM associa um core.LLM (ex.: DynamicRouter) ao transcritor.
func (d *DynamicAudioTranscriber) WithLLM(llm core.LLM) *DynamicAudioTranscriber {
	d.llm = llm
	return d
}

func (d *DynamicAudioTranscriber) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
	return d.TranscribeWithConfig(ctx, audio, mimeType, d.defaultProvider, d.defaultModel)
}

// TranscribeWithConfig executa a transcrição usando provedor/modelo resolvidos em tempo real.
func (d *DynamicAudioTranscriber) TranscribeWithConfig(ctx context.Context, audio []byte, mimeType, provider, model string) (string, error) {
	if len(audio) == 0 {
		return "", errors.New("transcrição: áudio vazio")
	}
	p := strings.TrimSpace(provider)
	if p == "" {
		p = d.defaultProvider
	}
	m := strings.TrimSpace(model)
	if m == "" {
		m = d.defaultModel
	}

	// Se houver LLM e o provedor/modelo for do LLM (ex.: gemini, openrouter, multimodal) ou não houver transcritor whisper:
	lowerM := strings.ToLower(m)
	isMultimodalLLM := p == "gemini" || strings.Contains(lowerM, "gemini") || strings.Contains(lowerM, "gpt-4o")
	if d.llm != nil && (isMultimodalLLM || (d.custom == nil && d.openAI == nil)) {
		return d.transcribeWithLLM(ctx, audio, mimeType, p, m)
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
	if d.llm != nil {
		return d.transcribeWithLLM(ctx, audio, mimeType, p, m)
	}
	return "", errors.New("transcrição indisponível")
}

func (d *DynamicAudioTranscriber) transcribeWithLLM(ctx context.Context, audio []byte, mimeType, provider, model string) (string, error) {
	if d.llm == nil {
		return "", errors.New("transcrição via LLM indisponível: modelo não configurado")
	}
	normMime := strings.TrimSpace(mimeType)
	if normMime == "" {
		normMime = "audio/ogg"
	}
	prompt := "Transcreva o áudio a seguir com fidelidade total em português do Brasil. Retorne apenas o texto falado no áudio, sem explicações, sem comentários, sem pontuações ou introduções adicionais."
	req := core.LLMRequest{
		Stage:       "audio",
		Provider:    provider,
		Model:       model,
		System:      "Você é um transcritor de áudio em português de alta fidelidade. Retorne única e exclusivamente a transcrição literal do áudio enviado.",
		Prompt:      prompt,
		MediaParts:  []core.MediaPart{{MimeType: normMime, Data: audio}},
		Temperature: 0.1,
		MaxTokens:   1024,
	}
	resp, err := d.llm.Generate(ctx, req)
	if err != nil {
		return "", fmt.Errorf("transcrição via LLM falhou: %w", err)
	}
	return strings.TrimSpace(resp.Content), nil
}
