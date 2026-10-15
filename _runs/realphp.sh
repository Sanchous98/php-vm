#!/bin/bash
# realphp.sh <phpt-path> — прогон .phpt живым php как run-tests.php:
# cwd = корень корпуса, FILE выкладывается рядом, --INI-- применяется через -d.
set -u
f="$1"
dir=$(dirname "$f")
phpf="${f%.phpt}.php"
# FILE section
awk '/^--FILE--$/{p=1;next} /^--[A-Z_]+--$/{if(p)exit} p' "$f" > "$phpf"
args=(-d display_errors=1 -d error_reporting=E_ALL)
# INI section
ini=$(awk '/^--INI--$/{p=1;next} /^--[A-Z_]+--$/{if(p)exit} p' "$f")
if [ -n "$ini" ]; then
  while IFS= read -r ln; do
    ln="${ln%$'\r'}"
    [ -z "$ln" ] && continue
    case "$ln" in \[*|';'*) continue;; esac
    args+=(-d "$ln")
  done <<< "$ini"
fi
cd "$(dirname "$(dirname "$(dirname "$(dirname "$f")")")")" 2>/dev/null
cd /home/dev/e1_corp
php "${args[@]}" "$phpf" 2>&1
