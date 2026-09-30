# Benchmark comparison: muse-main-docs, muse-simpler-docs

## Runs

| Label             | Commit       | Dirty | Model                             | Effort  | Results |
| ----------------- | ------------ | ----- | --------------------------------- | ------- | ------- |
| muse-main-docs    | `7de8ace06a` | no    | `meta/muse-spark-1.3-contributor` | default | 3       |
| muse-simpler-docs | `1118f41980` | no    | `meta/muse-spark-1.3-contributor` | default | 3       |

## Summary

Changes are relative to `muse-main-docs`. A task's values are medians over its
repetitions, with the range in parentheses. Totals are sums of task medians.

| Metric            | muse-main-docs | muse-simpler-docs | Change |
| ----------------- | -------------: | ----------------: | -----: |
| passed            |            3/3 |               3/3 |        |
| seconds           |            577 |               583 |    +1% |
| requests          |             64 |                78 |   +22% |
| tool_calls        |             96 |               113 |   +18% |
| failed_calls      |              0 |                 1 |        |
| cancelled_calls   |              0 |                 0 |        |
| repeated_calls    |              0 |                 0 |        |
| input_tokens      |        4533955 |           4917105 |    +8% |
| cached_tokens     |        1090601 |           1621373 |   +49% |
| output_tokens     |          19499 |             19604 |    +1% |
| reasoning_tokens  |           7760 |              6907 |   -11% |
| cost              |        $0.3582 |           $0.3364 |    -6% |
| max_input_tokens  |          88193 |             84646 |    -4% |
| tool_output_chars |         237437 |            228613 |    -4% |
| compactions       |              0 |                 0 |        |
| summarizer_cost   |        $0.0000 |           $0.0000 |        |
| subagents         |              0 |                 0 |        |

### Pass rate and cost by task

| Task              | muse-main-docs passed | muse-simpler-docs passed |       muse-main-docs cost |    muse-simpler-docs cost | Change |
| ----------------- | --------------------: | -----------------------: | ------------------------: | ------------------------: | -----: |
| `mini-redis-docs` |                   3/3 |                      3/3 | $0.3582 ($0.2986–$0.3961) | $0.3364 ($0.2921–$0.3459) |    -6% |

## Tasks

### `mini-redis-docs`

| Metric               |            muse-main-docs |         muse-simpler-docs | Change |
| -------------------- | ------------------------: | ------------------------: | -----: |
| passed               |                       3/3 |                       3/3 |        |
| status               |                finished 3 |                finished 3 |        |
| seconds              |             577 (524–728) |             583 (547–606) |    +1% |
| requests             |                64 (58–81) |                78 (66–79) |   +22% |
| tool_calls           |               96 (90–113) |              113 (97–116) |   +18% |
| failed_calls         |                   0 (0–4) |                   1 (0–1) |        |
| cancelled_calls      |                         0 |                         0 |        |
| `apply_patch` calls  |                39 (31–45) |                         0 |  -100% |
| `apply_patch` failed |                   0 (0–4) |                         0 |        |
| `edit_file` calls    |                         0 |                39 (38–45) |        |
| `edit_file` failed   |                         0 |                         0 |        |
| `glob` calls         |                   1 (1–2) |                   1 (0–1) |    +0% |
| `glob` failed        |                         0 |                         0 |        |
| `grep` calls         |                         0 |                   0 (0–1) |        |
| `grep` failed        |                         0 |                         0 |        |
| `read_file` calls    |                50 (38–52) |                50 (44–62) |    +0% |
| `read_file` failed   |                         0 |                         0 |        |
| `shell` calls        |                12 (11–17) |                13 (11–17) |    +8% |
| `shell` failed       |                         0 |                   1 (0–1) |        |
| `write_file` calls   |                         0 |                   1 (1–2) |        |
| `write_file` failed  |                         0 |                         0 |        |
| repeated_calls       |                   0 (0–1) |                   0 (0–2) |        |
| input_tokens         | 4533955 (4017758–5637050) | 4917105 (4324899–5597223) |    +8% |
| cached_tokens        | 1090601 (1011407–1755487) | 1621373 (1472945–2226798) |   +49% |
| output_tokens        |       19499 (18555–22002) |       19604 (17934–22261) |    +1% |
| reasoning_tokens     |          7760 (6959–8866) |          6907 (5308–8325) |   -11% |
| cost                 | $0.3582 ($0.2986–$0.3961) | $0.3364 ($0.2921–$0.3459) |    -6% |
| max_input_tokens     |       88193 (85795–96215) |       84646 (82817–94693) |    -4% |
| tool_output_chars    |    237437 (232348–270415) |    228613 (228430–254280) |    -4% |
| compactions          |                         0 |                         0 |        |
| summarizer_cost      |                   $0.0000 |                   $0.0000 |        |
| subagents            |                         0 |                         0 |        |
