// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package reviewtask

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alibaba/open-code-review/internal/llm"
	ocrmcp "github.com/alibaba/open-code-review/internal/mcp"
	"github.com/alibaba/open-code-review/internal/session"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPToolErrorsReachReviewFailureTracking(t *testing.T) {
	const name = "lookup_symbols"
	const failure = "index unavailable"
	const success = "Error: is part of the source text"
	var calls atomic.Int64
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "fixture", Version: "1"}, nil)
	server.AddTool(&sdkmcp.Tool{Name: name, InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{"mode": map[string]any{"type": "string"}},
	}}, func(_ context.Context, request *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		calls.Add(1)
		var args struct {
			Mode string `json:"mode"`
		}
		if err := json.Unmarshal(request.Params.Arguments, &args); err != nil {
			return nil, err
		}
		failed, text := args.Mode != "ok", failure
		if !failed {
			text = success
		}
		return &sdkmcp.CallToolResult{IsError: failed, Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: text}}}, nil
	})
	httpServer := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil))
	defer httpServer.Close()
	mcpClient, err := ocrmcp.NewRemoteClient(t.Context(), "fixture", httpServer.URL, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer mcpClient.Close()

	client := &fakeClient{responses: []*llm.ChatResponse{
		toolCallResponse(name, `{"mode":"fail"}`),
		toolCallResponse(name, `{"mode":"fail"}`),
		toolCallResponse(name, `{"mode":"fail"}`),
		toolCallResponse(name, `{"mode":"ok"}`),
		toolCallResponse(name, `{"mode":"fail"}`),
		taskDoneResponse(),
	}}
	deps := newTestDeps(client)
	deps.Template.MaxToolRequestTimes = 10
	deps.Session = session.New(t.TempDir(), "main", "fixture", session.SessionOptions{})
	ocrmcp.RegisterAll(deps.Tools, mcpClient, nil)
	deps.MainToolDefs = append(deps.MainToolDefs, ocrmcp.CollectToolDefs([]*ocrmcp.Client{mcpClient}, deps.Tools)...)
	runner := NewRunner(deps)
	completed, _, err := runner.RunMainTask(t.Context(), []llm.Message{llm.NewTextMessage("user", "review")}, "main.go")
	if err != nil || !completed || calls.Load() != 5 || len(client.requests) != 6 {
		t.Fatalf("completed=%v error=%v MCP calls=%d LLM requests=%d", completed, err, calls.Load(), len(client.requests))
	}
	for index, expected := range []string{failure, "second consecutive failure", "has failed repeatedly", success, failure} {
		messages := client.requests[index+1].Messages
		result := messages[len(messages)-1].ExtractText()
		if !strings.Contains(result, expected) {
			t.Errorf("result %d = %q, want %q", index+1, result, expected)
		}
		if index == 4 && strings.Contains(result, "second consecutive failure") {
			t.Error("successful MCP call did not reset the failure streak")
		}
	}
	failures := runner.ToolFailures()
	if len(failures) != 4 {
		t.Errorf("recorded %d failures, want 4: %+v", len(failures), failures)
	}
	for _, failure := range failures {
		if failure.ToolName != name || failure.FilePath != "main.go" || !strings.Contains(failure.Error, "index unavailable") {
			t.Errorf("failure lost MCP identity or diagnostic: %+v", failure)
		}
	}
	records := deps.Session.GetOrCreateFileSession("main.go").TaskRecords[session.MainTask]
	if len(records) != 6 {
		t.Fatalf("task records = %d, want 6", len(records))
	}
	for index, record := range records[:5] {
		if len(record.ToolResults) != 1 || record.ToolResults[0].OK != (index == 3) {
			t.Errorf("tool result %d has wrong success status: %+v", index+1, record.ToolResults)
		}
	}
}
