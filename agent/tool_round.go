package agent

import "github.com/mosaxiv/clawlet/llm"

func appendToolRound(
	messages []llm.Message,
	assistantContent string,
	toolCalls []llm.ToolCall,
	exec func(tc llm.ToolCall) string,
) []llm.Message {
	if len(toolCalls) == 0 {
		return messages
	}

	tcs := make([]llm.ToolCallPayload, 0, len(toolCalls))
	for _, tc := range toolCalls {
		tcs = append(tcs, llm.ToolCallPayload{
			ID:   tc.ID,
			Type: "function",
			Function: llm.ToolCallPayloadFunc{
				Name:      tc.Name,
				Arguments: string(tc.Arguments),
			},
		})
	}
	messages = append(messages, llm.Message{Role: "assistant", Content: assistantContent, ToolCalls: tcs})

	for _, tc := range toolCalls {
		out := exec(tc)
		messages = append(messages, llm.Message{
			Role:       "tool",
			ToolCallID: tc.ID,
			Name:       tc.Name,
			Content:    out,
		})
	}

	return append(messages, llm.Message{Role: "user", Content: "Reflect on the results and decide next steps."})
}
