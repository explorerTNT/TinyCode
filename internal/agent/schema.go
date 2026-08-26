package agent

import (
	"github.com/explorerTNT/TinyCode/internal/tools"
	"github.com/sashabaranov/go-openai"
)

// buildToolSchemas converts the tools plus the synthetic respond tool into
// go-openai tool definitions.
func buildToolSchemas(toolList []*tools.Tool) []openai.Tool {
	out := make([]openai.Tool, 0, len(toolList)+1)
	for _, t := range toolList {
		out = append(out, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Schema(),
			},
		})
	}
	out = append(out, respondTool())
	return out
}

func respondTool() openai.Tool {
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name: "respond",
			Description: "Send your final answer to the user. Call this when the task is done " +
				"or when you need to reply with text instead of doing more work.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]any{
						"type":        "string",
						"description": "the reply to show the user",
					},
				},
				"required":             []string{"message"},
				"additionalProperties": false,
			},
		},
	}
}
