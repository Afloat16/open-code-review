// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package scan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alibaba/open-code-review/internal/config/template"
	"github.com/alibaba/open-code-review/internal/llm"
	"github.com/alibaba/open-code-review/internal/model"
	"github.com/alibaba/open-code-review/internal/session"
	"github.com/alibaba/open-code-review/internal/stdout"
	"github.com/alibaba/open-code-review/internal/tool"
)

func preflightTokenFiles(tb testing.TB) string {
	tb.Helper()
	dir := tb.TempDir()
	for file := range 4 {
		var content strings.Builder
		content.WriteString("package fixture\n\n")
		for line := range 300 {
			fmt.Fprintf(&content, "func function%d_%d() int { return %d }\n", file, line, line)
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%d.go", file)), []byte(content.String()), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	return dir
}

func runTokenPreflight(tb testing.TB, repo string) {
	tb.Helper()
	client := &fakeBudgetClient{}
	sh := &session.SessionHistory{
		SessionID: "scan-token-preflight", RepoDir: repo, Model: "fake",
		ReviewMode: session.ReviewModeFullScan, StartTime: time.Now(),
		FileSessions: make(map[string]*session.FileSession),
	}
	a := NewAgent(Args{
		RepoDir: repo, LLMClient: client, Model: "fake", Session: sh,
		Tools: tool.NewRegistry(), CommentCollector: tool.NewCommentCollector(),
		MaxTokensBudget: 1, MaxConcurrency: 1, Template: budgetTestTemplate(),
	})
	if _, err := a.Run(tb.Context()); err != nil {
		tb.Fatal(err)
	}
	if got := atomic.LoadInt64(&client.calls); got != 0 {
		tb.Fatalf("budget preflight made %d LLM calls", got)
	}
	if !a.BudgetExceeded() || len(a.items) != 4 {
		tb.Fatalf("preflight result: budget exceeded = %v, selected = %d", a.BudgetExceeded(), len(a.items))
	}
}

func TestRunTokenPreflight(t *testing.T) {
	runTokenPreflight(t, preflightTokenFiles(t))
}

func BenchmarkRunTokenPreflight(b *testing.B) {
	defer stdout.Quiet()()
	repo := preflightTokenFiles(b)
	llm.CountTokens("package fixture")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		runTokenPreflight(b, repo)
	}
}

func TestSelectionTokenCountsPreserveEstimates(t *testing.T) {
	items := []model.ScanItem{
		{Path: "first.go", Content: "package fixture\nfunc one() int { return 1 }\n"},
		{Path: "second.go", Content: "package fixture\n"},
		{Path: "empty.go"},
		{Path: "binary.go", IsBinary: true, Content: "binary"},
	}
	for _, maxTokens := range []int{0, 100000} {
		t.Run(fmt.Sprint(maxTokens), func(t *testing.T) {
			a := &Agent{args: Args{Template: template.ScanTemplate{MaxTokens: maxTokens}}}
			decisions := a.selectScanItems(items)
			a.reuseSelectionTokenCounts(decisions)
			kept := selectedScanItems(decisions)
			for _, enabled := range []bool{false, true} {
				plan := func(model.ScanItem) bool { return enabled }
				want := estimateCost(kept, plan, enabled, enabled, llm.CountTokens)
				if got := estimateCost(kept, plan, enabled, enabled, a.countFileTokens); got != want {
					t.Fatalf("aggregate estimate = %+v, want %+v", got, want)
				}
				for _, item := range kept {
					if got, want := estimateFileTokens(item, plan, a.countFileTokens), estimateFileTokens(item, plan, llm.CountTokens); got != want {
						t.Fatalf("file %s estimate = %d, want %d", item.Path, got, want)
					}
				}
			}
			changed := items[0]
			changed.Content = strings.Repeat("different source text ", 100)
			if got, want := a.countFileTokens(changed.Content), llm.CountTokens(changed.Content); got != want {
				t.Fatalf("new contents for the same path count = %d, want %d", got, want)
			}
			a.reuseSelectionTokenCounts(a.selectScanItems([]model.ScanItem{changed}))
			if _, retained := a.fileTokenCounts[items[0].Content]; retained {
				t.Fatal("a later selection retained the previous source text")
			}
		})
	}
}
