package doctor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/explorerTNT/TinyCode/internal/config"
	"github.com/explorerTNT/TinyCode/internal/i18n"
)

func doctorServer(t *testing.T, model string, nCtx int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"object":"list","data":[{"id":%q,"object":"model"},{"id":"other","object":"model"}]}`, model)
	})
	mux.HandleFunc("/props", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"total_slots":1,"chat_template":"{%% for tool in tools %%}","default_generation_settings":{"n_ctx":%d}}`, nCtx)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[],"timings":{"prompt_per_second":812.5,"predicted_per_second":10.4}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func doctorConfig(url, model string) *config.Config {
	cfg := config.Default()
	_, _ = fmt.Sscanf(url, "http://127.0.0.1:%d", &cfg.LM.Port)
	cfg.LM.Host = "127.0.0.1"
	cfg.LM.Name = model
	return cfg
}

func TestRunHealthyServer(t *testing.T) {
	i18n.Set(i18n.En)
	cfg := doctorConfig(doctorServer(t, "Qwen/Qwen3.5-2B", 32768).URL, "qwen3.5-2b")

	report := Run(cfg)
	text := strings.Join(report.Lines, "\n")
	if report.Problems != 0 {
		t.Fatalf("problems = %d:\n%s", report.Problems, text)
	}
	for _, want := range []string{"models served: Qwen/Qwen3.5-2B", "context 32768 tokens", "prompt 812 tok/s", "generation 10 tok/s", "OK:"} {
		if !strings.Contains(text, want) {
			t.Errorf("report misses %q:\n%s", want, text)
		}
	}
}

func TestRunReportsProblems(t *testing.T) {
	i18n.Set(i18n.En)
	// A model that is not served and a context smaller than the agent asks for.
	cfg := doctorConfig(doctorServer(t, "other", 4096).URL, "missing-model")

	report := Run(cfg)
	text := strings.Join(report.Lines, "\n")
	if report.Problems < 2 {
		t.Fatalf("problems = %d:\n%s", report.Problems, text)
	}
	for _, want := range []string{"'missing-model' is not among them", "context 4096", "smaller than context_limit"} {
		if !strings.Contains(text, want) {
			t.Errorf("report misses %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "OK:") {
		t.Error("a report with problems must not claim everything is fine")
	}
}

func TestRunServerDown(t *testing.T) {
	i18n.Set(i18n.En)
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	report := Run(doctorConfig(url, "m"))
	if report.Problems == 0 || !strings.Contains(strings.Join(report.Lines, "\n"), "cannot reach the model server") {
		t.Fatalf("unreachable server not reported: %+v", report)
	}
}

func TestProbeFallsBackToUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"usage": map[string]int{"completion_tokens": 40}})
	}))
	t.Cleanup(srv.Close)

	prompt, gen, err := probe(srv.URL, "m")
	if err != nil || prompt != 0 || gen <= 0 {
		t.Fatalf("probe = (%.1f, %.1f, %v)", prompt, gen, err)
	}
}

func TestProbeSurfacesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"error":{"message":"model not loaded"}}`))
	}))
	t.Cleanup(srv.Close)

	if _, _, err := probe(srv.URL, "m"); err == nil || !strings.Contains(err.Error(), "model not loaded") {
		t.Fatalf("err = %v", err)
	}
}

func TestSameModel(t *testing.T) {
	for _, pair := range [][2]string{{"Qwen/Qwen3.5-2B", "qwen3.5-2b"}, {"m", "owner/m"}, {"m", "m"}} {
		if !sameModel(pair[0], pair[1]) {
			t.Errorf("sameModel(%q, %q) = false", pair[0], pair[1])
		}
	}
	if sameModel("m", "other") {
		t.Error("different models must not match")
	}
}
