#!/usr/bin/env bash
# Точка входа образа phpvm-bench.
#   entry.sh [N]   — прогнать bench/ab.sh N (по умолчанию 3)
#   entry.sh bash  — интерактивный shell
set -euo pipefail
if [ "${1:-}" = "bash" ] || [ "${1:-}" = "shell" ]; then
    exec bash
fi
if [ ! -f /repo/bench/ab.sh ]; then
    echo "Репозиторий не смонтирован. Запусти:" >&2
    echo "  docker run --rm -v \"\$PWD\":/repo phpvm-bench" >&2
    exec bash
fi
cd /repo
exec bash bench/ab.sh "${1:-3}"
