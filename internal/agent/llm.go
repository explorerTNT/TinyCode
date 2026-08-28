package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"

	"github.com/explorerTNT/TinyCode/internal/i18n"
)

type callOptions struct {
	spinnerMessage string
	spinner        bool
	silent         bool
	forcePrompt    string
	maxTokens      int
	useTools       bool
	noThinking     bool
}

type streamToolCall struct {
	name string
	args string
	id   string
}

// callLLM streams one completion and returns the built assistant message, or
// nil on any error (which is printed to the reporter). It owns the agent-level
// side effects: spinner, cancel registration, call counter, and the single
// history append.
func (a *Agent) callLLM(o callOptions) *Message {
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.cancel = cancel
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.cancel = nil
		a.mu.Unlock()
		cancel()
	}()

	o.spinner = true

	if !o.silent {
		a.llmCalls++
		a.io.Status(i18n.T("llm.thinking", a.llmCalls))
	}

	msg := a.streamCompletion(ctx, a.prepareMessages(), o)

	if msg != nil && (msg.Content != "" || len(msg.ToolCalls) > 0) {
		a.addMsg(*msg)
	}
	return msg
}

// prepareMessages returns a trimmed, tool-result-cleared snapshot of the
// conversation history, ready to hand to streamCompletion.
func (a *Agent) prepareMessages() []Message {
	trimmed := trimMessages(a.snapshotMessages(), a.ctx.maxTokens, a.ctx.reserve)
	return clearOldToolResults(trimmed, keepFullToolResults)
}

// streamCompletion streams one completion from already-prepared messages and
// returns the built assistant message, or nil on error. It is stateless with
// respect to the agent: no history mutation, no cancel registration, no call
// counter, no status output when silent.
func (a *Agent) streamCompletion(ctx context.Context, messages []Message, o callOptions) *Message {
	normalized := normalizeMessages(messages)

	maxTokens := o.maxTokens
	if maxTokens == 0 {
		maxTokens = a.config.TN.MaxTokens
	}

	req := openai.ChatCompletionRequest{
		Model:         a.config.LM.Name,
		Messages:      toOpenAI(normalized),
		MaxTokens:     maxTokens,
		Temperature:   float32(a.config.TN.Temperature),
		StreamOptions: &openai.StreamOptions{IncludeUsage: true},
	}

	if o.noThinking {
		req.ChatTemplateKwargs = map[string]any{"enable_thinking": false}
	}
	if o.useTools {
		req.Tools = a.toolSchemas
		req.Temperature = float32(a.config.TN.ActionTemperature)
		req.ToolChoice = "required"
	}

	if len(req.Messages) > 0 && req.Messages[0].Role == openai.ChatMessageRoleSystem {
		req.Messages[0].Content = req.Messages[0].Content + "\n\n" + a.envState()
	} else {
		req.Messages = append([]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleSystem, Content: a.envState()}}, req.Messages...)
	}

	if o.forcePrompt != "" {
		req.Messages = append(req.Messages, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: o.forcePrompt})
	}

	stopSpinner := func() {}
	if o.spinner {
		stopSpinner = a.startSpinner(o.spinnerMessage)
	}

	stream, err := a.client.CreateChatCompletionStream(ctx, req)
	stopSpinner()
	if err != nil {
		a.reportStreamError(err)
		return nil
	}
	defer stream.Close()

	callNum := 0
	if !o.silent {
		callNum = a.llmCalls
	}

	return a.processStream(ctx, stream, o.silent, callNum)
}

func (a *Agent) reportStreamError(err error) {
	var apiErr *openai.APIError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		a.io.Println(i18n.T("llm.stalled"))
	case errors.As(err, &apiErr):
		switch apiErr.HTTPStatusCode {
		case 429:
			a.io.Println(i18n.T("llm.rate_limited"))
		default:
			a.io.Println(i18n.T("llm.error", err))
		}
	default:
		a.io.Println(i18n.T("llm.cant_connect", a.config.LM.Host, a.config.LM.Port))
		a.io.Println(i18n.T("llm.port_hint", a.config.LM.Port))
	}
}

func (a *Agent) startSpinner(message string) func() {
	if message == "" {
		message = i18n.T("llm.spinner_default")
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		chars := `-\|/-\|/`
		i := 0
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				a.io.Status(string(chars[i%len(chars)]) + " " + message)
				i++
			}
		}
	}()
	return func() {
		close(stop)
		<-done
		a.io.Status("")
	}
}

func (a *Agent) processStream(ctx context.Context, stream *openai.ChatCompletionStream, silent bool, callNum int) *Message {
	start := time.Now()
	var content, reasoning strings.Builder
	toolCalls := map[int]*streamToolCall{}
	var finishReason openai.FinishReason
	var usage *openai.Usage
	repeated := false

	for {
		chunk, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, context.DeadlineExceeded) {
				if !silent {
					a.io.Println(i18n.T("llm.stalled"))
				}
				return nil
			}
			if !silent {
				a.io.Println(i18n.T("llm.stream_error", err))
			}
			return nil
		}
		if ctx.Err() != nil {
			return nil
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
		if len(chunk.Choices) == 0 {
			continue
		}

		delta := chunk.Choices[0].Delta
		finish := chunk.Choices[0].FinishReason
		if finish != "" {
			finishReason = finish
		}

		if delta.ReasoningContent != "" {
			reasoning.WriteString(delta.ReasoningContent)
			if !silent {
				a.io.Status(i18n.T("llm.thinking_toks", callNum, reasoning.Len(), time.Since(start).Seconds()))
			}
		}
		if delta.Content != "" {
			content.WriteString(delta.Content)
			if !silent {
				a.io.Status(i18n.T("llm.answering_toks", callNum, content.Len(), time.Since(start).Seconds()))
			}
			if content.Len() > 600 {
				lowered := strings.ToLower(content.String())
				tail := lowered[len(lowered)-200:]
				prev := lowered[:len(lowered)-200]
				if strings.LastIndex(prev, tail) >= max(0, len(prev)-400) {
					repeated = true
					break
				}
			}
		}
		if len(delta.ToolCalls) > 0 {
			if !silent {
				a.io.Status(i18n.T("llm.preparing_tool", callNum))
			}
			for _, tc := range delta.ToolCalls {
				idx := 0
				if tc.Index != nil {
					idx = *tc.Index
				}
				stc := toolCalls[idx]
				if stc == nil {
					stc = &streamToolCall{}
					toolCalls[idx] = stc
				}
				if tc.ID != "" {
					stc.id = tc.ID
				}
				if tc.Function.Name != "" {
					stc.name += tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					stc.args += tc.Function.Arguments
				}
			}
		}
	}

	if ctx.Err() != nil {
		return nil
	}

	if !silent {
		elapsed := time.Since(start).Seconds()
		tok := i18n.T("llm.tok_chars", reasoning.Len()+content.Len())
		if usage != nil && usage.CompletionTokens > 0 {
			tok = i18n.T("llm.tok_tokens", usage.CompletionTokens)
		}
		var summary string
		switch {
		case len(toolCalls) > 0:
			summary = i18n.T("llm.summary_tool", callNum, tok, elapsed)
		case reasoning.Len()+content.Len() > 0:
			summary = i18n.T("llm.summary_model", callNum, tok, elapsed)
		default:
			summary = i18n.T("llm.summary_empty", callNum, elapsed)
		}
		a.io.Println(summary)
	}

	if repeated && len(toolCalls) == 0 {
		if !silent {
			a.io.Println(i18n.T("llm.repetition"))
		}
		return &Message{Role: "assistant", Content: content.String(), Cut: "repetition"}
	}

	if len(toolCalls) > 0 {
		if result := buildFromStream(content.String(), toolCalls, finishReason); result != nil {
			return result
		}
	}

	if len(toolCalls) == 0 && content.Len() > 0 {
		if parsed := parseXMLToolCalls(content.String()); parsed != nil {
			return &Message{Role: "assistant", Content: content.String(), ToolCalls: parsed}
		}
	}

	if content.Len() > 0 {
		return &Message{Role: "assistant", Content: content.String()}
	}

	if reasoning.Len() > 0 {
		return &Message{Role: "assistant", Content: "", ReasoningContent: reasoning.String()}
	}

	return nil
}

func buildFromStream(content string, toolCalls map[int]*streamToolCall, finish openai.FinishReason) *Message {
	keys := make([]int, 0, len(toolCalls))
	for k := range toolCalls {
		keys = append(keys, k)
	}
	sort.Ints(keys)

	built := make([]ToolCall, 0, len(keys))
	truncated := false
	for _, idx := range keys {
		tc := toolCalls[idx]
		rawArgs := tc.args
		var parsed any
		if rawArgs != "" {
			parsed = safeJSONLoads(rawArgs)
		}
		if rawArgs != "" && finish == openai.FinishReasonLength {
			truncated = true
		} else if rawArgs != "" {
			var v any
			if err := json.Unmarshal([]byte(rawArgs), &v); err != nil {
				truncated = true
			}
		}

		argsJSON := "{}"
		if parsed != nil {
			if b, err := json.Marshal(parsed); err == nil {
				argsJSON = string(b)
			}
		}

		id := tc.id
		if id == "" {
			id = fmt.Sprintf("call_%d", idx)
		}
		built = append(built, ToolCall{ID: id, Type: "function", Name: tc.name, Arguments: argsJSON})
	}

	if len(built) == 0 {
		return nil
	}

	return &Message{Role: "assistant", Content: content, ToolCalls: built, Truncated: truncated}
}
