#!/usr/bin/env bash
# A/B-сравнение производительности VM: текущий master vs перф-референс 70d92dd.
# Все замеры — CPU-время (устойчиво к посторонней нагрузке на машину).
# Два перемежающихся прохода (cur/ref, cur/ref), для каждой нагрузки берётся
# min по проходам — это гасит ноутбучный шум (троттлинг, антивирус, прогрев).
#
# Usage: bench/ab.sh [N]        N — повторов php-замеров в bench.py (по умолчанию 3)
set -euo pipefail
cd "$(dirname "$0")/.."
N=${1:-3}
export GOTOOLCHAIN=${GOTOOLCHAIN:-go1.27.1}
mkdir -p bench/work_cur bench/work_ref

go test -c -vet=off -o bench/phpvm_cur.test ./internal/php/
ref=$(mktemp -d)/ref
git worktree add -q "$ref" 70d92dd
(cd "$ref" && go test -c -vet=off -o "$OLDPWD/bench/phpvm_ref.test" ./internal/php/)
git worktree remove --force "$ref" 2>/dev/null || true

for round in 1 2; do
    echo "=== проход ${round}/2: cur ==="
    python3 bench/bench.py bench/phpvm_cur.test bench/work_cur "$N" | tee "bench/cur_${round}.txt"
    echo "=== проход ${round}/2: ref ==="
    python3 bench/bench.py bench/phpvm_ref.test bench/work_ref "$N" | tee "bench/ref_${round}.txt"
done

python3 - <<'PY'
import re
def load(prefix):
    d = {}
    for p in (f'bench/{prefix}_1.txt', f'bench/{prefix}_2.txt'):
        for line in open(p, encoding='utf-8', errors='replace'):
            m = re.match(r'(\S+)\s+([\d.]+)\s+([\d.]+)\s+([\d.]+)\s+([\d.]+)\s+([\d.]+)', line)
            if not m:
                continue
            name, cpu, rss = m.group(1), float(m.group(2)), float(m.group(5))
            if name not in d or cpu < d[name][0]:
                d[name] = (cpu, rss)
    return d
c, r = load('cur'), load('ref')
print()
print(f"{'нагрузка':22} {'ref_CPU':>8} {'cur_CPU':>8} {'cur/ref':>8} {'ref_RSS':>8} {'cur_RSS':>8}")
for k in sorted(set(c) & set(r)):
    print(f"{k:22} {r[k][0]:8.1f} {c[k][0]:8.1f} {c[k][0]/r[k][0]:8.2f} {r[k][1]:8.1f} {c[k][1]:8.1f}")
PY
