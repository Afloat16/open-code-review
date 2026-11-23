// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package agent

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alibaba/open-code-review/internal/llm"
	"github.com/alibaba/open-code-review/internal/session"
	"github.com/alibaba/open-code-review/internal/tool"
)

func TestDispatchIncludesToolDefinitionsInPromptBudget(t *testing.T) {
	setTestHome(t, t.TempDir())
	tpl := budgetAgentTestTemplate()
	tpl.MaxTokens = 1000
	client := &fakeBudgetAgentClient{}
	agent := New(Args{
		LLMClient: client, Model: "fake", Template: tpl,
		CommentCollector: tool.NewCommentCollector(), Tools: tool.NewRegistry(), MaxConcurrency: 1,
		MainToolDefs: []llm.ToolDef{{Type: "function", Function: llm.FunctionDef{
			Name: "task_done", Description: strings.Repeat("tool instructions ", 450),
		}}},
	})
	t.Cleanup(func() { _ = agent.Session().Finalize() })
	agent.diffs = makeBudgetDiffs(1)
	agent.args.Tools.Freeze()
	if _, err := agent.dispatchSubtasks(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := agent.finalizeManifest(); err != nil {
		t.Fatal(err)
	}
	manifest := agent.RunManifest()
	if calls := atomic.LoadInt64(&client.calls); calls != 0 || manifest.TerminalState == session.StateComplete || len(manifest.Coverage.Completed) != 0 {
		t.Fatalf("oversized tool definitions were sent: calls=%d manifest=%+v", calls, manifest)
	}
}
