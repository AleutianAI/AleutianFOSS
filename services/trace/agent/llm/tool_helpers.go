// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

package llm

import (
	"encoding/json"

	"github.com/AleutianAI/AleutianFOSS/services/trace/cli/tools"
)

// convertToolDefs converts agent tool definitions to generic LLM tool format.
//
// Description:
//
//	Maps tools.ToolDefinition to ToolDef for use with provider
//	ChatWithTools methods. Preserves parameter types, descriptions,
//	and required fields.
//
// Inputs:
//   - defs: Tool definitions in agent format.
//
// Outputs:
//   - []ToolDef: Tools in generic LLM format.
//
// Thread Safety: This function is safe for concurrent use.
func convertToolDefs(defs []tools.ToolDefinition) []ToolDef {
	if len(defs) == 0 {
		return nil
	}

	result := make([]ToolDef, 0, len(defs))
	for _, def := range defs {
		properties := make(map[string]ToolParamDef)
		var required []string

		for paramName, paramDef := range def.Parameters {
			pd := ToolParamDef{
				Type:        string(paramDef.Type),
				Description: paramDef.Description,
				Enum:        paramDef.Enum,
				Default:     paramDef.Default,
			}
			if paramDef.Items != nil {
				pd.Items = &ToolParamDef{
					Type:        string(paramDef.Items.Type),
					Description: paramDef.Items.Description,
				}
			}
			properties[paramName] = pd
			if paramDef.Required {
				required = append(required, paramName)
			}
		}

		result = append(result, ToolDef{
			Type: "function",
			Function: ToolFunction{
				Name:        def.Name,
				Description: def.Description,
				Parameters: ToolParameters{
					Type:       "object",
					Properties: properties,
					Required:   required,
				},
			},
		})
	}

	return result
}

// convertToChat converts agent Messages to ChatMessage, preserving tool IDs.
//
// Description:
//
//	Converts the agent's Request into ChatMessage slice suitable for
//	provider ChatWithTools methods. Handles:
//	- System prompt → ChatMessage{Role: "system"}
//	- User/assistant → ChatMessage{Role, Content}
//	- Tool results → ChatMessage{Role: "tool", ToolCallID, Content, ToolName}
//	- Assistant with ToolCalls → ChatMessage{Role: "assistant", ToolCalls}
//
// Inputs:
//   - request: The agent request containing messages and system prompt.
//
// Outputs:
//   - []ChatMessage: Messages in generic LLM format with tool metadata.
//
// Thread Safety: This function is safe for concurrent use.
func convertToChat(request *Request) []ChatMessage {
	if request == nil {
		return nil
	}

	messages := make([]ChatMessage, 0, len(request.Messages)+1)

	if request.SystemPrompt != "" {
		messages = append(messages, ChatMessage{
			Role:    "system",
			Content: request.SystemPrompt,
		})
	}

	for _, msg := range request.Messages {
		chatMsg := ChatMessage{
			Role:    msg.Role,
			Content: msg.Content,
		}

		// Handle assistant messages with tool calls
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				chatMsg.ToolCalls = append(chatMsg.ToolCalls, ToolCallResponse{
					ID:               tc.ID,
					Name:             tc.Name,
					Arguments:        json.RawMessage(tc.Arguments),
					ThoughtSignature: tc.ThoughtSignature,
				})
			}
		}

		// Handle tool result messages
		if msg.Role == "tool" && len(msg.ToolResults) > 0 {
			// Use the first tool result's ID and content
			tr := msg.ToolResults[0]
			chatMsg.ToolCallID = tr.ToolCallID
			if tr.Content != "" {
				chatMsg.Content = tr.Content
			}
			// Look up tool name from the preceding assistant message's ToolCalls.
			// Gemini requires ToolName for functionResponse.
			chatMsg.ToolName = tr.ToolCallID // fallback
			for j := len(messages) - 1; j >= 0; j-- {
				for _, tc := range messages[j].ToolCalls {
					if tc.ID == tr.ToolCallID {
						chatMsg.ToolName = tc.Name
						break
					}
				}
				if chatMsg.ToolName != tr.ToolCallID {
					break
				}
			}
		}

		messages = append(messages, chatMsg)
	}

	return messages
}

// estimateInputTokensChat estimates input tokens from ChatMessage slice.
//
// Description:
//
//	Provides a rough token estimate (~4 characters per token) for
//	ChatMessage slices used by the completeWithTools path.
//
// Inputs:
//   - messages: The ChatMessage input messages.
//
// Outputs:
//   - int: Estimated input token count.
//
// Thread Safety: This function is safe for concurrent use.
func estimateInputTokensChat(messages []ChatMessage) int {
	total := 0
	for _, msg := range messages {
		total += len(msg.Content)
	}
	return total / 4
}
