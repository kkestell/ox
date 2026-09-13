package agent

import (
	"context"
	"strings"
	"testing"
)

func TestSubagentWaitReturnsWhenAnySelectedChildFinishes(t *testing.T) {
	group := &subagentGroup{
		children: map[string]*subagent{
			"first":  {id: "first", name: "first", status: subagentStatusRunning},
			"second": {id: "second", name: "second", status: subagentStatusRunning},
		},
		order:   []string{"first", "second"},
		changed: make(chan struct{}),
	}
	result := make(chan []SubagentSnapshot, 1)
	go func() {
		snapshots, err := group.waitFor(context.Background(), []string{"first", "second"})
		if err != nil {
			t.Errorf("wait: %v", err)
			return
		}
		result <- snapshots
	}()

	group.complete("first", subagentStatusCompleted, "done", nil)
	snapshots := <-result
	if len(snapshots) != 2 || snapshots[0].Status != subagentStatusCompleted ||
		snapshots[1].Status != subagentStatusRunning {
		t.Fatalf("snapshots = %#v", snapshots)
	}
}

func TestSubagentResultIsBoundedAndValidUTF8(t *testing.T) {
	value := strings.Repeat("界", maxSubagentResult)
	result := boundedSubagentResult(value)
	if len(result) > maxSubagentResult || !strings.HasSuffix(result, "[subagent result truncated]") {
		t.Fatalf("bounded result = %d bytes, suffix %q", len(result), result[len(result)-32:])
	}
}
