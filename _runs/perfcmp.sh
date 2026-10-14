#!/bin/bash
# Сравнение перфа cur (r21cur.test) против базы r20.test: min-of-N чередованием.
# Запуск: bash /home/dev/php/_runs/perfcmp.sh [N]
N=${1:-5}
BASE=/tmp/bench/r20.test
CUR=/home/dev/php/_runs/r21cur.test
one() { (cd /tmp/bench && CX_DIR=/tmp/bench/work timeout 300 "$1" -test.run '^$' -test.bench "^$2$" -test.benchtime 1x 2>/dev/null | awk '/ns\/op/{print $3}'); }
c2() {
  local l=$1 b=$2 x=(99999999999 99999999999) v
  for k in $(seq "$N"); do
    for j in 0 1; do
      if [ $(( (j + k) % 2 )) -eq 0 ]; then v=$(one "$BASE" "$b"); else v=$(one "$CUR" "$b"); fi
      if [ $(( (j + k) % 2 )) -eq 0 ]; then [ -n "$v" ] && [ "$v" -lt "${x[0]}" ] && x[0]=$v; else [ -n "$v" ] && [ "$v" -lt "${x[1]}" ] && x[1]=$v; fi
    done
  done
  printf "%-24s %9s %9s %8s\n" "$l" "$(awk -v v=${x[0]} 'BEGIN{printf "%.1f", v/1e6}')" "$(awk -v v=${x[1]} 'BEGIN{printf "%.1f", v/1e6}')" "$(awk -v a=${x[1]} -v b=${x[0]} 'BEGIN{printf "%.3fx", a/b}')"
}
printf "%-24s %9s %9s %8s\n" "bench (ms, min-of-$N)" "r20" "cur" "cur/r20"
for n in 01_arr_build_sum 02_arr_sort 03_arr_assoc 04_arr_fns 05_str_chain 06_str_builder 07_str_fns 08_obj_props 09_obj_methods 10_recursion 11_mixed_json 12_mixed_sort; do
  c2 "$n" "BenchmarkComplex/$n"
done
for n in MathSum10M MethodCall1M MathFib30; do
  c2 "$n" "Benchmark$n"
done
