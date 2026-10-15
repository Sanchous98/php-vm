# bench — перф-стенд PHP-VM

12 PHP-нагрузок, наш VM vs php: CPU-время (устойчиво к посторонней загрузке) и RSS.

## Docker (ничего не нужно ставить локально)
Linux / macOS / Git Bash:
```bash
docker build -t phpvm-bench .
docker run --rm -v "$PWD":/repo phpvm-bench          # A/B: cur vs референс 70d92dd, N=3
docker run --rm -v "$PWD":/repo phpvm-bench 5        # N=5
docker run --rm -it -v "$PWD":/repo phpvm-bench bash # ручной режим (+ /corpus = php-src PHP-8.3)
```

Windows PowerShell — кавычить весь volume-spec целиком и `$PWD` брать в скобки
(`"$PWD":/repo` распадается на два токена → docker: invalid reference format):
```powershell
docker build -t phpvm-bench .
docker run --rm -v "${PWD}:/repo" phpvm-bench
docker run --rm -v "${PWD}:/repo" phpvm-bench 5
docker run --rm -it -v "${PWD}:/repo" phpvm-bench bash
```
Альтернатива без монтирования: `docker run --rm phpvm-bench` после `docker build`
(стенд прогонится по копии репозитория внутри образа).

## Локально
Требования: Go (1.27+), python3, php 8.3 (`PHP_BIN` или php в PATH).
```bash
bash bench/ab.sh 3                                   # A/B одной командой
GOTOOLCHAIN=go1.27.1 go test -c -vet=off -o bench/phpvm.test ./internal/php/
python3 bench/bench.py bench/phpvm.test bench/work 3 # только наш VM vs php
```

## Что смотреть
- `cur/ref` ≈ 1.00 — регрессий нет; >1.10 — присмотреться к соответствующей нагрузке.
- Сравнивать только cur vs ref, снятые в одном сеансе; абсолютные числа зависят от машины.
- Артефакты: `bench/cur.txt`, `bench/ref.txt` (полные таблицы с ratio к php).
