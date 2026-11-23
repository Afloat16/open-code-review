// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package behaviortask

import (
	"fmt"
	"sync"
	"testing"
)

func TestFileFindKeepsSidesAndAuthorization(t *testing.T) {
	code := &codeTools{allowed: map[string]map[string]bool{
		"old": {"before.go": true, "shared.go": true, "secret.go": false},
		"new": {"after.go": true, "shared.go": true, "secret.go": false},
	}}
	var workers sync.WaitGroup
	for range 8 {
		for _, fixture := range []struct{ side, want string }{
			{"old", "before.go\nshared.go"}, {"new", "after.go\nshared.go"},
		} {
			workers.Go(func() {
				result, err := code.execute(t.Context(), "file_find", map[string]any{"side": fixture.side, "query_name": ".go"})
				if err != nil || result.ReturnedContent != fixture.want || result.Truncated {
					t.Errorf("%s file_find = %+v, %v", fixture.side, result, err)
				}
			})
		}
	}
	workers.Wait()
}

func BenchmarkAnalysisFileFind(b *testing.B) {
	for _, size := range []int{1000, 100000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			allowed := make(map[string]bool, size)
			for index := range size {
				allowed[fmt.Sprintf("src/package%06d/main.go", index)] = true
			}
			code := &codeTools{allowed: map[string]map[string]bool{"new": allowed}}
			args := map[string]any{"query_name": "main.go"}
			if _, err := code.execute(b.Context(), "file_find", args); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				result, err := code.execute(b.Context(), "file_find", args)
				if err != nil || !result.Truncated {
					b.Fatalf("file_find = %+v, %v", result, err)
				}
			}
		})
	}
}
