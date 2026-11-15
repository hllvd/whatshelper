package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"whatshelper/internal/core"
)

// OutboundHandler executa envios e sinalização de digitação recebidos do Brain.
type OutboundHandler interface {
	Send(ctx context.Context, msg core.NormalizedMessage) error
	SendTyping(ctx context.Context, sourceKey string, typing bool) error
	Health() core.ChannelHealth
}

// MediaUploadParams descreve os metadados do upload de mídia para /api/connectors/v1/media.
type MediaUploadParams struct {
	TenantID  string
	Channel   string
	SourceKey string
	MediaID   string
	Ext       string
	MimeType  string
	Timestamp time.Time
}

// ClientConfig configura o cliente HTTP/WebSocket executado no processo de connector.
type ClientConfig struct {
	BrainBaseURL   string
	Token          string
	Channel        string
	Account        string
	HTTPClient     *http.Client
	BackoffBase    time.Duration
	BackoffMax     time.Duration
	MaxHTTPRetries int
	SleepFunc      func(context.Context, time.Duration) error
	Logger         *slog.Logger
}

// Client encapsula a comunicação resiliente do connector externo com o Brain (HTTP inbound/media + WS outbound).
type Client struct {
	baseURL    string
	token      string
	channel    string
	account    string
	httpClient *http.Client
	backoffMin time.Duration
	backoffMax time.Duration
	maxRetries int
	sleep      func(context.Context, time.Duration) error
	logger     *slog.Logger
}

// NewClient inicializa um Client com defaults seguros de backoff e timeouts HTTP.
func NewClient(cfg ClientConfig) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BrainBaseURL), "/")
	if base == "" {
		return nil, errors.New("BrainBaseURL é obrigatório")
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	backoffMin := cfg.BackoffBase
	if backoffMin <= 0 {
		backoffMin = 200 * time.Millisecond
	}
	backoffMax := cfg.BackoffMax
	if backoffMax < backoffMin {
		backoffMax = 30 * time.Second
	}
	maxRetries := cfg.MaxHTTPRetries
	if maxRetries <= 0 {
		maxRetries = 5
	}
	sleepFn := cfg.SleepFunc
	if sleepFn == nil {
		sleepFn = defaultSleepWithContext
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	channel := strings.TrimSpace(cfg.Channel)
	if channel == "" {
		channel = "whatsapp"
	}
	account := strings.TrimSpace(cfg.Account)
	if account == "" {
		account = "main"
	}
	return &Client{
		baseURL:    base,
		token:      strings.TrimSpace(cfg.Token),
		channel:    channel,
		account:    account,
		httpClient: httpClient,
		backoffMin: backoffMin,
		backoffMax: backoffMax,
		maxRetries: maxRetries,
		sleep:      sleepFn,
		logger:     logger,
	}, nil
}

// SendInboundWithRetry envia uma mensagem inbound ao Brain com retries exponenciais para falhas transitórias.
func (c *Client) SendInboundWithRetry(ctx context.Context, req InboundRequest) (InboundResponse, error) {
	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return InboundResponse{}, err
	}

	var lastErr error
	for attempt := 0; attempt < c.maxRetries; attempt++ {
		if ctx.Err() != nil {
			return InboundResponse{}, ctx.Err()
		}
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/connectors/v1/inbound", bytes.NewReader(bodyBytes))
		if err != nil {
			return InboundResponse{}, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		c.applyAuthHeaders(httpReq.Header)

		resp, err := c.httpClient.Do(httpReq)
		if err == nil {
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				var parsed InboundResponse
				if err := json.Unmarshal(respBody, &parsed); err != nil {
					return InboundResponse{}, err
				}
				return parsed, nil
			}
			lastErr = fmt.Errorf("HTTP %d ao enviar inbound: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
			if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
				return InboundResponse{}, lastErr
			}
		} else {
			lastErr = err
		}

		if attempt+1 < c.maxRetries {
			delay := BackoffDelay(attempt, c.backoffMin, c.backoffMax)
			if err := c.sleep(ctx, delay); err != nil {
				return InboundResponse{}, err
			}
		}
	}
	return InboundResponse{}, lastErr
}

// SendTypingEvent envia o estado de digitação de um contato ao Brain (best-effort).
func (c *Client) SendTypingEvent(ctx context.Context, req TypingRequest) error {
	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/connectors/v1/typing", bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.applyAuthHeaders(httpReq.Header)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	_, _ = io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d ao enviar typing", resp.StatusCode)
	}
	return nil
}

// UploadMediaWithRetry envia mídia ao BlobStore via Brain com retries em falhas transitórias.
func (c *Client) UploadMediaWithRetry(ctx context.Context, params MediaUploadParams, data []byte) (string, error) {
	var lastErr error
	for attempt := 0; attempt < c.maxRetries; attempt++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("tenant_id", params.TenantID)
		_ = mw.WriteField("channel", params.Channel)
		_ = mw.WriteField("source_key", params.SourceKey)
		_ = mw.WriteField("media_id", params.MediaID)
		_ = mw.WriteField("ext", params.Ext)
		_ = mw.WriteField("mime_type", params.MimeType)
		if !params.Timestamp.IsZero() {
			_ = mw.WriteField("timestamp", params.Timestamp.UTC().Format(time.RFC3339Nano))
		}
		part, err := mw.CreateFormFile("file", params.MediaID+"."+strings.TrimPrefix(params.Ext, "."))
		if err != nil {
			return "", err
		}
		if _, err := part.Write(data); err != nil {
			return "", err
		}
		_ = mw.Close()

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/connectors/v1/media", &buf)
		if err != nil {
			return "", err
		}
		httpReq.Header.Set("Content-Type", mw.FormDataContentType())
		c.applyAuthHeaders(httpReq.Header)

		resp, err := c.httpClient.Do(httpReq)
		if err == nil {
			respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				var parsed MediaUploadResponse
				if err := json.Unmarshal(respBody, &parsed); err != nil {
					return "", err
				}
				return parsed.MediaRef, nil
			}
			lastErr = fmt.Errorf("HTTP %d no upload de mídia: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
			if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
				return "", lastErr
			}
		} else {
			lastErr = err
		}

		if attempt+1 < c.maxRetries {
			delay := BackoffDelay(attempt, c.backoffMin, c.backoffMax)
			if err := c.sleep(ctx, delay); err != nil {
				return "", err
			}
		}
	}
	return "", lastErr
}

// RunWS mantém a sessão WebSocket ativa com o Brain, executando reconexão automática com backoff exponencial.
func (c *Client) RunWS(ctx context.Context, handler OutboundHandler) error {
	attempt := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := c.runSingleWSSession(ctx, handler, &attempt)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			c.logger.Warn("sessão WS do connector desconectada, agendando reconexão",
				slog.String("channel", c.channel),
				slog.Int("attempt", attempt),
				slog.Any("error", err),
			)
		}
		delay := BackoffDelay(attempt, c.backoffMin, c.backoffMax)
		attempt++
		if err := c.sleep(ctx, delay); err != nil {
			return err
		}
	}
}

func (c *Client) runSingleWSSession(ctx context.Context, handler OutboundHandler, attempt *int) error {
	wsURL, err := c.wsURL()
	if err != nil {
		return err
	}
	header := http.Header{}
	c.applyAuthHeaders(header)

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, resp, err := dialer.DialContext(ctx, wsURL, header)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	*attempt = 0

	var writeMu sync.Mutex
	writeJSON := func(frame WSFrame) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteJSON(frame)
	}

	h := handler.Health()
	_ = writeJSON(WSFrame{
		Type:      FrameHealth,
		Connected: h.Healthy,
		Detail:    h.Detail,
	})

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var frame WSFrame
		if err := json.Unmarshal(data, &frame); err != nil {
			continue
		}
		switch frame.Type {
		case FrameSend:
			if frame.Message == nil {
				continue
			}
			sendErr := handler.Send(ctx, *frame.Message)
			ack := WSFrame{
				Type:      FrameAck,
				RequestID: frame.RequestID,
				OK:        sendErr == nil,
			}
			if sendErr != nil {
				ack.Error = sendErr.Error()
			}
			if err := writeJSON(ack); err != nil {
				return err
			}
		case FrameTyping:
			_ = handler.SendTyping(ctx, frame.SourceKey, frame.Typing)
		case FramePing:
			if err := writeJSON(WSFrame{Type: FramePong}); err != nil {
				return err
			}
		}
	}
}

func (c *Client) wsURL() (string, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/connectors/v1/ws"
	q := u.Query()
	q.Set("channel", c.channel)
	q.Set("account", c.account)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (c *Client) applyAuthHeaders(h http.Header) {
	h.Set(HeaderProtocolVersion, ProtocolVersion)
	if c.token != "" {
		h.Set("Authorization", "Bearer "+c.token)
	}
}

func defaultSleepWithContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
