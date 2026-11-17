// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package rules

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/gitcmd"
)

func TestSnifferRefFirstLineWithLargeBlob(t *testing.T) {
	for _, firstLine := range []string{"#import <Foundation/Foundation.h>", "#import " + strings.Repeat("x", 96*1024)} {
		dir, ref := initRepo(t, map[string]string{"large.m": "\n \t\n" + firstLine + "\n" + strings.Repeat("int value;\n", 1024*1024)})
		writeFile(t, dir, "large.m", matlabSource)
		for _, runner := range []*gitcmd.Runner{nil, gitcmd.New(1)} {
			s := &sniffer{repoDir: dir, ref: ref, runner: runner}
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			if got := s.peekFirstLine("large.m"); got != firstLine {
				t.Fatalf("first line length %d, runner %v: got %d bytes", len(firstLine), runner != nil, len(got))
			}
			runtime.ReadMemStats(&after)
			if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 4<<20 {
				t.Errorf("first line length %d, runner %v: allocated %d bytes to read a header", len(firstLine), runner != nil, allocated)
			}
		}
	}
}

func BenchmarkSnifferCommittedBlob(b *testing.B) {
	for _, size := range []int{1 << 20, 16 << 20} {
		b.Run(fmt.Sprintf("%dMiB", size>>20), func(b *testing.B) {
			dir := b.TempDir()
			run := func(args ...string) {
				b.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir = dir
				if output, err := cmd.CombinedOutput(); err != nil {
					b.Fatalf("git %v: %v: %s", args, err, output)
				}
			}
			run("init", "-q")
			source := "#import <Foundation/Foundation.h>\n" + strings.Repeat("int value;\n", size/11)
			if err := os.WriteFile(filepath.Join(dir, "large.m"), []byte(source), 0o644); err != nil {
				b.Fatal(err)
			}
			run("add", "large.m")
			run("-c", "user.name=Fixture", "-c", "user.email=fixture@example.com", "commit", "-q", "-m", "fixture")
			for _, useRunner := range []bool{false, true} {
				b.Run(fmt.Sprintf("runner_%v", useRunner), func(b *testing.B) {
					s := &sniffer{repoDir: dir, ref: "HEAD"}
					if useRunner {
						s.runner = gitcmd.New(1)
					}
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						if got := s.peekFirstLine("large.m"); got != "#import <Foundation/Foundation.h>" {
							b.Fatal("wrong first line")
						}
					}
				})
			}
		})
	}
}
