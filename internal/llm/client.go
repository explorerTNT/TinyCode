// Package llm wraps the OpenAI-compatible client used to talk to a local
// llama.cpp / LM Studio server.
package llm

import (
	"net/http"
	"time"

	"github.com/sashabaranov/go-openai"
)

// NewClient builds a client for an OpenAI-compatible endpoint. baseURL must
// already include the /v1 suffix (e.g. http://127.0.0.1:1234/v1).
func NewClient(baseURL string) *openai.Client {
	cfg := openai.DefaultConfig("not-needed")
	cfg.BaseURL = baseURL
	cfg.HTTPClient = &http.Client{Timeout: 90 * time.Second}
	return openai.NewClientWithConfig(cfg)
}
