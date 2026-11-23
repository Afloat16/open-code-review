// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package agent

import (
	"fmt"
	"os"
	"os/exec"
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

func preflightTokenRepo(tb testing.TB) string {
	tb.Helper()
	dir := tb.TempDir()
	run := func(args ...string) {
		tb.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			tb.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.name", "Token Test")
	run("config", "user.email", "tokens@example.com")
	run("commit", "--allow-empty", "-m", "base")
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
	run("add", ".")
	run("commit", "-m", "source")
	return dir
}

func runTokenPreflight(tb testing.TB, repo string) {
	tb.Helper()
	client := &fakeBudgetAgentClient{}
	sh := &session.SessionHistory{
		SessionID: "token-preflight", RepoDir: repo, Model: "fake",
		ReviewMode: session.ReviewModeRange, StartTime: time.Now(),
		FileSessions: make(map[string]*session.FileSession),
	}
	a := New(Args{
		RepoDir: repo, From: "HEAD^", To: "HEAD", ReviewMode: session.ReviewModeRange,
		LLMClient: client, Model: "fake", Session: sh,
		Tools: tool.NewRegistry(), CommentCollector: tool.NewCommentCollector(),
		MaxTokensBudget: 1, MaxConcurrency: 1,
		Template: template.Template{
			MaxTokens: 100000, GroupingMinFiles: 20, GroupingBundleLineThreshold: 10000,
		},
	})
	if _, err := a.Run(tb.Context()); err != nil {
		tb.Fatal(err)
	}
	if got := atomic.LoadInt64(&client.calls); got != 0 {
		tb.Fatalf("budget preflight made %d LLM calls", got)
	}
	if !a.BudgetExceeded() || len(a.diffs) != 4 {
		tb.Fatalf("preflight result: budget exceeded = %v, selected = %d", a.BudgetExceeded(), len(a.diffs))
	}
}

func TestRunTokenPreflight(t *testing.T) {
	runTokenPreflight(t, preflightTokenRepo(t))
}

func BenchmarkRunTokenPreflight(b *testing.B) {
	defer stdout.Quiet()()
	repo := preflightTokenRepo(b)
	llm.CountTokens("package fixture")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		runTokenPreflight(b, repo)
	}
}

func TestSelectionTokenCountsPreserveEstimates(t *testing.T) {
	diffs := []model.Diff{
		{NewPath: "first.go", Diff: "+package fixture\n+func one() int { return 1 }\n"},
		{NewPath: "second.go", Diff: "+package fixture\n"},
		{NewPath: "empty.go"},
		{OldPath: "deleted.go", IsDeleted: true},
		{NewPath: "binary.go", IsBinary: true, Diff: "binary"},
	}
	for _, maxTokens := range []int{0, 100000} {
		t.Run(fmt.Sprint(maxTokens), func(t *testing.T) {
			a := &Agent{args: Args{Template: template.Template{MaxTokens: maxTokens}}}
			decisions := a.selectFiles(diffs)
			a.reuseSelectionTokenCounts(decisions)
			kept, _ := summarizeSelection(decisions)
			want := estimateDiffCost(kept, llm.CountTokens)
			if got := estimateDiffCost(kept, a.countDiffTokens); got != want {
				t.Fatalf("aggregate estimate = %+v, want %+v", got, want)
			}
			for _, d := range kept {
				if got, want := estimateDiffFileTokens(d, a.countDiffTokens), estimateDiffFileTokens(d, llm.CountTokens); got != want {
					t.Fatalf("file %s estimate = %d, want %d", d.NewPath, got, want)
				}
			}
			changed := diffs[0]
			changed.Diff = "+" + strings.Repeat("different source text ", 100)
			if got, want := a.countDiffTokens(changed.Diff), llm.CountTokens(changed.Diff); got != want {
				t.Fatalf("new contents for the same path count = %d, want %d", got, want)
			}
			a.reuseSelectionTokenCounts(a.selectFiles([]model.Diff{changed}))
			if _, retained := a.diffTokenCounts[diffs[0].Diff]; retained {
				t.Fatal("a later selection retained the previous source text")
			}
		})
	}
}
