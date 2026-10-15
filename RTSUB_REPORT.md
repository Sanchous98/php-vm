# rtsub — отчёт волны (остаток подкаталогов Zend/tests)

Ветка: **rtsub**, worktree `/home/dev/wavebench/rtsub_work`, HEAD `61c4832`.
Скоуп: `/home/dev/wavebench/lists/z3_subdirs.txt` (78 тестов), корпус
`/home/dev/rtsub_corp`. Харнесс: `PHPT_DIR=... PHPT_LIST=... go test -run TestPHPTree`.
Сборка/прогон: `GOTOOLCHAIN=go1.27.1`, `-vet=off`, gofmt.

## Было / стало

| набор | до сессии (WIP 324c4d2) | после (61c4832) |
|---|---|---|
| z3_subdirs (78) | **54 / 78** (24 падения) | **56 / 78** (22 падения) |
| Zend/tests/anon/011 | FAIL (`Undefined property anon#1::$foo`) | **PASS** |
| Zend/tests/return_types/reflection001 | FAIL (нет `@@`-секций) | **PASS** |
| вне списка: bug43200, ctor_promotion_mixing | FAIL | **PASS** |
| ext/standard/tests | 2715 / 2723 | **2716 / 2723** (восстановлен pre-existing регресс `parse_ini_string_error`) |
| tests/ (733) | 723 / 733 | 723 / 733 (набор падений идентичен) |
| юниты `go test ./...` | только предсуществующий `TestRegress21` | только `TestRegress21` |

Регрессий не выявлено (сверка наборов падений tests/ до/после; ext — база ветки
без моих правок тоже 2715 с тем же набором, значит `parse_ini_string_error`
сломался раньше, коммитом `f9b0b2f`, и восстановлен здесь).

## Фиксы (файлы + суть)

1. **`internal/php/compiler.go` — `mainStaticStmt`** (constexpr/new).
   Верхнеуровневое `static $x = ...` определяло «уже инициализировано» по
   наличию одноимённой глобальной. Если переменная с тем же именем уже была
   (в тесте — `$e` из `catch`), инициализация пропускалась и `$e` оставался
   исключением. Теперь признак инициализации — отдельная скрытая ячейка
   глобалов (ключ без ведущего `$`, в `$GLOBALS`/`GlobalsSnapshot` не видна),
   а рекурсивный guard сохраняет прежнюю семантику (`static $a = $a;`).

2. **`internal/php/compiler.go` — `anonNew`** (anon/011).
   Анонимный класс с родителем/интерфейсом, неизвестным на компиляции
   (`new class extends AnonBase`, где `AnonBase` — `class_alias` анонимного
   класса, объявленный перед выражением), регистрировался скрытым, но
   `OpDeclareClass` для него не эмитился — родитель не линковался, свойства
   терялись. Теперь в точке `new class` эмитится `OpDeclareClass`.

3. **`internal/php/parser.go` — statement()**.
   `namespace\Foo();` внутри блока разбиралось как объявление namespace →
   ложный Fatal «Namespace declaration statement has to be the very first…».
   `nsRelativeStmtAhead()` теперь проверяется и в `statement()` (уходим в
   разбор выражения как для T_NAME_RELATIVE). Убирает лишний
   parse/fatal-барьер (assert/expect_015 и др.).

4. **`internal/php/compiler.go` — `dynCallByNameRef`**.
   Вызов функции, неизвестной на компиляции, с распаковкой `f(...$args)`
   падал `компилятор: неизвестное выражение *php.ArgSpreadExpr`. Добавлена
   ветка: `buildArgArray` + `__php_call_named` (как для именованных).

5. **`ReflectionClass::__toString` для userland-классов** (reflection001,
   bug43200, ctor_promotion_mixing; ранее печаталось только `Class [...] {\n}\n`):
   - `internal/php/parser.go`: `ClassMember.EndLine`, `ClassDecl.EndLine`,
     поле `Parser.lastBraceLine` (строка закрывающей `}` тел метода/класса);
   - `internal/php/compiler.go`: `Function.DefLine/DefEnd`, `ParamDefaults`
     (`renderDefaultExpr`), `SetClassDefinition` получает реальный конец
     класса (`classEndLine`);
   - `internal/vm/function.go`: поля `DefEnd`, `ParamDefaults`;
   - `internal/vm/reflection.go`: `reflUserClassString` / `reflUserMethodString` /
     `reflParamFirstLine` / `renderDefaultValue` — разделы Constants / Static
     properties / Static methods / Properties / Methods, `@@ file start-end`,
     параметры (`<required>/<optional>`, тип, `&`, `...`, `= default`), возврат,
     флаги `<user[, ctor][, inherits X][, prototype I]>` (прототип — последний
     интерфейс в порядке `implements`, объявляющий метод; учитываются
     `sigFns` интерфейсов). `ReflectionMethod::getEndLine` теперь использует
     `DefEnd`.
   Вне списка восстановлены `Zend/tests/bug43200` и
   `Zend/tests/ctor_promotion_mixing`.

6. **`internal/php/builtins_file.go` — ini `${var:-fallback}` без `}`**
   (восстановление ext `parse_ini_string_error`; защищённый ini-фикс не
   откатывается, а уточняется). Коммит `f9b0b2f` сделал сообщение всегда
   «unexpected end of file, expecting '}'» (фикс oss_fuzz_428983568), но для
   `a=${b:-a` (EOF после fallback, без перевода строки) Zend печатает
   «unexpected TC_FALLBACK, expecting TC_VARNAME». Теперь различаем: EOF →
   TC_FALLBACK/VARNAME, перевод строки → «expecting '}'». Оба теста зелёные.

## Проверки

- z3_subdirs: **56/78** (`_runs/rtsub_final.log`).
- `go test ./...`: только предсуществующий `TestRegress21` (`_runs/units_after3.log`).
- tests/: 723/733, набор падений идентичен базе (`_runs/tests_after3.log`).
- ext/standard/tests: 2716/2723, набор падений = хвосты e1 (array 3, strings 3,
  serialize 1) (`_runs/ext_after3b.log`).
- Полный Zend-прогон не делался; изменение Reflection-`__toString` затрагивает
  только userland-`ReflectionClass::__toString` (в корпусе — 3 теста, все три
  зелёные).
- Отмечено один раз (не воспроизведено за 4 профильных прогона): редкий
  SIGSEGV в `ssaGraph.computeLiveness` при компиляции — похоже на
  предсуществующую флейку (unsafe-модель/GC), к правкам не привязан.

## Оставшиеся 22 (с причинами)

- **AST-export в assert() (3: expect_015, expect_020, attributes/012_ast_export;
  + enum/ast-dumper)**. Сообщение assert строится из *исходного текста*
  аргумента + текстовые нормализации (`parser.go:finalizeAssertText`), а не из
  настоящего `zend_ast_export`. Ожидаемые выходы требуют структурной
  перепечатки AST (разбиение `global $a, $$b`, `list()`→`[]`, `${var}`→`{$var}`,
  канонизация пустых тел/классов, отступы/пустые строки у членов enum). Для
  expect_015/020 дополнительно нужно подавление Deprecated `${var}` внутри
  assert (у нас оно печатается, Zend — нет) и прохождение полного парсера.
  Это отдельная полноценная фича, высокая цена/риск.
- **Номера объектов/хендлы (7: constexpr/new — теперь расходится только
  внутренний `stdClass#3` vs `#1`; generator_with_nonscalar_keys, cache_slot,
  typed_properties_043, union_types/multiple_classes, readonly_containing_object,
  weakmap_weakness)**. Нужна точная модель object-store Zend (порядок
  выделения/освобождения, free-list LIFO с учётом временных объектов,
  ленивые хендлы). У Zend `yield K=>V` с двумя объектами даёт #3/#4, а один
  объект — #2: расхождения из-за внутренних временных, точное воспроизведение
  требует отдельного моделирования; правка глобального счётчика рискует
  множеством проходящих handle-тестов.
- **GC-циклы генераторов (2: bug76427, gc_with_yield_from)**. `bug76427`:
  `gc_collect_cycles()` считает 3 вместо 2 — генератор, достижимый только из
  циклического мусора, у нас входит в «белый» счёт, а Zend уничтожает его по
  refcount (не считает). Снятие этого требует различать «корневые» белые узлы
  от «производных». `gc_with_yield_from`: `unset($gen,$gens)` у нас запускает
  `finally` немедленно (печатается NULL до `collect`), а Zend — только на
  `gc_collect_cycles()`. Обе — в тонкой модели сборщика (раунд 21), высокая
  цена и риск для уже закрытых gc_*.
- **zend.multibyte + mbstring (4: multibyte_encoding_001/003/004/005)**. Нужна
  поддержка `zend.multibyte`, `zend.script_encoding`, перекодировки исходника
  до лексера (Shift_JIS/UTF-16, multibyte-aware разбор строк) и
  `mbstring.encoding_translation`. Целой подсистемы нет.
- **pcntl-сигналы в fibers (2: signal-async, signal-dispatch)**. Нужны
  `pcntl_async_signals`, `pcntl_signal`, `pcntl_signal_dispatch`, SIGUSR1,
  `posix_kill`/`posix_getpid`, `time_nanosleep` и семантика запрета переключения
  файберов в обработчике сигнала (FiberError). Нет ни сигналов, ни posix.
- **Цепочка lifetime для SensitiveParameterValue
  (function_arguments/sensitive_parameter_value_keeps_object_alive)**.
  Трейс исключения у нас не удерживает аргументы (deepCopyExceptionFrames не
  addref'ит), поэтому объект уничтожается при выходе из функции-владельца, а
  Zend держит его до `unset($v)`. Попытка addref при снятии трейса не помогает:
  lifetime-обход не сканирует `Object.excTrace`, т.е. объект всё равно
  признаётся недостижимым (нужен обход аргументов трейса в сборщике). Именно
  эта правка была откачена в `79158a5`; трогать без обхода — риск утечек.
- **gh11028_3**. `[...generator()]` с недопустимым ключом: Zend при ошибке
  уничтожает итератор, `finally` бросает исключение и оно заменяет исходную
  ошибку (`exception`). У нас ключ-ошибка возвращается, генератор не
  освобождается (локальная ссылка в `SpreadValues`), его `finally` не
  выполняется. Нужно освобождение итератора при ошибке распаковки.
- **yield_from_deep_recursion (50000 уровней)**. `yield from` у нас рекурсивен
  (нативный Go-стек) → stack overflow. Нужна плоская модель делегирования.
- **readonly_containing_object**. Нативный `ArrayObject`: `$ao[0][] = 1`
  должен писать по ссылке в storage (Zend read_dimension BP_VAR_W), у нас —
  Notice «Indirect modification» и потеря записи (Ref-модель слотов не
  адресует элемент массива), плюс расхождение хендлов.

## Прочее

- Защищённые решения не откатывались (writeback+entryNull, Ref.sources,
  rope/strbuf, raw-опкоды by-ref, packed, IC, variance, list-ref, named-args,
  ticks, shutdown-порядок, killOrphan, jsontext, frameIdx, staticUnbound/
  bindStatic, opGlobalSetDyn, F1/F2/p1/p3). Ini-фикс не откачен, а уточнён.
- Ветка rtsub: `61c4832`; не мержить.
