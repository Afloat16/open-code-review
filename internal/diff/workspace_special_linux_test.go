// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package diff

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReadWorkspaceFileForDiffRejectsNamedPipe(t *testing.T) {
	repo := t.TempDir()
	pipe := filepath.Join(repo, "events.go")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	writer, err := os.OpenFile(pipe, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	done := make(chan error, 1)
	go func() {
		_, err := readWorkspaceFileForDiff(repo, "events.go")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("error = %v, want non-regular file rejection", err)
		}
	case <-time.After(time.Second):
		_ = writer.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("FIFO reader did not exit after its writer closed")
		}
		t.Fatal("workspace diff read blocked on a named pipe")
	}
	if err := os.Symlink("events.go", filepath.Join(repo, "link.go")); err != nil {
		t.Fatal(err)
	}
	content, err := readWorkspaceFileForDiff(repo, "link.go")
	if err != nil || string(content) != "events.go" {
		t.Fatalf("symlink diff content = %q, error = %v; want link target text", content, err)
	}
}
