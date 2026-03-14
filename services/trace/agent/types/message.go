// Copyright (C) 2025 Aleutian AI (jinterlante@aleutian.ai)
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
// See the LICENSE.txt file for the full license text.
//
// NOTE: This work is subject to additional terms under AGPL v3 Section 7.
// See the NOTICE.txt file for details regarding AI system attribution.

// Package types provides shared types used across the trace agent's sub-packages.
//
// This package exists to break import cycles between the providers and llm
// packages, both of which need the Message type.
//
// Thread Safety: All types in this package are value types, safe for concurrent reads.
package types

// Message represents a single chat message in a conversation.
//
// Description:
//
//	Used by ChatClient and all LLM provider adapters to pass conversation
//	history. Previously defined in orchestrator/datatypes; internalized here
//	so the trace service has no cross-service dependencies.
//
// Thread Safety: Message is a value type; safe for concurrent reads.
type Message struct {
	// MessageID is an optional UUID4 identifier for the message.
	MessageID string `json:"message_id,omitempty"`

	// Timestamp is the Unix millisecond UTC timestamp of the message.
	Timestamp int64 `json:"timestamp,omitempty"`

	// Role is the message sender role: "user", "assistant", or "system".
	Role string `json:"role"`

	// Content is the message text.
	Content string `json:"content"`
}
