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
)

type callOptions struct {
	spinnerMessage string
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
// nil on any error (which is printed to the reporter).
func (a *Agent) callLLM(o callOptions) *Message {
	messages := clearOldToolResults(a.ctx.trim(a.messages), keepFullToolResults)
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

	stopSpinner := a.startSpinner(o.spinnerMessage)

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

	stream, err := a.client.CreateChatCompletionStream(ctx, req)
	if err != nil {
		stopSpinner()
		a.reportStreamError(err)
		return nil
	}
	stopSpinner()

	if !o.silent {
		a.llmCalls++
		a.io.Status(fmt.Sprintf("  [#%d модель думает…]", a.llmCalls))
	}

	msg := a.processStream(stream, o.silent)
	_ = stream.Close()
	return msg
}

func (a *Agent) reportStreamError(err error) {
	var apiErr *openai.APIError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		a.io.Println("\n  [Model stalled (90s timeout). Forcing continue.]")
	case errors.As(err, &apiErr):
		switch apiErr.HTTPStatusCode {
		case 429:
			a.io.Println("\n  [Error: Rate limited. Wait and try again.]")
		default:
			a.io.Println(fmt.Sprintf("\n  [Error: %s]", err))
		}
	default:
		a.io.Println(fmt.Sprintf("\n  [Error: Can't connect to the model server at http://%s:%d]", a.config.LM.Host, a.config.LM.Port))
		a.io.Println(fmt.Sprintf("  [Make sure the server is running on port %d]\n", a.config.LM.Port))
	}
}

func (a *Agent) startSpinner(message string) func() {
	if message == "" {
		message = "жду ответа модели…"
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

func (a *Agent) processStream(stream *openai.ChatCompletionStream, silent bool) *Message {
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
			if errors.Is(err, context.Canceled) && a.aborted.Load() {
				return nil
			}
			if errors.Is(err, context.DeadlineExceeded) {
				if !silent {
					a.io.Println("\n  [Model stalled (90s timeout). Forcing continue.]")
				}
				return nil
			}
			if !silent {
				a.io.Println(fmt.Sprintf("\n  [stream error: %v]", err))
			}
			return nil
		}
		if a.aborted.Load() {
			break
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
				a.io.Status(fmt.Sprintf("  [#%d модель думает: %d ток • %.0fс]", a.llmCalls, reasoning.Len(), time.Since(start).Seconds()))
			}
		}
		if delta.Content != "" {
			content.WriteString(delta.Content)
			if !silent {
				a.io.Status(fmt.Sprintf("  [#%d модель отвечает: %d ток • %.0fс]", a.llmCalls, content.Len(), time.Since(start).Seconds()))
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
				a.io.Status(fmt.Sprintf("  [#%d модель готовит вызов инструмента…]", a.llmCalls))
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

	if a.aborted.Load() {
		return nil
	}

	if !silent {
		elapsed := time.Since(start).Seconds()
		tok := fmt.Sprintf("%d симв", reasoning.Len()+content.Len())
		if usage != nil && usage.CompletionTokens > 0 {
			tok = fmt.Sprintf("%d ток", usage.CompletionTokens)
		}
		var summary string
		switch {
		case len(toolCalls) > 0:
			summary = fmt.Sprintf("\r  [#%d вызов инструмента • %s • %.0fс]", a.llmCalls, tok, elapsed)
		case reasoning.Len()+content.Len() > 0:
			summary = fmt.Sprintf("\r  [#%d модель • %s • %.0fс]", a.llmCalls, tok, elapsed)
		default:
			summary = fmt.Sprintf("\r  [#%d пусто • %.0fс]", a.llmCalls, elapsed)
		}
		a.io.Println(summary)
	}

	if repeated && len(toolCalls) == 0 {
		if !silent {
			a.io.Println("\n  [repetition detected, response cut]")
		}
		return &Message{Role: "assistant", Content: content.String(), Cut: "repetition"}
	}

	if len(toolCalls) > 0 {
		if result := a.buildFromStream(content.String(), toolCalls, finishReason); result != nil {
			return result
		}
	}

	if len(toolCalls) == 0 && content.Len() > 0 {
		if parsed := parseXMLToolCalls(content.String()); parsed != nil {
			msg := &Message{Role: "assistant", Content: content.String(), ToolCalls: parsed}
			a.addMsg(*msg)
			return msg
		}
	}

	if content.Len() > 0 {
		msg := &Message{Role: "assistant", Content: content.String()}
		a.addMsg(*msg)
		return msg
	}

	if reasoning.Len() > 0 {
		return &Message{Role: "assistant", Content: "", ReasoningContent: reasoning.String()}
	}

	return nil
}

func (a *Agent) buildFromStream(content string, toolCalls map[int]*streamToolCall, finish openai.FinishReason) *Message {
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

	msg := &Message{Role: "assistant", Content: content, ToolCalls: built, Truncated: truncated}
	a.addMsg(*msg)
	return msg
}
