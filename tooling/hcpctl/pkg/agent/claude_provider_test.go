// Copyright 2025 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

func TestToolDefinitionToAnthropicTool(t *testing.T) {
	tests := []struct {
		name        string
		td          ToolDefinition
		wantName    string
		wantDesc    string
		wantProps   bool
		wantReq     []string
		wantErr     bool
		errContains string
	}{
		{
			name: "basic tool with properties and required fields",
			td: ToolDefinition{
				Name:        "kusto_query",
				Description: "Execute a KQL query.",
				ParamSchema: json.RawMessage(`{
					"type": "object",
					"properties": {"kql": {"type": "string", "description": "The KQL query."}},
					"required": ["kql"]
				}`),
			},
			wantName: "kusto_query",
			wantDesc: "Execute a KQL query.",
			wantProps: true,
			wantReq:  []string{"kql"},
		},
		{
			name: "tool with empty param schema",
			td: ToolDefinition{
				Name:        "no_params",
				Description: "A tool with no parameters.",
			},
			wantName: "no_params",
			wantDesc: "A tool with no parameters.",
		},
		{
			name: "tool with invalid JSON schema",
			td: ToolDefinition{
				Name:        "bad_schema",
				Description: "Should fail.",
				ParamSchema: json.RawMessage(`{not valid json}`),
			},
			wantErr:     true,
			errContains: "parsing parameter schema",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toolDefinitionToAnthropicTool(tt.td)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got.OfTool == nil {
				t.Fatal("expected OfTool to be non-nil")
			}
			if got.OfTool.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", got.OfTool.Name, tt.wantName)
			}
			if got.OfTool.Description.Value != tt.wantDesc {
				t.Errorf("Description = %q, want %q", got.OfTool.Description.Value, tt.wantDesc)
			}
			if tt.wantProps {
				props, ok := got.OfTool.InputSchema.Properties.(map[string]any)
				if !ok || len(props) == 0 {
					t.Error("expected non-empty Properties map")
				}
			}
			if len(tt.wantReq) > 0 {
				if len(got.OfTool.InputSchema.Required) != len(tt.wantReq) {
					t.Errorf("Required length = %d, want %d", len(got.OfTool.InputSchema.Required), len(tt.wantReq))
				}
				for i, r := range tt.wantReq {
					if i < len(got.OfTool.InputSchema.Required) && got.OfTool.InputSchema.Required[i] != r {
						t.Errorf("Required[%d] = %q, want %q", i, got.OfTool.InputSchema.Required[i], r)
					}
				}
			}
		})
	}
}

func TestExtractTextFromResponse(t *testing.T) {
	t.Run("single text block", func(t *testing.T) {
		resp := &anthropic.Message{
			Content: []anthropic.ContentBlockUnion{
				textContentBlock(t, "Hello, world!"),
			},
		}
		got := extractTextFromResponse(resp)
		if got != "Hello, world!" {
			t.Errorf("got %q, want %q", got, "Hello, world!")
		}
	})

	t.Run("multiple text blocks concatenated", func(t *testing.T) {
		resp := &anthropic.Message{
			Content: []anthropic.ContentBlockUnion{
				textContentBlock(t, "Part 1"),
				textContentBlock(t, " Part 2"),
			},
		}
		got := extractTextFromResponse(resp)
		if got != "Part 1 Part 2" {
			t.Errorf("got %q, want %q", got, "Part 1 Part 2")
		}
	})

	t.Run("empty content", func(t *testing.T) {
		resp := &anthropic.Message{
			Content: []anthropic.ContentBlockUnion{},
		}
		got := extractTextFromResponse(resp)
		if got != "" {
			t.Errorf("got %q, want empty string", got)
		}
	})

	t.Run("mixed blocks — only text extracted", func(t *testing.T) {
		resp := &anthropic.Message{
			Content: []anthropic.ContentBlockUnion{
				textContentBlock(t, "analysis result"),
				toolUseContentBlock(t, "tool_123", "kusto_query", json.RawMessage(`{"kql":"traces"}`)),
			},
		}
		got := extractTextFromResponse(resp)
		if got != "analysis result" {
			t.Errorf("got %q, want %q", got, "analysis result")
		}
	})
}

func TestBuildToolHandlerMap(t *testing.T) {
	called := false
	tools := []ToolDefinition{
		{
			Name:        "test_tool",
			Description: "A test tool.",
			Handler: func(_ context.Context, _ json.RawMessage) (string, error) {
				called = true
				return "ok", nil
			},
		},
		{
			Name:        "another_tool",
			Description: "Another test tool.",
			Handler: func(_ context.Context, _ json.RawMessage) (string, error) {
				return "also ok", nil
			},
		},
	}

	m := buildToolHandlerMap(tools)

	if len(m) != 2 {
		t.Fatalf("expected 2 handlers, got %d", len(m))
	}

	// Verify known tool dispatches correctly.
	handler, exists := m["test_tool"]
	if !exists {
		t.Fatal("expected test_tool handler to exist")
	}
	result, err := handler(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok" {
		t.Errorf("result = %q, want %q", result, "ok")
	}
	if !called {
		t.Error("expected handler to be called")
	}

	// Verify unknown tool is absent.
	if _, exists := m["nonexistent"]; exists {
		t.Error("expected nonexistent tool to be absent from handler map")
	}
}

func TestNewClaudeProvider_EmptyAPIKeyFile(t *testing.T) {
	// Create a temporary file with only whitespace.
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "empty_key")
	if err := os.WriteFile(keyFile, []byte("   \n"), 0o600); err != nil {
		t.Fatalf("failed to write test key file: %v", err)
	}

	_, err := NewClaudeProvider(context.Background(), &ClaudeConfig{
		APIKeyFile: keyFile,
		Backend:    ClaudeBackendAPI,
	})
	if err == nil {
		t.Fatal("expected error for empty API key file, got nil")
	}
	if !contains(err.Error(), "empty") {
		t.Errorf("error %q does not mention empty key", err.Error())
	}
}

func TestNewClaudeProvider_MissingAPIKeyFile(t *testing.T) {
	_, err := NewClaudeProvider(context.Background(), &ClaudeConfig{
		APIKeyFile: "/nonexistent/path/key",
		Backend:    ClaudeBackendAPI,
	})
	if err == nil {
		t.Fatal("expected error for missing key file, got nil")
	}
	if !contains(err.Error(), "reading Anthropic API key") {
		t.Errorf("error %q does not mention reading key file", err.Error())
	}
}

func TestNewClaudeProvider_VertexNoAPIKey(t *testing.T) {
	// Vertex backend should not require an API key. It will fail at
	// runtime when ADC is unavailable, but NewClaudeProvider itself
	// should succeed without an API key file.
	provider, err := NewClaudeProvider(context.Background(), &ClaudeConfig{
		Backend:       ClaudeBackendVertex,
		VertexProject: "test-project",
		VertexRegion:  "us-east5",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := provider.Stop(); err != nil {
		t.Fatalf("unexpected error on Stop: %v", err)
	}
}

// unmarshalContentBlock creates a ContentBlockUnion by unmarshaling JSON.
// This ensures the internal fields (used by AsAny) are correctly populated.
func unmarshalContentBlock(t *testing.T, jsonStr string) anthropic.ContentBlockUnion {
	t.Helper()
	var block anthropic.ContentBlockUnion
	if err := json.Unmarshal([]byte(jsonStr), &block); err != nil {
		t.Fatalf("failed to unmarshal content block: %v", err)
	}
	return block
}

// textContentBlock creates a ContentBlockUnion containing a TextBlock.
func textContentBlock(t *testing.T, text string) anthropic.ContentBlockUnion {
	t.Helper()
	data, err := json.Marshal(map[string]string{"type": "text", "text": text})
	if err != nil {
		t.Fatalf("failed to marshal text block: %v", err)
	}
	return unmarshalContentBlock(t, string(data))
}

// toolUseContentBlock creates a ContentBlockUnion containing a ToolUseBlock.
func toolUseContentBlock(t *testing.T, id, name string, input json.RawMessage) anthropic.ContentBlockUnion {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"type":  "tool_use",
		"id":    id,
		"name":  name,
		"input": input,
	})
	if err != nil {
		t.Fatalf("failed to marshal tool_use block: %v", err)
	}
	return unmarshalContentBlock(t, string(data))
}

// contains checks whether s contains substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
