# Типизированные packed-массивы (packedInt / packedFloat / packedStr)

## 1. Представление и переходы

`internal/vm/array.go`:

```go
type packedKind uint8
const (
    packedGeneric packedKind = iota // values []Slot    (24 Б/эл) — как раньше
    packedInt                       // ints   []int64   (8 Б/эл)
    packedFloat                     // floats []float64 (8 Б/эл)
    packedStr                       // strs   []pstr    (16 Б/эл)
)

type Array struct {
    packed bool
    kind   packedKind
    values []Slot    // packedGeneric
    ints   []int64   // packedInt
    floats []float64 // packedFloat
    strs   []pstr    // packedStr
    entries []KV; mStr map[string]int32; mInt map[int64]int32 // mixed — без изменений
    nextFree int64; shared bool; pos int
}

// элемент packedStr: те же (ptr, num), что у строкового слота,
// включая биты interned/buf — семантика 1:1
type pstr struct { ptr unsafe.Pointer; num uint64 }
```

* Вид выбирается при **первой записи в пустой** массив (`storeAppend` /
  `SetInt` по последовательному ключу — покрывает и `$a[] = v`, и `$a[$i] = v`).
* Первое значение «не того» типа один раз конвертирует массив в `packedGeneric`
  (`toGeneric`, копия O(n)); обратной типизации нет.
* int+float НЕ смешиваются: промоушен int→float менял бы наблюдаемый тип
  (`var_dump`/`===`/`is_int`) — такой массив уходит в generic.
* `packed → mixed` — как раньше: непоследовательный/строковый/отрицательный
  ключ, `unset`, ссылка на элемент (`RefType` не хранится типизированно).
* Строки хранятся парой (ptr,num) — биты `interned` и `buf` сохраняются;
  дописывание в строковый буфер (OpConcatApp) и его COW-инвариант не затронуты.
  Никакого unsafe-fetch элементов: int/float — обычные срезы, строки — тот же
  интерьерный указатель, что и в Slot (GC держит данные живыми).
* Рост типизированных срезов — удвоением (`appendInt64/appendFloat64/appendPstr`),
  а не Go-append (~1.25x у больших срезов): меньше промежуточных копий и пиковый RSS.
* `ReserveInts(n)` — пред-резерв под n int (array_keys/range).

## 2. Операции с типизированным путём

| Операция | Путь |
|---|---|
| Append (`$a[] = v`), IndexSet (`$a[$i] = v`) | `storeAppend`/`setAt` по kind, без Slot для int/float |
| IndexGet (`$a[$i]`), foreach | `GetInt`/`Get`: packedInt/packedGeneric разбираются без лишних вызовов (opIndex) |
| count/empty | `Len` по kind |
| foreach | `array_keys` для packed: `ReserveInts(n)` + `AppendInt(0..n-1)` (8 Б/ключ вместо 24) |
| array_sum/array_product | прямые циклы по `ints`/`floats` (`binopInts`), без `Values()` и per-element switch |
| sort/rsort | прямая сортировка `ints`/`floats` (SORT_REGULAR/SORT_NUMERIC) и pstr (SORT_STRING) на месте, без `[]arrayElem` и сравнения слотов |
| array_values | прямой цикл по ints + ReserveInts |
| array_keys/values/map/filter/merge/slice/push/pop/shift/unshift/unique/column, in_array/array_search, var_dump/var_export/json/serialize, reflection | результат/обход через типизированные `All()`/`Values()`/`Append`/`Set` (без материализации Slot для int/float) |
| unset элемента, ссылки (`&$a[i]`, `$r=&$a[0]`), дыры | перевод в mixed/generic — как в PHP по наблюдаемому поведению |

## 3. Аудит COW / ссылок

* `Unshare` копирует **активное** хранилище (ints/floats/strs/values), оригинал
  остаётся shared. Вложенных массивов в типизированных видах быть не может,
  поэтому рекурсивная пометка `MarkShared` для них — no-op.
* `MarkShared`/`shareSlot`/raw-опкоды не тронуты; транзитные пути по-прежнему
  не ставят shared.
* Запись ссылки (`$a[0] = &$b`, foreach-by-ref через `OpIndexRef`) кладёт
  RefType → одноразовый `toGeneric`, значение и алиасинг сохраняются.
* `array_walk` пишет обратно **значение** (`cell.Ref().Value()`), не ссылку, —
  типизированный массив остаётся типизированным.
* `sort`/`SortStrs` сортируют на месте только `Unshare`-нутую копию; далее
  `set(b)` пишет её обратно в Ref-ячейку аргумента.

## 4. Тесты

* `internal/vm/array_typed_test.go` — представление: выбор/смена kind, round-trip
  (MinInt64/MaxInt64, -0.0, +Inf, interned), COW, ссылка→generic, unset→mixed
  (дыры, порядок, nextFree), внутренний указатель, SortStrs, ReserveInts/рост.
* `internal/php/array_typed_diff_test.go` — **61** дифференциальный скрипт
  против живого php 8.3 (вывод байт-в-байт, включая предупреждения):
  COW/`&`-ссылки/foreach-by-ref/unset-дыры/отрицательные и строковые ключи/
  overflow int→float/`+` с float/var_export/var_dump/json/serialize/count/empty/
  array_splice/reverse/sort/asort/ksort/column/unique/list()/push-много/shift/
  unshift/pop/search/in_array(strict)/sum/product/map(null-callback)/merge/slice/
  filter/fill/range/flip/walk/objects/count_values/reduce/max/min/pointer/…
* Гейты: `gofmt` чисто; `go build ./...`; `go test -vet=off -count=1 ./...`;
  `GOGC=1 go test …`; `-race ./internal/vm/` — всё зелёное.
* PHPT-корпус: tests **670/733**, Zend/tests **2985/4179**,
  ext/standard/tests **2553/2723** — не ниже требуемого.

## 5. Микробенчи (min-of-3; до — дерево со старым array.go, после — текущее)

| bench | до | после | эффект |
|---|---|---|---|
| BenchmarkIterPacked (обход 1e5) | 123.0 мкс | 64.1 мкс | **1.92x** |
| BenchmarkBuild1e6 B/op | 127 920 200 | 16 777 328 | **7.6x меньше** |
| BenchmarkBuild1e6 ns/op | 58.0 мс | 15.0 мс | **3.9x** |
| BenchmarkBuild1e5 B/op | 12 970 056 | 2 097 264 | 6.2x меньше |
| BenchmarkBuild1e4 B/op | 976 968 | 262 256 | 3.7x меньше |
| BenchmarkBuild1e3 B/op | 59 464 | 16 496 | 3.6x меньше |
| BenchmarkPacked1e6 (random Get) | 14.6 ns | 10.0 ns | **1.45x** |
| BenchmarkPacked1e3 | 1.91 ns | 3.28 ns | +1.4 ns (граница вызова `Get`) |
| BenchmarkMixedInt1e6 | 144.2 ns | 145.6 ns | 0 |
| BenchmarkMixedStr1e6 | 193.0 ns | 208.0 ns | +8% (та же граница вызова; mixed-представление не тронуто) |

Пояснение к мелким размерам: `Get(ArrayKey)` до правок влезала в бюджет
инлайнера (cost 74/80) и разворачивалась на месте вызова; теперь в ней есть
типизированный разбор и один вызов хвоста — на L1-размерах это ~1.4 нс на
обращение. На 1e6 (уже не L1) выигрыш 8-байтового хранилища перекрывает вызов
(14.6 → 10.0 нс). Внутренние горячие пути VM (opIndex и т.п.) разбирают
packedInt/packedGeneric без лишнего вызова.

## 6. Комплексные нагрузки

`WORK=/tmp/bench/work RUNS=3 LIMIT=120 /tmp/bench/run_complex.sh`:
до — базовый прогон в начале сессии; после — финальный, плюс перепроверка
min-of-7 для шумных нагрузок (чередование old/new бинарей).

| нагрузка | наш до, мс | наш после, мс | RSS до, МБ | RSS после, МБ |
|---|---|---|---|---|
| 01_arr_build_sum | 301 | **149-155** | 142.8 | **37.4** |
| 02_arr_sort | 753 | **98-99** | 70.1 | **21.0-22.1** |
| 03_arr_assoc | 78 | 77-79 | 31.2 | 29.4 |
| 04_arr_fns | 183 | **156-167** | 69.2 | **48.3-49.3** |
| 05_str_chain | 56 | 51-56 | 31.4 | 25.4 |
| 06_str_builder | 21 | 20-22 | 17.0 | 16.8 |
| 07_str_fns | 77 | 72-76 | 22.6 | 18.3-18.5 |
| 08_obj_props | 154 | 136-146 | 57.6 | 52.0 |
| 09_obj_methods | 298 | 293-331 | 16.8 | 17.1-17.7 |
| 10_recursion | 129 | 124 | 13.6 | 13.5 |
| 11_mixed_json | 123 | 125-141 | 17.3 | 17.5-18.1 |
| 12_mixed_sort | 370 | 314-348 | 56.6 | 58.5 |

08/09/11 в одиночных прогонах шумят (±10%, разброс php-эталона в тех же
прогонах до 35%); при чередовании old/new min-of-7 разницы нет
(08: 133/143 vs 136/139; 09: 305/325 vs 293/306; 11: 124/125 vs 125/127).

Дополнительно: при `GOGC=50` пик RSS на 01 — 38.4 МБ (при базе ~11.5 МБ),
т.е. сами данные занимают ~27 МБ против ~130 МБ до правок.
