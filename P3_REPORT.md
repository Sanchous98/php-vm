# F3: foreach по значению — позиционный обход снимка (без array_keys)

Ветка `p3_foreach`, коммит **06e44cd** (база `72d8a6b` = master c F1/F2). Не мержить.

## 1. Что сделано

### Компилятор (`internal/php/compiler.go`, `foreachStmt`, массивная by-value ветка)

Было (дизасм 03: `CALL __php_foreach_check`, `CALL array_keys`, далее на каждый элемент 2×INDEX):
```
keys = array_keys(src)            ; копия ВСЕХ ключей
i = 0; limit = len(keys)
loop: LESS/JF; keyR = INDEX keys[i]; valR = INDEX src[keyR]; <цели>; i++; JUMP
```

Стало:
```
ITER_INIT  state, src, C=3        ; состояние: снимок = src, pos = 0
loop: ITER_VALID  cond, state; JF end
      [ITER_KEY  keyR, state]     ; только если ключ реально нужен
      ITER_CURRENT valR, state
      <store целей цикла>; тело
      ITER_NEXT  state; JUMP
end:  NULL state/src/keys/i/limit/...
```
`keyR/valR` выделяются всегда: они входят в список `free` (NULL по выходу из цикла,
в т.ч. по break/return), а нулевой регистр — живая переменная main-скоупа.
By-ref ветка (C=2) и объектная ветка не тронуты.

### VM (`internal/vm/iterator.go`, `execArray.go`, `array.go`)

- Новый режим состояния (C=3) → `iterInitArrayVal(src)`: PHP-массив-состояние с
  Go-полями `valIter=true`, `valSnap *Array` (снимок), `valPos int32`.
- `iterValid/iterKey/iterCurrent/iterNext` получили быстрые ветки по флагу
  `valIter` **до** map-диспетчера по `"mode"` — горячий цикл не платит map-lookup
  состояния.
- `iterValValid` — позиция ведётся по хранилищу снимка (`entries` для mixed,
  `values|ints|floats|strs` для packed); tombstone-ы (unset ДО цикла)
  пропускаются. `iterValKey` — `keyAt(pos)` (ключ packed = позиция).
  `iterValCurrent` — `elementAt(pos)` + `deref` (элемент-ссылка разыменовывается,
  значение фиксируется в момент чтения, как FETCH_DIM_R). `iterNext` — `valPos++`.
- Состояния by-value НЕ регистрируются в `iterHints`/`anyArrayRefIter`:
  iterremap/позиционный пересчёт by-ref-обходов не затронут.

### Почему снимок-семантика сохраняется

Инвариант тот же, что у прежнего пути (`INDEX src[key]` по снимку): `OpMove` перед
циклом помечает источник `shared`; любая запись в теле (элемент, `unset`,
by-ref builtin, массив-свойство, запись через Ref-алиас) идёт через `Unshare()` →
COW-копия, а `valSnap` не меняется. Позиционный обход неизменяемого снимка
эквивалентен обходу по `array_keys(src)`: тот же порядок, те же пропуски
tombstone-ов, добавления/удаления в теле не видны, вложенность — независимые
состояния. `current/key/reset/next/end` работают с `Array.pos`, как и раньше
(foreach его не трогает). Warning-и (`foreach(): argument must be of type
array|object`) выдаёт прежний `__php_foreach_check` в той же точке.

## 2. Гейты (кандидат vs база 72d8a6b)

| гейт | база | кандидат | падения |
|---|---|---|---|
| `go test ./...` | TestRegress21 | TestRegress21 | идентично |
| merged_check | 92/162 | 92/162 | набор FAIL идентичен (diff) |
| tests | 731/733 | 731/733 | те же 2 (basic/029, constants_basic_006) |
| ext/standard/tests | 2719/2723 | 2719/2723 | те же 4 (array_walk_closure, bug25708, bug45303, bug55871) |
| Zend/tests + tests/lang foreach* | — | **73/73** (Zend 44, tests 29) | 0 |
| foreach_ref / iterremap / iterator юниты | зелёные | зелёные | 0 |

Новый дифференциальный тест `internal/php/foreach_val_agent_test.go` — **67 кейсов
против живого php 8.3**: снимок при append/присваивании/unset/alias, ссылки в
элементах и ссылки-элементы на другие элементы, tombstone-ы до цикла, вложенные
foreach и by-ref внутри by-value, current/key/reset/next/end внутри и после,
packed int/float/str/generic и mixed (int-ключи вразнобой, канонизация),
свойство/элемент/временное выражение как источник, `unset($GLOBALS[...])` и
запись через `$GLOBALS`, by-ref builtins в теле (push/pop/shift/unshift/splice/
sort/rsort/array_walk), list-деструктуризация, break/continue 2, warning-и.
Зелёный и на базе, и на кандидате.

## 3. Замеры (машина загружена, load 14–18 на 4 ядрах)

### min-of-5, benchtime 1x, перемежение base/cand (протокольный замер)

| нагрузка | base | cand | Δ | содержит foreach по массиву? |
|---|---:|---:|---:|---|
| 01_arr_build_sum | 89.8 | 89.2 | −0.7% | да (300k packed-int) |
| 02_arr_sort | 166.7 | 156.5 | −6.1% | да |
| **03_arr_assoc** | **281.6** | **251.3** | **−10.8%** | да (150k mixed) |
| 04_arr_fns | 73.6 | 77.4 | +5.2% | нет (array_map/filter/reduce) |
| 05_str_chain | 53.4 | 64.7 | +21.0% | нет |
| 06_str_builder | 34.2 | 37.6 | +10.1% | нет |
| 07_str_fns | 98.6 | 110.1 | +11.6% | нет |
| 08_obj_props | 344.4 | 339.4 | −1.4% | нет |
| 09_obj_methods | 380.6 | 375.6 | −1.3% | нет |
| 10_recursion | 165.8 | 163.6 | −1.3% | нет |
| 11_mixed_json | 78.5 | 78.5 | +0.0% | нет |
| 12_mixed_sort | 432.3 | 446.0 | +3.2% | нет |

Повторы одиночной 03: −11.9%, −11.9%, −4.3%; 01: −6.8%, −11.9%. Разброс — фон.

### Дельта-метод `cpu(10x−1x)/9`, min-of-3, перемежение (без стартапа процесса)

| нагрузка | base | cand | Δ |
|---|---:|---:|---:|
| 01_arr_build_sum | 79.1 | 72.4 | −8.5% |
| 02_arr_sort | 153.1 | 148.8 | −2.8% |
| **03_arr_assoc** | **282.1** | **228.5** | **−19.0%** |
| 03a (тот же 03, foreach без unset) | 197.7 | 164.9 | −16.6% |
| 03b (03 с unset) | 284.5 | 242.0 | −15.0% |

Цель F3 (03 −15…20%) по дельта-методу достигнута: −16.6% на самой foreach-части
(сравн. с оценкой в перф-отчёте «a3 95→~20 мс»); −10.8% по min-of-5 1x с
учётом стартапа процесса.

### Шумовая полоса (A/A: база против базы, та же дельта-методика)

| нагрузка | base | base' | Δ |
|---|---:|---:|---:|
| 04_arr_fns | 64.5 | 66.7 | +3.5% |
| 05_str_chain | 48.3 | 44.3 | −8.1% |
| 06_str_builder | 21.3 | 22.7 | +6.7% |
| 07_str_fns | 100.2 | 112.3 | +12.2% |

Отсюда: дельты по нагрузкам 04–12 (F3 их кода не касается) целиком лежат в
шумовой полосе вплоть до ±20% — на загруженной машине их «регрессии» не значимы;
для 01/02/03 сигнал устойчив (те же знаки во всех методах/повторах).

## 4. Риски и остаток

1. Инвариант «снимок неизменяем» — тот же, что у прежнего пути по array_keys
   (INDEX по снимку). Если найдётся путь записи в массив без `Unshare()`, новый
   обход отреагирует иначе (добавленные в теле элементы могут попасть в обход);
   таких путей не найдено (батареи + by-ref builtins + полный foreach-корпус).
2. COW-копия при `unset` в теле (03b: ~30–40 мс/итер, перестройка map в
   `Unshare`) в F3 не входит — это территория S1 (своя хеш-таблица).
3. Полные Zend/tests целиком мной не прогонялись (по директиве — за
   координатором); прогнаны: полный foreach*-срез (73), merged_check, tests,
   ext/standard — падения идентичны базе.
4. Остаток по F3: fused ITER_VALID+KEY+CURRENT сократил бы ещё ~1 handler-вызов
   на элемент (~1–2% 03); `iterModeArrayVal` (4) оставлена как документация
   нумерации режимов (состояние идентифицируется флагом `valIter`).

Артефакты: `p3_work` (ветка), `p3_base.test`/`p3_cand.test`,
`/tmp/p3_logs/*` (гейты), `/tmp/p3_bench*.txt` (замеры),
`/tmp/p3_bench*.py`, `/tmp/p3_bench_aa.py` (харнессы).
