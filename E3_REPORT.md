# e3_ext — отчёт волны (остаток ext/standard/tests минус кластеры e1)

Ветка: **e3**, worktree `/home/dev/wavebench/e3_work`, HEAD `050a33d`
(база — актуальный master `33f0319`, в него координатором уже влиты e1/e2/z3;
ветка e3 = master + merge e2-фиксов + дельта волны).
Корпус: `/home/dev/e3_corp` (копия php-src; ext/standard/tests, tests, Zend/tests).

## Было / стало

| корпус | база (master 3197be0, до e1/e2) | master 33f0319 (e1+e2+z3) | e3 (итог) |
|---|---|---|---|
| ext/standard/tests | 2638 / 2723 | 2709 / 2723 | **2716 / 2723 (99.7%)** |
| tests/ | 710 / 733 | (см. ниже) | 723 / 733 (98.6%) |

- Мой скоуп (ext/standard/tests без /array|strings|math|class_object|serialize|directory|dir/):
  на базе было 46 падений; к моменту работы 42 из них уже закрыты волной e2.
  **В e3 падений в скоупе нет** (7 оставшихся — хвосты кластеров e1:
  array 3, serialize 1, strings 3).
- Дельта e3 против master 33f0319 по ext/standard/tests: **+6** (0 регрессий).
  Закрыты: `bug75220`, `filters/filter_errors_user`, `general_functions/bug40398`,
  `general_functions/debug_zval_dump_b(+_64bit)`, `general_functions/debug_zval_dump_refs`,
  `streams/stream_get_meta_data_file_variation2`, `general_functions/is_callable_basic1`
  (последний — регрессия z-волн, в скоупе).

## Фиксы (системные)

1. **scoped-магия** (`internal/vm/callable.go`, `internal/vm/vm.go`):
   `staticMagicFallback`/`callMagicUndefStatic` — наличие `__call` проверяется по
   *целевому* классу, вызов диспетчеризуется по классу объекта (LSB);
   deprecation «Use of self/parent/static in callables» печатается после успешного
   разрешения класса (bug75220: `is_callable('parent::foo')===false` + deprecation;
   self:: с `__call` потомка — true; parent::foo() при `__call` только у потомка —
   Error; Zend bug45186/45186_2/48533 зелёные).
2. **debug_zval_dump** (`internal/php/builtins_misc.go`, `builtins_var.go`,
   новый `internal/vm/debug_refs.go`): refcount ссылки = вхождения при обходе
   аргумента + внешние держатели (globals/динамические локальные/помеченные
   регистры кадра); refcount массива = max(обход, держатели). Все 4 значения
   `debug_zval_dump_refs` совпадают с php 8.3 (2/3/2/1).
3. **Пре-буфер чтения** (`fbuf`, Zend `stream->readbuf`; `builtins_file.go`,
   `stream_wrappers.go`, `builtins_streams.go`): заполнение chunk-чтением,
   согласование SEEK_CUR/записи/усечения, `unread_bytes` в stream_get_meta_data,
   EOF не защёлкивается (bug41815). `stream_get_meta_data_file_variation2` — 13/13
   кластера.
4. **Ошибки пользовательских фильтров** (`stream_filters.go`):
   - `stream_filter_append` прогоняет пред-буфер: FATAL → Warning «Filter failed
     to process pre-buffered data» + фильтр не подключается; FEED_ME → буфер
     сброшен; PASS_ON → буфер заменён результатом;
   - предупреждение «Unprocessed filter buckets remaining on input brigade» при
     непустой входной бригаде после `filter()` (из `userfilter_filter`), с именем
     текущей функции;
   - `stream_filter_remove` — flush FLUSH_CLOSE; FATAL → «Unable to flush filter,
     not removing» (фильтр остаётся).
   `filters/filter_errors_user` — полностью зелёный.
5. **Литеральные строки элементов массивов** (`internal/php/compiler.go` ×
   `internal/vm/chunk.go`, `StrConstCopy`): в *полностью константных* литералах
   массива строковые элементы длиной >1 хранятся не-interned копиями (php 8.3:
   debug_zval_dump печатает `refcount(1)`); в смешанных литералах (с динамическим
   элементом) константы остаются interned. `debug_zval_dump_b`/`_64bit` — зелёные,
   `array/bug72369` не регрессирован.
6. **Восстановлен `OpFalse` в `emitUndefCheck`** (`compiler.go`, одноразовость
   «Undefined variable»): e2 его сняли — падал юнит `TestDiagTailUndefReadBeforeWrite`
   и 7 corpus-тестов с «Undefined variable» на e2. В master 33f0319 координатор
   тоже вернул OpFalse — здесь совпадает.
7. **Ремонт merge-бага master**: в `internal/vm/vm.go` (html-фатал) был пустой
   `else {}` — не печаталось «Uncaught … Class in file:line» для html-ошибок;
   блок выровнен по формату с ctxPrefix.
8. **`null`/`true`/`false` как имена функций** (`parser.go`): в грамматике Zend
   это T_STRING; `function NULL() {}` снова парсится (`is_callable_basic1`).
9. **Возвращён guard `lastFilterReadFailed`** (`compiler.go`, диагностика include):
   при сбое пользовательского фильтра во время чтения Zend не печатает
   «Failed to open stream», только «Failed opening … for inclusion»
   (`Zend/tests/include_fail_during_read`; guard был в master 3197be0, потерян
   при мерже e2).

## Проверки

- Целевые тесты 20/20; `stream_get_meta_data*` 13/13; `debug_zval_dump*` 4/4;
  Zend callable-батч 15/16 (единственный «провал» — `bug78770` c `--EXTENSIONS-- intl`,
  харнесс его не скипает — предсуществующее).
- Юнит-тесты волны: `TestE2IO*`, `TestRootTailARegress*`, `TestDiagTail*` — зелёные.
  `TestRegress21` падает на master и e2 (предсуществующее, security-батч).
- `go test ./...`: кроме `TestRegress21` — чисто (проверено прогоном набора).

## Оставшееся (вне скоупа, предсуществующее на master 33f0319)

- tests/ (10 падений): z-регрессии `classes/abstract_redeclare`, `classes/array_access_001/002`,
  `classes/constants_basic_006`, `lang/bug35176`, `lang/engine_assignExecutionOrder_001/008`,
  `lang/foreachLoop.016`; хвосты e2 `basic/029`, `lang/passByReference_005`.
  Все воспроизводятся на master 33f0319 (у меня нет ни одной новой).
- ext/standard/tests: хвосты e1 — array (array_product_variation5, array_walk_closure,
  bug25708), strings (bug55871, bug72434, bug72663_2), serialize (bug70219_1).
- Известные общие: `Zend/tests` — вне моего набора (полный прогон Zend в фоне;
  фиксирую только отсутствие новых падений на затронутых областях).
