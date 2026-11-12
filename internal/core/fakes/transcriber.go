package fakes

import (
	"context"
	"errors"
	"sync"
)

// FakeTranscriber implementa core.AudioTranscriber para testes determinísticos sem chamadas de rede.
type FakeTranscriber struct {
	mu      sync.Mutex
	Replies []string
	Err     error
	Calls   int
}

func (f *FakeTranscriber) Transcribe(ctx context.Context, audio []byte, mimeType string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls++
	if f.Err != nil {
		return "", f.Err
	}
	if len(audio) == 0 {
		return "", errors.New("fake transcriber: áudio vazio")
	}
	if len(f.Replies) == 0 {
		return "", errors.New("fake transcriber: nenhuma resposta configurada")
	}
	out := f.Replies[0]
	if len(f.Replies) > 1 {
		f.Replies = f.Replies[1:]
	}
	return out, nil
}
