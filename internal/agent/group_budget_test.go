// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package agent

import (
	"sync/atomic"
	"testing"

	"github.com/alibaba/open-code-review/internal/llm"
	"github.com/alibaba/open-code-review/internal/session"
	"github.com/alibaba/open-code-review/internal/tool"
)

func TestDispatchSubtasks_SharedGroupBudget(t *testing.T) {
	setTestHome(t, t.TempDir())
	tpl := budgetAgentTestTemplate()
	tpl.GroupingMinFiles = 4
	tpl.GroupingBundleLineThreshold = 200
	fake := &fakeBudgetAgentClient{perCallTokens: 1000}
	a := New(Args{
		LLMClient: fake, Model: "fake", Template: tpl,
		CommentCollector: tool.NewCommentCollector(), Tools: tool.NewRegistry(),
		MaxConcurrency: 1, MaxTokensBudget: 30_000,
		MainToolDefs: []llm.ToolDef{
			{Type: "function", Function: llm.FunctionDef{Name: "task_done", Description: "done"}},
		},
	})
	t.Cleanup(func() { _ = a.Session().Finalize() })
	a.diffs = makeBudgetDiffs(2)
	a.currentDate = "2026-11-23 13:00"
	a.args.Tools.Freeze()
	if _, err := a.dispatchSubtasks(t.Context()); err != nil {
		t.Fatalf("dispatchSubtasks: %v", err)
	}
	if got := atomic.LoadInt64(&fake.calls); got != 1 {
		t.Fatalf("review calls = %d, want one shared review for both files", got)
	}
	if a.BudgetExceeded() {
		t.Fatal("a fitting shared review must not exhaust the budget before dispatch")
	}
	if err := a.finalizeManifest(); err != nil {
		t.Fatal(err)
	}
	manifest := a.RunManifest()
	if manifest.TerminalState != session.StateComplete || len(manifest.Coverage.Completed) != 2 {
		t.Fatalf("expected both files completed, got %+v", manifest)
	}
}
