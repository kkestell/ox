# Benchmark comparison: deepseek-main-docs, deepseek-simpler-docs

## Runs

| Label                 | Commit       | Dirty | Model                          | Effort  | Results |
| --------------------- | ------------ | ----- | ------------------------------ | ------- | ------- |
| deepseek-main-docs    | `7de8ace06a` | no    | `deepseek/deepseek-v4.1-flash` | default | 3       |
| deepseek-simpler-docs | `1118f41980` | no    | `deepseek/deepseek-v4.1-flash` | default | 3       |

## Summary

Changes are relative to `deepseek-main-docs`. A task's values are medians over
its repetitions, with the range in parentheses. Totals are sums of task medians.

| Metric            | deepseek-main-docs | deepseek-simpler-docs | Change |
| ----------------- | -----------------: | --------------------: | -----: |
| passed            |                3/3 |                   3/3 |        |
| seconds           |                198 |                   226 |   +14% |
| requests          |                 43 |                    65 |   +51% |
| tool_calls        |                 64 |                    94 |   +47% |
| failed_calls      |                  2 |                     1 |   -50% |
| cancelled_calls   |                  0 |                     0 |        |
| repeated_calls    |                  0 |                     0 |        |
| input_tokens      |            2013921 |               3329701 |   +65% |
| cached_tokens     |            1960064 |               3131392 |   +60% |
| output_tokens     |              14217 |                 16524 |   +16% |
| reasoning_tokens  |               6453 |                  6009 |    -7% |
| cost              |            $0.0721 |               $0.1532 |  +112% |
| max_input_tokens  |              69380 |                 72331 |    +4% |
| tool_output_chars |             198008 |                193131 |    -2% |
| compactions       |                  0 |                     0 |        |
| summarizer_cost   |            $0.0000 |               $0.0000 |        |
| subagents         |                  0 |                     0 |        |

### Pass rate and cost by task

| Task              | deepseek-main-docs passed | deepseek-simpler-docs passed |   deepseek-main-docs cost | deepseek-simpler-docs cost | Change |
| ----------------- | ------------------------: | ---------------------------: | ------------------------: | -------------------------: | -----: |
| `mini-redis-docs` |                       3/3 |                          3/3 | $0.0721 ($0.0477–$0.1001) |  $0.1532 ($0.0778–$0.1594) |  +112% |

## Tasks

### `mini-redis-docs`

| Metric               |        deepseek-main-docs |     deepseek-simpler-docs | Change |
| -------------------- | ------------------------: | ------------------------: | -----: |
| passed               |                       3/3 |                       3/3 |        |
| status               |                finished 3 |                finished 3 |        |
| seconds              |              198 (79–289) |             226 (176–417) |   +14% |
| requests             |                43 (40–48) |                65 (46–95) |   +51% |
| tool_calls           |                64 (63–67) |               94 (83–103) |   +47% |
| failed_calls         |                   2 (2–3) |                   1 (1–2) |   -50% |
| cancelled_calls      |                         0 |                         0 |        |
| `apply_patch` calls  |                 13 (9–15) |                         0 |  -100% |
| `apply_patch` failed |                         0 |                         0 |        |
| `edit_file` calls    |                         0 |                42 (37–51) |        |
| `edit_file` failed   |                         0 |                         0 |        |
| `read_file` calls    |                38 (35–39) |                38 (38–39) |    +0% |
| `read_file` failed   |                   1 (0–1) |                         0 |  -100% |
| `shell` calls        |                15 (13–17) |                 13 (8–14) |   -13% |
| `shell` failed       |                   1 (1–3) |                   1 (1–2) |    +0% |
| repeated_calls       |                   0 (0–1) |                         0 |        |
| input_tokens         | 2013921 (1800052–2042178) | 3329701 (2260223–4216920) |   +65% |
| cached_tokens        | 1960064 (1518848–1972224) | 3131392 (2024832–4066816) |   +60% |
| output_tokens        |       14217 (12081–17141) |       16524 (15723–17309) |   +16% |
| reasoning_tokens     |         6453 (4900–10170) |          6009 (5766–7020) |    -7% |
| cost                 | $0.0721 ($0.0477–$0.1001) | $0.1532 ($0.0778–$0.1594) |  +112% |
| max_input_tokens     |       69380 (66556–74375) |       72331 (69453–81489) |    +4% |
| tool_output_chars    |    198008 (169444–207859) |    193131 (186307–224372) |    -2% |
| compactions          |                         0 |                         0 |        |
| summarizer_cost      |                   $0.0000 |                   $0.0000 |        |
| subagents            |                         0 |                         0 |        |
