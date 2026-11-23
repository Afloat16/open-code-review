// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package main

import (
	"strings"
	"testing"
	"time"
)

func TestFailureUsageReportsIncompleteBreakdown(t *testing.T) {
	provider := &mockResultProvider{inputTokens: 30, totalTokens: 100}
	output := captureStderr(t, func() { emitFailureUsage(provider, time.Second, "text", nil, nil) })
	if !strings.Contains(output, "100 total tokens") || !strings.Contains(output, "unattributed: 70") || strings.Contains(output, "30 input + 0 output = 100") {
		t.Fatalf("missing breakdown rendered as a false equality: %s", output)
	}
}
