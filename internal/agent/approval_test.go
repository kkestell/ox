package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/openrouter"
)

func TestPermissionOptionsAndDecisions(t *testing.T) {
	options := permissionOptions("", false)
	if len(options) != 3 {
		t.Fatalf("options = %#v", options)
	}
	for index, want := range []struct {
		id   string
		kind acp.PermissionOptionKind
	}{
		{permissionAllowOnceID, acp.PermissionOptionAllowOnce},
		{permissionAllowAlwaysID, acp.PermissionOptionAllowAlways},
		{permissionRejectOnceID, acp.PermissionOptionRejectOnce},
	} {
		if options[index].OptionID != want.id ||
			options[index].Kind != want.kind {
			t.Fatalf("option %d = %#v", index, options[index])
		}
	}
	ruleOptions := permissionOptions("go test", true)
	if len(ruleOptions) != 3 ||
		ruleOptions[1].Name != `Allow "go test" for this session` {
		t.Fatalf("rule options = %#v", ruleOptions)
	}
	unsuggested := permissionOptions("", true)
	if len(unsuggested) != 2 ||
		unsuggested[0].OptionID != permissionAllowOnceID ||
		unsuggested[1].OptionID != permissionRejectOnceID {
		t.Fatalf("unsuggested options = %#v", unsuggested)
	}

	tests := []struct {
		name     string
		response acp.RequestPermissionResponse
		err      error
		want     approvalDecision
	}{
		{
			name:     "allow once",
			response: permissionResponse("selected", permissionAllowOnceID),
			want:     decisionAllowOnce,
		},
		{
			name:     "allow always",
			response: permissionResponse("selected", permissionAllowAlwaysID),
			want:     decisionAllowAlways,
		},
		{
			name:     "reject",
			response: permissionResponse("selected", permissionRejectOnceID),
			want:     decisionRefused,
		},
		{
			name:     "cancelled outcome",
			response: permissionResponse("cancelled", ""),
			want:     decisionCancelled,
		},
		{
			name:     "unknown option",
			response: permissionResponse("selected", "unknown"),
			want:     decisionRefused,
		},
		{
			name:     "unknown outcome",
			response: permissionResponse("future", ""),
			want:     decisionRefused,
		},
		{name: "rpc error", err: errors.New("client failed"), want: decisionRefused},
		{
			name: "context error",
			err:  errors.Join(errors.New("callback failed"), context.Canceled),
			want: decisionCancelled,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := decideApproval(test.response, test.err); got != test.want {
				t.Fatalf("decision = %q, want %q", got, test.want)
			}
		})
	}
}

func TestExecuteBatchAsksForZeroValueAndSkipsUnknownTools(t *testing.T) {
	var gatedExecutions atomic.Int32
	var safeExecutions atomic.Int32
	instance, err := New(Config{
		Logger: discardLogger(),
		Tools: []Tool{
			{
				Name:        "gated",
				InputSchema: json.RawMessage(`{"type":"object"}`),
				Execute: func(context.Context, Invocation) (string, error) {
					gatedExecutions.Add(1)
					return "gated", nil
				},
			},
			{
				Name:        "safe",
				InputSchema: json.RawMessage(`{"type":"object"}`),
				Approval:    ApprovalNone,
				Execute: func(context.Context, Invocation) (string, error) {
					safeExecutions.Add(1)
					return "safe", nil
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var asked []string
	events := make(chan event, 16)
	results := instance.executeBatch(
		context.Background(),
		ephemeralToolSession(t),
		[]openrouter.ToolCall{
			toolCall("1", "gated"),
			toolCall("2", "safe"),
			toolCall("3", "missing"),
			toolCall("4", "gated"),
		},
		func(
			_ context.Context,
			request acp.RequestPermissionRequest,
		) (acp.RequestPermissionResponse, error) {
			asked = append(asked, request.ToolCall.ToolCallID)
			return permissionResponse("selected", permissionAllowOnceID), nil
		},
		events,
	)

	if len(asked) != 2 || asked[0] != "1" || asked[1] != "4" {
		t.Fatalf("approval requests = %#v", asked)
	}
	if gatedExecutions.Load() != 2 || safeExecutions.Load() != 1 {
		t.Fatalf(
			"executions = gated:%d safe:%d",
			gatedExecutions.Load(),
			safeExecutions.Load(),
		)
	}
	if !results[2].failed || results[2].content != `unknown tool "missing"` {
		t.Fatalf("unknown result = %#v", results[2])
	}
}

func TestExecuteBatchScopesAllowAlwaysToToolRules(t *testing.T) {
	var executions atomic.Int32
	tool := Tool{
		Name:        "ruled",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Suggest: func(arguments json.RawMessage) string {
			var value struct {
				Command string `json:"command"`
			}
			_ = json.Unmarshal(arguments, &value)
			if value.Command == "" {
				return ""
			}
			return "go test"
		},
		Covered: func(rules []string, arguments json.RawMessage) bool {
			var value struct {
				Command string `json:"command"`
			}
			_ = json.Unmarshal(arguments, &value)
			return value.Command == "go test ./internal/agent" &&
				len(rules) == 1 && rules[0] == "go test"
		},
		Execute: func(context.Context, Invocation) (string, error) {
			executions.Add(1)
			return "done", nil
		},
	}
	instance, err := New(Config{Logger: discardLogger(), Tools: []Tool{tool}})
	if err != nil {
		t.Fatal(err)
	}
	var requests []acp.RequestPermissionRequest
	results := instance.executeBatch(
		context.Background(),
		ephemeralToolSession(t),
		[]openrouter.ToolCall{
			{
				ID: "1",
				Function: openrouter.ToolCallFunction{
					Name:      "ruled",
					Arguments: `{"command":"go test ./..."}`,
				},
			},
			{
				ID: "2",
				Function: openrouter.ToolCallFunction{
					Name:      "ruled",
					Arguments: `{"command":"go test ./internal/agent"}`,
				},
			},
			{
				ID: "3",
				Function: openrouter.ToolCallFunction{
					Name:      "ruled",
					Arguments: `{"command":"rm -rf /"}`,
				},
			},
		},
		func(
			_ context.Context,
			request acp.RequestPermissionRequest,
		) (acp.RequestPermissionResponse, error) {
			requests = append(requests, request)
			if len(requests) == 1 {
				return permissionResponse("selected", permissionAllowAlwaysID), nil
			}
			return permissionResponse("selected", permissionAllowOnceID), nil
		},
		make(chan event, 32),
	)
	if len(requests) != 2 ||
		requests[0].ToolCall.ToolCallID != "1" ||
		requests[1].ToolCall.ToolCallID != "3" {
		t.Fatalf("requests = %#v", requests)
	}
	if requests[0].Options[1].Name != `Allow "go test" for this session` {
		t.Fatalf("rule option = %#v", requests[0].Options)
	}
	if executions.Load() != 3 {
		t.Fatalf("executions = %d", executions.Load())
	}
	for _, result := range results {
		if result.failed {
			t.Fatalf("results = %#v", results)
		}
	}
}

func TestExecuteBatchOmitsUnscopedAllowAlways(t *testing.T) {
	var executions atomic.Int32
	tool := Tool{
		Name:        "ruled",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Suggest:     func(json.RawMessage) string { return "" },
		Covered:     func([]string, json.RawMessage) bool { return false },
		Execute: func(context.Context, Invocation) (string, error) {
			executions.Add(1)
			return "done", nil
		},
	}
	instance, err := New(Config{Logger: discardLogger(), Tools: []Tool{tool}})
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	results := instance.executeBatch(
		context.Background(),
		ephemeralToolSession(t),
		[]openrouter.ToolCall{toolCall("1", "ruled"), toolCall("2", "ruled")},
		func(
			_ context.Context,
			request acp.RequestPermissionRequest,
		) (acp.RequestPermissionResponse, error) {
			requests++
			if len(request.Options) != 2 {
				t.Fatalf("options = %#v", request.Options)
			}
			return permissionResponse("selected", permissionAllowAlwaysID), nil
		},
		make(chan event, 16),
	)
	if requests != 2 || executions.Load() != 2 {
		t.Fatalf("requests = %d, executions = %d", requests, executions.Load())
	}
	for _, result := range results {
		if result.failed {
			t.Fatalf("results = %#v", results)
		}
	}
}

func TestConcurrentBatchesSerializeApprovalAndRecheckGrant(t *testing.T) {
	var executions atomic.Int32
	tool := Tool{
		Name:        "gated",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Execute: func(context.Context, Invocation) (string, error) {
			executions.Add(1)
			return "done", nil
		},
	}
	instance, err := New(Config{Logger: discardLogger(), Tools: []Tool{tool}})
	if err != nil {
		t.Fatal(err)
	}
	value := ephemeralToolSession(t)
	asked := make(chan string, 2)
	release := make(chan struct{})
	ask := func(
		_ context.Context,
		request acp.RequestPermissionRequest,
	) (acp.RequestPermissionResponse, error) {
		asked <- request.ToolCall.ToolCallID
		<-release
		return permissionResponse("selected", permissionAllowAlwaysID), nil
	}
	done := make(chan []toolResult, 2)
	go func() {
		done <- instance.executeBatch(
			context.Background(),
			value,
			[]openrouter.ToolCall{toolCall("first", "gated")},
			ask,
			make(chan event, 8),
		)
	}()
	if call := <-asked; call != "first" {
		t.Fatalf("first approval = %q", call)
	}
	go func() {
		done <- instance.executeBatch(
			context.Background(),
			value,
			[]openrouter.ToolCall{toolCall("second", "gated")},
			ask,
			make(chan event, 8),
		)
	}()
	select {
	case call := <-asked:
		t.Fatalf("second approval was concurrently outstanding: %q", call)
	default:
	}
	close(release)
	for range 2 {
		results := <-done
		if len(results) != 1 || results[0].failed {
			t.Fatalf("results = %#v", results)
		}
	}
	select {
	case call := <-asked:
		t.Fatalf("waiting call ignored the shared grant: %q", call)
	default:
	}
	if executions.Load() != 2 {
		t.Fatalf("executions = %d", executions.Load())
	}
}

func permissionResponse(outcome, optionID string) acp.RequestPermissionResponse {
	return acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
		Outcome:  outcome,
		OptionID: optionID,
	}}
}
