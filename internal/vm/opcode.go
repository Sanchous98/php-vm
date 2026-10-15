package vm

// Opcode — код операции регистровой VM.
//
// Регистровая модель: инструкции адресуют регистры (A, B, C), а не двигают
// значения по операционному стеку. Это устраняет push/pop/Load/Store, которые
// в стековой модели давали заметную долю времени. Стек вызовов (кадры)
// сохраняется — он нужен для функций и рекурсии.
//
// Соглашение об операндах:
//   - A — регистр-назначение (или регистр-условие у переходов);
//   - B — регистр-источник;
//   - C — регистр-источник ИЛИ индекс константы (у *K) ИЛИ адрес перехода,
//     в зависимости от опкода.
//
//go:generate go run gen_opcode_string.go
type Opcode uint8

const (
	OpHalt Opcode = iota // HALT

	// Загрузка значений
	OpLoadK // LOADK
	OpMove  // MOVE
	OpNull  // NULL
	OpTrue  // TRUE
	OpFalse // FALSE

	// Унарные
	OpNeg // NEG
	OpNot // NOT

	// Арифметика (generic, полная семантика PHP)
	OpAdd // ADD
	OpSub // SUB
	OpMul // MUL
	OpDiv // DIV
	OpMod // MOD
	OpPow // POW

	// Арифметика с константой справа (C = индекс константы)
	OpAddK // ADDK
	OpSubK // SUBK
	OpMulK // MULK
	OpDivK // DIVK

	// Строки
	OpConcat // CONCAT

	// Сравнения (A = bool-регистр)
	OpEqual        // EQUAL
	OpNotEqual     // NOT_EQUAL
	OpIdentical    // IDENTICAL
	OpNotIdentical // NOT_IDENTICAL
	OpLess         // LESS
	OpLessOrEqual  // LESS_OR_EQUAL
	OpGreater      // GREATER
	OpGreaterEqual // GREATER_OR_EQUAL

	// Сравнения с константой справа
	OpLessK         // LESSK
	OpLessOrEqualK  // LESS_OR_EQUAL_K
	OpGreaterK      // GREATERK
	OpGreaterEqualK // GREATER_OR_EQUAL_K
	OpEqualK        // EQUAL_K

	// --- SPEC: арифметика для доказанных типов ---
	OpAddInt   // ADD_INT
	OpAddFloat // ADD_FLOAT
	OpSubInt   // SUB_INT
	OpSubFloat // SUB_FLOAT
	OpMulInt   // MUL_INT
	OpMulFloat // MUL_FLOAT
	OpDivInt   // DIV_INT
	OpDivFloat // DIV_FLOAT

	// SPEC арифметика с константой
	OpAddIntK   // ADD_INT_K
	OpAddFloatK // ADD_FLOAT_K
	OpSubIntK   // SUB_INT_K
	OpMulIntK   // MUL_INT_K
	OpModIntK   // MOD_INT_K

	// SPEC сравнения
	OpLessInt           // LESS_INT
	OpLessFloat         // LESS_FLOAT
	OpLessOrEqualInt    // LESS_OR_EQUAL_INT
	OpLessOrEqualFloat  // LESS_OR_EQUAL_FLOAT
	OpGreaterInt        // GREATER_INT
	OpGreaterFloat      // GREATER_FLOAT
	OpGreaterEqualInt   // GREATER_OR_EQUAL_INT
	OpGreaterEqualFloat // GREATER_OR_EQUAL_FLOAT
	OpEqualInt          // EQUAL_INT
	OpEqualFloat        // EQUAL_FLOAT
	OpIdenticalInt      // IDENTICAL_INT
	OpIdenticalFloat    // IDENTICAL_FLOAT

	// SPEC сравнения с константой
	OpLessIntK         // LESS_INT_K
	OpLessFloatK       // LESS_FLOAT_K
	OpLessOrEqualIntK  // LESS_OR_EQUAL_INT_K
	OpGreaterIntK      // GREATER_INT_K
	OpGreaterEqualIntK // GREATER_OR_EQUAL_INT_K
	OpEqualIntK        // EQUAL_INT_K

	// Управление
	OpEcho        // ECHO
	OpJump        // JUMP
	OpJumpIfFalse // JUMP_IF_FALSE
	OpJumpIfTrue  // JUMP_IF_TRUE

	// Вызовы функций
	OpCall // CALL
	OpRet  // RET

	// Массивы
	OpArray    // ARRAY       (A = новый массив)
	OpIndex    // INDEX       (A = B[C])
	OpIndexSet // INDEX_SET   (A[B] = C)
	OpAppend   // APPEND      (A[] = B)
	OpLen      // LEN         (A = count(B))
	OpUnset    // UNSET       (unset A[B])

	// ООП
	OpNew        // NEW         (A = new <classes[C]>(args B..))
	OpPropGet    // PROP_GET    (A = B->const[C])
	OpPropSet    // PROP_SET    (A->const[C] = B)
	OpMethodCall // METHOD_CALL (A = B->const[C](args B+1..))
	OpThis       // THIS        (A = $this)
	OpInstanceOf // INSTANCEOF  (A = B instanceof <classes[C]>)
	OpClassOf    // CLASS_OF    (A = get_class(B))

	// ООП: статика, константы, clone
	OpClone     // CLONE       (A = clone B)
	OpStaticGet // STATIC_GET  (A = <selector B>::const[C])
	OpStaticSet // STATIC_SET  (<selector B>::const[C] = A)
	OpConstGet  // CONST_GET   (A = <selector B>::const[C])

	// Логика
	OpLogicalAnd // LOGICAL_AND (A = (bool)B && (bool)C)

	// Глобальные и статические переменные
	OpGlobalGet   // GLOBAL_GET   (A = global[const[C]])
	OpGlobalSet   // GLOBAL_SET   (global[const[C]] = A)
	OpGlobalIsset // GLOBAL_ISSET (A = isset(global[const[C]]))

	// ООП: статический вызов метода (A = result; B = selector self/static/parent
	// или 3+индекс класса; C = const-имя метода; аргументы в A+1..).
	OpStaticCall // STATIC_CALL

	// Исключения
	OpPushTry      // PUSH_TRY      (C = индекс try-региона в Chunk)
	OpPopTry       // POP_TRY
	OpThrow        // THROW         (A = значение исключения)
	OpEnterFinally // ENTER_FINALLY (C = индекс try-региона)
	OpFinallyEnd   // FINALLY_END   (C = индекс try-региона)

	// Ссылки (&)
	OpNewRef        // NEW_REF         (A = новая Ref{v: B})
	OpRefGet        // REF_GET         (A = deref(B))
	OpRefSet        // REF_SET         (если A — Ref, inner = B; иначе A = B)
	OpRefBind       // REF_BIND        (A = B, alias, если B — Ref)
	OpRefEnsure     // REF_ENSURE      (A: если не Ref, A = Ref{v: A})
	OpGlobalRefGet  // GLOBAL_REF_GET  (A = deref(global[C]))
	OpGlobalRefSet  // GLOBAL_REF_SET  (ячейка global[C] = A)
	OpGlobalRefAddr // GLOBAL_REF_ADDR (A = ячейка global[C], создав Ref)
	OpGlobalRefBind // GLOBAL_REF_BIND (global[C] = A)

	// Приведение типов: A = (CastKind C) B.
	OpCast // CAST

	// Динамические имена: функция/класс берутся из регистра B, аргументы —
	// B+1.. (C = число аргументов), A = результат.
	OpCallDyn // CALL_DYN
	OpNewDyn  // NEW_DYN

	// Замыкания: A = замыкание; B = первый регистр захваченных значений
	// (их количество — funcs[C].NumCaptures); C = индекс функции.
	OpClosure // CLOSURE

	// Массивы в глобальной таблице: append/index-set без чтения-записи слота
	// (иначе каждый append копирует массив — O(n^2)).
	OpGlobalAppend   // GLOBAL_APPEND    (A = значение, C = const-имя)
	OpGlobalIndexSet // GLOBAL_INDEX_SET (A = ключ, B = значение, C = const-имя)

	// OpCallArr — вызов функции с аргументами, упакованными в массив.
	// Нужен для вызовов с числом аргументов, превышающим регистровый файл
	// (иначе индексы регистров и argc переполняются). A = результат,
	// B = регистр с массивом аргументов (значения по порядку), C = индекс функции.
	OpCallArr // CALL_ARR

	// --- Динамические имена (переменные переменных, свойств, методов) ---

	// OpGlobalGetDyn — A = globals["$"+C] (C — регистр со строкой-именем).
	OpGlobalGetDyn // GLOBAL_GET_DYN
	// OpGlobalSetDyn — globals["$"+C] = A (C — регистр со строкой-именем).
	OpGlobalSetDyn // GLOBAL_SET_DYN

	// OpPropGetDyn — A = B->C (C — регистр с именем свойства).
	OpPropGetDyn // PROP_GET_DYN
	// OpPropSetDyn — A->C = B (C — регистр с именем свойства).
	OpPropSetDyn // PROP_SET_DYN

	// OpPropIsset — A = isset(B->const[C]) с учётом __isset.
	OpPropIsset // PROP_ISSET
	// OpPropIssetDyn — A = isset(B->C), C — регистр с именем.
	OpPropIssetDyn // PROP_ISSET_DYN
	// OpPropUnset — unset(A->const[C]) с учётом __unset.
	OpPropUnset // PROP_UNSET
	// OpPropUnsetDyn — unset(A->C), C — регистр с именем.
	OpPropUnsetDyn // PROP_UNSET_DYN

	// OpMethodCallDyn — A = B->C(args B+1..); C = имя-регистр | argc<<16.
	OpMethodCallDyn // METHOD_CALL_DYN

	// OpStaticGetDyn — A = <selector B>::C (C — регистр с именем свойства).
	OpStaticGetDyn // STATIC_GET_DYN
	// OpStaticSetDyn — <selector B>::C = A.
	OpStaticSetDyn // STATIC_SET_DYN
	// OpStaticCallDyn — A = <selector B>::C(args A+1..); C = имя-регистр | argc<<16.
	OpStaticCallDyn // STATIC_CALL_DYN

	// --- Битовые операции ---
	// A = B OP C; семантика PHP 8.3 (int/int либо string/string).
	OpBitAnd // BIT_AND
	OpBitOr  // BIT_OR
	OpBitXor // BIT_XOR
	// OpBitNot — унарное ~ (A = ~B).
	OpBitNot // BIT_NOT
	// Сдвиги: A = B << C / B >> C (только int-домен).
	OpShl // SHL
	OpShr // SHR

	// --- Обход объектов (Iterator/IteratorAggregate) в foreach ---
	// Состояние итерации — обычный PHP-массив (внутренний, только для VM).
	// A/B — регистры.
	OpIsObject    // IS_OBJECT    (A = bool(B — объект))
	OpIterInit    // ITER_INIT    (A = состояние, B = источник)
	OpIterValid   // ITER_VALID   (A = bool, B = состояние)
	OpIterKey     // ITER_KEY     (A = ключ, B = состояние)
	OpIterCurrent // ITER_CURRENT (A = значение, B = состояние)
	OpIterNext    // ITER_NEXT    (B = состояние)
	// OpIndexIsset — A = isset(B[C]) с учётом ArrayAccess::offsetExists.
	OpIndexIsset // INDEX_ISSET (A = bool, B = container/object, C = ключ-регистр)

	// OpIndexEmpty — A = empty(B[C]) (ISSET_ISEMPTY_DIM_OBJ с isempty=1):
	// как OpIndexIsset, но при существующем элементе читает значение и
	// инвертирует его истинность. Для ArrayAccess это ровно один вызов
	// offsetExists и один offsetGet.
	OpIndexEmpty // INDEX_EMPTY (A = bool, B = container/object, C = ключ-регистр)

	// OpSpaceship — оператор `<=>`: A = B <=> C (-1/0/1).
	OpSpaceship // SPACESHIP (A = B <=> C)

	// OpExit — exit/die: печатает A, если это строка, и завершает скрипт.
	OpExit // EXIT

	// OpSuppressOn/OpSuppressOff — подавление ошибок оператором `@` (счётчик).
	OpSuppressOn  // SUPPRESS_ON
	OpSuppressOff // SUPPRESS_OFF
	// OpGlobalUnset — удаляет глобальную переменную (unset делает её
	// неопределённой, а не null).
	OpGlobalUnset // GLOBAL_UNSET

	// OpYield — приостановка генератора. A = регистр значения yield,
	// B = регистр ключа (0xFF — авто-ключ), C = регистр, куда при следующем
	// resume попадёт значение send()/next() (результат yield-выражения).
	// Исполняется только внутри тела функции-генератора: сохраняет кадр в
	// Generator и возвращает из exec сигнал yieldSignal.
	OpYield // YIELD

	// OpJumpIfLessIntK — fusion «LESS_INT_K + JUMP_IF_FALSE»: если B < const[C],
	// то переход на Chunk.jumps[A]. Уменьшает число диспетчеризаций в цикле.
	OpJumpIfLessIntK // JUMP_IF_LESS_INT_K

	// --- Immediate-формы для малых целых констант ---
	//
	// C — само значение (int32), без обращения к пулу констант. Компилятор
	// выбирает *Imm, когда целочисленный литерал влезает в int32; иначе —
	// соответствующую *IntK-форму с пулом. Горячие циклы (`$i<10000000`,
	// `$s+1`) получают константу прямо из инструкции.
	OpAddIntImm          // ADD_INT_IMM
	OpSubIntImm          // SUB_INT_IMM
	OpMulIntImm          // MUL_INT_IMM
	OpModIntImm          // MOD_INT_IMM
	OpLessIntImm         // LESS_INT_IMM
	OpLessOrEqualIntImm  // LESS_OR_EQUAL_INT_IMM
	OpGreaterIntImm      // GREATER_INT_IMM
	OpGreaterEqualIntImm // GREATER_OR_EQUAL_INT_IMM
	OpEqualIntImm        // EQUAL_INT_IMM
	// OpJumpIfLessIntImm — fusion «LESS_INT_IMM + JUMP_IF_FALSE»: если B < C,
	// то переход на Chunk.jumps[A].
	OpJumpIfLessIntImm // JUMP_IF_LESS_INT_IMM

	// OpFatal — фатальная ошибка компиляции, отложенная до рантайма: печатает
	// "Fatal error: <consts[C]> in <file> on line <curLine>" и завершает скрипт.
	// Используется, чтобы сообщения уровня declare-time («must be compatible»,
	// «declared abstract») совпадали с PHP и попадали в вывод.
	OpFatal // FATAL

	// OpPropGetRW — чтение свойства как базы для записи/косвенной модификации
	// (`$x->p[] = ...`, `$x->p->q = ...`, `$x->p =& ...`). В отличие от OpPropGet,
	// для неинициализированного readonly-свойства даёт
	// «Cannot indirectly modify readonly property ...».
	OpPropGetRW    // PROP_GET_RW
	OpPropGetRWDyn // PROP_GET_RW_DYN

	// OpClassName — A = имя класса по селектору B (0 — self, 1 — static/called,
	// 2 — parent, 3+ — класс из таблицы). nil-класс даёт пустую строку.
	OpClassName // CLASS_NAME

	// OpIndexRef — A = ячейка-ссылка элемента B[C]: если элемент уже ссылка,
	// возвращается она; иначе создаётся Ref поверх значения (или null) и
	// сохраняется обратно в массив. Регистр B получает unshare-нутый массив.
	OpIndexRef // INDEX_REF
	// OpPropRef — A = ячейка-ссылка свойства B->const[C] (создаётся при
	// необходимости и сохраняется в объекте).
	OpPropRef // PROP_REF
	// OpPropRefDyn — A = ячейка-ссылка свойства B->C (C — регистр с именем).
	OpPropRefDyn // PROP_REF_DYN
	// OpPropUnrefDyn — снять ссылку со свойства B->C (C — регистр с именем):
	// если свойство хранит Ref, заменить его разыменованным значением.
	OpPropUnrefDyn // PROP_UNREF_DYN
	// OpIterCurrentRef — A = ячейка-ссылка текущего элемента обхода объекта в
	// режиме публичных свойств (для foreach по ссылке). Для итераторов
	// (Iterator/IteratorAggregate) возвращает значение без ссылки.
	OpIterCurrentRef // ITER_CURRENT_REF
	// OpParseFatal — ошибка разбора: печатает "Parse error: ... in <file> on
	// line N" и завершает программу.
	OpParseFatal // PARSE_FATAL

	// OpPropSetRef — присваивание свойства по ссылке (`$o->p =& ...`):
	// A->const[C] = ссылка B (создаётся/сохраняется без разыменования, на
	// типизированное свойство вешается guard).
	OpPropSetRef // PROP_SET_REF
	// OpPropSetRefDyn — то же для динамического имени (C — регистр с именем).
	OpPropSetRefDyn // PROP_SET_REF_DYN
	// OpStaticSetRef — <selector B>::const[C] = ссылка A.
	OpStaticSetRef // STATIC_SET_REF
	// OpStaticSetRefDyn — <selector B>::C = ссылка A (C — регистр с именем).
	OpStaticSetRefDyn // STATIC_SET_REF_DYN
	// OpStaticRef — A = ячейка-ссылка <selector B>::const[C].
	OpStaticRef // STATIC_REF
	// OpStaticRefDyn — A = ячейка-ссылка <selector B>::C (C — регистр с именем).
	OpStaticRefDyn // STATIC_REF_DYN

	// OpNop — пустая операция. Используется оптимизирующим проходом
	// (см. opt.go) как заполнитель удалённых инструкций; SSA-аллокатор не
	// эмитит OpNop в финальный байткод, поэтому реальный размер программы
	// уменьшается уже после AllocateRegisters. Не меняет наблюдаемую
	// семантику.
	OpNop // NOP
	// OpStaticIsset — A = isset(<selector B>::const[C]) с учётом наличия и
	// не-null значения статического свойства (несуществующее → false).
	OpStaticIsset // STATIC_ISSET

	// OpDeclareClass — рантайм-объявление класса/интерфейса/трейта,
	// расположенного вне верхнего уровня (в теле функции/замыкания, под
	// условием и т.п.). C — индекс дескриптора в таблице классов, скрытого до
	// этого объявления. Делает класс видимым по имени, разрешает отложенного
	// родителя/интерфейсы (с автозагрузкой) и строит плоские таблицы. Повторное
	// объявление — фатал «... already in use».
	OpDeclareClass // DECLARE_CLASS

	// OpIndexSetOverload — запись элемента контейнера как косвенная
	// модификация (write-back вложенной размерности / ++--): если контейнер —
	// объект с ArrayAccess::offsetGet, не возвращающим ссылку, печатает
	// "Indirect modification of overloaded element of X has no effect" и
	// ничего не делает. Для массивов ведёт себя как OpIndexSet.
	OpIndexSetOverload // INDEX_SET_OVERLOAD

	// OpIncDec — A = ++/--(B) с PHP-семантикой (числа, строки, переполнение);
	// C = 0 для "++", 1 для "--". B не изменяется. Инлайн-версия без нативного
	// вызова (для горячих циклов `$i++`).
	OpIncDec // INC_DEC

	// --- Запись через переменную-ссылку без ложного COW ---
	//
	// REF_GET/REF_SET помечают массив разделённым (shareSlot) на каждый
	// раундтрип; если использовать их для базы записи (`$a[] = v`,
	// `$a[k] = v`, `unset($a[k])`, `$a[k] op= v`, `$a[k][j] = v`),
	// каждый Unshare в OP_APPEND/OP_INDEX_SET копировал бы весь массив
	// (O(n^2)). Для базы ЗАПИСИ читаем/пишем ячейку «как есть»: значение
	// гарантированно записывается обратно в ту же ячейку, поэтому
	// разделённость выставляют только реальные копирования (OP_MOVE, запись
	// значения в массив/свойство, возврат, передача по значению).

	// OpRefGetRaw — A = deref(B) без пометки массива разделённым.
	// Только для баз записи: результат обязан быть записан обратно в B
	// через OpRefSetRaw (или производную запись того же контейнера).
	OpRefGetRaw // REF_GET_RAW
	// OpRefSetRaw — запись B в ячейку A без пометки массива разделённым
	// (как OpRefSet, но без shareSlot).
	OpRefSetRaw // REF_SET_RAW
	// OpIndexRaw — A = B[C] без пометки элемента разделённым. Только для
	// контейнера-базы записи: результат (или изменённый контейнер) обязан
	// быть записан обратно в тот же элемент через INDEX_SET_OVERLOAD.
	OpIndexRaw // INDEX_RAW
	// OpIndexSetRaw — A[B] = C без пометки записываемого значения разделённым
	// (семантика OpIndexSet, но запись — writeback контейнера-приёмника,
	// который умирает сразу после записи).
	OpIndexSetRaw // INDEX_SET_RAW
	// OpIndexSetOverloadRaw — то же для косвенной модификации
	// (семантика OpIndexSetOverload + без shareSlot значения).
	OpIndexSetOverloadRaw // INDEX_SET_OVERLOAD_RAW

	// OpConcatN — конкатенация N частей одной аллокацией: C = число частей
	// (>= 1), части лежат подряд в регистрах B..B+C-1 (слева направо),
	// результат — в A. Компилятор применяет её для цепочек >= 3 частей,
	// вычисление которых инертно (литералы и простые чтения локальных
	// переменных): у таких частей нет наблюдаемых эффектов, поэтому
	// отложенное приведение к строке даёт ту же последовательность
	// диагностик/__toString, что и вложенные OpConcat.
	OpConcatN // CONCAT_N

	// OpConcatApp — дописывание для `.=` (compound assign конкатенации):
	// A = B . C. Если B — строка-буфер, дописывание идёт на месте (при
	// разделении буфера — COW-копия, при переполнении — геометрический
	// рост), иначе заводится буфер. Порядок приведения к строке и
	// диагностик совпадает с OpConcat.
	OpConcatApp // CONCAT_APP

	// OpIndexSafe — A = B[C] в IS-режиме: отсутствующие ключи и неподходящие
	// типы дают null без диагностик (база цепочек isset()/empty()).
	OpIndexSafe // INDEX_SAFE

	// OpIndexSafeWarn — как OpIndexSafe, но в режиме `??`: ведуще-числовой
	// строковый offset даёт значение с предупреждением (FETCH_DIM_IS).
	OpIndexSafeWarn // INDEX_SAFE_WARN

	// OpIndexSetRes — как OpIndexSet, но регистр значения получает результат
	// присваивания (для строкового offset — записанный символ). Нужна, когда
	// результат присваивания используется как значение выражения.
	OpIndexSetRes // INDEX_SET_RES

	// OpIndexRW — A = B[C] для чтения базы составного присваивания
	// ($a[k] op= v): null/false автовивифицируются в массив, строка и скаляры
	// дают Error (Zend FETCH_DIM_RW).
	OpIndexRW // INDEX_RW

	// OpIndexAppendRaw — A = B[C] в режиме append-базы (`$x[][k] = v`,
	// C — результат count(B)): для классов с запретом append (WeakMap) —
	// Error "Cannot append to <Class>", иначе как OpIndexRaw.
	OpIndexAppendRaw // INDEX_APPEND_RAW
	// OpSweep — точка сбора мусора для детерминированного времени жизни
	// объектов (вызов __destruct при потере последней ссылки, освобождение
	// PHP-хендлов). Эмитится компилятором в конце оператора. Операндов нет;
	// если сборщик не активен (см. lifetime.go), это no-op.
	OpSweep // SWEEP
	// OpStoreVar — запись в регистр именованной переменной (промоушен
	// главного скопа и локальные функции): A = B. Отличие от OpMove в том,
	// что теряемое значение регистра — это ссылка из переменной, поэтому
	// сборщик времени жизни учитывает её как потерю ссылки (lifetime.go).
	OpStoreVar // STORE_VAR
	// OpPropGetRWRead — чтение свойства как базы составного присваивания:
	// как OpPropGetRW, но readonly-свойство не диагностируется (ошибку даёт
	// последующая запись), а неинициализированное типизированное — ошибка
	// доступа (Zend FETCH_OBJ_RW для ASSIGN_OP; variation.phpt).
	OpPropGetRWRead // PROP_GET_RW_READ
	// OpPropGetRWInterm — чтение свойства как ПРОМЕЖУТОЧНОГО звена цепочки
	// записи (`$o->p->q = ...`, `$o->p[] = ...`): в отличие от OpPropGetRW
	// инициализированное readonly-свойство допустимо (модифицируется
	// вложенный объект), неинициализированное — «Cannot indirectly modify
	// readonly property» (variation_nested).
	OpPropGetRWInterm // PROP_GET_RW_INTERM
	// OpPropGetSafe — A = B[C] в IS-режиме (цепочки isset/??): отсутствующее
	// или неинициализированное свойство даёт null без диагностик; магические
	// __isset (true) + __get (Zend FETCH_OBJ_IS).
	OpPropGetSafe // PROP_GET_SAFE
	// OpPropGetSafeDyn — то же для динамического имени (C — регистр-имя).
	OpPropGetSafeDyn // PROP_GET_SAFE_DYN

	// OpListFetchRef — A = ячейка-ссылка элемента B[C] для ссылочной
	// деструктуризации list(&$a, ...) = ...: Zend FETCH_LIST_W + MAKE_REF.
	// Если B — Ref-ячейка, выборка идёт «на месте» (массив разделяется по
	// COW и записывается обратно в ячейку, элемент становится ссылкой).
	// Иначе ссылку взять нельзя (временное значение без ссылки): печатается
	// Notice "Attempting to set reference to non referenceable value" и
	// возвращается КОПИЯ значения (Zend read path is_list=1), поэтому
	// вложенный ссылочный список получит обычное значение и повторит
	// диагностику.
	OpListFetchRef // LIST_FETCH_REF

	// OpPropGetRWArr — чтение свойства как базы ЗАПИСИ МАССИВА
	// (`$o->p[...] = ...`, `$o->p[] = ...`, `$o->p[k] =& ...`, `??=`):
	// как OpPropGetRW, но для типизированного свойства, чей тип не допускает
	// array, при значении null/false/undef выдаёт TypeError «Cannot
	// auto-initialize an array inside property ...» (Zend FETCH_OBJ_W с
	// флагом ZEND_FETCH_DIM_WRITE), не инициализируя свойство.
	OpPropGetRWArr // PROP_GET_RW_ARR
	// OpPropGetRWArrDyn — то же для динамического имени (C — регистр-имя).
	OpPropGetRWArrDyn // PROP_GET_RW_ARR_DYN
	// OpStaticGetRW — чтение статического свойства как базы записи массива:
	// неинициализированное типизированное свойство даёт null (не ошибку),
	// но при типе без array — auto-init TypeError (Zend
	// zend_fetch_static_property_address BP_VAR_W + ZEND_FETCH_DIM_WRITE).
	OpStaticGetRW // STATIC_GET_RW
	// OpStaticGetRWDyn — то же для динамического имени (C — регистр-имя).
	OpStaticGetRWDyn // STATIC_GET_RW_DYN
	// OpPropSetOverload — writeback контейнера `$obj->prop[...] = v`: если
	// свойство получено через __get без ссылки, запись не имеет эффекта
	// (Notice для не-объекта), иначе — обычная запись (как OpPropSet).
	OpPropSetOverload // PROP_SET_OVERLOAD
	// OpPropSetOverloadDyn — то же для динамического имени (C — регистр-имя).
	OpPropSetOverloadDyn // PROP_SET_OVERLOAD_DYN
	// OpIndexUnsetRaw — промежуточное звено цепочки unset ($a[k1][k2][...]):
	// как OpIndexRaw, но контейнер-строка с нечисловым строковым ключом даёт
	// Error "Cannot unset string offsets" (Zend BP_VAR_UNSET при
	// разыменовании промежуточного звена), а не TypeError.
	OpIndexUnsetRaw // INDEX_UNSET_RAW
	// OpPropGetIncDec — база ++/-- на свойстве: как OpPropGet, но не-объект
	// даёт Error "Attempt to increment/decrement property ..." (без warning'а
	// предварительного чтения, как Zend FETCH_OBJ_INC/DEC).
	OpPropGetIncDec // PROP_GET_INC_DEC
	// OpPropGetIncDecDyn — то же для динамического имени (C — регистр-имя).
	OpPropGetIncDecDyn // PROP_GET_INC_DEC_DYN
	// OpPropGetCompound — промежуточное звено цепочки составного присваивания
	// и ++/-- (`X->p->q += v`, `X->p->q++`): как OpPropGetRWInterm, но
	// неинициализированное типизированное свойство даёт ошибку доступа
	// (Zend FETCH_OBJ_RW), а не-объект — Error "Attempt to modify property ...".
	OpPropGetCompound // PROP_GET_COMPOUND
	// OpPropGetCompoundDyn — то же для динамического имени (C — регистр-имя).
	OpPropGetCompoundDyn // PROP_GET_COMPOUND_DYN

	// OpAppendCompound — A[] op= B (C — опкод бинарной операции, см.
	// compoundSlow): для массива Zend просто дописывает правый операнд (как
	// OpAppend), для ArrayAccess — offsetGet(null) op B, затем
	// offsetSet(null, результат). Регистр B получает результат присваивания
	// (bug69955).
	OpAppendCompound // APPEND_COMPOUND

	// OpIndexRawWrite — A = B[C] как база ЗАПИСИ (nested write chain): как
	// OpIndexRaw, но для строкового контейнера Zend не отдаёт символ-временный,
	// а бросает Error "Cannot use string offset as an array" (после диагностик
	// о нелегальном ключе): bug53432, bug70089, offset_assign.
	OpIndexRawWrite // INDEX_RAW_WRITE
)

// numOpcodes — число опкодов (для таблиц, индексируемых опкодом).
const numOpcodes = int(OpIndexRawWrite) + 1

// IsUnconditionalTerminator сообщает, что после инструкции исполнение не
// продолжается со следующей позиции (нет fallthrough): компилятор не ставит
// после такой инструкции точку сбора OpSweep.
func IsUnconditionalTerminator(op Opcode) bool {
	switch op {
	case OpJump, OpRet, OpHalt, OpExit, OpFatal, OpParseFatal, OpThrow:
		return true
	}
	return false
}

// Instruction — регистровая инструкция. A и B — индексы регистров (uint8,
// 256 регистров на функцию), C — регистр/индекс константы/адрес перехода
// (int32, чтобы хватало на большие функции и пул констант).
type Instruction struct {
	Op Opcode
	A  uint8
	B  uint8
	C  int32
}
