# Агент `z3_subdirs` — отчёт

Корпус: 78 тестов Zend/tests в подкаталогах (generators 13, readonly_props/classes 8,
enum, match, assert, attributes, constexpr, multibyte, typehints, nullsafe_operator,
varSyntax, arrow_functions, anon и др.). Список — `wavebench/lists/z3_subdirs.txt`,
корпус — `/home/dev/z3_corp` (копия phpsrc), ветка `z3` в worktree `z3_work`.

## Было/стало

| прогон | результат |
|---|---|
| база (master 5edebad) | **0 / 78** |
| после всех правок | **39 / 78 (50%)** |
| контроль: затронутые подкаталоги целиком (`Zend/tests/{anon,arrow_functions,assert,attributes,constants,constexpr,enum,fibers,function_arguments,generators,grammar,in-de-crement,match,nullsafe_operator,numeric_strings,object_types,readonly_classes,readonly_props,restrict_globals,return_types,throw,traits,type_declarations,typehints,varSyntax,weakrefs,zend_ini}`) | 1471/1513 → **1478/1513 (97.7%)**, все провалы — тесты из моего списка или из списков других агентов (`fails_*`, `z2_*`, `root_*`); регрессий нет |
| контроль: весь `Zend/tests` чанками по 400 (PHPT_OFFSET/PHPT_LIMIT) | без изменений множества провалов по чанкам; чанки 400/800/2000 — предсуществующее зависание (`decRefsSeen` на больших массивах в `wRegSlow`, воспроизводится и до правок) |
| перф | изменений нет (правки вне горячих путей; `trackedType`/инлайн-опкоды не трогались) |

## Сделанные фиксы (по кластерам)

### yield from / генераторы (главный кластер)
- **DO_INIT** (Zend `ZEND_GENERATOR_DO_INIT`): флаг ставится в начале делегации
  (только при вложенном resume), а у внешнего генератора снимается по завершении
  resume; первый `next()/send()/throw()` на делегирующем генераторе, у которого у
  текущего (самого вложенного) генератора уже есть значение, — no-op. Проверка
  только на внешнем вызове (`genCallDepth`), не в рекурсии цепочки — без этого
  ломались `yield_from_array`, `basic_yield_from_proxying` и др.
  → `backtrace_multi_yield_from`.
- **Освобождение кадра генератора** (`dropSavedRegs`): при завершении тела и при
  принудительном закрытии локальные переменные освобождаются (аналог
  `zend_free_compiled_variables` + flush жизненного цикла) → `bug71013`.
- **Ссылка на вложенный генератор** (`releaseInnerRef`): снимается до finally
  внешнего; закрываем вложенный, только если счётчик ссылок учитывался и обнулился
  (Zend `zend_generator_dtor_storage`) → `bug71601` (остался зелёным),
  `repeated_yield_from_with_immediate_release`.
- **Трейс-кадры делегирующих генераторов**: место вызова вложенного — строка его
  `yield from`; кадр снимается до освобождения локалов, поэтому деструкторы локалов
  видят вызывающий кадр → `backtrace_multi_yield_from`, `gh15851`.

### readonly (clone/переинициализация)
- `IS_PROP_REINITABLE`: флаг снимается только после успешной проверки типа
  (провал типа его сохраняет); unset в `__clone` разрешён; косвенная модификация
  (`$this->prop[] = 1`) снимает флаг, а запись-обратно идёт без повторной проверки
  (`wbSkip` с пометкой `roOK`); значения-объекты в W/RW-режиме отдаются копией.
  → `readonly_clone_success2/3/4`, `readonly_containing_object` (частично),
  `readonly_classes/*` (`#[AllowDynamicProperties]` на readonly-классе — фатал;
  `unserialize` — «Cannot create dynamic property» вместо deprecation).
- `PRE_INC_OBJ` перечитывает ячейку свойства (обработчик ошибки из warning'а мог
  записать в то же свойство) → `in-de-crement/unset_property_converted_to_obj_in_error_handler`.

### Парсер/компилятор
- `function <ключевое слово>()` и `const <ключевое слово> =` на верхнем уровне —
  Parse error с текстом Zend; `[1, FOREACH]` — `expecting "]"`;
  `exit` как аргумент (`new stdClass(exit)`); атрибуты перед модификаторами класса
  (`#[A] readonly class Foo`) — раньше терялись; квалифицированные встроенные типы
  (`\int`, `namespace\iterable`) — «must be unqualified»; `__CLASS__` вне класса = "".
  → grammar/regression_004/005/012, typehints/*, constants? throw/leaks, readonly_classes.

### Прочее
- **Анонимные классы**: display-имя из первого интерфейса (`Two@anonymous`),
  корректное имя в var_dump private-свойств (`class@anonymous`), `funcDisplayName`.
- **Динамические переменные** (`$$x`, `${expr}`) в функциях ищутся в локальной
  области кадра, а не в глобалах; `${"this"}` → `$this`; автозахват в стрелочных
  функциях (`$$var`, `${5}`); `ReflectionFunction::getClosureThis()`.
- **numeric_strings**: смещения в интерполяции (`"$a[00]"`) — строковые ключи;
  warning на каждый нечисловой операнд; унарный плюс = `x * 1`.
- **restrict_globals**: чтение `$GLOBALS[k]` → «Undefined global variable $k»;
  `$ref =& $GLOBALS['x']` — ссылка на саму глобальную переменную.
- **nullsafe/static**: динамическое имя статического свойства (чтение) — Error;
  динамическое имя метода — «Method name must be a string».
- **by-ref аргумент-значение**: в прямом вызове (не через `call_user_func*`) —
  фатальная Error «could not be passed by reference», а не Warning
  (`match/027`, `match/028`, `nullsafe_operator/016`).
- **enum**: `debug_zval_dump` печатает кейс как объект, `name` — interned,
  refcount кейса ≥ 2 → `enum/cases-refcount`, `enum/unserialize-refcount`.
- **varSyntax**: «Cannot use temporary expression in write context» для by-ref
  аргумента-временного выражения.
- **SPL**: реализован `IteratorIterator` (обёртка над итератором, `parent::key()`,
  ленивая привязка на `rewind`), кадр пользовательского метода итератора в трейсе
  виден, нативного — скрыт → `generators/gh8289`.

## Остаток (39 тестов) и причины

- **Трассировки/цепочки исключений**: `function_arguments/sensitive_parameter_eval_call`
  (кадр eval()'d code), `enum/update-class-constant-failure` (у нас лишний кадр
  `[constant expression]` — в Zend для enum-констант кадра нет, но для property
  defaults/ленивых констант он есть: `enum/offsetGet-in-const-expr`,
  `typed_properties_022` требует обратного — решено признаком enum-скоупа
  у константы), `generators/gh14387` (в живом php
  8.3.6 тест падает сегфолтом), `yield_from_throw_with_throwing_exception`
  (исключение деструктора при раскрутке: нужно `zend_exception_set_previous`,
  у нас ошибка теряется в flush'е).
- **AST-export в assert()** (`arrow_functions/007`, `assert/bug70528`,
  `assert/expect_015/020`, `attributes/012_ast_export`, `enum/ast-dumper`,
  `match/009_ast_export`): нужен полноценный `zend_ast_export` по AST
  (у нас текст из исходника: лишние скобки, `new X` без `()`, форматирование тел).
  Часть точечно исправлена (см. список ниже).
- **Номера объектов/handle'ы** (`readonly_clone_success2`, `readonly_props/cache_slot`,
  `readonly_clone_error5`, `generator_with_nonscalar_keys`, `typed_properties_043`,
  `union_types/multiple_classes`, `weakrefs/weakmap_weakness`,
  `function_arguments/sensitive_parameter_value_keeps_object_alive`): PHP выделяет
  handle объекта до вычисления аргументов `new`, учитывает объект-итератор foreach
  по генератору и повторно использует освобождённые handle'ы (free-list) — модель
  счётчика в VM иная; плюс трейс-аргументы исключения у нас не считаются ссылками.
- **GC-порядок/циклы** (`generators/bug76427`, `generators/gc_with_yield_from`):
  закрытие генератора на `unset` (по refcount, а не сразу) и число циклов в
  `gc_collect_cycles`.
- **zend.multibyte** (`multibyte/multibyte_encoding_001/003/004/005`): поддержка
  `zend.multibyte`/`mbstring.script_encoding` (BOM/UTF-16/Shift_JIS в лексере).
- **pcntl-сигналы** (`fibers/signal-async`, `fibers/signal-dispatch`): нужны
  `pcntl_async_signals`, `pcntl_signal`, `SIGUSR1` и доставка сигналов.
- **Deprecated для null-аргументов встроенных функций** и арность
  (`nullsafe_operator/013` — сделан: defined/chr/ord/array_slice, имена параметров,
  ArgumentCountError для func_num_args/func_get_args/get_called_class).
- **Прочее по одному**: `generators/yield_from_deep_recursion` (плоская модель
  цепочки yield from вместо Go-рекурсии, 50000 уровней), `generators/gh11028_3`
  (спред генератора с throwing finally: ошибка распаковки должна быть вытеснена
  исключением finally), `anon/011` (protected-свойство алиаса анонимного класса),
  `traits/language013` (static в методе трейта — на каждый использующий класс;
  требует пер-классового ключа без потери перфа глобалов), `traits/bug76773-deprecated`
  (двойной Deprecated), `return_types/029`/`return_reference_separation`
  (цепочка исключения при `return` в finally; возврат по ссылке из динамического
  вызова), `return_types/reflection001` (секция `@@ file lines` в
  ReflectionClass::__toString), `constants/gh18850` (`__COMPILER_HALT_OFFSET__`),
  `zend_ini/oss_fuzz_428983568` (текст ошибки ini-парсера — предсуществующее
  расхождение, отмечено и в мастере).
- `yield_from_deep_recursion` — единственный тест с падением по лимиту стека VM
  (не panic: `errStackOverflow`), остальные — расхождения вывода.

## Технические заметки
- Изменения: `internal/php/{parser,compiler,lexer,builtins_misc,builtins_serialize}.go`,
  `internal/vm/{generator,vm,oop,execObject,execArray,execData,execMethod,class,heap,
  ops,reflection,iterator,spl}.go`. `gofmt` чистый, `-vet=off`, `GOTOOLCHAIN=go1.27.1`.
- Диагностический хелпер: `internal/php/z3_diff_agent_test.go`
  (`Z3_SNIPPET`/`Z3_SRC` — прогон сниппета у нас и живым php; `TestZ3Full` — полные
  got/want/php по списку).
- Коммиты в ветке `z3`: 6750847, 0afcf36, 04a03b0, 5a30eab (не смержены).
