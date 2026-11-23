// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package tool

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alibaba/open-code-review/internal/gitcmd"
)

const (
	gitGrepMaxCount = 100
	gitGrepTimeout  = 10 * time.Second
)

// CodeSearchProvider performs text search across the repository using git grep.
type CodeSearchProvider struct {
	FileReader *FileReader
	Access     *CodeAccess
}

func NewCodeSearch(fr *FileReader) *CodeSearchProvider { return &CodeSearchProvider{FileReader: fr} }

func (p *CodeSearchProvider) Tool() Tool { return CodeSearch }

func (p *CodeSearchProvider) Execute(ctx context.Context, args map[string]any) (string, error) {
	return p.Access.execute(ctx, args, p.FileReader, func(reader *FileReader) (string, error) {
		selected := *p
		selected.FileReader = reader
		return selected.execute(ctx, args)
	})
}

func (p *CodeSearchProvider) execute(ctx context.Context, args map[string]any) (string, error) {
	searchText, _ := args["search_text"].(string)
	caseSensitive, _ := args["case_sensitive"].(bool)
	usePerlRegexp, _ := args["use_perl_regexp"].(bool)

	filePatternsIface, _ := args["file_patterns"].([]any)
	var patterns []string
	for _, item := range filePatternsIface {
		if s, ok := item.(string); ok && s != "" {
			if hasTraversalPathComponent(s) {
				return "Error: file_patterns must not contain ..", nil
			}
			// Treat backslashes as separators so Windows pathspecs match.
			patterns = append(patterns, strings.ReplaceAll(s, "\\", "/"))
		}
	}

	if strings.TrimSpace(searchText) == "" {
		return "Error: search_text is blank", nil
	}

	result, err := p.gitGrep(ctx, searchText, caseSensitive, usePerlRegexp, patterns)
	if err != nil {
		return "", err
	}
	return result, nil
}

func (p *CodeSearchProvider) buildGrepArgs(searchText string, caseSensitive bool, usePerlRegexp bool, noIndex bool, pathspec []string) []string {
	// core.quotepath=false reports non-ASCII paths literally instead of as
	// quoted octal escapes, which file_read cannot open.
	cmdArgs := []string{"--no-pager", "-c", "core.quotepath=false", "grep"}

	if noIndex {
		// Non-git directory: search the working tree directly while still
		// honoring .gitignore and skipping .git (via --exclude-standard).
		cmdArgs = append(cmdArgs, "--no-index", "--exclude-standard")
	} else if p.FileReader.Ref == "" {
		cmdArgs = append(cmdArgs, "--untracked")
	}

	if !caseSensitive {
		cmdArgs = append(cmdArgs, "-i")
	}
	if usePerlRegexp {
		cmdArgs = append(cmdArgs, "-P")
	} else {
		cmdArgs = append(cmdArgs, "-F")
	}

	cmdArgs = append(cmdArgs, "-n", "--no-color")
	// git grep limits matches per file. Fetch one extra to distinguish an exact
	// limit from truncated results, then enforce the global limit below.
	cmdArgs = append(cmdArgs, "--max-count", fmt.Sprintf("%d", gitGrepMaxCount+1))

	cmdArgs = append(cmdArgs, "-e", searchText)

	if ref := p.FileReader.Ref; ref != "" {
		if strings.HasPrefix(ref, "-") {
			// Defense-in-depth: reject option-like refs here even though
			// validateReviewRefs already verifies the ref upstream.
			// NOTE: git grep < 2.45 does not support --end-of-options before
			// the revision, so this is the one git invocation where we can't
			// rely on that separator.
			return nil
		}
		cmdArgs = append(cmdArgs, ref)
	}

	cmdArgs = append(cmdArgs, "--")
	cmdArgs = append(cmdArgs, pathspec...)

	return cmdArgs
}

func hasTraversalPathComponent(pathspec string) bool {
	norm := strings.ReplaceAll(pathspec, "\\", "/")
	return slices.Contains(strings.Split(norm, "/"), "..")
}

func (p *CodeSearchProvider) runGitGrep(parentCtx context.Context, cmdArgs []string) (*grepMatches, string, error) {
	ctx, cancel := context.WithTimeout(parentCtx, gitGrepTimeout)
	defer cancel()

	result := &grepMatches{hasRef: p.FileReader.Ref != "", files: make(map[string][]grepMatch), matchedFiles: make(map[string]bool)}
	runner := p.FileReader.Runner
	if runner == nil {
		runner = gitcmd.New(1)
	}
	err := runner.Stream(ctx, p.FileReader.RepoDir, result.read, cmdArgs...)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return result, string(exitErr.Stderr), exitErr
	}
	return result, "", err
}

func (p *CodeSearchProvider) gitGrep(ctx context.Context, searchText string, caseSensitive bool, usePerlRegexp bool, pathspec []string) (string, error) {
	if p.FileReader.Mode == ModeEmpty {
		return "No matches found", nil
	}
	cmdArgs := p.buildGrepArgs(searchText, caseSensitive, usePerlRegexp, false, pathspec)
	if cmdArgs == nil {
		return "Error: ref must not start with '-'", nil
	}

	outStr, errStr, err := p.runGitGrep(ctx, cmdArgs)

	// Non-git directory: `git grep` exits 128 with "not a git repository".
	// `ocr scan` supports plain directories, so retry in --no-index mode, which
	// searches the working tree directly while still honoring .gitignore.
	// Ref-based search needs a real repo, so it is not retried.
	if err != nil && p.FileReader.Ref == "" && isNotGitRepoError(err, errStr) {
		cmdArgs = p.buildGrepArgs(searchText, caseSensitive, usePerlRegexp, true, pathspec)
		outStr, errStr, err = p.runGitGrep(ctx, cmdArgs)
	}

	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "", fmt.Errorf("git grep timed out; try narrowing file_patterns to a more specific path: %w", err)
		}
		if errors.Is(err, context.Canceled) {
			return "", err
		}
		if !outStr.hasOutput {
			var exitErr *exec.ExitError
			exitCode := -1
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitCode()
			}
			if errStr == "" && exitCode == 1 {
				return "No matches found", nil
			}
			trimmedErr := trimGitUsage(errStr, exitCode)
			if trimmedErr == "" {
				return "", fmt.Errorf("git grep failed: %w", err)
			}
			return "", fmt.Errorf("git grep failed: %w: %s", err, trimmedErr)
		}
	}

	result := outStr.render()
	if err != nil && errStr != "" {
		result += fmt.Sprintf("Warning: %s\n", strings.TrimSpace(errStr))
	}
	return result, nil
}

type grepMatch struct {
	lineNum int
	content string
}

type grepMatches struct {
	hasRef       bool
	hasOutput    bool
	count        int
	truncated    bool
	files        map[string][]grepMatch
	fileOrder    []string
	matchedFiles map[string]bool
}

// read drains stdout even after the render limit so the matched-file count
// remains exact. Only rendered matches and distinct filenames survive a line.
func (matches *grepMatches) read(stdout io.Reader) error {
	reader := bufio.NewReader(stdout)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			matches.hasOutput = true
			matches.add(strings.TrimSuffix(line, "\n"))
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func (matches *grepMatches) add(line string) {
	splitN := 3
	offset := 0
	if matches.hasRef {
		splitN = 4
		offset = 1
	}
	parts := strings.SplitN(line, ":", splitN)
	if len(parts) < splitN {
		return
	}
	fname := parts[offset]
	ln, err := strconv.Atoi(parts[offset+1])
	if err != nil {
		return
	}
	if !matches.matchedFiles[fname] {
		// A substring of a discarded match must not retain its full line.
		matches.matchedFiles[strings.Clone(fname)] = true
	}
	if matches.count >= gitGrepMaxCount {
		matches.truncated = true
		return
	}
	if _, exists := matches.files[fname]; !exists {
		matches.fileOrder = append(matches.fileOrder, fname)
	}
	matches.files[fname] = append(matches.files[fname], grepMatch{lineNum: ln, content: parts[offset+2]})
	matches.count++
}

func (matches *grepMatches) render() string {
	var sb strings.Builder
	if matches.truncated {
		sb.WriteString(fmt.Sprintf("Note: Showing the first %d matches across %d matching files. Some files are partially shown or omitted entirely. Narrow file_patterns to see the rest.\n", gitGrepMaxCount, len(matches.matchedFiles)))
	}

	for _, path := range matches.fileOrder {
		fileMatches := matches.files[path]
		sb.WriteString(fmt.Sprintf("File: %s\nMatch lines: %d\n", path, len(fileMatches)))
		for _, m := range fileMatches {
			sb.WriteString(fmt.Sprintf("%d|%s\n", m.lineNum, m.content))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func trimGitUsage(stderr string, exitCode int) string {
	stderr = strings.TrimSpace(stderr)
	if exitCode == 129 {
		if idx := strings.IndexByte(stderr, '\n'); idx >= 0 {
			stderr = stderr[:idx]
		}
	}
	return strings.TrimSpace(stderr)
}

func isNotGitRepoError(err error, stderr string) bool {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 128 &&
		(strings.Contains(stderr, "not a git repository") || strings.Contains(stderr, ".git")) {
		return true
	}
	return false
}
