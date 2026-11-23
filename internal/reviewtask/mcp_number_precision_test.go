// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package reviewtask

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/llm"
	ocrmcp "github.com/alibaba/open-code-review/internal/mcp"
	"github.com/alibaba/open-code-review/internal/session"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestReviewMCPNumbersReachServerUnchanged(t *testing.T) {
	const arguments = `{"id":9007199254740993,"negative":-9007199254740993,"nested":{"ids":[9007199254740995,18446744073709551615]},"ratio":0.123456789012345678901234567890,"exponent":9.007199254740993e15,"small":3}`
	decode := func(t *testing.T, raw string) map[string]any {
		t.Helper()
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		var result map[string]any
		if err := decoder.Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	want := decode(t, arguments)
	for _, name := range []string{"lookup_record", "context_read", "submit_analysis"} {
		t.Run(name, func(t *testing.T) {
			server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "fixture", Version: "1"}, nil)
			server.AddTool(&sdkmcp.Tool{Name: name, InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, request *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
				return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: string(request.Params.Arguments)}}}, nil
			})
			httpServer := httptest.NewServer(sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return server }, nil))
			defer httpServer.Close()
			mcpClient, err := ocrmcp.NewRemoteClient(t.Context(), "fixture", httpServer.URL, nil, "test")
			if err != nil {
				t.Fatal(err)
			}
			defer mcpClient.Close()
			client := &fakeClient{responses: []*llm.ChatResponse{
				toolCallResponse(name, arguments),
				toolCallResponse(name, arguments+"<|end|>"),
				taskDoneResponse(),
			}}
			deps := newTestDeps(client)
			deps.Template.MaxToolRequestTimes = 5
			deps.Session = session.New(t.TempDir(), "main", "fixture", session.SessionOptions{})
			ocrmcp.RegisterAll(deps.Tools, mcpClient, nil)
			deps.MainToolDefs = append(deps.MainToolDefs, ocrmcp.CollectToolDefs([]*ocrmcp.Client{mcpClient}, deps.Tools)...)
			runner := NewRunner(deps)
			completed, _, err := runner.RunMainTask(t.Context(), []llm.Message{llm.NewTextMessage("user", "review")}, "main.go")
			if err != nil || !completed || len(client.requests) != 3 {
				t.Fatalf("completed=%v error=%v LLM requests=%d", completed, err, len(client.requests))
			}
			for _, request := range client.requests[1:] {
				messages := request.Messages
				got := decode(t, messages[len(messages)-1].ExtractText())
				if !reflect.DeepEqual(got, want) {
					t.Errorf("MCP server received changed numeric arguments:\ngot  %#v\nwant %#v", got, want)
				}
			}
		})
	}
}

func TestToolArgsPrecisionPreservesParsingContract(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		for _, raw := range []string{`null`, `{}`, `{"offset":2}`, `{"offset":2}<|end|>`, `prefix {"offset":2}`} {
			args, err := parseToolArgsWithPrecision(raw, preserve)
			if err != nil || args == nil {
				t.Fatalf("parse %q with preserve=%v: args=%v err=%v", raw, preserve, args, err)
			}
			if value, ok := args["offset"]; ok {
				var want any = float64(2)
				if preserve {
					want = json.Number("2")
				}
				if value != want {
					t.Errorf("parse %q with preserve=%v: offset=%#v (%T), want %#v (%T)", raw, preserve, value, value, want, want)
				}
			}
		}
		for _, raw := range []string{``, `[]`, `null trailing`, `{"offset":`, `{"offset":2,}`, `{"offset":[2}}`} {
			if args, err := parseToolArgsWithPrecision(raw, preserve); err == nil || args != nil {
				t.Errorf("invalid %q accepted with preserve=%v: args=%v err=%v", raw, preserve, args, err)
			}
		}
	}
}
