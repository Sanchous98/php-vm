package vm

import (
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"unsafe"
)

// maxRegsHard — жёсткий safety-cap на число регистров в одном кадре. Размер
// регистрового файла кадра определяется программой (VM.regWidth), а не этой
// константой. Верхняя граница нужна лишь потому, что A/B-поля инструкций —
// uint8, а также как защита от абсурдных значений.
const maxRegsHard = 256

// maxFrames — глубина стека вызовов.
const maxFrames = 512

// Frame — кадр вызова. Регистры живут ВНУТРИ кадра: при вызове не нужно
// копировать регистровый файл вызывающего, достаточно переключить кадр.
// regs — срез в общий пул VM (см. VM.regPool); длина = ширина, вычисленная по
// программе.
type Frame struct {
	regs []Slot
	pc   int

	// tracked — битовая карта регистров, значения которых учтены в оценках
	// ссылок (writeback, см. lifetime.go): бит ставится при записи в регистр
	// отслеживаемого значения (объект/массив/ссылка/замыкание) и снимается
	// при перезаписи. На выходе из кадра dropFrame списывает ссылки только по
	// выставленным битам. Снимков регистров и периодических сканов нет.
	// nTracked — число выставленных битов (быстрая проверка «пусто/непусто»).
	tracked  [maxRegsHard / 64]uint64
	nTracked int32

	// dynLocals — локальные переменные, созданные динамически в рантайме
	// (extract() с неизвестным на этапе компиляции именем). Ключ — имя без
	// ведущего `$`; dynOrder хранит порядок создания (для get_defined_vars).
	dynLocals map[string]Slot
	dynOrder  []string

	// scope вызова (self/static/this)
	this   Slot
	self   ClassInfo
	called ClassInfo
}

// VM — регистровая виртуальная машина.
type VM struct {
	frames [maxFrames]Frame
	fp     int
	cur    *Frame // = &frames[fp]

	// regWidth — ширина регистрового файла кадра, вычисленная по программе в
	// Init (max NumRegs по главному чанку и всем функциям, с минимумом).
	// regPool — общий пул размером maxFrames*regWidth, нарезанный по кадрам:
	// frames[i].regs = regPool[i*regWidth:(i+1)*regWidth]. Аллокаций на вызов
	// нет. Если при линковке include/eval появляется чанк шире — widenFrame
	// выдаёт конкретному кадру отдельный буфер (редкий путь).
	regWidth int
	regPool  []Slot

	// iterHints — регистры кадров, в которые OpIterInit с C=2 записывал
	// состояние позиционного by-ref обхода (см. iterremap.go): при перестройке
	// массива просматриваются только эти регистры. anyArrayRefIter — быстрый
	// признак «в программе был такой обход»: в программах без foreach by ref
	// перестройка массивов вообще не пересчитывает курсоры. Хинты не
	// сбрасываются при переиспользовании кадра: ложный хинт отсеивается
	// проверкой формы состояния и ячейки.
	iterHints       []iterHint
	anyArrayRefIter bool

	chunk     *Chunk
	functions []*Function
	classes   []ClassInfo
	ip        int
	out       io.Writer

	// funcIndex — ленивый индекс имя→функция для OpCallDyn (устраняет
	// O(N)-скан vm.functions). nil, пока не понадобился.
	funcIndex map[string]*Function
	// propKeyCache — однозаписный кэш ключа хранения private-свойства
	// ("\x00Класс\x00имя"): горячие циклы обращаются к одному и тому же
	// свойству, поэтому сборка/поиск ключа на каждом доступе не нужны.
	propKeyDecl ClassInfo
	propKeyName string
	propKeyVal  string
	// scoped* — однозаписный кэш резолва свойства (см. scopedProp):
	// повторный доступ $this->x в цикле не обходит иерархию классов.
	scopedValid bool
	scopedScope ClassInfo
	scopedObj   ClassInfo
	scopedName  string
	scopedPd    *propDecl
	scopedDecl  ClassInfo
	scopedOk    bool
	// scopedKey/scopedIdx — ключ хранения и индекс слота свойства из
	// последнего резолва (idx = -1, если хранилище не слот).
	scopedKey string
	scopedIdx int
	// unserState — разделяемое состояние активного unserialize() (var_hash):
	// вложенные вызовы (внутри Serializable::unserialize) переиспользуют его.
	unserState any
	// serState — разделяемое состояние активного serialize() (var_hash).
	serState any
	// serializeLock — счётчик "serialize_lock" Zend: пока >0, вложенные
	// serialize()/unserialize() (внутри __sleep/__wakeup/автозагрузки) создают
	// собственный var_hash, а не переиспользуют внешний.
	serializeLock int
	// Диагностика (Warning/Notice/Deprecated): файл/строка текущей операции,
	// уровень error_reporting и счётчик подавления оператором `@`.
	curFile string
	curLine int
	// curFileID — интернированный id vm.curFile; поддерживается в местах смены
	// файла, поэтому горячий путь вызова пишет в кадр лишь int32 (без
	// строкового заголовка и write-barrier). fileNames — таблица имён.
	curFileID    int32
	fileNames    []string
	errReporting int64
	// errReportingIni — значение INI-директивы error_reporting. Отличается от
	// errReporting (EG(error_reporting) в Zend): error_reporting($n) меняет
	// обе, оператор `@` — только errReporting. Файбер стартует со значением
	// INI-директивы (Zend: zend_fiber_execute читает INI_INT), поэтому `@`
	// вызывающего в него не протекает (fibers/silence-operator-outside-fiber).
	errReportingIni int64
	suppress        int
	// suppressStack — сохранённые значения error_reporting для активных
	// операторов `@` (Zend BEGIN_SILENCE/END_SILENCE).
	suppressStack []int64
	// errHandler — пользовательский обработчик ошибок (set_error_handler).
	// Null-слот означает «не установлен». inErrHandler защищает от рекурсии.
	errHandler   Slot
	inErrHandler bool
	// iniLookup — доступ к INI-настройкам (log_errors/error_log/date.timezone)
	// для журналирования диагностик. Ставится хост-пакетом php.
	iniLookup func(name string) (string, bool)
	// exceptionHandler — пользовательский обработчик непойманных исключений
	// (set_exception_handler). inExcHandler защищает от рекурсии.
	exceptionHandler Slot
	inExcHandler     bool
	// noActiveFile — выполняется shutdown-обработчик вне какого-либо
	// компилируемого файла: ошибки локализуются как "[no active file]:0".
	noActiveFile bool
	// headersSent — был ли уже вывод (заголовки отправлены) и где впервые.
	headersSent     bool
	headersSentFile string
	headersSentLine int
	// lastErr* — снимок последней выведенной диагностики для error_get_last().
	lastErrLevel int64
	lastErrMsg   string
	lastErrFile  string
	lastErrLine  int
	lastErrSet   bool

	// fatalDiag — в этом запуске печаталась фатальная диагностика
	// (trigger_error(E_USER_ERROR) и т.п.): PHP завершил бы скрипт, поэтому
	// проверки неявного возврата `never` подавляются.
	fatalDiag bool
	// retImplicit — последний OpRet был неявным (конец функции без return);
	// нужно для сообщения «none returned» при нарушении типа возврата.
	retImplicit bool
	// retLine — строка последнего return (для локации ошибки типа возврата).
	retLine int
	// retReg — регистр последнего OpRet (нужен для освобождения возврата
	// main-скрипта в порядке Zend на завершении запроса, см. releaseMainReturn).
	retReg int

	// callStack — информация о вызовах для построения PHP-подобного stack trace.
	// Хранятся компактные callEntry: callee, файл вызова и место вызова
	// (chunk/PC) для ленивого вычисления строки. Раскрывается в callFrameInfo
	// только при запросе трейса.
	callStack []callEntry

	// suppressNextFrame — не регистрировать кадр одного ближайшего вызова
	// (внутренняя итерация foreach/yield from: методы Iterator не должны
	// появляться в трейсе, но вложенные вызовы внутри них — должны).
	suppressNextFrame bool

	// markNextFrameInternal — пометить один ближайший кадр как
	// "[internal function]" (методы пользовательских потоковых обёрток).
	markNextFrameInternal bool

	// nextFrameSet/File/Chunk/PC/Line — явная позиция вызывающего для
	// следующего кадра (обработчик ошибок set_error_handler: Zend зовёт его
	// из точки ошибки, а не из "[internal function]").
	nextFrameSet   bool
	nextFrameFile  string
	nextFrameChunk *Chunk
	nextFramePC    int
	nextFrameLine  int

	// argNoLoc > 0, когда вызов идёт из внутренней функции через CallCallable
	// (array_map/usort/...). В этом режиме ошибка «Too few arguments» не
	// содержит «in FILE on line N» — как в Zend при вызове callback'а из C.
	argNoLoc int

	// diagLineOverride > 0 — переопределяет строку для следующей диагностики
	// (Zend приписывает неявные преобразования к объявлению функции).
	diagLineOverride int

	// Буферизация вывода (ob_*).
	obStack         []*obBuffer
	obInHandler     bool
	obImplicitFlush bool
	obFile          string

	// consts — пользовательские константы (define()).
	consts map[string]Slot

	// globals — глобальные и статические переменные.
	globals map[string]Slot

	// pendingThis — receiver для ближайшего вызываемого метода (execCompiled
	// перекладывает его в Frame.this). Для обычных функций — нулевой слот.
	pendingThis Slot
	// attrCallFile/attrCallLine — временная подмена места вызова на время
	// newInstance() атрибута (Zend: dummy-кадр в месте объявления атрибута).
	attrCallFile string
	attrCallLine int
	// attrStrictSet/attrStrict — режим strict_types файла-объявления атрибута
	// на время newInstance() (перекрывает текущий чанк; 030_strict_types).
	attrStrictSet bool
	attrStrict    bool
	// requestEndDone — обработчик завершения запроса уже выполнен.
	requestEndDone bool
	// outerSelf/outerCalled — scope ближайшего userland-кадра (для
	// internal-методов: zend_get_executed_scope пропускает internal-кадры).
	outerSelf   ClassInfo
	outerCalled ClassInfo
	// forwardCalled — called scope для текущего forward_static_call*
	// (позднее статическое связывание сохраняет static вызывающего).
	forwardCalled ClassInfo
	// cufCall — выполняется вызов из call_user_func*/forward_static_call*:
	// аргументы-значения by-ref параметров дают Warning "value given".
	cufCall bool
	// namedVariadicTail — именованные аргументы, которые нужно положить в
	// вариативный массив со строковыми ключами (Zend: extra named args).
	namedVariadicTail *Array

	// steps/maxSteps — бюджет инструкций (0 = без лимита). Нужен раннеру, чтобы
	// не зависать на бесконечных циклах в тестах.
	steps    int64
	maxSteps int64

	// retVal — значение последнего return (чтобы не боксить retSignal в error
	// на каждом вызове функции: это давало 1 аллокацию на вызов).
	retVal Slot

	// provisionalClosures — замыкания, созданные встроенными функциями
	// (getClosure и пр.): если к концу оператора ссылок на замыкание не
	// осталось, его хендл возвращается в free-list (Zend освобождает
	// временное замыкание в конце оператора; bug75290/bug69802_2).
	provisionalClosures []*Closure

	// pendingProvidedMask/Set — маска реально переданных позиционных
	// аргументов следующего userland-вызова (именованные аргументы):
	// пропуски заполнены заглушками и не считаются явным null.
	pendingProvidedMask    int64
	pendingProvidedMaskSet bool

	// retVerifiedDepth — глубина callStack, для которой тип возврата уже
	// проверен в точке return (__php_verify_return, bug72347): на выходе из
	// функции повторная проверка пропускается. 0 — проверки не было.
	retVerifiedDepth int

	// resDropPending/resDrops — ресурсы с OnDrop, потерявшие последнего
	// учтённого владельца: финализаторы выполняются в точке OpSweep (аналог
	// детерминированного уничтожения при обнулении refcount в Zend).
	resDropPending bool
	resDrops       []*Resource

	// curFuncArgs — аргументы текущей userland-функции (включая тело
	// генератора на момент resume). Используются func_get_args/func_num_args:
	// нативная функция не видит аргументы вызвавшего её байткода через ctx.args.
	curFuncArgs []Slot
	// pendingFuncArgs — «логические» аргументы ближайшего вызова замыкания:
	// переданные аргументы БЕЗ захваченных `use`-переменных и без усечения до
	// числа объявленных параметров. execCompiled использует их для
	// func_get_args/func_num_args вместо полного регистрового набора.
	pendingFuncArgs []Slot
	// curNamedCount — число аргументов текущего кадра для func_num_args, когда
	// вызов был с именованными аргументами (лишние именованные Zend в счёт не
	// включает); -1 — кадр без именованных. pendingNamedCount — значение,
	// подготовленное BindNamedArgs для ближайшего вызова.
	curNamedCount     int
	pendingNamedCount int
	// pendingFrameNamed — extra named args для кадра ближайшего вызова
	// (pushCall забирает и сбрасывает).
	pendingFrameNamed *Array
	// pendingNativeProvided — сколько аргументов реально передано в вызов
	// известной встроенной функции (позиции + именованные, попавшие в
	// параметры; без NULL-заглушек); -1 — неизвестно. ZPP считает арность
	// именно по этому числу.
	pendingNativeProvided int
	// namedUnpacking — счётчик активных распаковок массива аргументов: ошибка
	// «positional after named» в этом контексте дополняется Zend-суффиксом
	// «during unpacking».
	namedUnpacking int

	// funcParams — число объявленных параметров текущей функции (без
	// захваченных замыканием переменных). funcVariadic/funcNumFixed описывают
	// вариативный хвост. inFunction — исполняется ли сейчас тело userland-
	// функции (нужно, чтобы func_get_args() ругался в глобальном scope).
	funcParams   int
	funcVariadic bool
	funcNumFixed int
	inFunction   bool

	// tries — стек активных try-обработчиков.
	tries []tryEntry

	// included — множество уже подключённых файлов (для include_once и
	// require_once, выполняемых во время работы).
	included map[string]bool
	// includedOrder — пути подключённых файлов в порядке подключения
	// (get_included_files).
	includedOrder []string

	// genClass/genClassIdx — дескриптор и индекс класса Generator в vm.classes
	// (для создания объектов-генераторов). -1, если класс не зарегистрирован.
	genClass    ClassInfo
	genClassIdx int

	// fiberClass/fiberClassIdx — дескриптор и индекс класса Fiber.
	fiberClass    ClassInfo
	fiberClassIdx int
	// currentFiber — файбер, исполняющийся сейчас (nil в главном контексте).
	currentFiber *Fiber
	// forceClosingFiber — файбер, который сейчас принудительно закрывается:
	// переключение файберов запрещено, suspend даёт особую ошибку.
	forceClosingFiber *Fiber
	// noSwitch — переключение файберов запрещено (деструкторы, GC, tick-
	// обработчики): start/resume/throw/suspend дают FiberError.
	noSwitch bool
	// bailoutShutdown — в запросе печаталась фатальная диагностика (bailout
	// Zend): незавершённые файберы не принудительно закрываются, их finally не
	// выполняется (см. runShutdownDestructors).
	bailoutShutdown bool
	// dtorInternal — деструкторы текущего прохода вызываются вне точки кода
	// (сборщик циклов, разрушение стека файбера): кадр __destruct в трейсе
	// печатается как "[internal function]" (Zend: zend_gc_call_destructors,
	// zend_fiber_cleanup), а не с позицией последнего опкода.
	dtorInternal bool
	// fiberStackSize — значение INI fiber.stack_size (если задано). Пустая
	// строка допустима как «задано, но некорректно».
	fiberStackSize    string
	fiberStackSizeSet bool

	// runningGens — стек исполняющихся сейчас генераторов (для проверки
	// `yield from $this-generator` и сообщения "already running").
	runningGens []*Generator

	// forceClosing — генератор, который сейчас принудительно закрывается:
	// yield внутри него запрещён.
	forceClosing *Generator

	// pendingDefaultErr — ошибка вычисления ленивого дефолта свойства при
	// конструировании объекта. OpNew поднимает её как исключение.
	pendingDefaultErr error

	// classAliases — псевдонимы классов, созданные class_alias().
	classAliases map[string]ClassInfo

	// disabledClasses — disable_classes (имена в нижнем регистре): Zend
	// разрешает new, но печатает Warning "X() has been disabled for security
	// reasons" (errmsg_021). Флаг наследуется подклассами.
	disabledClasses map[string]bool

	// autoloaders — зарегистрированные spl_autoload_register callables
	// (в порядке регистрации). autoloading — guard от рекурсии по имени
	// (регистронезависимо).
	autoloaders []Slot
	autoloading map[string]bool

	// magicBusy — защита от рекурсивного вызова магических методов
	// (__get/__set/__isset/__unset) для одного и того же свойства объекта.
	magicBusy map[string]bool

	// wbSkip — стек «отменённых» записей-обратно после дим-записи в
	// перегруженное свойство: Zend читает свойство через __get, модифицирует
	// временную копию и не пишет её назад (fetch_property_address, ветка
	// overloaded). real=true — by-ref __get вернул реальную ячейку и значение
	// нужно записать в само свойство (обход проверки видимости).
	wbSkip []wbSkipEntry

	// liveObjs — объекты в порядке создания (для вызова деструкторов при
	// завершении скрипта); destructed/shutdownDone — защита от повторного вызова.
	liveObjs     []*Object
	destructed   map[*Object]bool
	shutdownDone bool
	// inShutdown — сейчас выполняются shutdown-деструкторы: закрытие
	// генератора/файбера рисует кадр "[internal function]" (bug71604_3).
	inShutdown bool
	// weakRefs — существующие WeakReference по объекту-цели: Zend
	// WeakReference::create на тот же объект возвращает уже созданную
	// ссылку (zend_weakref_find), а не создаёт новую.
	weakRefs map[*Object]*Object
	// haltRequested — вложенный eval/include увидел фатальную ошибку или
	// exit() и просит остановить весь скрипт (см. RequestHalt).
	haltRequested bool
	// noDtor — объекты, созданные при неуспешном unserialize()/брошенном
	// __wakeup(): Zend освобождает их без вызова __destruct.
	noDtor map[*Object]bool

	// shutdownFuncs — функции, зарегистрированные register_shutdown_function.
	shutdownFuncs   []shutdownHandler
	shutdownFnsDone bool
	// headerCallbacks — callbacks header_register_callback: PHP вызывает их в
	// самом конце запроса, после shutdown-функций.
	headerCallbacks []Slot

	// nextObjID — счётчик PHP-хендлов объектов (#N в var_dump).
	nextObjID int

	// --- lifetime.go: детерминированное время жизни объектов ---
	// freeObjIDs — LIFO-список освобождённых PHP-хендлов (Zend берёт id из
	// free-list при следующем создании объекта).
	freeObjIDs []int
	// lifeCandidate — после последнего сбора оценка ссылок какого-то объекта
	// упала до нуля: есть кандидат на проверку достижимости.
	lifeCandidate bool
	// constFetchPath — исходные написания ссылок на константы внутри
	// вычисляемых отложенных константных выражений (диагностика
	// self-referencing constant); thunkEvalDepth — глубина вычисления
	// отложенных выражений (0 вне них).
	constFetchPath []string
	thunkEvalDepth int
	// lifeUrgent — среди кандидатов есть объект, требующий немедленного
	// уничтожения (__destruct, незавершённый генератор/файбер).
	lifeUrgent bool
	// handlesArmed — программа печатала PHP-хендлы (#N): сборщик должен
	// освобождать хендлы сразу, иначе нумерация разойдётся с Zend.
	handlesArmed bool
	// zeroObjs — сколько живых объектов имеют оценку ссылок <= 0 (временные,
	// выброшенные в выражениях, или потерявшие все ссылки): кандидаты на сбор.
	zeroObjs int32
	// sweepBusy — защита от рекурсивного сбора (деструктор сам выполняется
	// под сборщиком).
	sweepBusy bool
	// destroying — глубина выполняющихся __destruct: Zend уничтожает объекты,
	// потерявшие последнюю ссылку ВНУТРИ деструктора, вложенно (порядок
	// вызовов и переиспользование хендлов наблюдаемы; GH-10168).
	destroying int
	// pendingDestroyErr — ошибка вложенного уничтожения: деструктор не может
	// вернуть error через noteDropped, поэтому она поднимается из внешнего
	// destroyObject (сборщик вернёт её в точку OpSweep).
	pendingDestroyErr error
	// inFlightTh — исключение, раскручиваемое прямо сейчас (handleThrow):
	// его объект — корень для сборщика. Zend держит объект в EG(exception),
	// поэтому раскрутка сама по себе не запускает __destruct; у нас регистр
	// THROW-операнда уже мёртв, и без этого корня деструктор сработал бы в
	// момент throw, а не в обработчике (catch_novar_2).
	inFlightTh *Throwable
	// dispTh — «эффективное» исключение последней неуспешной раскрутки:
	// при закрытии генератора сигнал errForceClose заменяется реальным
	// исключением, отложенным во внешних finally (bug71604), и вызывающий
	// (FINALLY_END/closeGenerator) несёт наружу именно его.
	dispTh *Throwable
	// staticRootsCache/staticRootsLen — кэш классов со статиками для сборщика.
	staticRootsCache []*runtimeClass
	staticRootsLen   int
	// gcPendingObjs — циклический мусор, ожидающий gc_collect_cycles (или
	// завершения скрипта): Zend не вызывает их __destruct до сборки циклов.
	gcPendingObjs []*Object
	// cyclicArrs — массивы с прямой само-ссылкой (`$a[] =& $a`): нужны для
	// подсчёта gc_collect_cycles (Zend считает и собранные массивы).
	cyclicArrs map[*Array]bool
	// gcRuns/gcCollected — статистика gc_status(): число проходов сборщика и
	// суммарно собранных узлов (GC_G(gc_runs)/GC_G(collected) в Zend).
	gcRuns      int64
	gcCollected int64
	// gcDtorCount — счётчик фактически выполненных деструкторов: проход
	// сверяет его до/после и проверяет воскрешение узлов (gc_041).
	gcDtorCount int64

	// Отложенные проверки совместимости сигнатур (variance): очередь имён на
	// автозагрузку (порядок вставки), guard разрешения и набор уже
	// обрабатываемых классов.
	delayedAutoloads   []string
	delayedAutoloadSet map[string]bool
	resolvingVariance  map[*runtimeClass]bool
	// unlinkedUses — классы, использованные отложенной проверкой, когда их
	// объявление ещё не было связано (Zend CG(unlinked_uses)). Если такое
	// объявление затем не сможет разрешить родителя, «мягкий» откат
	// невозможен — Zend фаталит «During inheritance of ... with variance
	// dependencies».
	unlinkedUses map[*runtimeClass]bool

	// tickFuncs/tickExecuting — register_tick_function: обработчики
	// declare(ticks=N) и флаг «идёт выполнение тика» (запрещает
	// реентерабельность и удаление во время выполнения). Состояние на VM:
	// файлы/includes одного процесса не делят его.
	tickFuncs     []TickFunc
	tickExecuting bool

	ctx Context
}

// TickFunc — tick-функция с аргументами, переданными в
// register_tick_function (Zend: zend_llist tick_functions).
type TickFunc struct {
	Cb   Slot
	Args []Slot
}

// RegisterTickFunc добавляет tick-функцию (повторная регистрация — no-op).
func (vm *VM) RegisterTickFunc(cb Slot, args []Slot) {
	for _, f := range vm.tickFuncs {
		if f.Cb.Type() == cb.Type() && f.Cb.ToString() == cb.ToString() {
			return
		}
	}
	vm.tickFuncs = append(vm.tickFuncs, TickFunc{Cb: cb, Args: append([]Slot(nil), args...)})
}

// UnregisterTickFunc удаляет tick-функцию. busy=true — функция выполняется
// (удаление запрещено); found — функция вообще была зарегистрирована.
func (vm *VM) UnregisterTickFunc(cb Slot) (found, busy bool) {
	for i, f := range vm.tickFuncs {
		if f.Cb.Type() == cb.Type() && f.Cb.ToString() == cb.ToString() {
			if vm.tickExecuting {
				return true, true
			}
			vm.tickFuncs = append(vm.tickFuncs[:i], vm.tickFuncs[i+1:]...)
			return true, false
		}
	}
	return false, false
}

// TickFuncs возвращает зарегистрированные tick-функции.
func (vm *VM) TickFuncs() []TickFunc { return vm.tickFuncs }

// TicksExecuting сообщает, что сейчас выполняется tick-функция.
func (vm *VM) TicksExecuting() bool { return vm.tickExecuting }

// SetTicksExecuting переключает флаг выполнения tick-функций.
func (vm *VM) SetTicksExecuting(v bool) { vm.tickExecuting = v }

// SetNoSwitch устанавливает запрет переключения файберов и возвращает прежнее
// значение (Zend: zend_fiber_switch_block/unblock вокруг tick-функций,
// сигналов и деструкторов). Переключение внутри tick-обработчика даёт
// FiberError "Cannot switch fibers in current execution context"
// (Zend/tests/fibers/ticks).
func (vm *VM) SetNoSwitch(v bool) bool {
	old := vm.noSwitch
	vm.noSwitch = v
	return old
}

// shutdownHandler — зарегистрированный обработчик завершения скрипта.
type shutdownHandler struct {
	cb   Slot
	args []Slot
}

// tryEntry — активный try-обработчик. Привязан к кадру fp; tiIdx — индекс
// TryInfo в чанке текущего кадра.
type tryEntry struct {
	fp        int
	tiIdx     int
	inCatch   bool       // уже вошли в catch этого try (повторный catch запрещён)
	inFinally bool       // уже выполняем finally (повторный вход запрещён)
	savedErr  *Throwable // исключение для перевозбуждения после finally
}

// SetStepLimit задаёт максимальное число инструкций на один Run (0 — без лимита).
func (vm *VM) SetStepLimit(n int64) { vm.maxSteps = n }

// StepLimit возвращает текущий лимит инструкций (0 — без лимита).
func (vm *VM) StepLimit() int64 { return vm.maxSteps }

// Init сбрасывает состояние под новую программу.
// UnserState возвращает состояние активного unserialize() (var_hash) или nil.
func (vm *VM) UnserState() any { return vm.unserState }

// SetUnserState задаёт/сбрасывает состояние активного unserialize().
func (vm *VM) SetUnserState(s any) { vm.unserState = s }

// SerState возвращает состояние активного serialize() или nil.
func (vm *VM) SerState() any { return vm.serState }

// SetSerState задаёт/сбрасывает состояние активного serialize().
func (vm *VM) SetSerState(s any) { vm.serState = s }

// SerializeLocked сообщает, что удерживается serialize_lock (вложенные
// (де)сериализации внутри __sleep/__wakeup/автозагрузки изолированы).
func (vm *VM) SerializeLocked() bool { return vm.serializeLock > 0 }

// WithSerializeLock выполняет f, удерживая serialize_lock.
func (vm *VM) WithSerializeLock(f func()) {
	vm.serializeLock++
	defer func() { vm.serializeLock-- }()
	f()
}

func (vm *VM) Init(chunk *Chunk, funcs []*Function, classes []ClassInfo) {
	vm.chunk = chunk
	vm.functions = funcs
	vm.classes = classes
	vm.funcIndex = nil
	vm.ip = 0
	vm.fp = 0
	vm.cur = &vm.frames[0]
	vm.out = os.Stdout
	// материализуем константы всех функций один раз
	chunk.materialize()
	chunk.checkConstKinds()
	for _, f := range funcs {
		if cf, ok := f.Executable.(*CompiledFunction); ok {
			cf.chunk.materialize()
			cf.chunk.checkConstKinds()
		}
	}
	for _, c := range classes {
		if rc, ok := c.(*runtimeClass); ok {
			rc.vm = vm
		}
		// Классы, объявляемые в рантайме (OpDeclareClass), линкуются в момент
		// объявления — после этого их родитель/interfaces уже разрешены.
		if !ClassHidden(c) {
			c.link()
		}
		c.materialize()
	}
	vm.ctx.vm = vm
	vm.obStack = vm.obStack[:0]
	vm.obInHandler = false
	vm.obImplicitFlush = false
	if vm.obFile == "" {
		vm.obFile = "php"
	}
	vm.computeRegWidth(chunk, funcs)
	vm.clearRegs(vm.cur)
	vm.tries = vm.tries[:0]
	vm.pendingDefaultErr = nil
	vm.globals = map[string]Slot{}
	vm.consts = map[string]Slot{}
	vm.classAliases = map[string]ClassInfo{}
	vm.autoloaders = nil
	vm.autoloading = nil
	vm.magicBusy = nil
	vm.wbSkip = vm.wbSkip[:0]
	vm.liveObjs = nil
	vm.weakRefs = nil
	vm.haltRequested = false
	vm.destructed = nil
	vm.shutdownDone = false
	vm.bailoutShutdown = false
	vm.requestEndDone = false
	vm.gcRuns = 0
	vm.gcCollected = 0
	vm.gcDtorCount = 0
	vm.freeObjIDs = nil
	vm.lifeCandidate = false
	vm.lifeUrgent = false
	vm.handlesArmed = false
	vm.fatalDiag = false
	vm.zeroObjs = 0
	vm.sweepBusy = false
	vm.destroying = 0
	vm.pendingDestroyErr = nil
	vm.staticRootsCache = nil
	vm.staticRootsLen = 0
	vm.gcPendingObjs = nil
	vm.cyclicArrs = nil
	vm.releaseShutdownRefs()
	vm.shutdownFnsDone = false
	vm.headerCallbacks = nil
	vm.included = nil
	vm.includedOrder = nil
	vm.genClass = nil
	vm.genClassIdx = -1
	vm.fiberClass = nil
	vm.fiberClassIdx = -1
	vm.currentFiber = nil
	vm.forceClosingFiber = nil
	vm.noSwitch = false
	vm.fiberStackSize = ""
	vm.fiberStackSizeSet = false
	vm.tickFuncs = nil
	vm.tickExecuting = false
	if chunk.File != "" {
		vm.setCurFile(chunk.File)
	} else if vm.curFile == "" {
		vm.setCurFile("php")
	}
	vm.RecordIncluded(vm.curFile)
	vm.curLine = 0
	vm.errReporting = E_ALL
	vm.errReportingIni = E_ALL
	vm.suppress = 0
	vm.suppressStack = nil
	vm.argNoLoc = 0
	// Сброс состояния, которое могло остаться после нелокального выхода
	// (exit/die через panic fatalSignal разворачивает стек в обход ручного
	// восстановления в invoke/invokeMethod/execCompiled).
	vm.callStack = vm.callStack[:0]
	vm.ctx.this = Slot{}
	vm.ctx.self = nil
	vm.ctx.called = nil
	vm.pendingThis = Slot{}
	vm.funcParams = 0
	vm.funcVariadic = false
	vm.funcNumFixed = 0
	vm.inFunction = false
	vm.curFuncArgs = nil
	vm.pendingFuncArgs = nil
	vm.curNamedCount = -1
	vm.pendingNamedCount = -1
	vm.pendingNativeProvided = -1
	vm.diagLineOverride = 0
	vm.errHandler = MakeNull()
	vm.inErrHandler = false
	vm.exceptionHandler = MakeNull()
	vm.inExcHandler = false
	vm.lastErrSet = false
	vm.headersSent = false
	vm.nextObjID = 0
	for i, c := range classes {
		if c.Name() == "Generator" {
			vm.genClass = c
			vm.genClassIdx = i
			break
		}
	}
	for i, c := range classes {
		if c.Name() == "Fiber" {
			vm.fiberClass = c
			vm.fiberClassIdx = i
			break
		}
	}
	// Кейсы enum НЕ занимают PHP-хендлы в момент объявления: Zend материализует
	// их лениво, при первом обращении (см. markEnumCaseVal/markAllEnumCases).
}

// MarkIncluded помечает путь как подключённый и сообщает, был ли он уже
// подключён ранее (для include_once/require_once, выполняемых в рантайме).
func (vm *VM) MarkIncluded(path string) bool {
	if vm.included == nil {
		vm.included = map[string]bool{}
	}
	if vm.included[path] {
		return true
	}
	vm.included[path] = true
	vm.includedOrder = append(vm.includedOrder, path)
	return false
}

// RecordIncluded добавляет путь в список подключённых (идемпотентно,
// сохраняя порядок). Для обычного include (без _once).
func (vm *VM) RecordIncluded(path string) {
	if vm.included == nil {
		vm.included = map[string]bool{}
	}
	if !vm.included[path] {
		vm.included[path] = true
		vm.includedOrder = append(vm.includedOrder, path)
	}
}

// CurrentFile возвращает путь текущего исполняемого файла (fallback для
// php_resolve_path при поиске по include_path).
func (vm *VM) CurrentFile() string { return vm.curFile }

// CurrentLine возвращает текущую строку исполняемой инструкции (для метки
// eval()/include при отсутствии явного file/line).
func (vm *VM) CurrentLine() int { return vm.line() }

// IncludedFiles возвращает пути подключённых файлов в порядке подключения.
func (vm *VM) IncludedFiles() []string {
	out := make([]string, len(vm.includedOrder))
	copy(out, vm.includedOrder)
	return out
}

// Output возвращает writer вывода VM (для вложенных запусков, напр. eval).
func (vm *VM) Output() io.Writer { return vm.out }

// DefineConst регистрирует пользовательскую константу (define()).
func (vm *VM) DefineConst(name string, v Slot) {
	if vm.consts == nil {
		vm.consts = map[string]Slot{}
	}
	vm.consts[name] = v
}

// setPendingDefaultError запоминает первую ошибку вычисления ленивого дефолта.
func (vm *VM) setPendingDefaultError(err error) {
	if vm.pendingDefaultErr == nil {
		vm.pendingDefaultErr = err
	}
}

// takePendingDefaultError возвращает и очищает отложенную ошибку дефолта.
func (vm *VM) takePendingDefaultError() error {
	err := vm.pendingDefaultErr
	vm.pendingDefaultErr = nil
	return err
}

// TakePendingDefaultError — экспорт takePendingDefaultError для встроенных
// функций, инстанцирующих класс пользовательской обёртки.
func (vm *VM) TakePendingDefaultError() error { return vm.takePendingDefaultError() }

// Const возвращает пользовательскую константу.
func (vm *VM) Const(name string) (Slot, bool) {
	v, ok := vm.consts[name]
	return v, ok
}

// HasFunction сообщает, есть ли функция с таким именем.
func (vm *VM) HasFunction(name string) bool {
	for _, f := range vm.functions {
		if f.Disabled {
			continue
		}
		if f.Name == name {
			return true
		}
	}
	return false
}

// StrictTypes сообщает, что текущий (вызывающий) файл объявил
// declare(strict_types=1); на время newInstance() атрибута действует режим
// файла-объявления атрибута.
func (vm *VM) StrictTypes() bool {
	if vm.attrStrictSet {
		return vm.attrStrict
	}
	return vm.chunk != nil && vm.chunk.Strict
}

// argStrict возвращает режим strict_types в точке текущей инструкции
// вызывающего кадра. Инлайненные include/eval смешивают в одном чанке код с
// разным strict_types, поэтому берём позиционный флаг (fallback — режим чанка).
func (vm *VM) argStrict() bool {
	if vm.attrStrictSet {
		return vm.attrStrict
	}
	if vm.chunk == nil {
		return false
	}
	if vm.ip <= 0 {
		return vm.chunk.Strict
	}
	return vm.chunk.StrictAt(vm.ip - 1)
}

// GlobalsTouch создаёт запись в таблице глобалов, если её нет (global $x).
func (vm *VM) GlobalsTouch(name string) {
	if vm.globals == nil {
		vm.globals = map[string]Slot{}
	}
	key := "$" + name
	if _, ok := vm.globals[key]; !ok {
		vm.globals[key] = MakeNull()
		vm.syncGlobalsSnapshot(key)
	}
}

// globalsArrayKey — ключ снимка $GLOBALS для имени переменной (без `$`):
// числовые имена (${1}) канонизируются в int, как в хеш-таблице Zend, поэтому
// $GLOBALS[1] и $GLOBALS["1"] видят одну запись.
func globalsArrayKey(name string) ArrayKey {
	if i, ok := canonicalIntKey(name); ok {
		return intKey(i)
	}
	return stringKey(name)
}

// syncGlobalsSnapshot синхронизирует массив-снимок $GLOBALS с таблицей
// глобалов, чтобы `$GLOBALS['x']` отражал реальную глобальную $x.
func (vm *VM) syncGlobalsSnapshot(name string) {
	if name == "$GLOBALS" || len(name) < 2 || name[0] != '$' {
		return
	}
	g := deref(vm.globals["$GLOBALS"])
	if g.typ != ArrayType {
		return
	}
	ga := g.AsArray().Unshare()
	key := globalsArrayKey(name[1:])
	// Если элемент снимка — ссылка (напр. `=& $GLOBALS['x']`), пишем в неё,
	// чтобы алиасы замечали изменение.
	if cur, found := ga.Get(key); found && cur.typ == RefType {
		if r := cur.Ref(); r != nil {
			r.v = deref(vm.globals[name])
			vm.globals["$GLOBALS"] = MakeArray(ga)
			return
		}
	}
	ga.Set(key, deref(vm.globals[name]))
	vm.globals["$GLOBALS"] = MakeArray(ga)
}

func (vm *VM) unsyncGlobalsSnapshot(name string) {
	if name == "$GLOBALS" || len(name) < 2 || name[0] != '$' {
		return
	}
	g := deref(vm.globals["$GLOBALS"])
	if g.typ != ArrayType {
		return
	}
	ga := g.AsArray().Unshare()
	ga.Unset(MakeStrKey(name[1:]))
	vm.globals["$GLOBALS"] = MakeArray(ga)
}

// GlobalsSnapshot собирает массив-снимок $GLOBALS из таблицы глобальных
// переменных на момент вызова.
func (vm *VM) GlobalsSnapshot() Slot {
	arr := newArray()
	for k, v := range vm.globals {
		if k == "$GLOBALS" || len(k) < 2 || k[0] != '$' {
			continue
		}
		arr.Set(globalsArrayKey(k[1:]), deref(v))
	}
	return MakeArray(arr)
}

// GlobalsRefAddr возвращает Ref-ячейку глобальной переменной с заданным
// (не содержащим `$`) именем, создавая её при необходимости.
func (vm *VM) GlobalsRefAddr(key string) Slot {
	if vm.globals == nil {
		vm.globals = map[string]Slot{}
	}
	name := "$" + key
	v, ok := vm.globals[name]
	if !ok {
		v = MakeNull()
	}
	if v.typ != RefType {
		v = MakeRef(&Ref{v: v})
	}
	vm.globals[name] = v
	vm.syncGlobalsSnapshot(name)
	return v
}

// GlobalsSetRef делает глобальную переменную key ссылкой на cell.
func (vm *VM) GlobalsSetRef(key string, cell Slot) {
	if vm.globals == nil {
		vm.globals = map[string]Slot{}
	}
	name := "$" + key
	vm.globals[name] = cell
	vm.syncGlobalsSnapshot(name)
	// Снимок должен хранить саму ячейку, чтобы чтение $GLOBALS[key] давало
	// объект/значение.
	if g := deref(vm.globals["$GLOBALS"]); g.typ == ArrayType {
		ga := g.AsArray().Unshare()
		ga.Set(globalsArrayKey(key), cell)
		vm.globals["$GLOBALS"] = MakeArray(ga)
	}
}

// syncGlobalsFromSnapshot переносит записи снимка $GLOBALS в таблицу глобальных
// переменных (нужно для `$GLOBALS['x'][...] = ...`).
func (vm *VM) syncGlobalsFromSnapshot() {
	g := deref(vm.globals["$GLOBALS"])
	if g.typ != ArrayType {
		return
	}
	for k, val := range g.AsArray().All() {
		name := "$" + k.PHPString()
		if cur, ok := vm.globals[name]; ok && cur.typ == RefType {
			if r := cur.Ref(); r != nil {
				r.v = deref(val)
			}
			continue
		}
		vm.globals[name] = deref(val)
	}
}

// GlobalsUnsetDyn удаляет глобальную переменную по динамическому имени
// (unset($$x) в main-скоупе; имя — как у OpGlobalSetDyn, без `$`). Удаляет
// также элемент снимка $GLOBALS, чтобы удаление было видно через массив.
func (vm *VM) GlobalsUnsetDyn(name string) {
	if vm.globals == nil || name == "" {
		return
	}
	key := "$" + name
	if _, ok := vm.globals[key]; !ok {
		return
	}
	delete(vm.globals, key)
	if g := deref(vm.globals["$GLOBALS"]); g.typ == ArrayType {
		ga := g.AsArray().Unshare()
		ga.Unset(globalsArrayKey(name))
		vm.globals["$GLOBALS"] = MakeArray(ga)
	}
}

// SetSuperglobalSynced записывает суперглобал (например $_SESSION) в таблицу
// глобалов и синхронизирует снимок $GLOBALS.
func (vm *VM) SetSuperglobalSynced(name string, v Slot) {
	if vm.globals == nil {
		vm.globals = map[string]Slot{}
	}
	vm.globals[name] = v
	vm.syncGlobalsSnapshot(name)
}

// GlobalGet возвращает значение глобальной переменной.
func (vm *VM) GlobalGet(name string) (Slot, bool) {
	v, ok := vm.globals[name]
	return v, ok
}

// GlobalRef возвращает «сырой» слот глобальной переменной (может быть
// ссылкой). Второй результат — наличие переменной.
func (vm *VM) GlobalRef(name string) (Slot, bool) {
	v, ok := vm.globals[name]
	return v, ok
}

// GlobalSet устанавливает значение глобальной переменной.
func (vm *VM) GlobalSet(name string, v Slot) {
	if vm.globals == nil {
		vm.globals = map[string]Slot{}
	}
	vm.globals[name] = v
}

// SymtabGet возвращает значение переменной name в текущей области видимости —
// для compact()/get_defined_vars()/extract(). В main-области переменные лежат
// в таблице глобалов; в функции — в регистрах кадра, а объявленные через
// global/static — всё ещё в таблице глобалов. Второй результат — была ли
// переменная определена в текущей области.
func (vm *VM) SymtabGet(name string) (Slot, bool) {
	if vm.chunk != nil {
		if reg, ok := vm.chunk.LocalVarReg(name); ok {
			if reg >= 0 && reg < len(vm.cur.regs) {
				return vm.cur.regs[reg], true
			}
		}
	}
	if vm.inFunction {
		if vm.chunk != nil {
			if key, ok := vm.chunk.GlobalVarKey(name); ok {
				v, ok := vm.globals[key]
				return v, ok
			}
		}
		if vm.cur != nil {
			if v, ok := vm.cur.dynLocals[name]; ok {
				return v, true
			}
		}
		return Slot{}, false
	}
	v, ok := vm.globals["$"+name]
	return v, ok
}

// globalEntry читает запись таблицы глобалов по ключу. Ключ main-переменной
// совпадает с её именем с `$`; для global/static ключ берётся из чанка.
func (vm *VM) globalEntry(key string) (Slot, bool) {
	v, ok := vm.globals[key]
	return v, ok
}

// InDynamicCall сообщает, что текущий нативный вызов идёт не напрямую из
// userland (CallCallable: call_user_func, ob-колбэки, set_error_handler и т.п.).
// compact()/extract()/get_defined_vars() в Zend так вызывать нельзя.
func (vm *VM) InDynamicCall() bool { return vm.argNoLoc > 0 }

// SymtabHas сообщает, определена ли переменная name в текущей области.
func (vm *VM) SymtabHas(name string) bool {
	if vm.chunk != nil {
		if reg, ok := vm.chunk.LocalVarReg(name); ok {
			if reg >= 0 && reg < len(vm.cur.regs) {
				return true
			}
		}
	}
	if vm.inFunction {
		if vm.chunk != nil {
			if key, ok := vm.chunk.GlobalVarKey(name); ok {
				_, ok := vm.globals[key]
				return ok
			}
		}
		if vm.cur != nil {
			_, ok := vm.cur.dynLocals[name]
			return ok
		}
		return false
	}
	_, ok := vm.globals["$"+name]
	return ok
}

// SymtabSet записывает значение переменной name в текущую область видимости
// (extract без EXTR_REFS). Запись «проходит сквозь» ячейку ссылки с проверкой
// guard'а типизированного свойства. Возвращает ошибку типа при нарушении.
func (vm *VM) SymtabSet(name string, v Slot) error {
	if vm.chunk != nil {
		if reg, ok := vm.chunk.LocalVarReg(name); ok {
			if reg >= 0 && reg < len(vm.cur.regs) {
				if cur := vm.cur.regs[reg]; cur.typ == RefType {
					if r := cur.Ref(); r != nil {
						return vm.writeRef(r, deref(v), vm.chunk.Strict)
					}
				}
				{
					old := vm.cur.regs[reg]
					wv := deref(v)
					vm.cur.regs[reg] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(reg), old)
					}
				}
				vm.clearDefFlag(name)
				return nil
			}
		}
	}
	if vm.inFunction {
		if key, ok := vm.chunk.GlobalVarKey(name); ok {
			return vm.globalAssign(key, deref(v))
		}
		// Имя не объявлено на этапе компиляции (extract с динамическим
		// ключом): храним в рантайм-таблице динамических локальных переменных.
		if vm.cur != nil {
			if vm.cur.dynLocals == nil {
				vm.cur.dynLocals = map[string]Slot{}
			}
			if _, exists := vm.cur.dynLocals[name]; !exists {
				vm.cur.dynOrder = append(vm.cur.dynOrder, name)
			}
			vm.cur.dynLocals[name] = deref(v)
		}
		return nil
	}
	return vm.globalAssign("$"+name, deref(v))
}

// clearDefFlag опускает флаг «переменная определена» при записи в рантайме
// (extract()/EXTR_REFS): без этого чтение после extract() давало бы ложное
// «Undefined variable».
func (vm *VM) clearDefFlag(name string) {
	if vm.chunk == nil || vm.cur == nil {
		return
	}
	fr, ok := vm.chunk.DefReg(name)
	if !ok || fr < 0 || fr >= len(vm.cur.regs) {
		return
	}
	vm.cur.regs[fr] = MakeBool(false)
}

// SymtabBindRef привязывает переменную name к Ref-ячейке ref (extract с
// EXTR_REFS): последующие присваивания имени пишут сквозь ячейку, а общая
// ячейка остаётся видимой в исходном массиве.
func (vm *VM) SymtabBindRef(name string, ref Slot) {
	if vm.chunk != nil {
		if reg, ok := vm.chunk.LocalVarReg(name); ok {
			if reg >= 0 && reg < len(vm.cur.regs) {
				{
					old := vm.cur.regs[reg]
					wv := ref
					vm.cur.regs[reg] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(reg), old)
					}
				}
				vm.clearDefFlag(name)
				return
			}
		}
	}
	if vm.inFunction {
		if key, ok := vm.chunk.GlobalVarKey(name); ok {
			if vm.globals == nil {
				vm.globals = map[string]Slot{}
			}
			vm.globals[key] = ref
			vm.syncGlobalsSnapshot(key)
			return
		}
		if vm.cur != nil {
			if vm.cur.dynLocals == nil {
				vm.cur.dynLocals = map[string]Slot{}
			}
			if _, exists := vm.cur.dynLocals[name]; !exists {
				vm.cur.dynOrder = append(vm.cur.dynOrder, name)
			}
			vm.cur.dynLocals[name] = ref
		}
		return
	}
	if vm.globals == nil {
		vm.globals = map[string]Slot{}
	}
	vm.globals["$"+name] = ref
	vm.syncGlobalsSnapshot("$" + name)
}

// CountRefHolders считает, сколько «живых» слотов ссылаются на r: в глобальных
// переменных, именованных локальных регистрах активных кадров и транзитивно в
// массивах/объектах. Используется для разыменования ссылок с refcount==1
// (Zend unwrap reference).
func (vm *VM) CountRefHolders(r *Ref) int {
	return vm.countRefHolders(nil, r, false)
}

// CountRefHoldersWith — как CountRefHolders, но дополнительно обходит extra
// (например, аргументы var_dump, не достижимые из глобалов/кадров).
func (vm *VM) CountRefHoldersWith(extra []Slot, r *Ref) int {
	return vm.countRefHolders(extra, r, false)
}

// CountRefHoldersDisplay — как CountRefHoldersWith, но в текущем кадре
// учитывает только именованные локальные регистры (карта имя→регистр), не
// задевая «повисшие» временные регистры. Используется для выбора `&` в
// var_dump (ссылка с rc==1 печатается без `&`).
func (vm *VM) CountRefHoldersDisplay(extra []Slot, r *Ref) int {
	return vm.countRefHolders(extra, r, true)
}

func (vm *VM) countRefHolders(extra []Slot, r *Ref, namedCurrentOnly bool) int {
	if r == nil {
		return 0
	}
	seenA := map[*Array]bool{}
	seenO := map[*Object]bool{}
	count := 0
	var walk func(s Slot)
	walk = func(s Slot) {
		if s.typ == RefType {
			if s.Ref() == r {
				count++
			}
			if rr := s.Ref(); rr != nil {
				walk(rr.v)
			}
			return
		}
		switch s.typ {
		case ArrayType:
			a := s.AsArray()
			if a == nil || seenA[a] {
				return
			}
			seenA[a] = true
			for _, x := range a.All() {
				walk(x)
			}
		case ObjectType:
			o := (*Object)(s.ptr)
			if o == nil || seenO[o] {
				return
			}
			seenO[o] = true
			// WeakMap: значение записи карты — самостоятельный держатель
			// ссылки (в дампе пары строятся отдельно, но zval записи живёт в
			// карте; Zend в var_dump учитывает его refcount).
			if o.ClassName() == "WeakMap" {
				if d := objectAs[weakMapData](o, o.class); d != nil {
					for i := range d.entries {
						walk(d.entries[i].val)
					}
				}
			}
			for _, de := range vm.ObjectDumpEntries(o) {
				if de.Uninit {
					continue
				}
				walk(de.Val)
			}
		}
	}
	for _, v := range vm.globals {
		walk(v)
	}
	for i := 0; i <= vm.fp && i < len(vm.frames); i++ {
		f := &vm.frames[i]
		if namedCurrentOnly && i == vm.fp {
			var regs map[string]int
			if vm.chunk != nil {
				regs = vm.chunk.NamedRegs()
				if len(regs) == 0 {
					regs = vm.chunk.localVars
				}
			}
			if len(regs) > 0 {
				for _, reg := range regs {
					if reg >= 0 && reg < len(f.regs) {
						walk(f.regs[reg])
					}
				}
				continue
			}
			// Нет карты имён. В main-области все именованные переменные
			// живут в таблице глобалов, временные регистры учитывать не
			// нужно. В функциях карты может не быть — считаем все регистры.
			if !vm.inFunction {
				continue
			}
		}
		for _, v := range f.regs {
			walk(v)
		}
	}
	for _, v := range extra {
		walk(v)
	}
	return count
}

// LocalVarEntry описывает локальную переменную текущего кадра.
type LocalVarEntry struct {
	Name string
	Reg  int
	Val  Slot
}

// DefinedLocalVars возвращает локальные переменные текущего кадра,
// упорядоченные по регистру (порядок объявления в исходнике). Переменные,
// чьи значения ещё не присвоены (слот Null), пропускаются — так ведёт себя
// zend_get_defined_vars() для читаемых регистров. Работает только в функциях,
// где компилятор записал карту имя→регистр (usesSymtab).
func (vm *VM) DefinedLocalVars() []LocalVarEntry {
	if vm.chunk == nil || vm.cur == nil {
		return nil
	}
	lv := vm.chunk.localVars
	if len(lv) == 0 {
		return nil
	}
	out := make([]LocalVarEntry, 0, len(lv))
	for name, reg := range lv {
		if reg < 0 || reg >= len(vm.cur.regs) {
			continue
		}
		v := vm.cur.regs[reg]
		if v.typ == NullType {
			continue
		}
		out = append(out, LocalVarEntry{Name: name, Reg: reg, Val: Deref(v)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Reg < out[j].Reg })
	// Динамические локальные переменные (extract) — в порядке создания.
	for i, name := range vm.cur.dynOrder {
		v, ok := vm.cur.dynLocals[name]
		if !ok || v.typ == NullType {
			continue
		}
		out = append(out, LocalVarEntry{Name: name, Reg: 1<<20 + i, Val: Deref(v)})
	}
	return out
}

// globalAssign пишет значение в таблицу глобалов, «проходя сквозь» ячейку
// ссылки, если переменная уже является ссылкой (как OpGlobalSet): иначе
// extract() разорвал бы существующий alias.
func (vm *VM) globalAssign(key string, v Slot) error {
	if cur, ok := vm.globals[key]; ok && cur.typ == RefType {
		if r := cur.Ref(); r != nil {
			if err := vm.writeRef(r, v, vm.chunk != nil && vm.chunk.Strict); err != nil {
				return err
			}
			vm.syncGlobalsSnapshot(key)
			return nil
		}
	}
	vm.GlobalSet(key, v)
	return nil
}

// SetOutput задаёт приёмник вывода (по умолчанию os.Stdout).
func (vm *VM) SetOutput(w io.Writer) { vm.out = w }

// clearRegs обнуляет регистры кадра и его карту учёта БЕЗ списания ссылок:
// вызывается там, где значения кадра уже перенесены (генератор/файбер) или
// заведомо не учитывались (свежий кадр). Для выхода из кадра — dropFrame.
func (vm *VM) clearRegs(f *Frame) {
	for i := range f.regs {
		f.regs[i] = MakeNull()
	}
	f.tracked = [maxRegsHard / 64]uint64{}
	f.nTracked = 0
}

// computeRegWidth вычисляет ширину регистрового файла кадра по программе
// (максимум по главному чанку и всем функциям, минимум 8) и нарезает общий пул
// по кадрам. Вызывается из Init один раз на программу.
func (vm *VM) computeRegWidth(chunk *Chunk, funcs []*Function) {
	w := 8
	upd := func(n int) {
		if n > w {
			w = n
		}
	}
	if chunk != nil {
		upd(chunk.NumRegs())
	}
	for _, f := range funcs {
		if cf, ok := f.Executable.(*CompiledFunction); ok {
			upd(cf.chunk.NumRegs())
		} else {
			upd(f.NumRegs)
		}
	}
	if w > maxRegsHard {
		w = maxRegsHard
	}
	vm.regWidth = w
	vm.regPool = make([]Slot, maxFrames*w)
	for i := range vm.frames {
		vm.frames[i].regs = vm.regPool[i*w : (i+1)*w]
	}
}

// widenFrame гарантирует, что кадр f имеет не менее need регистров. Обычно
// кадр уже нарезан из общего пула достаточной ширины (need <= regWidth) и
// метод ничего не делает. Отдельный буфер выделяется, только если после
// линковки include/eval появился чанк шире regWidth — то есть редко.
func (vm *VM) widenFrame(f *Frame, need int) {
	if len(f.regs) >= need {
		return
	}
	w := vm.regWidth
	if need > w {
		w = need
	}
	f.regs = make([]Slot, w)
}

// shareSlot помечает массив-значение разделённым при копировании слота (COW).
// Для не-массивов ничего не делает.
func shareSlot(s Slot) {
	if s.typ == ArrayType {
		s.AsArray().MarkShared()
	}
}

// Reg возвращает регистр текущего кадра (для тестов).
func (vm *VM) Reg(i int) Slot { return vm.cur.regs[i] }

// Run исполняет главную программу до HALT.
//
// Исключения PHP не используют panic/recover: они распространяются как error
// (error-протокол, см. Context.Throw / NativeFunction.Call / exec). Единственный
// recover здесь — страховка от НЕОЖИДАННЫХ Go-паник (не исключений): он
// распознаёт служебный fatalSignal (нелокальный выход фатальной ошибки
// буферизации), а всё прочее пробрасывает дальше. Непойманное PHP-исключение
// печатается в vm.out в PHP-совместимом виде и всё равно возвращается как error
// (раннер сам решает, как с ним поступить).
func (vm *VM) Run() (err error) {
	vm.steps = 0
	// Защита от «залипшего» исключения с предыдущего запуска этой же VM.
	vm.ctx.thrown = nil
	// Стартовые диагностики компиляции (устаревшая форма ${var} и т.п.).
	// PHP печатает их до начала выполнения.
	for _, d := range vm.chunk.startup {
		if vm.errReporting&E_DEPRECATED != 0 && vm.suppress == 0 {
			vm.Write("\nDeprecated: " + d + "\n")
		}
	}
	for _, d := range vm.chunk.startupWarn {
		if vm.errReporting&E_WARNING != 0 && vm.suppress == 0 {
			vm.Write("\nWarning: " + d + "\n")
		}
	}
	defer vm.runHeaderCallbacks()
	// Завершение запроса в порядке Zend: shutdown-функции, деструкторы,
	// сброс буферов (d5/d8: ob на момент shutdown-функции ещё активен).
	// Явные вызовы ниже задают порядок; defer'ы — страховка неожиданных
	// выходов (идемпотентны по shutdownFnsDone/shutdownDone).
	defer vm.runShutdownFunctions()
	defer vm.runShutdownDestructors()
	defer func() {
		if r := recover(); r != nil {
			// Единственный recover: только неожиданные Go-паники.
			if _, ok := r.(fatalSignal); ok {
				// Нелокальный выход (фатальная ошибка в обработчике
				// буферизации): буферы не сбрасываем.
				err = nil
				return
			}
			panic(r)
		}
	}()
	err = vm.exec(vm.chunk)
	// Нативные ошибки (деление на ноль и т.п.) нормализуем к PHP-исключению,
	// чтобы вызывающий код видел Throwable (формат вывода — Uncaught ...).
	if th, ok := asThrowable(err); ok && err != error(th) {
		err = th
	}
	if _, ok := err.(retSignal); ok {
		// return на верхнем уровне завершает скрипт штатно. Значение возврата
		// освобождается до call_destructors (Zend: php_execute_script), затем
		// деструкторы и только потом сброс буферов.
		vm.releaseMainReturn()
		vm.runShutdownFunctions()
		vm.runShutdownDestructors()
		vm.runRequestEnd()
		vm.flushOutputBuffers()
		return nil
	}
	if _, ok := err.(exitSignal); ok {
		vm.runShutdownFunctions()
		vm.runShutdownDestructors()
		vm.runRequestEnd()
		vm.flushOutputBuffers()
		return nil
	}
	if err == nil {
		vm.runShutdownFunctions()
		vm.runShutdownDestructors()
		vm.runRequestEnd()
		vm.flushOutputBuffers()
		return nil
	}
	// Непойманная ошибка: временные операнды main освобождаются до печати
	// (Zend раскручивает кадр: foreach-итератор закрывается на месте —
	// bug69740), затем ошибка и штатное завершение запроса.
	if vm.cur != nil && vm.cur == &vm.frames[0] && vm.chunk != nil {
		vm.dropTempRegs(vm.chunk, vm.ip-1)
		if vm.lifeUrgent && !vm.sweepBusy {
			_ = vm.sweepLifetime(false, vm.chunk, vm.ip-1)
		}
	}
	if vm.exceptionHandler.typ != NullType {
		// Обработчик непойманных исключений выполняется до сброса буферов
		// (как в Zend): он может очистить буфер.
		vm.reportUncaught(err)
		vm.flushOutputBuffers()
	} else {
		// Zend пишет сообщение в текущий буфер вывода, и только затем буферы
		// сбрасываются (callback-буферы при этом отбрасываются).
		vm.reportUncaught(err)
		vm.flushOutputBuffers()
	}
	vm.runShutdownFunctions()
	vm.runShutdownDestructors()
	vm.runRequestEnd()
	// shutdown-функции/деструкторы могли открыть собственные буферы.
	vm.flushOutputBuffers()
	return err
}

// RunNested выполняет главный чанк VM как самостоятельный запуск и возвращает
// значение верхнеуровневого return, если оно было. Непойманные исключения НЕ
// печатаются — их обрабатывает вызывающий. Для рантайм-include/eval теперь
// используется RunLinked (общая VM); RunNested оставлен как самостоятельный
// запуск чанка (напр. для встраивания/тестов).
func (vm *VM) RunNested() (res Slot, returned bool, err error) {
	vm.steps = 0
	err = vm.exec(vm.chunk)
	if th, ok := asThrowable(err); ok && err != error(th) {
		err = th
	}
	if _, ok := err.(retSignal); ok {
		return vm.retVal, true, nil
	}
	return Slot{}, false, err
}

// FunctionsTable возвращает текущую таблицу функций VM.
func (vm *VM) FunctionsTable() []*Function { return vm.functions }

// RunLinked выполняет main-чанк подключённого файла в этой же VM, разделяя
// globals и таблицы функций/классов с вызывающим кодом. Перед запуском
// принимает новые таблицы (funcs/classes) и индексы, с которых начинаются
// добавленные линковкой сущности (fnBase/classBase): новые функции и классы
// материализуются/линкуются, как это делает Init для стартовой программы.
//
// Возвращает значение верхнеуровневого return (returned=true), если оно было.
func (vm *VM) RunLinked(chunk *Chunk, funcs []*Function, classes []ClassInfo, fnBase, classBase int) (res Slot, returned bool, err error) {
	if vm.fp+1 >= maxFrames {
		return Slot{}, false, errStackOverflow
	}
	// Принимаем объединённые таблицы. nil-таблицы означают, что линковать
	// нечего (например, чанк-заглушка фатальной ошибки компиляции).
	if funcs != nil {
		vm.functions = funcs
	}
	if classes != nil {
		vm.classes = classes
	}
	vm.funcIndex = nil
	for i := classBase; i < len(classes); i++ {
		c := classes[i]
		if rc, ok := c.(*runtimeClass); ok {
			rc.vm = vm
		}
		if !ClassHidden(c) {
			c.link()
		}
		c.materialize()
	}
	for i := fnBase; i < len(funcs); i++ {
		if cf, ok := funcs[i].Executable.(*CompiledFunction); ok {
			cf.chunk.materialize()
			cf.chunk.checkConstKinds()
		}
	}
	chunk.materialize()
	chunk.checkConstKinds()

	// Запуск в новом кадре (как execCompiled, но без аргументов и с
	// семантикой верхнеуровневого return).
	callerPC := vm.ip
	prevChunk := vm.chunk
	prevFile := vm.curFile
	prevFileID := vm.curFileID
	if chunk.File != "" {
		vm.setCurFile(chunk.File)
	}
	vm.fp++
	vm.cur = &vm.frames[vm.fp]
	vm.cur.dynLocals = nil
	vm.cur.dynOrder = nil
	vm.dropFrameIfTracked(vm.cur)
	// Новые функции подключённого файла могли потребовать больше регистров,
	// чем стартовая программа: при необходимости выдаём кадру свой буфер.
	need := chunk.NumRegs()
	for i := fnBase; i < len(funcs); i++ {
		if cf, ok := funcs[i].Executable.(*CompiledFunction); ok {
			if n := cf.chunk.NumRegs(); n > need {
				need = n
			}
		} else if funcs[i].NumRegs > need {
			need = funcs[i].NumRegs
		}
	}
	vm.widenFrame(vm.cur, need)
	if n := chunk.NumRegs(); n <= len(vm.cur.regs) {
		regs := vm.cur.regs
		if vm.cur.nTracked != 0 {
			for i := 0; i < n; i++ {
				if vm.cur.nTracked != 0 && vm.cur.tracked[i>>6]&(1<<uint(i&63)) != 0 {
					vm.wDropReg(i)
				}
			}
		}
		for i := 0; i < n; i++ {
			regs[i] = MakeNull()
		}
	} else {
		vm.clearRegs(vm.cur)
	}
	vm.cur.this = Slot{}
	vm.ip = 0
	vm.chunk = chunk
	err = vm.exec(chunk)
	if _, ok := err.(retSignal); ok {
		res, returned, err = vm.retVal, true, nil
	}
	vm.dropFrameIfTracked(vm.cur)
	vm.popTriesForFrame(vm.fp)
	vm.fp--
	vm.cur = &vm.frames[vm.fp]
	vm.ip = callerPC
	vm.chunk = prevChunk
	vm.curFile = prevFile
	vm.curFileID = prevFileID
	return res, returned, err
}

// callFrameInfo — раскрытый (resolved) кадр стека для диагностик.
type callFrameInfo struct {
	callee     *Function
	callerFile string
	callerLine int
	objMethod  bool
	args       []Slot
	// named — именованные аргументы, не попавшие в позиции (вариативный
	// хвост userland-функции или extra-named встроенной): в трейсах Zend
	// печатает их как `name: value`, а debug_backtrace — строковыми ключами.
	named *Array
	// this — получатель вызова метода ($this), если он был. Нужен
	// debug_backtrace() для ключа "object".
	this Slot
	// includeFrame — синтетический кадр конструкции include/require
	// (вклеенный на компиляции include): debug_backtrace/print_backtrace
	// печатают его без аргументов (Zend).
	includeFrame bool
}

// objMethodBit — старший бит callEntry.callerPC, помечающий вызов метода
// объекта (в трейсе имя печатается через "->").
const objMethodBit = uint32(1) << 31

// Предопределённые id интернированных имён файлов (индексы 0 и 1 fileNames).
const (
	fileIDInternal int32 = iota // "[internal function]"
	fileIDNoActive              // "[no active file]"
)

// callEntry — компактная запись кадра на стеке вызовов (горячий путь). В
// отличие от callFrameInfo, номер строки НЕ вычисляется при вызове: хранится
// только чанк/PC места вызова, а строка разрешается лениво через LineAt в
// момент, когда трейс реально запрашивается (исключение/debug_backtrace/
// диагностика). Имя файла хранится компактным интернированным id.
type callEntry struct {
	callee *Function
	// callerChunk — чанк места вызова как uintptr, а не *Chunk: запись
	// указателя в горячий кадр влечёт write-barrier. Чанк заведомо жив, пока
	// кадр на стеке (его держат CompiledFunction и Go-стек исполнения), а GC
	// его и так сканирует. Трейс раскрывает чанк через (*Chunk)(unsafe...).
	callerChunk uintptr
	// callerPC — индекс инструкции вызова в callerChunk (vm.ip-1 на момент
	// вызова); старший бит — признак objMethod. callerChunk == 0 означает
	// внутренний кадр без позиции ([internal function]).
	callerPC uint32
	// fallback — vm.curLine на момент вызова: как и vm.line(), используется,
	// если LineAt не даёт строки.
	fallback int32
	// fileID — интернированный id файла вызова (см. internFile).
	fileID int32
	// args НЕ копируем: это под-срез регистров вызывающего кадра, который
	// приостановлен на время вызова и не меняется; запись снимается в popCall.
	args []Slot
	// named — extra named args кадра (см. callFrameInfo.named).
	named *Array
	// this — получатель вызова метода (заполняется только для методов
	// объектов, см. setTopThis); нужен debug_backtrace() ("object").
	this Slot
}

// internFile возвращает стабильный id для имени файла. Вызывается в местах
// смены файла (редко), а не на горячем пути вызова: используем линейный скан
// по небольшой таблице имён, чтобы не аллоцировать map на каждый запуск VM.
func (vm *VM) internFile(name string) int32 {
	if vm.fileNames == nil {
		// Индексы 0 и 1 зарезервированы под служебные имена.
		vm.fileNames = append(vm.fileNames, "[internal function]", "[no active file]")
	}
	for i, s := range vm.fileNames {
		if s == name {
			return int32(i)
		}
	}
	id := int32(len(vm.fileNames))
	vm.fileNames = append(vm.fileNames, name)
	return id
}

// setCurFile фиксирует текущий файл и его интернированный id.
func (vm *VM) setCurFile(name string) {
	vm.curFile = name
	vm.curFileID = vm.internFile(name)
}

// info раскрывает компактную запись в callFrameInfo, вычисляя строку.
func (vm *VM) info(e *callEntry) callFrameInfo { return vm.infoAt(e, -1) }

// infoAt — info с индексом кадра userland-функции: параметры читаются из его
// живых регистров (Zend debug_backtrace показывает текущие значения CV).
func (vm *VM) infoAt(e *callEntry, frame int) callFrameInfo {
	line := int(e.fallback)
	if e.callerChunk != 0 {
		c := (*Chunk)(unsafe.Pointer(e.callerChunk))
		if l := c.LineAt(int(e.callerPC &^ objMethodBit)); l > 0 {
			line = l
		}
	}
	file := ""
	if int(e.fileID) < len(vm.fileNames) {
		file = vm.fileNames[e.fileID]
	}
	args := e.args
	// Живой кадр: подменяем позиционные аргументы текущими значениями
	// параметров (unset($arg) виден в debug_backtrace — bug70547). Кадры с
	// #[SensitiveParameter] оставляем снимком (маскирование делается при
	// вызове).
	if frame >= 0 && frame <= vm.fp && frame < len(vm.frames) && e.callee != nil &&
		e.callee.SensitiveParams == nil && !e.callee.SensitiveVariadic {
		regs := vm.frames[frame].regs
		n := e.callee.NumArgs
		if n < 0 {
			n = len(e.callee.ParamNames)
			if cf, ok := e.callee.Executable.(*CompiledFunction); ok && cf.variadic && n > 0 {
				// Вариативный параметр в кадре лежит упакованным массивом;
				// Zend печатает позиционные «хвосты» отдельными аргументами
				// (снимок e.args), а не массивом (named_params/backtrace).
				n--
			}
		}
		if n > len(args) {
			n = len(args)
		}
		if n > len(regs) {
			n = len(regs)
		}
		if n > 0 {
			live := make([]Slot, len(args))
			copy(live, args)
			for i := 0; i < n; i++ {
				live[i] = deref(regs[i])
			}
			args = live
		}
	}
	return callFrameInfo{
		callee:     e.callee,
		callerFile: file,
		callerLine: line,
		objMethod:  e.callerPC&objMethodBit != 0,
		args:       args,
		named:      e.named,
		this:       e.this,
	}
}

// pushCall регистрирует вызов (перед исполнением callee). Место вызова
// (chunk/PC) сохраняется для ленивого вычисления строки; vm.line() при вызове
// не дёргается.
func (vm *VM) pushCall(fn *Function, args []Slot) {
	if len(vm.callStack) >= maxFrames {
		return
	}
	fileID := vm.curFileID
	var chunk uintptr
	var pc uint32
	fallback := int32(vm.curLine)
	switch {
	case vm.nextFrameSet:
		// Явная позиция (образботчик ошибок): файл/строка точки ошибки.
		// Снимаем и markNextFrameInternal: этот флаг (CallCallable)
		// относится к тому же кадру и не должен «протечь» на следующий.
		vm.nextFrameSet = false
		vm.markNextFrameInternal = false
		fileID = vm.internFile(vm.nextFrameFile)
		if vm.nextFrameChunk != nil && vm.nextFramePC > 0 {
			chunk = uintptr(unsafe.Pointer(vm.nextFrameChunk))
			pc = uint32(vm.nextFramePC)
		}
		fallback = int32(vm.nextFrameLine)
	case vm.markNextFrameInternal:
		vm.markNextFrameInternal = false
		fileID = fileIDInternal
	case len(vm.callStack) > 0 && isNativeCallee(vm.callStack[len(vm.callStack)-1].callee):
		// Вызов из внутренней функции (array_map/usort/Reflection::invoke…):
		// у кадра нет места вызова в userland-коде (Zend: prev_execute_data —
		// внутренний кадр). Сам внутренний кадр при этом локацию имеет.
		fileID = fileIDInternal
	default:
		chunk = uintptr(unsafe.Pointer(vm.chunk))
		if vm.ip > 0 {
			pc = uint32(vm.ip - 1)
		}
		// Вклеенный include/require: инструкция принадлежит файлу include,
		// а не файлу чанка (Zend: код подключаемого файла исполняется в своей
		// позиции исходника).
		if f := vm.chunk.InlineFileAt(int(pc)); f != "" {
			fileID = vm.internFile(f)
		}
	}
	entry := callEntry{
		callee: fn, callerChunk: chunk,
		callerPC: pc, fallback: fallback, fileID: fileID, args: args,
	}
	if vm.pendingFrameNamed != nil {
		entry.named = vm.pendingFrameNamed
		vm.pendingFrameNamed = nil
	}
	vm.callStack = append(vm.callStack, entry)
}

// pushCallAt регистрирует вызов с уже известной позицией вызывающего (нужно
// генераторам: их «место resume» фиксируется до подмены vm.chunk/vm.ip).
// chunk == nil означает внутренний кадр ([internal function]).
func (vm *VM) pushCallAt(fn *Function, file string, chunk *Chunk, pc int, fallbackLine int, args []Slot) {
	if len(vm.callStack) >= maxFrames {
		return
	}
	var p uint32
	var cptr uintptr
	if chunk != nil && pc > 0 {
		p = uint32(pc)
		cptr = uintptr(unsafe.Pointer(chunk))
	}
	vm.callStack = append(vm.callStack, callEntry{
		callee: fn, callerChunk: cptr,
		callerPC: p, fallback: int32(fallbackLine), fileID: vm.internFile(file), args: args,
	})
}

// isNativeCallee сообщает, что кадр — внутренняя (Go) функция.
func isNativeCallee(fn *Function) bool {
	if fn == nil {
		return false
	}
	_, ok := fn.Executable.(*NativeFunction)
	return ok
}

// NativeCallerName возвращает имя ближайшего нативного вызова в стеке (для
// диагностик вида "fwrite(): ..." из вложенного кода stream-фильтров).
// Пустая строка, если нативного кадра нет.
func (vm *VM) NativeCallerName() string {
	for i := len(vm.callStack) - 1; i >= 0; i-- {
		fn := vm.callStack[i].callee
		if fn == nil {
			continue
		}
		if isNativeCallee(fn) {
			return fn.Name
		}
	}
	return ""
}

// markTopObjMethod помечает верхний кадр как вызов метода объекта.
func (vm *VM) markTopObjMethod() {
	if n := len(vm.callStack); n > 0 {
		vm.callStack[n-1].callerPC |= objMethodBit
	}
}

// setTopThis сохраняет получателя в верхнем кадре (для debug_backtrace).
func (vm *VM) setTopThis(recv Slot) {
	if n := len(vm.callStack); n > 0 {
		vm.callStack[n-1].this = recv
	}
}

// MarkNextFrameInternal помечает следующий регистрируемый кадр как
// "[internal function]" (методы пользовательских потоковых обёрток,
// вызываемые из встроенных функций, в Zend отображаются так).
func (vm *VM) MarkNextFrameInternal() { vm.markNextFrameInternal = true }

// PushInternalMethodFrame добавляет синтетический внутренний кадр
// <Class>-><name>(...) в стек вызовов (для трейсов нативных методов, напр.
// ArrayObject->unserialize() при разборе C-формата). Возвращает функцию снятия
// кадра.
func (vm *VM) PushInternalMethodFrame(ci ClassInfo, name string, args []Slot) func() {
	fn := &Function{Name: name, Scope: ci}
	vm.pushCallAt(fn, "[internal function]", nil, 0, 0, args)
	vm.markTopObjMethod()
	return vm.popCall
}

func (vm *VM) popCall() {
	if len(vm.callStack) > 0 {
		vm.callStack = vm.callStack[:len(vm.callStack)-1]
	}
}

// line возвращает номер исходной строки текущей инструкции. Строки хранятся
// в сайд-таблице чанка (Chunk.lines) и читаются лениво — только при
// диагностике/трейсе. vm.chunk указывает на исполняемый сейчас чанк
// (поддерживается в execCompiled/Run), vm.ip — на следующую инструкцию,
// поэтому инструкция-источник — vm.ip-1.
func (vm *VM) line() int {
	if vm.chunk != nil {
		if l := vm.chunk.LineAt(vm.ip - 1); l > 0 {
			return l
		}
	}
	return vm.curLine
}

// curFileAt — файл текущей инструкции с учётом вклеенных include/require:
// инструкции инлайн-региона принадлежат подключаемому файлу, а не чанку
// (Zend печатает в диагностиках файл подключаемого файла).
func (vm *VM) curFileAt() string {
	if vm.chunk != nil && len(vm.chunk.inlineRegions) > 0 {
		if f := vm.chunk.InlineFileAt(vm.ip - 1); f != "" {
			return f
		}
	}
	return vm.curFile
}

// CurrentFuncArgs возвращает аргументы текущей userland-функции (для
// func_get_args/func_num_args).
func (vm *VM) CurrentFuncArgs() []Slot { return vm.curFuncArgs }

// mergeNamedVariadic добавляет именованные аргументы в вариативный массив
// (аргумент len(ParamNames)-1 упакованной функции). Поле namedVariadicTail
// всегда сбрасывается.
func (vm *VM) mergeNamedVariadic(args []Slot, fn *Function) {
	tail := vm.namedVariadicTail
	vm.namedVariadicTail = nil
	if tail == nil || fn == nil || fn.NumArgs >= 0 || len(fn.ParamNames) == 0 || len(args) == 0 {
		return
	}
	fixed := len(fn.ParamNames) - 1
	if fixed < 0 || fixed >= len(args) {
		return
	}
	arrSlot := deref(args[fixed])
	if arrSlot.typ != ArrayType || arrSlot.AsArray() == nil {
		return
	}
	a := arrSlot.AsArray().Unshare()
	for k, v := range tail.All() {
		_ = a.Set(k, v)
	}
	args[fixed] = MakeArray(a)
}

// enterFunc устанавливает контекст текущей userland-функции (для
// func_get_arg/func_get_args/func_num_args) и возвращает функцию
// восстановления предыдущего контекста. args — фактически переданные
// аргументы (до упаковки вариативного хвоста). namedCount — число
// аргументов до лишних именованных (-1, если вызов без них): Zend не
// учитывает extra named args в func_num_args/func_get_args.
// funcEnv — сохранённое окружение текущего вызова (замена замыканию enterFunc:
// восстановление после вызова без аллокации на каждый вызов).
type funcEnv struct {
	params     int
	numFixed   int
	named      int
	variadic   bool
	inFunction bool
}

// enterFuncEnv переключает VM в контекст тела функции и возвращает прежний.
func (vm *VM) enterFuncEnv(cf *CompiledFunction, namedCount int) funcEnv {
	prev := funcEnv{
		params:     vm.funcParams,
		numFixed:   vm.funcNumFixed,
		named:      vm.curNamedCount,
		variadic:   vm.funcVariadic,
		inFunction: vm.inFunction,
	}
	vm.funcParams, vm.funcVariadic, vm.funcNumFixed, vm.inFunction = cf.numParams, cf.variadic, cf.numFixed, true
	vm.curNamedCount = namedCount
	return prev
}

// restoreFuncEnv восстанавливает окружение вызывающего.
func (vm *VM) restoreFuncEnv(prev funcEnv) {
	vm.funcParams, vm.funcVariadic, vm.funcNumFixed, vm.inFunction = prev.params, prev.variadic, prev.numFixed, prev.inFunction
	vm.curNamedCount = prev.named
}

// InFunction сообщает, исполняется ли сейчас тело userland-функции.
func (vm *VM) InFunction() bool { return vm.inFunction }

// FuncArgCount возвращает число фактически переданных аргументов текущей
// userland-функции или -1, если мы не внутри функции.
func (vm *VM) FuncArgCount() int {
	if !vm.inFunction {
		return -1
	}
	if vm.curNamedCount >= 0 {
		return vm.curNamedCount
	}
	if vm.funcVariadic {
		n := vm.funcNumFixed
		if n >= 0 && n < len(vm.cur.regs) && vm.cur.regs[n].typ == ArrayType {
			n += vm.cur.regs[n].AsArray().Len()
		}
		return n
	}
	return len(vm.curFuncArgs)
}

// FuncArg возвращает i-й (0-based) аргумент текущей userland-функции. Для
// объявленных параметров берётся ТЕКУЩЕЕ значение регистра кадра (они могли
// быть изменены в теле функции — так ведёт себя PHP), для лишних переданных
// аргументов — исходный слот вызова.
func (vm *VM) FuncArg(i int) (Slot, bool) {
	if !vm.inFunction || i < 0 {
		return Slot{}, false
	}
	if vm.funcVariadic {
		if i < vm.funcNumFixed {
			if i < len(vm.cur.regs) {
				return deref(vm.cur.regs[i]), true
			}
			return Slot{}, false
		}
		base := vm.funcNumFixed
		if base >= 0 && base < len(vm.cur.regs) && vm.cur.regs[base].typ == ArrayType {
			vals := vm.cur.regs[base].AsArray().Values()
			if j := i - base; j < len(vals) {
				return deref(vals[j]), true
			}
		}
		return Slot{}, false
	}
	if i < vm.funcParams {
		if i < len(vm.cur.regs) {
			return deref(vm.cur.regs[i]), true
		}
		return Slot{}, false
	}
	if i < len(vm.curFuncArgs) {
		return deref(vm.curFuncArgs[i]), true
	}
	return Slot{}, false
}

// captureTrace снимает текущий стек вызовов (innermost-first).
func (vm *VM) captureTrace() []callFrameInfo {
	if vm.currentFiber != nil {
		// Трейс, снятый внутри файбера, должен быть самодостаточным: регистры
		// файбера будут затёрты при переключении контекста.
		return deepCopyFrames(vm.traceFrames())
	}
	return vm.traceFrames()
}

// CaptureExceptionTrace — экспорт captureExceptionTrace для встроенных
// функций, создающих Throwable вручную (assert).
func (vm *VM) CaptureExceptionTrace() []callFrameInfo { return vm.captureExceptionTrace() }

// captureExceptionTrace снимает трейс для исключения с глубокой копией
// кадров. Аргументы сохраняются: корпус php-src прогоняется run-tests.php с
// zend.exception_ignore_args=0 (args показываются в трейсах), поэтому глубокая
// копия нужна — args в callStack ссылаются на переиспользуемые регистры.
func (vm *VM) captureExceptionTrace() []callFrameInfo {
	return deepCopyExceptionFrames(vm.traceFrames())
}

// deepCopyExceptionFrames — копия кадров для трейса исключения: объект-
// получатель (this) не переносится, чтобы трейс не удерживал его живым.
// Zend не addref-ит получателя в трейсе; иначе цикл «генератор → отложенное
// исключение в finally → трейс → генератор» мешает закрытию генератора при
// потере последней настоящей ссылки (bug71604: деструктор генератора обязан
// сработать сразу, а не на shutdown).
func deepCopyExceptionFrames(frames []callFrameInfo) []callFrameInfo {
	out := deepCopyFrames(frames)
	for i := range out {
		out[i].this = Slot{}
	}
	return out
}

// deepCopyFrames глубоко копирует трейс (аргументы кадров — отдельные срезы).
func deepCopyFrames(frames []callFrameInfo) []callFrameInfo {
	n := len(frames)
	if n == 0 {
		return nil
	}
	out := make([]callFrameInfo, n)
	for i, fr := range frames {
		c := fr
		if len(fr.args) > 0 {
			c.args = append([]Slot(nil), fr.args...)
		}
		out[i] = c
	}
	return out
}

// internalTraceHidden сообщает, что служебную функцию-хелпер не следует
// включать в stack trace (в Zend такие операции не являются вызовами).
func internalTraceHidden(name string) bool {
	// __php_assert1 не скрывается: Zend показывает кадр assert(...) в трейсе.
	if name == "__php_assert1" {
		return false
	}
	return strings.HasPrefix(name, "__php_") || name == "__const_expr"
}

// DebugBacktraceArray строит результат debug_backtrace(options, limit):
// кадры от внутреннего к внешнему (без кадра самой debug_backtrace), с
// поддержкой DEBUG_BACKTRACE_IGNORE_ARGS (2) и PROVIDE_OBJECT (1).
func (vm *VM) DebugBacktraceArray(options int64, limit int) Slot {
	all := vm.traceFrames()
	if n := len(all); n > 0 {
		if top := all[n-1]; top.callee != nil && top.callee.Name == "debug_backtrace" {
			all = all[:n-1]
		}
	}
	// Zend не показывает аргументы конструкций include/require в
	// debug_backtrace (getTrace исключения их сохраняет).
	for i := range all {
		if all[i].includeFrame && len(all[i].args) > 0 {
			all[i].args = nil
		}
	}
	return vm.FramesArray(all, options, limit)
}

// FramesArray строит массив кадров в формате Zend (debug_backtrace и
// Throwable::getTrace): ключи в порядке file, line, function[, class[, object]],
// type, args. provideObject включает ключ "object" (только debug_backtrace с
// DEBUG_BACKTRACE_PROVIDE_OBJECT). limit > 0 ограничивает число кадров.
func (vm *VM) FramesArray(frames []callFrameInfo, options int64, limit int) Slot {
	out := NewArray()
	for i := len(frames) - 1; i >= 0; i-- {
		if limit > 0 && out.Len() >= limit {
			break
		}
		fr := frames[i]
		e := NewArray()
		if fr.callerFile != "" && fr.callerFile != "[internal function]" {
			_ = e.SetStr("file", MakeString(fr.callerFile))
			if fr.callerLine > 0 {
				_ = e.SetStr("line", MakeInt(int64(fr.callerLine)))
			}
		}
		if fr.callee != nil {
			name := funcDisplayName(fr.callee)
			cls := ""
			if idx := strings.Index(name, "::"); idx >= 0 {
				cls = name[:idx]
				name = name[idx+2:]
			} else if fr.objMethod {
				if fr.callee.Scope != nil {
					cls = ClassDisplayName(fr.callee.Scope.Name())
				} else if strings.HasPrefix(name, "{closure}") {
					// Замыкание, привязанное к объекту без scope:
					// Zend показывает класс Closure.
					cls = "Closure"
				}
			}
			_ = e.SetStr("function", MakeString(name))
			if cls != "" {
				_ = e.SetStr("class", MakeString(cls))
				if options&1 != 0 && fr.this.typ == ObjectType {
					_ = e.SetStr("object", fr.this)
				}
				typ := "::"
				if fr.objMethod {
					typ = "->"
				}
				_ = e.SetStr("type", MakeString(typ))
			}
		}
		if options&2 == 0 && !(fr.callee != nil && fr.callee.Name == "eval") {
			ea := NewArray()
			sargs, snamed := vm.traceFrameArgsSensitive(fr)
			for _, a := range sargs {
				ea.Append(a)
			}
			if snamed != nil {
				for k, v := range snamed.All() {
					_ = ea.Set(k, v)
				}
			}
			_ = e.SetStr("args", MakeArray(ea))
		}
		out.Append(MakeArray(e))
	}
	return MakeArray(out)
}

// DebugBacktraceString рендерит текущий стек (innermost-first, без текущего
// вызова debug_print_backtrace и без строки {main}) в формате Zend.
func (vm *VM) DebugBacktraceString() string {
	all := vm.traceFrames()
	n := len(all)
	if n <= 1 {
		return ""
	}
	frames := all[:n-1] // без кадра самой debug_print_backtrace
	var b strings.Builder
	for j, i := 0, len(frames)-1; i >= 0; i, j = i-1, j+1 {
		fr := frames[i]
		name := frameCalleeName(fr)
		if fr.includeFrame {
			// require()/include() в debug_print_backtrace печатается без
			// аргументов (Zend).
			fmt.Fprintf(&b, "#%d %s(%d): %s()\n", j, fr.callerFile, fr.callerLine, name)
			continue
		}
		if fr.callerFile == "[internal function]" || fr.callerFile == "" {
			sargs, snamed := vm.traceFrameArgsSensitive(fr)
			fmt.Fprintf(&b, "#%d [internal function]: %s(%s)\n", j, name, vm.traceArgsNamed(sargs, snamed))
			continue
		}
		sargs, snamed := vm.traceFrameArgsSensitive(fr)
		fmt.Fprintf(&b, "#%d %s(%d): %s(%s)\n", j, fr.callerFile, fr.callerLine, name, vm.traceArgsNamed(sargs, snamed))
	}
	return b.String()
}

// traceFrameArgs — аргументы кадра для стек-трейса: у assert() скрывается
// служебный третий аргумент (исходный текст проверки, дописанный компилятором).
func traceFrameArgs(fr callFrameInfo) []Slot {
	if fr.callee != nil && fr.callee.Name == "assert" && len(fr.args) > 2 {
		return fr.args[:2]
	}
	return fr.args
}

// traceFrameArgsSensitive возвращает аргументы кадра для трейса, подменяя
// значения параметров с #[SensitiveParameter] на SensitiveParameterValue
// (Zend: zend_get_args_from_frame). Само исходное значение при этом не
// теряется — обёртка хранит его и отдаёт через getValue().
// Второй результат — extra-named аргументы: для вариативного
// #[SensitiveParameter] параметра они также скрываются (gh20435).
func (vm *VM) traceFrameArgsSensitive(fr callFrameInfo) ([]Slot, *Array) {
	args := traceFrameArgs(fr)
	named := fr.named
	if fr.callee == nil || (fr.callee.SensitiveParams == nil && !fr.callee.SensitiveVariadic) {
		return args, named
	}
	mask := fr.callee.SensitiveParams
	sensitive := func(i int) bool {
		if i < len(mask) && mask[i] {
			return true
		}
		return fr.callee.SensitiveVariadic && len(mask) > 0 && i >= len(mask)-1
	}
	var out []Slot
	for i := range args {
		if !sensitive(i) || isSensitiveValue(args[i]) {
			continue
		}
		if out == nil {
			out = append([]Slot(nil), args...)
		}
		out[i] = vm.makeSensitiveValue(out[i])
	}
	if out == nil {
		out = args
	}
	// Именованные аргументы сверх сигнатуры попадают в вариативный параметр:
	// если он #[SensitiveParameter], их значения скрываются.
	if named != nil && fr.callee.SensitiveVariadic {
		masked := newArray()
		for k, v := range named.All() {
			if !isSensitiveValue(v) {
				v = vm.makeSensitiveValue(v)
			}
			masked.Set(k, v)
		}
		named = masked
	}
	return out, named
}

// traceString рендерит stack trace исключения в формате Zend (начиная с #0,
// заканчивая #N {main}). defFile/defLine — локация по умолчанию для кадров без
// неё. Аргументы кадров печатаются: корпус php-src прогоняется run-tests.php с
// zend.exception_ignore_args=0.
func (vm *VM) traceString(frames []callFrameInfo, defFile string, defLine int) string {
	var b strings.Builder
	if len(frames) == 0 {
		return "#0 {main}"
	}
	for i := len(frames) - 1; i >= 0; i-- {
		fr := frames[i]
		name := frameCalleeName(fr)
		cf := fr.callerFile
		if cf == "[internal function]" || cf == "" {
			sargs, snamed := vm.traceFrameArgsSensitive(fr)
			fmt.Fprintf(&b, "#%d [internal function]: %s(%s)\n", len(frames)-1-i, name, vm.traceArgsNamed(sargs, snamed))
			continue
		}
		if fr.includeFrame {
			// Zend печатает аргумент конструкции include как усечённый
			// маркер: `require('...')`.
			fmt.Fprintf(&b, "#%d %s(%d): %s('...')\n", len(frames)-1-i, cf, fr.callerLine, name)
			continue
		}
		cl := fr.callerLine
		if cl <= 0 {
			cl = defLine
		}
		sargs, snamed := vm.traceFrameArgsSensitive(fr)
		fmt.Fprintf(&b, "#%d %s(%d): %s(%s)\n", len(frames)-1-i, cf, cl, name, vm.traceArgsNamed(sargs, snamed))
	}
	fmt.Fprintf(&b, "#%d {main}", len(frames))
	return b.String()
}

// uncaughtChain возвращает цепочку previous-исключений от самого глубокого
// (первого выброшенного) к th. Для нативных ошибок без объекта-исключения —
// просто [th].
func (vm *VM) uncaughtChain(th *Throwable) []*Throwable {
	list := []*Throwable{th}
	cur := th
	for depth := 0; cur != nil && depth < 100; depth++ {
		if cur.Value.typ != ObjectType {
			break
		}
		o := vm.objectOf(cur.Value)
		if o == nil {
			break
		}
		v, ok := o.GetProp("previous")
		if !ok {
			break
		}
		s := valueSlot(v)
		if s.typ != ObjectType {
			break
		}
		po := vm.objectOf(s)
		if po == nil {
			break
		}
		next := &Throwable{
			Class:     po.ClassName(),
			Message:   exceptionMessage(po),
			Value:     s,
			Trace:     po.excTrace,
			LocFile:   po.excFile,
			LocLine:   po.excLine,
			DefinedIn: po.excDefined,
		}
		list = append(list, next)
		cur = next
	}
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	return list
}

// writeUncaughtChain печатает цепочку непойманных исключений: первое —
// "Fatal error: Uncaught ...", каждое следующее — "\nNext Exception: ...",
// суффикс "  thrown in ..." — у последнего (формат Zend).
func (vm *VM) writeUncaughtChain(chain []*Throwable) {
	for i, e := range chain {
		file := e.LocFile
		if file == "" {
			file = vm.curFile
		}
		if file == "" {
			file = "php"
		}
		line := e.LocLine
		if line <= 0 {
			line = 1
		}
		class := e.Class
		if class == "" {
			class = "Error"
		}
		prefix := "\nFatal error: Uncaught "
		if i > 0 {
			prefix = "\nNext "
		}
		if e.Message != "" {
			vm.Write(prefix + class + ": " + e.Message + " in " + file + ":" + strconv.Itoa(line) + "\n")
		} else {
			vm.Write(prefix + class + " in " + file + ":" + strconv.Itoa(line) + "\n")
		}
		vm.Write("Stack trace:\n")
		vm.Write(vm.traceString(e.Trace, file, line) + "\n")
	}
	last := chain[len(chain)-1]
	file := last.LocFile
	if file == "" {
		file = "Unknown"
	}
	line := last.LocLine
	vm.Write("  thrown in " + file + " on line " + strconv.Itoa(line) + "\n")
}

// reportUncaught печатает непойманное исключение в PHP-подобном формате.
// Прочие (не-PHP) ошибки не печатает.
func (vm *VM) reportUncaught(err error) {
	if err == nil {
		return
	}
	th, ok := asThrowable(err)
	if !ok {
		return
	}
	if !th.Fatal && !vm.inExcHandler && vm.exceptionHandler.typ != NullType {
		obj := vm.throwableObject(th, vm.throwableClass(th))
		if obj.typ == ObjectType {
			vm.inExcHandler = true
			_, herr := vm.CallCallable(&vm.ctx, vm.exceptionHandler, []Slot{obj})
			vm.inExcHandler = false
			if herr == nil {
				return
			}
			if hth, ok2 := asThrowable(herr); ok2 {
				th = hth
			}
		}
	}
	// Цепочка previous (исключения деструкторов/finally): Zend печатает
	// исключения от самого глубокого (первого выброшенного) к текущему,
	// разделяя их "\n\nNext Exception: ..." (gc_030, try_finally_024).
	if !th.Fatal && !th.close {
		if chain := vm.uncaughtChain(th); len(chain) > 1 {
			vm.writeUncaughtChain(chain)
			return
		}
	}
	file := vm.curFileAt()
	if vm.noActiveFile {
		file = "[no active file]"
	}
	if file == "" {
		file = "php"
	}
	if th.Fatal {
		line := vm.curLine
		if th.LocLine > 0 {
			if th.LocFile != "" {
				file = th.LocFile
			}
			line = th.LocLine
		} else if len(th.Trace) == 0 {
			if l := vm.line(); l > 0 {
				line = l
			}
		} else if l := vm.line(); l > 0 {
			// Ошибка пришла из нативного вызова: Zend печатает строку
			// текущей инструкции вызывающего кадра (место вызова).
			line = l
		}
		if vm.noActiveFile {
			line = 0
		}
		if line <= 0 && !vm.noActiveFile {
			line = 1
		}
		vm.Write("\nFatal error: " + th.Message + " in " + file + " on line " + strconv.Itoa(line) + "\n")
		return
	}
	class := th.Class
	if class == "" {
		class = "Error"
	}
	line := vm.curLine
	if th.LocLine > 0 || th.LocOverridden {
		if th.LocFile != "" || th.LocOverridden {
			file = th.LocFile
		}
		line = th.LocLine
	} else if len(th.Trace) == 0 {
		// Ошибка, поднятая в текущем кадре (нет трейса) — строка текущей
		// инструкции; ошибка, поднятая в callee (есть трейс), — «залипшая»
		// строка (напр. return в callee для ошибки типа возврата).
		if l := vm.line(); l > 0 {
			line = l
		}
	}
	if vm.noActiveFile {
		file = "[no active file]"
		line = 0
	}
	if line <= 0 && !vm.noActiveFile && !th.LocOverridden {
		line = 1
	}
	html := false
	if vm.iniLookup != nil {
		if v, ok := vm.iniLookup("html_errors"); ok && iniBool(v) {
			html = true
		}
	}
	// Непойманный ParseError PHP печатает в формате фатальной ошибки разбора
	// ("Parse error: ... in file on line N"), а не как Uncaught-исключение.
	if class == "ParseError" {
		vm.Write("\nParse error: " + th.Message + " in " + file + " on line " + strconv.Itoa(line) + "\n")
		return
	}
	if th.Message != "" {
		if html {
			msg := errHTML(th.Message)
			if th.DefinedIn {
				vm.Write("\n<br />\n<b>Fatal error</b>:  Uncaught " + class + ": " + msg + " and defined in " + file + ":" + strconv.Itoa(line) + "\n")
			} else {
				vm.Write("\n<br />\n<b>Fatal error</b>:  Uncaught " + class + ": " + msg + " in " + file + ":" + strconv.Itoa(line) + "\n")
			}
			vm.Write("Stack trace:\n")
			vm.Write(vm.traceString(th.Trace, file, line) + "\n")
			tailFile := file
			if tailFile == "" {
				tailFile = "Unknown"
			}
			vm.Write("  thrown in <b>" + tailFile + "</b> on line <b>" + strconv.Itoa(line) + "</b><br />\n")
			return
		}
		if th.DefinedIn {
			vm.Write("\nFatal error: Uncaught " + class + ": " + th.Message + " and defined in " + file + ":" + strconv.Itoa(line) + "\n")
		} else {
			vm.Write("\nFatal error: Uncaught " + class + ": " + th.Message + " in " + file + ":" + strconv.Itoa(line) + "\n")
		}
	} else {
		if th.DefinedIn {
			vm.Write("\nFatal error: Uncaught " + class + " and defined in " + file + ":" + strconv.Itoa(line) + "\n")
		} else {
			vm.Write("\nFatal error: Uncaught " + class + " in " + file + ":" + strconv.Itoa(line) + "\n")
		}
	}
	vm.Write("Stack trace:\n")
	vm.Write(vm.traceString(th.Trace, file, line) + "\n")
	// Zend: пустой файл локации печатается как "Unknown".
	tailFile := file
	if tailFile == "" {
		tailFile = "Unknown"
	}
	vm.Write("  thrown in " + tailFile + " on line " + strconv.Itoa(line) + "\n")
}

// reportUncaughtChain печатает непойманное исключение вместе с цепочкой
// previous: Zend (zend_exception_error) печатает голову цепочки как Uncaught,
// каждое previous — как "Next", а хвостовую строку "thrown in" — по последнему
// звену. Голова — исключение, брошенное ПОСЛЕДНИМ (Zend связывает предыдущее
// как previous нового: zend_exception_set_previous).
// Используется завершением запроса, где несколько незавершённых файберов
// бросают из finally: все они образуют одну непойманную цепочку
// (Zend/tests/fibers/throw-in-multiple-destroyed-fibers-after-shutdown).
func (vm *VM) reportUncaughtChain(list []*Throwable) {
	if len(list) == 0 {
		return
	}
	if len(list) == 1 {
		vm.reportUncaught(list[0])
		return
	}
	// Связываем объекты: голова — последнее брошенное, у него previous —
	// предыдущее, и так до первого.
	for i := len(list) - 1; i > 0; i-- {
		vm.chainPrevious(list[i], list[i-1])
	}
	head := list[len(list)-1]
	tail := list[0]
	headFile, headLine := vm.throwableSite(head)
	class := head.Class
	if class == "" {
		class = "Error"
	}
	vm.Write("\nFatal error: Uncaught " + class + ": " + head.Message + " in " + headFile + ":" + strconv.Itoa(headLine) + "\n")
	vm.Write("Stack trace:\n")
	vm.Write(vm.traceString(head.Trace, headFile, headLine) + "\n")
	for i := len(list) - 2; i >= 0; i-- {
		th := list[i]
		c := th.Class
		if c == "" {
			c = "Error"
		}
		file, line := vm.throwableSite(th)
		vm.Write("\nNext " + c + ": " + th.Message + " in " + file + ":" + strconv.Itoa(line) + "\n")
		vm.Write("Stack trace:\n")
		vm.Write(vm.traceString(th.Trace, file, line) + "\n")
	}
	tailFile, tailLine := vm.throwableSite(tail)
	if tailFile == "" {
		tailFile = "Unknown"
	}
	vm.Write("  thrown in " + tailFile + " on line " + strconv.Itoa(tailLine) + "\n")
}

// throwableSite возвращает файл/строку, в которых Zend печатает исключение:
// явная локация Throwable, иначе текущая позиция VM.
func (vm *VM) throwableSite(th *Throwable) (string, int) {
	file := vm.curFile
	if vm.noActiveFile {
		file = "[no active file]"
	}
	if file == "" {
		file = "php"
	}
	line := vm.curLine
	if th.LocLine > 0 || th.LocOverridden {
		if th.LocFile != "" || th.LocOverridden {
			file = th.LocFile
		}
		line = th.LocLine
	} else if len(th.Trace) == 0 {
		if l := vm.line(); l > 0 {
			line = l
		}
	}
	if vm.noActiveFile {
		line = 0
	}
	if line <= 0 && !vm.noActiveFile && !th.LocOverridden {
		line = 1
	}
	return file, line
}

// traceArgs рендерит аргументы кадра стека в формате Zend (напр. `foo(123)`).
// traceFloat рендерит float для стек-трейса: PHP сохраняет ".0" у целых
// значений (в отличие от echo/var_dump).
func traceFloat(f float64) string {
	s := formatFloat(f)
	if !strings.ContainsAny(s, ".EN") {
		s += ".0"
	}
	return s
}

func (vm *VM) traceArgs(args []Slot) string {
	return vm.traceArgsNamed(args, nil)
}

// traceArgsNamed рендерит аргументы кадра вместе с extra named args:
// позиционные как есть, именованные — `name: value` (Zend; пустое имя даёт
// `: value`, напр. `array_multisort(: 1)`).
func (vm *VM) traceArgsNamed(args []Slot, named *Array) string {
	parts := make([]string, 0, len(args)+4)
	for _, a := range args {
		parts = append(parts, vm.traceArg(a))
	}
	if named != nil {
		for k, v := range named.All() {
			name := ""
			if k.IsStr() {
				name = k.StrVal()
			}
			parts = append(parts, name+": "+vm.traceArg(v))
		}
	}
	return strings.Join(parts, ", ")
}

func (vm *VM) traceArg(s Slot) string {
	s = deref(s)
	switch s.typ {
	case NullType:
		return "NULL"
	case BoolType:
		if s.ToBool() {
			return "true"
		}
		return "false"
	case IntType:
		return strconv.FormatInt(s.ToInt(), 10)
	case FloatType:
		return traceFloat(s.ToFloat())
	case StringType:
		raw := s.StringVal()
		if len(raw) > 15 {
			raw = raw[:15] + "..."
		}
		return "'" + escapeTraceString(raw) + "'"
	case ArrayType:
		return "Array"
	case ObjectType:
		if ci := vm.classInfo(s); ci != nil {
			return "Object(" + ClassDisplayName(ci.Name()) + ")"
		}
		return "Object"
	case ClosureType:
		return "Object(Closure)"
	}
	return "NULL"
}

// escapeTraceString экранирует строку для показа аргумента в stack trace
// (как Zend: \n \t \xNN и т.п.).
func escapeTraceString(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\v':
			b.WriteString(`\v`)
		case '\f':
			b.WriteString(`\f`)
		case '\\':
			b.WriteString(`\\`)
		case '\'':
			b.WriteString(`\'`)
		default:
			if c < 0x20 || c == 0x7f {
				b.WriteString(`\x`)
				const hexd = "0123456789abcdef"
				b.WriteByte(hexd[c>>4])
				b.WriteByte(hexd[c&0xf])
			} else {
				b.WriteByte(c)
			}
		}
	}
	return b.String()
}

// MatchErrorMessage формирует сообщение UnhandledMatchError по значению
// subject, как это делает PHP: скалярные значения показываются с учётом типа,
// массивы/объекты — как «of type X», длинные строки усекаются.
func MatchErrorMessage(vm *VM, s Slot) string {
	// zend.exception_ignore_args=1: значения скрываются — печатается только
	// тип subject (Zend 8.3).
	if vm.iniLookup != nil {
		if v, ok := vm.iniLookup("zend.exception_ignore_args"); ok && iniBool(v) {
			return "Unhandled match case of type " + operandTypeName(s)
		}
	}
	switch s.Type() {
	case NullType:
		return "Unhandled match case NULL"
	case BoolType:
		if s.BoolVal() {
			return "Unhandled match case true"
		}
		return "Unhandled match case false"
	case IntType:
		return "Unhandled match case " + s.ToString()
	case FloatType:
		return "Unhandled match case " + matchFloat(s.Float())
	case StringType:
		return "Unhandled match case '" + matchStringParam(s.StringVal()) + "'"
	default:
		return "Unhandled match case of type " + operandTypeName(s)
	}
}

// matchFloat рендерит float для сообщения об ошибке match в формате PHP
// (всегда с точкой, верхний E без ведущих нулей в экспоненте).
func matchFloat(f float64) string {
	s := strconv.FormatFloat(f, 'G', 14, 64)
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mant := s[:i]
		exp := s[i+1:]
		sign := ""
		if len(exp) > 0 && (exp[0] == '+' || exp[0] == '-') {
			sign = string(exp[0])
			exp = exp[1:]
		}
		exp = strings.TrimLeft(exp, "0")
		if exp == "" {
			exp = "0"
		}
		if !strings.Contains(mant, ".") {
			mant += ".0"
		}
		return mant + "E" + sign + exp
	}
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

// SpreadValueByRef — параметры ссылочной распаковки аргументов: элементы,
// попадающие в by-ref позиции вызываемой функции, становятся ссылками в
// массиве-источнике (Zend SEND_UNPACK).
type SpreadValueByRef struct {
	Start      int      // позиция первого элемента распаковки среди аргументов
	Mask       uint64   // бит j — позиция Start+j должна стать ссылкой
	VarByRef   bool     // вариативный хвост принимает по ссылке
	Name       string   // имя функции для диагностики Traversable
	ParamNames []string // имена параметров (для строковых ключей-имён)
	ParamByRef []bool   // by-ref ли параметр с этим именем
}

// byRefAt сообщает, должна ли позиция i (абсолютный номер параметра) стать
// ссылкой: бит j маски — by-ref параметр с номером j; за 64-й позицией
// решает вариативный хвост.
func (b *SpreadValueByRef) byRefAt(i int) bool {
	if b == nil || i < 0 {
		return false
	}
	if i >= 64 {
		return b.VarByRef
	}
	return b.Mask&(1<<uint(i)) != 0
}

// byRefName сообщает, попадает ли именованный аргумент name в by-ref
// параметр; argNum — его 1-based номер (для диагностик).
func (b *SpreadValueByRef) byRefName(name string) (bool, int) {
	if b == nil {
		return false, 0
	}
	for i, p := range b.ParamNames {
		if p == name {
			if i < len(b.ParamByRef) {
				return b.ParamByRef[i], i + 1
			}
			return false, i + 1
		}
	}
	if b.VarByRef {
		return true, 0
	}
	return false, 0
}

// SpreadValuesRef — как SpreadValues, но для аргументов вызова с by-ref
// параметрами: элементы by-ref позиций становятся ссылками в массиве-
// источнике (src — Ref-ячейка; изменённый массив записывается обратно), а для
// Traversable печатается Warning и значение оборачивается в ссылку-копию.
func (vm *VM) SpreadValuesRef(dst Slot, src Slot, argMode bool, byref *SpreadValueByRef) (Slot, error) {
	if byref == nil {
		return vm.SpreadValues(dst, src, argMode)
	}
	res := NewArray()
	if d := deref(dst); d.typ == ArrayType {
		if byref.Start < 0 {
			// Начало распаковки неизвестно на этапе компиляции (перед ней была
			// другая распаковка): позиция = число позиционных аргументов.
			n := 0
			for k := range d.AsArray().All() {
				if !k.IsStr() {
					n++
				}
			}
			byref.Start = n
		}
		for k, v := range d.AsArray().All() {
			res.Set(k, v)
		}
	} else if byref.Start < 0 {
		byref.Start = 0
	}
	pos := byref.Start
	addVal := func(k ArrayKey, v Slot) error {
		if k.IsStr() {
			return res.Set(k, deref(v))
		}
		_, err := res.Append(deref(v))
		return err
	}
	s := deref(src)
	switch s.typ {
	case ArrayType:
		cell := src.Ref()
		a := s.AsArray()
		byRefWanted := byref.Mask != 0 || byref.VarByRef
		if !byRefWanted {
			for k := range a.All() {
				if k.IsStr() {
					if ok, _ := byref.byRefName(k.StrVal()); ok {
						byRefWanted = true
						break
					}
				}
			}
		}
		if byRefWanted {
			a = a.Unshare()
		}
		changed := a != s.AsArray()
		idx := pos
		for k, v := range a.All() {
			byrefSlot := false
			if k.IsStr() {
				byrefSlot, _ = byref.byRefName(k.StrVal())
			} else {
				byrefSlot = byref.byRefAt(idx)
				idx++
			}
			if byrefSlot {
				if v.typ == RefType {
					if k.IsStr() {
						if err := res.Set(k, v); err != nil {
							return Slot{}, err
						}
					} else if _, err := res.Append(v); err != nil {
						return Slot{}, err
					}
					continue
				}
				shareSlot(v)
				ref := MakeRef(&Ref{v: v})
				a.Set(k, ref)
				changed = true
				if k.IsStr() {
					if err := res.Set(k, ref); err != nil {
						return Slot{}, err
					}
				} else if _, err := res.Append(ref); err != nil {
					return Slot{}, err
				}
				continue
			}
			if err := addVal(k, v); err != nil {
				return Slot{}, err
			}
		}
		if changed && cell != nil {
			cell.Set(MakeArray(a))
		}
		return MakeArray(res), nil
	case ObjectType:
		ci := vm.classInfo(s)
		trav := vm.findClass("Traversable")
		if ci == nil || trav == nil || !ci.isA(trav) {
			return Slot{}, NewThrowable("Error", "Only arrays and Traversables can be unpacked")
		}
		st, err := vm.iterInit(s)
		if err != nil {
			return Slot{}, err
		}
		idx := pos
		for {
			ok, err := vm.iterValid(st)
			if err != nil {
				return Slot{}, err
			}
			if !ok {
				break
			}
			key, err := vm.iterKey(st)
			if err != nil {
				return Slot{}, err
			}
			val, err := vm.iterCurrent(st)
			if err != nil {
				return Slot{}, err
			}
			kt := key.Type()
			if kt != IntType && kt != StringType {
				return Slot{}, NewThrowable("Error", "Keys must be of type int|string during argument unpacking")
			}
			ak, ok := vm.KeyOfWarn(key)
			if !ok {
				return Slot{}, NewThrowable("Error", "Keys must be of type int|string during argument unpacking")
			}
			byrefSlot := false
			argNum := 0
			if ak.IsStr() {
				byrefSlot, argNum = byref.byRefName(ak.StrVal())
			} else {
				byrefSlot = byref.byRefAt(idx)
				argNum = idx + 1
				idx++
			}
			if byrefSlot {
				if argNum > 0 {
					vm.Warning("Cannot pass by-reference argument " + intToString(int64(argNum)) + " of " + byref.Name + "() by unpacking a Traversable, passing by-value instead")
				}
				shareSlot(val)
				ref := MakeRef(&Ref{v: deref(val)})
				if ak.IsStr() {
					if err := res.Set(ak, ref); err != nil {
						return Slot{}, err
					}
				} else if _, err := res.Append(ref); err != nil {
					return Slot{}, err
				}
				if err := vm.iterNext(st); err != nil {
					return Slot{}, err
				}
				continue
			}
			if err := addVal(ak, val); err != nil {
				return Slot{}, err
			}
			if err := vm.iterNext(st); err != nil {
				return Slot{}, err
			}
		}
		return MakeArray(res), nil
	}
	if argMode {
		return Slot{}, NewThrowable("Error", "Only arrays and Traversables can be unpacked")
	}
	return Slot{}, NewThrowable("Error", "Only arrays and Traversables can be unpacked")
}

// SpreadValues возвращает новый массив: копия dst плюс элементы src (массив
// или Traversable), распакованные по правилам PHP: строковые ключи
// перезаписывают, целые — перенумеровываются. argMode меняет текст ошибки для
// распаковки в аргументы вызова.
func (vm *VM) SpreadValues(dst Slot, src Slot, argMode bool) (Slot, error) {
	res := NewArray()
	if d := deref(dst); d.typ == ArrayType {
		for k, v := range d.AsArray().All() {
			res.Set(k, v)
		}
	}
	keyErr := func() error {
		if argMode {
			return NewThrowable("Error", "Keys must be of type int|string during argument unpacking")
		}
		return NewThrowable("Error", "Keys must be of type int|string during array unpacking")
	}
	addPair := func(k ArrayKey, v Slot) error {
		if k.IsStr() {
			return res.Set(k, v)
		}
		_, err := res.Append(v)
		return err
	}
	s := deref(src)
	switch s.typ {
	case ArrayType:
		// Распаковка копирует значения: ссылочные элементы разыменовываются
		// (Zend: zend_array_dup → значения без is_ref).
		for k, v := range s.AsArray().All() {
			if err := addPair(k, deref(v)); err != nil {
				return Slot{}, err
			}
		}
		return MakeArray(res), nil
	case ObjectType:
		ci := vm.classInfo(s)
		trav := vm.findClass("Traversable")
		if ci == nil || trav == nil || !ci.isA(trav) {
			return Slot{}, NewThrowable("Error", "Only arrays and Traversables can be unpacked")
		}
		st, err := vm.iterInit(s)
		if err != nil {
			return Slot{}, err
		}
		for {
			ok, err := vm.iterValid(st)
			if err != nil {
				return Slot{}, err
			}
			if !ok {
				break
			}
			key, err := vm.iterKey(st)
			if err != nil {
				return Slot{}, err
			}
			val, err := vm.iterCurrent(st)
			if err != nil {
				return Slot{}, err
			}
			if kt := key.Type(); kt != IntType && kt != StringType {
				return Slot{}, keyErr()
			}
			ak, ok := vm.KeyOfWarn(key)
			if !ok {
				return Slot{}, keyErr()
			}
			if err := addPair(ak, val); err != nil {
				return Slot{}, err
			}
			if err := vm.iterNext(st); err != nil {
				return Slot{}, err
			}
		}
		return MakeArray(res), nil
	}
	return Slot{}, NewThrowable("Error", "Only arrays and Traversables can be unpacked")
}

// NewInstanceByName создаёт объект класса по имени и вызывает конструктор.
// Используется встроенными функциями, которым нужно инстанциировать
// пользовательский класс (напр. обёртки потока stream_wrapper_register).
func (vm *VM) NewInstanceByName(name string, args []Slot) (Slot, error) {
	ci := vm.findClass(name)
	if ci == nil {
		return Slot{}, NewThrowable("Error", "Class \""+name+"\" not found")
	}
	return vm.NewInstanceArgs(vm.classIndex(ci), args)
}

// NewInstanceByNameFromInternal создаёт объект класса по имени из внутреннего
// (нативного) кода: кадр конструктора помечается как "[internal function]", и
// ошибка арности не содержит локацию — как при вызове из C в Zend.
// Используется потоковыми обёртками/фильтрами.
func (vm *VM) NewInstanceByNameFromInternal(name string, args []Slot) (Slot, error) {
	vm.argNoLoc++
	vm.MarkNextFrameInternal()
	defer func() { vm.argNoLoc-- }()
	return vm.NewInstanceByName(name, args)
}

// NewInstanceArgs создаёт объект класса classIdx и вызывает конструктор с
// аргументами args (для распаковки `new Foo(...$args)`).
func (vm *VM) NewInstanceArgs(classIdx int, args []Slot) (Slot, error) {
	if classIdx < 0 || classIdx >= len(vm.classes) {
		return Slot{}, NewThrowable("Error", "Class not found")
	}
	ci := vm.classes[classIdx]
	if ci == nil {
		return Slot{}, NewThrowable("Error", "Cannot instantiate class")
	}
	if ci.IsInterface() || ci.IsTrait() {
		return Slot{}, cannotInstantiateErr(ci)
	}
	if ci.IsAbstract() {
		return Slot{}, NewThrowable("Error", "Cannot instantiate abstract class "+ci.Name())
	}
	obj := ci.New(&vm.ctx, nil)
	if obj == nil {
		return Slot{}, cannotInstantiateErr(ci)
	}
	slot := ci.objectSlot(obj, classIdx)
	if ctor := ci.lookupCtor(); ctor != nil {
		vm.MarkNoDestruct(obj)
		if _, err := vm.invokeMethod(ctor, slot, ci, args); err != nil {
			// Конструктор бросил исключение: объект не считается построенным,
			// __destruct не вызывается (Zend: ctor_failure).
			return Slot{}, err
		}
		vm.UnmarkNoDestruct(obj)
	}
	return slot, nil
}

// memberCallTypeName — имя типа для сообщения "Call to a member function ...
// on X": для bool показывается true/false.
func memberCallTypeName(s Slot) string {
	if s.Type() == BoolType {
		if s.BoolVal() {
			return "true"
		}
		return "false"
	}
	return operandTypeName(s)
}

// matchStringParam рендерит строку для сообщения об ошибке match: усечение
// сырых байтов до 15 и экранирование управляющих символов.
func matchStringParam(s string) string {
	const maxLen = 15
	truncated := false
	if len(s) > maxLen {
		s = s[:maxLen]
		truncated = true
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(c)
		}
	}
	if truncated {
		b.WriteString("...")
	}
	return b.String()
}

// exec — цикл исполнения чанка.
func (vm *VM) exec(chunk *Chunk) error {
	code := chunk.code
	consts := chunk.consts
	regs := vm.cur.regs
	// Ленивая инициализация side-table inline-кэшей (один раз на чанк).
	if len(chunk.ic) != len(code) {
		chunk.ic = make([]icEntry, len(code))
	}
	for {
		// Error-протокол: исключение могло быть выставлено напрямую из
		// VM-внутреннего кода без panic (буферизация вывода, обработчик
		// ошибок). Превращаем его в error и ищем catch/finally.
		if t := vm.ctx.thrown; t != nil {
			vm.ctx.thrown = nil
			if !vm.handleThrow(chunk, t) {
				return t
			}
		}
		if vm.maxSteps != 0 {
			vm.steps++
			if vm.steps > vm.maxSteps {
				return errStepLimit
			}
		}
		in := code[vm.ip]
		vm.ip++
		// Горячие опкоды (арифметика, сравнения, прыжки, пересылки,
		// выход/фаталы, try/throw/finally) обрабатываются прямо здесь,
		// чтобы не платить за вызов на каждой инструкции. Остальные
		// группы вынесены в exec*.go (диспетчеризация во вложенном switch).
		switch in.Op {
		case OpHalt:
			return nil

		case OpNop:
			// пустая операция (заполнитель удалённых оптимизатором инструкций)

		case OpSuppressOn:
			vm.SuppressOn()

		case OpSuppressOff:
			vm.SuppressOff()

		case OpExit:
			if s := deref(regs[in.A]); s.typ == StringType {
				vm.Write(s.StringVal())
			}
			return exitSignal{}

		case OpFatal:
			msg := consts[in.C].StringVal()
			file := vm.curFileAt()
			if file == "" {
				file = "php"
			}
			line := vm.line()
			if line <= 0 {
				line = 1
			}
			fmt.Fprintf(vm.out, "\nFatal error: %s in %s on line %d\n", msg, file, line)
			return exitSignal{}

		case OpParseFatal:
			file := vm.curFileAt()
			if file == "" {
				file = "php"
			}
			line := vm.line()
			if line <= 0 {
				line = 1
			}
			fmt.Fprintf(vm.out, "\nParse error: %s in %s on line %d\n", consts[in.C].StringVal(), file, line)
			return exitSignal{}

		case OpIncDec:
			r := &regs[in.B]
			dec := in.C&1 != 0
			switch r.typ {
			case IntType:
				// Результат — скаляр: достаточно списать старое значение
				// приёмника, если оно учитывалось (при A==B это сам операнд,
				// а он int — учитывать нечего).
				if in.A != in.B && trackedType(regs[in.A].typ) {
					vm.wDropReg(int(in.A))
				}
				v := int64(r.num)
				if dec {
					if v == math.MinInt64 {
						regs[in.A] = MakeFloat(float64(math.MinInt64))
					} else {
						regs[in.A] = MakeInt(v - 1)
					}
				} else {
					if v == math.MaxInt64 {
						regs[in.A] = MakeFloat(float64(math.MaxInt64))
					} else {
						regs[in.A] = MakeInt(v + 1)
					}
				}
			case FloatType:
				if in.A != in.B && trackedType(regs[in.A].typ) {
					vm.wDropReg(int(in.A))
				}
				f := math.Float64frombits(r.num)
				if dec {
					regs[in.A] = MakeFloat(f - 1)
				} else {
					regs[in.A] = MakeFloat(f + 1)
				}
			default:
				old := regs[in.A]
				nv, ierr := vm.IncDecValue(*r, dec)
				if ierr != nil {
					if !vm.handleThrow(chunk, ierr) {
						return ierr
					}
					continue
				}
				regs[in.A] = nv
				if trackedType(old.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}

		case OpLoadK:
			{
				old := regs[in.A]
				wv := consts[in.C]
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		case OpMove:
			old := regs[in.A]
			wv := regs[in.B]
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
			if regs[in.A].typ == ArrayType {
				regs[in.A].AsArray().MarkShared()
			}
		case OpStoreVar:
			old := regs[in.A]
			wv := regs[in.B]
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
			if regs[in.A].typ == ArrayType {
				regs[in.A].AsArray().MarkShared()
			}
		case OpNull:
			{
				old := regs[in.A]
				wv := MakeNull()
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		case OpSweep:
			// Точка запуска сборщика: кандидаты появляются при записи
			// (writeback), поэтому полного сканирования регистров здесь нет.
			// Списываются только учтённые значения мёртвых регистров; при
			// нулевой карте учёта (типичный случай чистой арифметики) — пара
			// сравнений.
			if vm.sweepBusy {
				break
			}
			if f := vm.cur; f.nTracked != 0 {
				// Быстрая проверка: есть ли учтённый регистр, который в этой
				// точке мёртв по SSA и не является именованной переменной
				// (только такие списываются). Частый случай — узкий чанк
				// (numRegs <= 64): одно слово без вызова.
				pc := vm.ip - 1
				off := pc * chunk.liveStride
				if chunk.numRegs > 0 && chunk.numRegs <= 64 && off >= 0 &&
					off+liveMaskStride <= len(chunk.liveMasks) {
					if f.tracked[0]&^(chunk.liveMasks[off]|chunk.varRegs[0]) != 0 {
						vm.dropDeadRegs(chunk, pc)
					}
				} else if vm.deadTrackedWide(chunk, pc) {
					vm.dropDeadRegs(chunk, pc)
				}
			}
			if len(vm.provisionalClosures) > 0 {
				vm.releaseProvisionalClosures()
			}
			if (vm.lifeCandidate && (vm.lifeUrgent || vm.handlesArmed)) ||
				(vm.handlesArmed && vm.zeroObjs > 0) {
				if vm.lifetimeAllowed() {
					for round := 0; round < 8; round++ {
						run := (vm.lifeCandidate && (vm.lifeUrgent || vm.handlesArmed)) ||
							(vm.handlesArmed && vm.zeroObjs > 0)
						if !run {
							break
						}
						res := vm.lifetimeSweep(false, chunk, vm.ip-1)
						if res.err != nil {
							if !vm.handleThrow(chunk, res.err) {
								return res.err
							}
							break
						}
						if !vm.lifeUrgent {
							break
						}
					}
				}
			}
		case OpTrue:
			{
				old := regs[in.A]
				wv := MakeBool(true)
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		case OpFalse:
			{
				old := regs[in.A]
				wv := MakeBool(false)
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}

		case OpNeg:
			r, err := vm.Neg(regs[in.B])
			if err != nil {
				if !vm.handleThrow(chunk, err) {
					return err
				}
				continue
			}
			{
				old := regs[in.A]
				wv := r
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		case OpNot:
			{
				old := regs[in.A]
				wv := vm.notSlow(regs[in.B])
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}

		case OpCast:
			if CastKind(in.C) == CastString && deref(regs[in.B]).typ == ObjectType {
				str, serr := vm.stringify(regs[in.B])
				if serr != nil {
					th, ok := asThrowable(serr)
					if !ok {
						th = NewThrowable("Error", serr.Error())
					}
					if !vm.handleThrow(chunk, th) {
						return th
					}
					continue
				}
				{
					old := regs[in.A]
					wv := MakeString(str)
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
				break
			}
			{
				old := regs[in.A]
				wv := castSlot(vm, regs[in.B], int(in.C))
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}

		case OpAdd:
			r, err := vm.addSlow(regs[in.B], regs[in.C])
			if err != nil {
				if !vm.handleThrow(chunk, err) {
					return err
				}
				continue
			}
			{
				old := regs[in.A]
				wv := r
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		case OpSub:
			r, err := vm.subSlow(regs[in.B], regs[in.C])
			if err != nil {
				if !vm.handleThrow(chunk, err) {
					return err
				}
				continue
			}
			{
				old := regs[in.A]
				wv := r
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		case OpMul:
			r, err := vm.mulSlow(regs[in.B], regs[in.C])
			if err != nil {
				if !vm.handleThrow(chunk, err) {
					return err
				}
				continue
			}
			{
				old := regs[in.A]
				wv := r
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		case OpDiv:
			r, err := vm.divSlow(regs[in.B], regs[in.C])
			if err != nil {
				if !vm.handleThrow(chunk, err) {
					return err
				}
				continue
			}
			{
				old := regs[in.A]
				wv := r
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		case OpMod:
			r, err := vm.modSlow(regs[in.B], regs[in.C])
			if err != nil {
				if !vm.handleThrow(chunk, err) {
					return err
				}
				continue
			}
			{
				old := regs[in.A]
				wv := r
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		case OpPow:
			r, err := vm.powSlow(regs[in.B], regs[in.C])
			if err != nil {
				if !vm.handleThrow(chunk, err) {
					return err
				}
				continue
			}
			{
				old := regs[in.A]
				wv := r
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}

		case OpBitAnd, OpBitOr, OpBitXor:
			op := bitAnd
			switch in.Op {
			case OpBitOr:
				op = bitOr
			case OpBitXor:
				op = bitXor
			}
			r, err := vm.bitwiseSlow(regs[in.B], regs[in.C], op)
			if err != nil {
				if !vm.handleThrow(chunk, err) {
					return err
				}
				continue
			}
			{
				old := regs[in.A]
				wv := r
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		case OpBitNot:
			r, err := vm.bitNotSlow(regs[in.B])
			if err != nil {
				if !vm.handleThrow(chunk, err) {
					return err
				}
				continue
			}
			{
				old := regs[in.A]
				wv := r
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		case OpShl:
			r, err := vm.shiftSlow(regs[in.B], regs[in.C], true)
			if err != nil {
				if !vm.handleThrow(chunk, err) {
					return err
				}
				continue
			}
			{
				old := regs[in.A]
				wv := r
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		case OpShr:
			r, err := vm.shiftSlow(regs[in.B], regs[in.C], false)
			if err != nil {
				if !vm.handleThrow(chunk, err) {
					return err
				}
				continue
			}
			{
				old := regs[in.A]
				wv := r
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}

		case OpAddK:
			r, err := vm.addSlow(regs[in.B], consts[in.C])
			if err != nil {
				if !vm.handleThrow(chunk, err) {
					return err
				}
				continue
			}
			{
				old := regs[in.A]
				wv := r
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		case OpSubK:
			r, err := vm.subSlow(regs[in.B], consts[in.C])
			if err != nil {
				if !vm.handleThrow(chunk, err) {
					return err
				}
				continue
			}
			{
				old := regs[in.A]
				wv := r
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		case OpMulK:
			r, err := vm.mulSlow(regs[in.B], consts[in.C])
			if err != nil {
				if !vm.handleThrow(chunk, err) {
					return err
				}
				continue
			}
			{
				old := regs[in.A]
				wv := r
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		case OpDivK:
			r, err := vm.divSlow(regs[in.B], consts[in.C])
			if err != nil {
				if !vm.handleThrow(chunk, err) {
					return err
				}
				continue
			}
			{
				old := regs[in.A]
				wv := r
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}

		case OpConcat:
			ls, lerr := vm.stringify(regs[in.B])
			if lerr != nil {
				if !vm.handleThrow(chunk, lerr) {
					return lerr
				}
				continue
			}
			rs, rerr := vm.stringify(regs[in.C])
			if rerr != nil {
				if !vm.handleThrow(chunk, rerr) {
					return rerr
				}
				continue
			}
			// Если левый операнд уже накапливается в буфере — дописываем в
			// него (на месте либо копией при фактическом разделении).
			if lb := deref(regs[in.B]); lb.typ == StringType && lb.num&bufBit != 0 {
				{
					old := regs[in.A]
					wv := appendStringSlot(lb, ls, rs)
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			} else {
				{
					old := regs[in.A]
					wv := MakeString(ls + rs)
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}

		case OpConcatApp:
			// `.=` : A = B . C с накоплением в буфере B.
			ls, lerr := vm.stringify(regs[in.B])
			if lerr != nil {
				if !vm.handleThrow(chunk, lerr) {
					return lerr
				}
				continue
			}
			rs, rerr := vm.stringify(regs[in.C])
			if rerr != nil {
				if !vm.handleThrow(chunk, rerr) {
					return rerr
				}
				continue
			}
			{
				old := regs[in.A]
				wv := appendStringSlot(deref(regs[in.B]), ls, rs)
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}

		case OpConcatN:
			// Конкатенация C частей из регистров B..B+C-1: одна аллокация.
			n := int(in.C)
			base := int(in.B)
			var small [16]string
			var parts []string
			if n <= len(small) {
				parts = small[:n]
			} else {
				parts = make([]string, n)
			}
			total := 0
			bad := false
			for i := 0; i < n; i++ {
				ps, perr := vm.stringify(regs[base+i])
				if perr != nil {
					if !vm.handleThrow(chunk, perr) {
						return perr
					}
					bad = true
					break
				}
				parts[i] = ps
				total += len(ps)
			}
			if bad {
				continue
			}
			{
				old := regs[in.A]
				wv := concatNResult(parts, total)
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}

		case OpEqual, OpNotEqual, OpIdentical, OpNotIdentical,
			OpLess, OpLessOrEqual, OpGreater, OpGreaterEqual:
			{
				old := regs[in.A]
				wv := compareSlow2(vm, in.Op, regs[in.B], regs[in.C])
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		case OpSpaceship:
			{
				old := regs[in.A]
				wv := spaceshipSlow(vm, regs[in.B], regs[in.C])
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		case OpLessK:
			{
				old := regs[in.A]
				wv := compareSlow2(vm, OpLess, regs[in.B], consts[in.C])
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		case OpLessOrEqualK:
			{
				old := regs[in.A]
				wv := compareSlow2(vm, OpLessOrEqual, regs[in.B], consts[in.C])
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		case OpGreaterK:
			{
				old := regs[in.A]
				wv := compareSlow2(vm, OpGreater, regs[in.B], consts[in.C])
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		case OpGreaterEqualK:
			{
				old := regs[in.A]
				wv := compareSlow2(vm, OpGreaterEqual, regs[in.B], consts[in.C])
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		case OpEqualK:
			{
				old := regs[in.A]
				wv := compareSlow2(vm, OpEqual, regs[in.B], consts[in.C])
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}

		case OpAddInt:
			a, b := &regs[in.B], &regs[in.C]
			switch {
			case a.typ == IntType && b.typ == IntType:
				ai, bi := int64(a.num), int64(b.num)
				r := ai + bi
				d := &regs[in.A]
				// При A==B/C старое значение регистра — сам операнд (int,
				// не учитывается), поэтому барьер не нужен вовсе.
				if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				if (bi > 0 && r < ai) || (bi < 0 && r > ai) {
					d.typ, d.num, d.ptr = FloatType, math.Float64bits(float64(ai)+float64(bi)), nil
				} else {
					d.typ, d.num, d.ptr = IntType, uint64(r), nil
				}
			case a.typ == FloatType && b.typ == FloatType:
				d := &regs[in.A]
				if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = FloatType, math.Float64bits(math.Float64frombits(a.num)+math.Float64frombits(b.num)), nil
			default:
				r, err := vm.addSlow(regs[in.B], regs[in.C])
				if err != nil {
					if !vm.handleThrow(chunk, err) {
						return err
					}
					continue
				}
				{
					old := regs[in.A]
					wv := r
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}
		case OpAddFloat:
			d := &regs[in.A]
			if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
				vm.wDropReg(int(in.A))
			}
			d.typ, d.num, d.ptr = FloatType, math.Float64bits(math.Float64frombits(regs[in.B].num)+math.Float64frombits(regs[in.C].num)), nil
		case OpSubInt:
			a, b := &regs[in.B], &regs[in.C]
			switch {
			case a.typ == IntType && b.typ == IntType:
				ai, bi := int64(a.num), int64(b.num)
				r := ai - bi
				d := &regs[in.A]
				if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				if (bi < 0 && r < ai) || (bi > 0 && r > ai) {
					d.typ, d.num, d.ptr = FloatType, math.Float64bits(float64(ai)-float64(bi)), nil
				} else {
					d.typ, d.num, d.ptr = IntType, uint64(r), nil
				}
			case a.typ == FloatType && b.typ == FloatType:
				d := &regs[in.A]
				if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = FloatType, math.Float64bits(math.Float64frombits(a.num)-math.Float64frombits(b.num)), nil
			default:
				r, err := vm.subSlow(regs[in.B], regs[in.C])
				if err != nil {
					if !vm.handleThrow(chunk, err) {
						return err
					}
					continue
				}
				{
					old := regs[in.A]
					wv := r
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}
		case OpSubFloat:
			d := &regs[in.A]
			if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
				vm.wDropReg(int(in.A))
			}
			d.typ, d.num, d.ptr = FloatType, math.Float64bits(math.Float64frombits(regs[in.B].num)-math.Float64frombits(regs[in.C].num)), nil
		case OpMulInt:
			a, b := &regs[in.B], &regs[in.C]
			switch {
			case a.typ == IntType && b.typ == IntType:
				ai, bi := int64(a.num), int64(b.num)
				d := &regs[in.A]
				if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				if r, ok := mulInt64(ai, bi); ok {
					d.typ, d.num, d.ptr = IntType, uint64(r), nil
				} else {
					d.typ, d.num, d.ptr = FloatType, math.Float64bits(float64(ai)*float64(bi)), nil
				}
			case a.typ == FloatType && b.typ == FloatType:
				d := &regs[in.A]
				if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = FloatType, math.Float64bits(math.Float64frombits(a.num)*math.Float64frombits(b.num)), nil
			default:
				r, err := vm.mulSlow(regs[in.B], regs[in.C])
				if err != nil {
					if !vm.handleThrow(chunk, err) {
						return err
					}
					continue
				}
				{
					old := regs[in.A]
					wv := r
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}
		case OpMulFloat:
			d := &regs[in.A]
			if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
				vm.wDropReg(int(in.A))
			}
			d.typ, d.num, d.ptr = FloatType, math.Float64bits(math.Float64frombits(regs[in.B].num)*math.Float64frombits(regs[in.C].num)), nil
		case OpDivInt:
			a, b := &regs[in.B], &regs[in.C]
			if a.typ == IntType && b.typ == IntType {
				ai, bi := int64(a.num), int64(b.num)
				switch {
				case bi == 0:
					r, err := vm.divSlow(*a, *b)
					if err != nil {
						if !vm.handleThrow(chunk, err) {
							return err
						}
						continue
					}
					{
						old := regs[in.A]
						wv := r
						regs[in.A] = wv
						if trackedType(old.typ) || trackedType(wv.typ) {
							vm.wRegSlow(int(in.A), old)
						}
					}
				case ai == math.MinInt64 && bi == -1:
					d := &regs[in.A]
					if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
						vm.wDropReg(int(in.A))
					}
					d.typ, d.num, d.ptr = FloatType, math.Float64bits(float64(ai)/float64(bi)), nil
				case ai%bi == 0:
					d := &regs[in.A]
					if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
						vm.wDropReg(int(in.A))
					}
					d.typ, d.num, d.ptr = IntType, uint64(ai/bi), nil
				default:
					d := &regs[in.A]
					if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
						vm.wDropReg(int(in.A))
					}
					d.typ, d.num, d.ptr = FloatType, math.Float64bits(float64(ai)/float64(bi)), nil
				}
			} else if a.typ == FloatType && b.typ == FloatType {
				bf := math.Float64frombits(b.num)
				if bf == 0 {
					r, err := vm.divSlow(*a, *b)
					if err != nil {
						if !vm.handleThrow(chunk, err) {
							return err
						}
						continue
					}
					{
						old := regs[in.A]
						wv := r
						regs[in.A] = wv
						if trackedType(old.typ) || trackedType(wv.typ) {
							vm.wRegSlow(int(in.A), old)
						}
					}
				} else {
					d := &regs[in.A]
					if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
						vm.wDropReg(int(in.A))
					}
					d.typ, d.num, d.ptr = FloatType, math.Float64bits(math.Float64frombits(a.num)/bf), nil
				}
			} else {
				r, err := vm.divSlow(*a, *b)
				if err != nil {
					if !vm.handleThrow(chunk, err) {
						return err
					}
					continue
				}
				{
					old := regs[in.A]
					wv := r
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}
		case OpDivFloat:
			a, b := &regs[in.B], &regs[in.C]
			bf := math.Float64frombits(b.num)
			if bf == 0 {
				r, err := vm.divSlow(*a, *b)
				if err != nil {
					if !vm.handleThrow(chunk, err) {
						return err
					}
					continue
				}
				{
					old := regs[in.A]
					wv := r
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			} else {
				d := &regs[in.A]
				if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = FloatType, math.Float64bits(math.Float64frombits(a.num)/bf), nil
			}

		case OpAddIntK:
			a := &regs[in.B]
			if a.typ == IntType {
				ai, bi := int64(a.num), chunk.constInts[in.C]
				r := ai + bi
				d := &regs[in.A]
				if in.A != in.B && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				if (bi > 0 && r < ai) || (bi < 0 && r > ai) {
					d.typ, d.num, d.ptr = FloatType, math.Float64bits(float64(ai)+float64(bi)), nil
				} else {
					d.typ, d.num, d.ptr = IntType, uint64(r), nil
				}
			} else {
				r, err := vm.addSlow(*a, consts[in.C])
				if err != nil {
					if !vm.handleThrow(chunk, err) {
						return err
					}
					continue
				}
				{
					old := regs[in.A]
					wv := r
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}
		case OpAddFloatK:
			a := &regs[in.B]
			kf := chunk.constFloats[in.C]
			switch a.typ {
			case FloatType:
				d := &regs[in.A]
				if in.A != in.B && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = FloatType, math.Float64bits(math.Float64frombits(a.num)+kf), nil
			case IntType:
				d := &regs[in.A]
				if in.A != in.B && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = FloatType, math.Float64bits(float64(int64(a.num))+kf), nil
			default:
				r, err := vm.addSlow(*a, MakeFloat(kf))
				if err != nil {
					if !vm.handleThrow(chunk, err) {
						return err
					}
					continue
				}
				{
					old := regs[in.A]
					wv := r
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}
		case OpSubIntK:
			a := &regs[in.B]
			if a.typ == IntType {
				ai, bi := int64(a.num), chunk.constInts[in.C]
				r := ai - bi
				d := &regs[in.A]
				if in.A != in.B && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				if (bi < 0 && r < ai) || (bi > 0 && r > ai) {
					d.typ, d.num, d.ptr = FloatType, math.Float64bits(float64(ai)-float64(bi)), nil
				} else {
					d.typ, d.num, d.ptr = IntType, uint64(r), nil
				}
			} else {
				r, err := vm.subSlow(*a, consts[in.C])
				if err != nil {
					if !vm.handleThrow(chunk, err) {
						return err
					}
					continue
				}
				{
					old := regs[in.A]
					wv := r
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}
		case OpMulIntK:
			a := &regs[in.B]
			if a.typ == IntType {
				ai, bi := int64(a.num), chunk.constInts[in.C]
				d := &regs[in.A]
				if in.A != in.B && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				if r, ok := mulInt64(ai, bi); ok {
					d.typ, d.num, d.ptr = IntType, uint64(r), nil
				} else {
					d.typ, d.num, d.ptr = FloatType, math.Float64bits(float64(ai)*float64(bi)), nil
				}
			} else {
				r, err := vm.mulSlow(*a, consts[in.C])
				if err != nil {
					if !vm.handleThrow(chunk, err) {
						return err
					}
					continue
				}
				{
					old := regs[in.A]
					wv := r
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}

		case OpAddIntImm:
			a := &regs[in.B]
			if a.typ == IntType {
				ai, bi := int64(a.num), int64(in.C)
				r := ai + bi
				d := &regs[in.A]
				if in.A != in.B && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				if (bi > 0 && r < ai) || (bi < 0 && r > ai) {
					d.typ, d.num, d.ptr = FloatType, math.Float64bits(float64(ai)+float64(bi)), nil
				} else {
					d.typ, d.num, d.ptr = IntType, uint64(r), nil
				}
			} else {
				r, err := vm.addSlow(*a, MakeInt(int64(in.C)))
				if err != nil {
					if !vm.handleThrow(chunk, err) {
						return err
					}
					continue
				}
				{
					old := regs[in.A]
					wv := r
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}
		case OpSubIntImm:
			a := &regs[in.B]
			if a.typ == IntType {
				ai, bi := int64(a.num), int64(in.C)
				r := ai - bi
				d := &regs[in.A]
				if in.A != in.B && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				if (bi < 0 && r < ai) || (bi > 0 && r > ai) {
					d.typ, d.num, d.ptr = FloatType, math.Float64bits(float64(ai)-float64(bi)), nil
				} else {
					d.typ, d.num, d.ptr = IntType, uint64(r), nil
				}
			} else {
				r, err := vm.subSlow(*a, MakeInt(int64(in.C)))
				if err != nil {
					if !vm.handleThrow(chunk, err) {
						return err
					}
					continue
				}
				{
					old := regs[in.A]
					wv := r
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}
		case OpMulIntImm:
			a := &regs[in.B]
			if a.typ == IntType {
				ai, bi := int64(a.num), int64(in.C)
				d := &regs[in.A]
				if in.A != in.B && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				if r, ok := mulInt64(ai, bi); ok {
					d.typ, d.num, d.ptr = IntType, uint64(r), nil
				} else {
					d.typ, d.num, d.ptr = FloatType, math.Float64bits(float64(ai)*float64(bi)), nil
				}
			} else {
				r, err := vm.mulSlow(*a, MakeInt(int64(in.C)))
				if err != nil {
					if !vm.handleThrow(chunk, err) {
						return err
					}
					continue
				}
				{
					old := regs[in.A]
					wv := r
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}

		case OpLessInt:
			a, b := &regs[in.B], &regs[in.C]
			switch {
			case a.typ == IntType && b.typ == IntType:
				d := &regs[in.A]
				if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = BoolType, b2i(int64(a.num) < int64(b.num)), nil
			case a.typ == FloatType && b.typ == FloatType:
				d := &regs[in.A]
				if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = BoolType, b2i(math.Float64frombits(a.num) < math.Float64frombits(b.num)), nil
			default:
				{
					old := regs[in.A]
					wv := compareSlow2(vm, OpLess, *a, *b)
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}
		case OpLessFloat:
			d := &regs[in.A]
			if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
				vm.wDropReg(int(in.A))
			}
			d.typ, d.num, d.ptr = BoolType, b2i(math.Float64frombits(regs[in.B].num) < math.Float64frombits(regs[in.C].num)), nil
		case OpLessOrEqualInt:
			a, b := &regs[in.B], &regs[in.C]
			switch {
			case a.typ == IntType && b.typ == IntType:
				d := &regs[in.A]
				if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = BoolType, b2i(int64(a.num) <= int64(b.num)), nil
			case a.typ == FloatType && b.typ == FloatType:
				d := &regs[in.A]
				if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = BoolType, b2i(math.Float64frombits(a.num) <= math.Float64frombits(b.num)), nil
			default:
				{
					old := regs[in.A]
					wv := compareSlow2(vm, OpLessOrEqual, *a, *b)
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}
		case OpLessOrEqualFloat:
			d := &regs[in.A]
			if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
				vm.wDropReg(int(in.A))
			}
			d.typ, d.num, d.ptr = BoolType, b2i(math.Float64frombits(regs[in.B].num) <= math.Float64frombits(regs[in.C].num)), nil
		case OpGreaterInt:
			a, b := &regs[in.B], &regs[in.C]
			switch {
			case a.typ == IntType && b.typ == IntType:
				d := &regs[in.A]
				if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = BoolType, b2i(int64(a.num) > int64(b.num)), nil
			case a.typ == FloatType && b.typ == FloatType:
				d := &regs[in.A]
				if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = BoolType, b2i(math.Float64frombits(a.num) > math.Float64frombits(b.num)), nil
			default:
				{
					old := regs[in.A]
					wv := compareSlow2(vm, OpGreater, *a, *b)
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}
		case OpGreaterFloat:
			d := &regs[in.A]
			if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
				vm.wDropReg(int(in.A))
			}
			d.typ, d.num, d.ptr = BoolType, b2i(math.Float64frombits(regs[in.B].num) > math.Float64frombits(regs[in.C].num)), nil
		case OpGreaterEqualInt:
			a, b := &regs[in.B], &regs[in.C]
			switch {
			case a.typ == IntType && b.typ == IntType:
				d := &regs[in.A]
				if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = BoolType, b2i(int64(a.num) >= int64(b.num)), nil
			case a.typ == FloatType && b.typ == FloatType:
				d := &regs[in.A]
				if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = BoolType, b2i(math.Float64frombits(a.num) >= math.Float64frombits(b.num)), nil
			default:
				{
					old := regs[in.A]
					wv := compareSlow2(vm, OpGreaterEqual, *a, *b)
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}
		case OpGreaterEqualFloat:
			d := &regs[in.A]
			if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
				vm.wDropReg(int(in.A))
			}
			d.typ, d.num, d.ptr = BoolType, b2i(math.Float64frombits(regs[in.B].num) >= math.Float64frombits(regs[in.C].num)), nil
		case OpEqualInt:
			a, b := &regs[in.B], &regs[in.C]
			switch {
			case a.typ == IntType && b.typ == IntType:
				d := &regs[in.A]
				if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = BoolType, b2i(int64(a.num) == int64(b.num)), nil
			case a.typ == FloatType && b.typ == FloatType:
				d := &regs[in.A]
				if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = BoolType, b2i(math.Float64frombits(a.num) == math.Float64frombits(b.num)), nil
			default:
				{
					old := regs[in.A]
					wv := compareSlow2(vm, OpEqual, *a, *b)
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}
		case OpEqualFloat:
			d := &regs[in.A]
			if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
				vm.wDropReg(int(in.A))
			}
			d.typ, d.num, d.ptr = BoolType, b2i(math.Float64frombits(regs[in.B].num) == math.Float64frombits(regs[in.C].num)), nil
		case OpIdenticalInt:
			a, b := &regs[in.B], &regs[in.C]
			d := &regs[in.A]
			if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
				vm.wDropReg(int(in.A))
			}
			d.typ, d.num, d.ptr = BoolType, b2i(a.typ == IntType && b.typ == IntType && a.num == b.num), nil
		case OpIdenticalFloat:
			a, b := &regs[in.B], &regs[in.C]
			d := &regs[in.A]
			if in.A != in.B && int(in.A) != int(in.C) && trackedType(d.typ) {
				vm.wDropReg(int(in.A))
			}
			d.typ, d.num, d.ptr = BoolType, b2i(a.typ == FloatType && b.typ == FloatType && math.Float64frombits(a.num) == math.Float64frombits(b.num)), nil

		case OpLessIntK:
			a := &regs[in.B]
			if a.typ == IntType {
				d := &regs[in.A]
				if in.A != in.B && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = BoolType, b2i(int64(a.num) < chunk.constInts[in.C]), nil
			} else {
				{
					old := regs[in.A]
					wv := compareSlow2(vm, OpLess, *a, consts[in.C])
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}
		case OpLessFloatK:
			d := &regs[in.A]
			if in.A != in.B && trackedType(d.typ) {
				vm.wDropReg(int(in.A))
			}
			d.typ, d.num, d.ptr = BoolType, b2i(math.Float64frombits(regs[in.B].num) < chunk.constFloats[in.C]), nil
		case OpLessOrEqualIntK:
			a := &regs[in.B]
			if a.typ == IntType {
				d := &regs[in.A]
				if in.A != in.B && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = BoolType, b2i(int64(a.num) <= chunk.constInts[in.C]), nil
			} else {
				{
					old := regs[in.A]
					wv := compareSlow2(vm, OpLessOrEqual, *a, consts[in.C])
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}
		case OpGreaterIntK:
			a := &regs[in.B]
			if a.typ == IntType {
				d := &regs[in.A]
				if in.A != in.B && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = BoolType, b2i(int64(a.num) > chunk.constInts[in.C]), nil
			} else {
				{
					old := regs[in.A]
					wv := compareSlow2(vm, OpGreater, *a, consts[in.C])
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}
		case OpGreaterEqualIntK:
			a := &regs[in.B]
			if a.typ == IntType {
				d := &regs[in.A]
				if in.A != in.B && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = BoolType, b2i(int64(a.num) >= chunk.constInts[in.C]), nil
			} else {
				{
					old := regs[in.A]
					wv := compareSlow2(vm, OpGreaterEqual, *a, consts[in.C])
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}
		case OpEqualIntK:
			a := &regs[in.B]
			if a.typ == IntType {
				d := &regs[in.A]
				if in.A != in.B && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = BoolType, b2i(int64(a.num) == chunk.constInts[in.C]), nil
			} else {
				{
					old := regs[in.A]
					wv := compareSlow2(vm, OpEqual, *a, consts[in.C])
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}

		case OpLessIntImm:
			a := &regs[in.B]
			if a.typ == IntType {
				d := &regs[in.A]
				if in.A != in.B && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = BoolType, b2i(int64(a.num) < int64(in.C)), nil
			} else {
				{
					old := regs[in.A]
					wv := compareSlow2(vm, OpLess, *a, MakeInt(int64(in.C)))
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}
		case OpLessOrEqualIntImm:
			a := &regs[in.B]
			if a.typ == IntType {
				d := &regs[in.A]
				if in.A != in.B && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = BoolType, b2i(int64(a.num) <= int64(in.C)), nil
			} else {
				{
					old := regs[in.A]
					wv := compareSlow2(vm, OpLessOrEqual, *a, MakeInt(int64(in.C)))
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}
		case OpGreaterIntImm:
			a := &regs[in.B]
			if a.typ == IntType {
				d := &regs[in.A]
				if in.A != in.B && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = BoolType, b2i(int64(a.num) > int64(in.C)), nil
			} else {
				{
					old := regs[in.A]
					wv := compareSlow2(vm, OpGreater, *a, MakeInt(int64(in.C)))
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}
		case OpGreaterEqualIntImm:
			a := &regs[in.B]
			if a.typ == IntType {
				d := &regs[in.A]
				if in.A != in.B && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = BoolType, b2i(int64(a.num) >= int64(in.C)), nil
			} else {
				{
					old := regs[in.A]
					wv := compareSlow2(vm, OpGreaterEqual, *a, MakeInt(int64(in.C)))
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}
		case OpEqualIntImm:
			a := &regs[in.B]
			if a.typ == IntType {
				d := &regs[in.A]
				if in.A != in.B && trackedType(d.typ) {
					vm.wDropReg(int(in.A))
				}
				d.typ, d.num, d.ptr = BoolType, b2i(int64(a.num) == int64(in.C)), nil
			} else {
				{
					old := regs[in.A]
					wv := compareSlow2(vm, OpEqual, *a, MakeInt(int64(in.C)))
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
			}

		case OpEcho:
			s, serr := vm.stringify(regs[in.A])
			if serr != nil {
				if !vm.handleThrow(chunk, serr) {
					return serr
				}
				continue
			}
			vm.Write(s)

		case OpJumpIfLessIntK:
			// Fusion LESS_INT_K + JUMP_IF_FALSE: переходим, когда сравнение
			// B < const[C] ЛОЖНО.
			a := &regs[in.B]
			var less bool
			if a.typ == IntType {
				less = int64(a.num) < chunk.constInts[in.C]
			} else {
				less = compareSlow2(vm, OpLess, *a, consts[in.C]).BoolVal()
			}
			if !less {
				vm.ip = int(chunk.jumps[in.A])
			}

		case OpJumpIfLessIntImm:
			// Fusion LESS_INT_IMM + JUMP_IF_FALSE: переходим, когда
			// сравнение B < C ЛОЖНО.
			a := &regs[in.B]
			var less bool
			if a.typ == IntType {
				less = int64(a.num) < int64(in.C)
			} else {
				less = compareSlow2(vm, OpLess, *a, MakeInt(int64(in.C))).BoolVal()
			}
			if !less {
				vm.ip = int(chunk.jumps[in.A])
			}

		case OpJump:
			vm.ip = int(in.C)
		case OpJumpIfFalse:
			if !regs[in.A].ToBool() {
				vm.ip = int(in.C)
			}
		case OpJumpIfTrue:
			if regs[in.A].ToBool() {
				vm.ip = int(in.C)
			}

		case OpLogicalAnd:
			{
				old := regs[in.A]
				wv := MakeBool(regs[in.B].ToBool() && regs[in.C].ToBool())
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}

		case OpPushTry:
			vm.tries = append(vm.tries, tryEntry{fp: vm.fp, tiIdx: int(in.C)})

		case OpPopTry:
			if n := len(vm.tries); n > 0 && vm.tries[n-1].fp == vm.fp {
				vm.tries = vm.tries[:n-1]
			}

		case OpThrow:
			err := vm.makeThrowable(regs[in.A])
			if l := vm.line(); l > 0 {
				vm.curLine = l
			}
			if !vm.handleThrow(chunk, err) {
				return err
			}

		case OpEnterFinally:
			if n := len(vm.tries); n > 0 {
				h := &vm.tries[n-1]
				if h.fp == vm.fp && h.tiIdx == int(in.C) {
					h.inFinally = true
				}
			}

		case OpFinallyEnd:
			ti := int(in.C)
			if n := len(vm.tries); n > 0 {
				h := &vm.tries[n-1]
				if h.fp == vm.fp && h.tiIdx == ti {
					saved := h.savedErr
					vm.tries = vm.tries[:n-1]
					if saved != nil && !vm.handleThrow(chunk, saved) {
						// Сигнал закрытия генератора мог быть заменён
						// реальным отложенным исключением (bug71604).
						if saved.close {
							if v := vm.dispTh; v != nil && !v.close {
								return v
							}
						}
						return saved
					}
				}
			}

		case OpCall:
			if hr := vm.opCall(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpCallArr:
			if hr := vm.opCallArr(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpCallDyn:
			if hr := vm.opCallDyn(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpRet:
			if hr := vm.opRet(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpYield:
			if hr := vm.opYield(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpClosure:
			if hr := vm.opClosure(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpArray:
			if hr := vm.opArray(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpIndex:
			if hr := vm.opIndex(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpIndexRaw:
			if hr := vm.opIndexRaw(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpIndexRawWrite:
			if hr := vm.opIndexRawWrite(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpIndexRef:
			if hr := vm.opIndexRef(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpIndexSet:
			if hr := vm.opIndexSet(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpIndexRW:
			if hr := vm.opIndexRW(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpIndexAppendRaw:
			if hr := vm.opIndexAppendRaw(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpIndexSetRaw:
			if hr := vm.opIndexSetRaw(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpIndexSetOverload:
			if hr := vm.opIndexSetOverload(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpIndexSetOverloadRaw:
			if hr := vm.opIndexSetOverloadRaw(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpPropSetOverload:
			if hr := vm.opPropSetOverload(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpPropSetOverloadDyn:
			if hr := vm.opPropSetOverloadDyn(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpAppend:
			if hr := vm.opAppend(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpAppendCompound:
			if hr := vm.opAppendCompound(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpLen:
			if hr := vm.opLen(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpUnset:
			if hr := vm.opUnset(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpIndexIsset:
			if hr := vm.opIndexIsset(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpIndexSafe:
			if hr := vm.opIndexSafe(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpIndexSafeWarn:
			if hr := vm.opIndexSafeWarn(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpIndexSetRes:
			if hr := vm.opIndexSetRes(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpIsObject:
			if hr := vm.opIsObject(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpIterInit:
			if hr := vm.opIterInit(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpIterValid:
			if hr := vm.opIterValid(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpIterKey:
			if hr := vm.opIterKey(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpIterCurrent:
			if hr := vm.opIterCurrent(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpIterCurrentRef:
			if hr := vm.opIterCurrentRef(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpIterNext:
			if hr := vm.opIterNext(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpNew:
			if hr := vm.opNew(regs, consts, chunk, in); hr.err != nil {
				if !vm.handleThrow(chunk, hr.err) {
					return hr.err
				}
				continue
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpDeclareClass:
			if hr := vm.opDeclareClass(chunk, in); hr.err != nil {
				if !vm.handleThrow(chunk, hr.err) {
					return hr.err
				}
				continue
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpNewDyn:
			if hr := vm.opNewDyn(regs, consts, chunk, in); hr.err != nil {
				if !vm.handleThrow(chunk, hr.err) {
					return hr.err
				}
				continue
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpPropGet:
			if hr := vm.opPropGet(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpPropGetDyn:
			if hr := vm.opPropGetDyn(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpPropRef:
			if hr := vm.opPropRef(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpPropRefDyn:
			if hr := vm.opPropRefDyn(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpPropUnrefDyn:
			if hr := vm.opPropUnrefDyn(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpPropGetRW, OpPropGetRWDyn, OpPropGetRWRead, OpPropGetRWInterm,
			OpPropGetRWArr, OpPropGetRWArrDyn:
			if hr := vm.opPropGetRW(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpPropGetSafe:
			if hr := vm.opPropGetSafe(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpPropGetSafeDyn:
			if hr := vm.opPropGetSafeDyn(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpPropSet:
			if hr := vm.opPropSet(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpPropSetDyn:
			if hr := vm.opPropSetDyn(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpPropSetRef:
			if hr := vm.opPropSetRef(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpPropSetRefDyn:
			if hr := vm.opPropSetRefDyn(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpPropIsset:
			if hr := vm.opPropIsset(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpPropIssetDyn:
			if hr := vm.opPropIssetDyn(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpPropUnset:
			if hr := vm.opPropUnset(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpPropUnsetDyn:
			if hr := vm.opPropUnsetDyn(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpMethodCall:
			if hr := vm.opMethodCall(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpMethodCallDyn:
			if hr := vm.opMethodCallDyn(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpStaticCall:
			if hr := vm.opStaticCall(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpStaticCallDyn:
			if hr := vm.opStaticCallDyn(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpThis:
			if hr := vm.opThis(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpInstanceOf:
			if hr := vm.opInstanceOf(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpClassOf:
			if hr := vm.opClassOf(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpClassName:
			if hr := vm.opClassName(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpClone:
			if hr := vm.opClone(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpStaticGet:
			if hr := vm.opStaticGet(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpStaticGetRW:
			if hr := vm.opStaticGetRW(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpStaticGetRWDyn:
			if hr := vm.opStaticGetRWDyn(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpStaticIsset:
			if hr := vm.opStaticIsset(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpStaticSet:
			if hr := vm.opStaticSet(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpStaticGetDyn:
			if hr := vm.opStaticGetDyn(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpStaticSetDyn:
			if hr := vm.opStaticSetDyn(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpStaticSetRef:
			if hr := vm.opStaticSetRef(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpStaticSetRefDyn:
			if hr := vm.opStaticSetRefDyn(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpStaticRef:
			if hr := vm.opStaticRef(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpStaticRefDyn:
			if hr := vm.opStaticRefDyn(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpConstGet:
			if hr := vm.opConstGet(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpGlobalGet:
			if hr := vm.opGlobalGet(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpGlobalSet:
			if hr := vm.opGlobalSet(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpGlobalUnset:
			if hr := vm.opGlobalUnset(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpGlobalGetDyn:
			if hr := vm.opGlobalGetDyn(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpGlobalSetDyn:
			if hr := vm.opGlobalSetDyn(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpGlobalIsset:
			if hr := vm.opGlobalIsset(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpGlobalAppend:
			if hr := vm.opGlobalAppend(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpGlobalIndexSet:
			if hr := vm.opGlobalIndexSet(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpNewRef:
			if hr := vm.opNewRef(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpRefGet:
			if hr := vm.opRefGet(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpRefGetRaw:
			if hr := vm.opRefGetRaw(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpRefSetRaw:
			if hr := vm.opRefSetRaw(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpRefSet:
			if hr := vm.opRefSet(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpRefBind:
			if hr := vm.opRefBind(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpRefEnsure:
			if hr := vm.opRefEnsure(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpGlobalRefGet:
			if hr := vm.opGlobalRefGet(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpGlobalRefSet:
			if hr := vm.opGlobalRefSet(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpGlobalRefAddr:
			if hr := vm.opGlobalRefAddr(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpGlobalRefBind:
			if hr := vm.opGlobalRefBind(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		case OpListFetchRef:
			if hr := vm.opListFetchRef(regs, consts, chunk, in); hr.err != nil {
				return hr.err
			} else if hr.halt {
				return nil
			} else if hr.next {
				continue
			}
		default:
			return errUnknownOpcode
		}
	}
}

// retSignal — сигнал возврата из функции. Поле значения не несёт (значение
// лежит в VM.retVal), чтобы возврат из exec не боксил структуру в error
// (иначе — аллокация на каждый вызов функции).
type retSignal struct{}

// exitSignal — сигнал завершения скрипта через exit/die. НЕ несёт Throwable,
// поэтому try/catch его не перехватывает, а Run трактует как штатное
// завершение.
type exitSignal struct{}

func (exitSignal) Error() string { return "exit" }

// IsExitSignal сообщает, что err — сигнал завершения скрипта (exit/die или
// фатальная Parse error, печатаемая как "Parse error:").
func IsExitSignal(err error) bool {
	_, ok := err.(exitSignal)
	return ok
}

func (retSignal) Error() string { return "phpvm: return" }

// warnByRefArgs печатает предупреждение, если by-ref параметр получил
// аргумент-значение (не ссылку): "must be passed by reference, value given".
func (vm *VM) warnByRefArgs(fn *Function, args []Slot) {
	for i := range fn.ByRefArgs {
		if !fn.ByRefArgs[i] {
			continue
		}
		// Variadic-хвост: Zend оборачивает значения в ссылки без диагностики
		// (ARG_MAY_BE_SENT_BY_REF).
		if fn.VariadicByRef && i >= len(fn.ByRefArgs)-1 {
			continue
		}
		if i < len(args) && !args[i].IsRef() {
			name := ""
			// У аргументов variadic-хвоста имя параметра не печатается
			// (Zend: arg_num > num_args).
			tail := fn.VariadicByRef && i >= len(fn.ParamNames)-1
			if !tail && i < len(fn.ParamNames) && fn.ParamNames[i] != "" {
				name = " ($" + fn.ParamNames[i] + ")"
			}
			vm.Warning(fmt.Sprintf("%s(): Argument #%d%s must be passed by reference, value given", fn.Name, i+1, name))
		}
	}
}

// tooFewArgs формирует ошибку ArgumentCountError для вызова userland-функции
// с меньшим числом аргументов, чем объявлено обязательных.
func (vm *VM) tooFewArgs(fn *Function, passed int) *Throwable {
	name := fn.Name
	if fn.DisplayName != "" {
		name = fn.DisplayName
	} else if fn.Scope != nil && name != "" && !strings.Contains(name, "::") &&
		!strings.HasPrefix(name, "{closure}") {
		name = ClassDisplayName(fn.Scope.Name()) + "::" + name
	}
	curFile, curLine := vm.curFile, vm.line()
	if vm.attrCallFile != "" {
		curFile, curLine = vm.attrCallFile, vm.attrCallLine
	}
	var th *Throwable
	quant := "exactly"
	if fn.NumArgs != fn.NumRequired { // есть необязательные/вариативные параметры
		quant = "at least"
	}
	if vm.argNoLoc > 0 {
		th = NewThrowable("ArgumentCountError", fmt.Sprintf(
			"Too few arguments to function %s(), %d passed and %s %d expected",
			name, passed, quant, fn.NumRequired))
	} else {
		th = NewThrowable("ArgumentCountError", fmt.Sprintf(
			"Too few arguments to function %s(), %d passed in %s on line %d and %s %d expected",
			name, passed, curFile, curLine, quant, fn.NumRequired))
	}
	// Zend указывает местом ошибки объявление функции, а не место вызова,
	// и включает кадр вызова в трейс.
	if fn.DefLine > 0 {
		th.LocFile = fn.DefFile
		th.LocLine = fn.DefLine
	} else {
		th.LocFile = vm.curFile
		th.LocLine = vm.line()
	}
	frameFile, frameLine := vm.curFile, vm.line()
	internal := false
	if vm.markNextFrameInternal {
		// Ошибка сгенерирована до pushCall: вручную помечаем кадр внутренним.
		vm.markNextFrameInternal = false
		frameFile, frameLine = "[internal function]", 0
		internal = true
	}
	frame := callFrameInfo{callee: fn, callerFile: frameFile, callerLine: frameLine, objMethod: fn.Scope != nil}
	stack := vm.resolveStack()
	frames := make([]callFrameInfo, 0, len(stack)+1)
	if internal {
		// Из внутреннего кода: кадр конструктора обёртки — самый внутренний
		// вызов (traceString печатает frames в обратном порядке).
		frames = append(frames, stack...)
		frames = append(frames, frame)
	} else {
		frames = append(frames, frame)
		frames = append(frames, stack...)
	}
	th.Trace = deepCopyExceptionFrames(frames)
	return th
}

// SetPendingProvidedMask задаёт маску переданных позиций следующего вызова
// (__php_set_provided_mask).
func (vm *VM) SetPendingProvidedMask(m int64) {
	vm.pendingProvidedMask = m
	vm.pendingProvidedMaskSet = true
}

// takeProvidedMask забирает маску переданных позиций: её потребляет ровно
// тот вызов, который следует за установкой (invoke/invokeMethod/
// invokeClosure берут её в самом начале, чтобы маска не «протекла» на
// следующий userland-вызов при раннем выходе).
func (vm *VM) takeProvidedMask() (int64, bool) {
	m, ok := vm.pendingProvidedMask, vm.pendingProvidedMaskSet
	vm.pendingProvidedMaskSet = false
	return m, ok
}

// checkArity проверяет минимальную арность userland-функции. Для вариативных
// функций достаточно обязательных фиксированных параметров.
func (vm *VM) checkArity(fn *Function, args []Slot) error {
	return vm.checkArityN(fn, args, -1)
}

// checkArityN — checkArity с явным числом фактически переданных аргументов
// (для вызовов с именованными аргументами: ZPP не считает NULL-заглушки
// отсутствующих параметров).
func (vm *VM) checkArityN(fn *Function, args []Slot, provided int) error {
	cf, ok := fn.Executable.(*CompiledFunction)
	if !ok {
		if _, isNative := fn.Executable.(*NativeFunction); isNative {
			n := len(args)
			if provided >= 0 {
				n = provided
			}
			if fn.MinArgs > 0 && n < fn.MinArgs {
				return vm.nativeArityErr(fn, n)
			}
			if !fn.NativeVariadic && fn.MaxArgs > 0 && n > fn.MaxArgs {
				return vm.nativeArityErr(fn, n)
			}
		}
		return nil
	}
	if cf.variadic || fn.NumRequired <= 0 {
		return nil
	}
	if len(args) < fn.NumRequired {
		return vm.tooFewArgs(fn, len(args))
	}
	return nil
}

// nativeArityErr — ArgumentCountError встроенной функции в форме Zend
// ("substr() expects at least 2 arguments, 1 given").
func (vm *VM) nativeArityErr(fn *Function, passed int) *Throwable {
	name := fn.Name
	if fn.DisplayName != "" {
		name = fn.DisplayName
	} else if fn.Scope != nil && name != "" && !strings.Contains(name, "::") {
		name = ClassDisplayName(fn.Scope.Name()) + "::" + name
	}
	plural := func(n int) string {
		if n == 1 {
			return "argument"
		}
		return "arguments"
	}
	var msg string
	switch {
	case passed < fn.MinArgs:
		if fn.MinArgs == fn.MaxArgs {
			msg = fmt.Sprintf("%s() expects exactly %d %s, %d given", name, fn.MinArgs, plural(fn.MinArgs), passed)
		} else {
			msg = fmt.Sprintf("%s() expects at least %d %s, %d given", name, fn.MinArgs, plural(fn.MinArgs), passed)
		}
	case !fn.NativeVariadic && fn.MaxArgs > 0:
		if fn.MinArgs == fn.MaxArgs {
			msg = fmt.Sprintf("%s() expects exactly %d %s, %d given", name, fn.MaxArgs, plural(fn.MaxArgs), passed)
		} else {
			msg = fmt.Sprintf("%s() expects at most %d %s, %d given", name, fn.MaxArgs, plural(fn.MaxArgs), passed)
		}
	}
	th := NewThrowable("ArgumentCountError", msg)
	// Трейс фиксируем до выхода из кадра: в Zend ошибочный вызов присутствует
	// в стеке (#0 [internal function]: substr()).
	th.Trace = vm.captureExceptionTrace()
	if th.LocLine == 0 && !th.LocOverridden {
		for i := len(th.Trace) - 1; i >= 0; i-- {
			f := th.Trace[i]
			if f.callerFile != "" && f.callerFile != "[internal function]" {
				th.LocFile = f.callerFile
				th.LocLine = f.callerLine
				break
			}
		}
	}
	return th
}

// extraNamedNativeErr — ошибка ZPP: встроенная функция не принимает
// неизвестные именованные аргументы (Zend: zend_unexpected_extra_named_error).
func (vm *VM) extraNamedNativeErr(fn *Function) *Throwable {
	name := fn.Name
	if fn.DisplayName != "" {
		name = fn.DisplayName
	} else if fn.Scope != nil && name != "" && !strings.Contains(name, "::") {
		name = ClassDisplayName(fn.Scope.Name()) + "::" + name
	}
	th := NewThrowable("ArgumentCountError", name+"() does not accept unknown named parameters")
	th.Trace = vm.captureExceptionTrace()
	if th.LocLine == 0 && !th.LocOverridden {
		for i := len(th.Trace) - 1; i >= 0; i-- {
			f := th.Trace[i]
			if f.callerFile != "" && f.callerFile != "[internal function]" {
				th.LocFile = f.callerFile
				th.LocLine = f.callerLine
				break
			}
		}
	}
	return th
}

// invoke вызывает функцию. Контекст живёт в VM, поэтому &vm.ctx не
// убегает в heap (в отличие от локальной копии).
func (vm *VM) invoke(fn *Function, args []Slot) (res Slot, err error) {
	namedCount := vm.pendingNamedCount
	vm.pendingNamedCount = -1
	provided := vm.pendingNativeProvided
	vm.pendingNativeProvided = -1
	pmask, pmaskSet := vm.takeProvidedMask()
	extras := vm.pendingFrameNamed
	vm.pendingFrameNamed = nil
	// display — аргументы для кадра трейса: без NULL-заглушек отсутствующих
	// параметров и без extra named (они лежат отдельно).
	display := args
	if provided >= 0 && provided <= len(args) {
		display = args[:provided]
	}
	_, isNativeFn := fn.Executable.(*NativeFunction)
	if !isNativeFn {
		if aerr := vm.checkArity(fn, args); aerr != nil {
			// Zend проверяет типы переданных аргументов раньше нехватки
			// остальных: при strict_types `f("123")` для `f(int $x, string $y)`
			// даёт TypeError, а не ArgumentCountError (арность — только если
			// переданные аргументы типам соответствуют).
			if _, perr := vm.checkParams(fn, args); perr != nil {
				return Slot{}, perr
			}
			return Slot{}, aerr
		}
	}
	// Встроенная функция (NativeFunction без Scope) исполняется в scope
	// вызывающего кадра: get_called_class/get_class_methods и т.п. должны
	// видеть класс вызова, как в Zend (встроенные не создают нового scope).
	if fn.Scope == nil {
		if _, ok := fn.Executable.(*NativeFunction); ok {
			// Кадр нужен для stack trace (напр. serialize() -> #0 ...).
			pushed := false
			if vm.suppressNextFrame || internalTraceHidden(fn.Name) {
				vm.suppressNextFrame = false
			} else {
				vm.pendingFrameNamed = extras
				vm.pushCall(fn, display)
				vm.pendingFrameNamed = nil
				pushed = true
			}
			// Предупреждение о by-ref аргументе-значении уместно при
			// динамическом вызове (call_user_func и т.п.): прямой вызов через
			// опкоды компилятор оформляет Ref-ячейками (в т.ч. spread).
			// Именованные аргументы позицию не заполняют и не предупреждают.
			if (vm.argNoLoc > 0 || vm.cufCall) && len(fn.ByRefArgs) > 0 {
				warnArgs := args
				if provided >= 0 && provided < len(args) {
					warnArgs = args[:provided]
				}
				vm.warnByRefArgs(fn, warnArgs)
			}
			// Проверка арности — после pushCall: кадр встроенной функции
			// присутствует в трейсе (Zend).
			if aerr := vm.checkArityN(fn, args, provided); aerr != nil {
				if pushed {
					vm.popCall()
				}
				return Slot{}, aerr
			}
			if extras != nil && !fn.AcceptsExtraNamed {
				th := vm.extraNamedNativeErr(fn)
				if pushed {
					vm.popCall()
				}
				return Slot{}, th
			}
			res, err = fn.Executable.Call(vm, &vm.ctx, args)
			if pushed {
				vm.popCall()
			}
			return res, err
		}
	}
	var env funcEnv
	haveEnv := false
	// rawArgs — аргументы как их видит вызывающий: в трейсе вариативные
	// аргументы показываются по отдельности (Zend хранит их в кадре), а не
	// упакованным массивом.
	rawArgs := display
	if cf, ok := fn.Executable.(*CompiledFunction); ok {
		env = vm.enterFuncEnv(cf, namedCount)
		haveEnv = true
		if len(fn.ByRefArgs) > 0 {
			// Диагностику by-ref смотрим на исходных аргументах: variadic-хвост
			// ещё не упакован (value-аргументы вариативного by-ref параметра
			// Zend оборачивает в ссылку без предупреждения).
			vm.warnByRefArgs(fn, args)
		}
		args = cf.PackVariadic(args)
		vm.mergeNamedVariadic(args, fn)
	}
	vm.pendingFrameNamed = extras
	vm.pushCall(fn, rawArgs)
	vm.pendingFrameNamed = nil
	if extras != nil && isNativeFn && !fn.AcceptsExtraNamed {
		vm.popCall()
		if haveEnv {
			vm.restoreFuncEnv(env)
		}
		return Slot{}, vm.extraNamedNativeErr(fn)
	}
	savedThis, savedSelf, savedCalled := vm.ctx.this, vm.ctx.self, vm.ctx.called
	savedSuppress := vm.suppress
	if ck, perr := vm.checkParamsN(fn, args, len(args), pmask, pmaskSet); perr != nil {
		err = perr
	} else {
		args = ck
		vm.ctx.this = Slot{}
		vm.ctx.self = fn.Scope
		vm.ctx.called = fn.Scope
		res, err = fn.Executable.Call(vm, &vm.ctx, args)
		if err == nil {
			if fn.RetType != "" {
				if vm.retVerifiedDepth == len(vm.callStack) {
					// Тип уже проверен в точке return (__php_verify_return).
					vm.retVerifiedDepth = 0
				} else {
					res, err = vm.checkReturnType(fn, res)
				}
			}
		}
	}
	vm.ctx.this, vm.ctx.self, vm.ctx.called = savedThis, savedSelf, savedCalled
	vm.suppress = savedSuppress
	vm.popCall()
	if haveEnv {
		vm.restoreFuncEnv(env)
	}
	return res, err
}

// CheckDefaultArg проверяет runtime-вычисленный дефолт параметра (значение
// константы/константного выражения) против объявленного типа: в Zend такой
// дефолт проверяется при вызове, а ошибка оформляется как ошибка аргумента с
// локацией ВЫЗОВА. Возвращает приведённое значение.
func (vm *VM) CheckDefaultArg(val Slot, argNum int) (Slot, error) {
	if len(vm.callStack) == 0 {
		return val, nil
	}
	fr := &vm.callStack[len(vm.callStack)-1]
	fn := fr.callee
	if fn == nil || argNum < 1 || argNum > len(fn.ParamTypes) {
		return val, nil
	}
	t := fn.ParamTypes[argNum-1]
	if t == "" {
		return val, nil
	}
	v := deref(val)
	if ok, out := vm.coerceReturnMode(t, v, vm.argStrict()); ok {
		return out, nil
	}
	name := ""
	if argNum-1 < len(fn.ParamNames) {
		name = fn.ParamNames[argNum-1]
	}
	file, line := vm.curFile, vm.line()
	if ci := vm.info(fr); ci.callerFile != "" && ci.callerFile != "[internal function]" && ci.callerLine > 0 {
		file, line = ci.callerFile, ci.callerLine
	}
	if vm.attrCallFile != "" {
		file, line = vm.attrCallFile, vm.attrCallLine
	}
	if file == "" {
		file = "php"
	}
	if line <= 0 {
		line = 1
	}
	if vm.argNoLoc > 0 {
		file = ""
	}
	th := vm.argTypeError(argFuncDisplayName(fn), argNum, name, typeDisplay(resolveTypeScope(t, fn.Scope)), valueTypeName(vm, v), file, line)
	th.Trace = vm.captureExceptionTrace()
	th.LocFile = fn.DefFile
	th.LocLine = fn.DefLine
	// Zend не добавляет «and defined in» для ошибки runtime-дефолта
	// (scalar_constant_defaults_error).
	return val, th
}

// checkParams проверяет/приводит типы аргументов вызова (слабая типизация).
func (vm *VM) checkParams(fn *Function, args []Slot) ([]Slot, error) {
	return vm.checkParamsN(fn, args, -1, 0, false)
}

// checkParamsN — checkParams с явным числом переданных вызывающим аргументов:
// null-заглушки отсутствующих параметров не считаются явным null (bug68446:
// `a(null)` при `array $a = FOO` — TypeError, а `a()` — дефолт). mask/maskSet —
// маска реально переданных позиций при именованных аргументах.
func (vm *VM) checkParamsN(fn *Function, args []Slot, provided int, mask int64, maskSet bool) ([]Slot, error) {
	types := fn.ParamTypes
	if len(types) == 0 {
		return args, nil
	}
	cf, ok := fn.Executable.(*CompiledFunction)
	if !ok {
		return args, nil
	}
	fixed := len(types)
	variadic := cf.variadic
	if variadic {
		fixed--
	}
	// Проверка позиционных аргументов.
	for i := 0; i < len(args) && i < len(types); i++ {
		if variadic && i >= fixed {
			// Вариативный хвост упакован в массив: проверяем его элементы
			// отдельно ниже, а не массив целиком.
			break
		}
		t := types[i]
		if t == "" {
			continue
		}
		arg := deref(args[i])
		byref := i < len(fn.ByRefArgs) && fn.ByRefArgs[i]
		// Null-заглушка для непереданного необязательного аргумента не является
		// нарушением типа (значение подставится default'ом в теле функции).
		// Явно переданный null (argNum < provided) — нарушение, если тип его
		// не допускает.
		// Пропуски именованных аргументов (маска) — не явный null; без маски
		// null-аргумент считается переданным явно (прежнее правило).
		omitted := false
		if maskSet {
			omitted = mask&(1<<uint(i)) == 0
		} else if provided >= 0 {
			omitted = i >= provided
		}
		if args[i].typ == NullType && omitted &&
			(i >= fn.NumRequired || (variadic && i >= fixed)) {
			continue
		}
		strict := vm.argStrict()
		if ok, out := vm.coerceReturnMode(t, arg, strict); ok {
			if out.typ == IntType && (arg.typ == FloatType || arg.typ == StringType) {
				vm.deprecateFloatToInt(fn.DefLine, arg)
			}
			if byref && args[i].typ == RefType {
				if r := args[i].Ref(); r != nil {
					r.v = out
				}
			} else if i < len(args) {
				args[i] = out
			}
			continue
		}
		name := ""
		if i < len(fn.ParamNames) {
			name = fn.ParamNames[i]
		}
		file := vm.curFileAt()
		line := vm.line()
		if vm.attrCallFile != "" {
			file, line = vm.attrCallFile, vm.attrCallLine
		}
		if file == "" {
			file = "php"
		}
		if line <= 0 {
			line = 1
		}
		if vm.argNoLoc > 0 {
			file = ""
		}
		th := vm.argTypeError(argFuncDisplayName(fn), i+1, name, typeDisplay(resolveTypeScope(t, fn.Scope)), valueTypeName(vm, arg), file, line)
		th.Trace = vm.captureExceptionTrace()
		th.LocFile = fn.DefFile
		th.LocLine = fn.DefLine
		if th.LocLine > 0 {
			th.DefinedIn = vm.argNoLoc == 0
		}
		return args, th
	}
	if variadic {
		vt := types[len(types)-1]
		if vt != "" && fixed < len(args) {
			if packed := deref(args[fixed]); packed.typ == ArrayType {
				arr := packed.AsArray()
				vals := arr.Values()
				strict := vm.argStrict()
				changed := false
				for j, v := range vals {
					arg := deref(v)
					if ok, out := vm.coerceReturnMode(vt, arg, strict); ok {
						if out != v {
							vals[j] = out
							changed = true
						}
					} else {
						// Zend не печатает имя параметра для variadic-хвоста.
						name := ""
						file := vm.curFileAt()
						if file == "" {
							file = "php"
						}
						line := vm.line()
						if line <= 0 {
							line = 1
						}
						if vm.argNoLoc > 0 {
							file = ""
						}
						th := vm.argTypeError(argFuncDisplayName(fn), fixed+j+1, name, typeDisplay(resolveTypeScope(vt, fn.Scope)), valueTypeName(vm, arg), file, line)
						th.Trace = vm.captureExceptionTrace()
						th.LocFile = fn.DefFile
						th.LocLine = fn.DefLine
						if th.LocLine > 0 {
							th.DefinedIn = vm.argNoLoc == 0
						}
						return args, th
					}
				}
				if changed {
					na := NewArray()
					for _, v := range vals {
						na.Append(v)
					}
					args[fixed] = MakeArray(na)
				}
			}
		}
	}
	return args, nil
}

// wbSkipEntry — запись в стеке vm.wbSkip: свойство ob::$name было прочитано
// через __get для записи (дим-запись/косвенная модификация). real — значение
// пришло из by-ref __get (запись в свойство нужна), иначе запись-обратно
// отменяется (Zend: «Indirect modification ... has no effect»).
type wbSkipEntry struct {
	ob   *Object
	name string
	real bool
}

// pushWbSkip регистрирует отмену записи-обратно для ob::$name.
func (vm *VM) pushWbSkip(ob *Object, name string, real bool) {
	if ob == nil {
		return
	}
	vm.wbSkip = append(vm.wbSkip, wbSkipEntry{ob: ob, name: name, real: real})
}

// accessError формирует PHP-совместимую ошибку доступа к непубличному методу.
func (vm *VM) accessError(decl ClassInfo, mname string, vis Visibility) error {
	kind := "private"
	if vis == Protected {
		kind = "protected"
	}
	cls := ""
	if decl != nil {
		cls = decl.Name()
	}
	scope := "global scope"
	if vm.ctx.self != nil {
		scope = "scope " + vm.ctx.self.Name()
	}
	return NewThrowable("Error", "Call to "+kind+" method "+cls+"::"+mname+"() from "+scope)
}

// ctorAccessError — сообщение о недоступном конструкторе/деструкторе/клон-
// методе (Zend печатает без слова "method").
func (vm *VM) ctorAccessError(vis Visibility, clsName, method string) error {
	return NewThrowable("Error", "Call to "+visWord(vis)+" "+clsName+"::"+method+"() from "+scopeDesc(vm.ctx.self))
}

// frameCalleeName — имя кадра стека с учётом objMethod: замыкание,
// привязанное к объекту без scope, Zend рендерит как Closure->{closure}().
func frameCalleeName(fr callFrameInfo) string {
	if fr.callee == nil {
		return ""
	}
	name := funcDisplayName(fr.callee)
	if fr.objMethod {
		if fr.callee.Scope == nil && strings.HasPrefix(name, "{closure}") {
			return "Closure->" + name
		}
		name = strings.Replace(name, "::", "->", 1)
	}
	return name
}

// funcDisplayName рендерит имя функции для трейсов (Scope::Name).
func funcDisplayName(fn *Function) string {
	if fn.DisplayName != "" {
		return fn.DisplayName
	}
	if fn.Scope != nil {
		return fn.Scope.Name() + "::" + fn.Name
	}
	return fn.Name
}

// typeDisplay рендерит тип в каноническом виде Zend для диагностик.
func typeDisplay(t string) string {
	return sanitizeAnonType(TypeString(t))
}

// sanitizeAnonType заменяет внутренние имена анонимных классов
// (`\x00anon#N`) на `class@anonymous` в строках типов.
func sanitizeAnonType(t string) string {
	if !strings.Contains(t, "\x00anon#") {
		return t
	}
	var b strings.Builder
	for i := 0; i < len(t); {
		if strings.HasPrefix(t[i:], "\x00anon#") {
			j := i + 7
			for j < len(t) && t[j] >= '0' && t[j] <= '9' {
				j++
			}
			b.WriteString("class@anonymous")
			i = j
			continue
		}
		b.WriteByte(t[i])
		i++
	}
	return b.String()
}

// argFuncDisplayName возвращает имя функции для диагностик аргументов.
// Для методов анонимных классов Zend печатает `class@anonymous` (ArgTypeError
// добавит `()`).
func argFuncDisplayName(fn *Function) string {
	if fn.Scope != nil && strings.HasPrefix(fn.Scope.Name(), "\x00anon#") {
		return ClassDisplayName(fn.Scope.Name())
	}
	return funcDisplayName(fn)
}

// displayTypeResolved рендерит тип, заменяя self/static/parent на имена
// соответствующих классов (как в диагностиках Zend).
func (vm *VM) displayTypeResolved(t string) string {
	low := strings.ToLower(t)
	if !strings.Contains(low, "static") && !strings.Contains(low, "self") && !strings.Contains(low, "parent") {
		return typeDisplay(t)
	}
	var b strings.Builder
	i := 0
	for i < len(t) {
		c := t[i]
		if isTypeNameByte(c) {
			j := i
			for j < len(t) && isTypeNameByte(t[j]) {
				j++
			}
			word := t[i:j]
			switch strings.ToLower(word) {
			case "self", "static", "parent":
				if ci := vm.resolveTypeClass(word); ci != nil {
					b.WriteString(ci.Name())
				} else {
					b.WriteString(word)
				}
			default:
				b.WriteString(word)
			}
			i = j
			continue
		}
		b.WriteByte(c)
		i++
	}
	return typeDisplay(b.String())
}

func isTypeNameByte(c byte) bool {
	return c == '_' || c == '\\' || c == '-' ||
		('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
}

// resolveTypeScope заменяет self/static/parent в типе на имена классов scope.
func resolveTypeScope(t string, scope ClassInfo) string {
	if scope == nil {
		return t
	}
	low := strings.ToLower(t)
	if !strings.Contains(low, "static") && !strings.Contains(low, "self") && !strings.Contains(low, "parent") {
		return t
	}
	var parent ClassInfo
	if scope.Parent() != nil {
		parent = scope.Parent()
	}
	var b strings.Builder
	i := 0
	for i < len(t) {
		c := t[i]
		if isTypeNameByte(c) {
			j := i
			for j < len(t) && isTypeNameByte(t[j]) {
				j++
			}
			word := t[i:j]
			switch strings.ToLower(word) {
			case "self", "static":
				b.WriteString(scope.Name())
			case "parent":
				if parent != nil {
					b.WriteString(parent.Name())
				} else {
					b.WriteString(word)
				}
			default:
				b.WriteString(word)
			}
			i = j
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// checkReturnType проверяет и при необходимости приводит значение к
// объявленному типу возврата (слабая типизация, как PHP).
// RefReturnTypeOk сообщает, что значение v подходит под объявленный тип
// возврата t (для условного notice о возврате не-ссылки из by-ref функции).
func (vm *VM) RefReturnTypeOk(t string, strict bool, v Slot) bool {
	if t == "" || t == "void" || t == "never" {
		return true
	}
	chk := v
	if chk.typ == RefType {
		chk = deref(chk)
	}
	ok, _ := vm.coerceReturnMode(t, chk, strict)
	return ok
}

func (vm *VM) checkReturnType(fn *Function, res Slot) (Slot, error) {
	t := fn.RetType
	if t == "" {
		return res, nil
	}
	if _, ok := fn.Executable.(*NativeFunction); ok {
		return res, nil
	}
	if cf, ok := fn.Executable.(*CompiledFunction); ok && cf.isGenerator {
		return res, nil
	}
	name := funcDisplayName(fn)
	loc := func(th *Throwable) *Throwable {
		th.Trace = vm.captureExceptionTrace()
		if fn.DefFile != "" {
			th.LocFile = fn.DefFile
		}
		if vm.retLine > 0 {
			th.LocLine = vm.retLine
		}
		return th
	}
	if t == "never" {
		if vm.fatalDiag {
			return res, nil
		}
		var msg string
		if vm.retImplicit {
			msg = name + "(): never-returning function must not implicitly return"
		} else {
			msg = name + "(): never-returning function must not return"
		}
		th := NewThrowable("TypeError", msg)
		th.Trace = vm.captureExceptionTrace()
		return res, th
	}
	if t == "void" {
		// Неявный/явный return без значения корректен; значение компилятор
		// отсекает отдельно.
		if res.typ == NullType {
			return res, nil
		}
		return res, nil
	}
	// Функция с объявленным типом возврата, завершившаяся без return.
	if vm.retImplicit {
		th := vm.returnTypeError(name, vm.displayTypeResolved(t), "none")
		return res, loc(th)
	}
	// Значение по ссылке проверяется по типу по внутреннему значению (Zend
	// разыменовывает zval при проверке типа возврата).
	chk := res
	if chk.typ == RefType {
		chk = deref(chk)
	}
	if ok, out := vm.coerceReturnMode(t, chk, fnStrict(fn)); ok {
		// PHP 8.1+: возврат float/числовой строки из функции с типом int —
		// Deprecated.
		if out.typ == IntType && (chk.typ == FloatType || chk.typ == StringType) {
			line := vm.retLine
			if line <= 0 {
				line = fn.DefLine
			}
			vm.deprecateFloatToInt(line, chk)
		}
		if fn.ByRefReturn {
			// Zend проверяет тип по значению ссылки, а приведение пишет в
			// саму ячейку (bug70117: `&foo(): string` возвращает "1", и
			// исходный элемент массива тоже становится строкой). Сама ссылка
			// возвращается как ссылка — иначе `++$o['x']` инкрементировал бы
			// копию (bug64417).
			if res.typ == RefType {
				if r := res.Ref(); r != nil {
					r.Set(out)
				}
			}
			return res, nil
		}
		return out, nil
	}
	kind := valueTypeName(vm, chk)
	if vm.retImplicit {
		kind = "none"
	}
	th := vm.returnTypeError(name, vm.displayTypeResolved(t), kind)
	return res, loc(th)
}

// AddProvisionalClosure регистрирует замыкание, созданное встроенной функцией,
// как временное: если в конце оператора на него нет ссылок, хендл освободится.
func (vm *VM) AddProvisionalClosure(cl *Closure) {
	if cl == nil {
		return
	}
	vm.markClosure(cl)
	vm.provisionalClosures = append(vm.provisionalClosures, cl)
}

// releaseProvisionalClosures освобождает хендлы временных замыканий, на
// которые не осталось ссылок в регистрах/глобалах/аргументах кадров.
func (vm *VM) releaseProvisionalClosures() {
	live := vm.provisionalClosures[:0]
	for _, cl := range vm.provisionalClosures {
		if cl == nil || cl.objID == 0 {
			continue
		}
		if vm.closureReferenced(cl) {
			live = append(live, cl)
			continue
		}
		if cl.objID == vm.nextObjID {
			vm.nextObjID--
		} else {
			vm.freeObjIDs = append(vm.freeObjIDs, cl.objID)
		}
		cl.objID = 0
	}
	vm.provisionalClosures = live
}

// closureReferenced ищет ссылку на замыкание в живых регистрах, глобалах и
// аргументах кадров (мелкий обход ссылок и массивов).
func (vm *VM) closureReferenced(cl *Closure) bool {
	check := func(s Slot) bool { return slotHasClosure(s, cl, 0) }
	for i := 0; i <= vm.fp && i < len(vm.frames); i++ {
		for _, s := range vm.frames[i].regs {
			if check(s) {
				return true
			}
		}
	}
	for _, s := range vm.globals {
		if check(s) {
			return true
		}
	}
	for i := range vm.callStack {
		for _, a := range vm.callStack[i].args {
			if check(a) {
				return true
			}
		}
	}
	return false
}

func slotHasClosure(s Slot, cl *Closure, depth int) bool {
	if depth > 2 {
		return false
	}
	for i := 0; s.typ == RefType && i < 10; i++ {
		r := s.Ref()
		if r == nil {
			return false
		}
		s = r.v
	}
	switch s.typ {
	case ClosureType:
		return s.asClosure() == cl
	case ArrayType:
		if a := s.AsArray(); a != nil {
			for _, v := range a.All() {
				if slotHasClosure(v, cl, depth+1) {
					return true
				}
			}
		}
	}
	return false
}

// VerifyReturnAtPointBuiltin — приведение значения к объявленному типу
// возврата в точке return (до раскрутки finally, как Zend VERIFY_RETURN).
// Регистрируется как __php_verify_return.
func VerifyReturnAtPointBuiltin(ctx *Context) Slot {
	vm := ctx.vm
	a := ctx.Args()
	if len(a) == 0 {
		return MakeNull()
	}
	// Кадр самой служебной функции может отсутствовать (быстрый прямой вызов):
	// ищем кадр userland-функции и глубину, на которой её проверять.
	n := len(vm.callStack)
	fn := (*Function)(nil)
	setDepth := n
	if n > 0 {
		if c := vm.callStack[n-1].callee; c != nil && c.Name == "__php_verify_return" {
			setDepth = n - 1
			if n >= 2 {
				fn = vm.callStack[n-2].callee
			}
		} else {
			fn = vm.callStack[n-1].callee
		}
	}
	if fn == nil || fn.RetType == "" {
		return a[0]
	}
	if l := vm.line(); l > 0 {
		vm.retLine = l
	}
	// Это явный return: retImplicit принадлежит другому (предыдущему) кадру
	// и не должен попасть в диагностику (bug72347: «none returned»).
	savedImplicit := vm.retImplicit
	vm.retImplicit = false
	out, err := vm.checkReturnType(fn, a[0])
	vm.retImplicit = savedImplicit
	if err != nil {
		if th, ok := err.(*Throwable); ok {
			ctx.Throw(th)
			return Slot{}
		}
		ctx.Throw(NewThrowable("Error", err.Error()))
		return Slot{}
	}
	vm.retVerifiedDepth = setDepth
	return out
}

// fnStrict сообщает, что функция объявлена в strict_types-файле.
func fnStrict(fn *Function) bool {
	if cf, ok := fn.Executable.(*CompiledFunction); ok && cf.chunk != nil {
		return cf.chunk.Strict
	}
	return false
}

// valueTypeName — PHP-имя типа значения для сообщений.
func valueTypeName(vm *VM, s Slot) string {
	switch s.typ {
	case NullType:
		return "null"
	case BoolType:
		if s.ToBool() {
			return "true"
		}
		return "false"
	case IntType:
		return "int"
	case FloatType:
		return "float"
	case StringType:
		return "string"
	case ArrayType:
		return "array"
	case ObjectType:
		if ci := vm.classInfo(s); ci != nil {
			return ClassDisplayName(ci.Name())
		}
		return "object"
	case ClosureType:
		return "Closure"
	case ResourceType:
		return "resource"
	}
	return s.typ.String()
}

// tryToString вызывает __toString у объекта для приведения к строке.
func (vm *VM) tryToString(s Slot) (string, bool) {
	ci := vm.classInfo(s)
	if ci == nil {
		return "", false
	}
	m := ci.lookupMethod("__toString")
	if m == nil {
		return "", false
	}
	res, err := vm.invokeMethod(m, s, ci, nil)
	if err != nil {
		return "", false
	}
	return deref(res).ToString(), true
}

// coerceReturnMode — coerceReturn с учётом режима strict_types: в строгом
// режиме разрешено только точное совпадение типа и расширение int->float.
func (vm *VM) coerceReturnMode(t string, s Slot, strict bool) (bool, Slot) {
	return vm.coerceReturnModeEx(t, s, strict, true)
}

// coerceReturnModeEx — как coerceReturnMode, но с управлением приведением
// Stringable-объектов к строке.
func (vm *VM) coerceReturnModeEx(t string, s Slot, strict, stringable bool) (bool, Slot) {
	if !strict {
		return vm.coerceReturnEx(t, s, stringable)
	}
	if t == "" || t == "mixed" {
		return true, s
	}
	if strings.HasPrefix(t, "?") {
		if s.typ == NullType {
			return true, s
		}
		return vm.coerceReturnModeEx(t[1:], s, true, stringable)
	}
	if topLevelSplit(t, '|') >= 0 {
		for _, alt := range splitTopLevelAll(t, '|') {
			if ok, out := vm.coerceReturnModeEx(alt, s, true, stringable); ok {
				return true, out
			}
		}
		return false, s
	}
	if vm.typeAccepts(t, s) {
		if t == "float" && s.typ == IntType {
			return true, MakeFloat(float64(s.ToInt()))
		}
		return true, s
	}
	return false, s
}

// coerceReturn приводит значение к объявленному типу возврата в слабом режиме.
// Возвращает (true, значение) при успехе.
func (vm *VM) coerceReturn(t string, s Slot) (bool, Slot) {
	return vm.coerceReturnEx(t, s, true)
}

// coerceReturnEx — как coerceReturn, но позволяет отключить приведение
// Stringable-объектов к строке (типизированные константы класса его не делают).
func (vm *VM) coerceReturnEx(t string, s Slot, stringable bool) (bool, Slot) {
	// Точное совпадение (без приведения) либо приведение.
	if vm.typeAccepts(t, s) {
		// int, возвращаемый для float, расширяется.
		if t == "float" && s.typ == IntType {
			return true, MakeFloat(float64(s.ToInt()))
		}
		// Union, содержащий float, но не int (`float|array`, ...): int
		// расширяется до float (Zend).
		if s.typ == IntType && vm.floatOnlyUnion(t) {
			return true, MakeFloat(float64(s.ToInt()))
		}
		return true, s
	}
	// Nullable: приводим к внутреннему типу.
	if strings.HasPrefix(t, "?") {
		return vm.coerceReturnEx(t[1:], s, stringable)
	}
	// Union: пробуем каждую альтернативу.
	if topLevelSplit(t, '|') >= 0 {
		alts := splitTopLevelAll(t, '|')
		// Числовая строка для `int|float`: целая -> int, дробная -> float,
		// независимо от порядка альтернатив (Zend).
		if s.typ == StringType && IsNumericString(s.StringVal()) &&
			containsName(alts, "int") && containsName(alts, "float") {
			if i, isInt, ok := parseLeadingNumericInt(s.StringVal()); ok && isInt {
				return true, MakeInt(i)
			}
			return vm.coerceReturnEx("float", s, stringable)
		}
		for _, alt := range alts {
			if ok, out := vm.coerceReturnEx(alt, s, stringable); ok {
				return true, out
			}
		}
		return false, s
	}
	// Приведение скаляров (только для простого не-union типа).
	if s.typ == NullType {
		return false, s
	}
	// Массивы, объекты (без __toString для string) и ресурсы не приводятся к
	// скалярным типам в слабом режиме — Zend бросает TypeError.
	if s.typ == ArrayType || s.typ == ResourceType {
		return false, s
	}
	if s.typ == ObjectType {
		if t != "string" || !stringable {
			return false, s
		}
	}
	switch t {
	case "bool":
		return true, MakeBool(s.ToBool())
	case "int":
		if coerced, ok := coerceStringToInt(s); ok {
			return true, coerced
		}
	case "float":
		if s.typ == BoolType || s.typ == IntType {
			return true, MakeFloat(s.ToFloat())
		}
		if s.typ == StringType && IsNumericString(s.StringVal()) {
			f, _, _ := parseLeadingNumeric(s.StringVal())
			return true, MakeFloat(f)
		}
	case "string":
		if s.typ == IntType || s.typ == FloatType || s.typ == BoolType {
			return true, MakeString(s.ToString())
		}
		if stringable && s.typ == ObjectType {
			if str, ok := vm.tryToString(s); ok {
				return true, MakeString(str)
			}
		}
	}
	return false, s
}

// floatOnlyUnion сообщает, что тип (возможно nullable/union) содержит float,
// но не int.
func (vm *VM) floatOnlyUnion(t string) bool {
	if strings.HasPrefix(t, "?") {
		t = t[1:]
	}
	if topLevelSplit(t, '|') < 0 {
		return false
	}
	alts := splitTopLevelAll(t, '|')
	return containsName(alts, "float") && !containsName(alts, "int")
}

// containsName сообщает, есть ли среди альтернатив ровно name.
func containsName(alts []string, name string) bool {
	for _, a := range alts {
		if a == name {
			return true
		}
	}
	return false
}

// coerceStringToInt приводит float/bool/числовую строку к int. Как в Zend,
// float/float-строки вне диапазона int64 (и NaN) не приводятся — вызывающий
// выдаёт TypeError.
func coerceStringToInt(s Slot) (Slot, bool) {
	switch s.typ {
	case FloatType:
		f := s.ToFloat()
		if math.IsNaN(f) || f >= twoTo63 || f < -twoTo63 {
			return s, false
		}
		return MakeInt(int64(f)), true
	case BoolType:
		return MakeInt(s.ToInt()), true
	case StringType:
		if IsNumericString(s.StringVal()) {
			if i, isInt, ok := parseLeadingNumericInt(s.StringVal()); ok {
				if isInt {
					return MakeInt(i), true
				}
				f, _, _ := parseLeadingNumeric(s.StringVal())
				if math.IsNaN(f) || f >= twoTo63 || f < -twoTo63 {
					return s, false
				}
				return MakeInt(int64(math.Trunc(f))), true
			}
		}
	}
	return s, false
}

// typeAccepts проверяет, соответствует ли значение типу t (строго, но int
// принимается для float; nullable/union/intersection/DNF учитываются).
func (vm *VM) typeAccepts(t string, s Slot) bool {
	if t == "" || t == "mixed" {
		return true
	}
	if strings.HasPrefix(t, "?") {
		if s.typ == NullType {
			return true
		}
		return vm.typeAccepts(t[1:], s)
	}
	if t == "null" {
		return s.typ == NullType
	}
	if t == "false" {
		return s.typ == BoolType && !s.ToBool()
	}
	if t == "true" {
		return s.typ == BoolType && s.ToBool()
	}
	if i := topLevelSplit(t, '|'); i >= 0 {
		return vm.typeAccepts(t[:i], s) || vm.typeAccepts(t[i+1:], s)
	}
	if strings.HasPrefix(t, "(") {
		inner := strings.TrimSuffix(strings.TrimPrefix(t, "("), ")")
		// DNF-группа: (A&B)|C уже разбита по |; здесь — пересечение.
		parts := splitTopLevelAll(inner, '&')
		for _, p := range parts {
			if !vm.typeAccepts(p, s) {
				return false
			}
		}
		return true
	}
	if i := topLevelSplit(t, '&'); i >= 0 {
		return vm.typeAccepts(t[:i], s) && vm.typeAccepts(t[i+1:], s)
	}
	switch t {
	case "bool":
		return s.typ == BoolType
	case "int":
		return s.typ == IntType
	case "float":
		return s.typ == FloatType || s.typ == IntType
	case "string":
		return s.typ == StringType
	case "array":
		return s.typ == ArrayType
	case "object":
		return s.typ == ObjectType || s.typ == ClosureType
	case "iterable":
		if s.typ == ArrayType {
			return true
		}
		if s.typ == ObjectType {
			tr := vm.findClass("Traversable")
			if ci := vm.classInfo(s); ci != nil && tr != nil {
				return ci.isA(tr)
			}
		}
		return false
	case "callable":
		return vm.IsCallable(s)
	case "void":
		return s.typ == NullType
	case "never":
		return false
	}
	// имя класса
	if s.typ == ClosureType {
		// Замыкания — объекты класса Closure (Zend).
		if target := vm.resolveTypeClass(t); target != nil {
			return strings.EqualFold(target.Name(), "Closure")
		}
		return false
	}
	if s.typ != ObjectType {
		return false
	}
	ci := vm.classInfo(s)
	if ci == nil {
		return false
	}
	target := vm.resolveTypeClass(t)
	if target == nil {
		return false
	}
	return ci.isA(target)
}

// resolveTypeClass приводит self/static/parent к текущему scope.
func (vm *VM) resolveTypeClass(name string) ClassInfo {
	switch strings.ToLower(name) {
	case "self":
		return vm.ctx.self
	case "static":
		if vm.ctx.called != nil {
			return vm.ctx.called
		}
		return vm.ctx.self
	case "parent":
		if vm.ctx.self != nil {
			return vm.ctx.self.Parent()
		}
		return nil
	}
	// Тип мог быть записан с ведущим `\` (особенно в анонимных классах,
	// где имя не нормализуется на этапе компиляции).
	return vm.findClass(strings.TrimLeft(name, "\\"))
}

// topLevelSplit возвращает индекс первого sep на верхнем уровне вложенности.
func topLevelSplit(s string, sep byte) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case sep:
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func splitTopLevelAll(s string, sep byte) []string {
	var out []string
	for {
		i := topLevelSplit(s, sep)
		if i < 0 {
			return append(out, s)
		}
		out = append(out, s[:i])
		s = s[i+1:]
	}
}

// invokeClosure вызывает замыкание: аргументы вызова идут в регистры
// параметров, захваченные значения — следом за ними. $this/self берутся из
// замыкания (bind при создании), а не из места вызова.
func (vm *VM) invokeClosure(cl *Closure, args []Slot) (res Slot, err error) {
	namedCount := vm.pendingNamedCount
	vm.pendingNamedCount = -1
	provided := vm.pendingNativeProvided
	vm.pendingNativeProvided = -1
	pmask, pmaskSet := vm.takeProvidedMask()
	extras := vm.pendingFrameNamed
	vm.pendingFrameNamed = nil
	display := args
	if provided >= 0 && provided <= len(args) {
		display = args[:provided]
	}
	fn := cl.Fn
	if fn == nil {
		// Fake-замыкание через __call/__callStatic (Closure::fromCallable).
		if cl.MagicName != "" {
			if ci := vm.classInfo(cl.BoundThis); ci != nil {
				if res, ok, merr := vm.callMagicMethod(ci, cl.BoundThis, "__call",
					[]Slot{MakeString(cl.MagicName), argsArray(args)}); ok {
					return res, merr
				}
			}
			if cl.Scope != nil {
				called := cl.Called
				if called == nil {
					called = cl.Scope
				}
				if res, ok, merr := vm.callMagicMethodCalled(cl.Scope, called, Slot{}, "__callStatic",
					[]Slot{MakeString(cl.MagicName), argsArray(args)}); ok {
					return res, merr
				}
			}
		}
		return Slot{}, NewThrowable("Error", "Closure is not valid")
	}
	if cl.Scope != nil && fn.Scope == nil {
		// Диагностики/трейсы замыкания внутри класса называют scope:
		// foo::{closure}() в сообщении и foo->{closure}() в кадре (Zend).
		cp := *fn
		cp.Scope = cl.Scope
		fn = &cp
	}
	if aerr := vm.checkArityN(fn, args, provided); aerr != nil {
		return Slot{}, aerr
	}
	// Неявные захваты стрелочной функции, не определённые в точке создания:
	// Zend печатает "Undefined variable $x" при чтении в теле (bug81076,
	// arrow_functions/002) — у нас при входе в замыкание.
	for i, undef := range cl.capUndef {
		if !undef || i >= len(fn.CaptureNames) {
			continue
		}
		n := strings.TrimPrefix(fn.CaptureNames[i], "$")
		if n == "" {
			continue
		}
		vm.Warning("Undefined variable $" + n)
	}
	if len(fn.ByRefArgs) > 0 {
		vm.warnByRefArgs(fn, args)
	}
	cf, isCompiled := fn.Executable.(*CompiledFunction)
	var env funcEnv
	haveEnv := false
	if isCompiled {
		env = vm.enterFuncEnv(cf, namedCount)
		haveEnv = true
	}
	var base []Slot
	var full []Slot
	if isCompiled && !cf.variadic && len(cl.Captures) == 0 && !fn.HasParamCoercion() && fn.NumArgs >= 0 && len(args) >= fn.NumArgs {
		// Быстрый путь: параметры кладутся в кадр прямо из аргументов вызова
		// (без промежуточных срезов); типов нет — приводить нечего.
		base = args[:fn.NumArgs]
		full = base
	} else {
		if isCompiled && cf.variadic {
			base = cf.PackVariadic(args)
			vm.mergeNamedVariadic(base, fn)
		} else if !isCompiled {
			// Нативные функции получают аргументы как есть: у builtins NumArgs=-1.
			base = args
		} else {
			n := fn.NumArgs
			if n < 0 {
				n = 0
			}
			base = make([]Slot, n)
			copy(base, args)
		}
		full = make([]Slot, len(base)+len(cl.Captures))
		copy(full, base)
		copy(full[len(base):], cl.Captures)
	}
	savedThis, savedSelf, savedCalled := vm.ctx.this, vm.ctx.self, vm.ctx.called
	savedPending := vm.pendingThis
	savedSuppress := vm.suppress
	vm.ctx.this = cl.BoundThis
	vm.ctx.self = cl.Scope
	if cl.Called != nil {
		vm.ctx.called = cl.Called
	} else {
		vm.ctx.called = cl.Scope
	}
	vm.pendingThis = cl.BoundThis
	vm.pendingFrameNamed = extras
	vm.pushCall(fn, display)
	vm.pendingFrameNamed = nil
	if deref(cl.BoundThis).typ == ObjectType {
		// Замыкание, привязанное к объекту: кадр трейса — foo->{closure}().
		vm.markTopObjMethod()
		vm.setTopThis(cl.BoundThis)
	}
	// Именованные аргументы раскладываются по позициям с null-заглушками
	// отсутствующих параметров: считать их «переданными явно» нельзя
	// (gh19653_1: func1(a3: 'm3+') с дефолтными a1/a2).
	paramProvided := len(args)
	if namedCount >= 0 {
		paramProvided = -1
	}
	if ck, perr := vm.checkParamsN(fn, full, paramProvided, pmask, pmaskSet); perr != nil {
		err = perr
	} else {
		full = ck
		// func_get_args()/func_get_arg() внутри замыкания должны видеть исходные
		// переданные аргументы (без захваченных `use`-переменных и без усечения).
		if isCompiled && !cf.IsGenerator() {
			vm.pendingFuncArgs = args
		}
		res, err = fn.Executable.Call(vm, &vm.ctx, full)
		if err == nil {
			if fn.RetType != "" {
				if vm.retVerifiedDepth == len(vm.callStack) {
					// Тип уже проверен в точке return (__php_verify_return).
					vm.retVerifiedDepth = 0
				} else {
					res, err = vm.checkReturnType(fn, res)
				}
			}
		}
	}
	vm.popCall()
	vm.ctx.this, vm.ctx.self, vm.ctx.called = savedThis, savedSelf, savedCalled
	vm.pendingThis = savedPending
	vm.suppress = savedSuppress
	if haveEnv {
		vm.restoreFuncEnv(env)
	}
	return res, err
}

// invokeMethod вызывает метод объекта: $this = recv, self = класс объявления,
// static = класс получателя (late static binding).
func (vm *VM) invokeMethod(fn *Function, recv Slot, called ClassInfo, args []Slot) (res Slot, err error) {
	// Статический метод не получает $this, даже если вызван через объект
	// (`$obj->staticMethod()`, [obj,'staticMethod']): Zend выдаёт «Using $this
	// when not in object context» при обращении (bug77877).
	if fn.IsStatic {
		recv = Slot{}
	}
	namedCount := vm.pendingNamedCount
	vm.pendingNamedCount = -1
	provided := vm.pendingNativeProvided
	vm.pendingNativeProvided = -1
	pmask, pmaskSet := vm.takeProvidedMask()
	extras := vm.pendingFrameNamed
	vm.pendingFrameNamed = nil
	display := args
	if provided >= 0 && provided <= len(args) {
		display = args[:provided]
	}
	if aerr := vm.checkArityN(fn, args, provided); aerr != nil {
		return Slot{}, aerr
	}
	if len(fn.ByRefArgs) > 0 {
		vm.warnByRefArgs(fn, args)
	}
	savedThis, savedSelf, savedCalled := vm.ctx.this, vm.ctx.self, vm.ctx.called
	savedPending := vm.pendingThis
	savedSuppress := vm.suppress
	// outerSelf — scope вызывающего userland-кадра для нативных методов
	// (zend_get_executed_scope пропускает internal-кадры).
	savedOuterSelf, savedOuterCalled := vm.outerSelf, vm.outerCalled
	if _, isNative := fn.Executable.(*NativeFunction); isNative {
		vm.outerSelf = savedSelf
		vm.outerCalled = savedCalled
	}
	vm.ctx.this = recv
	vm.ctx.self = fn.Scope
	vm.ctx.called = called
	vm.pendingThis = recv
	var env funcEnv
	haveEnv := false
	rawArgs := display
	if cf, ok := fn.Executable.(*CompiledFunction); ok {
		env = vm.enterFuncEnv(cf, namedCount)
		haveEnv = true
		args = cf.PackVariadic(args)
		vm.mergeNamedVariadic(args, fn)
	}
	traced := true
	if vm.suppressNextFrame {
		traced = false
		vm.suppressNextFrame = false
	}
	pushed := false
	if traced {
		vm.pendingFrameNamed = extras
		vm.pushCall(fn, rawArgs)
		vm.pendingFrameNamed = nil
		if deref(recv).typ == ObjectType {
			vm.markTopObjMethod()
			vm.setTopThis(recv)
		}
		pushed = true
	}
	if extras != nil {
		if _, isNative := fn.Executable.(*NativeFunction); isNative && !fn.AcceptsExtraNamed {
			if pushed {
				vm.popCall()
			}
			if haveEnv {
				vm.restoreFuncEnv(env)
			}
			return Slot{}, vm.extraNamedNativeErr(fn)
		}
	}
	if ck, perr := vm.checkParamsN(fn, args, len(args), pmask, pmaskSet); perr != nil {
		err = perr
	} else {
		args = ck
		res, err = fn.Executable.Call(vm, &vm.ctx, args)
		if err == nil {
			if fn.RetType != "" {
				if vm.retVerifiedDepth == len(vm.callStack) {
					// Тип уже проверен в точке return (__php_verify_return).
					vm.retVerifiedDepth = 0
				} else {
					res, err = vm.checkReturnType(fn, res)
				}
			}
		}
	}
	if pushed {
		vm.popCall()
	}
	if haveEnv {
		vm.restoreFuncEnv(env)
	}
	vm.ctx.this, vm.ctx.self, vm.ctx.called = savedThis, savedSelf, savedCalled
	vm.pendingThis = savedPending
	vm.suppress = savedSuppress
	vm.outerSelf, vm.outerCalled = savedOuterSelf, savedOuterCalled
	return res, err
}

// staticCallRecv возвращает значение, передаваемое как $this при статическом
// вызове `Class::method()`. Для «scoped»-вызовов (self/parent/static) текущий
// $this является экземпляром cls — его и передаём. Для явного вызова чужого
// класса $this не передаётся (null): иначе метод получил бы чужой объект, что
// приводит к небезопасному приведению (напр. ArrayIterator::current() из
// метода другого класса — Bug #46381).
func (vm *VM) staticCallRecv(cls ClassInfo) Slot {
	if cls == nil {
		return Slot{}
	}
	if rci := vm.classInfo(vm.cur.this); rci != nil && rci.isA(cls) {
		return vm.cur.this
	}
	return Slot{}
}

// checkStaticCall запрещает вызов нестатического метода статически, если нет
// активного $this (PHP 8: "Non-static method C::m() cannot be called statically").
func (vm *VM) checkStaticCall(m *Function, cls ClassInfo) error {
	if m == nil || m.IsStatic || cls == nil {
		return nil
	}
	if deref(vm.staticCallRecv(cls)).typ == ObjectType {
		return nil
	}
	return NewThrowable("Error", "Non-static method "+cls.Name()+"::"+m.Name+"() cannot be called statically")
}

// DynClassByName резолвит динамическую ссылку на класс (имя-строка или объект)
// в рантайме. Для неизвестного имени бросает Error "Class \"X\" not found".
func (vm *VM) DynClassByName(s Slot) (ClassInfo, error) {
	s = deref(s)
	if s.typ == ObjectType {
		if ci := vm.classInfo(s); ci != nil {
			return ci, nil
		}
		return nil, NewThrowable("Error", "Class not found")
	}
	if s.typ != StringType {
		return nil, NewThrowable("Error", "Class name must be a valid object or a string")
	}
	name := strings.TrimLeft(s.ToString(), "\\")
	if name == "" {
		return nil, NewThrowable("Error", "Class \"\" not found")
	}
	if ci := vm.FindClass(name); ci != nil {
		return ci, nil
	}
	if _, err := vm.Autoload(&vm.ctx, name); err != nil {
		return nil, err
	}
	if ci := vm.FindClass(name); ci != nil {
		return ci, nil
	}
	return nil, NewThrowable("Error", "Class \""+name+"\" not found")
}

// DynStaticGet читает статическое свойство класса, заданного динамически.
func (vm *VM) DynStaticGet(cls Slot, name string) (Slot, error) {
	ci, err := vm.DynClassByName(cls)
	if err != nil {
		return Slot{}, err
	}
	if s := ci.lookupStatic(name); s != nil {
		if ci.staticIsUninit(name) {
			return Slot{}, NewThrowable("Error", "Typed static property "+ci.Name()+"::$"+name+" must not be accessed before initialization")
		}
		return *s, nil
	}
	return Slot{}, NewThrowable("Error", "Access to undeclared static property "+ci.Name()+"::$"+name)
}

// DynStaticSetByName — запись в статическое свойство класса, заданного
// значением (для `EXPR::$p = v`). Возвращает записанное значение.
func (vm *VM) DynStaticSetByName(cls Slot, name string, val Slot) (Slot, error) {
	ci, err := vm.DynClassByName(cls)
	if err != nil {
		return Slot{}, err
	}
	if ci.IsTrait() {
		vm.Deprecated("Accessing static trait property " + ci.Name() + "::$" + name + " is deprecated, it should only be accessed on a class using the trait")
	}
	s := ci.lookupStatic(name)
	if s == nil {
		return Slot{}, NewThrowable("Error", "Access to undeclared static property "+ci.Name()+"::$"+name)
	}
	if s.typ == RefType {
		if r := s.Ref(); r != nil {
			strict := vm.chunk != nil && vm.chunk.Strict
			typed := false
			if rc, ok := asRuntimeClass(ci); ok {
				if pd, _ := rc.propDeclOf(name); pd != nil && pd.hasType {
					typed = true
				}
			}
			if !typed {
				if werr := vm.writeRef(r, val, strict); werr != nil {
					return Slot{}, werr
				}
				return r.v, nil
			}
			out, verr := vm.coerceStaticProp(ci, name, deref(val))
			if verr != nil {
				return Slot{}, verr
			}
			r.v = out
			return out, nil
		}
	}
	out, verr := vm.coerceStaticProp(ci, name, deref(val))
	if verr != nil {
		return Slot{}, verr
	}
	*s = out
	ci.clearStaticUninit(name)
	return out, nil
}

// DynStaticIsset проверяет isset(EXPR::$p).
func (vm *VM) DynStaticIsset(cls Slot, name string) (bool, error) {
	ci, err := vm.DynClassByName(cls)
	if err != nil {
		return false, err
	}
	s := ci.lookupStatic(name)
	if s == nil || ci.staticIsUninit(name) {
		return false, nil
	}
	return deref(*s).typ != NullType, nil
}

// DynStaticRef возвращает ячейку-ссылку статического свойства класса,
// заданного значением (для `EXPR::$p =& v` и ++/--).
func (vm *VM) DynStaticRef(cls Slot, name string) (Slot, error) {
	ci, err := vm.DynClassByName(cls)
	if err != nil {
		return Slot{}, err
	}
	s := ci.lookupStatic(name)
	if s == nil {
		return Slot{}, NewThrowable("Error", "Access to undeclared static property "+ci.Name()+"::$"+name)
	}
	if s.typ == RefType {
		return *s, nil
	}
	cell := MakeRef(&Ref{v: *s})
	*s = cell
	return cell, nil
}

// DynClassConst читает константу класса, заданного динамически.
func (vm *VM) DynClassConst(cls Slot, name string) (Slot, error) {
	ci, err := vm.DynClassByName(cls)
	if err != nil {
		return Slot{}, err
	}
	v, ok, cerr := vm.classConstValue(ci, name)
	if cerr != nil {
		return Slot{}, cerr
	}
	if !ok {
		return Slot{}, NewThrowable("Error", "Undefined constant "+ci.Name()+"::"+name)
	}
	return v, nil
}

// DynClassConstValue — как DynClassConst, но принимает имя константы слотом:
// Zend требует строку (иначе «Cannot use value of type X as class constant
// name»), а имя "class" даёт имя класса.
func (vm *VM) DynClassConstValue(cls Slot, nameSlot Slot) (Slot, error) {
	ns := deref(nameSlot)
	if ns.typ != StringType {
		return Slot{}, NewThrowable("Error", "Cannot use value of type "+operandTypeName(ns)+" as class constant name")
	}
	name := ns.StringVal()
	if strings.EqualFold(name, "class") {
		ci, err := vm.DynClassByName(cls)
		if err != nil {
			return Slot{}, err
		}
		return MakeString(ci.Name()), nil
	}
	return vm.DynClassConst(cls, name)
}

// DynStaticCall вызывает статический метод класса, заданного динамически.
func (vm *VM) DynStaticCall(cls Slot, mname string, args []Slot) (Slot, error) {
	ci, err := vm.DynClassByName(cls)
	if err != nil {
		return Slot{}, err
	}
	if ci.IsTrait() {
		vm.Deprecated("Calling static trait method " + ci.Name() + "::" + mname + " is deprecated, it should only be called on a class using the trait")
	}
	m, decl, vis := ci.methodInfo(mname)
	if m == nil && mname == "__construct" {
		if rc, ok := asRuntimeClass(ci); ok {
			c, cd, cv := rc.ctorInfo()
			m, decl, vis = c, cd, cv
		} else {
			decl = ci
			vis = Public
		}
	}
	if m == nil {
		if sres, ok, merr := vm.callMagicUndefStatic(ci, ci, mname, args); ok {
			return sres, merr
		}
		return Slot{}, NewThrowable("Error", "Call to undefined method "+ci.Name()+"::"+mname+"()")
	}
	if vis != Public && !vm.methodAccessibleFromProto(ci, decl, mname, vis, vm.ctx.self) {
		if pm, pdecl, pvis, ok := scopeMethod(vm.ctx.self, mname); ok {
			m, decl, vis = pm, pdecl, pvis
		} else {
			return Slot{}, vm.accessError(decl, mname, vis)
		}
	}
	return vm.invokeMethod(m, vm.staticCallRecv(ci), ci, args)
}

// classInfo возвращает дескриптор класса объектного слота (num = индекс класса).
func (vm *VM) classInfo(s Slot) ClassInfo {

	s = deref(s)
	if s.typ != ObjectType {
		return nil
	}
	i := int(s.num)
	if i < 0 || i >= len(vm.classes) {
		return nil
	}
	return vm.classes[i]
}

// icAt возвращает запись inline-кэша для только что выбранной инструкции
// текущего чанка (vm.ip уже указывает на следующую, поэтому -1).
func (vm *VM) icAt(chunk *Chunk) *icEntry {
	pc := vm.ip - 1
	if pc < 0 || pc >= len(chunk.ic) {
		return nil
	}
	return &chunk.ic[pc]
}

// icProp резолвит объявленное свойство для инструкции с per-pc IC: повторные
// исполнения (горячие циклы) не обходят иерархию классов и не ищут слот.
func (vm *VM) icProp(ci ClassInfo, name string, chunk *Chunk) (*propDecl, ClassInfo, string, int, bool) {
	ic := vm.icAt(chunk)
	if ic != nil && ic.propState == icMono && ic.propClass == ci && ic.propName == name && ic.propScope == vm.ctx.self {
		return ic.propPd, ic.propDecl, ic.propKey, ic.propIdx, ic.propOk
	}
	pd, decl, key, idx, ok := vm.scopedSlot(ci, name)
	if ic != nil {
		switch ic.propState {
		case icEmpty:
			ic.propState = icMono
		case icMono:
			ic.propMiss++
			if ic.propMiss >= icMaxMiss {
				// Полиморфный сайт: IC не помогает, отключаем (и снимаем
				// закэшированный признак доступа — он относился к другому
				// ключу).
				ic.propState = icDisabled
				ic.propAccess = false
				return pd, decl, key, idx, ok
			}
		case icDisabled:
			return pd, decl, key, idx, ok
		}
		ic.propClass, ic.propScope, ic.propName = ci, vm.ctx.self, name
		ic.propPd, ic.propDecl, ic.propKey, ic.propIdx, ic.propOk = pd, decl, key, idx, ok
		ic.propAccess = true
		if ok && pd != nil && pd.vis != Public && vm.propAccessErr(pd, decl, name) != nil {
			ic.propAccess = false
		}
	}
	return pd, decl, key, idx, ok
}

// icPropAccess сообщает, доступно ли из scope записи IC свойство (доступ
// считается при заполнении IC: scope — часть ключа).
func (vm *VM) icPropAccess(ic *icEntry) bool { return ic == nil || ic.propAccess }

// icMaxMiss — сколько смен ключа допускает мономорфный кэш до отключения.
const icMaxMiss = 4

// icFillMethod заполняет мономорфный кэш метода. При частой смене ключа
// (полиморфный вызов) кэш отключается, чтобы замена не стоила дороже lookup.
func icFillMethod(ic *icEntry, class ClassInfo, name string, fn *Function, decl ClassInfo, vis Visibility) {
	if ic == nil {
		return
	}
	switch ic.state {
	case icEmpty:
		ic.class, ic.name, ic.fn, ic.decl, ic.vis = class, name, fn, decl, vis
		ic.state = icMono
	case icMono:
		if ic.class == class && ic.name == name {
			return
		}
		ic.miss++
		if ic.miss >= icMaxMiss {
			ic.state = icDisabled
			ic.fn = nil
			return
		}
		ic.class, ic.name, ic.fn, ic.decl, ic.vis = class, name, fn, decl, vis
	}
}

// icFillFunc заполняет мономорфный кэш «имя функции→*Function» (OpCallDyn).
func icFillFunc(ic *icEntry, name string, fn *Function) {
	if ic == nil {
		return
	}
	switch ic.state {
	case icEmpty:
		ic.class, ic.name, ic.fn = nil, name, fn
		ic.state = icMono
	case icMono:
		if ic.name == name {
			return
		}
		ic.miss++
		if ic.miss >= icMaxMiss {
			ic.state = icDisabled
			ic.fn = nil
			return
		}
		ic.class, ic.name, ic.fn = nil, name, fn
	}
}

// classSel выбирает класс для self::/static::/parent:: (B-селектор опкодов):
// 0 — self, 1 — static (late static binding), 2 — parent; 3+i — явный класс
// vm.classes[i] (для Foo::method()).
func (vm *VM) classSel(sel uint8) ClassInfo {
	switch sel {
	case 0:
		return vm.ctx.self
	case 1:
		return vm.ctx.called
	case 2:
		if vm.ctx.self != nil {
			return vm.ctx.self.Parent()
		}
	default:
		i := int(sel) - 3
		if i >= 0 && i < len(vm.classes) {
			return vm.classes[i]
		}
	}
	return nil
}

// ScopeKeywordErr проверяет имя класса self/static/parent: если область класса
// недоступна, возвращает ошибку Zend. Для остальных имён — nil.
func (vm *VM) ScopeKeywordErr(name string) error {
	low := strings.ToLower(strings.TrimLeft(name, "\\"))
	switch low {
	case "self":
		if vm.ctx.self == nil {
			return NewThrowable("Error", `Cannot access "self" when no class scope is active`)
		}
	case "static":
		if vm.ctx.called == nil {
			return NewThrowable("Error", `Cannot access "static" when no class scope is active`)
		}
	case "parent":
		if vm.ctx.self == nil {
			return NewThrowable("Error", `Cannot access "parent" when no class scope is active`)
		}
	}
	return nil
}

// scopeClassErr возвращает ошибку Zend для обращения к self/parent/static,
// когда область класса недоступна.
func scopeClassErr(sel uint8) error {
	switch sel {
	case 0:
		return NewThrowable("Error", `Cannot access "self" when no class scope is active`)
	case 1:
		return NewThrowable("Error", `Cannot access "static" when no class scope is active`)
	case 2:
		return NewThrowable("Error", `Cannot access "parent" when no class scope is active`)
	}
	return errUnknownClass
}

// throwScopeClass бросает ошибку недоступной области класса (для опкодов,
// у которых classSel вернул nil).
func (vm *VM) throwScopeClass(chunk *Chunk, sel uint8) execResult {
	err := scopeClassErr(sel)
	if th, ok := err.(*Throwable); ok {
		if !vm.handleThrow(chunk, th) {
			return execResult{err: th}
		}
		return execResult{next: true}
	}
	return execResult{err: err}
}

// WithAttrLocation выполняет fn, подменяя место вызова на объявление
// атрибута (для диагностик newInstance).
func (vm *VM) WithAttrLocation(file string, line int, strict bool, fn func() (Slot, error)) (Slot, error) {
	if file == "" || line <= 0 {
		if !strict {
			return fn()
		}
	}
	sf, sl := vm.attrCallFile, vm.attrCallLine
	sSetStrict, sStrict := vm.attrStrictSet, vm.attrStrict
	vm.attrCallFile, vm.attrCallLine = file, line
	vm.attrStrictSet, vm.attrStrict = true, strict
	// Кадр конструктора атрибута в трейсе тоже привязан к объявлению
	// (Zend: dummy-кадр на месте атрибута), а не к newInstance().
	sSet, sFile, sChunk, sPC, sLine := vm.nextFrameSet, vm.nextFrameFile, vm.nextFrameChunk, vm.nextFramePC, vm.nextFrameLine
	vm.nextFrameSet = true
	vm.nextFrameFile, vm.nextFrameChunk, vm.nextFramePC, vm.nextFrameLine = file, nil, 0, line
	res, err := fn()
	vm.attrCallFile, vm.attrCallLine = sf, sl
	vm.attrStrictSet, vm.attrStrict = sSetStrict, sStrict
	vm.nextFrameSet, vm.nextFrameFile, vm.nextFrameChunk, vm.nextFramePC, vm.nextFrameLine = sSet, sFile, sChunk, sPC, sLine
	return res, err
}

// evalThunk вычисляет отложенное константное выражение. Результат кэшируется в
// thunk. Ошибка возвращается наружу (PHP-исключение/фатал) и не кэшируется,
// чтобы повторное обращение могло пересчитать (после define()).
func (vm *VM) evalThunk(th *Thunk) (Slot, error) {
	if th == nil {
		return Slot{}, nil
	}
	if th.done {
		return valueSlot(th.val), th.err
	}
	if th.busy {
		// Диагностика Zend называет первую ссылку из внешнего константного
		// выражения (`self::B`, `D::B`) — исходное написание ссылки, а не
		// разрезолвленное имя.
		name := "self::"
		if len(vm.constFetchPath) > 0 {
			name = vm.constFetchPath[0]
		}
		return Slot{}, NewThrowable("Error", "Cannot declare self-referencing constant "+name)
	}
	th.materialize()
	th.busy = true
	vm.thunkEvalDepth++
	res, err := vm.invokeMethod(th.fn, Slot{}, th.self, nil)
	vm.thunkEvalDepth--
	th.busy = false
	if err != nil {
		return Slot{}, err
	}
	th.val = res
	th.done = true
	return res, nil
}

// classConstValue возвращает константу класса с учётом ленивого вычисления.
func (vm *VM) classConstValue(cls ClassInfo, name string) (Slot, bool, error) {
	if rc, ok := cls.(*runtimeClass); ok {
		if rc.IsTrait() {
			// Прямое обращение к СУЩЕСТВУЮЩЕЙ константе трейта по имени
			// трейта запрещено (константы доступны только через использующий
			// класс); несуществующая — обычная «Undefined constant» у вызова.
			if _, has := rc.lookupConst(name); !has {
				return Slot{}, false, nil
			}
			return Slot{}, false, NewThrowable("Error", "Cannot access trait constant "+cls.Name()+"::"+name+" directly")
		}
		v, ok, err := rc.lookupConstAccess(name, vm.ctx.self)
		if err != nil {
			return Slot{}, false, err
		}
		if ok {
			vm.markEnumCaseVal(v)
		}
		return valueSlot(v), ok, nil
	}
	v, ok := cls.lookupConst(name)
	if ok {
		vm.markEnumCaseVal(v)
	}
	return valueSlot(v), ok, nil
}

// resolveAllLazyConsts материализует все отложенные константы класса — PHP
// вычисляет их при первом использовании класса (напр. new Class()).
func (vm *VM) resolveAllLazyConsts(cls ClassInfo) error {
	rc, ok := cls.(*runtimeClass)
	if !ok || rc == nil {
		return nil
	}
	if rc.lazyConstsResolved {
		return nil
	}
	for name := range rc.allLazyConsts {
		if _, _, err := rc.lookupConstErr(name); err != nil {
			return err
		}
	}
	rc.lazyConstsResolved = true
	return nil
}

// callMagicUndefStatic обрабатывает вызов неопределённого статического метода
// при наличии объекта $this: PHP предпочитает instance-магию __call класса
// объекта (с поздним связыванием), если named-класс объявляет __call; иначе —
// __callStatic named-класса.
func (vm *VM) callMagicUndefStatic(cls ClassInfo, calledIn ClassInfo, mname string, passed []Slot) (Slot, bool, error) {
	called := calledIn
	if called == nil {
		called = cls
	}
	if deref(vm.cur.this).typ == ObjectType && cls.lookupMethod("__call") != nil {
		if tci := vm.classInfo(vm.cur.this); tci != nil {
			if m := tci.lookupMethod("__call"); m != nil {
				res, err := vm.invokeMethod(m, vm.cur.this, tci, []Slot{MakeString(mname), argsArray(passed)})
				return res, true, err
			}
		}
	}
	return vm.callMagicMethodCalled(cls, called, vm.cur.this, "__callStatic", []Slot{MakeString(mname), argsArray(passed)})
}

// callMagicMethod вызывает магический метод name у класса ci, если он есть.
// Возвращает (результат, найден ли метод, ошибку). Ошибка — исключение из тела
// магического метода (её нужно обрабатывать через handleThrow).
func (vm *VM) callMagicMethod(ci ClassInfo, recv Slot, name string, args []Slot) (Slot, bool, error) {
	m := ci.lookupMethod(name)
	if m == nil {
		return Slot{}, false, nil
	}
	// Рекурсивный вход в тот же магический метод для того же свойства
	// (напр. __isset внутри __isset) трактуется как «свойство недоступно».
	key := ""
	if len(args) > 0 {
		if o := vm.objectOf(deref(recv)); o != nil {
			key = name + "\x00" + strconv.Itoa(o.ID()) + "\x00" + deref(args[0]).ToString()
		}
	}
	if key != "" {
		if vm.magicBusy[key] {
			return Slot{}, false, nil
		}
		if vm.magicBusy == nil {
			vm.magicBusy = map[string]bool{}
		}
		vm.magicBusy[key] = true
		defer delete(vm.magicBusy, key)
	}
	res, err := vm.invokeMethod(m, deref(recv), ci, args)
	if err != nil {
		return Slot{}, true, err
	}
	return res, true, nil
}

// callMagicMethodCalled — callMagicMethod с явным «вызванным классом» (для
// late static binding в __callStatic).
func (vm *VM) callMagicMethodCalled(ci ClassInfo, called ClassInfo, recv Slot, name string, args []Slot) (Slot, bool, error) {
	m := ci.lookupMethod(name)
	if m == nil {
		return Slot{}, false, nil
	}
	if called == nil {
		called = ci
	}
	res, err := vm.invokeMethod(m, deref(recv), called, args)
	if err != nil {
		return Slot{}, true, err
	}
	return res, true, nil
}

// argsArray упаковывает аргументы в PHP-массив (для __call).
func argsArray(args []Slot) Slot {
	a := newArray()
	for _, s := range args {
		// Как zend_copy_parameters_array: в $arguments магического метода
		// значения копируются (ссылки разыменовываются).
		_, _ = a.Append(deref(s))
	}
	return MakeArray(a)
}

// objClassName возвращает имя класса объекта/замыкания для диагностик
// (пустая строка, если класс не найден).
func (vm *VM) objClassName(s Slot) string {
	s = deref(s)
	if s.typ == ClosureType {
		return "Closure"
	}
	if ci := vm.classInfo(s); ci != nil {
		return ci.Name()
	}
	return ""
}

// stringify приводит слот к строке с учётом __toString (конкатенация/echo).
func (vm *VM) stringify(s Slot) (string, error) {
	s = deref(s)
	if s.typ == ObjectType {
		if ci := vm.classInfo(s); ci != nil {
			if m := ci.lookupMethod("__toString"); m != nil {
				res, err := vm.invokeMethod(m, s, ci, nil)
				if err != nil {
					return "", err
				}
				return deref(res).ToString(), nil
			}
			return "", NewThrowable("Error", "Object of class "+ci.Name()+" could not be converted to string")
		}
		return "", NewThrowable("Error", "Object of class could not be converted to string")
	}
	if s.typ == ArrayType {
		vm.Warn("Array to string conversion")
		return "Array", nil
	}
	return s.ToString(), nil
}

// ToStringValue приводит значение к строке с учётом __toString и
// предупреждения о массиве (для встроенных функций вроде strval).
func (vm *VM) ToStringValue(s Slot) (string, error) {
	return vm.stringify(s)
}

// arrayKeyRepr форматирует ключ массива для диагностики: строковые ключи — в
// кавычках, целочисленные — как есть (как печатает PHP).
func arrayKeyRepr(k ArrayKey) string {
	if k.str {
		return `"` + k.s + `"`
	}
	return strconv.FormatInt(k.i, 10)
}

// arrayOffsetTypeName — имя типа значения для сообщения «Trying to access array
// offset on value of type ...» (bool печатается как true/false).
// stringOffsetBadKeyErr — Error для недопустимого ключа строкового offset'а:
// массив/объект/ресурс дают «Cannot access offset of type X on string» без
// Warning «String offset cast occurred» (bug81159). nil — ключ допустим.
func (vm *VM) stringOffsetBadKeyErr(k Slot) error {
	switch k.typ {
	case ObjectType, ArrayType, ResourceType, ClosureType:
		return NewThrowable("TypeError",
			"Cannot access offset of type "+vm.arrayOffsetTypeName(k)+" on string")
	}
	return nil
}

func (vm *VM) arrayOffsetTypeName(s Slot) string {
	switch s.typ {
	case BoolType:
		if s.BoolVal() {
			return "true"
		}
		return "false"
	case NullType:
		return "null"
	case IntType:
		return "int"
	case FloatType:
		return "float"
	case StringType:
		return "string"
	case ObjectType:
		if ci := vm.classInfo(s); ci != nil {
			return ClassDisplayName(ci.Name())
		}
		return "object"
	case ClosureType:
		return "Closure"
	case ResourceType:
		return "resource"
	}
	return s.typ.String()
}

// execCompiled выполняет байткод-функцию в новом кадре.
func (vm *VM) execCompiled(chunk *Chunk, args []Slot) (Slot, error) {
	if vm.fp+1 >= maxFrames {
		return Slot{}, errStackOverflow
	}
	// кадр вызывающего уже содержит свой ip (vm.ip) — он не перезаписывается,
	// потому что ip общий и мы его сохраним ниже.
	callerPC := vm.ip

	vm.fp++
	vm.cur = &vm.frames[vm.fp]
	vm.cur.dynLocals = nil
	vm.cur.dynOrder = nil
	// Остатки предыдущего использования этого слота кадров (если прошлый
	// выход был не через dropFrame — напр. кадр завершившегося файбера)
	// списываем, чтобы не потерять ссылки.
	vm.dropFrameIfTracked(vm.cur)
	vm.widenFrame(vm.cur, chunk.NumRegs())
	// Регистры кадра не зануляем целиком: компилятор определяет регистр до
	// чтения, а марк-фаза времени жизни ходит по точной карте tracked
	// (writeback), а не по значениям слотов — остатки прошлого вызова корнями
	// не становятся. Зануляем только «входные» регистры, которые SSA считает
	// читаемыми до первой записи (chunk.entryNull): им «неопределённость»
	// нужна как Null, а не как содержимое слота прошлого вызова. Параметры и
	// захваты привязываются ниже и перекрывают зануление (непереданные
	// параметры зануляются отдельно).
	if chunk.liveMasks != nil {
		for _, r := range chunk.entryNull {
			vm.cur.regs[r] = MakeNull()
		}
	} else {
		// Масок нет (ручные чанки): консервативно зануляем весь кадр.
		n := chunk.NumRegs()
		if n > len(vm.cur.regs) {
			n = len(vm.cur.regs)
		}
		for i := 0; i < n; i++ {
			vm.cur.regs[i] = MakeNull()
		}
	}
	vm.cur.this = vm.pendingThis
	vm.pendingThis = Slot{}
	vm.ip = 0
	byref := chunk.byRefArgs
	nbind := len(args)
	if chunk.numParamsSet && chunk.numParams < nbind {
		nbind = chunk.numParams
	}
	if nbind > len(args) {
		nbind = len(args)
	}
	// Зануляем только непереданные параметры: именно «слот = null» означает
	// «аргумент не передан» для кода доопределения дефолтов. Остальные регистры
	// не трогаем — их определяет последующий код (SSA), а марк-фаза времени
	// жизни ходит по точной карте tracked, а не по значениям слотов.
	if chunk.numParamsSet {
		for i := nbind; i < chunk.numParams && i < len(vm.cur.regs); i++ {
			vm.cur.regs[i] = MakeNull()
		}
	}
	for i := 0; i < nbind; i++ {
		a := args[i]
		if i < len(byref) && !byref[i] {
			a = deref(a)
		}
		if i < len(vm.cur.regs) {
			old := vm.cur.regs[i]
			vm.cur.regs[i] = a
			if trackedOr(old.typ, a.typ) {
				vm.wRegSlow(i, old)
			}
			shareSlot(a)
		}
	}

	prevChunk := vm.chunk
	prevArgs := vm.curFuncArgs
	if vm.pendingFuncArgs != nil {
		vm.curFuncArgs = vm.pendingFuncArgs
		vm.pendingFuncArgs = nil
	} else {
		vm.curFuncArgs = args
	}
	vm.chunk = chunk
	err := vm.exec(chunk)
	// «Залипающая» строка: при нормальном возврате диагностика типа возврата
	// (: T) должна указывать на строку return в callee, а не на место вызова.
	// На ошибке строку НЕ трогаем — её уже выставил источник ошибки.
	if _, ok := err.(retSignal); ok {
		if l := chunk.LineAt(vm.ip - 1); l > 0 {
			vm.curLine = l
		}
	}
	vm.chunk = prevChunk
	vm.curFuncArgs = prevArgs

	// Выход из кадра = переключение контекста наружу: значения регистров
	// теряются (учёт ссылок списывается по битам), наружу уходит только
	// vm.retVal. Работает и при исключительном раскручивании.
	vm.dropFrameIfTracked(vm.cur)
	// возврат к вызывающему: снимаем обработчики этого кадра
	vm.popTriesForFrame(vm.fp)
	vm.fp--
	vm.cur = &vm.frames[vm.fp]
	vm.ip = callerPC

	if _, ok := err.(retSignal); ok {
		return vm.retVal, nil
	}
	return Slot{}, err
}

// cannotInstantiateErr формирует ошибку попытки создать экземпляр
// интерфейса/трейта/абстрактного класса/enum.
func cannotInstantiateErr(ci ClassInfo) *Throwable {
	switch {
	case ci.IsInterface():
		return NewThrowable("Error", "Cannot instantiate interface "+ci.Name())
	case ci.IsTrait():
		return NewThrowable("Error", "Cannot instantiate trait "+ci.Name())
	case ci.IsAbstract():
		return NewThrowable("Error", "Cannot instantiate abstract class "+ci.Name())
	case ci.IsEnum():
		return NewThrowable("Error", "Cannot instantiate enum "+ci.Name())
	}
	return NewThrowable("Error", "Cannot instantiate class")
}

// AddShutdownFunction регистрирует обработчик register_shutdown_function.
func (vm *VM) AddShutdownFunction(cb Slot, args []Slot) {
	cp := make([]Slot, len(args))
	copy(cp, args)
	// Аргументы удерживаются таблицей VM: ресурсы с OnDrop (процессы
	// proc_open) должны оставаться живыми до вызова обработчика, даже если
	// регистр-владелец уже потерян.
	for _, a := range cp {
		vm.resourceHold(a)
	}
	vm.shutdownFuncs = append(vm.shutdownFuncs, shutdownHandler{cb: cb, args: cp})
}

// resourceHold увеличивает число владельцев ресурса с OnDrop вне регистров.
func (vm *VM) resourceHold(s Slot) {
	d := deref(s)
	if d.typ != ResourceType {
		return
	}
	r := AsResource(d)
	if r == nil || r.OnDrop == nil {
		return
	}
	if r.dropped && r.Refs <= 0 {
		r.dropped = false
	}
	r.Refs++
}

// releaseShutdownRefs отпускает удержания ресурсов таблицей shutdown-функций.
func (vm *VM) releaseShutdownRefs() {
	for i := range vm.shutdownFuncs {
		for _, a := range vm.shutdownFuncs[i].args {
			vm.dropResourceRef(a)
		}
	}
	vm.shutdownFuncs = nil
}

// RegisterHeaderCallback добавляет callback header_register_callback.
func (vm *VM) RegisterHeaderCallback(cb Slot) {
	vm.headerCallbacks = append(vm.headerCallbacks, cb)
}

// runHeaderCallbacks вызывает callbacks header_register_callback в самом конце.
func (vm *VM) runHeaderCallbacks() {
	for _, cb := range vm.headerCallbacks {
		if _, err := vm.CallCallable(&vm.ctx, cb, nil); err != nil {
			vm.reportUncaught(err)
		}
	}
}

// RequestHalt просит VM остановить текущий скрипт при первой возможности
// (вложенный eval/include напечатал фатальную ошибку или выполнил exit()).
func (vm *VM) RequestHalt() {
	if vm != nil {
		vm.haltRequested = true
	}
}

// runShutdownFunctions вызывает зарегистрированные shutdown-обработчики в
// порядке регистрации (PHP вызывает их после завершения скрипта).
func (vm *VM) runShutdownFunctions() {
	if vm.shutdownFnsDone {
		return
	}
	vm.shutdownFnsDone = true
	savedFile, savedFileID, savedLine := vm.curFile, vm.curFileID, vm.curLine
	vm.noActiveFile = true
	vm.curFile = "[no active file]"
	vm.curFileID = fileIDNoActive
	vm.curLine = 0
	defer func() {
		vm.noActiveFile = false
		vm.curFile, vm.curFileID, vm.curLine = savedFile, savedFileID, savedLine
	}()
	for _, h := range vm.shutdownFuncs {
		res, err := vm.CallCallable(&vm.ctx, h.cb, h.args)
		_ = res
		if err != nil {
			// exit/исключение в shutdown-функции прекращает вызов остальных
			// (bug62763/d11); исключение печатается как непойманное,
			// после чего выполняются деструкторы.
			if !IsExitSignal(err) {
				vm.reportUncaught(err)
			}
			break
		}
	}
	// Удержания аргументов снимаются: ресурсы, потерявшие всех владельцев,
	// финализируются (процессы proc_open закрываются).
	vm.releaseShutdownRefs()
}

// releaseMainReturn освобождает значение верхнеуровневого return сразу после
// завершения main-скрипта (Zend: php_execute_script уничтожает retval до
// php_request_shutdown). Деструкторы объектов, живущих только в возврате,
// срабатывают в позиции return и раньше shutdown-функций; объекты, удержанные
// глобалами/статикой, доживают до runShutdownDestructors (d3/d4).
func (vm *VM) releaseMainReturn() {
	v := vm.retVal
	if v.typ < ObjectType || v.typ == ResourceType {
		return
	}
	vm.retVal = Slot{}
	// Регистр возврата кадра main хранит вторую (учитываемую) ссылку на то же
	// значение: списываем её до прохода сборщика, иначе markRoots увидит
	// значение живым по SSA-маске в точке return.
	if r := vm.retReg; vm.cur != nil && r >= 0 && uint(r) < maxRegsHard {
		vm.wDropReg(r)
		vm.cur.regs[r] = MakeNull()
	}
	vm.noteDropped(v)
	// Ошибка деструктора (например, FiberError при попытке переключения)
	// становится непойманной фатальной (no-switch-dtor-*).
	if (!vm.lifeCandidate && vm.zeroObjs <= 0) || vm.sweepBusy {
		return
	}
	if out := vm.sweepLifetime(false, vm.chunk, vm.ip-1); out.err != nil {
		vm.reportUncaught(out.err)
	}
}

// RequestEndHook — завершение запроса на уровне пакета php (закрытие потоков
// и stream-фильтров: Zend уничтожает список ресурсов после деструкторов,
// вызывая filter(closing=true)/onClose — bug54350, ord1).
var RequestEndHook func(*VM)

// SetRequestEndHook регистрирует обработчик завершения запроса.
func SetRequestEndHook(fn func(*VM)) { RequestEndHook = fn }

// runRequestEnd выполняет обработчик завершения запроса один раз.
func (vm *VM) runRequestEnd() {
	if vm.requestEndDone {
		return
	}
	vm.requestEndDone = true
	if RequestEndHook != nil {
		RequestEndHook(vm)
	}
}

// runShutdownDestructors вызывает __destruct у всех ещё живых объектов в
// порядке, обратном созданию (упрощённая модель PHP shutdown).
func (vm *VM) runShutdownDestructors() {
	if vm.shutdownDone {
		return
	}
	vm.shutdownDone = true
	vm.inShutdown = true
	defer func() { vm.inShutdown = false }()
	vm.flushResourceDrops()
	// Ошибки деструкторов/незавершённых файберов собираются и печатаются одной
	// цепочкой (Zend: каждый следующий бросок связывает предыдущий как
	// previous).
	var shutdownErrs []*Throwable
	defer func() {
		if len(shutdownErrs) > 0 {
			vm.reportUncaughtChain(shutdownErrs)
		}
	}()
	if vm.destructed == nil {
		vm.destructed = map[*Object]bool{}
	}
	// Фатальная диагностика (E_ERROR/E_USER_ERROR) в Zend — bailout: до
	// деструкторов объектов дело не доходит, и finally незавершённых файберов
	// не выполняется (Zend/tests/fibers/fatal-error-with-multiple-fibers).
	if vm.fatalDiag {
		vm.bailoutShutdown = true
	}
	// Во время выполнения деструкторов переключение файберов запрещено.
	savedNoSwitch := vm.noSwitch
	vm.noSwitch = true
	defer func() { vm.noSwitch = savedNoSwitch }()
	// Zend вызывает деструкторы всех объектов в порядке, обратном созданию;
	// циклический мусор (gcPendingObjs) тоже уничтожается при завершении.
	all := make([]*Object, 0, len(vm.liveObjs)+len(vm.gcPendingObjs))
	for _, o := range vm.liveObjs {
		if o != nil && (o.dead || o.gcPending) {
			continue
		}
		all = append(all, o)
	}
	all = append(all, vm.gcPendingObjs...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].objID > all[j].objID })
	for _, o := range all {
		if o == nil || o.dead || vm.destructed[o] || vm.noDtor[o] {
			continue
		}
		vm.destructed[o] = true
		// Незавершённый генератор при уничтожении прогоняет свои finally
		// (Zend: zend_generator_close). Ошибки (напр. yield в finally)
		// становятся фатальными. Проверка дескриптора обязательна: у VM без
		// класса Generator (собранные вручную) objectAs с nil-владельцем
		// принял бы за генератор любой объект.
		if vm.genClass != nil {
			if g := objectAs[Generator](o, vm.genClass); g != nil {
				if err := vm.closeGenerator(g, true); err != nil {
					if th, ok := asThrowable(err); ok {
						shutdownErrs = append(shutdownErrs, th)
					}
				}
				continue
			}
		}
		// Незавершённый файбер при уничтожении прогоняет свои finally.
		// После фатальной ошибки (bailout) Zend не доходит до деструкторов и
		// finally файберов не выполняется (fatal-error-with-multiple-fibers).
		if vm.fiberClass != nil && !vm.bailoutShutdown {
			if f := objectAs[Fiber](o, vm.fiberClass); f != nil {
				if err := vm.closeFiber(f, true); err != nil {
					if th, ok := asThrowable(err); ok {
						shutdownErrs = append(shutdownErrs, th)
					}
				}
				continue
			}
		}
		ci := o.Class()
		if ci == nil {
			continue
		}
		m := ci.lookupMethod("__destruct")
		if m == nil {
			continue
		}
		_, ddecl, dvis := ci.methodInfo("__destruct")
		if !canAccess(dvis, ddecl, nil) {
			vm.Write(fmt.Sprintf("\nWarning: Call to %s %s::__destruct() from global scope during shutdown ignored in Unknown on line 0\n",
				visWord(dvis), ci.Name()))
			continue
		}
		slot := vm.slotOf(o)
		vm.markNextFrameInternal = true
		if _, err := vm.invokeMethod(m, slot, ci, nil); err != nil {
			// Исключение из __destruct при завершении скрипта — фатальная
			// ошибка (weakmap_dtor_exception): Zend печатает её как
			// непойманную, а не глотает.
			if th, ok := asThrowable(err); ok {
				shutdownErrs = append(shutdownErrs, th)
			}
		}
	}
}
