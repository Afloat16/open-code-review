// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package rules

import (
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"
)

func TestDefaultRulePreparationPreservesMatchProvenance(t *testing.T) {
	loaded, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	constructed := &SystemRule{DefaultRule: loaded.DefaultRule, PathRules: slices.Clone(loaded.PathRules)}
	for _, tc := range []struct{ path, pattern string }{
		{"src/main.GO", "**/*.go"},
		{"pkg/module.CPP", "**/*.{cpp,cc,cxx,hpp,hxx}"},
		{"web/component.TSX", "**/*.{ts,js,tsx,jsx,mjs,cjs,mts,cts}"},
		{"sub/CARGO.TOML", "**/Cargo.toml"},
		{".github/workflows/build.YML", ".github/workflows/**/*.{yaml,yml}"},
		{".github/config.yml", ".github/**/*.{yaml,yml}"},
		{"analysis/model.R", "**/*.R"},
		{"database/schema.sql", "**/*.sql"},
		{"other/unmatched.file", "default"},
	} {
		got := loaded.resolveDetail(tc.path)
		if got.Pattern != tc.pattern || got.Source != "system" || got.Rule == "" || got != constructed.resolveDetail(tc.path) {
			t.Errorf("%s resolved to %+v; expected pattern %q and unchanged rule", tc.path, got, tc.pattern)
		}
	}
	if !reflect.DeepEqual(loaded.CanonicalConfig(), constructed.CanonicalConfig()) {
		t.Fatal("pattern preparation changed canonical configuration")
	}
}

func TestDefaultRulePreparationRespectsCurrentPublicRules(t *testing.T) {
	loaded, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	loaded.PathRules[0] = PathRule{Pattern: "**/*.custom", Rule: "replacement rule"}
	if got := loaded.Resolve("config/app.custom"); got != "replacement rule" {
		t.Fatalf("new pattern resolved to %q", got)
	}
	if got := loaded.Resolve("config/app.properties"); got != loaded.DefaultRule {
		t.Fatal("replaced pattern still matched")
	}
	original := loaded.Resolve("pkg/main.go")
	loaded.PathRules = append([]PathRule{{Pattern: "**/*.go", Rule: "first rule"}}, loaded.PathRules...)
	if got := loaded.Resolve("pkg/main.go"); got != "first rule" {
		t.Fatalf("first-match priority changed: %q", got)
	}
	loaded.PathRules[0].Rule = "changed rule text"
	if got := loaded.Resolve("pkg/main.go"); got != "changed rule text" {
		t.Fatalf("rule text was cached: %q", got)
	}
	loaded.PathRules = loaded.PathRules[1:]
	if got := loaded.Resolve("pkg/main.go"); got != original {
		t.Fatal("removing the first rule did not restore the original")
	}
}

func TestDefaultRulesResolveConcurrently(t *testing.T) {
	loaded, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{"main.go", "src/main.py", "web/app.TSX", "db/schema.sql", "docs/unknown.file"}
	want := make([]RuleDetail, len(paths))
	for index, path := range paths {
		want[index] = loaded.resolveDetail(path)
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 8 {
				for index, path := range paths {
					if got := loaded.resolveDetail(path); got != want[index] {
						t.Errorf("concurrent lookup changed %s", path)
					}
				}
			}
		})
	}
	workers.Wait()
}

func BenchmarkDefaultRuleLookupBatch(b *testing.B) {
	loaded, err := LoadDefault()
	if err != nil {
		b.Fatal(err)
	}
	extensions := []string{"go", "py", "tsx", "tf", "swift", "sql", "clj", "unknown"}
	paths, expected := make([]string, 1024), make([]string, 1024)
	for index := range paths {
		paths[index] = fmt.Sprintf("pkg/module_%d/source.%s", index, extensions[index%len(extensions)])
		expected[index] = loaded.Resolve(paths[index])
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for index, path := range paths {
			if got := loaded.Resolve(path); got != expected[index] {
				b.Fatalf("rule changed for %s", path)
			}
		}
	}
}
