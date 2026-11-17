// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package agent

import (
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/config/template"
	"github.com/alibaba/open-code-review/internal/llm"
	"github.com/alibaba/open-code-review/internal/model"
	"github.com/alibaba/open-code-review/internal/session"
	"github.com/alibaba/open-code-review/internal/tool"
)

func TestReviewRequestsPreserveLiteralTemplateInputs(t *testing.T) {
	for _, phase := range []string{"main", "plan"} {
		t.Run(phase, func(t *testing.T) {
			client := &filterRequestCaptureClient{response: agentTaskDoneResponse()}
			conversation := template.LlmConversation{Messages: []template.ChatMessage{{
				Role: "user", Content: "<source>{{diffs}}</source>\n<background>{{requirement_background}}</background>",
			}}}
			background := "Check literal {{plan_guidance}} and {{plan_tools}} tokens."
			a := New(Args{
				LLMClient: client, Model: "test", Background: background,
				Session: session.New(t.TempDir(), "main", "test", session.SessionOptions{}),
				Tools:   tool.NewRegistry(), CommentCollector: tool.NewCommentCollector(),
				Template:     template.Template{MaxTokens: 100000, MaxToolRequestTimes: 5, MainTask: conversation},
				MainToolDefs: []llm.ToolDef{{Type: "function", Function: llm.FunctionDef{Name: "task_done"}}},
			})
			group := FileGroup{Label: "template.go", Diffs: []model.Diff{{
				NewPath: "template.go", Insertions: 1,
				Diff: `+const text = "{{requirement_background}} {{plan_guidance}} {{confirmed_comments}} {{plan_tools}}"`,
			}}}
			source := buildConcatenatedDiffs(group.Diffs)
			if phase == "plan" {
				a.args.Template.PlanTask = &conversation
				if _, err := a.executeGroupPlanPhase(t.Context(), group, source, "", ""); err != nil {
					t.Fatal(err)
				}
			} else {
				completed, stop, err := a.executeGroupSubtask(t.Context(), group)
				if err != nil || !completed || stop != nil {
					t.Fatalf("main review: completed=%v, stop=%v, err=%v", completed, stop, err)
				}
			}
			if client.calls != 1 || len(client.request.Messages) != 1 {
				t.Fatalf("expected one actual request, got %+v", client)
			}
			want := "<source>" + source + "</source>\n<background>" + background + "</background>"
			if got := client.request.Messages[0].ExtractText(); got != want {
				t.Fatalf("request changed literal input:\ngot:  %q\nwant: %q", got, want)
			}
		})
	}
}

func TestReviewMainOptionalSectionsOnlyAffectTemplate(t *testing.T) {
	for _, withGuidance := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "populated"}[withGuidance], func(t *testing.T) {
			rule := "### Review Plan\n{{plan_guidance}}\n\nLiteral {{diffs}} and {{confirmed_comments}}."
			plan, confirmed := "", ""
			if withGuidance {
				plan, confirmed = "Keep {{confirmed_comments}} literal.", "Keep {{system_rule}} literal."
			}
			a := New(Args{Template: template.Template{MainTask: template.LlmConversation{Messages: []template.ChatMessage{{
				Role: "user", Content: "<rule>{{system_rule}}</rule>\n### Review Plan\n{{plan_guidance}}\n\n<confirmed>{{confirmed_comments}}</confirmed>",
			}}}}})
			got := a.buildMainTaskMessages(rule, "", "diff evidence", plan, confirmed)[0].ExtractText()
			want := "<rule>" + rule + "</rule>\n"
			if withGuidance {
				want += "### Review Plan\n" + plan + "\n\n"
			}
			want += "<confirmed>" + confirmed + "</confirmed>"
			if got != want || !strings.Contains(got, rule) {
				t.Fatalf("optional sections changed inserted content:\ngot:  %q\nwant: %q", got, want)
			}
		})
	}
}
