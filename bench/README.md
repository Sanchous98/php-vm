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

## Сравнение с php+JIT
`JIT=1` включает у php opcache CLI + JIT (tracing):
```bash
JIT=1 bash bench/ab.sh 3
```
Docker:
```bash
docker run --rm phpvm-bench jit                      # или: -e JIT=1
docker run --rm -v "${PWD}:/repo" -e JIT=1 phpvm-bench
```
Свои опции php — через `PHP_ARGS` (пример: `PHP_ARGS="-d opcache.jit=function -d opcache.jit_buffer_size=32M"`).
Ориентир (php vs php+JIT на 12 нагрузках): JIT заметно помогает только объектным
нагрузкам (08_obj_props ×1.4, 09_obj_methods ×1.4, 10_recursion ×1.2), остальные —
в пределах шума: прогоны короткие, компиляция не окупается.

## Что смотреть
- Итоговая таблица печатается в конце: `cur/ref` ≈ 1.00 — регрессий нет; >1.10 — присмотреться.
- Каждая нагрузка берётся как **min по двум перемежающимся проходам** (гасит ноутбучный шум:
  троттлинг, антивирус, прогрев); сравнивать только cur vs ref из одного запуска.
- Сырые таблицы: `bench/cur_1.txt`, `bench/cur_2.txt`, `bench/ref_1.txt`, `bench/ref_2.txt`
  (там же ratio к php и RSS).
