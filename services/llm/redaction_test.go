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
	"strings"
	"testing"
)

func TestSafeLogString_AnthropicKey(t *testing.T) {
	input := "error with sk-ant-api03-abcdefghijklmnopqrstuvwxyz123456 in message"
	result := SafeLogString(input)

	if strings.Contains(result, "sk-ant-api03-") {
		t.Errorf("Anthropic key not redacted: %s", result)
	}
	if !strings.Contains(result, "[REDACTED:anthropic_key]") {
		t.Errorf("expected [REDACTED:anthropic_key] in result: %s", result)
	}
	if !strings.Contains(result, "error with") {
		t.Error("surrounding text was modified")
	}
	if !strings.Contains(result, "in message") {
		t.Error("trailing text was modified")
	}
}

func TestSafeLogString_OpenAIKey(t *testing.T) {
	input := "failed: sk-abcdefghijklmnopqrstuvwxyz1234 returned 401"
	result := SafeLogString(input)

	if strings.Contains(result, "sk-abcdefghijklmnopqrst") {
		t.Errorf("OpenAI key not redacted: %s", result)
	}
	if !strings.Contains(result, "[REDACTED:openai_key]") {
		t.Errorf("expected [REDACTED:openai_key] in result: %s", result)
	}
}

func TestSafeLogString_GeminiKey(t *testing.T) {
	input := "url has AIzaSyAbcDefGhiJklMnoPqrStUvWxYz0123456789extra in it"
	result := SafeLogString(input)

	if strings.Contains(result, "AIzaSy") {
		t.Errorf("Gemini key not redacted: %s", result)
	}
	if !strings.Contains(result, "[REDACTED:gemini_key]") {
		t.Errorf("expected [REDACTED:gemini_key] in result: %s", result)
	}
}

func TestSafeLogString_BearerToken(t *testing.T) {
	input := "Authorization: Bearer eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.abc"
	result := SafeLogString(input)

	if strings.Contains(result, "eyJhbGci") {
		t.Errorf("Bearer token not redacted: %s", result)
	}
	if !strings.Contains(result, "[REDACTED:bearer_token]") {
		t.Errorf("expected [REDACTED:bearer_token] in result: %s", result)
	}
}

func TestSafeLogString_URLKeyParam(t *testing.T) {
	input := "https://api.example.com/v1?key=abcdefghij1234567890 failed"
	result := SafeLogString(input)

	if strings.Contains(result, "abcdefghij1234567890") {
		t.Errorf("URL key param not redacted: %s", result)
	}
	if !strings.Contains(result, "key=[REDACTED]") {
		t.Errorf("expected key=[REDACTED] in result: %s", result)
	}
}

func TestSafeLogString_Password(t *testing.T) {
	input := "connection string: password=s3cretP@ss! failed"
	result := SafeLogString(input)

	if strings.Contains(result, "s3cretP@ss!") {
		t.Errorf("password not redacted: %s", result)
	}
	if !strings.Contains(result, "password=[REDACTED]") {
		t.Errorf("expected password=[REDACTED] in result: %s", result)
	}
}

func TestSafeLogString_PostgresConnectionString(t *testing.T) {
	input := "connecting to postgres://admin:secret123@db.example.com:5432/mydb"
	result := SafeLogString(input)

	if strings.Contains(result, "admin:secret123") {
		t.Errorf("connection string credentials not redacted: %s", result)
	}
	if !strings.Contains(result, "postgres://[REDACTED]@") {
		t.Errorf("expected postgres://[REDACTED]@ in result: %s", result)
	}
}

func TestSafeLogString_MySQLConnectionString(t *testing.T) {
	input := "mysql://root:password@localhost:3306/db"
	result := SafeLogString(input)

	if strings.Contains(result, "root:password") {
		t.Errorf("MySQL credentials not redacted: %s", result)
	}
	if !strings.Contains(result, "mysql://[REDACTED]@") {
		t.Errorf("expected mysql://[REDACTED]@ in result: %s", result)
	}
}

func TestSafeLogString_MongoDBConnectionString(t *testing.T) {
	input := "mongodb://user:pass@cluster0.example.net:27017"
	result := SafeLogString(input)

	if strings.Contains(result, "user:pass") {
		t.Errorf("MongoDB credentials not redacted: %s", result)
	}
	if !strings.Contains(result, "mongodb://[REDACTED]@") {
		t.Errorf("expected mongodb://[REDACTED]@ in result: %s", result)
	}
}

func TestSafeLogString_NoSecretsPassthrough(t *testing.T) {
	inputs := []string{
		"normal log message with no secrets",
		"parsing file /tmp/task_output.json",
		"user requested model gemini-1.5-flash",
		"status code 200, content length 1024",
		"",
	}

	for _, input := range inputs {
		result := SafeLogString(input)
		if result != input {
			t.Errorf("non-secret string was modified:\n  input:  %q\n  result: %q", input, result)
		}
	}
}

func TestSafeLogString_PartialMatchNotRedacted(t *testing.T) {
	t.Run("task contains sk but is not a key", func(t *testing.T) {
		input := "running task in background"
		result := SafeLogString(input)
		if result != input {
			t.Errorf("'task' was incorrectly redacted: %s", result)
		}
	})

	t.Run("sk-short is not long enough", func(t *testing.T) {
		input := "prefix sk-short suffix"
		result := SafeLogString(input)
		if result != input {
			t.Errorf("short sk- prefix was incorrectly redacted: %s", result)
		}
	})

	t.Run("key=short is not long enough", func(t *testing.T) {
		input := "key=abc"
		result := SafeLogString(input)
		if result != input {
			t.Errorf("short key value was incorrectly redacted: %s", result)
		}
	})

	t.Run("password with two chars is not redacted", func(t *testing.T) {
		input := "password=ab"
		result := SafeLogString(input)
		if result != input {
			t.Errorf("short password was incorrectly redacted: %s", result)
		}
	})

	t.Run("AIza without enough trailing chars", func(t *testing.T) {
		input := "prefix AIzaShort suffix"
		result := SafeLogString(input)
		if result != input {
			t.Errorf("short AIza prefix was incorrectly redacted: %s", result)
		}
	})
}

func TestSafeLogString_MultipleSecretsInOneString(t *testing.T) {
	input := "anthropic sk-ant-api03-abcdefghijklmnopqrstuvwxyz123456 " +
		"and openai sk-abcdefghijklmnopqrstuvwxyz1234 " +
		"and password=mysecret123"
	result := SafeLogString(input)

	if strings.Contains(result, "sk-ant-api03-") {
		t.Error("Anthropic key not redacted in multi-secret string")
	}
	if strings.Contains(result, "sk-abcdefghijklmnopqrst") {
		t.Error("OpenAI key not redacted in multi-secret string")
	}
	if strings.Contains(result, "mysecret123") {
		t.Error("password not redacted in multi-secret string")
	}
	if !strings.Contains(result, "[REDACTED:anthropic_key]") {
		t.Errorf("missing anthropic redaction label in: %s", result)
	}
	if !strings.Contains(result, "[REDACTED:openai_key]") {
		t.Errorf("missing openai redaction label in: %s", result)
	}
	if !strings.Contains(result, "password=[REDACTED]") {
		t.Errorf("missing password redaction label in: %s", result)
	}
}

func TestSafeLogString_EmptyString(t *testing.T) {
	result := SafeLogString("")
	if result != "" {
		t.Errorf("empty string should return empty, got: %q", result)
	}
}

func TestSafeLogString_AnthropicKeyBeforeOpenAI(t *testing.T) {
	// Anthropic keys start with "sk-" just like OpenAI keys.
	// The Anthropic pattern must match first to get the correct label.
	input := "key: sk-ant-api03-abcdefghijklmnopqrstuvwxyz123456"
	result := SafeLogString(input)

	if strings.Contains(result, "[REDACTED:openai_key]") {
		t.Errorf("Anthropic key was redacted as OpenAI key: %s", result)
	}
	if !strings.Contains(result, "[REDACTED:anthropic_key]") {
		t.Errorf("expected [REDACTED:anthropic_key] in result: %s", result)
	}
}
