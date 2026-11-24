package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/explorerTNT/TinyCode/internal/config"
)

// fakeModel is a scriptable OpenAI-compatible server: it records the
// messages of every request and answers each one as the script decides.
type fakeModel struct {
	mu       sync.Mutex
	requests [][]string // "role: content" of every message, per request
	answer   func(prompt string) string
	srv      *httptest.Server
}

func newFakeModel(t *testing.T, answer func(prompt string) string) *fakeModel {
	t.Helper()
	f := &fakeModel{answer: answer}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		var msgs []string
		for _, m := range body.Messages {
			msgs = append(msgs, m.Role+": "+m.Content)
		}
		f.mu.Lock()
		f.requests = append(f.requests, msgs)
		f.mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(f.answer(strings.Join(msgs, "\n"))))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeModel) request(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.requests[i], "\n")
}

func sseChunk(v any) string {
	b, _ := json.Marshal(v)
	return "data: " + string(b) + "\n\n"
}

// respondCall streams one call of the respond tool with the given message.
func respondCall(message string) string {
	args, _ := json.Marshal(map[string]string{"message": message})
	chunk := map[string]any{"id": "x", "object": "chat.completion.chunk", "created": 1, "model": "m"}
	chunk["choices"] = []any{map[string]any{"index": 0, "finish_reason": nil, "delta": map[string]any{
		"role": "assistant",
		"tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "type": "function",
			"function": map[string]any{"name": "respond", "arguments": string(args)}}},
	}}}
	final := map[string]any{"id": "x", "object": "chat.completion.chunk", "created": 1, "model": "m",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}},
		"usage":   map[string]any{"prompt_tokens": 100, "completion_tokens": 10, "total_tokens": 110}}
	return sseChunk(chunk) + sseChunk(final) + "data: [DONE]\n\n"
}

// silentStream is a model that returns nothing at all.
const silentStream = "data: [DONE]\n\n"

func fakeAgent(t *testing.T, f *fakeModel) *Agent {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	cfg := config.Default()
	cfg.LM.Host = "127.0.0.1"
	cfg.LM.Port = 0
	if _, err := fmt.Sscanf(f.srv.URL, "http://127.0.0.1:%d", &cfg.LM.Port); err != nil {
		t.Fatal(err)
	}
	cfg.TN.Workspace = t.TempDir()
	cfg.TN.PermissionMode = "auto"
	a, err := New(cfg, &memIO{})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestPlanSteps(t *testing.T) {
	plan := "## Plan\n1. Create `a.py`.\n2) Update the test.\n- top level bullet\n   - indented detail\n10. Last\n\nSome prose without a marker."
	got := planSteps(plan)
	want := []string{"Create `a.py`.", "Update the test.", "top level bullet", "Last"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("planSteps = %q, want %q", got, want)
	}
	if planSteps("just text") != nil {
		t.Fatal("no markers, no steps")
	}
}

// Every step runs in its own short context: the plan, the log of the
// earlier steps and the current step, nothing else.
func TestRunPlanStepsOneContextPerStep(t *testing.T) {
	f := newFakeModel(t, func(prompt string) string {
		switch {
		case strings.Contains(prompt, "CURRENT STEP (1 of 2)"):
			return respondCall("created a.py")
		case strings.Contains(prompt, "CURRENT STEP (2 of 2)"):
			return respondCall("updated a.py")
		}
		return respondCall("unexpected request")
	})
	a := fakeAgent(t, f)

	a.runPlanSteps("## Plan\n1. Create a.py\n2. Update a.py")

	if len(f.requests) != 2 {
		t.Fatalf("got %d requests, want one per step", len(f.requests))
	}
	first, second := f.request(0), f.request(1)
	if !strings.Contains(first, "STEPS ALREADY DONE (do not redo them):\n(none yet)") ||
		!strings.Contains(first, "CURRENT STEP (1 of 2):\n1. Create a.py") {
		t.Fatalf("first step prompt is wrong:\n%s", first)
	}
	if !strings.Contains(second, "[x] 1. Create a.py\n    result: created a.py") ||
		!strings.Contains(second, "CURRENT STEP (2 of 2):\n2. Update a.py") {
		t.Fatalf("second step must see the plan and the log:\n%s", second)
	}
	if strings.Contains(second, "CURRENT STEP (1 of 2)") {
		t.Fatal("the second step ran in the context of the first")
	}
	// The system prompt is sent as is and the folder listing is a separate
	// note: rewriting the system message breaks the server's prompt cache.
	if strings.Contains(f.requests[0][0], "ENVIRONMENT STATE") || !strings.HasPrefix(f.requests[0][0], "system: You are tiny-code") {
		t.Fatalf("system message was rewritten:\n%s", f.requests[0][0])
	}
	if !strings.Contains(a.io.(*memIO).text(), "all 2 steps done") {
		t.Fatal("the run did not finish")
	}
	// The log stays behind for follow-up questions.
	history := a.snapshotMessages()
	if len(history) != 3 || !strings.Contains(history[1].Content, "result: updated a.py") {
		t.Fatalf("log not kept: %+v", history)
	}
}

func TestRunPlanStepsStopsWhenAStepFails(t *testing.T) {
	f := newFakeModel(t, func(string) string { return silentStream })
	a := fakeAgent(t, f)

	a.runPlanSteps("1. First\n2. Second")

	if strings.Contains(a.io.(*memIO).text(), "all 2 steps done") {
		t.Fatal("a failed step must not pass as done")
	}
	if !strings.Contains(a.io.(*memIO).text(), "did not finish (no_response)") {
		t.Fatalf("the user must learn why the plan stopped:\n%s", a.io.(*memIO).text())
	}
	if history := a.snapshotMessages(); len(history) < 2 || !strings.Contains(history[1].Content, "CURRENT STEP (1 of 2)") {
		t.Fatal("the failed step's conversation must be kept for a follow-up")
	}
}

func TestRunPlanStepsSingleStepRunsInline(t *testing.T) {
	f := newFakeModel(t, func(string) string { return respondCall("done") })
	a := fakeAgent(t, f)

	a.runPlanSteps("1. Only step")

	if len(f.requests) != 1 || !strings.Contains(f.request(0), "Execute this plan step by step") {
		t.Fatalf("a single step should run as one ordinary turn:\n%s", f.request(0))
	}
}
