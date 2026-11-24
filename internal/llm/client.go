// Package llm wraps the OpenAI-compatible client used to talk to a local
// llama.cpp / LM Studio server.
package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sashabaranov/go-openai"
)

// ErrStalled is returned by a stream that stayed silent for longer than the
// configured stall timeout.
var ErrStalled = errors.New("model stream stalled")

// NewClient builds a client for an OpenAI-compatible endpoint. baseURL must
// already include the /v1 suffix (e.g. http://127.0.0.1:1234/v1).
//
// The client has no total timeout: one generation may legitimately run for
// minutes on a small local model. A stream that receives no bytes for
// stallTimeout is cancelled with ErrStalled instead; SSE keep-alive comments
// count as activity. stallTimeout <= 0 disables the watchdog.
func NewClient(baseURL string, stallTimeout time.Duration) *openai.Client {
	cfg := openai.DefaultConfig("not-needed")
	cfg.BaseURL = baseURL
	cfg.HTTPClient = &http.Client{Transport: newStallTransport(http.DefaultTransport, stallTimeout)}
	// SSE keep-alive comments are not "empty messages".
	cfg.EmptyMessagesLimit = 1 << 20
	return openai.NewClientWithConfig(cfg)
}

type stallTransport struct {
	base  http.RoundTripper
	stall time.Duration
}

func newStallTransport(base http.RoundTripper, stall time.Duration) http.RoundTripper {
	if stall <= 0 {
		return base
	}
	return &stallTransport{base: base, stall: stall}
}

func (t *stallTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = newStallBody(resp.Body, cancel, t.stall)
	return resp, nil
}

// stallBody cancels its request when no bytes arrive for a while. The check
// sits below the SSE parser on purpose: a keep-alive comment never reaches
// the parser, but it still proves the server is alive.
type stallBody struct {
	body    io.ReadCloser
	cancel  context.CancelFunc
	stall   time.Duration
	last    atomic.Int64
	stalled atomic.Bool
	once    sync.Once
	done    chan struct{}
}

func newStallBody(body io.ReadCloser, cancel context.CancelFunc, stall time.Duration) *stallBody {
	b := &stallBody{body: body, cancel: cancel, stall: stall, done: make(chan struct{})}
	b.last.Store(time.Now().UnixNano())
	go b.watch()
	return b
}

func (b *stallBody) watch() {
	interval := b.stall / 4
	if interval <= 0 {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-b.done:
			return
		case <-ticker.C:
			if time.Since(time.Unix(0, b.last.Load())) >= b.stall {
				b.stalled.Store(true)
				b.cancel()
				return
			}
		}
	}
}

func (b *stallBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if n > 0 {
		b.last.Store(time.Now().UnixNano())
	}
	if err != nil && b.stalled.Load() {
		return n, ErrStalled
	}
	return n, err
}

func (b *stallBody) Close() error {
	b.once.Do(func() { close(b.done) })
	b.cancel()
	return b.body.Close()
}
