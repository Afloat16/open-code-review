// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

func TestAnthropicCacheBreakpointPreservesRequestOwnership(t *testing.T) {
	native := anthropic.NewAssistantMessage(
		anthropic.NewThinkingBlock("retained signature", "retained reasoning"),
		anthropic.NewToolUseBlock("call-file", map[string]any{"file_path": "main.go"}, "file_read"),
	)
	assistant := NewToolCallMessage("", nil, NativeTurn{Family: "anthropic-messages", Payload: native}, "")
	before, err := json.Marshal(native)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		name     string
		messages []Message
		kind     string
	}{
		{"plain user", []Message{NewTextMessage("user", "inspect the change")}, "text"},
		{"plain assistant", []Message{NewTextMessage("assistant", "continue inspecting")}, "text"},
		{"native replay", []Message{assistant}, "tool_use"},
		{"tool result after native replay", []Message{assistant, NewToolResultMessage("call-file", "1|return value")}, "tool_result"},
		{"two tool results", []Message{assistant, NewToolResultMessage("first", "first result"), NewToolResultMessage("second", "second result")}, "tool_result"},
		{"content blocks", []Message{{Role: "user", Content: []ContentBlock{{Type: "text", Text: "inspect"}, {Type: "tool_result", ToolUseID: "call-file", Content: []ContentBlock{{Type: "text", Text: "result"}}}}}}, "tool_result"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			client := &AnthropicClient{}
			request := ChatRequest{Messages: fixture.messages}
			var workers sync.WaitGroup
			for range 8 {
				workers.Go(func() {
					params, err := client.buildAnthropicParams("claude-fixture", request)
					if err != nil {
						t.Error(err)
						return
					}
					data, err := json.Marshal(params.Messages)
					if err != nil {
						t.Error(err)
						return
					}
					var messages []struct {
						Content []struct {
							Type         string `json:"type"`
							CacheControl struct {
								Type string `json:"type"`
							} `json:"cache_control"`
						} `json:"content"`
					}
					if err := json.Unmarshal(data, &messages); err != nil {
						t.Error(err)
						return
					}
					for messageIndex, message := range messages {
						for blockIndex, block := range message.Content {
							last := messageIndex == len(messages)-1 && blockIndex == len(message.Content)-1
							if last && (block.Type != fixture.kind || block.CacheControl.Type != "ephemeral") {
								t.Errorf("last block = %+v; expected cached %s", block, fixture.kind)
							}
							if !last && block.CacheControl.Type != "" {
								t.Error("cache breakpoint was added to earlier history")
							}
						}
					}
				})
			}
			workers.Wait()
		})
	}
	after, err := json.Marshal(native)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("preparing cache breakpoints mutated the native replay payload")
	}
}

func anthropicCacheToolResult(content string) ChatRequest {
	return ChatRequest{Messages: []Message{
		NewTextMessage("system", "Inspect the selected code."),
		NewTextMessage("user", "Review main.go."),
		NewToolCallMessage("", []ToolCall{{ID: "call-file", Type: "function", Function: FunctionCall{Name: "file_read", Arguments: `{"file_path":"main.go"}`}}}, NativeTurn{}, ""),
		NewToolResultMessage("call-file", content),
	}}
}

func anthropicCachePage() string {
	var page strings.Builder
	line := strings.Repeat("inspect(value); ", 32)
	for index := 1; index <= 500; index++ {
		fmt.Fprintf(&page, "%d|%s\n", index, line)
	}
	return page.String()
}

func TestAnthropicCacheToolResultHTTP(t *testing.T) {
	content := anthropicCachePage()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content []struct {
					Type         string                        `json:"type"`
					ToolUseID    string                        `json:"tool_use_id"`
					Content      []struct{ Type, Text string } `json:"content"`
					CacheControl struct {
						Type string `json:"type"`
					} `json:"cache_control"`
				} `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		if len(body.Messages) != 3 || len(body.Messages[2].Content) != 1 {
			t.Error("unexpected request message layout")
		} else {
			last := body.Messages[2].Content[0]
			if body.Messages[2].Role != "user" || last.Type != "tool_result" || last.ToolUseID != "call-file" || len(last.Content) != 1 || last.Content[0].Type != "text" || last.Content[0].Text != content || last.CacheControl.Type != "ephemeral" {
				t.Error("tool result or final cache breakpoint changed on the wire")
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"msg-cache","type":"message","role":"assistant","model":"claude-fixture","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":12,"output_tokens":1}}`)
	}))
	defer server.Close()
	client := NewAnthropicClient(ClientConfig{URL: server.URL, APIKey: "fixture", Model: "claude-fixture"})
	response, err := client.CompletionsWithCtx(context.Background(), anthropicCacheToolResult(content))
	if err != nil {
		t.Fatal(err)
	}
	if response.Content() != "done" {
		t.Fatal("response changed")
	}
}

func BenchmarkAnthropicCacheToolResult(b *testing.B) {
	client := &AnthropicClient{}
	request := anthropicCacheToolResult(anthropicCachePage())
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		params, err := client.buildAnthropicParams("claude-fixture", request)
		if err != nil {
			b.Fatal(err)
		}
		if len(params.Messages) != 3 {
			b.Fatal("request messages changed")
		}
	}
}
