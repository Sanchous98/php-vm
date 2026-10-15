#!/usr/bin/env bash
# Точка входа образа phpvm-bench.
#   entry.sh [N]   — прогнать bench/ab.sh N (по умолчанию 3)
#   entry.sh bash  — интерактивный shell
set -euo pipefail

# CRLF-чекаут (Windows, core.autocrlf=true): bash/python не переваривают \r —
# нормализуем скрипты на месте (в образе они уже LF; это для -v-монтирования).
for f in /repo/bench/ab.sh /repo/bench/bench.py /repo/bench/entry.sh; do
    if [ -f "$f" ] && grep -qU $'\r' "$f" 2>/dev/null; then
        sed -i 's/\r$//' "$f"
        echo "phpvm-bench: нормализован CRLF → LF: $f" >&2
    fi
done

if [ "${1:-}" = "bash" ] || [ "${1:-}" = "shell" ]; then
    shift
    exec bash "$@"
fi
if [ ! -f /repo/bench/ab.sh ]; then
    echo "Репозиторий не смонтирован. Запусти:" >&2
    echo "  docker run --rm -v \"\$PWD\":/repo phpvm-bench" >&2
    exec bash
fi
cd /repo
exec bash bench/ab.sh "${1:-3}"
