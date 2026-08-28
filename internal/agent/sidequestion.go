package agent

import (
	"context"
	"strings"
)

// sideQuestionReminder wraps a /btw question, mirroring Claude Code's
// side-question system-reminder: the fork is a lightweight separate agent that
// must not interrupt the main agent, has no tools, and answers in one response.
const sideQuestionReminder = `<system-reminder>
You are answering a "by the way" side question from the user. A separate main agent is actively working on the project right now and must NOT be interrupted. You are a lightweight, isolated helper that only shares a snapshot of the same conversation history. You have no tools available. Answer the question directly and concisely in a single response. Do not ask for clarification and do not attempt to call any tool.
</system-reminder>`

// RunSideQuestion answers a question against a snapshot of the current
// conversation without mutating the main agent's state. It is safe to call
// concurrently with a running turn.
func (a *Agent) RunSideQuestion(question string) string {
	question = strings.TrimSpace(question)
	if question == "" {
		return ""
	}

	messages := a.prepareMessages()
	messages = append(messages, Message{Role: "user", Content: sideQuestionReminder + "\n\n" + question})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	msg := a.streamCompletion(ctx, messages, callOptions{
		silent:     true,
		useTools:   false,
		noThinking: true,
		maxTokens:  a.config.TN.BtwMaxTokens,
	})
	if msg == nil {
		return ""
	}
	return strings.TrimSpace(msg.Content)
}
