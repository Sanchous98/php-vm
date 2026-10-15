#!/usr/bin/env bash
# A/B-сравнение производительности VM: текущий master vs перф-референс 70d92dd.
# Все замеры — CPU-время (устойчиво к посторонней нагрузке на машину).
# Usage: bench/ab.sh [N]        N — повторов php-замеров (по умолчанию 3)
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
python3 bench/bench.py bench/phpvm_cur.test bench/work_cur "$N" | tee bench/cur.txt
python3 bench/bench.py bench/phpvm_ref.test bench/work_ref "$N" | tee bench/ref.txt
python3 - <<'PY'
import re
def load(p):
    d = {}
    for line in open(p):
        m = re.match(r'(\S+)\s+([\d.]+)\s+([\d.]+)\s+([\d.]+)\s+([\d.]+)\s+([\d.]+)', line)
        if m and not m.group(1)[:2].isdigit():
            d[m.group(1)] = (float(m.group(2)), float(m.group(4)), float(m.group(5)))
    return d
c = load('bench/cur.txt'); r = load('bench/ref.txt')
print(f"{'нагрузка':22} {'ref_CPU':>8} {'cur_CPU':>8} {'cur/ref':>8} {'ref_RSS':>8} {'cur_RSS':>8}")
for k in sorted(set(c) & set(r)):
    print(f"{k:22} {r[k][0]:8.1f} {c[k][0]:8.1f} {c[k][0]/r[k][0]:8.2f} {r[k][1]:8.1f} {c[k][1]:8.1f}")
PY
