package humanizer

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"whatshelper/internal/channels/bridge"
	"whatshelper/internal/core"
)

// Sleeper abstrai espera contextual para permitir testes determinísticos sem pausas reais.
type Sleeper func(ctx context.Context, d time.Duration) error

// Config define tempos e limites anti-rajada do envio humanizado (CH-05).
type Config struct {
	MinTypingDelay       time.Duration
	MaxTypingDelay       time.Duration
	MsPerChar            time.Duration
	MinIntervalPerSource time.Duration
	MinGlobalInterval    time.Duration
	Clock                core.Clock
	Sleep                Sleeper
}

func (c Config) withDefaults() Config {
	out := c
	if out.MinTypingDelay <= 0 {
		out.MinTypingDelay = 600 * time.Millisecond
	}
	if out.MaxTypingDelay < out.MinTypingDelay {
		out.MaxTypingDelay = 4 * time.Second
	}
	if out.MsPerChar <= 0 {
		out.MsPerChar = 35 * time.Millisecond
	}
	if out.MinIntervalPerSource < 0 {
		out.MinIntervalPerSource = 0
	}
	if out.MinGlobalInterval < 0 {
		out.MinGlobalInterval = 0
	}
	if out.Clock == nil {
		out.Clock = core.SystemClock{}
	}
	if out.Sleep == nil {
		out.Sleep = defaultSleep
	}
	return out
}

// ComputeTypingDelay calcula o tempo de digitação proporcional ao tamanho da mensagem, limitado entre [Min, Max].
func ComputeTypingDelay(text string, cfg Config) time.Duration {
	c := cfg.withDefaults()
	runes := utf8.RuneCountInString(strings.TrimSpace(text))
	if runes == 0 {
		return c.MinTypingDelay
	}
	d := time.Duration(runes) * c.MsPerChar
	if d < c.MinTypingDelay {
		return c.MinTypingDelay
	}
	if d > c.MaxTypingDelay {
		return c.MaxTypingDelay
	}
	return d
}

// HumanizedHandler envolve um OutboundHandler aplicando fila sequencial por source_key,
// indicador de digitação proporcional ao texto e limitador anti-rajada.
type HumanizedHandler struct {
	target bridge.OutboundHandler
	cfg    Config

	mu             sync.Mutex
	sourceQueues   map[string]*sync.Mutex
	lastSentSource map[string]time.Time
	lastSentGlobal time.Time
}

// New cria um HumanizedHandler envolvendo target.
func New(target bridge.OutboundHandler, cfg Config) *HumanizedHandler {
	return &HumanizedHandler{
		target:         target,
		cfg:            cfg.withDefaults(),
		sourceQueues:   make(map[string]*sync.Mutex),
		lastSentSource: make(map[string]time.Time),
	}
}

func (h *HumanizedHandler) Health() core.ChannelHealth {
	return h.target.Health()
}

func (h *HumanizedHandler) SendTyping(ctx context.Context, sourceKey string, typing bool) error {
	return h.target.SendTyping(ctx, sourceKey, typing)
}

// Send serializa o envio por source_key, aplica rate-limit anti-rajada e executa typing -> pausa -> send.
func (h *HumanizedHandler) Send(ctx context.Context, msg core.NormalizedMessage) error {
	q := h.queueForSource(msg.SourceKey)
	q.Lock()
	defer q.Unlock()

	if err := h.waitAntiBurst(ctx, msg.SourceKey); err != nil {
		return err
	}

	_ = h.target.SendTyping(ctx, msg.SourceKey, true)
	delay := ComputeTypingDelay(msg.Text, h.cfg)
	if err := h.cfg.Sleep(ctx, delay); err != nil {
		_ = h.target.SendTyping(ctx, msg.SourceKey, false)
		return err
	}
	_ = h.target.SendTyping(ctx, msg.SourceKey, false)

	if err := h.target.Send(ctx, msg); err != nil {
		return err
	}

	h.recordSent(msg.SourceKey)
	return nil
}

func (h *HumanizedHandler) queueForSource(sourceKey string) *sync.Mutex {
	h.mu.Lock()
	defer h.mu.Unlock()
	q, ok := h.sourceQueues[sourceKey]
	if !ok {
		q = &sync.Mutex{}
		h.sourceQueues[sourceKey] = q
	}
	return q
}

func (h *HumanizedHandler) waitAntiBurst(ctx context.Context, sourceKey string) error {
	for {
		wait := h.requiredBurstWait(sourceKey)
		if wait <= 0 {
			return nil
		}
		if err := h.cfg.Sleep(ctx, wait); err != nil {
			return err
		}
		// Se o Sleeper avançar o FakeClock ou o relógio real passar, o loop revalida até zerar wait.
		// Para evitar loop infinito se um teste injetar clock estático + sleeper no-op, limita a verificação quando Clock não avançou.
		if h.requiredBurstWait(sourceKey) >= wait {
			return nil
		}
	}
}

func (h *HumanizedHandler) requiredBurstWait(sourceKey string) time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.cfg.Clock.Now()
	var wait time.Duration

	if h.cfg.MinGlobalInterval > 0 && !h.lastSentGlobal.IsZero() {
		if diff := h.cfg.MinGlobalInterval - now.Sub(h.lastSentGlobal); diff > wait {
			wait = diff
		}
	}
	if h.cfg.MinIntervalPerSource > 0 {
		if prev, ok := h.lastSentSource[sourceKey]; ok {
			if diff := h.cfg.MinIntervalPerSource - now.Sub(prev); diff > wait {
				wait = diff
			}
		}
	}
	return wait
}

func (h *HumanizedHandler) recordSent(sourceKey string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.cfg.Clock.Now()
	h.lastSentGlobal = now
	h.lastSentSource[sourceKey] = now
}

func defaultSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
