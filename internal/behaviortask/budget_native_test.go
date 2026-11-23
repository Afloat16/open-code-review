// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package behaviortask

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/llm"
	anthropic "github.com/anthropics/anthropic-sdk-go"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

func TestBudgetIncludesOpaqueReasoningWhenUsageIsMissing(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		native llm.NativeTurn
	}{
		{"redacted thinking", llm.NativeTurn{Family: "anthropic-messages", Payload: anthropic.MessageParam{
			Role: anthropic.MessageParamRoleAssistant,
			Content: []anthropic.ContentBlockParamUnion{{OfRedactedThinking: &anthropic.RedactedThinkingBlockParam{
				Data: strings.Repeat("opaque", 1500),
			}}},
		}}},
		{"encrypted reasoning", llm.NativeTurn{Family: "openai-responses", Payload: []responses.ResponseInputItemUnionParam{{OfReasoning: &responses.ResponseReasoningItemParam{
			ID: "reasoning-1", EncryptedContent: openai.String(strings.Repeat("opaque", 1500)),
		}}}}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			budget := newBudget(1000)
			calls := 0
			client := budget.wrap(fakeClient(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
				calls++
				content := "Continue investigating."
				return &llm.ChatResponse{Choices: []llm.Choice{{Message: llm.ResponseMessage{
					Content: &content, Native: fixture.native,
				}}}}, nil
			}))
			response, err := client.CompletionsWithCtx(t.Context(), llm.ChatRequest{})
			if err != nil {
				t.Fatal(err)
			}
			if response.Usage == nil || response.Usage.CompletionTokens < 1000 {
				t.Errorf("opaque output was omitted from fallback usage: %+v", response.Usage)
			}
			if _, err := client.CompletionsWithCtx(t.Context(), llm.ChatRequest{}); !errors.Is(err, llm.ErrTokenBudget) || calls != 1 {
				t.Fatalf("opaque output did not stop the next paid request: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestBudgetDoesNotAddReasoningProjectionTwice(t *testing.T) {
	content := "Answer."
	reasoning := strings.Repeat("consider this path ", 100)
	native := llm.NativeTurn{Family: "openai-chat-completions", Payload: llm.ReasoningPayload(reasoning)}
	response := &llm.ChatResponse{Choices: []llm.Choice{{Message: llm.ResponseMessage{
		Content: &content, ReasoningContent: reasoning, Native: native,
	}}}}
	budget := newBudget(0)
	client := budget.wrap(fakeClient(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) { return response, nil }))
	if _, err := client.CompletionsWithCtx(t.Context(), llm.ChatRequest{}); err != nil {
		t.Fatal(err)
	}
	visibleTokens := int64(llm.CountTokens(content))
	projectionTokens := int64(llm.CountTokens(reasoning))
	replayTokens := int64(native.EstimatedTokens())
	if output := budget.snapshot().OutputTokens; output < visibleTokens+projectionTokens || output >= visibleTokens+projectionTokens+replayTokens {
		t.Fatalf("reasoning must be represented once, got %d output tokens", output)
	}
}

func TestBudgetKeepsReportedUsageWithNativeReasoning(t *testing.T) {
	reported := &llm.UsageInfo{PromptTokens: 7, CompletionTokens: 11, TotalTokens: 18}
	response := &llm.ChatResponse{Usage: reported, Choices: []llm.Choice{{Message: llm.ResponseMessage{
		Native: llm.NativeTurn{Family: "openai-chat-completions", Payload: llm.ReasoningPayload(strings.Repeat("reasoning ", 1000))},
	}}}}
	budget := newBudget(100)
	client := budget.wrap(fakeClient(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) { return response, nil }))
	if _, err := client.CompletionsWithCtx(t.Context(), llm.ChatRequest{}); err != nil {
		t.Fatal(err)
	}
	if response.Usage != reported || budget.snapshot().TotalTokens != 18 {
		t.Fatalf("native estimates replaced provider usage: response=%+v budget=%+v", response.Usage, budget.snapshot())
	}
}
