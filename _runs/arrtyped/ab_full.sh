#!/bin/bash
# Interleaved A/B: старый (OLD) и новый (NEW) бинарь на одних и тех же нагрузках.
# Для каждой нагрузки: OLD/NEW чередуются, берётся min по RUNS запусков.
set -u
WORK=${WORK:-/tmp/bench/work}
OLD=${OLD:-/tmp/old2/old_true.test}
NEW=${NEW:-/home/dev/php/_runs/arrtyped/phpcomplex_after.test}
RUNS=${RUNS:-3}
LIMIT=${LIMIT:-180}

best() { # bin file -> "ms kb"
  local bin=$1 f=$2 ms="" kb="" out
  for k in $(seq "$RUNS"); do
    out=$(CX_DIR="$WORK" timeout "$LIMIT" python3 /tmp/bench/measure.py "$bin" -test.run '^$' \
      -test.bench "^BenchmarkComplex/$(basename "$f" .php)$" -test.benchtime 1x 2>&1)
    local m kk
    m=$(printf '%s\n' "$out" | awk '/^TIME/{print $2}')
    kk=$(printf '%s\n' "$out" | awk '/^TIME/{print $3}')
    m=${m%.*}
    if [ -n "$m" ] && { [ -z "$ms" ] || [ "$m" -lt "$ms" ]; }; then ms=$m; kb=$kk; fi
  done
  echo "$ms $kb"
}

printf "%-20s %9s %9s %7s %10s %10s %7s\n" workload old_ms new_ms ratio old_MB new_MB MBx
for f in "$WORK"/*.php; do
  name=$(basename "$f" .php)
  o=$(best "$OLD" "$f"); n=$(best "$NEW" "$f")
  oms=${o%% *}; okb=${o##* }
  nms=${n%% *}; nkb=${n##* }
  ratio=$(awk -v a="$nms" -v b="$oms" 'BEGIN{printf "%.2f", (b>0? a/b : 0)}')
  mbr=$(awk -v a="$nkb" -v b="$okb" 'BEGIN{printf "%.2f", (b>0? a/b : 0)}')
  printf "%-20s %9s %9s %7s %10s %10s %7s\n" "$name" "$oms" "$nms" "$ratio" \
    "$(awk -v k="$okb" 'BEGIN{printf "%.1f", k/1024}')" \
    "$(awk -v k="$nkb" 'BEGIN{printf "%.1f", k/1024}')" "$mbr"
done
