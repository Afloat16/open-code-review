// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package scan

import (
	"context"
	"testing"

	"github.com/alibaba/open-code-review/internal/config/template"
	"github.com/alibaba/open-code-review/internal/llm"
	"github.com/alibaba/open-code-review/internal/model"
	"github.com/alibaba/open-code-review/internal/session"
	"github.com/alibaba/open-code-review/internal/tool"
)

type literalTemplateClient struct {
	request llm.ChatRequest
	calls   int
}

func (c *literalTemplateClient) CompletionsWithCtx(_ context.Context, request llm.ChatRequest) (*llm.ChatResponse, error) {
	c.request = request
	c.calls++
	return scanTaskDoneResponse(), nil
}

func TestScanRequestsPreserveLiteralTemplateInputs(t *testing.T) {
	for _, phase := range []string{"main", "plan"} {
		t.Run(phase, func(t *testing.T) {
			client := &literalTemplateClient{}
			conversation := template.LlmConversation{Messages: []template.ChatMessage{{
				Role: "user", Content: "<path>{{current_file_path}}</path>\n<rule>{{system_rule}}</rule>\n<source>{{file_content}}</source>",
			}}}
			a := NewAgent(Args{
				Template:  template.ScanTemplate{MaxTokens: 100000, MaxToolRequestTimes: 5, MainTask: conversation},
				LLMClient: client, Model: "test", Background: "actual background",
				Session: session.New(t.TempDir(), "main", "test", session.SessionOptions{ReviewMode: session.ReviewModeFullScan}),
				Tools:   tool.NewRegistry(), CommentCollector: tool.NewCommentCollector(),
				MainToolDefs: []llm.ToolDef{{Type: "function", Function: llm.FunctionDef{Name: "task_done"}}},
			})
			item := model.ScanItem{
				Path: "{{system_rule}}-{{file_content}}.go", LineCount: 1,
				Content: `const text = "{{requirement_background}}"`,
			}
			rule := ""
			if phase == "plan" {
				rule = "Preserve {{file_content}} tokens."
				a.args.Template.PlanTask = &conversation
				a.maybeRunPlan(t.Context(), item, rule)
			} else if completed, reason, err := a.executeSubtask(t.Context(), item); err != nil || !completed {
				t.Fatalf("main scan: completed=%v, reason=%q, err=%v", completed, reason, err)
			}
			if client.calls != 1 || len(client.request.Messages) != 1 {
				t.Fatalf("expected one actual request, got %+v", client)
			}
			want := "<path>" + item.Path + "</path>\n<rule>" + rule + "</rule>\n<source>" + item.Content + "</source>"
			if got := client.request.Messages[0].ExtractText(); got != want {
				t.Fatalf("request changed literal input:\ngot:  %q\nwant: %q", got, want)
			}
		})
	}
}

func TestScanMainInsertedGuidanceIsLiteral(t *testing.T) {
	tpl := template.ScanTemplate{MainTask: template.LlmConversation{Messages: []template.ChatMessage{{
		Role: "user", Content: "{{plan_guidance}}\n{{system_rule}}\n{{file_content}}\n{{requirement_background}}",
	}}}}
	a := newAgentForTest(t, tpl)
	a.args.Background = "background {{plan_guidance}}"
	item := model.ScanItem{Content: "source {{requirement_background}}"}
	rule, guidance := "rule {{file_content}}", "plan {{system_rule}}"
	want := guidance + "\n" + rule + "\n" + item.Content + "\n" + a.args.Background
	if got := a.renderMessages(item, rule, guidance)[0].ExtractText(); got != want {
		t.Fatalf("inserted data was recursively expanded:\ngot:  %q\nwant: %q", got, want)
	}
}
