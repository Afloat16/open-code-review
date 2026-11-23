// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package reviewtask

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/llm"
	"github.com/alibaba/open-code-review/internal/model"
	"github.com/alibaba/open-code-review/internal/stdout"
	"github.com/alibaba/open-code-review/internal/tool"
)

func resolutionBatchFixture(tb testing.TB, lineCount, commentCount int) (*model.Diff, llm.ToolCall) {
	tb.Helper()
	var patch strings.Builder
	fmt.Fprintf(&patch, "@@ -0,0 +1,%d @@\n", lineCount)
	comments := make([]map[string]string, 0, commentCount)
	for i := range lineCount {
		line := fmt.Sprintf("var symbol%04d = %d", i, i)
		fmt.Fprintf(&patch, "+%s\n", line)
		if i >= lineCount-commentCount {
			comments = append(comments, map[string]string{
				"path": "symbols.go", "content": "Inspect " + line, "existing_code": line,
			})
		}
	}
	body, err := json.Marshal(map[string]any{"comments": comments})
	if err != nil {
		tb.Fatal(err)
	}
	return &model.Diff{NewPath: "symbols.go", Diff: patch.String()}, llm.ToolCall{
		Function: llm.FunctionCall{Name: tool.CodeComment.Name(), Arguments: string(body)},
	}
}

func resolutionBatchRunner(d *model.Diff, pool *CommentWorkerPool) (*Runner, *tool.CommentCollector) {
	collector := tool.NewCommentCollector()
	registry := tool.NewRegistry()
	registry.Register(&tool.CodeCommentProvider{Collector: collector})
	registry.Freeze()
	return NewRunner(Deps{
		Tools: registry, CommentCollector: collector, CommentWorkerPool: pool,
		DiffLookup: func(string) *model.Diff { return d },
	}), collector
}

func TestCodeCommentBatchResolution(t *testing.T) {
	for _, async := range []bool{false, true} {
		t.Run(fmt.Sprintf("async=%v", async), func(t *testing.T) {
			d, call := resolutionBatchFixture(t, 100, 20)
			var pool *CommentWorkerPool
			if async {
				pool = NewCommentWorkerPool(2)
			}
			runner, collector := resolutionBatchRunner(d, pool)
			result := runner.executeToolCall(t.Context(), d.NewPath, call, nil, "")
			if result.Data != tool.CommentSucceed {
				t.Fatalf("tool result = %q", result.Data)
			}
			comments := runner.CollectPendingComments()
			if len(comments) != 20 {
				t.Fatalf("comments = %d, want 20", len(comments))
			}
			for i, comment := range comments {
				if comment.StartLine != 81+i || comment.EndLine != 81+i {
					t.Errorf("comment %d resolved to %d..%d", i, comment.StartLine, comment.EndLine)
				}
			}
			collector.ReplaceSince(0, nil)
			d.Diff = "@@ -0,0 +1,103 @@\n+// first\n+// second\n+// third\n" + strings.SplitN(d.Diff, "\n", 2)[1]
			runner.executeToolCall(t.Context(), d.NewPath, call, nil, "")
			comments = runner.CollectPendingComments()
			if len(comments) != 20 || comments[0].StartLine != 84 {
				t.Fatalf("a later batch reused stale source locations: %+v", comments)
			}
		})
	}
}

func BenchmarkCodeCommentBatchResolution(b *testing.B) {
	defer stdout.Quiet()()
	d, call := resolutionBatchFixture(b, 5000, 20)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		runner, collector := resolutionBatchRunner(d, nil)
		if result := runner.executeToolCall(b.Context(), d.NewPath, call, nil, ""); result.Data != tool.CommentSucceed {
			b.Fatalf("tool result = %q", result.Data)
		}
		comments := collector.Comments()
		if len(comments) != 20 || comments[0].StartLine != 4981 || comments[19].StartLine != 5000 {
			b.Fatalf("incorrect resolved batch: %+v", comments)
		}
	}
}
