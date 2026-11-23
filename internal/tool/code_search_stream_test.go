// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package tool

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/gitcmd"
)

func broadSearchFixture(t testing.TB) (string, int) {
	t.Helper()
	dir := t.TempDir()
	line := "Needle " + strings.Repeat("x", 2048) + "\n"
	contents := strings.Repeat(line, gitGrepMaxCount+1)
	for i := range 64 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%03d.txt", i)), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, 64 * len(contents)
}

func TestCodeSearchBroadMatches(t *testing.T) {
	dir, _ := broadSearchFixture(t)
	for _, args := range [][]string{{"init"}, {"add", "."}, {"-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "-m", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	var first string
	for _, ref := range []string{"", "HEAD"} {
		for _, shared := range []bool{false, true} {
			reader := &FileReader{RepoDir: dir, Ref: ref}
			if shared {
				reader.Runner = gitcmd.New(1)
			}
			result, err := NewCodeSearch(reader).Execute(t.Context(), map[string]any{"search_text": "Needle"})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(result, "|Needle ") != gitGrepMaxCount || !strings.Contains(result, "across 64 matching files") {
				t.Fatalf("lost matches or omitted-file count: %.200s", result)
			}
			if first == "" {
				first = result
			} else if result != first {
				t.Fatal("shared runner or commit mode changed the rendered result")
			}
		}
	}
}

func TestCodeSearchLongUnterminatedLine(t *testing.T) {
	for _, shared := range []bool{false, true} {
		dir := t.TempDir()
		content := "Needle " + strings.Repeat("x", 128<<10)
		if err := os.WriteFile(filepath.Join(dir, "long.txt"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		reader := &FileReader{RepoDir: dir}
		if shared {
			reader.Runner = gitcmd.New(1)
		}
		got, err := NewCodeSearch(reader).Execute(t.Context(), map[string]any{"search_text": "Needle"})
		if err != nil {
			t.Fatal(err)
		}
		want := "File: long.txt\nMatch lines: 1\n1|" + content + "\n\n"
		if got != want {
			t.Fatalf("unterminated long line changed: got %d bytes, want %d", len(got), len(want))
		}
	}
}

func BenchmarkCodeSearchBroadMatches(b *testing.B) {
	dir, bytes := broadSearchFixture(b)
	p := NewCodeSearch(&FileReader{RepoDir: dir, Runner: gitcmd.New(1)})
	b.ReportAllocs()
	b.SetBytes(int64(bytes))
	b.ResetTimer()
	for b.Loop() {
		result, err := p.Execute(b.Context(), map[string]any{"search_text": "Needle"})
		if err != nil || !strings.Contains(result, "across 64 matching files") {
			b.Fatalf("search failed: %v", err)
		}
	}
}
