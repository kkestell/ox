# Evaluation comparison: baseline

## Runs

| Label    | Commit       | Dirty | Model                                     | Effort  | Providers | Results |
| -------- | ------------ | ----- | ----------------------------------------- | ------- | --------- | ------- |
| baseline | `383962a262` | no    | `openrouter:deepseek/deepseek-v4.1-flash` | default | deepseek  | 16      |

## Summary

Changes are relative to `baseline`. A task's values are medians over its
repetitions, with the range in parentheses. Totals are sums of task medians.

| Metric            |  baseline |
| ----------------- | --------: |
| finished          |      9/16 |
| passed            |     13/16 |
| seconds           |      8043 |
| requests          |      1628 |
| tool_calls        |      1768 |
| failed_calls      |        75 |
| cancelled_calls   |         1 |
| repeated_calls    |        12 |
| input_tokens      | 176902308 |
| cached_tokens     | 175341696 |
| output_tokens     |   1191919 |
| reasoning_tokens  |    812493 |
| cost              |   $2.9505 |
| max_input_tokens  |   2596292 |
| tool_output_chars |   4845539 |

### Passed and cost by task

| Task                       | baseline passed | baseline cost |
| -------------------------- | --------------: | ------------: |
| `oberon-case-for-assert`   |             1/1 |       $0.2198 |
| `oberon-control-flow`      |             1/1 |       $0.1415 |
| `oberon-fixed-arrays`      |             1/1 |       $0.2016 |
| `oberon-integer-modules`   |             1/1 |       $0.1147 |
| `oberon-modules`           |             1/1 |       $0.1186 |
| `oberon-nested-procedures` |             1/1 |       $0.1271 |
| `oberon-open-arrays`       |             1/1 |       $0.2318 |
| `oberon-pointers`          |             1/1 |       $0.2261 |
| `oberon-procedure-types`   |             1/1 |       $0.1675 |
| `oberon-reals`             |             1/1 |       $0.1974 |
| `oberon-record-extension`  |             0/1 |       $0.2296 |
| `oberon-records`           |             1/1 |       $0.1697 |
| `oberon-scalar-procedures` |             1/1 |       $0.1415 |
| `oberon-sets`              |             1/1 |       $0.1949 |
| `oberon-standard-library`  |             0/1 |       $0.2048 |
| `oberon-strings`           |             0/1 |       $0.2639 |

## Tasks

### `oberon-case-for-assert`

| Metric               |  baseline |
| -------------------- | --------: |
| status               | timeout 1 |
| passed               |       1/1 |
| seconds              |       600 |
| requests             |       117 |
| tool_calls           |       124 |
| failed_calls         |         4 |
| cancelled_calls      |         0 |
| `apply_patch` calls  |        30 |
| `apply_patch` failed |         2 |
| `read_file` calls    |        25 |
| `read_file` failed   |         0 |
| `shell` calls        |        69 |
| `shell` failed       |         2 |
| repeated_calls       |         2 |
| input_tokens         |  12674023 |
| cached_tokens        |  12581120 |
| output_tokens        |     97040 |
| reasoning_tokens     |     66706 |
| cost                 |   $0.2198 |
| max_input_tokens     |    178649 |
| tool_output_chars    |    276422 |

### `oberon-control-flow`

| Metric               |   baseline |
| -------------------- | ---------: |
| status               | finished 1 |
| passed               |        1/1 |
| seconds              |        416 |
| requests             |         86 |
| tool_calls           |         96 |
| failed_calls         |          5 |
| cancelled_calls      |          0 |
| `apply_patch` calls  |         26 |
| `apply_patch` failed |          2 |
| `read_file` calls    |         20 |
| `read_file` failed   |          0 |
| `shell` calls        |         50 |
| `shell` failed       |          3 |
| repeated_calls       |          0 |
| input_tokens         |    6606315 |
| cached_tokens        |    6541056 |
| output_tokens        |      68932 |
| reasoning_tokens     |      42357 |
| cost                 |    $0.1415 |
| max_input_tokens     |     125248 |
| tool_output_chars    |     180508 |

### `oberon-fixed-arrays`

| Metric               |   baseline |
| -------------------- | ---------: |
| status               | finished 1 |
| passed               |        1/1 |
| seconds              |        520 |
| requests             |        105 |
| tool_calls           |        112 |
| failed_calls         |          3 |
| cancelled_calls      |          0 |
| `apply_patch` calls  |         37 |
| `apply_patch` failed |          2 |
| `grep` calls         |          1 |
| `grep` failed        |          0 |
| `read_file` calls    |         20 |
| `read_file` failed   |          0 |
| `shell` calls        |         54 |
| `shell` failed       |          1 |
| repeated_calls       |          1 |
| input_tokens         |   12211039 |
| cached_tokens        |   12099456 |
| output_tokens        |      79575 |
| reasoning_tokens     |      56424 |
| cost                 |    $0.2016 |
| max_input_tokens     |     181440 |
| tool_output_chars    |     346824 |

### `oberon-integer-modules`

| Metric               |   baseline |
| -------------------- | ---------: |
| status               | finished 1 |
| passed               |        1/1 |
| seconds              |        389 |
| requests             |         67 |
| tool_calls           |         70 |
| failed_calls         |          2 |
| cancelled_calls      |          0 |
| `apply_patch` calls  |         27 |
| `apply_patch` failed |          0 |
| `shell` calls        |         43 |
| `shell` failed       |          2 |
| repeated_calls       |          0 |
| input_tokens         |    3799447 |
| cached_tokens        |    3771136 |
| output_tokens        |      69633 |
| reasoning_tokens     |      41330 |
| cost                 |    $0.1147 |
| max_input_tokens     |      91345 |
| tool_output_chars    |      48355 |

### `oberon-modules`

| Metric               |   baseline |
| -------------------- | ---------: |
| status               | finished 1 |
| passed               |        1/1 |
| seconds              |        336 |
| requests             |         72 |
| tool_calls           |         82 |
| failed_calls         |          1 |
| cancelled_calls      |          0 |
| `apply_patch` calls  |         20 |
| `apply_patch` failed |          1 |
| `read_file` calls    |         22 |
| `read_file` failed   |          0 |
| `shell` calls        |         40 |
| `shell` failed       |          0 |
| repeated_calls       |          1 |
| input_tokens         |    5936016 |
| cached_tokens        |    5854592 |
| output_tokens        |      49201 |
| reasoning_tokens     |      34385 |
| cost                 |    $0.1186 |
| max_input_tokens     |     124449 |
| tool_output_chars    |     263320 |

### `oberon-nested-procedures`

| Metric               |   baseline |
| -------------------- | ---------: |
| status               | finished 1 |
| passed               |        1/1 |
| seconds              |        373 |
| requests             |         89 |
| tool_calls           |        100 |
| failed_calls         |         10 |
| cancelled_calls      |          0 |
| `apply_patch` calls  |         22 |
| `apply_patch` failed |          0 |
| `read_file` calls    |         24 |
| `read_file` failed   |          0 |
| `shell` calls        |         54 |
| `shell` failed       |         10 |
| repeated_calls       |          0 |
| input_tokens         |    6943770 |
| cached_tokens        |    6864128 |
| output_tokens        |      51681 |
| reasoning_tokens     |      35085 |
| cost                 |    $0.1271 |
| max_input_tokens     |     118073 |
| tool_output_chars    |     236832 |

### `oberon-open-arrays`

| Metric               |  baseline |
| -------------------- | --------: |
| status               | timeout 1 |
| passed               |       1/1 |
| seconds              |       600 |
| requests             |       124 |
| tool_calls           |       133 |
| failed_calls         |        10 |
| cancelled_calls      |         0 |
| `apply_patch` calls  |        33 |
| `apply_patch` failed |         1 |
| `grep` calls         |         6 |
| `grep` failed        |         0 |
| `read_file` calls    |        38 |
| `read_file` failed   |         0 |
| `shell` calls        |        56 |
| `shell` failed       |         9 |
| repeated_calls       |         1 |
| input_tokens         |  15754849 |
| cached_tokens        |  15628544 |
| output_tokens        |     83413 |
| reasoning_tokens     |     58064 |
| cost                 |   $0.2318 |
| max_input_tokens     |    199478 |
| tool_output_chars    |    423028 |

### `oberon-pointers`

| Metric               |  baseline |
| -------------------- | --------: |
| status               | timeout 1 |
| passed               |       1/1 |
| seconds              |       600 |
| requests             |       108 |
| tool_calls           |       122 |
| failed_calls         |         3 |
| cancelled_calls      |         0 |
| `apply_patch` calls  |        35 |
| `apply_patch` failed |         1 |
| `grep` calls         |         2 |
| `grep` failed        |         0 |
| `read_file` calls    |        27 |
| `read_file` failed   |         0 |
| `shell` calls        |        58 |
| `shell` failed       |         2 |
| repeated_calls       |         0 |
| input_tokens         |  14382230 |
| cached_tokens        |  14258560 |
| output_tokens        |     86230 |
| reasoning_tokens     |     58868 |
| cost                 |   $0.2261 |
| max_input_tokens     |    200425 |
| tool_output_chars    |    404162 |

### `oberon-procedure-types`

| Metric               |   baseline |
| -------------------- | ---------: |
| status               | finished 1 |
| passed               |        1/1 |
| seconds              |        442 |
| requests             |        116 |
| tool_calls           |        122 |
| failed_calls         |          3 |
| cancelled_calls      |          0 |
| `apply_patch` calls  |         20 |
| `apply_patch` failed |          1 |
| `read_file` calls    |         36 |
| `read_file` failed   |          0 |
| `shell` calls        |         66 |
| `shell` failed       |          2 |
| repeated_calls       |          0 |
| input_tokens         |   11720324 |
| cached_tokens        |   11605632 |
| output_tokens        |      52914 |
| reasoning_tokens     |      35283 |
| cost                 |    $0.1675 |
| max_input_tokens     |     157093 |
| tool_output_chars    |     371709 |

### `oberon-reals`

| Metric               |  baseline |
| -------------------- | --------: |
| status               | timeout 1 |
| passed               |       1/1 |
| seconds              |       600 |
| requests             |       102 |
| tool_calls           |       119 |
| failed_calls         |         5 |
| cancelled_calls      |         0 |
| `apply_patch` calls  |        30 |
| `apply_patch` failed |         1 |
| `read_file` calls    |        22 |
| `read_file` failed   |         0 |
| `shell` calls        |        67 |
| `shell` failed       |         4 |
| repeated_calls       |         0 |
| input_tokens         |  11707279 |
| cached_tokens        |  11595904 |
| output_tokens        |     78710 |
| reasoning_tokens     |     52603 |
| cost                 |   $0.1974 |
| max_input_tokens     |    181042 |
| tool_output_chars    |    326912 |

### `oberon-record-extension`

| Metric               |  baseline |
| -------------------- | --------: |
| status               | timeout 1 |
| passed               |       0/1 |
| seconds              |       600 |
| requests             |       130 |
| tool_calls           |       136 |
| failed_calls         |         5 |
| cancelled_calls      |         0 |
| `apply_patch` calls  |        38 |
| `apply_patch` failed |         1 |
| `glob` calls         |         1 |
| `glob` failed        |         0 |
| `read_file` calls    |        41 |
| `read_file` failed   |         0 |
| `shell` calls        |        56 |
| `shell` failed       |         4 |
| repeated_calls       |         2 |
| input_tokens         |  16455917 |
| cached_tokens        |  16327680 |
| output_tokens        |     77642 |
| reasoning_tokens     |     52191 |
| cost                 |   $0.2296 |
| max_input_tokens     |    194347 |
| tool_output_chars    |    421207 |

### `oberon-records`

| Metric               |   baseline |
| -------------------- | ---------: |
| status               | finished 1 |
| passed               |        1/1 |
| seconds              |        446 |
| requests             |        108 |
| tool_calls           |        119 |
| failed_calls         |          7 |
| cancelled_calls      |          0 |
| `apply_patch` calls  |         26 |
| `apply_patch` failed |          1 |
| `read_file` calls    |         23 |
| `read_file` failed   |          0 |
| `shell` calls        |         70 |
| `shell` failed       |          6 |
| repeated_calls       |          1 |
| input_tokens         |   11156561 |
| cached_tokens        |   11053952 |
| output_tokens        |      60499 |
| reasoning_tokens     |      35822 |
| cost                 |    $0.1697 |
| max_input_tokens     |     152493 |
| tool_output_chars    |     317989 |

### `oberon-scalar-procedures`

| Metric               |   baseline |
| -------------------- | ---------: |
| status               | finished 1 |
| passed               |        1/1 |
| seconds              |        416 |
| requests             |         86 |
| tool_calls           |         90 |
| failed_calls         |          2 |
| cancelled_calls      |          0 |
| `apply_patch` calls  |         19 |
| `apply_patch` failed |          0 |
| `read_file` calls    |         20 |
| `read_file` failed   |          0 |
| `shell` calls        |         51 |
| `shell` failed       |          2 |
| repeated_calls       |          2 |
| input_tokens         |    6859382 |
| cached_tokens        |    6787584 |
| output_tokens        |      66017 |
| reasoning_tokens     |      46954 |
| cost                 |    $0.1415 |
| max_input_tokens     |     130494 |
| tool_output_chars    |     219702 |

### `oberon-sets`

| Metric               |   baseline |
| -------------------- | ---------: |
| status               | finished 1 |
| passed               |        1/1 |
| seconds              |        504 |
| requests             |        107 |
| tool_calls           |        106 |
| failed_calls         |          5 |
| cancelled_calls      |          0 |
| `apply_patch` calls  |         31 |
| `apply_patch` failed |          1 |
| `read_file` calls    |         30 |
| `read_file` failed   |          0 |
| `shell` calls        |         45 |
| `shell` failed       |          4 |
| repeated_calls       |          2 |
| input_tokens         |   11688878 |
| cached_tokens        |   11579648 |
| output_tokens        |      77191 |
| reasoning_tokens     |      54410 |
| cost                 |    $0.1949 |
| max_input_tokens     |     176543 |
| tool_output_chars    |     334975 |

### `oberon-standard-library`

| Metric               |  baseline |
| -------------------- | --------: |
| status               | timeout 1 |
| passed               |       0/1 |
| seconds              |       600 |
| requests             |        87 |
| tool_calls           |       100 |
| failed_calls         |         5 |
| cancelled_calls      |         0 |
| `apply_patch` calls  |        20 |
| `apply_patch` failed |         0 |
| `read_file` calls    |         6 |
| `read_file` failed   |         0 |
| `shell` calls        |        74 |
| `shell` failed       |         5 |
| repeated_calls       |         0 |
| input_tokens         |   9755658 |
| cached_tokens        |   9675136 |
| output_tokens        |    102180 |
| reasoning_tokens     |     75412 |
| cost                 |   $0.2048 |
| max_input_tokens     |    173334 |
| tool_output_chars    |    245484 |

### `oberon-strings`

| Metric               |  baseline |
| -------------------- | --------: |
| status               | timeout 1 |
| passed               |       0/1 |
| seconds              |       600 |
| requests             |       124 |
| tool_calls           |       137 |
| failed_calls         |         5 |
| cancelled_calls      |         1 |
| `apply_patch` calls  |        59 |
| `apply_patch` failed |         5 |
| `read_file` calls    |        48 |
| `read_file` failed   |         0 |
| `shell` calls        |        30 |
| `shell` failed       |         0 |
| repeated_calls       |         0 |
| input_tokens         |  19250620 |
| cached_tokens        |  19117568 |
| output_tokens        |     91061 |
| reasoning_tokens     |     66599 |
| cost                 |   $0.2639 |
| max_input_tokens     |    211839 |
| tool_output_chars    |    428110 |
