// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package tool

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/gitcmd"
)

func TestFileFindCountsUniqueUnmergedPaths(t *testing.T) {
	for _, count := range []int{40, 110} {
		t.Run(fmt.Sprintf("%d_conflicted_files", count), func(t *testing.T) {
			dir := t.TempDir()
			run := func(input string, args ...string) string {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir = dir
				cmd.Stdin = strings.NewReader(input)
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v: %s", args, err, out)
				}
				return strings.TrimSpace(string(out))
			}
			run("", "init", "-q")
			if err := os.Mkdir(filepath.Join(dir, "pkg"), 0o755); err != nil {
				t.Fatal(err)
			}
			var blobs []string
			for _, source := range []string{"package base\n", "package ours\n", "package theirs\n"} {
				blobs = append(blobs, run(source, "hash-object", "-w", "--stdin"))
			}
			var index strings.Builder
			var paths []string
			for i := range count {
				path := fmt.Sprintf("pkg/match%03d.go", i)
				paths = append(paths, path)
				if err := os.WriteFile(filepath.Join(dir, path), []byte("package unresolved\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				for stage, blob := range blobs {
					fmt.Fprintf(&index, "100644 %s %d\t%s\n", blob, stage+1, path)
				}
			}
			run(index.String(), "update-index", "--index-info")
			if got := len(strings.Split(run("", "ls-files", "--unmerged"), "\n")); got != count*3 {
				t.Fatalf("expected three Git index stages per conflicted file, got %d records", got)
			}
			want := paths[:min(len(paths), fileFindMaxCount)]
			for _, query := range []string{".go", "pkg/"} {
				for _, useRunner := range []bool{false, true} {
					t.Run(fmt.Sprintf("query_%s_runner_%v", query, useRunner), func(t *testing.T) {
						reader := &FileReader{RepoDir: dir}
						if useRunner {
							reader.Runner = gitcmd.New(1)
						}
						output, err := NewFileFind(reader).Execute(context.Background(), map[string]any{"query_name": query})
						if err != nil {
							t.Fatal(err)
						}
						if got := strings.Split(output, "\n"); !reflect.DeepEqual(got, want) {
							t.Fatalf("file_find = %q; want each of %d distinct paths once: %q", got, len(want), want)
						}
					})
				}
			}
		})
	}
}
