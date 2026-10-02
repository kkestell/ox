# Benchmark comparison: luna-write-edit-3x-20261001, luna-patch-3x-20261001

## Runs

| Label                       | Commit       | Dirty | Model                 | Effort  | Providers | Results |
| --------------------------- | ------------ | ----- | --------------------- | ------- | --------- | ------- |
| luna-write-edit-3x-20261001 | `e610384016` | no    | `openai:gpt-5.6-luna` | default | any       | 36      |
| luna-patch-3x-20261001      | `93dee39560` | no    | `openai:gpt-5.6-luna` | default | any       | 36      |

## Chart

![luna-patch-3x-20261001 compared with luna-write-edit-3x-20261001](luna-write-edit-3x-20261001-vs-luna-patch-3x-20261001.svg)

## Summary

Write/edit is commit `e610384016`, immediately before apply_patch was restored.
Patch is commit `93dee39560`, which also includes the subsequent transcript
rendering change. Each model uses openai:gpt-5.6-luna with default effort, with
three repetitions of each of the 12 scenarios. OpenAI cost is unavailable; the
chart marks it unavailable and the tables retain that distinction.

Changes are relative to `luna-write-edit-3x-20261001`. A task's values are
medians over its repetitions, with the range in parentheses. Totals are sums of
task medians.

| Metric            | luna-write-edit-3x-20261001 | luna-patch-3x-20261001 | Change |
| ----------------- | --------------------------: | ---------------------: | -----: |
| passed            |                       36/36 |                  36/36 |        |
| seconds           |                         924 |                   1032 |   +12% |
| requests          |                         117 |                    119 |    +2% |
| tool_calls        |                         152 |                    145 |    -5% |
| failed_calls      |                           7 |                      7 |    +0% |
| cancelled_calls   |                           0 |                      0 |        |
| repeated_calls    |                           3 |                      8 |  +167% |
| input_tokens      |                     1184597 |                1309268 |   +11% |
| cached_tokens     |                      940544 |                1041920 |   +11% |
| output_tokens     |                       30235 |                  36859 |   +22% |
| reasoning_tokens  |                        9692 |                  11935 |   +23% |
| cost              |                 unavailable |            unavailable |        |
| max_input_tokens  |                      138664 |                 147451 |    +6% |
| tool_output_chars |                      296860 |                 284347 |    -4% |

### Pass rate and cost by task

| Task                  | luna-write-edit-3x-20261001 passed | luna-patch-3x-20261001 passed | luna-write-edit-3x-20261001 cost | luna-patch-3x-20261001 cost | Change |
| --------------------- | ---------------------------------: | ----------------------------: | -------------------------------: | --------------------------: | -----: |
| `coffee-site`         |                                3/3 |                           3/3 |                      unavailable |                 unavailable |        |
| `csv-stats`           |                                3/3 |                           3/3 |                      unavailable |                 unavailable |        |
| `inih-quoted`         |                                3/3 |                           3/3 |                      unavailable |                 unavailable |        |
| `itoa-boundaries`     |                                3/3 |                           3/3 |                      unavailable |                 unavailable |        |
| `jsmn-rename`         |                                3/3 |                           3/3 |                      unavailable |                 unavailable |        |
| `mini-redis-docs`     |                                3/3 |                           3/3 |                      unavailable |                 unavailable |        |
| `pi-digit`            |                                3/3 |                           3/3 |                      unavailable |                 unavailable |        |
| `sds-startswith`      |                                3/3 |                           3/3 |                      unavailable |                 unavailable |        |
| `temp-cli`            |                                3/3 |                           3/3 |                      unavailable |                 unavailable |        |
| `tinyexpr-clamp`      |                                3/3 |                           3/3 |                      unavailable |                 unavailable |        |
| `tinyexpr-precedence` |                                3/3 |                           3/3 |                      unavailable |                 unavailable |        |
| `todo-cli`            |                                3/3 |                           3/3 |                      unavailable |                 unavailable |        |

## Tasks

### `coffee-site`

| Metric                 | luna-write-edit-3x-20261001 | luna-patch-3x-20261001 | Change |
| ---------------------- | --------------------------: | ---------------------: | -----: |
| passed                 |                         3/3 |                    3/3 |        |
| status                 |                  finished 3 |             finished 3 |        |
| seconds                |               110 (106–115) |          111 (109–112) |    +1% |
| requests               |                           8 |                7 (6–7) |   -12% |
| tool_calls             |                     7 (7–8) |                6 (6–7) |   -14% |
| failed_calls           |                           0 |                      0 |        |
| cancelled_calls        |                           0 |                      0 |        |
| `apply_patch` calls    |                           0 |                2 (1–2) |        |
| `apply_patch` failed   |                           0 |                      0 |        |
| `glob` calls           |                     0 (0–1) |                0 (0–1) |        |
| `glob` failed          |                           0 |                      0 |        |
| `shell` calls          |                           3 |                3 (3–4) |    +0% |
| `shell` failed         |                           0 |                      0 |        |
| `shell_process` calls  |                           1 |                      1 |    +0% |
| `shell_process` failed |                           0 |                      0 |        |
| `write_file` calls     |                           3 |                      0 |  -100% |
| `write_file` failed    |                           0 |                      0 |        |
| repeated_calls         |                           0 |                      0 |        |
| input_tokens           |         38460 (37138–38987) |    39447 (31257–41023) |    +3% |
| cached_tokens          |         21504 (21504–25600) |    25088 (20992–29696) |   +17% |
| output_tokens          |            4814 (4551–4834) |       5046 (4640–5394) |    +5% |
| reasoning_tokens       |                  93 (92–98) |           160 (62–180) |   +72% |
| cost                   |                 unavailable |            unavailable |        |
| max_input_tokens       |            6788 (6522–6854) |       7447 (7052–7761) |   +10% |
| tool_output_chars      |               875 (837–966) |          944 (912–969) |    +8% |

### `csv-stats`

| Metric               | luna-write-edit-3x-20261001 | luna-patch-3x-20261001 | Change |
| -------------------- | --------------------------: | ---------------------: | -----: |
| passed               |                         3/3 |                    3/3 |        |
| status               |                  finished 3 |             finished 3 |        |
| seconds              |                 78 (77–115) |            87 (63–103) |   +12% |
| requests             |                    5 (5–11) |                4 (4–7) |   -20% |
| tool_calls           |                    8 (7–16) |               5 (4–10) |   -38% |
| failed_calls         |                     0 (0–2) |                      0 |        |
| cancelled_calls      |                           0 |                      0 |        |
| `apply_patch` calls  |                           0 |                1 (1–3) |        |
| `apply_patch` failed |                           0 |                      0 |        |
| `edit_file` calls    |                     0 (0–6) |                      0 |        |
| `edit_file` failed   |                     0 (0–2) |                      0 |        |
| `glob` calls         |                     0 (0–1) |                1 (0–1) |        |
| `glob` failed        |                           0 |                      0 |        |
| `grep` calls         |                     0 (0–1) |                      0 |        |
| `grep` failed        |                           0 |                      0 |        |
| `shell` calls        |                     2 (2–4) |                3 (2–7) |   +50% |
| `shell` failed       |                           0 |                      0 |        |
| `write_file` calls   |                           5 |                      0 |  -100% |
| `write_file` failed  |                           0 |                      0 |        |
| repeated_calls       |                           0 |                      0 |        |
| input_tokens         |         18484 (16658–55716) |    17143 (13834–36542) |    -7% |
| cached_tokens        |          11264 (4608–30720) |      6144 (4608–13824) |   -45% |
| output_tokens        |            3674 (3016–4825) |       4128 (2930–4765) |   +12% |
| reasoning_tokens     |             1493 (910–1823) |        1526 (915–1610) |    +2% |
| cost                 |                 unavailable |            unavailable |        |
| max_input_tokens     |            5239 (4695–6851) |       7221 (4795–8081) |   +38% |
| tool_output_chars    |              544 (414–1056) |        6099 (529–6229) | +1021% |

### `inih-quoted`

| Metric               | luna-write-edit-3x-20261001 | luna-patch-3x-20261001 | Change |
| -------------------- | --------------------------: | ---------------------: | -----: |
| passed               |                         3/3 |                    3/3 |        |
| status               |                  finished 3 |             finished 3 |        |
| seconds              |                101 (92–104) |             83 (82–90) |   -18% |
| requests             |                  16 (15–17) |             12 (12–13) |   -25% |
| tool_calls           |                  21 (20–23) |             17 (17–21) |   -19% |
| failed_calls         |                           1 |                2 (1–2) |  +100% |
| cancelled_calls      |                           0 |                      0 |        |
| `apply_patch` calls  |                           0 |                      1 |        |
| `apply_patch` failed |                           0 |                      0 |        |
| `edit_file` calls    |                     2 (2–4) |                      0 |  -100% |
| `edit_file` failed   |                           0 |                      0 |        |
| `glob` calls         |                     2 (0–2) |                2 (2–3) |    +0% |
| `glob` failed        |                           0 |                      0 |        |
| `grep` calls         |                           2 |                3 (1–3) |   +50% |
| `grep` failed        |                           0 |                      0 |        |
| `read_file` calls    |                     6 (4–7) |                6 (3–7) |    +0% |
| `read_file` failed   |                           0 |                      0 |        |
| `shell` calls        |                    8 (7–10) |                7 (7–8) |   -12% |
| `shell` failed       |                           1 |                2 (1–2) |  +100% |
| `write_file` calls   |                     1 (1–2) |                      0 |  -100% |
| `write_file` failed  |                           0 |                      0 |        |
| repeated_calls       |                           0 |                      0 |        |
| input_tokens         |      198362 (141654–216130) | 129713 (127456–170703) |   -35% |
| cached_tokens        |      158720 (112640–175616) |  106496 (92672–133632) |   -33% |
| output_tokens        |            3259 (2926–3857) |       2560 (2546–2778) |   -21% |
| reasoning_tokens     |            2019 (1806–2387) |       1561 (1458–1753) |   -23% |
| cost                 |                 unavailable |            unavailable |        |
| max_input_tokens     |         16955 (13073–17597) |    15068 (13113–19453) |   -11% |
| tool_output_chars    |         40789 (28354–41048) |    34089 (28733–48896) |   -16% |

### `itoa-boundaries`

| Metric               | luna-write-edit-3x-20261001 | luna-patch-3x-20261001 | Change |
| -------------------- | --------------------------: | ---------------------: | -----: |
| passed               |                         3/3 |                    3/3 |        |
| status               |                  finished 3 |             finished 3 |        |
| seconds              |                 65 (45–108) |             49 (44–73) |   -25% |
| requests             |                   11 (6–15) |                7 (7–9) |   -36% |
| tool_calls           |                   12 (7–18) |              10 (8–12) |   -17% |
| failed_calls         |                     0 (0–3) |                0 (0–1) |        |
| cancelled_calls      |                           0 |                      0 |        |
| `apply_patch` calls  |                           0 |                2 (1–2) |        |
| `apply_patch` failed |                           0 |                      0 |        |
| `edit_file` calls    |                     3 (1–6) |                      0 |  -100% |
| `edit_file` failed   |                     0 (0–1) |                      0 |        |
| `glob` calls         |                           1 |                      1 |    +0% |
| `glob` failed        |                           0 |                      0 |        |
| `grep` calls         |                     1 (0–1) |                      1 |    +0% |
| `grep` failed        |                           0 |                      0 |        |
| `read_file` calls    |                     4 (2–6) |                4 (3–4) |    +0% |
| `read_file` failed   |                           0 |                      0 |        |
| `shell` calls        |                     3 (2–5) |                2 (2–4) |   -33% |
| `shell` failed       |                     0 (0–2) |                0 (0–1) |        |
| repeated_calls       |                     1 (0–1) |                0 (0–1) |  -100% |
| input_tokens         |        92482 (35044–149180) |    49684 (42153–74555) |   -46% |
| cached_tokens        |        66560 (11776–120320) |    29696 (29696–51200) |   -55% |
| output_tokens        |            1926 (1033–2502) |        1480 (922–1886) |   -23% |
| reasoning_tokens     |               912 (357–944) |          531 (334–941) |   -42% |
| cost                 |                 unavailable |            unavailable |        |
| max_input_tokens     |          12176 (8708–13731) |     10576 (8786–11347) |   -13% |
| tool_output_chars    |         27495 (18754–30477) |    22013 (18300–23278) |   -20% |

### `jsmn-rename`

| Metric             | luna-write-edit-3x-20261001 | luna-patch-3x-20261001 | Change |
| ------------------ | --------------------------: | ---------------------: | -----: |
| passed             |                         3/3 |                    3/3 |        |
| status             |                  finished 3 |             finished 3 |        |
| seconds            |                  31 (30–38) |             28 (26–33) |    -9% |
| requests           |                     7 (6–8) |                6 (6–8) |   -14% |
| tool_calls         |                   10 (7–11) |                8 (7–9) |   -20% |
| failed_calls       |                           0 |                      0 |        |
| cancelled_calls    |                           0 |                      0 |        |
| `glob` calls       |                           1 |                      1 |    +0% |
| `glob` failed      |                           0 |                      0 |        |
| `grep` calls       |                     1 (0–2) |                      1 |    +0% |
| `grep` failed      |                           0 |                      0 |        |
| `read_file` calls  |                     1 (1–3) |                      1 |    +0% |
| `read_file` failed |                           0 |                      0 |        |
| `shell` calls      |                     6 (4–7) |                5 (4–6) |   -17% |
| `shell` failed     |                           0 |                      0 |        |
| repeated_calls     |                     0 (0–1) |                      0 |        |
| input_tokens       |         29071 (25407–36573) |    28009 (27116–40547) |    -4% |
| cached_tokens      |         18432 (16384–23552) |    14336 (10752–26624) |   -22% |
| output_tokens      |               654 (624–860) |          662 (616–735) |    +1% |
| reasoning_tokens   |               218 (154–288) |          218 (184–245) |    +0% |
| cost               |                 unavailable |            unavailable |        |
| max_input_tokens   |            5541 (5516–7648) |       6765 (6007–6933) |   +22% |
| tool_output_chars  |           9714 (9588–15521) |     11961 (9720–12039) |   +23% |

### `mini-redis-docs`

| Metric               | luna-write-edit-3x-20261001 | luna-patch-3x-20261001 | Change |
| -------------------- | --------------------------: | ---------------------: | -----: |
| passed               |                         3/3 |                    3/3 |        |
| status               |                  finished 3 |             finished 3 |        |
| seconds              |               146 (143–152) |          168 (119–185) |   +15% |
| requests             |                  15 (14–19) |             20 (14–20) |   +33% |
| tool_calls           |                  22 (17–23) |             22 (17–25) |    +0% |
| failed_calls         |                     3 (2–4) |                3 (2–5) |    +0% |
| cancelled_calls      |                           0 |                      0 |        |
| `apply_patch` calls  |                           0 |                9 (4–9) |        |
| `apply_patch` failed |                           0 |                1 (0–3) |        |
| `edit_file` calls    |                     3 (0–7) |                      0 |  -100% |
| `edit_file` failed   |                     0 (0–1) |                      0 |        |
| `shell` calls        |                  17 (16–19) |             13 (13–16) |   -24% |
| `shell` failed       |                     2 (2–4) |                      2 |    +0% |
| repeated_calls       |                     0 (0–2) |                3 (2–3) |        |
| input_tokens         |      315740 (302103–411803) | 433082 (285345–450281) |   +37% |
| cached_tokens        |      270848 (265728–344576) | 377856 (241664–389632) |   +40% |
| output_tokens        |            4468 (4273–4556) |       5180 (3669–6204) |   +16% |
| reasoning_tokens     |               870 (781–953) |          489 (439–643) |   -44% |
| cost                 |                 unavailable |            unavailable |        |
| max_input_tokens     |         31348 (30258–31755) |    30021 (28941–32522) |    -4% |
| tool_output_chars    |        98498 (93257–101692) |    90424 (82839–95332) |    -8% |

### `pi-digit`

| Metric               | luna-write-edit-3x-20261001 | luna-patch-3x-20261001 | Change |
| -------------------- | --------------------------: | ---------------------: | -----: |
| passed               |                         3/3 |                    3/3 |        |
| status               |                  finished 3 |             finished 3 |        |
| seconds              |                 96 (52–252) |          245 (173–266) |  +156% |
| requests             |                    8 (7–34) |             23 (16–34) |  +188% |
| tool_calls           |                    8 (8–35) |             23 (16–34) |  +188% |
| failed_calls         |                     1 (0–2) |                1 (0–2) |    +0% |
| cancelled_calls      |                           0 |                      0 |        |
| `apply_patch` calls  |                           0 |                5 (3–9) |        |
| `apply_patch` failed |                           0 |                      0 |        |
| `edit_file` calls    |                     1 (1–9) |                      0 |  -100% |
| `edit_file` failed   |                           0 |                      0 |        |
| `glob` calls         |                           1 |                      1 |    +0% |
| `glob` failed        |                           0 |                      0 |        |
| `read_file` calls    |                           1 |                1 (0–1) |    +0% |
| `read_file` failed   |                     1 (0–1) |                1 (0–1) |    +0% |
| `shell` calls        |                    2 (2–20) |             16 (12–23) |  +700% |
| `shell` failed       |                     0 (0–1) |                0 (0–1) |        |
| `write_file` calls   |                     3 (3–4) |                      0 |  -100% |
| `write_file` failed  |                           0 |                      0 |        |
| repeated_calls       |                     1 (0–3) |                4 (0–5) |  +300% |
| input_tokens         |        27847 (21161–244449) | 182151 (110119–274498) |  +554% |
| cached_tokens        |         17408 (5632–201728) |  141312 (68096–240128) |  +712% |
| output_tokens        |            3453 (1472–9169) |       9477 (7613–9913) |  +174% |
| reasoning_tokens     |             1790 (533–5416) |       5273 (4398–5729) |  +195% |
| cost                 |                 unavailable |            unavailable |        |
| max_input_tokens     |           5262 (3276–13270) |    12821 (10559–13204) |  +144% |
| tool_output_chars    |              330 (329–4677) |       2444 (2270–2596) |  +641% |

### `sds-startswith`

| Metric               | luna-write-edit-3x-20261001 | luna-patch-3x-20261001 | Change |
| -------------------- | --------------------------: | ---------------------: | -----: |
| passed               |                         3/3 |                    3/3 |        |
| status               |                  finished 3 |             finished 3 |        |
| seconds              |                  62 (52–62) |             55 (43–61) |   -11% |
| requests             |                   10 (7–10) |                9 (6–9) |   -10% |
| tool_calls           |                  15 (12–16) |             13 (10–14) |   -13% |
| failed_calls         |                           0 |                1 (0–2) |        |
| cancelled_calls      |                           0 |                      0 |        |
| `apply_patch` calls  |                           0 |                3 (2–3) |        |
| `apply_patch` failed |                           0 |                1 (0–1) |        |
| `edit_file` calls    |                     4 (3–4) |                      0 |  -100% |
| `edit_file` failed   |                           0 |                      0 |        |
| `glob` calls         |                           1 |                      1 |    +0% |
| `glob` failed        |                           0 |                      0 |        |
| `grep` calls         |                     1 (1–3) |                2 (2–3) |  +100% |
| `grep` failed        |                           0 |                0 (0–1) |        |
| `read_file` calls    |                     6 (6–7) |                5 (4–6) |   -17% |
| `read_file` failed   |                           0 |                      0 |        |
| `shell` calls        |                     2 (1–2) |                1 (1–2) |   -50% |
| `shell` failed       |                           0 |                      0 |        |
| repeated_calls       |                           0 |                      0 |        |
| input_tokens         |         67360 (38010–88227) |    57980 (41875–71696) |   -14% |
| cached_tokens        |         54272 (16896–58880) |    37376 (30720–38400) |   -31% |
| output_tokens        |            1535 (1457–2027) |       1689 (1281–1814) |   +10% |
| reasoning_tokens     |               378 (349–663) |          344 (294–398) |    -9% |
| cost                 |                 unavailable |            unavailable |        |
| max_input_tokens     |           9907 (8598–13180) |      9967 (8941–11027) |    +1% |
| tool_output_chars    |         18809 (15553–27193) |    18546 (13953–20958) |    -1% |

### `temp-cli`

| Metric               | luna-write-edit-3x-20261001 | luna-patch-3x-20261001 | Change |
| -------------------- | --------------------------: | ---------------------: | -----: |
| passed               |                         3/3 |                    3/3 |        |
| status               |                  finished 3 |             finished 3 |        |
| seconds              |                  44 (32–47) |             37 (32–59) |   -17% |
| requests             |                     5 (4–7) |                5 (4–7) |    +0% |
| tool_calls           |                           6 |                6 (5–7) |    +0% |
| failed_calls         |                     1 (0–1) |                0 (0–1) |  -100% |
| cancelled_calls      |                           0 |                      0 |        |
| `apply_patch` calls  |                           0 |                1 (1–2) |        |
| `apply_patch` failed |                           0 |                      0 |        |
| `glob` calls         |                     0 (0–1) |                      1 |        |
| `glob` failed        |                           0 |                      0 |        |
| `shell` calls        |                     3 (2–3) |                4 (3–4) |   +33% |
| `shell` failed       |                     1 (0–1) |                0 (0–1) |  -100% |
| `write_file` calls   |                           3 |                      0 |  -100% |
| `write_file` failed  |                           0 |                      0 |        |
| repeated_calls       |                           0 |                      0 |        |
| input_tokens         |          12589 (9330–17284) |    15248 (11275–22435) |   +21% |
| cached_tokens        |           8704 (5632–11776) |      7168 (6144–14848) |   -18% |
| output_tokens        |            1273 (1189–1588) |       1398 (1137–1635) |   +10% |
| reasoning_tokens     |               198 (181–493) |           217 (84–386) |   +10% |
| cost                 |                 unavailable |            unavailable |        |
| max_input_tokens     |            3223 (3043–3418) |       3822 (3614–4330) |   +19% |
| tool_output_chars    |              871 (841–1076) |        1114 (972–1829) |   +28% |

### `tinyexpr-clamp`

| Metric               | luna-write-edit-3x-20261001 | luna-patch-3x-20261001 | Change |
| -------------------- | --------------------------: | ---------------------: | -----: |
| passed               |                         3/3 |                    3/3 |        |
| status               |                  finished 3 |             finished 3 |        |
| seconds              |                  54 (51–59) |             43 (43–64) |   -20% |
| requests             |                   11 (9–13) |              10 (6–11) |    -9% |
| tool_calls           |                  17 (14–17) |              14 (9–16) |   -18% |
| failed_calls         |                     1 (1–2) |                0 (0–2) |  -100% |
| cancelled_calls      |                           0 |                      0 |        |
| `apply_patch` calls  |                           0 |                1 (1–4) |        |
| `apply_patch` failed |                           0 |                0 (0–2) |        |
| `edit_file` calls    |                     4 (4–5) |                      0 |  -100% |
| `edit_file` failed   |                     1 (1–2) |                      0 |  -100% |
| `glob` calls         |                           1 |                      1 |    +0% |
| `glob` failed        |                           0 |                      0 |        |
| `grep` calls         |                     2 (1–2) |                      1 |   -50% |
| `grep` failed        |                           0 |                      0 |        |
| `read_file` calls    |                     7 (6–8) |                8 (5–9) |   +14% |
| `read_file` failed   |                           0 |                      0 |        |
| `shell` calls        |                           2 |                2 (1–2) |    +0% |
| `shell` failed       |                           0 |                      0 |        |
| repeated_calls       |                           0 |                      0 |        |
| input_tokens         |        96220 (76463–124678) |  101805 (46202–131465) |    +6% |
| cached_tokens        |        69632 (48640–103424) |   83968 (19968–108544) |   +21% |
| output_tokens        |            1400 (1329–1535) |       1228 (1161–2040) |   -12% |
| reasoning_tokens     |               336 (273–583) |          470 (258–539) |   +40% |
| cost                 |                 unavailable |            unavailable |        |
| max_input_tokens     |         11929 (11684–12384) |    12559 (11493–19250) |    +5% |
| tool_output_chars    |         26292 (24285–28331) |    25526 (22849–50111) |    -3% |

### `tinyexpr-precedence`

| Metric               | luna-write-edit-3x-20261001 | luna-patch-3x-20261001 | Change |
| -------------------- | --------------------------: | ---------------------: | -----: |
| passed               |                         3/3 |                    3/3 |        |
| status               |                  finished 3 |             finished 3 |        |
| seconds              |                  92 (71–94) |             66 (58–66) |   -28% |
| requests             |                  15 (14–18) |             12 (11–14) |   -20% |
| tool_calls           |                  20 (16–21) |             16 (14–18) |   -20% |
| failed_calls         |                           0 |                      0 |        |
| cancelled_calls      |                           0 |                      0 |        |
| `apply_patch` calls  |                           0 |                      1 |        |
| `apply_patch` failed |                           0 |                      0 |        |
| `edit_file` calls    |                     2 (1–2) |                      0 |  -100% |
| `edit_file` failed   |                           0 |                      0 |        |
| `glob` calls         |                           1 |                1 (1–2) |    +0% |
| `glob` failed        |                           0 |                      0 |        |
| `grep` calls         |                     2 (2–3) |                2 (2–3) |    +0% |
| `grep` failed        |                           0 |                      0 |        |
| `read_file` calls    |                     8 (7–9) |                7 (6–8) |   -12% |
| `read_file` failed   |                           0 |                      0 |        |
| `shell` calls        |                     6 (4–8) |                4 (3–6) |   -33% |
| `shell` failed       |                           0 |                      0 |        |
| repeated_calls       |                           1 |                      1 |    +0% |
| input_tokens         |      271178 (263103–322710) | 241522 (186411–279756) |   -11% |
| cached_tokens        |      235008 (214528–285696) | 204288 (156160–245760) |   -13% |
| output_tokens        |            1859 (1795–2386) |       1622 (1439–2032) |   -13% |
| reasoning_tokens     |             1142 (933–1408) |         800 (733–1287) |   -30% |
| cost                 |                 unavailable |            unavailable |        |
| max_input_tokens     |         26513 (19346–28806) |    26496 (24541–27300) |    -0% |
| tool_output_chars    |         72041 (47576–78362) |    70533 (65033–70798) |    -2% |

### `todo-cli`

| Metric               | luna-write-edit-3x-20261001 | luna-patch-3x-20261001 | Change |
| -------------------- | --------------------------: | ---------------------: | -----: |
| passed               |                         3/3 |                    3/3 |        |
| status               |                  finished 3 |             finished 3 |        |
| seconds              |                  46 (46–55) |             60 (59–62) |   +30% |
| requests             |                     6 (4–7) |                4 (4–6) |   -33% |
| tool_calls           |                     6 (5–6) |                5 (5–7) |   -17% |
| failed_calls         |                           0 |                0 (0–1) |        |
| cancelled_calls      |                           0 |                      0 |        |
| `apply_patch` calls  |                           0 |                1 (1–2) |        |
| `apply_patch` failed |                           0 |                      0 |        |
| `glob` calls         |                           0 |                1 (0–1) |        |
| `glob` failed        |                           0 |                      0 |        |
| `shell` calls        |                     3 (2–3) |                3 (3–5) |    +0% |
| `shell` failed       |                           0 |                0 (0–1) |        |
| `write_file` calls   |                           3 |                      0 |  -100% |
| `write_file` failed  |                           0 |                      0 |        |
| repeated_calls       |                           0 |                      0 |        |
| input_tokens         |         16804 (10867–20056) |    13484 (13051–25642) |   -20% |
| cached_tokens        |           8192 (3072–11264) |      8192 (6656–12288) |    +0% |
| output_tokens        |            1920 (1868–1922) |       2389 (2203–2499) |   +24% |
| reasoning_tokens     |               243 (232–275) |          346 (282–360) |   +42% |
| cost                 |                 unavailable |            unavailable |        |
| max_input_tokens     |            3783 (3741–3785) |       4688 (4468–6344) |   +24% |
| tool_output_chars    |               602 (583–614) |         654 (650–7938) |    +9% |
