// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package llmloop

import (
	"context"
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/llm"
	"github.com/alibaba/open-code-review/internal/session"
)

func TestRetryWithoutToolsRespectsContextLimit(t *testing.T) {
	for _, compress := range []bool{false, true} {
		name := "stop_without_compression"
		if compress {
			name = "continue_after_compression"
		}
		t.Run(name, func(t *testing.T) {
			task := testTask()
			task.MaxContextTokens = 1000
			task.MaxCompletionTokens = 950
			if !compress {
				task.Compression.Messages = nil
			}
			requests := 0
			client := taskClient(func(_ context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
				if len(request.Tools) == 0 {
					content := "The previous response did not use a tool; use an evidence tool or submit."
					return &llm.ChatResponse{Choices: []llm.Choice{{Message: llm.ResponseMessage{Content: &content}}}}, nil
				}
				requests++
				if got := CountMessagesTokens(request.Messages); got >= PromptTokenLimit(task.MaxContextTokens) {
					t.Errorf("request %d sent %d tokens above the %d-token continuation limit", requests, got, PromptTokenLimit(task.MaxContextTokens))
				}
				if requests == 1 {
					content := strings.Repeat("investigate ", 900)
					return &llm.ChatResponse{Choices: []llm.Choice{{Message: llm.ResponseMessage{Content: &content}}}}, nil
				}
				return testTaskResponse("submit_test_result", `{"value":42}`), nil
			})
			history := session.New(t.TempDir(), "main", "fake", session.SessionOptions{})
			t.Cleanup(func() { _ = history.Finalize() })
			runner := NewRunner(Deps{LLMClient: client, Session: history}, task)
			result, err := runner.Run(t.Context(), taskMessages(), "data")
			runner.WaitBackground()
			if err != nil {
				t.Fatal(err)
			}
			if compress {
				if !result.Completed || requests != 2 || result.StopReason != StopNone {
					t.Fatalf("expected a compressed retry to finish: result=%+v requests=%d", result, requests)
				}
			} else if result.Completed || requests != 1 || result.StopReason != StopCompression {
				t.Fatalf("expected an oversized retry to stop: result=%+v requests=%d", result, requests)
			}
		})
	}
}
