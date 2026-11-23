// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package llmloop

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alibaba/open-code-review/internal/llm"
	"github.com/alibaba/open-code-review/internal/session"
)

func TestContextBudgetIncludesToolDefinitions(t *testing.T) {
	task := testTask()
	task.MaxContextTokens = 1000
	task.MaxCompletionTokens = 150
	task.ToolDefs[0].Function.Description = strings.Repeat("schema guidance ", 200)
	execute := task.ExecuteTool
	task.ExecuteTool = func(ctx context.Context, key string, call llm.ToolCall, record *session.TaskRecord, thinking string) ToolOutcome {
		if call.Function.Name == "read_test_data" {
			return ToolOutcome{Content: strings.Repeat("evidence ", 120)}
		}
		return execute(ctx, key, call, record, thinking)
	}
	var compressionCalls atomic.Int64
	mainCalls := 0
	client := taskClient(func(_ context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
		if len(request.Tools) == 0 {
			compressionCalls.Add(1)
			content := "Evidence was read in earlier rounds."
			return &llm.ChatResponse{Choices: []llm.Choice{{Message: llm.ResponseMessage{Content: &content}}}}, nil
		}
		mainCalls++
		definitions, err := json.Marshal(request.Tools)
		if err != nil {
			t.Fatal(err)
		}
		tokens := CountMessagesTokens(request.Messages) + llm.CountTokens(string(definitions))
		if tokens >= PromptTokenLimit(task.MaxContextTokens) {
			t.Errorf("request %d used %d tokens including tool schemas, limit %d", mainCalls, tokens, PromptTokenLimit(task.MaxContextTokens))
		}
		if mainCalls < 4 {
			return testTaskResponse("read_test_data", `{}`), nil
		}
		return testTaskResponse("submit_test_result", `{"value":42}`), nil
	})
	history := session.New(t.TempDir(), "main", "fake", session.SessionOptions{})
	t.Cleanup(func() { _ = history.Finalize() })
	runner := NewRunner(Deps{LLMClient: client, Session: history}, task)
	result, err := runner.Run(t.Context(), taskMessages(), "data")
	runner.WaitBackground()
	if err != nil || !result.Completed || mainCalls != 4 || compressionCalls.Load() == 0 {
		t.Fatalf("expected compression to preserve a fitting continuation: result=%+v main=%d compression=%d err=%v", result, mainCalls, compressionCalls.Load(), err)
	}
}

func TestCompressionReservesFixedToolDefinitions(t *testing.T) {
	task := testTask()
	task.MaxContextTokens = 1000
	task.ToolDefs[0].Function.Description = strings.Repeat("schema guidance ", 300)
	messages := []llm.Message{
		llm.NewTextMessage("system", "Analyze the source."),
		llm.NewTextMessage("user", strings.Repeat("requirements ", 250)),
		llm.NewTextMessage("assistant", "Read source."),
		llm.NewToolResultMessage("read", strings.Repeat("evidence ", 400)),
	}
	calls := 0
	client := taskClient(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
		calls++
		summary := "Earlier evidence."
		return &llm.ChatResponse{Choices: []llm.Choice{{Message: llm.ResponseMessage{Content: &summary}}}}, nil
	})
	history := session.New(t.TempDir(), "main", "fake", session.SessionOptions{})
	t.Cleanup(func() { _ = history.Finalize() })
	runner := NewRunner(Deps{LLMClient: client, Session: history}, task)
	got, err := runner.runCompression(t.Context(), messages, "data")
	if err == nil || calls != 0 || !reflect.DeepEqual(got, messages) {
		t.Fatalf("fixed tool schemas must reject an impossible summary without losing history: calls=%d err=%v", calls, err)
	}
}
