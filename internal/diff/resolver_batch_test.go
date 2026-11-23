// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package diff

import (
	"testing"

	"github.com/alibaba/open-code-review/internal/model"
)

func TestResolveLineNumbersBatchKeepsExactHunkPriority(t *testing.T) {
	diffs := []model.Diff{{NewPath: "main.go", Diff: "@@ -1,6 +1,6 @@\n-aOld\n+a\n \n-bOld\n+b\n d\n \n e\n@@ -10,2 +10,2 @@\n-dOld\n+d\n e"}}
	comments := []model.LlmComment{
		{Path: "main.go", ExistingCode: "a\nb"},
		{Path: "main.go", ExistingCode: "d\ne"},
		{Path: "main.go", ExistingCode: "d"},
		{Path: "main.go", ExistingCode: "dOld"},
		{Path: "main.go", ExistingCode: "d\ne"},
	}
	want := [][2]int{{1, 3}, {10, 11}, {4, 4}, {10, 10}, {10, 11}}
	got := ResolveLineNumbers(comments, diffs)
	for i, comment := range got {
		if comment.StartLine != want[i][0] || comment.EndLine != want[i][1] {
			t.Errorf("comment %d = %d..%d, want %v", i, comment.StartLine, comment.EndLine, want[i])
		}
		if comments[i].StartLine != 0 || comments[i].EndLine != 0 {
			t.Error("input comments were mutated")
		}
	}
}

func TestResolveLineNumbersBatchSwitchesFiles(t *testing.T) {
	diffs := []model.Diff{
		{NewPath: "a.go", Diff: "@@ -1,1 +1,1 @@\n a"},
		{NewPath: "b.go", Diff: "@@ -41,1 +41,1 @@\n a"},
		{NewPath: "c.go", NewFileContent: "first\n\na\n"},
	}
	comments := []model.LlmComment{
		{Path: "a.go", ExistingCode: "a"},
		{Path: "b.go", ExistingCode: "a"},
		{Path: "c.go", ExistingCode: "a"},
		{Path: "a.go", ExistingCode: "a"},
	}
	got := ResolveLineNumbers(comments, diffs)
	for i, line := range []int{1, 41, 3, 1} {
		if got[i].StartLine != line || got[i].EndLine != line {
			t.Errorf("comment %d = %d..%d, want %d", i, got[i].StartLine, got[i].EndLine, line)
		}
	}
}
