// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package scan

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnumerateBinarySniffBuffer(t *testing.T) {
	dir := t.TempDir()
	binary := bytes.Repeat([]byte{'x'}, binarySniffWindow)
	binary[len(binary)-1] = 0
	text := bytes.Repeat([]byte{'t'}, binarySniffWindow)
	files := map[string][]byte{
		"a_binary.go": binary,
		"b_short.go":  []byte("package short\n"),
		"c_empty.go":  nil,
		"d_text.go":   text,
		"e_tail.go":   append(bytes.Clone(text), 0),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	provider := NewProvider(dir, nil, nil, 0)
	for i := range 8 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			t.Parallel()
			items, err := provider.Enumerate(t.Context())
			if err != nil || len(items) != len(files) {
				t.Fatalf("Enumerate: items=%d err=%v", len(items), err)
			}
			for _, item := range items {
				if item.Path == "a_binary.go" {
					if !item.IsBinary || item.Content != "" || item.LineCount != 0 {
						t.Fatalf("binary placeholder = %+v", item)
					}
					continue
				}
				content, ok := files[item.Path]
				if !ok || item.IsBinary || item.Content != string(content) || item.LineCount != countLines(content) {
					t.Errorf("incorrect text result for %q", item.Path)
				}
			}
		})
	}
}

func BenchmarkEnumerateSmallFiles(b *testing.B) {
	dir := b.TempDir()
	content := []byte("package fixture\n\nvar value = \"" + strings.Repeat("x", 240) + "\"\n")
	for i := range 1000 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file_%04d.go", i)), content, 0o600); err != nil {
			b.Fatal(err)
		}
	}
	provider := NewProvider(dir, nil, nil, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		items, err := provider.Enumerate(b.Context())
		if err != nil || len(items) != 1000 {
			b.Fatalf("Enumerate: items=%d err=%v", len(items), err)
		}
		for _, item := range items {
			if item.Content != string(content) || item.IsBinary || item.LineCount != 3 {
				b.Fatalf("incorrect text result for %q", item.Path)
			}
		}
	}
}
