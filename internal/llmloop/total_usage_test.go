// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package llmloop

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alibaba/open-code-review/internal/llm"
	"github.com/alibaba/open-code-review/internal/session"
)

func TestTotalOnlyUsageStopsInvestigation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				var input struct {
					Tools []llm.ToolDef `json:"tools"`
				}
				if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
					t.Error(err)
					writer.WriteHeader(http.StatusBadRequest)
					return
				}
				name, arguments, usage := "read_test_data", `{}`, 100
				if len(input.Tools) == 1 && input.Tools[0].Function.Name == "submit_test_result" {
					name, arguments, usage = "submit_test_result", `{"value":42}`, 7
				}
				call := map[string]any{"id": "call", "type": "function", "function": map[string]any{"name": name, "arguments": arguments}}
				if stream {
					writer.Header().Set("Content-Type", "text/event-stream")
					call["index"] = 0
					chunk, err := json.Marshal(map[string]any{"id": "response", "object": "chat.completion.chunk", "choices": []any{map[string]any{
						"index": 0, "delta": map[string]any{"tool_calls": []any{call}}, "finish_reason": "tool_calls",
					}}})
					if err != nil {
						t.Error(err)
						return
					}
					fmt.Fprintf(writer, "data: %s\n\ndata: {\"id\":\"response\",\"choices\":[],\"usage\":{\"total_tokens\":%d}}\n\ndata: [DONE]\n\n", chunk, usage)
					return
				}
				writer.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(writer).Encode(map[string]any{"id": "response", "object": "chat.completion", "model": "fake",
					"choices": []any{map[string]any{"index": 0, "finish_reason": "tool_calls", "message": map[string]any{"role": "assistant", "tool_calls": []any{call}}}},
					"usage":   map[string]int{"total_tokens": usage}}); err != nil {
					t.Error(err)
				}
			}))
			t.Cleanup(server.Close)
			client := llm.NewOpenAIClient(llm.ClientConfig{URL: server.URL + "/v1", Model: "fake", ExtraBody: map[string]any{"stream": stream}, Timeout: 3 * time.Second})
			history := session.New(t.TempDir(), "main", "fake", session.SessionOptions{})
			t.Cleanup(func() { _ = history.Finalize() })
			task := testTask()
			task.MaxRounds = 4
			runner := NewRunner(Deps{LLMClient: client, Model: "fake", Session: history, MaxTokensBudget: 99}, task)
			result, err := runner.Run(t.Context(), taskMessages(), "data")
			runner.WaitBackground()
			if err != nil || !result.Completed || result.StopReason != StopTokenBudget || calls.Load() != 2 || runner.TotalTokensUsed() != 107 {
				t.Fatalf("total-only usage must allow only final submission: result=%+v calls=%d total=%d err=%v", result, calls.Load(), runner.TotalTokensUsed(), err)
			}
			if runner.TotalInputTokens() != 0 || runner.TotalOutputTokens() != 0 {
				t.Fatal("an absent input/output breakdown must not be invented")
			}
		})
	}
}

func TestRecordUsagePreservesAvailableTotal(t *testing.T) {
	for _, fixture := range []struct {
		name  string
		usage llm.UsageInfo
		total int64
	}{
		{"total only", llm.UsageInfo{TotalTokens: 100}, 100},
		{"partial breakdown", llm.UsageInfo{TotalTokens: 100, PromptTokens: 30}, 100},
		{"missing total", llm.UsageInfo{PromptTokens: 10, CompletionTokens: 20}, 30},
		{"small total", llm.UsageInfo{TotalTokens: 10, PromptTokens: 20, CompletionTokens: 30}, 50},
		{"complete breakdown", llm.UsageInfo{TotalTokens: 50, PromptTokens: 20, CompletionTokens: 30}, 50},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			runner := NewRunner(Deps{}, Task{})
			runner.RecordUsage(&fixture.usage)
			if runner.TotalTokensUsed() != fixture.total || runner.TotalInputTokens() != fixture.usage.PromptTokens || runner.TotalOutputTokens() != fixture.usage.CompletionTokens {
				t.Fatalf("reported usage lost: total=%d input=%d output=%d", runner.TotalTokensUsed(), runner.TotalInputTokens(), runner.TotalOutputTokens())
			}
		})
	}
}
