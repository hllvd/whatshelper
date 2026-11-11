package brain

import (
	"fmt"
	"sync"
	"time"

	"whatshelper/internal/core"
)

type tokenBucket struct {
	tokens     float64
	lastRefill time.Time
}

// TokenBucketLimiter aplica rate limiting em memória (global por tenant + por conversa) usando Clock injetável.
type TokenBucketLimiter struct {
	clock   core.Clock
	mu      sync.Mutex
	buckets map[string]*tokenBucket
}

// NewTokenBucketLimiter cria um limitador determinístico guiado pelo Clock informado.
func NewTokenBucketLimiter(clock core.Clock) *TokenBucketLimiter {
	if clock == nil {
		clock = core.SystemClock{}
	}
	return &TokenBucketLimiter{
		clock:   clock,
		buckets: make(map[string]*tokenBucket),
	}
}

// Allow verifica e consome 1 ficha dos buckets do tenant e da conversa.
// Se qualquer limite for excedido, nenhum bucket tem ficha descontada.
func (l *TokenBucketLimiter) Allow(tenant, channel, sourceKey string, policy core.RateLimitPolicy) (bool, string) {
	now := l.clock.Now().UTC()

	l.mu.Lock()
	defer l.mu.Unlock()

	if policy.PerSourcePerMinute > 0 {
		srcKey := fmt.Sprintf("S:%s:%s:%s", tenant, channel, sourceKey)
		sb := l.getOrInitBucket(srcKey, float64(policy.PerSourcePerMinute), now)
		refillBucket(sb, float64(policy.PerSourcePerMinute), now)
		if sb.tokens < 1.0 {
			return false, fmt.Sprintf("limite por conversa (%d/min) atingido", policy.PerSourcePerMinute)
		}
	}

	if policy.GlobalPerMinute > 0 {
		globalKey := fmt.Sprintf("T:%s", tenant)
		gb := l.getOrInitBucket(globalKey, float64(policy.GlobalPerMinute), now)
		refillBucket(gb, float64(policy.GlobalPerMinute), now)
		if gb.tokens < 1.0 {
			return false, fmt.Sprintf("limite global do tenant (%d/min) atingido", policy.GlobalPerMinute)
		}
	}

	// Só consome após confirmar que ambos permitem
	if policy.PerSourcePerMinute > 0 {
		srcKey := fmt.Sprintf("S:%s:%s:%s", tenant, channel, sourceKey)
		l.buckets[srcKey].tokens -= 1.0
	}
	if policy.GlobalPerMinute > 0 {
		globalKey := fmt.Sprintf("T:%s", tenant)
		l.buckets[globalKey].tokens -= 1.0
	}
	return true, ""
}

func (l *TokenBucketLimiter) getOrInitBucket(key string, capacity float64, now time.Time) *tokenBucket {
	b, ok := l.buckets[key]
	if !ok {
		b = &tokenBucket{
			tokens:     capacity,
			lastRefill: now,
		}
		l.buckets[key] = b
	}
	return b
}

func refillBucket(b *tokenBucket, capacity float64, now time.Time) {
	elapsed := now.Sub(b.lastRefill)
	if elapsed <= 0 {
		return
	}
	ratePerSecond := capacity / 60.0
	b.tokens += elapsed.Seconds() * ratePerSecond
	if b.tokens > capacity {
		b.tokens = capacity
	}
	b.lastRefill = now
}
