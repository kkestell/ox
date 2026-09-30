# Benchmark comparison: muse-main, muse-simpler

## Runs

| Label        | Commit       | Dirty | Model                             | Effort  | Results |
| ------------ | ------------ | ----- | --------------------------------- | ------- | ------- |
| muse-main    | `82d15b17c1` | no    | `meta/muse-spark-1.3-contributor` | default | 33      |
| muse-simpler | `2596cf26c4` | no    | `meta/muse-spark-1.3-contributor` | default | 33      |

## Summary

Changes are relative to `muse-main`. A task's values are medians over its
repetitions, with the range in parentheses. Totals are sums of task medians.

| Metric            | muse-main | muse-simpler | Change |
| ----------------- | --------: | -----------: | -----: |
| passed            |     33/33 |        33/33 |        |
| seconds           |       774 |          766 |    -1% |
| requests          |       117 |          113 |    -3% |
| tool_calls        |       142 |          141 |    -1% |
| failed_calls      |         7 |            2 |   -71% |
| cancelled_calls   |         0 |            0 |        |
| repeated_calls    |         0 |            1 |        |
| input_tokens      |   1635202 |      1621327 |    -1% |
| cached_tokens     |    374181 |       385851 |    +3% |
| output_tokens     |     53515 |        45382 |   -15% |
| reasoning_tokens  |     21629 |        18050 |   -17% |
| cost              |   $0.1386 |      $0.1387 |    +0% |
| max_input_tokens  |    201108 |       181925 |   -10% |
| tool_output_chars |    341142 |       326027 |    -4% |
| compactions       |         0 |            0 |        |
| summarizer_cost   |   $0.0000 |      $0.0000 |        |
| subagents         |         0 |            0 |        |

### Pass rate and cost by task

| Task                  | muse-main passed | muse-simpler passed |            muse-main cost |         muse-simpler cost | Change |
| --------------------- | ---------------: | ------------------: | ------------------------: | ------------------------: | -----: |
| `coffee-site`         |              3/3 |                 3/3 | $0.0051 ($0.0035–$0.0059) | $0.0024 ($0.0024–$0.0029) |   -53% |
| `csv-stats`           |              3/3 |                 3/3 | $0.0037 ($0.0031–$0.0107) | $0.0064 ($0.0043–$0.0064) |   +73% |
| `inih-quoted`         |              3/3 |                 3/3 | $0.0322 ($0.0224–$0.0642) | $0.0320 ($0.0147–$0.0393) |    -1% |
| `itoa-boundaries`     |              3/3 |                 3/3 | $0.0073 ($0.0035–$0.0086) | $0.0075 ($0.0053–$0.0094) |    +2% |
| `jsmn-rename`         |              3/3 |                 3/3 | $0.0126 ($0.0117–$0.0200) | $0.0097 ($0.0057–$0.0139) |   -23% |
| `pi-digit`            |              3/3 |                 3/3 | $0.0087 ($0.0061–$0.0152) | $0.0048 ($0.0046–$0.0060) |   -45% |
| `sds-startswith`      |              3/3 |                 3/3 | $0.0107 ($0.0091–$0.0148) | $0.0100 ($0.0084–$0.0120) |    -7% |
| `temp-cli`            |              3/3 |                 3/3 | $0.0039 ($0.0028–$0.0075) | $0.0025 ($0.0024–$0.0042) |   -36% |
| `tinyexpr-clamp`      |              3/3 |                 3/3 | $0.0137 ($0.0134–$0.0160) | $0.0215 ($0.0163–$0.0253) |   +57% |
| `tinyexpr-precedence` |              3/3 |                 3/3 | $0.0365 ($0.0299–$0.0479) | $0.0388 ($0.0262–$0.0666) |    +6% |
| `todo-cli`            |              3/3 |                 3/3 | $0.0043 ($0.0024–$0.0063) | $0.0033 ($0.0032–$0.0063) |   -24% |

## Tasks

### `coffee-site`

| Metric                 |                 muse-main |              muse-simpler | Change |
| ---------------------- | ------------------------: | ------------------------: | -----: |
| passed                 |                       3/3 |                       3/3 |        |
| status                 |                finished 3 |                finished 3 |        |
| seconds                |                48 (48–67) |                37 (31–40) |   -24% |
| requests               |                 11 (7–11) |                   6 (6–8) |   -45% |
| tool_calls             |                 10 (6–10) |                   7 (7–9) |   -30% |
| failed_calls           |                   1 (0–1) |                         0 |  -100% |
| cancelled_calls        |                         0 |                         0 |        |
| `apply_patch` calls    |                         1 |                         0 |  -100% |
| `apply_patch` failed   |                   1 (0–1) |                         0 |  -100% |
| `glob` calls           |                   0 (0–1) |                         0 |        |
| `glob` failed          |                         0 |                         0 |        |
| `shell` calls          |                   7 (4–7) |                   3 (3–4) |   -57% |
| `shell` failed         |                         0 |                         0 |        |
| `shell_process` calls  |                   1 (1–2) |                   1 (1–2) |    +0% |
| `shell_process` failed |                         0 |                         0 |        |
| `write_file` calls     |                         0 |                         3 |        |
| `write_file` failed    |                         0 |                         0 |        |
| repeated_calls         |                         0 |                         0 |        |
| input_tokens           |       77070 (38400–82473) |       29616 (28531–42307) |   -62% |
| cached_tokens          |        32347 (9623–36443) |        11942 (9781–19336) |   -63% |
| output_tokens          |          4302 (2876–4858) |          2857 (2646–2889) |   -34% |
| reasoning_tokens       |             278 (209–779) |             183 (176–195) |   -34% |
| cost                   | $0.0051 ($0.0035–$0.0059) | $0.0024 ($0.0024–$0.0029) |   -53% |
| max_input_tokens       |        10063 (6962–10303) |          6500 (6214–6836) |   -35% |
| tool_output_chars      |          5679 (1971–6334) |          2071 (1645–2302) |   -64% |
| compactions            |                         0 |                         0 |        |
| summarizer_cost        |                   $0.0000 |                   $0.0000 |        |
| subagents              |                         0 |                         0 |        |

### `csv-stats`

| Metric               |                 muse-main |              muse-simpler | Change |
| -------------------- | ------------------------: | ------------------------: | -----: |
| passed               |                       3/3 |                       3/3 |        |
| status               |                finished 3 |                finished 3 |        |
| seconds              |               87 (78–102) |               86 (65–104) |    -1% |
| requests             |                  6 (5–10) |                   8 (7–8) |   +33% |
| tool_calls           |                   5 (4–9) |                        10 |  +100% |
| failed_calls         |                         1 |                   0 (0–1) |  -100% |
| cancelled_calls      |                         0 |                         0 |        |
| `apply_patch` calls  |                         1 |                         0 |  -100% |
| `apply_patch` failed |                   0 (0–1) |                         0 |        |
| `shell` calls        |                   4 (3–8) |                   5 (4–5) |   +25% |
| `shell` failed       |                   1 (0–1) |                   0 (0–1) |  -100% |
| `write_file` calls   |                         0 |                   5 (5–6) |        |
| `write_file` failed  |                         0 |                         0 |        |
| repeated_calls       |                         0 |                         0 |        |
| input_tokens         |      41375 (38638–107800) |       64178 (43607–74766) |   +55% |
| cached_tokens        |       22378 (15398–22837) |       14728 (10519–29704) |   -34% |
| output_tokens        |         7496 (5252–10379) |          6928 (4644–9365) |    -8% |
| reasoning_tokens     |          4089 (1912–4193) |          3167 (1555–3462) |   -23% |
| cost                 | $0.0037 ($0.0031–$0.0107) | $0.0064 ($0.0043–$0.0064) |   +73% |
| max_input_tokens     |        11181 (9447–14775) |        10657 (8152–12894) |    -5% |
| tool_output_chars    |          3396 (1992–3828) |          1981 (1737–2378) |   -42% |
| compactions          |                         0 |                         0 |        |
| summarizer_cost      |                   $0.0000 |                   $0.0000 |        |
| subagents            |                         0 |                         0 |        |

### `inih-quoted`

| Metric               |                 muse-main |              muse-simpler | Change |
| -------------------- | ------------------------: | ------------------------: | -----: |
| passed               |                       3/3 |                       3/3 |        |
| status               |                finished 3 |                finished 3 |        |
| seconds              |             121 (120–189) |              103 (91–197) |   -15% |
| requests             |                20 (14–27) |                16 (13–20) |   -20% |
| tool_calls           |                32 (26–34) |                26 (21–29) |   -19% |
| failed_calls         |                   1 (0–1) |                         0 |  -100% |
| cancelled_calls      |                         0 |                         0 |        |
| `apply_patch` calls  |                   2 (1–4) |                         0 |  -100% |
| `apply_patch` failed |                         0 |                         0 |        |
| `edit_file` calls    |                         0 |                   1 (1–2) |        |
| `edit_file` failed   |                         0 |                         0 |        |
| `glob` calls         |                   1 (1–2) |                         1 |    +0% |
| `glob` failed        |                         0 |                         0 |        |
| `grep` calls         |                   1 (0–1) |                   1 (0–1) |    +0% |
| `grep` failed        |                         0 |                         0 |        |
| `read_file` calls    |                17 (12–18) |                13 (10–14) |   -24% |
| `read_file` failed   |                         0 |                         0 |        |
| `shell` calls        |                 11 (5–16) |                  9 (7–11) |   -18% |
| `shell` failed       |                   1 (0–1) |                         0 |  -100% |
| `write_file` calls   |                         0 |                         1 |        |
| `write_file` failed  |                         0 |                         0 |        |
| repeated_calls       |                         0 |                         0 |        |
| input_tokens         |    390268 (252182–816441) |    343026 (227953–515181) |   -12% |
| cached_tokens        |      85844 (42158–197995) |      94653 (36880–144468) |   +10% |
| output_tokens        |          7698 (6467–9614) |          6423 (5823–9523) |   -17% |
| reasoning_tokens     |          3280 (3022–4631) |          3479 (3462–5245) |    +6% |
| cost                 | $0.0322 ($0.0224–$0.0642) | $0.0320 ($0.0147–$0.0393) |    -1% |
| max_input_tokens     |       27303 (25968–41951) |       31059 (26373–35564) |   +14% |
| tool_output_chars    |       52797 (52132–93365) |       70706 (59176–76293) |   +34% |
| compactions          |                         0 |                         0 |        |
| summarizer_cost      |                   $0.0000 |                   $0.0000 |        |
| subagents            |                         0 |                         0 |        |

### `itoa-boundaries`

| Metric               |                 muse-main |              muse-simpler | Change |
| -------------------- | ------------------------: | ------------------------: | -----: |
| passed               |                       3/3 |                       3/3 |        |
| status               |                finished 3 |                finished 3 |        |
| seconds              |                55 (37–61) |                52 (50–68) |    -4% |
| requests             |                   9 (6–9) |                   9 (7–9) |    +0% |
| tool_calls           |                 10 (7–11) |                 10 (8–10) |    +0% |
| failed_calls         |                         0 |                         0 |        |
| cancelled_calls      |                         0 |                         0 |        |
| `apply_patch` calls  |                         1 |                         0 |  -100% |
| `apply_patch` failed |                         0 |                         0 |        |
| `edit_file` calls    |                         0 |                   2 (1–2) |        |
| `edit_file` failed   |                         0 |                         0 |        |
| `glob` calls         |                         1 |                         1 |    +0% |
| `glob` failed        |                         0 |                         0 |        |
| `read_file` calls    |                   5 (4–6) |                         5 |    +0% |
| `read_file` failed   |                         0 |                         0 |        |
| `shell` calls        |                   3 (1–3) |                   2 (1–2) |   -33% |
| `shell` failed       |                         0 |                         0 |        |
| repeated_calls       |                         0 |                   0 (0–1) |        |
| input_tokens         |      97069 (45703–100423) |       91262 (67364–91537) |    -6% |
| cached_tokens        |       22777 (15654–31097) |        20503 (3833–23545) |   -10% |
| output_tokens        |          3147 (2510–3928) |          3241 (2688–3250) |    +3% |
| reasoning_tokens     |          1264 (1155–2010) |          1352 (1090–1551) |    +7% |
| cost                 | $0.0073 ($0.0035–$0.0086) | $0.0075 ($0.0053–$0.0094) |    +2% |
| max_input_tokens     |       15949 (11326–16552) |       14289 (14217–14448) |   -10% |
| tool_output_chars    |       30793 (19588–30975) |       27808 (27273–28587) |   -10% |
| compactions          |                         0 |                         0 |        |
| summarizer_cost      |                   $0.0000 |                   $0.0000 |        |
| subagents            |                         0 |                         0 |        |

### `jsmn-rename`

| Metric             |                 muse-main |              muse-simpler | Change |
| ------------------ | ------------------------: | ------------------------: | -----: |
| passed             |                       3/3 |                       3/3 |        |
| status             |                finished 3 |                finished 3 |        |
| seconds            |                59 (44–66) |                64 (27–67) |    +9% |
| requests           |                11 (10–13) |                   9 (8–9) |   -18% |
| tool_calls         |                15 (14–19) |                 15 (9–15) |    +0% |
| failed_calls       |                         0 |                         0 |        |
| cancelled_calls    |                         0 |                         0 |        |
| `glob` calls       |                         1 |                         1 |    +0% |
| `glob` failed      |                         0 |                         0 |        |
| `grep` calls       |                   1 (1–2) |                         1 |    +0% |
| `grep` failed      |                         0 |                         0 |        |
| `read_file` calls  |                   9 (6–9) |                   8 (3–8) |   -11% |
| `read_file` failed |                         0 |                         0 |        |
| `shell` calls      |                   6 (4–7) |                   5 (4–5) |   -17% |
| `shell` failed     |                         0 |                         0 |        |
| repeated_calls     |                         0 |                         0 |        |
| input_tokens       |    158374 (151007–227862) |     104955 (76313–156680) |   -34% |
| cached_tokens      |       34365 (29034–46939) |       21896 (12409–23545) |   -36% |
| output_tokens      |          2550 (1883–2823) |          2053 (1254–2480) |   -19% |
| reasoning_tokens   |            851 (396–1043) |             572 (213–906) |   -33% |
| cost               | $0.0126 ($0.0117–$0.0200) | $0.0097 ($0.0057–$0.0139) |   -23% |
| max_input_tokens   |       21975 (20868–23354) |       15961 (14823–22991) |   -27% |
| tool_output_chars  |       48610 (45174–49901) |       32213 (31574–51605) |   -34% |
| compactions        |                         0 |                         0 |        |
| summarizer_cost    |                   $0.0000 |                   $0.0000 |        |
| subagents          |                         0 |                         0 |        |

### `pi-digit`

| Metric               |                 muse-main |              muse-simpler | Change |
| -------------------- | ------------------------: | ------------------------: | -----: |
| passed               |                       3/3 |                       3/3 |        |
| status               |                finished 3 |                finished 3 |        |
| seconds              |              102 (81–245) |                87 (73–97) |   -15% |
| requests             |                  8 (8–13) |                   7 (6–8) |   -12% |
| tool_calls           |                  8 (7–12) |                   7 (6–8) |   -12% |
| failed_calls         |                   2 (0–2) |                   0 (0–1) |  -100% |
| cancelled_calls      |                         0 |                         0 |        |
| `apply_patch` calls  |                   1 (1–2) |                         0 |  -100% |
| `apply_patch` failed |                   0 (0–1) |                         0 |        |
| `grep` calls         |                   0 (0–1) |                         0 |        |
| `grep` failed        |                         0 |                         0 |        |
| `shell` calls        |                  6 (6–10) |                   4 (3–5) |   -33% |
| `shell` failed       |                   1 (0–2) |                   0 (0–1) |  -100% |
| `write_file` calls   |                         0 |                         3 |        |
| `write_file` failed  |                         0 |                         0 |        |
| repeated_calls       |                         0 |                         0 |        |
| input_tokens         |      82001 (63961–166909) |       53649 (45961–61784) |   -35% |
| cached_tokens        |       16392 (12296–46013) |       16038 (14984–19863) |    -2% |
| output_tokens        |         8282 (6794–15093) |          6843 (6412–7720) |   -17% |
| reasoning_tokens     |          4229 (3675–9502) |          4051 (3909–4434) |    -4% |
| cost                 | $0.0087 ($0.0061–$0.0152) | $0.0048 ($0.0046–$0.0060) |   -45% |
| max_input_tokens     |       13592 (10410–19257) |        10147 (9811–10543) |   -25% |
| tool_output_chars    |          3113 (1495–6604) |           1317 (960–1459) |   -58% |
| compactions          |                         0 |                         0 |        |
| summarizer_cost      |                   $0.0000 |                   $0.0000 |        |
| subagents            |                         0 |                         0 |        |

### `sds-startswith`

| Metric               |                 muse-main |              muse-simpler | Change |
| -------------------- | ------------------------: | ------------------------: | -----: |
| passed               |                       3/3 |                       3/3 |        |
| status               |                finished 3 |                finished 3 |        |
| seconds              |                46 (42–64) |                59 (27–76) |   +30% |
| requests             |                 10 (9–12) |                 10 (9–13) |    +0% |
| tool_calls           |                13 (13–15) |                12 (12–16) |    -8% |
| failed_calls         |                         0 |                   0 (0–1) |        |
| cancelled_calls      |                         0 |                         0 |        |
| `apply_patch` calls  |                   3 (3–4) |                         0 |  -100% |
| `apply_patch` failed |                         0 |                         0 |        |
| `edit_file` calls    |                         0 |                   3 (3–4) |        |
| `edit_file` failed   |                         0 |                         0 |        |
| `glob` calls         |                         1 |                         1 |    +0% |
| `glob` failed        |                         0 |                         0 |        |
| `grep` calls         |                         1 |                   1 (1–2) |    +0% |
| `grep` failed        |                         0 |                         0 |        |
| `read_file` calls    |                   7 (7–8) |                   6 (6–7) |   -14% |
| `read_file` failed   |                         0 |                         0 |        |
| `shell` calls        |                         1 |                   1 (1–2) |    +0% |
| `shell` failed       |                         0 |                   0 (0–1) |        |
| repeated_calls       |                         0 |                         0 |        |
| input_tokens         |    126927 (114046–173414) |    112576 (104265–159901) |   -11% |
| cached_tokens        |       29049 (26218–32588) |       26361 (17898–47052) |    -9% |
| output_tokens        |          2860 (2810–3200) |          2603 (2288–3158) |    -9% |
| reasoning_tokens     |            999 (919–1089) |             710 (589–952) |   -29% |
| cost                 | $0.0107 ($0.0091–$0.0148) | $0.0100 ($0.0084–$0.0120) |    -7% |
| max_input_tokens     |       18643 (17591–20775) |       17429 (16157–17927) |    -7% |
| tool_output_chars    |       38298 (34156–45015) |       36238 (33536–36481) |    -5% |
| compactions          |                         0 |                         0 |        |
| summarizer_cost      |                   $0.0000 |                   $0.0000 |        |
| subagents            |                         0 |                         0 |        |

### `temp-cli`

| Metric               |                 muse-main |              muse-simpler | Change |
| -------------------- | ------------------------: | ------------------------: | -----: |
| passed               |                       3/3 |                       3/3 |        |
| status               |                finished 3 |                finished 3 |        |
| seconds              |                50 (39–60) |                43 (42–45) |   -13% |
| requests             |                 10 (8–13) |                  9 (8–10) |   -10% |
| tool_calls           |                  9 (7–12) |                   9 (8–9) |    +0% |
| failed_calls         |                   1 (0–1) |                         0 |  -100% |
| cancelled_calls      |                         0 |                         0 |        |
| `apply_patch` calls  |                   2 (1–2) |                         0 |  -100% |
| `apply_patch` failed |                   1 (0–1) |                         0 |  -100% |
| `read_file` calls    |                   1 (0–1) |                   1 (0–2) |    +0% |
| `read_file` failed   |                         0 |                         0 |        |
| `shell` calls        |                   6 (6–9) |                   5 (5–6) |   -17% |
| `shell` failed       |                         0 |                         0 |        |
| `write_file` calls   |                         0 |                   2 (2–3) |        |
| `write_file` failed  |                         0 |                         0 |        |
| repeated_calls       |                         0 |                         0 |        |
| input_tokens         |       53300 (49893–83601) |       44629 (37072–45693) |   -16% |
| cached_tokens        |       21000 (17740–32106) |        19080 (9322–27129) |    -9% |
| output_tokens        |          4464 (3204–4697) |          2780 (2593–3283) |   -38% |
| reasoning_tokens     |          1457 (1090–1473) |            698 (665–1208) |   -52% |
| cost                 | $0.0039 ($0.0028–$0.0075) | $0.0025 ($0.0024–$0.0042) |   -36% |
| max_input_tokens     |         8886 (8101–10534) |          6443 (6405–7423) |   -27% |
| tool_output_chars    |          5287 (2806–8410) |          2503 (2143–3421) |   -53% |
| compactions          |                         0 |                         0 |        |
| summarizer_cost      |                   $0.0000 |                   $0.0000 |        |
| subagents            |                         0 |                         0 |        |

### `tinyexpr-clamp`

| Metric               |                 muse-main |              muse-simpler | Change |
| -------------------- | ------------------------: | ------------------------: | -----: |
| passed               |                       3/3 |                       3/3 |        |
| status               |                finished 3 |                finished 3 |        |
| seconds              |                39 (37–54) |                38 (34–60) |    -2% |
| requests             |                  9 (9–10) |                12 (10–15) |   +33% |
| tool_calls           |                14 (13–14) |                15 (14–18) |    +7% |
| failed_calls         |                         0 |                         0 |        |
| cancelled_calls      |                         0 |                         0 |        |
| `apply_patch` calls  |                         2 |                         0 |  -100% |
| `apply_patch` failed |                         0 |                         0 |        |
| `edit_file` calls    |                         0 |                         3 |        |
| `edit_file` failed   |                         0 |                         0 |        |
| `glob` calls         |                         1 |                         1 |    +0% |
| `glob` failed        |                         0 |                         0 |        |
| `grep` calls         |                         0 |                   0 (0–1) |        |
| `grep` failed        |                         0 |                         0 |        |
| `read_file` calls    |                 10 (9–10) |                 10 (9–11) |    +0% |
| `read_file` failed   |                         0 |                         0 |        |
| `shell` calls        |                         1 |                   1 (1–2) |    +0% |
| `shell` failed       |                         0 |                         0 |        |
| repeated_calls       |                         0 |                         0 |        |
| input_tokens         |    180763 (171094–190910) |    235148 (184278–289802) |   +30% |
| cached_tokens        |       50041 (16505–62954) |       26474 (25164–43167) |   -47% |
| output_tokens        |          2552 (2367–2656) |          2340 (2092–2716) |    -8% |
| reasoning_tokens     |             598 (461–715) |             567 (404–636) |    -5% |
| cost                 | $0.0137 ($0.0134–$0.0160) | $0.0215 ($0.0163–$0.0253) |   +57% |
| max_input_tokens     |       29268 (29110–30000) |       27212 (26276–28488) |    -7% |
| tool_output_chars    |       71921 (71921–74566) |       68047 (61907–71881) |    -5% |
| compactions          |                         0 |                         0 |        |
| summarizer_cost      |                   $0.0000 |                   $0.0000 |        |
| subagents            |                         0 |                         0 |        |

### `tinyexpr-precedence`

| Metric               |                 muse-main |              muse-simpler | Change |
| -------------------- | ------------------------: | ------------------------: | -----: |
| passed               |                       3/3 |                       3/3 |        |
| status               |                finished 3 |                finished 3 |        |
| seconds              |              118 (80–161) |              142 (77–184) |   +21% |
| requests             |                15 (14–17) |                19 (13–24) |   +27% |
| tool_calls           |                19 (17–22) |                23 (17–27) |   +21% |
| failed_calls         |                   0 (0–3) |                   1 (0–1) |        |
| cancelled_calls      |                         0 |                         0 |        |
| `apply_patch` calls  |                   2 (2–3) |                         0 |  -100% |
| `apply_patch` failed |                   0 (0–3) |                         0 |        |
| `edit_file` calls    |                         0 |                   4 (2–4) |        |
| `edit_file` failed   |                         0 |                         0 |        |
| `glob` calls         |                         1 |                         1 |    +0% |
| `glob` failed        |                         0 |                         0 |        |
| `read_file` calls    |                 11 (9–12) |                 13 (9–14) |   +18% |
| `read_file` failed   |                         0 |                         0 |        |
| `shell` calls        |                   5 (5–6) |                   5 (4–9) |    +0% |
| `shell` failed       |                         0 |                   1 (0–1) |        |
| repeated_calls       |                         0 |                   1 (0–2) |        |
| input_tokens         |    386470 (332008–519179) |    499947 (291876–760829) |   +29% |
| cached_tokens        |       45614 (35487–74497) |     117272 (39869–125155) |  +157% |
| output_tokens        |         6865 (5957–16404) |          5442 (4764–9835) |   -21% |
| reasoning_tokens     |          4059 (2444–9829) |          2388 (2007–5832) |   -41% |
| cost                 | $0.0365 ($0.0299–$0.0479) | $0.0388 ($0.0262–$0.0666) |    +6% |
| max_input_tokens     |       37282 (33553–44869) |       34643 (31894–41785) |    -7% |
| tool_output_chars    |       79643 (74511–83823) |       81297 (74185–87664) |    +2% |
| compactions          |                         0 |                         0 |        |
| summarizer_cost      |                   $0.0000 |                   $0.0000 |        |
| subagents            |                         0 |                         0 |        |

### `todo-cli`

| Metric               |                 muse-main |              muse-simpler | Change |
| -------------------- | ------------------------: | ------------------------: | -----: |
| passed               |                       3/3 |                       3/3 |        |
| status               |                finished 3 |                finished 3 |        |
| seconds              |                49 (38–58) |                54 (48–75) |    +9% |
| requests             |                  8 (6–12) |                  8 (6–14) |    +0% |
| tool_calls           |                  7 (5–11) |                  7 (6–13) |    +0% |
| failed_calls         |                   1 (0–2) |                   1 (0–1) |    +0% |
| cancelled_calls      |                         0 |                         0 |        |
| `apply_patch` calls  |                   3 (2–4) |                         0 |  -100% |
| `apply_patch` failed |                   0 (0–1) |                         0 |        |
| `edit_file` calls    |                         0 |                   0 (0–5) |        |
| `edit_file` failed   |                         0 |                         0 |        |
| `glob` calls         |                   0 (0–1) |                         0 |        |
| `glob` failed        |                         0 |                         0 |        |
| `read_file` calls    |                   0 (0–1) |                         0 |        |
| `read_file` failed   |                         0 |                         0 |        |
| `shell` calls        |                   3 (3–6) |                   4 (3–5) |   +33% |
| `shell` failed       |                   1 (0–1) |                   1 (0–1) |    +0% |
| `write_file` calls   |                         0 |                         3 |        |
| `write_file` failed  |                         0 |                         0 |        |
| repeated_calls       |                   0 (0–1) |                   0 (0–1) |        |
| input_tokens         |       41585 (31859–86138) |       42341 (33543–82820) |    +2% |
| cached_tokens        |        14374 (5256–32716) |       16904 (10150–28590) |   +18% |
| output_tokens        |          3299 (3283–4266) |          3872 (3500–4288) |   +17% |
| reasoning_tokens     |             525 (498–878) |            883 (660–1324) |   +68% |
| cost                 | $0.0043 ($0.0024–$0.0063) | $0.0033 ($0.0032–$0.0063) |   -24% |
| max_input_tokens     |          6966 (6930–9858) |          7585 (6864–7805) |    +9% |
| tool_output_chars    |          1605 (1378–8680) |          1846 (1697–3323) |   +15% |
| compactions          |                         0 |                         0 |        |
| summarizer_cost      |                   $0.0000 |                   $0.0000 |        |
| subagents            |                         0 |                         0 |        |
