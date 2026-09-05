package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/acp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func webProcessFixture(t *testing.T, handler http.Handler) (string, string) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	return "http://public.test:" + port, parsed.Host
}

func webProcessOptions(model *mockModel, target string) []startOption {
	return []startOption{
		withModel(model),
		withEnvironment("OX_E2E_WEB_FETCH_HOST", "public.test"),
		withEnvironment("OX_E2E_WEB_FETCH_TARGET", target),
	}
}

func TestMCPSearchToWebFetchThroughShippedBinary(t *testing.T) {
	pageURL, target := webProcessFixture(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/html")
		fmt.Fprint(writer, `<html><body><h1>Verified fixture</h1><p>The answer is 42.</p></body></html>`)
	}))
	pageURL += "/answer"

	var searches atomic.Int32
	search := sdk.NewServer(&sdk.Implementation{Name: "search", Version: "1"}, nil)
	search.AddTool(&sdk.Tool{
		Name: "query", Description: "search the web",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}`),
	}, func(_ context.Context, request *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		searches.Add(1)
		return &sdk.CallToolResult{Content: []sdk.Content{
			&sdk.TextContent{Text: "Result: " + pageURL},
		}}, nil
	})
	searchHTTP := httptest.NewServer(sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return search },
		&sdk.StreamableHTTPOptions{JSONResponse: true, Stateless: true},
	))
	defer searchHTTP.Close()

	const searchTool = "mcp__search__query"
	fetchArguments, _ := json.Marshal(map[string]string{"url": pageURL})
	model := startModel(t,
		toolResponse("call-search", searchTool, `{"query":"fixture answer"}`),
		toolResponse("call-fetch", "web_fetch", string(fetchArguments)),
		sse(evText("[Fixture source]("+pageURL+") says 42."), evFinishReason("stop")),
	)
	child := start(t, webProcessOptions(model, target)...)
	initialize(t, child)
	session := newMCPSession(t, child, mcpHTTPServer("search", searchHTTP.URL, ""))
	turn := child.begin("session/prompt", acp.PromptRequest{
		SessionID: session, Prompt: textPrompt("search, fetch, and answer with a source"),
	})
	searchMessage := child.serverRequest()
	searchPermission := permissionRequest(t, searchMessage, "call-search")
	if searchPermission.ToolCall.Name != searchTool {
		t.Fatalf("search permission = %#v", searchPermission.ToolCall)
	}
	child.respond(searchMessage, acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
		Outcome: "selected", OptionID: "allow_once",
	}})
	fetchMessage := child.serverRequest()
	fetchPermission := permissionRequest(t, fetchMessage, "call-fetch")
	if fetchPermission.ToolCall.Name != "web_fetch" || fetchPermission.ToolCall.Title != "Fetch "+pageURL {
		t.Fatalf("fetch permission = %#v", fetchPermission.ToolCall)
	}
	child.respond(fetchMessage, acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
		Outcome: "selected", OptionID: "allow_once",
	}})
	if response := promptResponse(t, child.result(child.await(turn))); response.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("stop reason = %q", response.StopReason)
	}
	var linked bool
	for _, notification := range updates(t, child, session) {
		if strings.Contains(notification.Update.Content.Text, "[Fixture source]("+pageURL+")") {
			linked = true
		}
	}
	if !linked {
		t.Fatal("final answer omitted fetched source link")
	}
	if searches.Load() != 1 {
		t.Fatalf("searches = %d", searches.Load())
	}
	requests := model.requests()
	if len(requests) != 3 || !requestHasTool(requests[0], searchTool) ||
		!requestHasTool(requests[0], "web_fetch") ||
		!requestContainsText(requests[1], pageURL) ||
		!requestContainsText(requests[2], "Final URL: "+pageURL) ||
		!requestContainsText(requests[2], "<untrusted_web_source>") ||
		!requestContainsText(requests[2], "The answer is 42") {
		t.Fatalf("search-to-fetch model requests = %#v", requests)
	}
}

func TestWebFetchWithoutSearchAndCancellationThroughShippedBinary(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	baseURL, target := webProcessFixture(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
		close(cancelled)
	}))
	fetchArguments, _ := json.Marshal(map[string]string{"url": baseURL + "/wait"})
	model := startModel(t)
	model.queueFor("cancel fetch", toolResponse("call-fetch-wait", "web_fetch", string(fetchArguments)))
	model.queueFor("other prompt", sse(evText("other done"), evFinishReason("stop")))
	child := start(t, webProcessOptions(model, target)...)
	initialize(t, child)
	first := newSession(t, child, child.cwd)
	second := newSession(t, child, child.cwd)
	turn := child.begin("session/prompt", acp.PromptRequest{
		SessionID: first, Prompt: textPrompt("cancel fetch"),
	})
	allowPermission(t, child, "call-fetch-wait", true)
	select {
	case <-started:
	case <-time.After(readTimeout):
		t.Fatal("web fetch did not start")
	}
	prompt(t, child, second, "other prompt")
	_ = updates(t, child, second)
	child.notify("session/cancel", acp.CancelNotification{SessionID: first})
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("web request context was not cancelled")
	}
	if response := promptResponse(t, child.result(child.await(turn))); response.StopReason != acp.StopReasonCancelled {
		t.Fatalf("cancelled stop reason = %q", response.StopReason)
	}
	_ = updates(t, child, first)

	requests := model.requests()
	if len(requests) != 2 || !requestHasTool(requests[0], "web_fetch") ||
		requestHasMCPTool(requests[0]) ||
		!requestContainsText(requests[1], "other prompt") {
		t.Fatalf("requests without search = %#v", requests)
	}
}

func requestHasMCPTool(request modelRequest) bool {
	for _, tool := range request.Tools {
		if strings.Contains(string(tool), `"name":"mcp__`) {
			return true
		}
	}
	return false
}
