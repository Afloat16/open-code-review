// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package tool

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/gitcmd"
)

func TestDirectoryQueryMatching(t *testing.T) {
	files := []string{"Packages/Core/First.go", "Packages/Core/Second.go", "Packages/Core/Third.go", "elsewhere/Core.go"}
	for _, tc := range []struct {
		name, query string
		sensitive   bool
		want        []string
	}{
		{"forward slash", "packages/core", false, []string{files[1], files[2]}},
		{"backslash query", `PACKAGES\CORE`, false, []string{files[1], files[2]}},
		{"case sensitive match", "Packages/Core", true, []string{files[1], files[2]}},
		{"case sensitive miss", "packages/core", true, nil},
		{"basename priority", "Core", true, []string{files[3]}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MatchFileNames(t.Context(), files, tc.query, tc.sensitive, 2, func(name string) bool {
				return name != files[0]
			})
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := MatchFileNames(ctx, files, "packages/core", false, 2, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled query returned %v", err)
	}
}

func directoryQueryFiles(count int) []string {
	files := make([]string, count)
	for i := range files {
		if i < fileFindMaxCount {
			files[i] = fmt.Sprintf("a-target/Component%06d.go", i)
		} else {
			files[i] = fmt.Sprintf("packages/feature%05d/Component%06d.go", i/1000, i)
		}
	}
	return files
}

func TestDirectoryQueryAllocatesForReturnedWindow(t *testing.T) {
	files := directoryQueryFiles(20000)
	var got []string
	var err error
	allocs := testing.AllocsPerRun(3, func() {
		got, err = MatchFileNames(t.Context(), files, "a-target/", false, 10, nil)
	})
	if err != nil || !reflect.DeepEqual(got, files[:10]) {
		t.Fatalf("got %v, %v", got, err)
	}
	// A bounded early result should not allocate lowercased copies of every
	// unrelated basename in the inventory. Leave room for the result slice.
	if allocs > 64 {
		t.Fatalf("ten returned paths required %.0f allocations for a %d-path inventory", allocs, len(files))
	}
}

// Build a real committed tree without materializing thousands of worktree files.
func directoryQueryProvider(tb testing.TB, count int) *FileFindProvider {
	tb.Helper()
	dir := tb.TempDir()
	run := func(input string, args ...string) string {
		tb.Helper()
		cmd := exec.CommandContext(tb.Context(), "git", args...)
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.CombinedOutput()
		if err != nil {
			tb.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("", "init", "-q")
	blob := run("package fixture\n", "hash-object", "-w", "--stdin")
	var index strings.Builder
	for _, name := range directoryQueryFiles(count) {
		fmt.Fprintf(&index, "100644 %s\t%s\n", blob, name)
	}
	run(index.String(), "update-index", "--index-info")
	tree := run("", "write-tree")
	commit := run("fixture\n", "-c", "user.name=Test User", "-c", "user.email=test@example.com", "commit-tree", tree)
	return NewFileFind(&FileReader{RepoDir: dir, Mode: ModeCommit, Ref: commit, Runner: gitcmd.New(1)})
}

func TestDirectoryQueryFileFindAtCommittedRef(t *testing.T) {
	provider := directoryQueryProvider(t, 1000)
	got, err := provider.Execute(t.Context(), map[string]any{"query_name": `A-TARGET\`})
	want := strings.Join(directoryQueryFiles(1000)[:fileFindMaxCount], "\n")
	if err != nil || got != want {
		t.Fatalf("file_find returned %q, %v; want %q", got, err, want)
	}
}

func BenchmarkDirectoryQueryMatcher(b *testing.B) {
	files := directoryQueryFiles(100000)
	for _, query := range []string{"a-target/", "packages/feature00099/", "missing/"} {
		b.Run(query, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, err := MatchFileNames(b.Context(), files, query, false, fileFindMaxCount, nil)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkDirectoryQueryFileFind(b *testing.B) {
	provider := directoryQueryProvider(b, 100000)
	args := map[string]any{"query_name": "a-target/"}
	want := strings.Join(directoryQueryFiles(100000)[:fileFindMaxCount], "\n")
	b.ReportAllocs()
	for b.Loop() {
		got, err := provider.Execute(b.Context(), args)
		if err != nil || got != want {
			b.Fatalf("file_find result changed: %v", err)
		}
	}
}
