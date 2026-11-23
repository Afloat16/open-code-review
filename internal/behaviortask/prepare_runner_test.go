// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package behaviortask

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/config/template"
	"github.com/alibaba/open-code-review/internal/diff"
	"github.com/alibaba/open-code-review/internal/llm"
	"github.com/alibaba/open-code-review/internal/model"
)

func preparationDeps(test *testing.T) Deps {
	test.Helper()
	return Deps{
		LLMClient: fakeClient(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
			test.Fatal("preparing a runner must not call the LLM")
			return nil, nil
		}),
		Model: "preparation-test", ArtifactDir: filepath.Join(test.TempDir(), "artifacts"),
	}
}

func tracePreparationTrees(test *testing.T) (reset func(), count func() int) {
	test.Helper()
	if runtime.GOOS == "windows" {
		test.Skip("Git invocation counting uses a POSIX shell wrapper")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		test.Fatal(err)
	}
	directory := test.TempDir()
	logPath := filepath.Join(directory, "trees.log")
	program := "#!/bin/sh\ntree=no\nrecursive=no\nfor argument in \"$@\"; do\n case \"$argument\" in ls-tree) tree=yes ;; -r) recursive=yes ;; esac\ndone\nif [ \"$tree:$recursive\" = yes:yes ]; then printf 'tree\\n' >> \"$OCR_TEST_PREPARE_TREE_LOG\"; fi\nexec \"$OCR_TEST_PREPARE_REAL_GIT\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(directory, "git"), []byte(program), 0700); err != nil {
		test.Fatal(err)
	}
	test.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	test.Setenv("OCR_TEST_PREPARE_REAL_GIT", realGit)
	test.Setenv("OCR_TEST_PREPARE_TREE_LOG", logPath)
	reset = func() {
		test.Helper()
		if err := os.WriteFile(logPath, nil, 0600); err != nil {
			test.Fatal(err)
		}
	}
	count = func() int {
		test.Helper()
		data, err := os.ReadFile(logPath)
		if err != nil {
			test.Fatal(err)
		}
		return strings.Count(string(data), "tree\n")
	}
	reset()
	return reset, count
}

func TestPrepareRunnerEnumeratesTreesOnce(test *testing.T) {
	input := fixture(test)
	reset, count := tracePreparationTrees(test)
	for _, root := range []bool{false, true} {
		test.Run(map[bool]string{false: "range", true: "root"}[root], func(test *testing.T) {
			input := input
			want := 2
			if root {
				input.Mode = "commit"
				input.Resolution = diff.InputResolution{ResolvedHead: input.Resolution.ResolvedBase, BaseIsEmpty: true}
				want = 1
			}
			reset()
			runner, err := PrepareRunner(test.Context(), preparationDeps(test), input)
			if err != nil {
				test.Fatal(err)
			}
			test.Cleanup(func() { _ = runner.history.Finalize() })
			if got := count(); got != want {
				test.Errorf("enumerated %d trees; want %d, one per nonempty comparison side", got, want)
			}
			observation, err := runner.code.execute(test.Context(), "file_read_diff", map[string]any{"path_array": []any{"main.go"}})
			if err != nil || !strings.Contains(observation.ReturnedContent, "@@") || len(observation.Locations) == 0 {
				test.Fatalf("prepared patches were not bound to the runner: %+v, %v", observation, err)
			}
		})
	}
}

func TestPrepareRunnerMatchesPreparedInput(test *testing.T) {
	input := fixture(test)
	for _, name := range []string{"added.go", ".env", "excluded.go"} {
		if err := os.WriteFile(filepath.Join(input.RepoDir, name), []byte("updated "+name+"\n"), 0600); err != nil {
			test.Fatal(err)
		}
	}
	git(test, input.RepoDir, "add", ".")
	git(test, input.RepoDir, "commit", "-qm", "Add source and update excluded changes")
	input.Resolution.ResolvedHead = git(test, input.RepoDir, "rev-parse", "HEAD")
	input.Diffs = []model.Diff{{OldPath: "stale.go", NewPath: "stale.go", Diff: "stale input must be replaced"}}
	for _, variant := range []string{"range", "root", "exclude_old", "exclude_new", "empty"} {
		test.Run(variant, func(test *testing.T) {
			input := input
			switch variant {
			case "root":
				input.Mode = "commit"
				input.Resolution = diff.InputResolution{ResolvedHead: input.Resolution.ResolvedBase, BaseIsEmpty: true}
			case "exclude_old":
				input.ExcludePatterns = []string{"excluded.go", "consumer.go"}
			case "exclude_new":
				input.ExcludePatterns = []string{"excluded.go", "renamed.go"}
			case "empty":
				input.Resolution.ResolvedBase = input.Resolution.ResolvedHead
			}
			prepared, err := PrepareInput(test.Context(), input)
			if err != nil {
				test.Fatal(err)
			}
			previous, err := NewRunner(test.Context(), preparationDeps(test), prepared)
			if err != nil {
				test.Fatal(err)
			}
			test.Cleanup(func() { _ = previous.history.Finalize() })
			current, err := PrepareRunner(test.Context(), preparationDeps(test), input)
			if err != nil {
				test.Fatal(err)
			}
			test.Cleanup(func() { _ = current.history.Finalize() })
			if previous.code.comparison != current.code.comparison || !reflect.DeepEqual(previous.code.allowed, current.code.allowed) ||
				!reflect.DeepEqual(previous.code.scope, current.code.scope) || !reflect.DeepEqual(previous.code.diffs, current.code.diffs) {
				test.Fatal("prepared comparison, authorization, scope, or patches changed")
			}
			before, beforeErr := previous.messages()
			after, afterErr := current.messages()
			if beforeErr != nil || afterErr != nil || !reflect.DeepEqual(before, after) {
				test.Fatalf("initial messages changed: before=%v after=%v", beforeErr, afterErr)
			}
			for _, call := range []struct {
				name string
				args map[string]any
			}{
				{"file_read", map[string]any{"file_path": "main.go", "side": "old"}},
				{"file_read", map[string]any{"file_path": "main.go", "side": "new"}},
				{"file_read", map[string]any{"file_path": ".env"}},
				{"file_read", map[string]any{"file_path": "excluded.go"}},
				{"file_find", map[string]any{"query_name": ".go", "side": "old"}},
				{"file_find", map[string]any{"query_name": ".go", "side": "new"}},
				{"file_read_diff", map[string]any{"path_array": []any{"main.go", "deleted.go", "renamed.go", "added.go"}}},
			} {
				before, beforeErr := previous.code.execute(test.Context(), call.name, call.args)
				after, afterErr := current.code.execute(test.Context(), call.name, call.args)
				if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(beforeErr, afterErr) {
					test.Fatalf("%s %v changed: before=%+v %v after=%+v %v", call.name, call.args, before, beforeErr, after, afterErr)
				}
			}
		})
	}
}

func TestPrepareRunnerValidatesBeforeTreeReads(test *testing.T) {
	input := fixture(test)
	reset, count := tracePreparationTrees(test)
	for _, invalid := range []string{"dependencies", "contexts", "repository", "template", "canceled"} {
		test.Run(invalid, func(test *testing.T) {
			input := input
			deps := preparationDeps(test)
			ctx := test.Context()
			switch invalid {
			case "dependencies":
				deps.LLMClient = nil
			case "contexts":
				input.Contexts = []Context{{ID: "missing-kind"}}
			case "repository":
				input.RepoDir = filepath.Join(test.TempDir(), "missing")
			case "template":
				deps.Template = &template.BehaviorTemplate{}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			reset()
			if runner, err := PrepareRunner(ctx, deps, input); err == nil || runner != nil {
				test.Fatalf("invalid preparation accepted: runner=%v error=%v", runner, err)
			}
			if got := count(); got != 0 {
				test.Errorf("read %d trees before rejecting invalid preparation", got)
			}
		})
	}
}
