package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sashabaranov/go-openai"
)

const chunkPrefix = "data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\""

func chunk(text string) string {
	return chunkPrefix + text + "\"}}]}\n\n"
}

func streamServer(t *testing.T, serve func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		serve(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestClient(t *testing.T, srv *httptest.Server, stall time.Duration) *openai.Client {
	t.Helper()
	cfg := openai.DefaultConfig("not-needed")
	cfg.BaseURL = srv.URL + "/v1"
	cfg.HTTPClient = &http.Client{Transport: newStallTransport(http.DefaultTransport, stall)}
	return openai.NewClientWithConfig(cfg)
}

func collect(t *testing.T, client *openai.Client) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	stream, err := client.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model:    "m",
		Messages: []openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleUser, Content: "hi"}},
	})
	if err != nil {
		return "", err
	}
	defer stream.Close()
	var b strings.Builder
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return b.String(), nil
		}
		if err != nil {
			return b.String(), err
		}
		if len(resp.Choices) > 0 {
			b.WriteString(resp.Choices[0].Delta.Content)
		}
	}
}

// A stream that keeps producing must not be cut by its total duration.
func TestLongStreamIsNotCut(t *testing.T) {
	srv := streamServer(t, func(w http.ResponseWriter, _ *http.Request) {
		for i := 0; i < 40; i++ {
			_, _ = w.Write([]byte(chunk("x")))
			w.(http.Flusher).Flush()
			time.Sleep(20 * time.Millisecond)
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})
	got, err := collect(t, newTestClient(t, srv, 200*time.Millisecond))
	if err != nil {
		t.Fatalf("stream ended with %v after %d chunks", err, len(got))
	}
	if got != strings.Repeat("x", 40) {
		t.Fatalf("received %d chunks, want 40", len(got))
	}
}

func TestSilentStreamStalls(t *testing.T) {
	srv := streamServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(chunk("x")))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	start := time.Now()
	_, err := collect(t, newTestClient(t, srv, 150*time.Millisecond))
	if !errors.Is(err, ErrStalled) {
		t.Fatalf("err = %v, want ErrStalled", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("stall detected after %v", elapsed)
	}
}

// Keep-alive comments are not data, but they still count as activity.
func TestKeepAlivePreventsStall(t *testing.T) {
	srv := streamServer(t, func(w http.ResponseWriter, _ *http.Request) {
		for i := 0; i < 20; i++ {
			_, _ = w.Write([]byte(":\n\n"))
			w.(http.Flusher).Flush()
			time.Sleep(20 * time.Millisecond)
		}
		_, _ = w.Write([]byte(chunk("x") + "data: [DONE]\n\n"))
	})
	got, err := collect(t, newTestClient(t, srv, 150*time.Millisecond))
	if err != nil {
		t.Fatalf("stream ended with %v", err)
	}
	if got != "x" {
		t.Fatalf("got %q, want \"x\"", got)
	}
}

func TestWatchdogDisabled(t *testing.T) {
	if _, ok := newStallTransport(http.DefaultTransport, 0).(*stallTransport); ok {
		t.Fatal("zero stall timeout must not install a watchdog")
	}
}
