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
	"regexp"
)

// redactionPattern pairs a compiled regex with a replacement label.
//
// Description:
//
//	Each pattern identifies a specific class of secret (API key, token,
//	password) and provides a labeled replacement string so the log reader
//	knows what was redacted without seeing the secret value.
//
// Fields:
//   - Pattern: Compiled regex that matches the secret format.
//   - Replacement: The string that replaces matched secrets in output.
//
// Thread Safety: This type is immutable after construction.
type redactionPattern struct {
	Pattern     *regexp.Regexp
	Replacement string
}

// redactionPatterns is the ordered list of secret patterns to redact.
//
// IMPORTANT: Order matters. More specific patterns (e.g., sk-ant-api03-)
// must appear BEFORE less specific patterns (e.g., sk-) to prevent
// partial redaction. For example, "sk-ant-api03-abc123" should match
// the Anthropic pattern, not the OpenAI pattern.
//
// Thread Safety: This slice is initialized once and never modified.
// All access is read-only.
var redactionPatterns = []redactionPattern{
	// Anthropic API key: sk-ant-api03-<base62>
	// Must be before OpenAI pattern because both start with "sk-".
	{
		Pattern:     regexp.MustCompile(`sk-ant-api03-[A-Za-z0-9_-]{20,}`),
		Replacement: "[REDACTED:anthropic_key]",
	},
	// OpenAI API key: sk-<base62, 20+ chars>
	// Requires 20+ chars after "sk-" to avoid matching short strings like "sk-test".
	{
		Pattern:     regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`),
		Replacement: "[REDACTED:openai_key]",
	},
	// Gemini/Google API key: AIza<base62, 30+ chars>
	{
		Pattern:     regexp.MustCompile(`AIza[A-Za-z0-9_-]{30,}`),
		Replacement: "[REDACTED:gemini_key]",
	},
	// Bearer token in Authorization header values
	{
		Pattern:     regexp.MustCompile(`Bearer\s+[A-Za-z0-9._-]{10,}`),
		Replacement: "[REDACTED:bearer_token]",
	},
	// API key in URL query parameter: key=<value>
	{
		Pattern:     regexp.MustCompile(`key=[A-Za-z0-9._-]{10,}`),
		Replacement: "key=[REDACTED]",
	},
	// Password in connection strings or config: password=<value>
	{
		Pattern:     regexp.MustCompile(`password=[^\s&]{3,}`),
		Replacement: "password=[REDACTED]",
	},
	// Database connection strings with credentials: proto://user:pass@host
	{
		Pattern:     regexp.MustCompile(`(postgres|mysql|mongodb)://[^\s]+@`),
		Replacement: "${1}://[REDACTED]@",
	},
}

// SafeLogString redacts known secret patterns from a string before logging.
//
// Description:
//
//	Iterates through a predefined set of regex patterns that match common
//	API key formats, bearer tokens, passwords, and connection strings.
//	Each match is replaced with a labeled placeholder (e.g., [REDACTED:openai_key])
//	so the log reader knows what class of secret was present without seeing
//	the actual value.
//
// Inputs:
//   - s: The string to redact. May contain zero or more secrets.
//     Empty string is valid and returns empty string.
//
// Outputs:
//   - string: The input with all matched secret patterns replaced.
//     If no patterns match, returns the original string unchanged.
//
// Examples:
//
//	SafeLogString("error: sk-ant-api03-abc123def456ghi789jkl012 returned 401")
//	// Returns: "error: [REDACTED:anthropic_key] returned 401"
//
//	SafeLogString("normal log message with no secrets")
//	// Returns: "normal log message with no secrets"
//
//	SafeLogString("key=AIzaSyAbcDefGhiJklMnoPqrStUvWxYz01234567 in URL")
//	// Returns: "key=[REDACTED] in URL"
//
// Limitations:
//   - Pattern-based detection only. Cannot detect secrets that do not match
//     known formats (e.g., custom API keys with non-standard prefixes).
//   - This is NOT cryptographically secure redaction. It catches common
//     patterns per IT-00b lesson C-11 (regex can't do semantic work).
//   - A secret that spans multiple lines will not be matched (single-line regex).
//
// Assumptions:
//   - The input string is a single log line or error message.
//   - Patterns are ordered most-specific-first (see redactionPatterns comment).
//   - The caller does not rely on the exact redacted output format for parsing.
//
// Thread Safety: This function is safe for concurrent use.
func SafeLogString(s string) string {
	if s == "" {
		return s
	}
	for _, p := range redactionPatterns {
		s = p.Pattern.ReplaceAllString(s, p.Replacement)
	}
	return s
}
