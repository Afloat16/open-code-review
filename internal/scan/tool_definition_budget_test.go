// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package scan

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alibaba/open-code-review/internal/llm"
	"github.com/alibaba/open-code-review/internal/session"
)

func TestRunIncludesToolDefinitionsInPromptBudget(t *testing.T) {
	repo := initTestRepo(t)
	writeFile(t, repo, "main.go", []byte("package main\n"))
	gitCommit(t, repo, "initial")
	tpl := budgetTestTemplate()
	tpl.MaxTokens = 1000
	client := &fakeBudgetClient{}
	agent := NewAgent(Args{
		RepoDir: repo, Template: tpl, LLMClient: client, MaxConcurrency: 1,
		SkipPlan: true, SkipDedup: true, SkipSummary: true,
		Session: session.New(repo, "main", "fake", session.SessionOptions{ReviewMode: session.ReviewModeFullScan}),
		MainToolDefs: []llm.ToolDef{{Type: "function", Function: llm.FunctionDef{
			Name: "task_done", Description: strings.Repeat("tool instructions ", 450),
		}}},
	})
	_, err := agent.Run(t.Context())
	if calls := atomic.LoadInt64(&client.calls); err == nil || calls != 0 {
		t.Fatalf("oversized tool definitions were sent: calls=%d err=%v", calls, err)
	}
}
