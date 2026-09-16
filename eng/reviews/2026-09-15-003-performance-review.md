# Go performance review

- **Scope:** Uncommitted Go changes in
  `internal/agent/{agent.go,config_options.go,loop.go,state.go,subagent.go}`,
  their changed tests, `internal/e2e/auto_test.go`, and
  `integration/agent_loop_test.go`. Unrelated documentation changes were
  excluded.
- **Mode:** Specific-topic review.
- **Topic:** Performance — request frequency, algorithmic work, allocations,
  copies, and streamed/provider I/O.

## Findings

No confirmed performance findings.

The changed mode behavior does add bounded per-provider-request work when plan
mode is active: `applyMode` clones and filters the declared tool slice and
rebuilds the tool-kind map (`internal/agent/config_options.go:293-303`), while a
subagent plan request filters its tool set before constructing the request
(`internal/agent/subagent.go:102-108`). This is linear in the number of
configured tools and runs once per primary or subagent provider request. It is
not reported as a defect because tool catalogs are small relative to the
request/history payload and provider latency, and the work is required to ensure
the current mode is reflected at each request boundary.

The focused benchmark measured `applyMode` at 388 ns / 1.6 KB for 16 tools, 1.43
µs / 7.3 KB for 64 tools, and 5.57 µs / 28.6 KB for 256 tools on an Apple M4.
These results do not show a material bottleneck for the current call path. The
fingerprint path also computes a mode-filtered configuration once per turn
(`internal/agent/loop.go:546-553`), but it must serialize the prefix for the
diagnostic hash, so avoiding the filtering copy alone would not remove the
dominant work.

## Checks

- `make test` — passed before this review.
- `go test ./internal/agent -run '^$' -bench ApplyModePlanTmp -benchmem -count=1`
  — passed; temporary benchmark removed after measurement.
- `go vet ./...` — passed.
- `staticcheck ./...` — passed.

The benchmark covered the changed plan-mode filtering helper, not
provider/network latency or a full turn benchmark. A future performance
regression should be evaluated with a representative streamed tool-loop
benchmark before introducing caching or shared mutable tool slices.

## Verdict

- **Performance:** no confirmed finding; current added work is linear, measured
  in the low-microsecond range for catalogs up to 256 tools, and remains simple
  and bounded.
