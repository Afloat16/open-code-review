// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestFileReaderRejectsNamedPipes(t *testing.T) {
	for _, throughLink := range []bool{false, true} {
		for _, operation := range []string{"Read", "ReadLines", "file_read"} {
			name := operation
			if throughLink {
				name += "/symlink"
			}
			t.Run(name, func(t *testing.T) {
				repo := t.TempDir()
				pipe := filepath.Join(repo, "events.go")
				if err := syscall.Mkfifo(pipe, 0o600); err != nil {
					t.Fatal(err)
				}
				// Keep a writer open so a reader of the FIFO blocks until cleanup,
				// rather than letting a regression hang the entire test process.
				writer, err := os.OpenFile(pipe, os.O_RDWR, 0)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = writer.Close() })
				path := "events.go"
				if throughLink {
					path = "link.go"
					if err := os.Symlink("events.go", filepath.Join(repo, path)); err != nil {
						t.Fatal(err)
					}
				}
				fr := &FileReader{RepoDir: repo, Mode: ModeWorkspace}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				done := make(chan error, 1)
				go func() {
					var err error
					switch operation {
					case "Read":
						_, err = fr.Read(ctx, path)
					case "ReadLines":
						_, _, err = fr.ReadLines(ctx, path, 1, 10)
					case "file_read":
						_, err = NewFileRead(fr).Execute(ctx, map[string]any{"file_path": path})
					}
					done <- err
				}()
				select {
				case err := <-done:
					if err == nil || !strings.Contains(err.Error(), "not a regular file") {
						t.Fatalf("error = %v, want non-regular file rejection", err)
					}
				case <-ctx.Done():
					_ = writer.Close()
					select {
					case <-done:
					case <-time.After(time.Second):
						t.Fatal("FIFO reader did not exit after its writer closed")
					}
					t.Fatal("workspace read blocked on a named pipe")
				}
			})
		}
	}
}
