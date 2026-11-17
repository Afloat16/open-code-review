// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveWorkingDirPreservesRootWhitespace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not support these trailing filename characters")
	}
	for _, suffix := range []string{" ", "\t", "\n", "\r", "\u00a0"} {
		t.Run("suffix_"+suffix, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "repository"+suffix)
			if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("git", "init", "-q", root)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("git init: %v: %s", err, output)
			}
			want, err := filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, input := range []string{root, filepath.Join(root, "nested")} {
				got, isGit, err := resolveWorkingDir(input, true)
				if err != nil || !isGit || got != want {
					t.Fatalf("resolveWorkingDir(%q) = %q, %v, %v; want %q", input, got, isGit, err, want)
				}
				if _, err := os.Stat(got); err != nil {
					t.Fatalf("resolved repository root does not exist: %v", err)
				}
			}
			scanRoot, _, err := resolveWorkingDir(filepath.Join(root, "nested"), false)
			if err != nil || scanRoot != filepath.Join(root, "nested") {
				t.Fatalf("scan scope changed: %q, %v", scanRoot, err)
			}
		})
	}
}
