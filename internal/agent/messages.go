package agent

import (
	"github.com/sashabaranov/go-openai"
)

// ToolCall is an assistant tool invocation.
type ToolCall struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Message is the agent's internal conversation message representation.
type Message struct {
	Role             string     `json:"role"`
	Content          string     `json:"content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	Truncated        bool       `json:"truncated,omitempty"`
	Cut              string     `json:"cut,omitempty"`
}

// toOpenAI converts the internal message list to go-openai messages.
func toOpenAI(messages []Message) []openai.ChatCompletionMessage {
	out := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, m := range messages {
		om := openai.ChatCompletionMessage{
			Role:    m.Role,
			Content: m.Content,
		}
		if m.Role == "tool" {
			om.ToolCallID = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			om.ToolCalls = make([]openai.ToolCall, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				om.ToolCalls = append(om.ToolCalls, openai.ToolCall{
					ID:   tc.ID,
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      tc.Name,
						Arguments: tc.Arguments,
					},
				})
			}
		}
		out = append(out, om)
	}
	return out
}

// normalizeMessages strips reasoning/cut fields and merges adjacent assistant
// messages, appending a "Continue." prompt when the last message is an
// assistant message without tool calls.
func normalizeMessages(messages []Message) []Message {
	normalized := make([]Message, 0, len(messages))
	for _, m := range messages {
		m.ReasoningContent = ""
		m.Cut = ""

		if m.Role == "assistant" && len(normalized) > 0 && normalized[len(normalized)-1].Role == "assistant" {
			prev := &normalized[len(normalized)-1]
			if prev.Content != "" && m.Content != "" {
				prev.Content = trimRightSpace(prev.Content) + "\n\n" + m.Content
			} else if m.Content != "" {
				prev.Content = m.Content
			}
			if len(m.ToolCalls) > 0 {
				prev.ToolCalls = append(append([]ToolCall{}, prev.ToolCalls...), m.ToolCalls...)
			}
			continue
		}
		normalized = append(normalized, m)
	}

	if len(normalized) > 0 {
		last := &normalized[len(normalized)-1]
		if last.Role == "assistant" && len(last.ToolCalls) == 0 {
			normalized = append(normalized, Message{Role: "user", Content: "Continue."})
		}
	}
	return normalized
}

func trimRightSpace(s string) string {
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
