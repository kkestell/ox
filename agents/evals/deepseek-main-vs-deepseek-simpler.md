# Benchmark comparison: deepseek-main, deepseek-simpler

## Runs

| Label            | Commit       | Dirty | Model                          | Effort  | Results |
| ---------------- | ------------ | ----- | ------------------------------ | ------- | ------- |
| deepseek-main    | `82d15b17c1` | no    | `deepseek/deepseek-v4.1-flash` | default | 33      |
| deepseek-simpler | `2596cf26c4` | no    | `deepseek/deepseek-v4.1-flash` | default | 33      |

## Summary

Changes are relative to `deepseek-main`. A task's values are medians over its
repetitions, with the range in parentheses. Totals are sums of task medians.

| Metric            | deepseek-main | deepseek-simpler | Change |
| ----------------- | ------------: | ---------------: | -----: |
| passed            |         33/33 |            33/33 |        |
| seconds           |          1460 |             1465 |    +0% |
| requests          |           242 |              244 |    +1% |
| tool_calls        |           260 |              289 |   +11% |
| failed_calls      |            13 |                7 |   -46% |
| cancelled_calls   |             0 |                0 |        |
| repeated_calls    |             0 |                2 |        |
| input_tokens      |       9604839 |         10278634 |    +7% |
| cached_tokens     |       8396544 |          9167104 |    +9% |
| output_tokens     |        172378 |           178034 |    +3% |
| reasoning_tokens  |        114916 |           115832 |    +1% |
| cost              |       $0.5942 |          $0.6042 |    +2% |
| max_input_tokens  |        335464 |           328047 |    -2% |
| tool_output_chars |        383987 |           354055 |    -8% |
| compactions       |             0 |                0 |        |
| summarizer_cost   |       $0.0000 |          $0.0000 |        |
| subagents         |             0 |                0 |        |

### Pass rate and cost by task

| Task                  | deepseek-main passed | deepseek-simpler passed |        deepseek-main cost |     deepseek-simpler cost | Change |
| --------------------- | -------------------: | ----------------------: | ------------------------: | ------------------------: | -----: |
| `coffee-site`         |                  3/3 |                     3/3 | $0.0051 ($0.0041–$0.0079) | $0.0037 ($0.0031–$0.0063) |   -26% |
| `csv-stats`           |                  3/3 |                     3/3 | $0.0066 ($0.0052–$0.0095) | $0.0085 ($0.0048–$0.0131) |   +29% |
| `inih-quoted`         |                  3/3 |                     3/3 | $0.2027 ($0.1681–$0.2089) | $0.1435 ($0.1202–$0.4186) |   -29% |
| `itoa-boundaries`     |                  3/3 |                     3/3 | $0.0376 ($0.0314–$0.0525) | $0.0656 ($0.0577–$0.0663) |   +75% |
| `jsmn-rename`         |                  3/3 |                     3/3 | $0.0018 ($0.0015–$0.0030) | $0.0016 ($0.0014–$0.0027) |   -12% |
| `pi-digit`            |                  3/3 |                     3/3 | $0.0143 ($0.0134–$0.0179) | $0.0134 ($0.0118–$0.0150) |    -6% |
| `sds-startswith`      |                  3/3 |                     3/3 | $0.0045 ($0.0031–$0.0045) | $0.0040 ($0.0040–$0.0044) |   -10% |
| `temp-cli`            |                  3/3 |                     3/3 | $0.0063 ($0.0050–$0.0070) | $0.0098 ($0.0052–$0.0100) |   +57% |
| `tinyexpr-clamp`      |                  3/3 |                     3/3 | $0.0138 ($0.0110–$0.0209) | $0.0070 ($0.0051–$0.0088) |   -49% |
| `tinyexpr-precedence` |                  3/3 |                     3/3 | $0.2969 ($0.2327–$0.3787) | $0.3418 ($0.1564–$0.3779) |   +15% |
| `todo-cli`            |                  3/3 |                     3/3 | $0.0047 ($0.0041–$0.0091) | $0.0052 ($0.0049–$0.0055) |   +11% |

## Tasks

### `coffee-site`

| Metric                 |             deepseek-main |          deepseek-simpler | Change |
| ---------------------- | ------------------------: | ------------------------: | -----: |
| passed                 |                       3/3 |                       3/3 |        |
| status                 |                finished 3 |                finished 3 |        |
| seconds                |                14 (14–18) |                 11 (9–14) |   -25% |
| requests               |                   8 (7–9) |                   5 (5–6) |   -38% |
| tool_calls             |                   8 (6–8) |                   6 (6–9) |   -25% |
| failed_calls           |                         0 |                         0 |        |
| cancelled_calls        |                         0 |                         0 |        |
| `apply_patch` calls    |                         1 |                         0 |  -100% |
| `apply_patch` failed   |                         0 |                         0 |        |
| `glob` calls           |                   0 (0–1) |                   0 (0–1) |        |
| `glob` failed          |                         0 |                         0 |        |
| `shell` calls          |                   4 (4–5) |                   2 (2–3) |   -50% |
| `shell` failed         |                         0 |                         0 |        |
| `shell_process` calls  |                   2 (1–2) |                   1 (1–2) |   -50% |
| `shell_process` failed |                         0 |                         0 |        |
| `write_file` calls     |                         0 |                         3 |        |
| `write_file` failed    |                         0 |                         0 |        |
| repeated_calls         |                         0 |                         0 |        |
| input_tokens           |       43826 (36264–69235) |       24155 (22217–34688) |   -45% |
| cached_tokens          |       40960 (35328–67200) |       23424 (21120–32512) |   -43% |
| output_tokens          |          3301 (2977–5735) |          2817 (2223–4532) |   -15% |
| reasoning_tokens       |               63 (49–242) |                35 (24–45) |   -44% |
| cost                   | $0.0051 ($0.0041–$0.0079) | $0.0037 ($0.0031–$0.0063) |   -26% |
| max_input_tokens       |         6930 (6463–10061) |          5692 (5383–7720) |   -18% |
| tool_output_chars      |          1790 (1264–3121) |           1241 (795–1368) |   -31% |
| compactions            |                         0 |                         0 |        |
| summarizer_cost        |                   $0.0000 |                   $0.0000 |        |
| subagents              |                         0 |                         0 |        |

### `csv-stats`

| Metric               |             deepseek-main |          deepseek-simpler | Change |
| -------------------- | ------------------------: | ------------------------: | -----: |
| passed               |                       3/3 |                       3/3 |        |
| status               |                finished 3 |                finished 3 |        |
| seconds              |              163 (54–244) |              233 (66–245) |   +43% |
| requests             |                  8 (7–15) |                 12 (9–12) |   +50% |
| tool_calls           |                  7 (6–14) |                 13 (8–15) |   +86% |
| failed_calls         |                   1 (0–1) |                   1 (0–1) |    +0% |
| cancelled_calls      |                         0 |                         0 |        |
| `apply_patch` calls  |                   2 (1–3) |                         0 |  -100% |
| `apply_patch` failed |                         0 |                         0 |        |
| `glob` calls         |                         0 |                   0 (0–1) |        |
| `glob` failed        |                         0 |                         0 |        |
| `shell` calls        |                  5 (5–11) |                   8 (3–9) |   +60% |
| `shell` failed       |                   1 (0–1) |                   1 (0–1) |    +0% |
| `write_file` calls   |                         0 |                         5 |        |
| `write_file` failed  |                         0 |                         0 |        |
| repeated_calls       |                         0 |                         0 |        |
| input_tokens         |      64826 (59004–156223) |     121866 (58890–167823) |   +88% |
| cached_tokens        |      54272 (49152–135424) |     102144 (54528–133888) |   +88% |
| output_tokens        |          8886 (6343–9819) |        10151 (5632–14847) |   +14% |
| reasoning_tokens     |          4089 (2854–4826) |          4694 (2413–8056) |   +15% |
| cost                 | $0.0066 ($0.0052–$0.0095) | $0.0085 ($0.0048–$0.0131) |   +29% |
| max_input_tokens     |        12399 (9539–14535) |        14143 (8421–18855) |   +14% |
| tool_output_chars    |          1711 (1388–5168) |           3772 (947–5894) |  +120% |
| compactions          |                         0 |                         0 |        |
| summarizer_cost      |                   $0.0000 |                   $0.0000 |        |
| subagents            |                         0 |                         0 |        |

### `inih-quoted`

| Metric               |             deepseek-main |          deepseek-simpler | Change |
| -------------------- | ------------------------: | ------------------------: | -----: |
| passed               |                       3/3 |                       3/3 |        |
| status               |                finished 3 |                finished 3 |        |
| seconds              |             379 (229–417) |             259 (180–537) |   -32% |
| requests             |                64 (58–68) |               52 (50–107) |   -19% |
| tool_calls           |                73 (72–79) |               61 (60–125) |   -16% |
| failed_calls         |                   2 (1–4) |                   1 (1–3) |   -50% |
| cancelled_calls      |                         0 |                         0 |        |
| `apply_patch` calls  |                   2 (1–4) |                         0 |  -100% |
| `apply_patch` failed |                         0 |                         0 |        |
| `edit_file` calls    |                         0 |                   2 (1–9) |        |
| `edit_file` failed   |                         0 |                         0 |        |
| `glob` calls         |                   1 (1–2) |                   1 (1–2) |    +0% |
| `glob` failed        |                         0 |                         0 |        |
| `grep` calls         |                   0 (0–2) |                   1 (0–2) |        |
| `grep` failed        |                         0 |                         0 |        |
| `read_file` calls    |                 12 (9–13) |                 11 (7–18) |    -8% |
| `read_file` failed   |                         0 |                         0 |        |
| `shell` calls        |                57 (55–65) |                49 (43–89) |   -14% |
| `shell` failed       |                   2 (1–4) |                   1 (1–2) |   -50% |
| `write_file` calls   |                         0 |                   2 (2–6) |        |
| `write_file` failed  |                         0 |                   0 (0–1) |        |
| repeated_calls       |                         0 |                   0 (0–1) |        |
| input_tokens         | 2938663 (2494816–2968630) | 1923530 (1807452–8409305) |   -35% |
| cached_tokens        | 2433920 (2098048–2475008) | 1570560 (1528704–7775872) |   -35% |
| output_tokens        |       45684 (39754–53673) |       39605 (28620–90208) |   -13% |
| reasoning_tokens     |       35961 (30553–43458) |       31219 (22242–69238) |   -13% |
| cost                 | $0.2027 ($0.1681–$0.2089) | $0.1435 ($0.1202–$0.4186) |   -29% |
| max_input_tokens     |       81794 (77721–85875) |      68443 (58535–149322) |   -16% |
| tool_output_chars    |     103444 (89415–106309) |      83542 (83084–172191) |   -19% |
| compactions          |                         0 |                         0 |        |
| summarizer_cost      |                   $0.0000 |                   $0.0000 |        |
| subagents            |                         0 |                         0 |        |

### `itoa-boundaries`

| Metric               |             deepseek-main |          deepseek-simpler | Change |
| -------------------- | ------------------------: | ------------------------: | -----: |
| passed               |                       3/3 |                       3/3 |        |
| status               |                finished 3 |                finished 3 |        |
| seconds              |              123 (78–129) |             124 (112–133) |    +1% |
| requests             |                18 (17–24) |                25 (22–31) |   +39% |
| tool_calls           |                20 (19–27) |                35 (25–37) |   +75% |
| failed_calls         |                   1 (0–1) |                   1 (0–2) |    +0% |
| cancelled_calls      |                         0 |                         0 |        |
| `apply_patch` calls  |                         1 |                         0 |  -100% |
| `apply_patch` failed |                         0 |                         0 |        |
| `edit_file` calls    |                         0 |                   2 (1–3) |        |
| `edit_file` failed   |                         0 |                         0 |        |
| `glob` calls         |                         1 |                         1 |    +0% |
| `glob` failed        |                         0 |                         0 |        |
| `grep` calls         |                         0 |                   0 (0–1) |        |
| `grep` failed        |                         0 |                         0 |        |
| `read_file` calls    |                   5 (5–7) |                         7 |   +40% |
| `read_file` failed   |                         0 |                         0 |        |
| `shell` calls        |                13 (12–18) |                22 (13–27) |   +69% |
| `shell` failed       |                   1 (0–1) |                   1 (0–2) |    +0% |
| `write_file` calls   |                         0 |                   1 (1–2) |        |
| `write_file` failed  |                         0 |                         0 |        |
| repeated_calls       |                   0 (0–1) |                         1 |        |
| input_tokens         |    277087 (273372–507741) |    498801 (467562–758068) |   +80% |
| cached_tokens        |    188544 (157056–356864) |    288896 (263424–586624) |   +53% |
| output_tokens        |       14748 (12428–17822) |       21094 (13836–24009) |   +43% |
| reasoning_tokens     |        10313 (7537–13637) |        15232 (8291–18320) |   +48% |
| cost                 | $0.0376 ($0.0314–$0.0525) | $0.0656 ($0.0577–$0.0663) |   +75% |
| max_input_tokens     |       26868 (25332–34359) |       39714 (31047–41597) |   +48% |
| tool_output_chars    |       30770 (29584–41925) |       46894 (45137–49976) |   +52% |
| compactions          |                         0 |                         0 |        |
| summarizer_cost      |                   $0.0000 |                   $0.0000 |        |
| subagents            |                         0 |                         0 |        |

### `jsmn-rename`

| Metric             |             deepseek-main |          deepseek-simpler | Change |
| ------------------ | ------------------------: | ------------------------: | -----: |
| passed             |                       3/3 |                       3/3 |        |
| status             |                finished 3 |                finished 3 |        |
| seconds            |                 28 (4–37) |                29 (20–40) |    +5% |
| requests           |                  8 (5–10) |                   8 (7–8) |    +0% |
| tool_calls         |                 13 (5–14) |                 10 (9–12) |   -23% |
| failed_calls       |                   0 (0–1) |                         0 |        |
| cancelled_calls    |                         0 |                         0 |        |
| `grep` calls       |                   1 (1–3) |                   1 (1–2) |    +0% |
| `grep` failed      |                         0 |                         0 |        |
| `read_file` calls  |                   3 (1–3) |                   3 (2–3) |    +0% |
| `read_file` failed |                         0 |                         0 |        |
| `shell` calls      |                   8 (3–9) |                   7 (4–8) |   -12% |
| `shell` failed     |                   0 (0–1) |                         0 |        |
| repeated_calls     |                         0 |                         0 |        |
| input_tokens       |       51335 (21494–90943) |       48792 (38870–55841) |    -5% |
| cached_tokens      |       46336 (19456–78592) |       44288 (34432–46464) |    -4% |
| output_tokens      |           2259 (676–2344) |          1911 (1598–2765) |   -15% |
| reasoning_tokens   |            987 (136–1152) |            841 (603–1331) |   -15% |
| cost               | $0.0018 ($0.0015–$0.0030) | $0.0016 ($0.0014–$0.0027) |   -12% |
| max_input_tokens   |         8641 (5241–13680) |         8126 (7514–10334) |    -6% |
| tool_output_chars  |         9609 (3691–23991) |         9893 (9196–14182) |    +3% |
| compactions        |                         0 |                         0 |        |
| summarizer_cost    |                   $0.0000 |                   $0.0000 |        |
| subagents          |                         0 |                         0 |        |

### `pi-digit`

| Metric               |             deepseek-main |          deepseek-simpler | Change |
| -------------------- | ------------------------: | ------------------------: | -----: |
| passed               |                       3/3 |                       3/3 |        |
| status               |                finished 3 |                finished 3 |        |
| seconds              |                48 (40–61) |                47 (46–72) |    -2% |
| requests             |                  8 (6–10) |                   6 (6–7) |   -25% |
| tool_calls           |                   7 (6–9) |                  8 (7–10) |   +14% |
| failed_calls         |                   0 (0–1) |                         0 |        |
| cancelled_calls      |                         0 |                         0 |        |
| `apply_patch` calls  |                   1 (1–2) |                         0 |  -100% |
| `apply_patch` failed |                         0 |                         0 |        |
| `shell` calls        |                   6 (5–7) |                   5 (4–7) |   -17% |
| `shell` failed       |                   0 (0–1) |                         0 |        |
| `write_file` calls   |                         0 |                         3 |        |
| `write_file` failed  |                         0 |                         0 |        |
| repeated_calls       |                         0 |                         0 |        |
| input_tokens         |      80826 (58083–110581) |       57545 (53966–71520) |   -29% |
| cached_tokens        |       67584 (23296–79616) |       33024 (20096–33408) |   -51% |
| output_tokens        |        11210 (8807–11263) |          9288 (9181–9784) |   -17% |
| reasoning_tokens     |          7032 (5491–7164) |          6390 (5876–6404) |    -9% |
| cost                 | $0.0143 ($0.0134–$0.0179) | $0.0134 ($0.0118–$0.0150) |    -6% |
| max_input_tokens     |       14771 (12143–14810) |       12339 (12061–12686) |   -16% |
| tool_output_chars    |           1361 (905–1484) |            873 (784–1236) |   -36% |
| compactions          |                         0 |                         0 |        |
| summarizer_cost      |                   $0.0000 |                   $0.0000 |        |
| subagents            |                         0 |                         0 |        |

### `sds-startswith`

| Metric               |             deepseek-main |          deepseek-simpler | Change |
| -------------------- | ------------------------: | ------------------------: | -----: |
| passed               |                       3/3 |                       3/3 |        |
| status               |                finished 3 |                finished 3 |        |
| seconds              |                35 (27–37) |                34 (29–36) |    -2% |
| requests             |                14 (11–15) |                12 (11–13) |   -14% |
| tool_calls           |                17 (15–18) |                16 (15–17) |    -6% |
| failed_calls         |                   1 (1–2) |                         0 |  -100% |
| cancelled_calls      |                         0 |                         0 |        |
| `apply_patch` calls  |                   4 (3–4) |                         0 |  -100% |
| `apply_patch` failed |                         1 |                         0 |  -100% |
| `edit_file` calls    |                         0 |                   4 (3–5) |        |
| `edit_file` failed   |                         0 |                         0 |        |
| `glob` calls         |                   1 (0–1) |                         1 |    +0% |
| `glob` failed        |                         0 |                         0 |        |
| `grep` calls         |                   2 (2–3) |                   2 (1–3) |    +0% |
| `grep` failed        |                         0 |                         0 |        |
| `read_file` calls    |                   6 (4–7) |                   5 (4–7) |   -17% |
| `read_file` failed   |                         0 |                         0 |        |
| `shell` calls        |                   4 (3–6) |                   3 (3–5) |   -25% |
| `shell` failed       |                   0 (0–1) |                         0 |        |
| repeated_calls       |                         0 |                         0 |        |
| input_tokens         |     121593 (80687–152524) |     100956 (78741–123291) |   -17% |
| cached_tokens        |     109952 (72704–140672) |      91136 (68864–108928) |   -17% |
| output_tokens        |          3413 (3045–4102) |          4038 (3304–4588) |   +18% |
| reasoning_tokens     |           1197 (813–1493) |          1676 (1598–2305) |   +40% |
| cost                 | $0.0045 ($0.0031–$0.0045) | $0.0040 ($0.0040–$0.0044) |   -10% |
| max_input_tokens     |       12708 (10500–14061) |       12077 (11982–16367) |    -5% |
| tool_output_chars    |       14404 (11415–22205) |       14494 (13059–30375) |    +1% |
| compactions          |                         0 |                         0 |        |
| summarizer_cost      |                   $0.0000 |                   $0.0000 |        |
| subagents            |                         0 |                         0 |        |

### `temp-cli`

| Metric               |             deepseek-main |          deepseek-simpler | Change |
| -------------------- | ------------------------: | ------------------------: | -----: |
| passed               |                       3/3 |                       3/3 |        |
| status               |                finished 3 |                finished 3 |        |
| seconds              |                16 (14–17) |                28 (14–48) |   +82% |
| requests             |                         6 |                  8 (7–10) |   +33% |
| tool_calls           |                         5 |                 11 (8–11) |  +120% |
| failed_calls         |                         0 |                         0 |        |
| cancelled_calls      |                         0 |                         0 |        |
| `apply_patch` calls  |                   1 (1–2) |                         0 |  -100% |
| `apply_patch` failed |                         0 |                         0 |        |
| `edit_file` calls    |                         0 |                   0 (0–1) |        |
| `edit_file` failed   |                         0 |                         0 |        |
| `glob` calls         |                         0 |                   0 (0–1) |        |
| `glob` failed        |                         0 |                         0 |        |
| `shell` calls        |                   4 (3–4) |                         5 |   +25% |
| `shell` failed       |                         0 |                         0 |        |
| `write_file` calls   |                         0 |                   5 (3–5) |        |
| `write_file` failed  |                         0 |                         0 |        |
| repeated_calls       |                         0 |                   0 (0–1) |        |
| input_tokens         |       34154 (29851–35462) |       54451 (33469–73668) |   +59% |
| cached_tokens        |       16256 (15744–29440) |       26752 (20736–52608) |   +65% |
| output_tokens        |          4011 (2732–4209) |          5554 (3084–6246) |   +38% |
| reasoning_tokens     |           1225 (314–1247) |           1768 (745–2068) |   +44% |
| cost                 | $0.0063 ($0.0050–$0.0070) | $0.0098 ($0.0052–$0.0100) |   +57% |
| max_input_tokens     |          7661 (6345–7917) |         8923 (6237–10521) |   +16% |
| tool_output_chars    |          2152 (2091–3084) |          3082 (2214–5932) |   +43% |
| compactions          |                         0 |                         0 |        |
| summarizer_cost      |                   $0.0000 |                   $0.0000 |        |
| subagents            |                         0 |                         0 |        |

### `tinyexpr-clamp`

| Metric               |             deepseek-main |          deepseek-simpler | Change |
| -------------------- | ------------------------: | ------------------------: | -----: |
| passed               |                       3/3 |                       3/3 |        |
| status               |                finished 3 |                finished 3 |        |
| seconds              |             159 (141–180) |               88 (56–161) |   -45% |
| requests             |                29 (23–33) |                17 (13–20) |   -41% |
| tool_calls           |                30 (26–40) |                24 (19–25) |   -20% |
| failed_calls         |                   1 (0–1) |                         0 |  -100% |
| cancelled_calls      |                         0 |                         0 |        |
| `apply_patch` calls  |                  7 (5–10) |                         0 |  -100% |
| `apply_patch` failed |                   0 (0–1) |                         0 |        |
| `edit_file` calls    |                         0 |                   6 (6–7) |        |
| `edit_file` failed   |                         0 |                         0 |        |
| `glob` calls         |                         1 |                         1 |    +0% |
| `glob` failed        |                         0 |                         0 |        |
| `grep` calls         |                   4 (4–5) |                   0 (0–4) |  -100% |
| `grep` failed        |                         0 |                         0 |        |
| `read_file` calls    |                 12 (9–16) |                  7 (6–11) |   -42% |
| `read_file` failed   |                         0 |                         0 |        |
| `shell` calls        |                   7 (5–9) |                   6 (3–9) |   -14% |
| `shell` failed       |                   0 (0–1) |                         0 |        |
| repeated_calls       |                         0 |                         0 |        |
| input_tokens         |    496230 (375085–796967) |    212252 (152754–295273) |   -57% |
| cached_tokens        |    463104 (335872–741120) |    194816 (133376–246528) |   -58% |
| output_tokens        |          6404 (5764–8271) |          4560 (3337–4834) |   -29% |
| reasoning_tokens     |          2564 (2377–3447) |           1695 (946–1849) |   -34% |
| cost                 | $0.0138 ($0.0110–$0.0209) | $0.0070 ($0.0051–$0.0088) |   -49% |
| max_input_tokens     |       25598 (22791–33751) |       17728 (16809–22549) |   -31% |
| tool_output_chars    |       45027 (37940–65934) |       31214 (29876–42300) |   -31% |
| compactions          |                         0 |                         0 |        |
| summarizer_cost      |                   $0.0000 |                   $0.0000 |        |
| subagents            |                         0 |                         0 |        |

### `tinyexpr-precedence`

| Metric               |             deepseek-main |          deepseek-simpler | Change |
| -------------------- | ------------------------: | ------------------------: | -----: |
| passed               |                       3/3 |                       3/3 |        |
| status               |                finished 3 |                finished 3 |        |
| seconds              |             483 (361–762) |             600 (278–640) |   +24% |
| requests             |                72 (61–73) |                90 (49–99) |   +25% |
| tool_calls           |                74 (73–75) |               95 (56–107) |   +28% |
| failed_calls         |                   6 (1–7) |                   4 (2–6) |   -33% |
| cancelled_calls      |                         0 |                         0 |        |
| `apply_patch` calls  |                  2 (2–14) |                         0 |  -100% |
| `apply_patch` failed |                   0 (0–1) |                         0 |        |
| `edit_file` calls    |                         0 |                 13 (2–22) |        |
| `edit_file` failed   |                         0 |                         0 |        |
| `glob` calls         |                   0 (0–2) |                         1 |        |
| `glob` failed        |                         0 |                         0 |        |
| `grep` calls         |                   0 (0–3) |                   0 (0–3) |        |
| `grep` failed        |                         0 |                         0 |        |
| `read_file` calls    |                15 (13–16) |                17 (14–17) |   +13% |
| `read_file` failed   |                   0 (0–1) |                   0 (0–1) |        |
| `shell` calls        |                52 (45–58) |                51 (39–59) |    -2% |
| `shell` failed       |                   4 (1–7) |                   4 (2–4) |    +0% |
| `write_file` calls   |                         0 |                  8 (0–10) |        |
| `write_file` failed  |                         0 |                   0 (0–1) |        |
| repeated_calls       |                         0 |                   1 (0–3) |        |
| input_tokens         | 5460299 (4545502–6980145) | 7186403 (2702324–8162955) |   +32% |
| cached_tokens        | 4940672 (4202112–6504192) | 6743808 (2336000–7620736) |   +36% |
| output_tokens        |      68928 (58334–135973) |       75361 (43888–78362) |    +9% |
| reasoning_tokens     |      51065 (43389–114841) |       51938 (31467–58952) |    +2% |
| cost                 | $0.2969 ($0.2327–$0.3787) | $0.3418 ($0.1564–$0.3779) |   +15% |
| max_input_tokens     |    131157 (103921–207253) |     133630 (94038–133838) |    +2% |
| tool_output_chars    |    172078 (124511–200091) |    156476 (138763–160936) |    -9% |
| compactions          |                         0 |                         0 |        |
| summarizer_cost      |                   $0.0000 |                   $0.0000 |        |
| subagents            |                         0 |                         0 |        |

### `todo-cli`

| Metric               |             deepseek-main |          deepseek-simpler | Change |
| -------------------- | ------------------------: | ------------------------: | -----: |
| passed               |                       3/3 |                       3/3 |        |
| status               |                finished 3 |                finished 3 |        |
| seconds              |                13 (10–21) |                13 (12–14) |    +6% |
| requests             |                  7 (6–13) |                  9 (8–11) |   +29% |
| tool_calls           |                  6 (5–12) |                 10 (8–13) |   +67% |
| failed_calls         |                   1 (0–1) |                         0 |  -100% |
| cancelled_calls      |                         0 |                         0 |        |
| `apply_patch` calls  |                   1 (1–6) |                         0 |  -100% |
| `apply_patch` failed |                   0 (0–1) |                         0 |        |
| `edit_file` calls    |                         0 |                   2 (1–6) |        |
| `edit_file` failed   |                         0 |                         0 |        |
| `shell` calls        |                   5 (4–6) |                   4 (3–6) |   -20% |
| `shell` failed       |                   0 (0–1) |                         0 |        |
| `write_file` calls   |                         0 |                         3 |        |
| `write_file` failed  |                         0 |                         0 |        |
| repeated_calls       |                         0 |                         0 |        |
| input_tokens         |      36000 (32093–104314) |       49883 (41336–60567) |   +39% |
| cached_tokens        |      34944 (31104–102144) |       48256 (40320–58880) |   +38% |
| output_tokens        |          3534 (3010–6568) |          3655 (3646–3968) |    +3% |
| reasoning_tokens     |             420 (342–703) |             344 (281–778) |   -18% |
| cost                 | $0.0047 ($0.0041–$0.0091) | $0.0052 ($0.0049–$0.0055) |   +11% |
| max_input_tokens     |         6937 (6408–10496) |          7232 (6558–7423) |    +4% |
| tool_output_chars    |          1641 (1395–3634) |          2574 (1155–3199) |   +57% |
| compactions          |                         0 |                         0 |        |
| summarizer_cost      |                   $0.0000 |                   $0.0000 |        |
| subagents            |                         0 |                         0 |        |
