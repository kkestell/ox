#!/bin/sh
# A deliberately loud validator: it reports every checked item and then fails.
i=1
while [ "$i" -le 4000 ]; do
	printf 'check %04d ok\n' "$i"
	i=$((i + 1))
done
printf 'FAIL: 1 of 4000 checks failed\n'
exit 3
