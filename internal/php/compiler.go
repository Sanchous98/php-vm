package php

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"phpvm/internal/vm"
)

// Compiled — результат компиляции: главный чанк + таблица функций.
type Compiled struct {
	Main    *vm.Chunk
	Funcs   []*vm.Function
	Classes []vm.ClassInfo
	// IncompleteIdx — индекс дескриптора __PHP_Incomplete_Class в Classes.
	// Хранится явно, потому что при линковке подключённого файла (см.
	// compileFileModeLink) он может оказаться не последним элементом.
	IncompleteIdx int
	// ParseFatal — не nil, если чанк лишь печатает фатальную Parse error.
	// Нужно, чтобы eval() мог бросить перехватываемый ParseError вместо
	// печати фатала (PHP: parse error в eval — исключение).
	ParseFatal *parseError
	// CatchableFatal — не nil, если чанк лишь печатает фатал, который в
	// eval()/include Zend превращает в перехватываемый CompileError
	// (диагностики фазы парсера, напр. «Multiple final modifiers…», bug75218).
	CatchableFatal *fatalError
}

// compilerDeprecations — стартовые deprecation-сообщения, собранные при
// компиляции (например, устаревшая форма ${var} в строках). PHP печатает их
// до начала выполнения.
var compilerDeprecations []string

// compilerWarnings — стартовые Warning-сообщения компиляции (напр. final
// private method). PHP печатает их до начала выполнения.
var compilerWarnings []string

// fatalError — фатальная ошибка уровня declare-времени (аналог zend fatal).
// Вместо аварийного возврата при компиляции она превращается в программу-
// «одну инструкцию OpFatal», чтобы сообщение попало в вывод и совпало с PHP.
type fatalError struct {
	msg  string
	line int
	// file — файл диагностики, если она привязана не к компилируемому юниту
	// (например, метод примешан из трейта другого файла).
	file string
	// warnMsg/warnLine — предупреждение, печатаемое до фатальной ошибки
	// (напр. declare(encoding) при выключенном zend.multibyte).
	warnMsg  string
	warnLine int
}

func (e *fatalError) Error() string { return e.msg }

// newFatal создаёт fatalError с текущей строкой компилятора.
func (c *compiler) newFatal(msg string) error {
	line := c.curLine
	if line <= 0 {
		line = 1
	}
	return &fatalError{msg: msg, line: line, file: c.errFile}
}

// pushStrict входит в регион strict_types с новым значением.
func (c *compiler) pushStrict(v bool) {
	c.strictStack = append(c.strictStack, c.strict)
	c.strict = v
	c.chunk.SetEmitStrict(v)
}

// pushFile запоминает текущий файл и переключает его (для inlined include).
func (c *compiler) pushFile(file string) {
	c.fileStack = append(c.fileStack, c.file)
	if file != "" {
		c.file = file
	}
}

// popFile восстанавливает файл после региона StrictStmt.
func (c *compiler) popFile() {
	if n := len(c.fileStack); n > 0 {
		c.file = c.fileStack[n-1]
		c.fileStack = c.fileStack[:n-1]
	}
}

// popStrict выходит из региона strict_types.
func (c *compiler) popStrict() {
	if n := len(c.strictStack); n > 0 {
		c.strict = c.strictStack[n-1]
		c.strictStack = c.strictStack[:n-1]
		c.chunk.SetEmitStrict(c.strict)
	}
}

// markDeclStrict проставляет Strict/DefFile у FuncDecl/ClassDecl по позиции в
// плоском списке операторов, отслеживая регионы StrictStmt.
func markDeclStrict(stmts []Stmt, initial bool, initialFile string) {
	strict := initial
	file := initialFile
	var sstack []bool
	var fstack []string
	for _, st := range stmts {
		switch s := st.(type) {
		case *StrictStmt:
			if s.Push {
				sstack = append(sstack, strict)
				fstack = append(fstack, file)
				strict = s.Strict
				if s.File != "" {
					file = s.File
				}
			} else if n := len(sstack); n > 0 {
				strict = sstack[n-1]
				sstack = sstack[:n-1]
				file = fstack[n-1]
				fstack = fstack[:n-1]
			}
		case *FuncDecl:
			s.Strict = strict
			s.DefFile = file
		case *ClassDecl:
			s.Strict = strict
			s.DefFile = file
		}
	}
}

// checkSpecialAttrTargets валидирует специальные внутренние атрибуты по цели
// применения (Zend: ReturnTypeWillChange разрешён только на методах).
func checkSpecialAttrTargets(base *compiler, decls []*FuncDecl, classDecls []*ClassDecl) error {
	rtwc := func(as []AttrDecl) bool {
		for _, a := range as {
			n := strings.TrimPrefix(a.Name, `\`)
			if strings.EqualFold(n, "ReturnTypeWillChange") {
				return true
			}
		}
		return false
	}
	fail := func(what string, line int) error {
		base.curLine = line
		return base.newFatal(fmt.Sprintf(
			`Attribute "ReturnTypeWillChange" cannot target %s (allowed targets: method)`, what))
	}
	for _, fd := range decls {
		if rtwc(fd.Attrs) {
			return fail("function", base.stmtLines[fd])
		}
	}
	for _, cd := range classDecls {
		if rtwc(cd.Attrs) {
			return fail("class", cd.Line)
		}
		for _, m := range cd.Members {
			if m.Kind == "prop" && rtwc(m.Attrs) {
				return fail("property", m.Line)
			}
		}
	}
	return nil
}

// fatalCompiled строит минимальную программу, печатающую Fatal error.
func fatalCompiled(fe *fatalError, file string) *Compiled {
	if fe.file != "" {
		file = fe.file
	}
	chunk := vm.NewChunk()
	chunk.File = file
	line := fe.line
	if line <= 0 {
		line = 1
	}
	chunk.SetLine(line)
	if fe.warnMsg != "" {
		wl := fe.warnLine
		if wl <= 0 {
			wl = line
		}
		chunk.SetStartupWarnings([]string{fmt.Sprintf("%s in %s on line %d", fe.warnMsg, file, wl)})
	}
	chunk.Emit(vm.Instruction{Op: vm.OpFatal, C: int32(chunk.AddConst(vm.StrConst(fe.msg)))})
	chunk.Emit(vm.Instruction{Op: vm.OpHalt})
	return &Compiled{Main: chunk, IncompleteIdx: -1}
}

// parseFatalCompiled строит минимальную программу, печатающую Parse error.
func parseFatalCompiled(pe *parseError, file string) *Compiled {
	chunk := vm.NewChunk()
	chunk.File = file
	line := pe.line
	if line <= 0 {
		line = 1
	}
	chunk.SetLine(line)
	chunk.Emit(vm.Instruction{Op: vm.OpParseFatal, C: int32(chunk.AddConst(vm.StrConst(pe.msg)))})
	chunk.Emit(vm.Instruction{Op: vm.OpHalt})
	return &Compiled{Main: chunk, IncompleteIdx: -1, ParseFatal: pe}
}

// globalsModifyMsg — диагностика PHP 8.1+ для недопустимой записи в $GLOBALS.
const globalsModifyMsg = "$GLOBALS can only be modified using the $GLOBALS[$name] = $value syntax"

// isAssertFuncName сообщает, что имя — встроенная функция assert().
func isAssertFuncName(name string) bool {
	return strings.EqualFold(strings.TrimLeft(name, "\\"), "assert")
}

// isFCCArgs сообщает, что аргументы — маркер first-class callable `f(...)`.
func isFCCArgs(args []Expr) bool {
	if len(args) != 1 {
		return false
	}
	if sp, ok := args[0].(*ArgSpreadExpr); ok {
		return sp.X == nil
	}
	return false
}

// funcFCCCall компилирует first-class callable для функции: f(...).
func (c *compiler) funcFCCCall(x *CallExpr) (uint8, error) {
	fcc := func(kind int, a Expr, b Expr) *CallExpr {
		args := []Expr{&IntLit{V: int64(kind)}}
		if a != nil {
			args = append(args, a)
		}
		if b != nil {
			args = append(args, b)
		}
		return &CallExpr{Name: "__php_fcc", Args: args}
	}
	for _, name := range []string{x.Name, x.Fallback} {
		if name == "" {
			continue
		}
		if i, ok := c.idx[name]; ok {
			return c.expr(fcc(1, &IntLit{V: int64(i)}, nil))
		}
	}
	return c.expr(fcc(5, &StrLit{V: x.Name}, nil))
}

// memberFCCCall компилирует first-class callable для метода:
// kind 2 — $obj->m(...), kind 3 — Class::m(...).
func (c *compiler) memberFCCCall(kind int, recv Expr, name string, cls ...string) (uint8, error) {
	args := []Expr{&IntLit{V: int64(kind)}}
	switch kind {
	case 2:
		args = append(args, recv)
	case 3:
		if recv != nil {
			args = append(args, recv)
		} else {
			args = append(args, &StrLit{V: cls[0]})
		}
	}
	args = append(args, &StrLit{V: name})
	return c.expr(&CallExpr{Name: "__php_fcc", Args: args})
}

// isGlobalsVar сообщает, что выражение — сама суперглобальная $GLOBALS
// (а не элемент/свойство).
func isGlobalsVar(e Expr) bool {
	v, ok := e.(*VarExpr)
	return ok && v.NameExpr == nil && (v.Name == "$GLOBALS" || v.Name == "GLOBALS")
}

// isThisVar сообщает, что выражение — переменная $this.
func isThisVar(e Expr) bool {
	v, ok := e.(*VarExpr)
	return ok && v.NameExpr == nil && (v.Name == "$this" || v.Name == "this")
}

type loopCtx struct {
	continueJumps []int
	breakJumps    []int
	regDepth      int // глубина c.regions (включая сам цикл) на входе
	// isSwitch — уровень принадлежит switch (continue в switch эквивалентен
	// break; Zend печатает Warning).
	isSwitch bool
	freeRegs []uint8 // временные регистры foreach: освобождаются при выходе
}

// pendingGoto — прямой goto на ещё не определённую метку.
type pendingGoto struct {
	at      int
	depth   int // c.loopDepth в точке goto
	line    int
	regions []regionRef  // активные регионы в точке goto
	parts   []unwindPart // цепочка раскрутки: невышедшие части гасятся в NOP
}

// regionRef — элемент стека вложенности компилятора: try-регион либо цикл.
// Раскрутка (return/break/continue/goto и диспетчер исключений) идёт от
// внутренних регионов к внешним; цикл с freeRegs перед снятием освобождает
// свои временные регистры (аналог Zend FE_FREE/cleanup_live_vars).
type regionRef struct {
	try  *tryScope
	loop *loopCtx
}

// unwindPart — диапазон инструкций [start, end), эмитированный для одного
// региона при раскрутке forward-goto (Zend: zend_resolve_goto_label гасит
// FAST_CALL/FREE регионов, которые переход НЕ покидает).
type unwindPart struct {
	start, end int
}

// tryScope — активный try-регион, нужный для прогона finally на
// return/break/continue.
type tryScope struct {
	finally    []Stmt
	hasFinally bool
	tiIdx      int
}

type compiler struct {
	chunk      *vm.Chunk
	vars       map[string]uint8
	nvars      uint8
	nvars0     uint8 // число регистров параметров+захватов (pinned для аллокатора)
	tmp        uint8
	max        uint8
	funcs      []*vm.Function
	idx        map[string]int
	loops      []*loopCtx
	internal   int
	isMain     bool
	funcName   string
	file       string // путь к компилируемому файлу (для __FILE__/__DIR__)
	globals    map[string]bool
	staticKeys map[string]string
	// detached — static/global-переменные, для которых текущий вызов сделал
	// `=&`: после этого CV живёт в локальном ref-регистре, а глобальная/
	// статическая таблица не меняется (Zend ASSIGN_REF заменяет CV, а не
	// запись таблицы).
	detached map[string]bool
	// localStatics — имена условных static-деклараций (внутри if/switch/…
	// в теле функции): они компилируются как локальные переменные с
	// инициализацией в точке объявления (ленивая семантика BIND_STATIC).
	localStatics map[string]bool
	// mainStaticSeen — имена static-переменных главной программы (для
	// диагностики повторного объявления).
	mainStaticSeen map[string]bool
	// promote — main-переменные, которые держатся в регистрах кадра, а не в
	// VM.globals (см. promote.go). Для функций не используется.
	promote map[string]bool

	// ООП
	classes  []vm.ClassInfo
	classIdx map[string]int
	curClass vm.ClassInfo
	// declIdx — индекс в таблице классов для объявлений, выполняемых в
	// рантайме (OpDeclareClass): объявления внутри тел функций/замыканий,
	// блоков условий и т.п. Для верхнеуровневых объявлений пусто (они
	// хойстятся и не эмитят OpDeclareClass).
	declIdx map[*ClassDecl]int
	// declIndexAll — индекс в таблице классов для ЛЮБОГО объявления (включая
	// рантайм-объявления, отсутствующие в classIdx). Нужен смешиванию трейтов.
	declIndexAll map[*ClassDecl]int
	// classAlias — псевдонимы классов (class_alias с литеральными аргументами):
	// alias (нижний регистр) → имя целевого класса. Используется при
	// компиляционном сравнении типов (инвариантность свойств и т.п.).
	classAlias map[string]string
	// rtLookup/rtIsA — резолверы классов для отложенной рантайм-проверки
	// совместимости сигнатур (см. phpVarianceHook): lookup смотрит в таблицу
	// классов VM, rtIsA учитывает «почти слинкованные» классы. nil при
	// компиляции (обычная статическая проверка).
	rtLookup func(string) vm.ClassInfo
	rtIsA    func(child, parent vm.ClassInfo) bool
	// topVariance — отложенные до рантайма проверки классов верхнего уровня
	// (subUnknown на этапе компиляции, как Zend без early binding); план
	// прикрепляется к дескриптору класса, а на позиции объявления эмитится
	// вызов __php_variance_check. deferClassBase — индекс первого класса файла.
	topVariance    map[*ClassDecl]*rtVarPlan
	deferClassBase int
	// availLimit — при проверке совместимости сигнатур (Zend delayed early
	// binding) классы, объявленные ПОСЛЕ текущего в исходном порядке, ещё не
	// «существуют»: их индекс в classes больше availLimit, и lookupClassInfo
	// их не видит. -1 — ограничение снято.
	availLimit int
	// errFile — файл для следующей фатальной ошибки компиляции, если он
	// отличается от компилируемого юнита (метод примешан из трейта в другом
	// файле: Zend привязывает диагностику к файлу трейта, bug81192).
	errFile string
	// ticks — declare(ticks=N) текущей области; tickCount — счётчик
	// операторов чанка для эмиссии вызовов __php_ticks (только в областях,
	// объявивших declare(ticks): вне них цена нулевая).
	ticks     int
	tickCount int

	// Ссылки: refs[name] — регистр companion-ячейки для alias-переменной.
	// Для глобальных/main-переменных значение не используется (0xFF).
	refs map[string]uint8
	// defRegs — регистры-флаги «переменная определена» для локальных
	// переменных, к которым применён простой unset(): после unset регистр
	// получает Null, и Zend при последующем чтении печатает
	// «Undefined variable». Флаг поднимается unset-ом и опускается записью.
	defRegs map[string]uint8
	// writes — локальные переменные, которым в теле функции что-то
	// присваивается. Переменные, которые только читаются, остаются UNDEF
	// (как CV в Zend) и дают "Undefined variable" при чтении.
	writes map[string]bool
	// byrefReturn — текущая функция объявлена как `function &`.
	byrefReturn bool
	// closureBody — компилируется тело замыкания (self/parent в параметрах
	// разрешены и резолвятся при вызове через bindTo).
	closureBody bool
	// scopeFatal — self/parent/static в функции без области класса: Zend
	// сообщает фатальную ошибку компиляции.
	scopeFatal error
	// strict — файл скомпилирован в режиме declare(strict_types=1).
	strict bool
	// fileStack — сохранённые значения file при входе в регионы StrictStmt
	// (подключаемые файлы inlined: __FILE__/__DIR__ должны указывать на них).
	fileStack []string
	// strictStack — сохранённые значения strict при входе в регионы StrictStmt
	// (инлайн include/eval с иным strict_types).
	strictStack []bool
	// noParentScope — текущий класс не имеет родителя, поэтому `parent` как
	// имя типа использовать нельзя (Zend: "has no parent").
	noParentScope bool
	// curRetType — объявленный тип возврата текущей компилируемой функции
	// (для declare-time проверок void/never).
	curRetType string
	// allVarsGlobal — в теле функции есть динамический `global ${expr}`:
	// имена заранее неизвестны, поэтому все переменные, кроме параметров и
	// захватов (localNames), читаются/пишутся через таблицу глобалов.
	allVarsGlobal bool
	localNames    map[string]bool

	// hasYield — тело содержало `yield`; после компиляции функция помечается
	// как генератор (compileBody вызывает SetGenerator).
	hasYield bool
	// usesThisVar — тело обращается к $this вне обычного чтения (isset($this)
	// компилируется в вызов __php_isset_this без OpThis): нужно для флага
	// ZEND_ACC_USES_THIS у замыканий (Closure::bindTo(null) диагностика).
	usesThisVar bool
	// undefInit — регистры-флаги defRegs, которые в прологе тела получают
	// true («переменная ещё не определена»): переменные, у которых в теле
	// есть и чтение, и запись. Чтение до первой записи печатает
	// «Undefined variable»; запись опускает флаг (clearUndef).
	undefInit []uint8
	// discardResult — результат верхнеуровневого присваивания-оператора не
	// используется (`$a[$i] = $v;`): можно писать значение напрямую, не
	// материализуя результат присваивания (для строковых offset'ов Zend
	// возвращает записанный символ — он нужен только когда результат
	// используется).
	discardResult bool
	// pendingRetFatal — отложенная фатальная ошибка «функция с типом возврата
	// должна вернуть значение». Не поднимается сразу, т.к. `return;` в
	// функции-генераторе легален, а `yield` может встретиться ниже по коду.
	pendingRetFatal error

	// methodRefs — union byref-масок методов по имени (для by-ref аргументов
	// при вызовах $obj->m() / C::m() / new C()).
	methodRefs map[string][]bool

	// Исключения: стек активных регионов (try и foreach-циклы) для прогона
	// finally/освобождения темпов на return/break/continue/goto.
	regions []regionRef
	// loopLog — все foreach-циклы в порядке компиляции (для блоков
	// освобождения на входе catch/finally: исключение покидает циклы тела).
	loopLog []*loopCtx

	// closureIdx — соответствие AST-узла замыкания/стрелочной функции её
	// скомпилированной функции в общем пуле funcs. Таблица общая для всех
	// компиляторов (создаётся один раз в Compile).
	closureIdx map[Expr]*closureInfo

	// goto/метки: адреса меток и неразрешённые переходы (forward goto).
	labels       map[string]int
	labelDepth   map[string]int
	labelRegions map[string][]regionRef
	pendingGotos map[string][]pendingGoto
	// loopDepth — глубина вложенности циклов/switch (правила goto: прыжок
	// внутрь цикла запрещён).
	loopDepth int

	// anonNames — соответствие AST-узлов анонимных классов именам, под
	// которыми они зарегистрированы в таблице классов.
	anonNames map[Expr]string

	// Диагностика: строки узлов (из парсера) и текущая строка оператора.
	stmtLines map[Stmt]int
	exprLines map[Expr]int
	curLine   int
	// inlineStack — вложенные регионы вклеенных include/require: строки их
	// файлов и открытые inline-регионы чанка (для файла/трейса инструкций).
	inlineStack []inlineCtx

	// nullsafeBase — регистр базового значения активной цепочки `?->`
	// (для NullsafeBase). hasBase — признак активности подстановки.
	nullsafeBase uint8
	hasBase      bool

	// constCtx — компиляция константного выражения (thunk): неизвестные
	// глобальные константы трактуются как фатал (constant-выражение PHP).
	constCtx bool
	// constEval — счётчик вложенности контекстов, в которых выражение
	// вычисляется как константное (zend_ast_evaluate): значение оператора
	// `const NAME = ...;` и значения по умолчанию параметров.
	constEval int
	// haltOffset — значение магической константы __COMPILER_HALT_OFFSET__
	// текущего файла (-1 — __halt_compiler() не встречался).
	haltOffset int64

	// usesFuncArg — тело ссылается на func_get_arg()/func_get_args()/
	// func_num_args(): эти встроенные читают регистры параметров кадра
	// напрямую, поэтому для функции отключается SSA-распределение регистров
	// (иначе параметры могли бы быть переиспользованы как временные).
	usesFuncArg bool

	// usesSymtab — тело ссылается на compact()/extract()/get_defined_vars():
	// встроенные должны видеть локальную таблицу символов кадра. Регистры
	// всех переменных pin'ятся, а в чанк пишется карта имя→регистр.
	usesSymtab bool
}

// closureInfo описывает анонимную/стрелочную функцию для компиляции.
type closureInfo struct {
	idx      int          // индекс в пуле функций
	params   []Param      //
	uses     []Param      // use (...) для анонимных функций
	free     []string     // неявные захваты стрелочной функции
	isArrow  bool         //
	byrefRet bool         //
	retType  string       // объявленный тип возврата
	static   bool         //
	cls      vm.ClassInfo // класс, в котором определено замыкание
	body     []Stmt       // тело анонимной функции
	expr     Expr         // тело стрелочной функции
	attrs    []AttrDecl   // атрибуты замыкания
	line     int          // строка объявления
	ticks    int          // declare(ticks=N) в точке объявления замыкания
}

func newCompiler(funcs []*vm.Function, idx map[string]int) *compiler {
	return &compiler{chunk: vm.NewChunk(), vars: map[string]uint8{}, funcs: funcs, idx: idx,
		globals: map[string]bool{}, staticKeys: map[string]string{},
		refs: map[string]uint8{}, methodRefs: map[string][]bool{},
		defRegs: map[string]uint8{},
		labels:  map[string]int{}, labelDepth: map[string]int{}, labelRegions: map[string][]regionRef{},
		pendingGotos: map[string][]pendingGoto{},
		availLimit:   -1, haltOffset: -1}
}

// isRef сообщает, что переменная участвует в ссылках (alias/by-ref параметр).
func (c *compiler) isRef(name string) bool {
	_, ok := c.refs[name]
	return ok
}

// ensureRefReg возвращает регистр-ячейку ссылки для локальной переменной,
// заводя его при необходимости. Вызывать только из prescan/compileFuncBody.
func (c *compiler) ensureRefReg(name string) uint8 {
	if r, ok := c.refs[name]; ok {
		return r
	}
	if r, ok := c.vars[name]; ok {
		c.refs[name] = r
		return r
	}
	r := c.varReg(name)
	c.refs[name] = r
	return r
}

// paramNames возвращает имена параметров без ведущего `$`.
// classAttributeMarker ищет `#[Attribute(...)]` среди атрибутов класса.
// Возвращает флаг, статические цели (если аргументов нет) и выражение
// аргумента flags (для отложенного вычисления). Zend принимает аргумент и
// под произвольным именем (проверка имени для внутреннего Attribute не
// выполняется — ср. named_params/attributes_named_flags_incorrect).
func classAttributeMarker(decls []AttrDecl) (bool, uint32, Expr) {
	for _, d := range decls {
		if !strings.EqualFold(strings.TrimLeft(d.Name, "\\"), "Attribute") {
			continue
		}
		for _, a := range d.Args {
			if a.Val != nil {
				return true, 0, a.Val
			}
		}
		return true, 0x3f, nil
	}
	return false, 0, nil
}

// internalAttributeTargets — цели, допустимые для встроенных классов-атрибутов
// (проверяются на этапе компиляции, как в Zend для internal-классов).
var internalAttributeTargets = map[string]int{
	"attribute":              1,
	"sensitiveparameter":     32,
	"returntypewillchange":   4,
	"allowdynamicproperties": 1,
	"overrid":                4,
}

// checkInternalAttributeTarget проверяет применение встроенного
// класса-атрибута к цели target: Zend валидирует internal-атрибуты при
// компиляции. Возвращает фатал или nil.
func checkInternalAttributeTarget(decls []AttrDecl, target int, line int) error {
	for _, d := range decls {
		nm := strings.ToLower(strings.TrimLeft(d.Name, "\\"))
		allowed, ok := internalAttributeTargets[nm]
		if !ok {
			continue
		}
		if target != 0 && allowed&target == 0 {
			display := strings.TrimLeft(d.Name, "\\")
			return &fatalError{msg: "Attribute \"" + display + "\" cannot target " +
				vm.AttrTargetNames(target) + " (allowed targets: " + vm.AttrTargetNames(allowed) + ")", line: line}
		}
	}
	return nil
}

// countClassAttr считает атрибуты с указанным именем (case-insensitive).
func countClassAttr(decls []AttrDecl, name string) int {
	n := 0
	for _, d := range decls {
		if strings.EqualFold(strings.TrimLeft(d.Name, "\\"), name) {
			n++
		}
	}
	return n
}

// isArrayOrNullableArrayType — тип `array`, `?array` или union только из
// array|null (Zend допускает такие объявления __debugInfo).
func isArrayOrNullableArrayType(t string) bool {
	t = strings.TrimPrefix(t, "?")
	hasArray, hasNull := false, false
	for _, part := range strings.Split(t, "|") {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "array":
			hasArray = true
		case "null":
			hasNull = true
		default:
			return false
		}
	}
	_ = hasNull
	return hasArray
}

// paramAttrsInfo собирает атрибуты параметров (nil-элементы для параметров без
// атрибутов), как vm.Function.ParamAttrs. build — построитель отложенных
// выражений аргументов (nil — только константы, вычисляемые сразу).
func paramAttrsInfo(params []Param, file string, strict bool, build func(Expr) (*vm.Function, error)) [][]vm.AttributeInfo {
	any := false
	for _, p := range params {
		if len(p.Attrs) > 0 {
			any = true
			break
		}
	}
	if !any {
		return nil
	}
	out := make([][]vm.AttributeInfo, len(params))
	for i, p := range params {
		if len(p.Attrs) > 0 {
			out[i] = makeAttrsInfo(p.Attrs, file, strict, build)
		}
	}
	return out
}

func paramNames(params []Param) []string {
	out := make([]string, len(params))
	for i, p := range params {
		out[i] = strings.TrimPrefix(p.Name, "$")
	}
	return out
}

// defaultCoercesToFloat сообщает, что целочисленное значение по умолчанию надо
// расширить до float: в типе есть float и нет int (strict-исключение Zend).
func defaultCoercesToFloat(typ string) bool {
	typ = strings.TrimPrefix(strings.TrimSpace(typ), "?")
	hasInt, hasFloat := false, false
	for _, a := range splitTopLevelAll(typ, '|') {
		switch strings.ToLower(strings.TrimSpace(a)) {
		case "int":
			hasInt = true
		case "float":
			hasFloat = true
		}
	}
	return hasFloat && !hasInt
}

// coerceFloatDefaultValue расширяет целочисленное значение по умолчанию до
// float, если этого требует объявленный тип (strict-исключение int->float).
func coerceFloatDefaultValue(v vm.Value, typ string) vm.Value {
	if !defaultCoercesToFloat(typ) {
		return v
	}
	if s, ok := v.(vm.Slot); ok && s.Type() == vm.IntType {
		return vm.MakeFloat(float64(s.ToInt()))
	}
	return v
}

// paramTypes возвращает объявленные типы параметров ("", если нет). Для
// параметров с `= null` тип становится неявно nullable (`T` -> `T|null`), как в
// Zend.
func paramTypes(params []Param) []string {
	out := make([]string, len(params))
	for i, p := range params {
		t := normalizedType(p.Type)
		if t != "" && !typeNullable(t) {
			if k, ok := defaultKind(p.Default); ok && k == "null" {
				t = "?" + t
			}
		}
		out[i] = t
	}
	return out
}

// normalizedType приводит запись типа из парсера к виду для рантайма
// (в частности, `integer` -> `int` не требуется; тип сохраняется как в PHP для
// сообщений об ошибках).
func normalizedType(t string) string { return t }

// funcNumArgs возвращает значение NumArgs для Function: отрицательное для
// вариативной функции (VM тогда использует упакованное число аргументов).
func funcNumArgs(params []Param) int {
	if n := len(params); n > 0 && params[n-1].Variadic {
		return -1
	}
	return len(params)
}

// numRequired возвращает число обязательных параметров (до первого с
// default-значением).
func numRequired(params []Param) int {
	// Zend: required_num_args = индекс последнего параметра без дефолта (+1);
	// вариативный параметр обязательным не делает.
	last := -1
	for i, p := range params {
		if p.Default == nil && !p.Variadic {
			last = i
		}
	}
	return last + 1
}

// paramImplicitlyNullable сообщает, что параметр записан в PHP5-стиле
// «Type $x = null» (тип не допускает null, дефолт — ровно null). Zend не
// считает такой параметр необязательным и не печатает для него Deprecated.
func paramImplicitlyNullable(p Param) bool {
	if p.Type == "" || p.Default == nil || typeNullable(p.Type) {
		return false
	}
	_, ok := p.Default.(*NullLit)
	return ok
}

// variadicFixed возвращает число фиксированных параметров (до `...$rest`).
func variadicFixed(params []Param) (int, bool) {
	if n := len(params); n > 0 && params[n-1].Variadic {
		return n - 1, true
	}
	return 0, false
}

// variadicByRef сообщает, что последний (вариативный) параметр by-ref.
func variadicByRef(params []Param) bool {
	n := len(params)
	return n > 0 && params[n-1].Variadic && params[n-1].ByRef
}

// namedArgCheck — результат проверки именованных аргументов вызова известной
// функции: либо переупорядоченные аргументы, либо ошибка (компиляции или
// рантайм-исключение Zend).
type namedArgCheck struct {
	args     []Expr
	fatal    string
	throwMsg string
	countMsg string
	// namedRuntime — компиляторная раскладка непригодна (вариативная функция
	// с лишними именованными аргументами: Zend складывает их в вариативный
	// массив со строковыми ключами); вызов идёт через рантайм-биндинг
	// (buildArgArray + __php_call_named).
	namedRuntime bool
}

// intrinsicDefaultExpr строит выражение для известного значения по умолчанию
// параметра встроенной функции.
func intrinsicDefaultExpr(d intrinsicDefault) Expr {
	switch d.Kind {
	case "null":
		return &NullLit{}
	case "bool":
		return &BoolLit{V: d.Str == "true"}
	case "int":
		return &IntLit{V: int64(d.Num)}
	case "float":
		return &FloatLit{V: d.Num}
	case "str":
		return &StrLit{V: d.Str}
	case "const":
		return &ConstExpr{Name: d.Str}
	}
	return &NullLit{}
}

// fillNilArgs заменяет неразобранные пропуски null-заглушками: переданные
// аргументы вычисляются до диагностики, а nil-выражение скомпилировать нельзя.
func fillNilArgs(out []Expr) {
	for i := range out {
		if out[i] == nil {
			out[i] = &NullLit{}
		}
	}
}

// reorderNamedArgsChecked раскладывает именованные аргументы по позициям
// параметров функции и проверяет Zend-диагностики (unknown/duplicate/
// positional-after-named/missing required).
func reorderNamedArgsChecked(args []Expr, names []string, required int, display string, variadic bool, sig *intrinsicSig) namedArgCheck {
	if len(names) == 0 {
		return namedArgCheck{args: args}
	}
	any := false
	for _, a := range args {
		if _, ok := a.(*NamedArgExpr); ok {
			any = true
			break
		}
	}
	if !any {
		return namedArgCheck{args: args}
	}
	idx := map[string]int{}
	for i, n := range names {
		idx[n] = i
	}
	slots := map[int]Expr{}
	pos := 0
	maxIdx := -1
	namedSeen := false
	used := map[string]bool{}
	res := namedArgCheck{}
	for _, a := range args {
		if na, ok := a.(*NamedArgExpr); ok {
			namedSeen = true
			if used[na.Name] {
				res.throwMsg = "Named parameter $" + na.Name + " overwrites previous argument"
				res.args = args
				return res
			}
			used[na.Name] = true
			i, ok := idx[na.Name]
			if !ok {
				if variadic && sig == nil {
					// Вариативная userland-функция принимает неизвестные имена
					// как именованные элементы массива (Zend). Раскладка на
					// позиции потеряла бы ключи — уходим в рантайм-биндинг.
					res.namedRuntime = true
					res.args = args
					return res
				}
				if variadic && sig != nil {
					// Встроенные вариативные функции неизвестные имена не
					// принимают (zend_handle_named_arg).
					res.countMsg = display + "() does not accept unknown named parameters"
					res.args = args
					return res
				}
				res.throwMsg = "Unknown named parameter $" + na.Name
				res.args = args
				return res
			}
			if _, exists := slots[i]; exists {
				res.throwMsg = "Named parameter $" + na.Name + " overwrites previous argument"
				res.args = args
				return res
			}
			slots[i] = na.X
			if i > maxIdx {
				maxIdx = i
			}
			pos++
			continue
		}
		if namedSeen {
			res.fatal = "Cannot use positional argument after named argument"
			return res
		}
		slots[pos] = a
		if pos > maxIdx {
			maxIdx = pos
		}
		pos++
	}
	out := make([]Expr, maxIdx+1)
	for i := 0; i <= maxIdx; i++ {
		if slots[i] == nil {
			continue // пропуск заполняется ниже
		}
		out[i] = slots[i]
	}
	for i := 0; i < required; i++ {
		if _, ok := slots[i]; !ok {
			name := ""
			if i < len(names) {
				name = " ($" + names[i] + ")"
			}
			res.countMsg = fmt.Sprintf("%s(): Argument #%d%s not passed", display, i+1, name)
			fillNilArgs(out)
			res.args = out
			return res
		}
	}
	// Пропуски (позиции между переданными аргументами): у встроенных функций
	// Zend подставляет известное значение по умолчанию, а при неизвестном —
	// ArgumentCountError (zend_handle_named_arg).
	for i := 0; i <= maxIdx; i++ {
		if out[i] != nil {
			continue
		}
		if sig != nil {
			if d, ok := sig.Defaults[i]; ok {
				out[i] = intrinsicDefaultExpr(d)
				continue
			}
			name := ""
			if i < len(names) {
				name = " ($" + names[i] + ")"
			}
			res.countMsg = fmt.Sprintf("%s(): Argument #%d%s must be passed explicitly, because the default value is not known",
				display, i+1, name)
			out[i] = &NullLit{}
			res.args = out
			return res
		}
		out[i] = &NullLit{}
	}
	res.args = out
	return res
}

// reorderNamedArgs раскладывает именованные аргументы по позициям
// параметров функции. Если именованных аргументов нет или имена параметров
// неизвестны — возвращает исходный список.
func reorderNamedArgs(args []Expr, names []string) []Expr {
	if len(names) == 0 {
		return args
	}
	any := false
	for _, a := range args {
		if _, ok := a.(*NamedArgExpr); ok {
			any = true
			break
		}
	}
	if !any {
		return args
	}
	idx := map[string]int{}
	for i, n := range names {
		idx[n] = i
	}
	slots := map[int]Expr{}
	pos := 0
	maxIdx := -1
	for _, a := range args {
		if na, ok := a.(*NamedArgExpr); ok {
			i, ok := idx[na.Name]
			if !ok {
				i = pos
			}
			slots[i] = na.X
			if i > maxIdx {
				maxIdx = i
			}
			pos++
			continue
		}
		slots[pos] = a
		if pos > maxIdx {
			maxIdx = pos
		}
		pos++
	}
	out := make([]Expr, maxIdx+1)
	for i := 0; i <= maxIdx; i++ {
		if slots[i] == nil {
			out[i] = &NullLit{}
			continue
		}
		out[i] = slots[i]
	}
	return out
}

// sensitiveMask возвращает маску параметров, помеченных #[SensitiveParameter],
// и признак того, что последний (вариативный) параметр чувствителен: тогда
// все собранные в него аргументы скрываются в трейсах.
func sensitiveMask(params []Param) ([]bool, bool) {
	var mask []bool
	any := false
	for i, p := range params {
		sens := false
		for _, at := range p.Attrs {
			if strings.EqualFold(strings.TrimLeft(at.Name, "\\"), "SensitiveParameter") {
				sens = true
			}
		}
		if sens {
			any = true
		}
		if sens || mask != nil {
			for len(mask) < i {
				mask = append(mask, false)
			}
			mask = append(mask, sens)
		}
	}
	if !any {
		return nil, false
	}
	for len(mask) < len(params) {
		mask = append(mask, false)
	}
	variadicSens := false
	if n := len(params); n > 0 && params[n-1].Variadic && mask[n-1] {
		variadicSens = true
	}
	return mask, variadicSens
}

// byrefMask возвращает маску by-ref параметров (nil, если таких нет).
func byrefMask(params []Param) []bool {
	any := false
	m := make([]bool, len(params))
	for i, p := range params {
		if p.ByRef {
			m[i] = true
			any = true
		}
	}
	if !any {
		return nil
	}
	return m
}

// mergeMask поэлементно объединяет маску by-ref (OR), возвращая новую.
func mergeMask(dst, src []bool) []bool {
	if len(src) > len(dst) {
		grown := make([]bool, len(src))
		copy(grown, dst)
		dst = grown
	}
	for i, b := range src {
		if b {
			dst[i] = true
		}
	}
	return dst
}

// markByRef вызывает addRef для простых переменных-аргументов, стоящих в
// by-ref позициях. Нужно в prescan, чтобы companion-ячейки существовали до
// компиляции (иначе регистры затрут временные).
func markByRef(args []Expr, mask []bool, addRef func(string)) {
	for i, a := range args {
		if i < len(mask) && mask[i] {
			if v, ok := a.(*VarExpr); ok {
				addRef(v.Name)
			}
		}
	}
}

// globalKey возвращает ключ глобальной таблицы для имени переменной.
func (c *compiler) globalKey(name string) string {
	if k, ok := c.staticKeys[name]; ok {
		return k
	}
	return name
}

// useGlobal: переменная живёт в глобальной таблице (main-скоуп, global/static).
func (c *compiler) useGlobal(name string) bool {
	// Суперглобалы доступны в любой области видимости.
	if superglobalNames[name] {
		return true
	}
	if c.detached[name] {
		return false
	}
	if c.allVarsGlobal && !c.localNames[name] {
		// Динамический global в теле функции: имена переменных неизвестны,
		// поэтому все обращения (кроме параметров/захватов) — к глобалам.
		return true
	}
	if c.isMain {
		// main-переменные, прошедшие анализ промоушена, живут в регистрах;
		// все прочие (суперглобалы, $GLOBALS, наблюдаемые/возможно-неопределённые)
		// — по-прежнему в VM.globals.
		return !c.promote[name]
	}
	return c.globals[name] || c.staticKeys[name] != ""
}

// loadVar читает именованную переменную в регистр.
func (c *compiler) loadVar(name string) uint8 {
	if name == "$this" || name == "this" {
		// $this живёт в слоте кадра (OpThis), а не в таблице переменных.
		r := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpThis, A: r})
		return r
	}
	if c.isRef(name) {
		if c.useGlobal(name) {
			key := c.globalKey(name)
			r := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpGlobalRefGet, A: r, C: int32(c.chunk.AddConst(vm.StrConst(key)))})
			return r
		}
		r := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpRefGet, A: r, B: c.refs[name]})
		return r
	}
	if c.useGlobal(name) {
		key := c.globalKey(name)
		r := c.alloc()
		// Переменная, объявленная через `global`, читается как ячейка
		// ссылки: её отсутствие не порождает "Undefined variable".
		silent := c.globals[name] && !c.isMain
		if silent {
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpGlobalGet, A: r, C: int32(c.chunk.AddConst(vm.StrConst(key)))})
		if silent {
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
		}
		return r
	}
	if _, ok := c.vars[name]; !ok && !c.isMain && !c.usesSymtab {
		// Неизвестная переменная внутри функции: Zend выдаёт "Undefined
		// variable" и возвращает NULL. Ключ гарантированно отсутствует в
		// таблице глобалов (значение из main-скоупа недоступно).
		r := c.alloc()
		key := "\x00undef\x00" + name
		c.chunk.Emit(vm.Instruction{Op: vm.OpGlobalGet, A: r, C: int32(c.chunk.AddConst(vm.StrConst(key)))})
		return r
	}
	// Переменная, которой в теле функции ничего не присваивалось, в Zend
	// остаётся UNDEF: чтение даёт "Undefined variable". Такой локальный
	// регистр всегда null, поэтому используем тот же путь, что и для
	// неизвестной переменной.
	if !c.isMain && !c.usesSymtab && name != "" && name != "$this" && name != "this" &&
		!c.writes[name] && !c.isRef(name) {
		if _, ok := c.vars[name]; ok {
			r := c.alloc()
			key := "\x00undef\x00" + name
			c.chunk.Emit(vm.Instruction{Op: vm.OpGlobalGet, A: r, C: int32(c.chunk.AddConst(vm.StrConst(key)))})
			return r
		}
	}
	return c.varReg(name)
}

// loadVarChecked читает переменную и, если она была снята unset()-ом,
// печатает «Undefined variable» (Zend: CV переходит в IS_UNDEF).
func (c *compiler) loadVarChecked(name string, line int) uint8 {
	r := c.loadVar(name)
	c.emitUndefCheck(name, line)
	return r
}

// emitUndefReadWarn печатает «Undefined variable» при чтении заведомо
// неопределённой переменной вне обычного пути чтения (например, базы
// `unset($x[0])`, где загрузка базы обёрнута подавлением диагностик).
func (c *compiler) emitUndefReadWarn(name string, line int) {
	if _, ok := c.defRegs[name]; ok {
		c.emitUndefCheck(name, line)
		return
	}
	if c.isRef(name) {
		return
	}
	if c.useGlobal(name) {
		r := c.alloc()
		c.emitLine(line)
		c.chunk.Emit(vm.Instruction{Op: vm.OpGlobalGet, A: r,
			C: int32(c.chunk.AddConst(vm.StrConst(c.globalKey(name))))})
	}
}

// emitUndefCheck эмитит проверку флага «переменная определена»: если он поднят// (переменная была снята unset()-ом), печатается «Undefined variable» и флаг
// опускается.
func (c *compiler) emitUndefCheck(name string, line int) {
	def, ok := c.defRegs[name]
	if !ok {
		return
	}
	jf := c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfFalse, A: def})
	c.emitLine(line)
	nm := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nm, C: int32(c.chunk.AddConst(vm.StrConst(name)))})
	c.emitCall("__php_warn_undef", []uint8{nm})
	c.chunk.Emit(vm.Instruction{Op: vm.OpFalse, A: def})
	c.setJump(jf, c.chunk.Len())
}

// clearUndef снимает флаг «переменная определена» после записи в неё.
func (c *compiler) clearUndef(name string) {
	if def, ok := c.defRegs[name]; ok {
		c.chunk.Emit(vm.Instruction{Op: vm.OpFalse, A: def})
	}
}

// setUndef поднимает флаг «переменная снята unset()-ом».
func (c *compiler) setUndef(name string) {
	if def, ok := c.defRegs[name]; ok {
		c.chunk.Emit(vm.Instruction{Op: vm.OpTrue, A: def})
	}
}

// storeVar пишет регистр в именованную переменную.
func (c *compiler) storeVar(name string, reg uint8) {
	if c.isRef(name) {
		if c.useGlobal(name) {
			key := c.globalKey(name)
			c.chunk.Emit(vm.Instruction{Op: vm.OpGlobalRefSet, A: reg, C: int32(c.chunk.AddConst(vm.StrConst(key)))})
			return
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpRefSet, A: c.refs[name], B: reg})
		return
	}
	if c.useGlobal(name) {
		key := c.globalKey(name)
		c.chunk.Emit(vm.Instruction{Op: vm.OpGlobalSet, A: reg, C: int32(c.chunk.AddConst(vm.StrConst(key)))})
		return
	}
	v := c.varReg(name)
	c.clearUndef(name)
	// В теле, где есть extract()/compact()/get_defined_vars(), переменная могла
	// динамически стать ссылкой (extract EXTR_REFS). OpRefSet пишет сквозь
	// Ref-ячейку, если регистр её содержит, иначе — обычное присваивание.
	if c.usesSymtab {
		c.chunk.Emit(vm.Instruction{Op: vm.OpRefSet, A: v, B: reg})
		return
	}
	if v != reg {
		// OpStoreVar: сборщик времени жизни отмечает потерю ссылки из
		// переменной (в отличие от пересылок-времянок OpMove).
		c.chunk.Emit(vm.Instruction{Op: vm.OpStoreVar, A: v, B: reg})
	}
}

// loadVarDyn читает переменную с динамическим именем ($$x, ${expr}).
// Ограничение: динамические переменные всегда берутся из глобальной таблицы
// (main-скоуп / global / static). В теле функции они тоже читают/пишут
// глобальную таблицу, а не локальный кадр — это компромисс (см. README-заметку
// в отчёте).
func (c *compiler) loadVarDyn(nameExpr Expr) (uint8, error) {
	ne, err := c.expr(nameExpr)
	if err != nil {
		return 0, err
	}
	d := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpGlobalGetDyn, A: d, C: int32(ne)})
	return d, nil
}

// storeVarDyn пишет регистр в переменную с динамическим именем.
func (c *compiler) storeVarDyn(nameExpr Expr, reg uint8) error {
	ne, err := c.expr(nameExpr)
	if err != nil {
		return err
	}
	c.chunk.Emit(vm.Instruction{Op: vm.OpGlobalSetDyn, A: reg, C: int32(ne)})
	return nil
}

// refCell возвращает регистр, содержащий Ref-ячейку переменной (создавая её
// при необходимости). Используется для by-ref возврата и by-ref аргументов.
// setVarRegs пиннит регистры именованных переменных чанка (SetVarRegs),
// исключая регистр объекта исключения (\x00excN): его значение VM
// переопределяет при входе в catch, поэтому «держать переменную живой»
// до конца функции нельзя — иначе значение, оставшееся в этом регистре от
// временного выражения, удерживало бы объект от __destruct
// (tests/classes/destructor_and_exceptions).
func (c *compiler) setVarRegs() {
	vars, refs := c.vars, c.refs
	for n := range c.vars {
		if strings.HasPrefix(n, "\x00exc") {
			vars = make(map[string]uint8, len(c.vars))
			for k, v := range c.vars {
				if !strings.HasPrefix(k, "\x00exc") {
					vars[k] = v
				}
			}
			break
		}
	}
	c.chunk.SetVarRegs(vars, refs)
	// Пользовательское подмножество (без внутренних времён) — для освобождения
	// временных операндов на раскрутке непойманного исключения (bug69740).
	c.chunk.SetUserVarRegs(c.vars, c.refs)
}

func (c *compiler) refCell(name string) uint8 {
	if c.useGlobal(name) {
		// Запоминаем, что переменная теперь участвует в ссылке, чтобы
		// последующие чтения/записи шли через ячейку, а не переписывали
		// глобальную запись значением.
		if !c.isRef(name) {
			c.refs[name] = 0xFF
		}
		r := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpGlobalRefAddr, A: r, C: int32(c.chunk.AddConst(vm.StrConst(c.globalKey(name))))})
		return r
	}
	reg, ok := c.refs[name]
	if !ok {
		reg = c.ensureRefReg(name)
	}
	c.chunk.Emit(vm.Instruction{Op: vm.OpRefEnsure, A: reg})
	return reg
}

func (c *compiler) alloc() uint8 {
	r := c.tmp
	c.tmp++
	if c.tmp > c.max {
		c.max = c.tmp
	}
	return r
}

func (c *compiler) varReg(name string) uint8 {
	if r, ok := c.vars[name]; ok {
		return r
	}
	// Обычно все локальные переменные уже назначены prescan-проходом. Сюда
	// попадаем лишь для внутренних переменных; НЕ сбрасываем c.tmp назад,
	// иначе затрём уже выданные временные регистры.
	r := c.nvars
	c.nvars++
	if c.tmp < c.nvars {
		c.tmp = c.nvars
	}
	if c.nvars > c.max {
		c.max = c.nvars
	}
	c.vars[name] = r
	return r
}

func (c *compiler) internalName(kind string) string {
	c.internal++
	return "\x00" + kind + fmt.Sprint(c.internal)
}

// buildSymtab записывает в чанк локальную таблицу символов (имя→регистр и
// global/static имя→ключ глобалов) для compact()/extract()/get_defined_vars().
// includeGlobals — включать ли global/static переменные (для функций). Имена
// без ведущего `$`; внутренние (с `\x00`) исключаются.
func (c *compiler) buildSymtab(includeGlobals bool) {
	lv := c.namedRegsMap()
	c.chunk.SetLocalVars(lv)
	c.chunk.SetNamedRegs(lv)
	if !includeGlobals {
		return
	}
	gv := make(map[string]string, len(c.globals)+len(c.staticKeys))
	for name := range c.globals {
		bn := strings.TrimPrefix(name, "$")
		if bn == "" {
			continue
		}
		gv[bn] = c.globalKey(name)
	}
	for name := range c.staticKeys {
		bn := strings.TrimPrefix(name, "$")
		if bn == "" {
			continue
		}
		gv[bn] = c.staticKeys[name]
	}
	c.chunk.SetGlobalVars(gv)
}

// namedRegsMap строит карту имя→регистр всех именованных переменных (без `$`,
// без внутренних имён). Используется для таблицы символов и подсчёта ссылок.
func (c *compiler) namedRegsMap() map[string]int {
	lv := make(map[string]int, len(c.vars))
	for name, reg := range c.vars {
		if name == "" || name[0] == '\x00' {
			continue
		}
		bn := strings.TrimPrefix(name, "$")
		if bn == "" || bn == "this" {
			continue
		}
		lv[bn] = int(reg)
	}
	return lv
}

// prescan назначает регистры всем локальным переменным тела до компиляции.
// Иначе первое появление переменной в середине выражения могло бы «отдать»
// регистр, занятый уже вычисленным временным значением (затирание).
// markWriteTarget отмечает переменную как записываемую по цели присваивания:
// `$x = ...` и `$x[...] = ...` (append/по ключу) пишут в переменную, а
// `$x->p = ...` — читают её (значение должно быть объектом).
func markWriteTarget(t Expr, mark func(string)) {
	switch v := t.(type) {
	case *VarExpr:
		if v.NameExpr == nil {
			mark(v.Name)
		}
	case *IndexExpr:
		if arr, ok := v.Arr.(*VarExpr); ok && arr.NameExpr == nil {
			mark(arr.Name)
		}
	}
}

func (c *compiler) prescan(stmts []Stmt) {
	seen := map[string]bool{}
	for k := range c.vars {
		seen[k] = true
	}
	if c.writes == nil {
		c.writes = map[string]bool{}
	}
	undefTargets := map[string]bool{}
	// readCnt — счётчик чтений переменной в теле (см. undefInit).
	readCnt := map[string]int{}
	markRead := func(name string) {
		if name != "" {
			readCnt[name]++
		}
	}
	// markWrite отмечает переменную как записываемую: только такие локальные
	// читаются из своего регистра, остальные (только читаемые) в Zend остаются
	// UNDEF и дают "Undefined variable".
	markWrite := func(name string) {
		if name != "" && !c.isRef(name) && !c.useGlobal(name) {
			c.writes[name] = true
		}
	}
	add := func(name string) {
		if name == "" || c.useGlobal(name) || seen[name] {
			return
		}
		seen[name] = true
		c.vars[name] = c.nvars
		c.nvars++
	}
	// addRef регистрирует переменную как self-describing ячейку ссылки. У
	// локальных переменных ячейка — это их собственный регистр (для by-val
	// параметра — регистр параметра), поэтому значение не теряется. Для
	// глобальных/main-переменных регистр не нужен.
	addRef := func(name string) {
		if name == "" || c.isRef(name) {
			return
		}
		if c.useGlobal(name) {
			c.refs[name] = 0xFF
			return
		}
		if r, ok := c.vars[name]; ok {
			c.refs[name] = r
			return
		}
		seen[name] = true
		c.refs[name] = c.nvars
		c.vars[name] = c.nvars
		c.nvars++
	}
	var walkE func(Expr)
	var walkS func(Stmt)
	// walkTarget обходит lvalue-позицию (цель присваивания/unset/isset):
	// база-переменная здесь НЕ чтение, но вложенные ключи/имена читаются.
	var walkTarget func(Expr)
	walkE = func(e Expr) {
		switch x := e.(type) {
		case nil:
		case *VarExpr:
			add(x.Name)
			markRead(x.Name)
			if x.NameExpr != nil {
				walkE(x.NameExpr)
			}
		case *AssignExpr:
			markWriteTarget(x.Target, markWrite)
			if le, ok := x.Target.(*ListExpr); ok {
				for _, el := range le.Elems {
					markWriteTarget(el.Target, markWrite)
				}
			}
			walkTarget(x.Target)
			walkE(x.Value)
			if x.ByRef {
				if v, ok := x.Target.(*VarExpr); ok {
					addRef(v.Name)
				}
				if v, ok := x.Value.(*VarExpr); ok {
					addRef(v.Name)
				}
			}
		case *IndexExpr:
			walkE(x.Arr)
			walkE(x.Key)
		case *BinExpr:
			walkE(x.L)
			walkE(x.R)
		case *LogicalExpr:
			walkE(x.L)
			walkE(x.R)
		case *UnaryExpr:
			walkE(x.X)
		case *IncDecExpr:
			markWriteTarget(x.X, markWrite)
			walkE(x.X)
		case *TernaryExpr:
			walkE(x.Cond)
			walkE(x.Then)
			walkE(x.Else)
		case *CoalesceExpr:
			// `$x ?? ...` не печатает «Undefined variable» (isset-семантика).
			walkTarget(x.L)
			walkE(x.R)
		case *YieldExpr:
			walkE(x.Key)
			walkE(x.Val)
		case *CallExpr:
			// compact()/extract()/get_defined_vars(): переменные живут в
			// таблице символов кадра; пометка нужна уже на этапе prescan —
			// от неё зависит выделение флагов определённости (undefInit).
			for _, nm := range []string{x.Name, x.Fallback} {
				switch strings.ToLower(strings.TrimLeft(nm, "\\")) {
				case "compact", "extract", "get_defined_vars":
					c.usesSymtab = true
				}
			}
			var cmask []bool
			if fi, ok := c.idx[x.Name]; ok {
				cmask = c.funcs[fi].ByRefArgs
			}
			for i, a := range x.Args {
				if i < len(cmask) && cmask[i] {
					walkTarget(a)
				} else {
					walkE(a)
				}
			}
			markByRef(x.Args, cmask, addRef)
		case *NamedArgExpr:
			walkE(x.X)
		case *MethodCallExpr:
			walkE(x.Recv)
			if x.NameExpr != nil {
				walkE(x.NameExpr)
			}
			mmask := c.methodRefs[x.Name]
			for i, a := range x.Args {
				if i < len(mmask) && mmask[i] {
					walkTarget(a)
				} else {
					walkE(a)
				}
			}
			markByRef(x.Args, mmask, addRef)
		case *StaticCallExpr:
			if x.NameExpr != nil {
				walkE(x.NameExpr)
			}
			smask := c.methodRefs[x.Name]
			for i, a := range x.Args {
				if i < len(smask) && smask[i] {
					walkTarget(a)
				} else {
					walkE(a)
				}
			}
			markByRef(x.Args, smask, addRef)
		case *NewExpr:
			if x.ClassExpr != nil {
				walkE(x.ClassExpr)
			}
			nmask := c.methodRefs["__construct"]
			for i, a := range x.Args {
				if i < len(nmask) && nmask[i] {
					walkTarget(a)
				} else {
					walkE(a)
				}
			}
			markByRef(x.Args, nmask, addRef)
		case *NewAnonClassExpr:
			for _, a := range x.Args {
				walkE(a)
			}
		case *DynCallExpr:
			walkE(x.Name)
			for _, a := range x.Args {
				walkE(a)
			}
		case *PropExpr:
			walkE(x.Recv)
			if x.NameExpr != nil {
				walkE(x.NameExpr)
			}
		case *StaticPropExpr:
			if x.NameExpr != nil {
				walkE(x.NameExpr)
			}
		case *ArrayLit:
			for _, el := range x.Elems {
				walkE(el.Key)
				walkE(el.Val)
			}
		case *MatchExpr:
			walkE(x.Subject)
			for _, arm := range x.Arms {
				for _, cond := range arm.Conds {
					walkE(cond)
				}
				walkE(arm.Body)
			}
		case *NullsafeExpr:
			walkE(x.Recv)
			walkE(x.Body)
		case *NullsafeBase:
		case *CastExpr:
			walkE(x.X)
		case *ListExpr:
			for _, el := range x.Elems {
				walkE(el.Key)
				walkTarget(el.Target)
			}
		case *IssetExpr:
			for _, a := range x.Args {
				walkTarget(a)
			}
		case *EmptyExpr:
			walkTarget(x.X)
		case *PrintExpr:
			walkE(x.X)
		case *ExitExpr:
			walkE(x.X)
		case *CloneExpr:
			walkE(x.X)
		case *ThrowExpr:
			walkE(x.X)
		case *InstanceOfExpr:
			walkE(x.X)
			if x.ClassExpr != nil {
				walkE(x.ClassExpr)
			}
		case *ClosureExpr:
			for _, u := range x.Uses {
				if u.ByRef {
					addRef(u.Name)
				} else {
					add(u.Name)
				}
			}
		case *ArrowFnExpr:
			for _, n := range arrowFreeVars(x.Body, x.Params) {
				add(n)
			}
		}
	}
	walkTarget = func(e Expr) {
		switch x := e.(type) {
		case nil:
		case *VarExpr:
			add(x.Name)
			if x.NameExpr != nil {
				walkE(x.NameExpr)
			}
		case *IndexExpr:
			walkTarget(x.Arr)
			walkE(x.Key)
		case *ListExpr:
			for _, el := range x.Elems {
				walkE(el.Key)
				walkTarget(el.Target)
			}
		default:
			// Чтение базы (свойство объекта, статическое свойство и т.п.).
			walkE(e)
		}
	}
	walkS = func(st Stmt) {
		switch s := st.(type) {
		case nil:
		case *BlockStmt:
			for _, x := range s.Body {
				walkS(x)
			}
		case *EchoStmt:
			for _, a := range s.Args {
				walkE(a)
			}
		case *ExprStmt:
			walkE(s.X)
		case *IfStmt:
			walkE(s.Cond)
			for _, x := range s.Then {
				walkS(x)
			}
			for _, x := range s.Else {
				walkS(x)
			}
		case *WhileStmt:
			walkE(s.Cond)
			for _, x := range s.Body {
				walkS(x)
			}
		case *DoWhileStmt:
			for _, x := range s.Body {
				walkS(x)
			}
			walkE(s.Cond)
		case *ForStmt:
			for _, e := range s.Init {
				walkE(e)
			}
			for _, e := range s.Cond {
				walkE(e)
			}
			for _, e := range s.Post {
				walkE(e)
			}
			for _, x := range s.Body {
				walkS(x)
			}
		case *ForeachStmt:
			walkE(s.Expr)
			add(s.Key)
			add(s.Val)
			markWrite(s.Key)
			markWrite(s.Val)
			if s.KeyTarget != nil {
				walkE(s.KeyTarget)
			}
			if s.ValTarget != nil {
				walkE(s.ValTarget)
			}
			if s.ByRef {
				addRef(s.Val)
			}
			for _, x := range s.Body {
				walkS(x)
			}
		case *SwitchStmt:
			walkE(s.Subject)
			for _, cs := range s.Cases {
				walkE(cs.Cond)
				for _, x := range cs.Body {
					walkS(x)
				}
			}
		case *ReturnStmt:
			walkE(s.X)
			if c.byrefReturn {
				if v, ok := s.X.(*VarExpr); ok {
					addRef(v.Name)
				}
			}
		case *ThrowStmt:
			walkE(s.X)
		case *TryStmt:
			for _, x := range s.Body {
				walkS(x)
			}
			for _, cc := range s.Catches {
				add(cc.Var)
				markWrite(cc.Var)
				for _, x := range cc.Body {
					walkS(x)
				}
			}
			for _, x := range s.Finally {
				walkS(x)
			}
		case *UnsetStmt:
			for _, a := range s.Args {
				if v, ok := a.(*VarExpr); ok && v.NameExpr == nil && v.Name != "$this" && v.Name != "this" &&
					!c.isRef(v.Name) && !c.useGlobal(v.Name) && v.Name != "$GLOBALS" {
					// Простой unset($x) локальной переменной: чтение после него
					// обязано напечатать «Undefined variable $x».
					undefTargets[v.Name] = true
				}
				walkTarget(a)
			}
		}
	}
	for _, st := range stmts {
		walkS(st)
	}
	// Регистры-флаги «определена» для целей unset(). Выделяются последними,
	// чтобы временные регистры выражений их не перекрыли.
	allocDef := func(name string) bool {
		if _, ok := c.defRegs[name]; ok {
			return true
		}
		if _, ok := c.vars[name]; !ok {
			// Переменной нет в кадре (main-скоуп/global) — флаг не нужен.
			return false
		}
		if c.nvars >= 240 {
			// Защита от переполнения uint8-регистров в очень больших
			// функциях: без флага диагностика просто не печатается.
			return false
		}
		c.defRegs[name] = c.nvars
		c.nvars++
		return true
	}
	undefNames := make([]string, 0, len(undefTargets))
	for name := range undefTargets {
		undefNames = append(undefNames, name)
	}
	sort.Strings(undefNames)
	for _, name := range undefNames {
		allocDef(name)
	}
	// Переменные с чтением до записи (и записью где-то в теле): в Zend такая
	// CV в первой точке чтения находится в состоянии UNDEF и печатает
	// «Undefined variable». Флаг инициализируется true в прологе, каждая
	// запись его опускает (clearUndef).
	if !c.isMain {
		initNames := make([]string, 0, len(readCnt))
		for name, cnt := range readCnt {
			// В функциях с extract()/compact() переменная может быть
			// определена в рантайме: флаг нужен и без статической записи
			// (SymtabSet опускает его), иначе чтение недиагностируется.
			if cnt == 0 || (!c.writes[name] && !c.usesSymtab) || c.isRef(name) || c.useGlobal(name) {
				continue
			}
			// Параметры/захваты определены всегда (регистры ниже nvars0).
			if r, ok := c.vars[name]; ok && int(r) < int(c.nvars0) {
				continue
			}
			if name == "$this" || name == "this" || name == "$GLOBALS" || name == "GLOBALS" {
				continue
			}
			if _, isStatic := c.staticKeys[name]; isStatic || c.localStatics[name] {
				continue
			}
			initNames = append(initNames, name)
		}
		sort.Strings(initNames)
		for _, name := range initNames {
			if allocDef(name) {
				c.undefInit = append(c.undefInit, c.defRegs[name])
			}
		}
	}
	if c.nvars > c.max {
		c.max = c.nvars
	}
	c.tmp = c.nvars
}

func (c *compiler) emit(in vm.Instruction) int { return c.chunk.Emit(in) }

// inlineCtx — открытый регион вклеенного include: карты строк его файла и
// индекс региона в чанке (для атрибуции инструкций файлу).
type inlineCtx struct {
	lines  map[Stmt]int
	exprs  map[Expr]int
	region int32
}

// inlineLine ищет строку узла в открытых inline-регионах (самый внутренний
// первым): узлы вклеенного файла не попадают в stmtLines главного файла.
func (c *compiler) inlineLine(st Stmt) int {
	for i := len(c.inlineStack) - 1; i >= 0; i-- {
		if ln, ok := c.inlineStack[i].lines[st]; ok && ln > 0 {
			return ln
		}
	}
	return 0
}

// inlineExprLine как inlineLine, но для выражений.
func (c *compiler) inlineExprLine(e Expr) int {
	for i := len(c.inlineStack) - 1; i >= 0; i-- {
		if ln, ok := c.inlineStack[i].exprs[e]; ok && ln > 0 {
			return ln
		}
	}
	return 0
}

// emitLine задаёт строку для последующих инструкций (если известна).
func (c *compiler) emitLine(line int) {
	if line > 0 {
		c.chunk.SetLine(line)
	}
}

// exprLine возвращает строку выражения (или текущую строку оператора).
func (c *compiler) exprLine(e Expr) int {
	if c.exprLines != nil && e != nil {
		if ln, ok := c.exprLines[e]; ok && ln > 0 {
			return ln
		}
	}
	if e != nil {
		if ln := c.inlineExprLine(e); ln > 0 {
			return ln
		}
	}
	return c.curLine
}

// warnLine эмитит OpLine с корректной строкой перед диагностикой для e.
func (c *compiler) warnLine(e Expr) { c.emitLine(c.exprLine(e)) }

func (c *compiler) setJump(at int, pc int) {
	// Fused-сравнения хранят адрес перехода в отдельной таблице (C занят
	// индексом константы или иммедиатом).
	op := c.chunk.At(at).Op
	if op == vm.OpJumpIfLessIntK || op == vm.OpJumpIfLessIntImm {
		c.chunk.SetJumpTable(int(c.chunk.At(at).A), int32(pc))
		return
	}
	c.chunk.SetJump(at, int32(pc))
}

// emitCondJFalse компилирует условие и условный переход «если ложно».
// Сворачивает `$x < intK` в один fused-опкод.
func (c *compiler) emitCondJFalse(cond Expr) (int, error) {
	if b, ok := cond.(*BinExpr); ok && b.Op == "<" {
		if k, ok2 := numConst(b.R); ok2 && k.Type() == vm.IntType {
			l, err := c.expr(b.L)
			if err != nil {
				return 0, err
			}
			ji := c.chunk.AddJump()
			if il, ok := b.R.(*IntLit); ok && il.V >= math.MinInt32 && il.V <= math.MaxInt32 {
				return c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfLessIntImm, A: uint8(ji), B: l, C: int32(il.V)}), nil
			}
			return c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfLessIntK, A: uint8(ji), B: l, C: int32(c.chunk.AddConst(k))}), nil
		}
	}
	r, err := c.expr(cond)
	if err != nil {
		return 0, err
	}
	return c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfFalse, A: r}), nil
}

// Compile парсит и компилирует исходник PHP в опкоды.
func Compile(src string) (*Compiled, error) { return CompileFile(src, "") }

// --- include/require/eval: инлайнинг литеральных аргументов ---

// inlineStmts рекурсивно раскрывает include/require с литеральным путём и
// eval с литеральной строкой, подставляя разобранный код на место конструкции.
// file — путь текущего компилируемого файла (для относительных include и
// магических констант __DIR__/__FILE__). inc — множество уже подключённых
// абсолютных путей (для *_once).
// inlineDepth ограничивает глубину раскрытия include/require/eval на этапе
// компиляции. При превышении конструкция остаётся нераскрытой и обрабатывается
// в рантайме (это корректно для рекурсивных include, зависящих от условий).
var inlineDepth int

// inliningNow отслеживает файлы, раскрываемые прямо сейчас (защита от циклов).
var inliningNow = map[string]bool{}

func inlineStmts(stmts []Stmt, file string, inc map[string]bool) ([]Stmt, error) {
	if inlineDepth > 30 {
		return stmts, nil
	}
	inlineDepth++
	defer func() { inlineDepth-- }()
	out := make([]Stmt, 0, len(stmts))
	for _, st := range stmts {
		ns, err := inlineStmt(st, file, inc)
		if err != nil {
			return nil, err
		}
		out = append(out, ns...)
	}
	return out, nil
}

func inlineStmt(st Stmt, file string, inc map[string]bool) ([]Stmt, error) {
	switch s := st.(type) {
	case nil:
		return nil, nil
	case *ExprStmt:
		x, pre, err := inlineExpr(s.X, file, inc)
		if err != nil {
			return nil, err
		}
		s.X = x
		return append(pre, s), nil
	case *EchoStmt:
		pre, err := inlineExprs(s.Args, file, inc)
		if err != nil {
			return nil, err
		}
		return append(pre, s), nil
	case *ReturnStmt:
		if s.X == nil {
			return []Stmt{s}, nil
		}
		x, pre, err := inlineExpr(s.X, file, inc)
		if err != nil {
			return nil, err
		}
		s.X = x
		return append(pre, s), nil
	case *ThrowStmt:
		x, pre, err := inlineExpr(s.X, file, inc)
		if err != nil {
			return nil, err
		}
		s.X = x
		return append(pre, s), nil
	case *UnsetStmt:
		pre, err := inlineExprs(s.Args, file, inc)
		if err != nil {
			return nil, err
		}
		return append(pre, s), nil
	case *IfStmt:
		x, pre, err := inlineExpr(s.Cond, file, inc)
		if err != nil {
			return nil, err
		}
		s.Cond = x
		if s.Then, err = inlineStmts(s.Then, file, inc); err != nil {
			return nil, err
		}
		if s.Else, err = inlineStmts(s.Else, file, inc); err != nil {
			return nil, err
		}
		return append(pre, s), nil
	case *WhileStmt:
		x, pre, err := inlineExpr(s.Cond, file, inc)
		if err != nil {
			return nil, err
		}
		s.Cond = x
		if s.Body, err = inlineStmts(s.Body, file, inc); err != nil {
			return nil, err
		}
		return append(pre, s), nil
	case *DoWhileStmt:
		var err error
		if s.Body, err = inlineStmts(s.Body, file, inc); err != nil {
			return nil, err
		}
		x, pre, err := inlineExpr(s.Cond, file, inc)
		if err != nil {
			return nil, err
		}
		s.Cond = x
		return append(pre, s), nil
	case *ForStmt:
		pre, err := inlineExprs(s.Init, file, inc)
		if err != nil {
			return nil, err
		}
		p2, err := inlineExprs(s.Cond, file, inc)
		if err != nil {
			return nil, err
		}
		p3, err := inlineExprs(s.Post, file, inc)
		if err != nil {
			return nil, err
		}
		if s.Body, err = inlineStmts(s.Body, file, inc); err != nil {
			return nil, err
		}
		pre = append(pre, p2...)
		pre = append(pre, p3...)
		return append(pre, s), nil
	case *ForeachStmt:
		x, pre, err := inlineExpr(s.Expr, file, inc)
		if err != nil {
			return nil, err
		}
		s.Expr = x
		if s.Body, err = inlineStmts(s.Body, file, inc); err != nil {
			return nil, err
		}
		return append(pre, s), nil
	case *SwitchStmt:
		x, pre, err := inlineExpr(s.Subject, file, inc)
		if err != nil {
			return nil, err
		}
		s.Subject = x
		for _, cs := range s.Cases {
			if cs.Cond != nil {
				ce, cp, err := inlineExpr(cs.Cond, file, inc)
				if err != nil {
					return nil, err
				}
				cs.Cond = ce
				pre = append(pre, cp...)
			}
			if cs.Body, err = inlineStmts(cs.Body, file, inc); err != nil {
				return nil, err
			}
		}
		return append(pre, s), nil
	case *BlockStmt:
		body, err := inlineStmts(s.Body, file, inc)
		if err != nil {
			return nil, err
		}
		s.Body = body
		return []Stmt{s}, nil
	case *TryStmt:
		var err error
		if s.Body, err = inlineStmts(s.Body, file, inc); err != nil {
			return nil, err
		}
		for _, c := range s.Catches {
			if c.Body, err = inlineStmts(c.Body, file, inc); err != nil {
				return nil, err
			}
		}
		if s.Finally, err = inlineStmts(s.Finally, file, inc); err != nil {
			return nil, err
		}
		return []Stmt{s}, nil
	case *FuncDecl:
		body, err := inlineStmts(s.Body, file, inc)
		if err != nil {
			return nil, err
		}
		s.Body = body
		return []Stmt{s}, nil
	case *ClassDecl:
		for _, m := range s.Members {
			if len(m.Body) > 0 {
				body, err := inlineStmts(m.Body, file, inc)
				if err != nil {
					return nil, err
				}
				m.Body = body
			}
		}
		return []Stmt{s}, nil
	case *StaticVarStmt:
		pre, err := inlineExprs(s.Default, file, inc)
		if err != nil {
			return nil, err
		}
		return append(pre, s), nil
	default:
		return []Stmt{st}, nil
	}
}

// inlineExprs раскрывает include/eval в наборе выражений, возвращая
// утверждения, которые нужно выполнить перед содержащим оператором.
func inlineExprs(es []Expr, file string, inc map[string]bool) ([]Stmt, error) {
	var pre []Stmt
	for i, e := range es {
		x, p, err := inlineExpr(e, file, inc)
		if err != nil {
			return nil, err
		}
		pre = append(pre, p...)
		es[i] = x
	}
	return pre, nil
}

func inlineExpr(e Expr, file string, inc map[string]bool) (Expr, []Stmt, error) {
	switch x := e.(type) {
	case nil:
		return nil, nil, nil
	case *IncludeExpr:
		return inlineInclude(x, file, inc)
	case *CallExpr:
		if x.Name == "eval" {
			return inlineEval(x, file, inc)
		}
		pre, err := inlineExprs(x.Args, file, inc)
		if err != nil {
			return nil, nil, err
		}
		return x, pre, nil
	case *BinExpr:
		l, p1, err := inlineExpr(x.L, file, inc)
		if err != nil {
			return nil, nil, err
		}
		r, p2, err := inlineExpr(x.R, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.L, x.R = l, r
		return x, append(p1, p2...), nil
	case *LogicalExpr:
		l, p1, err := inlineExpr(x.L, file, inc)
		if err != nil {
			return nil, nil, err
		}
		r, p2, err := inlineExpr(x.R, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.L, x.R = l, r
		return x, append(p1, p2...), nil
	case *AssignExpr:
		t, p1, err := inlineExpr(x.Target, file, inc)
		if err != nil {
			return nil, nil, err
		}
		v, p2, err := inlineExpr(x.Value, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.Target, x.Value = t, v
		return x, append(p1, p2...), nil
	case *TernaryExpr:
		c, p1, err := inlineExpr(x.Cond, file, inc)
		if err != nil {
			return nil, nil, err
		}
		t, p2, err := inlineExpr(x.Then, file, inc)
		if err != nil {
			return nil, nil, err
		}
		f, p3, err := inlineExpr(x.Else, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.Cond, x.Then, x.Else = c, t, f
		return x, append(append(p1, p2...), p3...), nil
	case *CoalesceExpr:
		l, p1, err := inlineExpr(x.L, file, inc)
		if err != nil {
			return nil, nil, err
		}
		r, p2, err := inlineExpr(x.R, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.L, x.R = l, r
		return x, append(p1, p2...), nil
	case *YieldExpr:
		k, p1, err := inlineExpr(x.Key, file, inc)
		if err != nil {
			return nil, nil, err
		}
		v, p2, err := inlineExpr(x.Val, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.Key, x.Val = k, v
		return x, append(p1, p2...), nil
	case *UnaryExpr:
		v, p, err := inlineExpr(x.X, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.X = v
		return x, p, nil
	case *IncDecExpr:
		v, p, err := inlineExpr(x.X, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.X = v
		return x, p, nil
	case *CastExpr:
		v, p, err := inlineExpr(x.X, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.X = v
		return x, p, nil
	case *IndexExpr:
		a, p1, err := inlineExpr(x.Arr, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.Arr = a
		if x.Key != nil {
			k, p2, err := inlineExpr(x.Key, file, inc)
			if err != nil {
				return nil, nil, err
			}
			x.Key = k
			p1 = append(p1, p2...)
		}
		return x, p1, nil
	case *PropExpr:
		r, p, err := inlineExpr(x.Recv, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.Recv = r
		return x, p, nil
	case *MethodCallExpr:
		r, p1, err := inlineExpr(x.Recv, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.Recv = r
		p2, err := inlineExprs(x.Args, file, inc)
		if err != nil {
			return nil, nil, err
		}
		return x, append(p1, p2...), nil
	case *StaticCallExpr:
		p, err := inlineExprs(x.Args, file, inc)
		if err != nil {
			return nil, nil, err
		}
		return x, p, nil
	case *NewExpr:
		var pre []Stmt
		if x.ClassExpr != nil {
			ce, p, err := inlineExpr(x.ClassExpr, file, inc)
			if err != nil {
				return nil, nil, err
			}
			x.ClassExpr = ce
			pre = append(pre, p...)
		}
		p, err := inlineExprs(x.Args, file, inc)
		if err != nil {
			return nil, nil, err
		}
		return x, append(pre, p...), nil
	case *DynCallExpr:
		n, p1, err := inlineExpr(x.Name, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.Name = n
		p2, err := inlineExprs(x.Args, file, inc)
		if err != nil {
			return nil, nil, err
		}
		return x, append(p1, p2...), nil
	case *MatchExpr:
		var pre []Stmt
		s, p, err := inlineExpr(x.Subject, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.Subject = s
		pre = append(pre, p...)
		for i := range x.Arms {
			for j := range x.Arms[i].Conds {
				c, p, err := inlineExpr(x.Arms[i].Conds[j], file, inc)
				if err != nil {
					return nil, nil, err
				}
				x.Arms[i].Conds[j] = c
				pre = append(pre, p...)
			}
			b, p, err := inlineExpr(x.Arms[i].Body, file, inc)
			if err != nil {
				return nil, nil, err
			}
			x.Arms[i].Body = b
			pre = append(pre, p...)
		}
		return x, pre, nil
	case *NullsafeExpr:
		r, p1, err := inlineExpr(x.Recv, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.Recv = r
		b, p2, err := inlineExpr(x.Body, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.Body = b
		return x, append(p1, p2...), nil
	case *NullsafeBase:
		return x, nil, nil
	case *ArrayLit:
		var pre []Stmt
		for i := range x.Elems {
			if x.Elems[i].Key != nil {
				k, p, err := inlineExpr(x.Elems[i].Key, file, inc)
				if err != nil {
					return nil, nil, err
				}
				x.Elems[i].Key = k
				pre = append(pre, p...)
			}
			v, p, err := inlineExpr(x.Elems[i].Val, file, inc)
			if err != nil {
				return nil, nil, err
			}
			x.Elems[i].Val = v
			pre = append(pre, p...)
		}
		return x, pre, nil
	case *IssetExpr:
		p, err := inlineExprs(x.Args, file, inc)
		if err != nil {
			return nil, nil, err
		}
		return x, p, nil
	case *EmptyExpr:
		v, p, err := inlineExpr(x.X, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.X = v
		return x, p, nil
	case *PrintExpr:
		v, p, err := inlineExpr(x.X, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.X = v
		return x, p, nil
	case *ExitExpr:
		if x.X == nil {
			return x, nil, nil
		}
		v, p, err := inlineExpr(x.X, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.X = v
		return x, p, nil
	case *CloneExpr:
		v, p, err := inlineExpr(x.X, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.X = v
		return x, p, nil
	case *ThrowExpr:
		v, p, err := inlineExpr(x.X, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.X = v
		return x, p, nil
	case *InstanceOfExpr:
		v, p, err := inlineExpr(x.X, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.X = v
		if x.ClassExpr != nil {
			ce, p2, cerr := inlineExpr(x.ClassExpr, file, inc)
			if cerr != nil {
				return nil, nil, cerr
			}
			x.ClassExpr = ce
			p = append(p, p2...)
		}
		return x, p, nil
	case *ClosureExpr:
		body, err := inlineStmts(x.Body, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.Body = body
		return x, nil, nil
	case *ArrowFnExpr:
		v, p, err := inlineExpr(x.Body, file, inc)
		if err != nil {
			return nil, nil, err
		}
		x.Body = v
		return x, p, nil
	default:
		return e, nil, nil
	}
}

// inlineInclude раскрывает include/require с литеральным путём.
// Динамический путь и файлы с синтаксической ошибкой остаются рантайму.
func inlineInclude(x *IncludeExpr, file string, inc map[string]bool) (Expr, []Stmt, error) {
	p, ok := constString(x.Path, file)
	if !ok {
		return x, nil, nil
	}
	abs := resolveIncludePath(p, file)
	// URL-обёртки (в т.ч. пользовательские) не инлайнятся: их нельзя прочитать
	// на этапе компиляции, и они подчиняются allow_url_include/allow_url_fopen.
	if strings.Contains(abs, "://") {
		return x, nil, nil
	}
	// Самовключение/цикл include: не раскрываем на компиляции (иначе
	// экспоненциальное разворачивание и OOM) — оставляем рантайму.
	if abs == "" || abs == filepath.Clean(file) || inliningNow[abs] {
		return x, nil, nil
	}
	if x.Once && inc[abs] {
		return &IntLit{V: 1}, nil, nil
	}
	// Каталог нельзя инлайнить: PHP предупреждает «Failed to open stream»
	// и «Failed opening ... for inclusion» в рантайме.
	if fi, serr := os.Stat(abs); serr == nil && fi.IsDir() {
		return x, nil, nil
	}
	inliningNow[abs] = true
	defer delete(inliningNow, abs)
	data, err := os.ReadFile(abs)
	if err != nil {
		// Не найден на этапе компиляции: возможно, будет найден по
		// include_path (в т.ч. через пользовательские обёртки) — отдаём рантайму.
		return x, nil, nil
	}
	sub, err := ParseMixed(string(data))
	if err != nil {
		return x, nil, nil
	}
	stmts, err := inlineStmts(sub.Stmts, abs, inc)
	if err != nil {
		return nil, nil, err
	}
	if x.Once {
		inc[abs] = true
	}
	val, pre := splitTopReturn(stmts, &IntLit{V: 1})
	// Место объявления функций подключаемого файла: строки из sub.Lines
	// (в общий stmtLines главной программы они не попадают).
	for _, st := range stmts {
		if fd, ok := st.(*FuncDecl); ok && fd.DefLine == 0 {
			fd.DefFile = abs
			fd.DefLine = sub.Lines[fd]
		}
	}
	// Строгость типов у подключаемого файла своя: оборачиваем inlined-код в
	// регион с его режимом (см. StrictStmt), чтобы вызовы внутри сохраняли
	// weak/strict своего файла, а не файла-включателя.
	// Первым делом регистрируем файл в get_included_files() (как рантайм
	// include; inline-подстановка не проходит через includeBuiltin).
	record := &ExprStmt{X: &CallExpr{Name: "__php_record_included", Args: []Expr{&StrLit{V: abs}}}}
	callName := "include"
	if x.Require {
		callName = "require"
	}
	if x.Once {
		callName += "_once"
	}
	pre = append([]Stmt{record, &StrictStmt{Push: true, Strict: sub.StrictTypes, File: abs,
		Lines: sub.Lines, ExprLines: sub.ExprLines, CallName: callName, CallNode: x}}, pre...)
	pre = append(pre, &StrictStmt{Push: false})
	return val, pre, nil
}

// runtimeClassAliases — псевдонимы классов (class_alias), созданные к моменту
// рантайм-компиляции eval/include: alias (нижний регистр) → имя целевого
// класса. Устанавливается на время compileFileModeLink в runSubCode.
var runtimeClassAliases map[string]string

// inlineEvalParents — имена классов (в нижнем регистре), которые в текущем
// компилируемом юните используются как родитель/интерфейс/трейт. Классы,
// объявляемые через eval(), нельзя инлайнить, если их нет в этом множестве:
// инлайн делает их видимыми до вызова eval() и переносит deprecation-сообщения
// объявления в начало скрипта. Если же на такой класс ссылается extends/
// implements другого класса, инлайн нужен для раннего связывания.
var inlineEvalParents map[string]bool

// collectParentNames собирает имена родителей/интерфейсов/трейтов всех
// объявлений классов в потоке операторов (рекурсивно по блокам).
func collectParentNames(stmts []Stmt, out map[string]bool) {
	add := func(n string) {
		if n != "" {
			out[strings.ToLower(strings.TrimLeft(n, "\\"))] = true
		}
	}
	var walk func(ss []Stmt)
	walk = func(ss []Stmt) {
		for _, st := range ss {
			switch s := st.(type) {
			case *ClassDecl:
				add(s.Parent)
				for _, i := range s.Interfaces {
					add(i)
				}
				for _, t := range s.Uses {
					add(t)
				}
				for _, m := range s.Members {
					if len(m.Body) > 0 {
						walk(m.Body)
					}
				}
			case *IfStmt:
				walk(s.Then)
				walk(s.Else)
			case *WhileStmt:
				walk(s.Body)
			case *DoWhileStmt:
				walk(s.Body)
			case *ForStmt:
				walk(s.Body)
			case *ForeachStmt:
				walk(s.Body)
			case *SwitchStmt:
				for _, c := range s.Cases {
					walk(c.Body)
				}
			case *TryStmt:
				walk(s.Body)
				walk(s.Finally)
				for _, c := range s.Catches {
					walk(c.Body)
				}
			case *BlockStmt:
				walk(s.Body)
			case *FuncDecl:
				walk(s.Body)
			}
		}
	}
	walk(stmts)
}

// evalDeclaredClasses собирает имена классов, объявленных в потоке операторов
// (рекурсивно по блокам).
func evalDeclaredClasses(stmts []Stmt, out []string) []string {
	for _, st := range stmts {
		switch s := st.(type) {
		case *ClassDecl:
			if s.Name != "" {
				out = append(out, s.Name)
			}
		case *IfStmt:
			out = evalDeclaredClasses(s.Then, out)
			out = evalDeclaredClasses(s.Else, out)
		case *WhileStmt:
			out = evalDeclaredClasses(s.Body, out)
		case *DoWhileStmt:
			out = evalDeclaredClasses(s.Body, out)
		case *ForStmt:
			out = evalDeclaredClasses(s.Body, out)
		case *ForeachStmt:
			out = evalDeclaredClasses(s.Body, out)
		case *SwitchStmt:
			for _, c := range s.Cases {
				out = evalDeclaredClasses(c.Body, out)
			}
		case *TryStmt:
			out = evalDeclaredClasses(s.Body, out)
			out = evalDeclaredClasses(s.Finally, out)
			for _, c := range s.Catches {
				out = evalDeclaredClasses(c.Body, out)
			}
		case *BlockStmt:
			out = evalDeclaredClasses(s.Body, out)
		}
	}
	return out
}

// stmtsHaveClassDecl сообщает, содержит ли поток операторов объявление класса
// (в том числе внутри условных/циклических блоков). Такие объявления из
// eval()/include не должны инлайниться в охватывающий юнит.
func stmtsHaveClassDecl(stmts []Stmt) bool {
	for _, st := range stmts {
		switch s := st.(type) {
		case *ClassDecl:
			return true
		case *IfStmt:
			if stmtsHaveClassDecl(s.Then) || stmtsHaveClassDecl(s.Else) {
				return true
			}
		case *WhileStmt:
			if stmtsHaveClassDecl(s.Body) {
				return true
			}
		case *DoWhileStmt:
			if stmtsHaveClassDecl(s.Body) {
				return true
			}
		case *ForStmt:
			if stmtsHaveClassDecl(s.Body) {
				return true
			}
		case *ForeachStmt:
			if stmtsHaveClassDecl(s.Body) {
				return true
			}
		case *SwitchStmt:
			for _, c := range s.Cases {
				if stmtsHaveClassDecl(c.Body) {
					return true
				}
			}
		case *TryStmt:
			if stmtsHaveClassDecl(s.Body) || stmtsHaveClassDecl(s.Finally) {
				return true
			}
			for _, c := range s.Catches {
				if stmtsHaveClassDecl(c.Body) {
					return true
				}
			}
		case *BlockStmt:
			if stmtsHaveClassDecl(s.Body) {
				return true
			}
		}
	}
	return false
}

// inlineEval раскрывает eval("...") с литеральной строкой. Если строка не
// парсится (в PHP это ParseError), конструкция остаётся рантайму, где ошибка
// возникает как исключение ParseError.
func inlineEval(x *CallExpr, file string, inc map[string]bool) (Expr, []Stmt, error) {
	if len(x.Args) != 1 {
		return x, nil, nil
	}
	src, ok := constString(x.Args[0], file)
	if !ok {
		return x, nil, nil
	}
	sub, err := Parse(src)
	if err != nil {
		return x, nil, nil
	}
	// Диагностики компиляции eval-кода печатаются в момент выполнения
	// (с локацией "file(line) : eval()'d code"): инлайн потерял бы и текст,
	// и время вывода (bug69388 — use-warning через error handler).
	if len(sub.TypeWarnings) > 0 {
		return x, nil, nil
	}
	// Классы, объявленные в eval(), PHP регистрирует в рантайме (в момент
	// выполнения eval), а не hoist'ит в охватывающий юнит. Инлайн сделал бы их
	// видимыми до вызова eval и перенёс бы стартовые deprecation-сообщения
	// объявления класса в начало скрипта. Оставляем такой eval рантайму, кроме
	// случая, когда на объявленный класс ссылается extends/implements/use
	// другого класса (тогда инлайн нужен для раннего связывания).
	if stmtsHaveClassDecl(sub.Stmts) {
		needInline := false
		for _, name := range evalDeclaredClasses(sub.Stmts, nil) {
			if inlineEvalParents[strings.ToLower(strings.TrimLeft(name, "\\"))] {
				needInline = true
				break
			}
		}
		if !needInline {
			return x, nil, nil
		}
	}
	stmts, err := inlineStmts(sub.Stmts, file, inc)
	if err != nil {
		return nil, nil, err
	}
	val, pre := splitTopReturn(stmts, &NullLit{})
	// Предупреждения компиляции eval-фрагмента (напр. use некомпаундного
	// имени) печатаются в момент выполнения eval, а не как startup-диагностика
	// охватывающего файла (Zend компилирует eval в рантайме).
	for _, tw := range sub.TypeWarnings {
		pre = append(pre, &ExprStmt{X: &CallExpr{Name: "__php_parse_warn",
			Args: []Expr{&StrLit{V: tw.Msg}}}})
	}
	return val, pre, nil
}

// splitTopReturn отделяет верхнеуровневый return подключаемого кода: он
// задаёт возвращаемое значение include/eval, а не выход из внешней функции.
func splitTopReturn(stmts []Stmt, def Expr) (Expr, []Stmt) {
	if n := len(stmts); n > 0 {
		if r, ok := stmts[n-1].(*ReturnStmt); ok {
			v := r.X
			if v == nil {
				v = &NullLit{}
			}
			return v, stmts[:n-1]
		}
	}
	return def, stmts
}

// resolveIncludePath переводит путь из include в путь для чтения. Путь,
// начинающийся с "./" или "../", трактуется относительно текущего рабочего
// каталога (как в PHP); остальные относительные пути — от каталога файла.
func resolveIncludePath(p, file string) string {
	// URL-обёртки (в т.ч. пользовательские) не подлежат filepath.Clean:
	// "test://hello" иначе превратилось бы в "test:/hello".
	if strings.Contains(p, "://") {
		return p
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	if strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../") || p == "." || p == ".." {
		return filepath.Clean(p)
	}
	if file != "" {
		return filepath.Clean(filepath.Join(filepath.Dir(file), p))
	}
	return filepath.Clean(p)
}

// constString пытается вычислить выражение в строку на этапе компиляции.
// Поддерживает литералы, __FILE__/__DIR__, конкатенацию и dirname(__FILE__).
func constString(e Expr, file string) (string, bool) {
	switch x := e.(type) {
	case *StrLit:
		return x.V, true
	case *IntLit:
		return strconv.FormatInt(x.V, 10), true
	case *BoolLit:
		if x.V {
			return "1", true
		}
		return "", true
	case *MagicExpr:
		switch x.Name {
		case "__FILE__":
			return file, true
		case "__DIR__":
			if file != "" {
				return filepath.Dir(file), true
			}
			return "", true
		}
		return "", false
	case *BinExpr:
		if x.Op == "." {
			l, ok1 := constString(x.L, file)
			r, ok2 := constString(x.R, file)
			if ok1 && ok2 {
				return l + r, true
			}
		}
		return "", false
	case *CallExpr:
		if x.Name == "dirname" && len(x.Args) >= 1 {
			s, ok := constString(x.Args[0], file)
			if !ok {
				return "", false
			}
			n := int64(1)
			if len(x.Args) >= 2 {
				if nv, ok := x.Args[1].(*IntLit); ok {
					n = nv.V
				}
			}
			for ; n > 0; n-- {
				s = filepath.Dir(s)
			}
			return s, true
		}
		return "", false
	}
	return "", false
}

// internalFuncs — служебные функции рантайма для eval/include. Имена с
// префиксом __php_ не могут быть введены пользователем.
func internalFuncs() []*vm.Function {
	funcs := []*vm.Function{
		vm.Register("__php_eval", evalBuiltin),
		vm.Register("__php_ticks", ticksBuiltin),
		vm.Register("__php_include", includeBuiltin),
		vm.Register("__php_record_included", func(ctx *vm.Context) vm.Value {
			ctx.VM().RecordIncluded(arg(ctx, 0).ToString())
			return vm.MakeNull()
		}),
		vm.Register("__php_count", vm.CountBuiltin),
		vm.Register("__php_sizeof", vm.SizeofBuiltin),
		vm.Register("__php_is_countable", vm.IsCountableBuiltin),
		vm.Register("__php_error_reporting", vm.ErrorReportingBuiltin),
		vm.Register("iterator_to_array", vm.IteratorToArrayBuiltin),
		vm.Register("__php_yield_from_ret", vm.YieldFromRetBuiltin),
		vm.Register("__php_yield_from_begin", vm.YieldFromBeginBuiltin),
		vm.Register("__php_unhandled_match", unhandledMatchBuiltin),
		vm.Register("__php_spread", spreadBuiltin),
		vm.Register("__php_const_spread_check", constSpreadCheckBuiltin),
		vm.Register("__php_instanceof", instanceOfBuiltin),
		vm.Register("__php_spread_args", spreadArgsBuiltin),
		vm.Register("__php_const_prop_check", constPropCheckBuiltin),
		vm.Register("__php_const_prop_name", constPropNameBuiltin),
		vm.Register("__php_new_spread", newSpreadBuiltin),
		vm.Register("__php_require_class", requireClassFn),
		vm.Register("__php_variance_check", varianceCheckFn),
		vm.Register("__php_call_named", callNamedBuiltin),
		vm.Register("__php_call_cb_named", callCbNamedBuiltin),
		vm.Register("__php_forward_cb_named", forwardCbNamedBuiltin),
		vm.Register("__php_incdec_prop", incDecPropBuiltin),
		vm.Register("__php_incdec_ref", incDecRefBuiltin),
		vm.Register("__php_incdec_val", incDecValBuiltin),
		vm.Register("__php_incdec_global", incDecGlobalBuiltin),
		vm.Register("__php_ref_notice", refNoticeBuiltin),
		vm.Register("__php_global_bind", globalBindBuiltin),
		vm.Register("__php_ref_ret_notice", refRetNoticeBuiltin),
		vm.Register("__php_ref_assign_check", refAssignCheckBuiltin),
		vm.Register("__php_catch_bind", catchBindBuiltin),
		vm.Register("__php_globals_ref", globalsRefBuiltin),
		vm.Register("__php_globals_set_ref", globalsSetRefBuiltin),
		vm.Register("__php_foreach_check", foreachCheckBuiltin),
		vm.Register("__php_iter_byref_check", iterByRefCheckBuiltin),
		vm.Register("__php_byref_error", byRefErrorBuiltin),
		vm.Register("__php_named_arg_check", namedArgCheckFn),
		vm.Register("__php_default_check", defaultArgCheckBuiltin),
		vm.Register("__php_index_ref_check", indexRefCheckBuiltin),
		vm.Register("__php_append_ref_check", appendRefCheckBuiltin),
		vm.Register("__php_dyn_static_get", dynStaticGetBuiltin),
		vm.Register("__php_dyn_static_set", dynStaticSetBuiltin),
		vm.Register("__php_dyn_static_isset", dynStaticIssetBuiltin),
		vm.Register("__php_dyn_static_ref", dynStaticRefBuiltin),
		vm.Register("__php_dyn_class_const", dynClassConstBuiltin),
		vm.Register("__php_dyn_static_call", dynStaticCallBuiltin),
		vm.Register("__php_check_dyn_class", checkDynClassBuiltin),
		vm.Register("__php_method_call_named", methodCallNamedBuiltin),
		vm.Register("__php_static_call_named", staticCallNamedBuiltin),
		vm.Register("__php_new_named", newNamedBuiltin),
		vm.Register("__php_class_of", classOfBuiltin),
		vm.Register("__php_close_gen", closeGenBuiltin),
		vm.Register("__php_parse_warn", func(ctx *vm.Context) vm.Value {
			// Предупреждение компиляции eval-фрагмента (выводится в точке
			// выполнения eval, в т.ч. через set_error_handler).
			ctx.VM().Warn(arg(ctx, 0).ToString())
			return vm.MakeNull()
		}),
		vm.Register("__php_unset_dyn", func(ctx *vm.Context) vm.Value {
			// unset($$x): динамическое имя удаляет саму переменную (а не
			// записывает null) — последующее чтение даёт «Undefined variable».
			ctx.VM().GlobalsUnsetDyn(arg(ctx, 0).ToString())
			return vm.MakeNull()
		}),
		vm.Register("__php_warn_undef", func(ctx *vm.Context) vm.Value {
			// Чтение локальной переменной после unset(): Zend печатает
			// «Undefined variable $x».
			name := arg(ctx, 0).ToString()
			n := strings.TrimPrefix(name, "$")
			if n == "" {
				return vm.MakeNull()
			}
			ctx.VM().Warn("Undefined variable $" + n)
			return vm.MakeNull()
		}),
		vm.Register("__php_yield_from_set", func(ctx *vm.Context) vm.Value {
			ctx.VM().SetCurrentYieldFromInner(arg(ctx, 0))
			return vm.MakeNull()
		}),
		vm.Register("__php_list_index", listIndexBuiltin),
	}
	return funcs
}

// appendRefCheckBuiltin — предварительная проверка LHS `$arr[] =& expr` ДО
// вычисления RHS: Zend выполняет FETCH_DIM_W первым, поэтому ошибка занятого
// авто-ключа имеет приоритет над Notice о передаче не-переменной
// (assign_ref_error_var_handling).
func appendRefCheckBuiltin(ctx *vm.Context) vm.Slot {
	a := vm.Deref(arg(ctx, 0))
	if a.Type() == vm.ArrayType && a.AsArray().NextFree() == math.MaxInt64 {
		ctx.Throw(vm.NewThrowable("Error", "Cannot add element to the array as the next element is already occupied"))
		return vm.MakeNull()
	}
	return vm.MakeNull()
}

// listIndexBuiltin — чтение элемента для деструктуризации list()/[$a,$b]:
// Zend FETCH_LIST. Массив с отсутствующим ключом даёт Warning "Undefined
// array key" и null; объект с ArrayAccess — offsetGet (как FETCH_LIST_R:
// read_dimension BP_VAR_R); строка/скаляр — null БЕЗ диагностик (в отличие от
// обычного чтения индекса). Ссылочные элементы ($a = &$arr[0]) идут другим
// путём (OpListFetchRef) и предупреждений не печатают.
func listIndexBuiltin(ctx *vm.Context) vm.Slot {
	src := vm.Deref(arg(ctx, 0))
	if src.Type() == vm.ObjectType {
		o := ctx.VM().SlotObject(src)
		if o == nil {
			return vm.MakeNull()
		}
		ci := ctx.VM().FindClass(o.ClassName())
		if ci == nil {
			return vm.MakeNull()
		}
		if !ctx.VM().ClassHasMethod(ci, "offsetGet") {
			ctx.Throw(vm.NewThrowable("Error", "Cannot use object of type "+o.ClassName()+" as array"))
			return vm.MakeNull()
		}
		res, err := ci.Call(o, ctx, "offsetGet", []vm.Slot{arg(ctx, 1)})
		if err != nil {
			if th, ok := err.(*vm.Throwable); ok {
				ctx.Throw(th)
				return *new(vm.Slot)
			}
			return vm.MakeNull()
		}
		return vm.Deref(res)
	}
	if src.Type() == vm.ClosureType {
		ctx.Throw(vm.NewThrowable("Error", "Cannot use object of type Closure as array"))
		return *new(vm.Slot)
	}
	if src.Type() != vm.ArrayType {
		return vm.MakeNull()
	}
	k, ok := ctx.VM().KeyOfWarn(arg(ctx, 1))
	if !ok {
		return vm.MakeNull()
	}
	v, found := src.AsArray().Get(k)
	if !found {
		if k.IsStr() {
			ctx.VM().Warn("Undefined array key \"" + k.StrVal() + "\"")
		} else {
			ctx.VM().Warn("Undefined array key " + k.PHPString())
		}
		return vm.MakeNull()
	}
	return v
}

// closeGenBuiltin закрывает генератор в слоте (при unset), прогоняя finally.
func closeGenBuiltin(ctx *vm.Context) vm.Value {
	if args := ctx.Args(); len(args) > 0 {
		if err := ctx.VM().CloseIfGenerator(args[0]); err != nil {
			if th, ok := err.(*vm.Throwable); ok {
				ctx.Throw(th)
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
		}
		if err := ctx.VM().CloseIfFiber(args[0]); err != nil {
			if th, ok := err.(*vm.Throwable); ok {
				ctx.Throw(th)
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
		}
	}
	return vm.MakeNull()
}

// iterByRefCheckBuiltin запрещает foreach по ссылке по Iterator/IteratorAggregate.
func iterByRefCheckBuiltin(ctx *vm.Context) vm.Slot {
	src := arg(ctx, 0)
	if err := ctx.VM().CheckForeachByRef(src); err != nil {
		if th, ok := err.(*vm.Throwable); ok {
			ctx.Throw(th)
			if ctx.HasThrown() {
				return *new(vm.Slot)
			}
		} else {
			ctx.Throw(vm.NewThrowable("Error", err.Error()))
			if ctx.HasThrown() {
				return *new(vm.Slot)
			}
		}
	}
	return vm.MakeNull()
}

// foreachCheckBuiltin проверяет, что источник foreach — array|object; иначе
// печатает Warning и возвращает false.
func foreachCheckBuiltin(ctx *vm.Context) vm.Slot {
	v := arg(ctx, 0)
	switch v.Type() {
	case vm.ArrayType, vm.ObjectType:
		return vm.MakeBool(true)
	}
	ctx.VM().Warning("foreach() argument must be of type array|object, " + foreachTypeName(v) + " given")
	return vm.MakeBool(false)
}

func foreachTypeName(v vm.Slot) string {
	switch v.Type() {
	case vm.NullType:
		return "null"
	case vm.BoolType:
		if v.ToBool() {
			return "true"
		}
		return "false"
	case vm.IntType:
		return "int"
	case vm.FloatType:
		return "float"
	case vm.StringType:
		return "string"
	case vm.ArrayType:
		return "array"
	case vm.ObjectType:
		return "object"
	case vm.ResourceType:
		return "resource"
	}
	return "mixed"
}

// refNoticeBuiltin поднимает Notice "Only variable references should be
// yielded/returned by reference" (by-ref генераторы/functions).
func refNoticeBuiltin(ctx *vm.Context) vm.Slot {
	if args := ctx.Args(); len(args) > 0 {
		ctx.VM().Notice(args[0].ToString())
	}
	return vm.MakeNull()
}

// globalBindBuiltin реализует динамический `global $$x` / `global ${expr}`:
// создаёт глобальную переменную с вычисленным именем (Zend создаёт запись,
// даже если её не было). Доступ к локальному имени в той же области видимости
// не перенаправляется (компилятор не знает имени заранее).
func globalBindBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) == 0 {
		return vm.MakeNull()
	}
	name, err := ctx.VMInstance().ToStringValue(args[0])
	if err != nil {
		if th, ok := err.(*vm.Throwable); ok {
			ctx.Throw(th)
		} else {
			ctx.Throw(vm.NewThrowable("Error", err.Error()))
		}
		if ctx.HasThrown() {
			return *new(vm.Slot)
		}
	}
	ctx.VMInstance().GlobalsTouch(name)
	return vm.MakeNull()
}

// refRetNoticeBuiltin — notice «Only variable references should be returned by
// reference» для by-ref return: Zend проверяет объявленный тип возврата раньше
// notice, поэтому при несовпадении типов (TypeError у вызывающего) notice не
// печатается. args: значение, текст, тип возврата, strict.
func refRetNoticeBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) < 4 {
		return vm.MakeNull()
	}
	if ctx.VMInstance().RefReturnTypeOk(args[2].ToString(), args[3].BoolVal(), args[0]) {
		ctx.VM().Notice(args[1].ToString())
	}
	return args[0]
}

// globalsRefBuiltin возвращает ссылку на глобальную переменную по имени
// (для `$GLOBALS[name]`).
func globalsRefBuiltin(ctx *vm.Context) vm.Slot {
	return ctx.VM().GlobalsRefAddr(arg(ctx, 0).ToString())
}

// globalsSetRefBuiltin — `$GLOBALS[name] = &value`: делаем глобальную
// переменную name ссылкой на value.
func globalsSetRefBuiltin(ctx *vm.Context) vm.Slot {
	key := arg(ctx, 0).ToString()
	cell := arg(ctx, 1)
	ctx.VM().GlobalsSetRef(key, cell)
	return cell
}

// refAssignCheckBuiltin поднимает Notice с заданным текстом, если значение не
// является ссылкой (для `$x =& expr` и возврата вызова из by-ref функции).
// Аргументы: значение, текст Notice, [wrap]. Возвращает значение; при wrap и
// отсутствии ссылки оборачивает его в новую Ref-ячейку (чтобы «вернуть по
// ссылке» можно было без caller-Notice).
func refAssignCheckBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) >= 2 {
		if !args[0].IsRef() {
			ctx.VM().Notice(args[1].ToString())
			if len(args) >= 3 && args[2].ToBool() {
				return vm.NewRef(args[0])
			}
		}
		return args[0]
	}
	if len(args) == 1 {
		return args[0]
	}
	return vm.MakeNull()
}

// catchBindBuiltin — привязка переменной catch: строгая запись в ячейку-ссылку
// (ZEND_CATCH выполняет строгое присваивание, без приведения Stringable).
func catchBindBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) < 2 {
		return vm.MakeNull()
	}
	cell := args[0]
	val := args[1]
	if r := cell.Ref(); r != nil {
		if err := ctx.VM().SetRefCheckedStrict(r, val); err != nil {
			throwCallable(ctx, err)
			return vm.MakeNull()
		}
	}
	return val
}

// indexRefCheckBuiltin проверяет `$obj[$k] = &$x` для ArrayAccess-объекта.
func indexRefCheckBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) > 0 {
		if err := ctx.VMInstance().IndexRefCheck(args[0]); err != nil {
			return throwErr(ctx, err)
		}
	}
	return vm.MakeNull()
}

// byRefErrorBuiltin бросает фатальную Error "could not be passed by reference"
// при передаче не-переменного выражения в by-ref параметр.
func byRefErrorBuiltin(ctx *vm.Context) vm.Slot {
	msg := "Argument could not be passed by reference"
	if args := ctx.Args(); len(args) > 0 {
		msg = args[0].ToString()
	}
	ctx.Throw(vm.NewThrowable("Error", msg))
	if ctx.HasThrown() {
		return *new(vm.Slot)
	}
	return vm.MakeNull()
}

// dynStaticGetBuiltin — чтение статического свойства класса, заданного
// динамически: `$cls::$prop`.
func dynStaticGetBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) < 2 {
		return vm.MakeNull()
	}
	v, err := ctx.VMInstance().DynStaticGet(args[0], args[1].ToString())
	if err != nil {
		return throwErr(ctx, err)
	}
	return v
}

// classOfBuiltin — `EXPR::class` (PHP 8): имя класса значения выражения.
// Для объекта возвращается имя класса; строковое значение недопустимо
// (TypeError: Cannot use "::class" on string) — строковые литералы
// резолвятся на компиляции, сюда не попадают.
func classOfBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) == 0 {
		return vm.MakeString("")
	}
	v := derefSlot(args[0])
	switch v.Type() {
	case vm.ObjectType:
		if ci := ctx.VMInstance().SlotClassInfo(v); ci != nil {
			return vm.MakeString(ci.Name())
		}
		return vm.MakeString("")
	}
	ctx.Throw(vm.NewThrowable("TypeError", `Cannot use "::class" on `+phpTypeName(ctx, 0)))
	if ctx.HasThrown() {
		return *new(vm.Slot)
	}
	return vm.MakeString("")
}

// dynStaticSetBuiltin — запись в статическое свойство класса, заданного
// значением: `EXPR::$p = v` / `unset(EXPR::$p)`.
func dynStaticSetBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) < 3 {
		return vm.MakeNull()
	}
	v, err := ctx.VMInstance().DynStaticSetByName(args[0], args[1].ToString(), args[2])
	if err != nil {
		return throwErr(ctx, err)
	}
	return v
}

// dynStaticIssetBuiltin — isset(EXPR::$p).
func dynStaticIssetBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) < 2 {
		return vm.MakeBool(false)
	}
	ok, err := ctx.VMInstance().DynStaticIsset(args[0], args[1].ToString())
	if err != nil {
		return throwErr(ctx, err)
	}
	return vm.MakeBool(ok)
}

// dynStaticRefBuiltin — ссылка на статическое свойство класса, заданного
// значением.
func dynStaticRefBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) < 2 {
		return vm.MakeNull()
	}
	cell, err := ctx.VMInstance().DynStaticRef(args[0], args[1].ToString())
	if err != nil {
		return throwErr(ctx, err)
	}
	return cell
}

// dynClassConstBuiltin — чтение константы класса, заданного динамически:
// `$cls::CONST`.
func dynClassConstBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) < 2 {
		return vm.MakeNull()
	}
	v, err := ctx.VMInstance().DynClassConstValue(args[0], args[1])
	if err != nil {
		return throwErr(ctx, err)
	}
	return v
}

// dynStaticCallBuiltin — вызов статического метода класса, заданного
// динамически: `$cls::m(args)`.
// checkDynClassBuiltin — проверка типа динамического класса для `$a::$b(...)`:
// Zend резолвит класс до вычисления имени метода.
func checkDynClassBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) < 1 {
		return vm.MakeNull()
	}
	if s := derefSlot(args[0]); s.Type() != vm.StringType && s.Type() != vm.ObjectType {
		return throwErr(ctx, vm.NewThrowable("Error", "Class name must be a valid object or a string"))
	}
	return vm.MakeNull()
}

func dynStaticCallBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) < 3 {
		return vm.MakeNull()
	}
	// Zend сначала резолвит класс и лишь затем проверяет тип имени метода.
	if m := derefSlot(args[1]); m.Type() != vm.StringType {
		if cs := derefSlot(args[0]); cs.Type() != vm.StringType && cs.Type() != vm.ObjectType {
			return throwErr(ctx, vm.NewThrowable("Error", "Class name must be a valid object or a string"))
		}
		return throwErr(ctx, vm.NewThrowable("Error", "Method name must be a string"))
	}
	var callArgs []vm.Slot
	if a := derefSlot(args[2]); a.Type() == vm.ArrayType {
		callArgs = a.AsArray().Values()
	}
	res, err := ctx.VMInstance().DynStaticCall(args[0], args[1].ToString(), callArgs)
	if err != nil {
		return throwErr(ctx, err)
	}
	return res
}

// throwErr поднимает ошибку из нативного хелпера как исключение.
func throwErr(ctx *vm.Context, err error) vm.Slot {
	if th, ok := err.(*vm.Throwable); ok {
		ctx.Throw(th)
		if ctx.HasThrown() {
			return *new(vm.Slot)
		}
	}
	ctx.Throw(vm.NewThrowable("Error", err.Error()))
	if ctx.HasThrown() {
		return *new(vm.Slot)
	}
	return vm.MakeNull()
}

// methodCallNamedBuiltin — $obj->m(именованные аргументы): recv, имя, массив.
func methodCallNamedBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) < 3 {
		return vm.MakeNull()
	}
	var arr *vm.Array
	if a := derefSlot(args[2]); a.Type() == vm.ArrayType {
		arr = a.AsArray()
	}
	res, err := ctx.VMInstance().MethodCallNamed(args[0], args[1].ToString(), arr)
	if err != nil {
		return throwErr(ctx, err)
	}
	return res
}

// staticCallNamedBuiltin — Class::m(именованные аргументы): класс, имя, массив.
func staticCallNamedBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) < 3 {
		return vm.MakeNull()
	}
	var arr *vm.Array
	if a := derefSlot(args[2]); a.Type() == vm.ArrayType {
		arr = a.AsArray()
	}
	res, err := ctx.VMInstance().StaticCallNamed(args[0], args[1].ToString(), arr)
	if err != nil {
		return throwErr(ctx, err)
	}
	return res
}

// newNamedBuiltin — new Class(именованные аргументы): класс (индекс или имя),
// массив аргументов.
func newNamedBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) < 2 {
		return vm.MakeNull()
	}
	var arr *vm.Array
	if a := derefSlot(args[1]); a.Type() == vm.ArrayType {
		arr = a.AsArray()
	}
	res, err := ctx.VMInstance().NewInstanceNamed(args[0], arr)
	if err != nil {
		return throwErr(ctx, err)
	}
	return res
}

// newSpreadBuiltin создаёт объект класса с аргументами из массива
// (для `new Foo(...$args)`).
func newSpreadBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) < 2 {
		return vm.MakeNull()
	}
	idx := int(args[0].ToInt())
	values := args[1].AsArray().Values()
	res, err := ctx.VMInstance().NewInstanceArgs(idx, values)
	if err != nil {
		ctx.Throw(vm.NewThrowable("Error", err.Error()))
		if ctx.HasThrown() {
			return *new(vm.Slot)
		}
	}
	return res
}

// spreadBuiltin распаковывает массив/Traversable в массив-литерал.
func spreadBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) < 2 {
		return args[0]
	}
	res, err := ctx.VMInstance().SpreadValues(args[0], args[1], false)
	if err != nil {
		ctx.Throw(vm.NewThrowable("Error", err.Error()))
		if ctx.HasThrown() {
			return *new(vm.Slot)
		}
	}
	return res
}

// constSpreadCheckBuiltin — проверка распаковки в константном выражении:
// допустимы только массивы (zend_ast_evaluate).
func constSpreadCheckBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) == 0 {
		return vm.MakeNull()
	}
	if args[0].Type() != vm.ArrayType {
		ctx.Throw(vm.NewThrowable("Error", "Only arrays can be unpacked in constant expression"))
		if ctx.HasThrown() {
			return *new(vm.Slot)
		}
	}
	return args[0]
}

// constPropCheckBuiltin запрещает чтение свойства не-enum объекта в
// константном выражении (zend_ast_evaluate).
func constPropCheckBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) == 0 {
		return vm.MakeNull()
	}
	if err := ctx.VMInstance().ConstPropCheck(args[0]); err != nil {
		if th, ok := err.(*vm.Throwable); ok {
			ctx.Throw(th)
		} else {
			ctx.Throw(vm.NewThrowable("Error", err.Error()))
		}
		if ctx.HasThrown() {
			return *new(vm.Slot)
		}
	}
	return vm.MakeNull()
}

// constPropNameBuiltin приводит динамическое имя свойства в константном
// выражении к строке (zend_ast_evaluate).
func constPropNameBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) == 0 {
		return vm.MakeNull()
	}
	res, err := ctx.VMInstance().ConstPropName(args[0])
	if err != nil {
		if th, ok := err.(*vm.Throwable); ok {
			ctx.Throw(th)
		} else {
			ctx.Throw(vm.NewThrowable("Error", err.Error()))
		}
		if ctx.HasThrown() {
			return *new(vm.Slot)
		}
	}
	return res
}

// spreadArgsBuiltin распаковывает массив/Traversable в список аргументов.
// Дополнительные аргументы (start, mask, varByRef, name) включают ссылочную
// распаковку: элементы by-ref позиций становятся ссылками в источнике.
func spreadArgsBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	if len(args) < 2 {
		return args[0]
	}
	var byref *vm.SpreadValueByRef
	if len(args) >= 6 {
		byref = &vm.SpreadValueByRef{
			Start:    int(args[2].ToInt()),
			Mask:     uint64(args[3].ToInt()),
			VarByRef: args[4].ToBool(),
			Name:     args[5].ToString(),
		}
		if len(args) >= 8 {
			if a := vm.Deref(args[6]).AsArray(); a != nil {
				byref.ParamNames = make([]string, 0, a.Len())
				for _, v := range a.All() {
					byref.ParamNames = append(byref.ParamNames, vm.Deref(v).ToString())
				}
			}
			if a := vm.Deref(args[7]).AsArray(); a != nil {
				byref.ParamByRef = make([]bool, 0, a.Len())
				for _, v := range a.All() {
					byref.ParamByRef = append(byref.ParamByRef, vm.Deref(v).ToBool())
				}
			}
		}
	}
	res, err := ctx.VMInstance().SpreadValuesRef(args[0], args[1], true, byref)
	if err != nil {
		if th, ok := err.(*vm.Throwable); ok {
			ctx.Throw(th)
			return *new(vm.Slot)
		}
		ctx.Throw(vm.NewThrowable("Error", err.Error()))
		if ctx.HasThrown() {
			return *new(vm.Slot)
		}
	}
	return res
}

// instanceOfBuiltin реализует `$obj instanceof $cls` с динамическим именем.
func instanceOfBuiltin(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	if len(args) < 2 {
		return vm.MakeBool(false)
	}
	if err := ctx.VM().ScopeKeywordErr(args[1].ToString()); err != nil {
		ctx.Throw(err.(*vm.Throwable))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeBool(false)
	}
	return vm.MakeBool(ctx.VM().InstanceOfName(args[0], args[1].ToString()))
}

// unhandledMatchBuiltin бросает UnhandledMatchError для выражения match без
// подходящей ветви. Сообщение формируется как в PHP.
func unhandledMatchBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	var s vm.Slot
	if len(args) > 0 {
		s = args[0]
	}
	ctx.Throw(vm.NewThrowable("UnhandledMatchError", vm.MatchErrorMessage(ctx.VMInstance(), s)))
	if ctx.HasThrown() {
		return *new(vm.Slot)
	}
	return vm.MakeNull()
}

// runSubCode компилирует и исполняет фрагмент PHP-кода в ТОЙ ЖЕ VM, что и
// вызывающий код: функции/классы подключаемого файла линкуются в общие
// таблицы и становятся видимыми, глобальные переменные разделяются.
// Возвращает значение верхнеуровневого return.
// phpMode выбирает режим разбора: true — чистый PHP (eval), false — файл
// со смешанным PHP/HTML (include/require).
// subCodeDepth ограничивает вложенность рантайм-include/eval, чтобы
// рекурсивный require(__FILE__) давал ошибку, а не бесконечную рекурсию/OOM.
var subCodeDepth int

func runSubCode(ctx *vm.Context, src, file string, phpMode bool) (vm.Slot, bool) {
	if subCodeDepth > 25 {
		ctx.Throw(vm.NewThrowable("Error", "Maximum function nesting level reached"))
		if ctx.HasThrown() {
			return *new(vm.Slot), *new(bool)
		}
		return vm.MakeNull(), false
	}
	subCodeDepth++
	defer func() { subCodeDepth-- }()

	vmc := ctx.VM()
	funcs := vmc.FunctionsTable()
	classes := vmc.Classes()
	incompleteIdxV := -1
	for i, c := range classes {
		if c.Name() == incompleteClassName {
			incompleteIdxV = i
			break
		}
	}
	base := &Compiled{Funcs: funcs, Classes: classes, IncompleteIdx: incompleteIdxV}
	warnMark := len(compilerWarnings)
	// Псевдонимы class_alias() к моменту компиляции: нужны проверкам
	// совместимости сигнатур в рантайм-подключённом коде (bug54624).
	runtimeClassAliases = vmc.ClassAliasTargets()
	c, err := compileFileModeLink(src, file, phpMode, base)
	runtimeClassAliases = nil
	// Диагностики компиляции eval/include печатаются в момент выполнения
	// (в основном скрипте они выводятся как startup-предупреждения чанка).
	// compileFileModeLink сбрасывает список деприкаций, так что здесь он
	// содержит ровно диагностики этого фрагмента.
	for _, d := range compilerDeprecations {
		vmc.PrintStartupDeprecated(d)
	}
	compilerDeprecations = nil
	for _, w := range compilerWarnings[warnMark:] {
		msg, dfile, dline := splitCompilerWarning(w)
		vmc.WarnAt(msg, dfile, dline)
	}
	compilerWarnings = compilerWarnings[:warnMark]
	if err != nil {
		ctx.Throw(vm.NewThrowable("ParseError", err.Error()))
		if ctx.HasThrown() {
			return *new(vm.Slot), *new(bool)
		}
		return vm.MakeNull(), false
	}
	// eval(): синтаксическая ошибка — перехватываемое исключение ParseError,
	// а не фатальный вывод (в отличие от include/require). Локацию берём из
	// eval-чанка, чтобы uncaught-текст указывал на "<file>(N) : eval()'d code".
	if phpMode && c.ParseFatal != nil {
		th := vm.NewThrowable("ParseError", c.ParseFatal.Error())
		th.LocFile = c.Main.File
		th.LocLine = c.ParseFatal.line
		ctx.Throw(th)
		if ctx.HasThrown() {
			return *new(vm.Slot), *new(bool)
		}
		return vm.MakeNull(), false
	}
	// Компиляционные диагностики фазы парсера в eval()/include — тоже
	// перехватываемый CompileError (bug75218), а не печать Fatal error.
	if c.CatchableFatal != nil {
		th := vm.NewThrowable("ParseError", c.CatchableFatal.msg)
		th.LocFile = c.Main.File
		th.LocLine = c.CatchableFatal.line
		ctx.Throw(th)
		if ctx.HasThrown() {
			return *new(vm.Slot), *new(bool)
		}
		return vm.MakeNull(), false
	}
	// Линкуем новые функции/классы и исполняем подключённый файл в той же VM,
	// что и вызывающий код (общие globals, видимость объявлений).
	res, returned, err := vmc.RunLinked(c.Main, c.Funcs, c.Classes, len(funcs), len(classes))
	if err != nil {
		if vm.IsExitSignal(err) {
			// Фатальная Parse error (или явный exit) в eval/include: сообщение
			// уже напечатано, дальше исполнять нечего — останавливаем скрипт.
			ctx.VMInstance().RequestHalt()
			return vm.MakeNull(), false
		}
		if th, ok := err.(*vm.Throwable); ok {
			ctx.Throw(th)
			if ctx.HasThrown() {
				return *new(vm.Slot), *new(bool)
			}
		} else {
			ctx.Throw(vm.NewThrowable("Error", err.Error()))
			if ctx.HasThrown() {
				return *new(vm.Slot), *new(bool)
			}
		}
		return vm.MakeNull(), false
	}
	return res, returned
}

func evalBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	var src, file string
	if len(args) > 0 {
		src = args[0].ToString()
	}
	if len(args) > 1 {
		file = args[1].ToString()
	}
	line := 0
	if len(args) > 2 {
		line = int(args[2].ToInt())
	}
	// PHP помечает eval-код как "<file>(<line>) : eval()'d code". Без явных
	// file/line (обычный eval('...')) берём файл и строку вызывающего кода.
	if file == "" {
		file = ctx.VMInstance().CurrentFile()
		line = ctx.VMInstance().CurrentLine()
	} else if line == 0 {
		line = ctx.VMInstance().CurrentLine()
	}
	label := file
	if file != "" && line > 0 {
		label = fmt.Sprintf("%s(%d) : eval()'d code", file, line)
	}
	res, _ := runSubCode(ctx, src, label, true)
	if ctx.HasThrown() {
		return *new(vm.Slot)
	}
	return res
}

func includeBuiltin(ctx *vm.Context) vm.Slot {
	args := ctx.Args()
	var p, file string
	var flags int64
	if len(args) > 0 {
		p = args[0].ToString()
	}
	if len(args) > 1 {
		file = args[1].ToString()
	}
	if len(args) > 2 {
		flags = args[2].ToInt()
	}
	require := flags&2 != 0
	once := flags&1 != 0
	path := resolveIncludePath(p, file)
	// Поиск по include_path (в т.ч. записи-пользовательские обёртки): для
	// относительного имени пробуем include_path/скрипт/cwd по «сырому» пути,
	// как php_resolve_path, а не по пути, склеенному с каталогом файла.
	if !strings.Contains(p, "://") && !strings.HasPrefix(p, "/") {
		if cand, found := fsResolveIncludePath(p, ctx); found {
			path = cand
		}
	}
	if once && ctx.VM().MarkIncluded(path) {
		return vm.MakeInt(1)
	}
	// Пользовательские обёртки: URL-обёртки подчиняются allow_url_include,
	// обычные — читаются и исполняются.
	if scheme, ok := userScheme(path); ok {
		if _, reg := streamWrappers[strings.ToLower(scheme)]; reg {
			if streamWrapperURL[strings.ToLower(scheme)] {
				if !iniEnabled("allow_url_include") {
					ctx.VM().Warning("include(): " + scheme + ":// wrapper is disabled in the server configuration by allow_url_include=0")
					ctx.VM().Warning("include(" + p + "): Failed to open stream: no suitable wrapper could be found")
					if require {
						ctx.Throw(vm.NewThrowable("Error", "require(): Failed opening required '"+p+"'"))
						if ctx.HasThrown() {
							return *new(vm.Slot)
						}
						return vm.MakeBool(false)
					}
					ctx.VM().Warning(fmt.Sprintf("include(): Failed opening '%s' for inclusion (include_path='%s')", p, fsIncludePath))
					return vm.MakeBool(false)
				}
				if !iniEnabled("allow_url_fopen") {
					ctx.VM().Warning("include(): " + scheme + ":// wrapper is disabled in the server configuration by allow_url_fopen=0")
					ctx.VM().Warning("include(" + p + "): Failed to open stream: no suitable wrapper could be found")
					if require {
						ctx.Throw(vm.NewThrowable("Error", "require(): Failed opening required '"+p+"'"))
						if ctx.HasThrown() {
							return *new(vm.Slot)
						}
						return vm.MakeBool(false)
					}
					ctx.VM().Warning(fmt.Sprintf("include(): Failed opening '%s' for inclusion (include_path='%s')", p, fsIncludePath))
					return vm.MakeBool(false)
				}
			}
			openForInclude = true
			data, ok2 := readStreamBytes(ctx, path)
			if ctx.HasThrown() {
				return *new(vm.Slot)
			}
			openForInclude = false
			if !ok2 {
				reason := lastUserOpenErr
				if reason == "" {
					reason = "operation failed"
				}
				ctx.VM().Warning(fmt.Sprintf("include(%s): Failed to open stream: %s", path, reason))
				ctx.VM().Warning(fmt.Sprintf("include(): Failed opening '%s' for inclusion (include_path='%s')", p, fsIncludePath))
				return vm.MakeBool(false)
			}
			if !once {
				ctx.VM().RecordIncluded(path)
			}
			res, returned := runSubCode(ctx, string(data), path, false)
			if ctx.HasThrown() {
				return *new(vm.Slot)
			}
			if returned {
				return res
			}
			return vm.MakeInt(1)
		}
	}
	// readStreamBytes покрывает php://filter (в т.ч. пользовательские фильтры,
	// object_init_failure_2), data:// и обычные файлы.
	data, ok := readStreamBytes(ctx, path)
	if ctx.HasThrown() {
		return *new(vm.Slot)
	}
	if !ok {
		if require {
			// Zend: сначала Warning о потоке, затем Error (include_path в
			// тексте исключения).
			ctx.VM().Warning(fmt.Sprintf("require(%s): Failed to open stream: %s", p, fileOpenReason(path, "")))
			ctx.Throw(vm.NewThrowable("Error", fmt.Sprintf("Failed opening required '%s' (include_path='%s')", p, fsIncludePath)))
			if ctx.HasThrown() {
				return *new(vm.Slot)
			}
		} else {
			ctx.VM().Warning(fmt.Sprintf("include(%s): Failed to open stream: %s", p, fileOpenReason(path, "")))
			ctx.VM().Warning(fmt.Sprintf("include(): Failed opening '%s' for inclusion (include_path='%s')", p, fsIncludePath))
		}
		return vm.MakeBool(false)
	}
	if !once {
		ctx.VM().RecordIncluded(path)
	}
	res, returned := runSubCode(ctx, string(data), path, false)
	if ctx.HasThrown() {
		return *new(vm.Slot)
	}
	if returned {
		return res
	}
	return vm.MakeInt(1)
}

// CompileFile — как Compile, но позволяет указать путь к исходному файлу:
// он используется магическими константами __FILE__ и __DIR__.
func CompileFile(src, file string) (*Compiled, error) {
	return compileFileMode(src, file, false)
}

// compileFileMode компилирует исходник. phpMode=true — исходник является
// чистым PHP-кодом (eval); false — смешанный PHP/HTML-файл.
func compileFileMode(src, file string, phpMode bool) (*Compiled, error) {
	return compileFileModeLink(src, file, phpMode, nil)
}

// compileFileModeLink — как compileFileMode, но с линковкой к уже загруженным
// таблицам (linkBase != nil) для рантайм-include/eval.
func compileFileModeLink(src, file string, phpMode bool, linkBase *Compiled) (*Compiled, error) {
	if !phpMode {
		// Файл со скриптом в режиме zend.multibyte + не-UTF-8 internal_encoding:
		// UTF-8 BOM в начале распознаётся как маркер кодировки и съедается
		// (Zend zend_multibyte_script_encoding / BOM detection).
		src = stripScriptBOM(src)
	}
	c, err := compileProgramLink(src, file, phpMode, linkBase)
	if err != nil {
		if fe, ok := err.(*fatalError); ok {
			c := fatalCompiled(fe, file)
			if evalCatchableCompileError(fe.msg) {
				c.CatchableFatal = fe
			}
			return c, nil
		}
		if pe, ok := err.(*parseError); ok {
			return parseFatalCompiled(pe, file), nil
		}
		return nil, err
	}
	return c, nil
}

// evalCatchableCompileError сообщает, что диагностика компиляции в Zend
// относится к фазе парсера и потому в eval()/include превращается в
// перехватываемый CompileError (а в основном скрипте остаётся Fatal error).
func evalCatchableCompileError(msg string) bool {
	switch msg {
	case "Multiple final modifiers are not allowed",
		"Multiple readonly modifiers are not allowed",
		"Multiple access type modifiers are not allowed",
		"Multiple abstract modifiers are not allowed",
		"Multiple static modifiers are not allowed",
		"Cannot use the final modifier on an abstract class",
		"Cannot use the final modifier on an abstract method",
		"Cannot use the final modifier on a property",
		"Encoding must be a literal",
		"__HALT_COMPILER() can only be used from the outermost scope":
		return true
	}
	return false
}

// stripScriptBOM убирает ведущий UTF-8 BOM скрипта при включённом
// zend.multibyte и заданном не-UTF-8 internal_encoding (Zend принимает BOM за
// маркер кодировки файла). Без multibyte BOM остаётся частью вывода, как в PHP.
func stripScriptBOM(src string) string {
	if !strings.HasPrefix(src, "\xEF\xBB\xBF") {
		return src
	}
	if iniBoolInt(iniValues["zend.multibyte"]) == 0 {
		return src
	}
	enc := iniValues["internal_encoding"]
	if enc == "" {
		enc = iniValues["mbstring.internal_encoding"]
	}
	if enc == "" || strings.EqualFold(enc, "UTF-8") {
		return src
	}
	if c, ok := mbCanon(enc); ok && c == "UTF-8" {
		return src
	}
	return src[3:]
}

// compileProgram — как compileProgramLink, но без линковки.
func compileProgram(src, file string, phpMode bool) (*Compiled, error) {
	return compileProgramLink(src, file, phpMode, nil)
}

// compileProgramLink — как compileProgram, но при base != nil линкует
// результат с уже загруженными таблицами функций/классов: новые функции и
// классы дописываются ПОСЛЕ base, а байткод ссылается на индексы объединённой
// таблицы. Это позволяет исполнять подключённый файл в той же VM, что и
// вызывающий код (общие globals и таблицы), не переписывая операнды.
//
// Используется рантайм-include/eval: в этом случае подключаемый файл не
// инлайнится на этапе компиляции (динамический путь), поэтому функции/классы
// из него должны стать видимыми вызывающему коду.
func compileProgramLink(src, file string, phpMode bool, linkBase *Compiled) (*Compiled, error) {
	compilerDeprecations = nil
	compilerWarnings = nil
	var prog *Program
	var err error
	if phpMode {
		prog, err = Parse(src)
	} else {
		prog, err = ParseMixed(src)
	}
	if err != nil {
		return nil, err
	}
	// __COMPILER_HALT_OFFSET__ относится к главному файлу (инлайнинг include
	// может затереть значение при разборе подключаемых файлов).
	mainHaltOffset := prog.HaltOffset
	// Инлайним include/require/eval с литеральным аргументом до построения
	// таблиц функций/классов, чтобы объявления из подключаемых файлов
	// прошли обычный hoisting.
	inlineEvalParents = map[string]bool{}
	collectParentNames(prog.Stmts, inlineEvalParents)
	prog.Stmts, err = inlineStmts(prog.Stmts, file, map[string]bool{})
	inlineEvalParents = nil
	if err != nil {
		return nil, err
	}
	// Строгость типов теперь позиционная: StrictStmt-регионы, расставленные
	// inlineStmts, позволяют каждому файлу (включая вложенные include)
	// сохранять свой режим. Общий режим чанка — режим главного файла.
	markDeclStrict(prog.Stmts, prog.StrictTypes, file)
	for _, tw := range prog.TypeWarnings {
		compilerWarnings = append(compilerWarnings, fmt.Sprintf("%s in %s on line %d", tw.Msg, file, tw.Line))
	}

	// Анонимные классы: присваиваем имена и регистрируем как обычные
	// объявления классов.
	anonInfos, anonMap, nestedClassDecls, compileAliases := collectAnonClasses(prog)
	// Псевдонимы class_alias(), созданные к моменту рантайм-компиляции
	// (eval/include): нужны проверкам совместимости сигнатур, чтобы типы вида
	// `AliasA $x` резолвились (bug54624).
	for k, v := range runtimeClassAliases {
		if _, ok := compileAliases[k]; !ok {
			compileAliases[k] = v
		}
	}

	// 1. Таблица функций: встроенные + hoisting пользовательских.
	// При линковке (linkBase != nil) базой служат уже загруженные функции:
	// новые объявления дописываются после них, а ссылки по индексам попадают
	// в объединённую таблицу.
	var funcs []*vm.Function
	if linkBase != nil {
		funcs = append([]*vm.Function(nil), linkBase.Funcs...)
	} else {
		funcs = builtins()
		funcs = append(funcs, internalFuncs()...)
		// Перехватываем var_dump, чтобы enum-кейсы печатались как `enum(C::X)`
		// (иначе они выглядят как обычные объекты). Остальные значения
		// обрабатываются исходной встроенной функцией.
		for i, f := range funcs {
			if f.Name == "var_dump" {
				funcs[i] = wrapVarDump(f)
				break
			}
		}
	}
	idx := map[string]int{}
	for i, f := range funcs {
		idx[f.Name] = i
	}
	var decls []*FuncDecl
	for _, fd := range collectFuncDecls(prog.Stmts) {
		if _, exists := idx[fd.Name]; !exists {
			idx[fd.Name] = len(funcs)
			sens, sensVar := sensitiveMask(fd.Params)
			funcs = append(funcs, &vm.Function{
				Name: fd.Name, NumArgs: funcNumArgs(fd.Params),
				ByRefArgs: byrefMask(fd.Params), ByRefReturn: fd.ByRefReturn,
				ParamNames: paramNames(fd.Params), ParamTypes: paramTypes(fd.Params),
				RetType:         normalizedType(fd.RetType),
				NumRequired:     numRequired(fd.Params),
				Executable:      &vm.CompiledFunction{},
				Attrs:           makeAttrsInfo(fd.Attrs, attrFileOf(fd.DefFile, file), fd.Strict, nil),
				ParamAttrs:      paramAttrsInfo(fd.Params, attrFileOf(fd.DefFile, file), fd.Strict, nil),
				SensitiveParams: sens, SensitiveVariadic: sensVar,
				VariadicByRef: variadicByRef(fd.Params),
			})
			decls = append(decls, fd)
		} else if linkBase == nil {
			// В одном файле повторное объявление перезаписывает
			// предыдущее (историческое поведение).
			decls = append(decls, fd)
		}
	}

	// 2. Классы: дескрипторы, родители, интерфейсы.
	var classDecls []*ClassDecl
	for _, st := range prog.Stmts {
		if cd, ok := st.(*ClassDecl); ok {
			classDecls = append(classDecls, cd)
		}
	}
	for _, ai := range anonInfos {
		classDecls = append(classDecls, ai.decl)
	}
	// Объявления вне верхнего уровня (в телах функций/замыканий, под условием
	// и т.п.) собираются для построения дескрипторов, но не попадают в
	// classIdx: они делаются видимыми в рантайме через OpDeclareClass.
	nestedSet := map[*ClassDecl]bool{}
	for _, cd := range nestedClassDecls {
		nestedSet[cd] = true
		if cd.DefFile == "" {
			cd.DefFile = file
		}
		cd.Strict = prog.StrictTypes
		classDecls = append(classDecls, cd)
	}
	base := newCompiler(funcs, idx)
	base.haltOffset = mainHaltOffset
	base.file = file
	base.strict = prog.StrictTypes
	base.chunk.Strict = prog.StrictTypes
	base.chunk.SetEmitStrict(prog.StrictTypes)
	base.anonNames = anonMap
	base.classAlias = compileAliases
	base.stmtLines = prog.Lines
	base.exprLines = prog.ExprLines
	base.chunk.File = file
	if err := checkSpecialAttrTargets(base, decls, classDecls); err != nil {
		return nil, err
	}
	var classes []vm.ClassInfo
	if linkBase != nil {
		classes = append([]vm.ClassInfo(nil), linkBase.Classes...)
	} else {
		classes = append(classes, vm.NewExceptionClasses()...)
		registerDirectoryMethods(classes)
		builtinIfaces := vm.NewBuiltinInterfaces()
		classes = append(classes, builtinIfaces...)
		classes = append(classes, vm.NewClosureClass())
		classes = append(classes, vm.NewSensitiveParameterValueClass())
		classes = append(classes, vm.NewDateClasses()...)
		classes = append(classes, vm.NewSplClasses(builtinIfaces)...)
		classes = append(classes, vm.NewStreamClasses()...)
		classes = append(classes, vm.NewReflectionClasses()...)
	}
	classBase := len(classes)
	classIdx := map[string]int{}
	builtinClassNames := map[string]bool{}
	for i, ci := range classes {
		classIdx[ci.Name()] = i
		builtinClassNames[strings.ToLower(ci.Name())] = true
	}
	seenTopClass := map[string]bool{}
	for _, cd := range classDecls {
		if err := base.reservedClassName(cd.Name); err != nil {
			return nil, err
		}
		if err := reservedClassRefFatal(cd); err != nil {
			return nil, err
		}
		if !cd.IsInterface {
			for _, n := range cd.Interfaces {
				if strings.EqualFold(strings.TrimLeft(n, "\\"), "Throwable") {
					noun := "Class"
					tail := ", extend Exception or Error instead"
					if cd.IsEnum {
						noun = "Enum"
						tail = ""
					}
					return nil, &fatalError{
						msg:  fmt.Sprintf("%s %s cannot implement interface Throwable%s", noun, cd.Name, tail),
						line: cd.Line,
					}
				}
			}
		}
		kind := "class"
		if cd.IsInterface {
			kind = "interface"
		} else if cd.IsTrait {
			kind = "trait"
		} else if cd.IsEnum {
			kind = "enum"
		}
		key := strings.ToLower(strings.TrimLeft(cd.Name, "\\"))
		if builtinClassNames[key] {
			// Имя занято встроенным классом/интерфейсом.
			cd.RuntimeFatal = fmt.Sprintf("Cannot declare %s %s, because the name is already in use", kind, cd.Name)
		} else if !nestedSet[cd] {
			if seenTopClass[key] {
				cd.RuntimeFatal = fmt.Sprintf("Cannot declare %s %s, because the name is already in use", kind, cd.Name)
			}
			seenTopClass[key] = true
		}
		var ci vm.ClassInfo
		if cd.IsInterface {
			ci = vm.NewInterface(cd.Name)
		} else {
			ci = vm.NewClass(cd.Name, nil)
		}
		if cd.IsTrait {
			ci.SetDeclaredTrait()
		}
		if cd.IsEnum {
			ci.SetEnumBacking(cd.EnumBacking)
			// Виртуальные readonly-свойства enum-кейса (видны рефлексии).
			ci.SetPropMeta("name", "string", true, false, vm.Public)
			if cd.EnumBacking != "" {
				ci.SetPropMeta("value", cd.EnumBacking, true, false, vm.Public)
			}
			if cd.EnumBacking != "" && cd.EnumBacking != "int" && cd.EnumBacking != "string" {
				return nil, base.newFatal(fmt.Sprintf("Enum backing type must be int or string, %s given", canonicalUnionType(cd.EnumBacking)))
			}
		}
		if !nestedSet[cd] {
			classIdx[cd.Name] = len(classes)
		}
		classes = append(classes, ci)
	}

	// Дескриптор __PHP_Incomplete_Class нужен уже при разрешении родителей
	// (final-проверка FR #78638), поэтому добавляется сразу после
	// пользовательских дескрипторов. При линковке (linkBase != nil) дескриптор
	// уже есть в базовой таблице — переиспользуем его индекс.
	incompleteIdx := -1
	if linkBase != nil && linkBase.IncompleteIdx >= 0 {
		incompleteIdx = linkBase.IncompleteIdx
	} else {
		classes = append(classes, incompleteClassInfo)
		incompleteIdx = len(classes) - 1
	}
	classIdx[incompleteClassName] = incompleteIdx

	for i, cd := range classDecls {
		ci := classes[classBase+i]
		if cd.Parent != "" {
			pi, ok := classIdx[cd.Parent]
			if !ok {
				// Имена классов в PHP регистронезависимы.
				pi, ok = ciLookup(classIdx, cd.Parent)
			}
			if !ok {
				// Родитель может быть определён автозагрузкой в рантайме.
				cd.MissingParent = cd.Parent
			} else if vm.IsFinalClass(classes[pi]) {
				base.curLine = cd.Line
				return nil, base.newFatal(fmt.Sprintf("Class %s cannot extend final class %s", classDeclDisplayName(cd), classes[pi].Name()))
			} else {
				for _, pc := range classDecls {
					if pc.IsFinal && strings.EqualFold(pc.Name, cd.Parent) {
						return nil, base.newFatal(fmt.Sprintf("Class %s cannot extend final class %s", cd.Name, pc.Name))
					}
					if pc.IsInterface && strings.EqualFold(pc.Name, cd.Parent) {
						return nil, base.newFatal(fmt.Sprintf("Class %s cannot extend interface %s", cd.Name, pc.Name))
					}
					if pc.IsTrait && strings.EqualFold(pc.Name, cd.Parent) {
						return nil, base.newFatal(fmt.Sprintf("Class %s cannot extend trait %s", cd.Name, pc.Name))
					}
					if strings.EqualFold(pc.Name, cd.Parent) {
						parentReadonly := pc.IsReadonly
						if cd.IsReadonly && !parentReadonly {
							return nil, base.newFatal(fmt.Sprintf("Readonly class %s cannot extend non-readonly class %s", ci.Name(), pc.Name))
						}
						if !cd.IsReadonly && parentReadonly {
							return nil, base.newFatal(fmt.Sprintf("Non-readonly class %s cannot extend readonly class %s", ci.Name(), pc.Name))
						}
					}
				}
				ci.SetParent(classes[pi])
			}
		}
		for _, in := range cd.Interfaces {
			ii, ok := classIdx[in]
			if !ok {
				ii, ok = ciLookup(classIdx, in)
			}
			if !ok {
				// Интерфейс может быть определён автозагрузкой в рантайме.
				if cd.MissingInterface == "" {
					cd.MissingInterface = in
				}
				cd.MissingIfaces = append(cd.MissingIfaces, in)
				continue
			}
			if in == "UnitEnum" || in == "BackedEnum" {
				if cd.IsEnum {
					if in == "BackedEnum" && cd.EnumBacking == "" {
						return nil, base.newFatal(fmt.Sprintf("Non-backed enum %s cannot implement interface BackedEnum", cd.Name))
					}
					return nil, base.newFatal(fmt.Sprintf("Enum %s cannot implement previously implemented interface %s", cd.Name, in))
				}
				return nil, base.newFatal(fmt.Sprintf("Non-enum class %s cannot implement interface %s", cd.Name, in))
			}
			if cd.IsEnum && in == "Serializable" {
				if cd.RuntimeFatal == "" {
					cd.RuntimeFatal = "Enum " + cd.Name + " cannot implement the Serializable interface"
				}
				continue
			}
			if !cd.IsInterface && !cd.IsEnum && strings.EqualFold(classes[ii].Name(), "DateTimeInterface") &&
				!classInheritsInterface(ci.Parent(), "DateTimeInterface") {
				if cd.RuntimeFatal == "" {
					cd.RuntimeFatal = "DateTimeInterface can't be implemented by user classes"
				}
				continue
			}
			if id := declByName0(classDecls, in); id != nil && !id.IsInterface && !nestedSet[cd] {
				if cd.MissingParent != "" {
					// Родитель разрешается в рантайме: Zend проверяет
					// «интерфейс» после его автозагрузки (OpFatal на позиции
					// объявления, см. emitStmt).
					if cd.IfaceNotInterface == "" {
						cd.IfaceNotInterface = in
					}
					continue
				}
				return nil, base.newFatal(fmt.Sprintf("%s cannot implement %s - it is not an interface", cd.Name, in))
			}
			ci.Implements(classes[ii])
		}
		if cd.IsEnum {
			ifaceName := "UnitEnum"
			if cd.EnumBacking != "" {
				ifaceName = "BackedEnum"
			}
			if ii, ok := classIdx[ifaceName]; ok {
				ci.Implements(classes[ii])
			}
		}
		// Zend: класс не может реализовывать одновременно Iterator и
		// IteratorAggregate (в т.ч. унаследовав один из них).
		if !cd.IsInterface && !cd.IsEnum && cd.MissingParent == "" && cd.MissingInterface == "" &&
			classInheritsInterface(ci, "Iterator") && classInheritsInterface(ci, "IteratorAggregate") {
			base.curLine = cd.Line
			return nil, base.newFatal(fmt.Sprintf(
				"Class %s cannot implement both Iterator and IteratorAggregate at the same time",
				classDeclDisplayName(cd)))
		}
	}
	// Deprecated: class implements Serializable without __serialize/__unserialize.
	// PHP печатает это до начала выполнения (объявление класса с ранним
	// связыванием), поэтому копим в стартовых диагностиках.
	serDeclByName := map[string]*ClassDecl{}
	for _, cd := range classDecls {
		key := strings.ToLower(cd.Name)
		if _, ok := serDeclByName[key]; !ok {
			serDeclByName[key] = cd
		}
	}
	for _, cd := range classDecls {
		if cd.IsEnum {
			if !declImplementsSerializable(cd, serDeclByName) {
				continue
			}
			if cd.RuntimeFatal == "" {
				cd.RuntimeFatal = "Enum " + cd.Name + " cannot implement the Serializable interface"
			}
			hasSer, hasUnser := false, false
			for _, m := range cd.Members {
				if m.Kind != "method" {
					continue
				}
				if strings.EqualFold(m.MName, "__serialize") {
					hasSer = true
				}
				if strings.EqualFold(m.MName, "__unserialize") {
					hasUnser = true
				}
			}
			if !(hasSer && hasUnser) {
				compilerDeprecations = append(compilerDeprecations, fmt.Sprintf(
					"%s implements the Serializable interface, which is deprecated. Implement __serialize() and __unserialize() instead (or in addition, if support for old PHP versions is necessary) in %s on line %d",
					cd.Name, file, cd.Line))
			}
			continue
		}
		if cd.IsInterface || cd.IsTrait {
			continue
		}
		serializable, hasSer, hasUnser := false, false, false
		for cur := cd; cur != nil; {
			for _, in := range cur.Interfaces {
				if strings.EqualFold(in, "Serializable") {
					serializable = true
				}
			}
			for _, m := range cur.Members {
				if m.Kind != "method" {
					continue
				}
				if strings.EqualFold(m.MName, "__serialize") {
					hasSer = true
				}
				if strings.EqualFold(m.MName, "__unserialize") {
					hasUnser = true
				}
			}
			if cur.Parent == "" {
				break
			}
			next := serDeclByName[strings.ToLower(strings.TrimLeft(cur.Parent, "\\"))]
			if next == nil && linkBase != nil {
				// Родитель объявлен в другом юните (рантайм-eval/include):
				// Serializable мог прийти из уже загруженной иерархии.
				// Диагностику печатает runSubCode при исполнении фрагмента.
				if pi, ok := ciLookup(classIdx, cur.Parent); ok {
					pci := classes[pi]
					if classInheritsInterface(pci, "Serializable") {
						serializable = true
					}
					if pci.HasMethod("__serialize") {
						hasSer = true
					}
					if pci.HasMethod("__unserialize") {
						hasUnser = true
					}
				}
				break
			}
			cur = next
		}
		if !serializable || (hasSer && hasUnser) {
			continue
		}
		compilerDeprecations = append(compilerDeprecations, fmt.Sprintf(
			"%s implements the Serializable interface, which is deprecated. Implement __serialize() and __unserialize() instead (or in addition, if support for old PHP versions is necessary) in %s on line %d",
			cd.Name, file, cd.Line))
	}

	// Дескриптор __PHP_Incomplete_Class зарегистрирован выше (до разрешения
	// родителей): final-проверка и MissingParent видят его в classIdx.
	base.classes = classes
	base.classIdx = classIdx
	// Рантайм-объявления (вне верхнего уровня): фиксируем индекс дескриптора,
	// скрываем класс до объявления и запоминаем отложенные родителя/интерфейсы.
	base.declIdx = map[*ClassDecl]int{}
	base.declIndexAll = make(map[*ClassDecl]int, len(classDecls))
	for i, cd := range classDecls {
		base.declIndexAll[cd] = classBase + i
		if !nestedSet[cd] {
			continue
		}
		idx := classBase + i
		base.declIdx[cd] = idx
		ci := classes[idx]
		vm.HideClass(ci)
		if cd.MissingParent != "" {
			vm.SetClassMissingParent(ci, cd.MissingParent)
		}
		for _, in := range cd.MissingIfaces {
			vm.AddClassMissingInterface(ci, in)
		}
	}

	// 2-pre. Замыкания/стрелочные функции: регистрируем в пуле заранее (чтобы
	// OpClosure знал индекс) и компилируем тела.
	closureInfos, closureMap := collectClosures(prog, classes, classIdx)
	base.closureIdx = closureMap
	for _, ci := range closureInfos {
		ncap := len(ci.uses)
		if ci.isArrow {
			ncap = len(ci.free)
		}
		sens, sensVar := sensitiveMask(ci.params)
		var capNames []string
		if ci.isArrow {
			capNames = append([]string(nil), ci.free...)
		} else {
			for _, u := range ci.uses {
				capNames = append(capNames, strings.TrimPrefix(u.Name, "$"))
			}
		}
		fn := &vm.Function{Name: "{closure}", NumArgs: funcNumArgs(ci.params),
			ByRefArgs: byrefMask(ci.params), ByRefReturn: ci.byrefRet,
			ParamNames: paramNames(ci.params), ParamTypes: paramTypes(ci.params),
			NumRequired: numRequired(ci.params), DefLine: ci.line,
			RetType:     normalizedType(ci.retType),
			NumCaptures: ncap, IsStatic: ci.static,
			CaptureNames:    capNames,
			SensitiveParams: sens, SensitiveVariadic: sensVar,
			VariadicByRef: variadicByRef(ci.params), Attrs: makeAttrsInfo(ci.attrs, base.file, base.strict, base.attrThunks(ci.cls)),
			ParamAttrs: paramAttrsInfo(ci.params, base.file, base.strict, base.attrThunks(ci.cls))}
		ci.idx = len(funcs)
		funcs = append(funcs, fn)
	}
	// funcs вырос (append мог перевыделить массив); обновляем срезы всех
	// компиляторов, иначе allocEnv видит устаревшую длину.
	base.funcs = funcs
	for _, ci := range closureInfos {
		save := base.curClass
		base.curClass = ci.cls
		base.closureBody = true
		cf, err := base.compileClosureBody(ci)
		base.closureBody = false
		base.curClass = save
		if err != nil {
			return nil, err
		}
		funcs[ci.idx].Executable = cf
		funcs[ci.idx].UsesThis = cf.UsesThis()
	}

	// Предпроход: byref-маски методов классов/трейтов собираются до компиляции
	// тел, чтобы вызов `self::method()` в теле трейта учитывал маску метода
	// класса-потребителя независимо от порядка объявления.
	for _, cd := range classDecls {
		for _, m := range cd.Members {
			if m.Kind != "method" {
				continue
			}
			if mask := byrefMask(m.Params); mask != nil {
				base.methodRefs[m.MName] = mergeMask(base.methodRefs[m.MName], mask)
			}
		}
	}

	// 2a. Трейты первыми: их методы копируются через Use.
	for _, isTrait := range []bool{true, false} {
		for i, cd := range classDecls {
			if cd.IsTrait != isTrait {
				continue
			}
			ci := classes[classBase+i]
			base.curClass = ci
			base.noParentScope = !cd.IsTrait && cd.Parent == ""
			if len(cd.Attrs) > 0 {
				vm.SetClassAttrs(ci, makeAttrsInfo(cd.Attrs, attrFileOf(cd.DefFile, base.file), cd.Strict, base.attrThunks(ci)))
				if err := checkInternalAttributeTarget(cd.Attrs, 1, cd.Line); err != nil {
					return nil, err
				}
				if countClassAttr(cd.Attrs, "Attribute") > 1 {
					return nil, base.newFatal("Attribute \"Attribute\" must not be repeated")
				}
				found, flags, expr := classAttributeMarker(cd.Attrs)
				if found {
					if expr == nil {
						ci.MarkAttributeClass(flags)
					} else if v, verr := constValueOf(expr); verr == nil {
						if n, ok := constInt(v); ok {
							ci.MarkAttributeClass(uint32(int32(n)))
						} else {
							// Неверный тип аргумента проверяется при newInstance.
							if th, terr := base.compileConstThunk(ci, expr); terr == nil {
								ci.MarkAttributeClassDynamic(vm.NewThunk(th, ci))
							} else {
								ci.MarkAttributeClass(0x3f)
							}
						}
					} else if th, terr := base.compileConstThunk(ci, expr); terr == nil {
						ci.MarkAttributeClassDynamic(vm.NewThunk(th, ci))
					} else {
						ci.MarkAttributeClass(0x3f)
					}
				}
			}
			if cd.IsFinal || cd.IsEnum {
				vm.MarkFinal(ci)
			}
			vm.SetClassDefinition(ci, base.file, cd.Line, cd.Line)
			if strings.HasPrefix(ci.Name(), "\x00anon#") {
				// PHP 8.3: анонимные классы запрещены к (де)сериализации.
				ci.SetNotSerializable()
			}
			if cd.ReadonlyDup {
				return nil, base.newFatal("Multiple readonly modifiers are not allowed")
			}
			if cd.FinalDup {
				return nil, base.newFatal("Multiple final modifiers are not allowed")
			}
			if cd.IsFinal && cd.IsAbstract {
				return nil, base.newFatal("Cannot use the final modifier on an abstract class")
			}
			for _, at := range cd.Attrs {
				if strings.EqualFold(strings.TrimLeft(at.Name, "\\"), "AllowDynamicProperties") {
					if cd.IsInterface {
						return nil, base.newFatal("Cannot apply #[AllowDynamicProperties] to interface")
					}
					if cd.IsTrait {
						return nil, base.newFatal("Cannot apply #[AllowDynamicProperties] to trait")
					}
				}
			}
			if cd.IsReadonly {
				ci.SetReadonlyClass()
			}
			if cd.IsAbstract {
				ci.SetAbstract()
			}
			seenConsts := map[string]bool{}
			seenProps := map[string]bool{}
			seenMethods := map[string]bool{}
			for _, m := range cd.Members {
				base.curLine = m.Line
				switch m.FirstDup {
				case "access":
					return nil, base.newFatal("Multiple access type modifiers are not allowed")
				case "final":
					return nil, base.newFatal("Multiple final modifiers are not allowed")
				case "readonly":
					return nil, base.newFatal("Multiple readonly modifiers are not allowed")
				case "abstract":
					return nil, base.newFatal("Multiple abstract modifiers are not allowed")
				case "static":
					return nil, base.newFatal("Multiple static modifiers are not allowed")
				}
				if m.Kind == "prop" {
					if m.Abstract {
						return nil, base.newFatal("Cannot use the abstract modifier on a property")
					}
					if m.Final {
						return nil, base.newFatal("Cannot use the final modifier on a property")
					}
				}
				if m.Kind == "method" || m.Kind == "signature" {
					switch strings.ToLower(m.MName) {
					case "__construct", "__destruct", "__clone":
						if m.Static {
							return nil, base.newFatal(fmt.Sprintf("Method %s::%s() cannot be static", ci.Name(), m.MName))
						}
					}
					if seenMethods[strings.ToLower(m.MName)] {
						return nil, base.newFatal(fmt.Sprintf("Cannot redeclare %s::%s()", ci.Name(), m.MName))
					}
					seenMethods[strings.ToLower(m.MName)] = true
					// В трейтах абстрактный private-метод разрешён (PHP 8.0):
					// реализацию обязан предоставить использующий класс.
					if m.Abstract && !cd.IsInterface && !cd.IsTrait && visOf(m.Vis) == vm.Private {
						return nil, base.newFatal(fmt.Sprintf("Abstract function %s::%s() cannot be declared private", ci.Name(), m.MName))
					}
				}
				switch m.Kind {
				case "case":
					if !cd.IsEnum {
						return nil, base.newFatal("Case can only be used in enums")
					}
					if cd.EnumBacking == "" && m.ConstVal != nil {
						return nil, base.newFatal(fmt.Sprintf("Case %s of non-backed enum %s must not have a value", m.ConstName, cd.Name))
					}
					if cd.EnumBacking != "" && m.ConstVal == nil {
						return nil, base.newFatal(fmt.Sprintf("Case %s of backed enum %s must have a value", m.ConstName, cd.Name))
					}
					var val vm.Value
					var th *vm.Thunk
					if m.ConstVal != nil {
						v, t, err := base.makeConstValue(ci, m.ConstVal, ci.Name()+"::"+m.ConstName)
						if err != nil {
							return nil, err
						}
						val, th = v, t
					}
					if th != nil {
						ci.AddLazyEnumCase(m.ConstName, th, classBase+i)
					} else {
						ci.AddEnumCase(m.ConstName, val, m.ConstVal != nil, classBase+i)
					}
					if len(m.Attrs) > 0 {
						if err := checkInternalAttributeTarget(m.Attrs, 16, m.Line); err != nil {
							return nil, err
						}
						vm.SetConstAttrs(ci, m.ConstName, makeAttrsInfo(m.Attrs, attrFileOf(cd.DefFile, base.file), cd.Strict, base.attrThunks(ci)))
					}
				case "prop":
					if cd.IsEnum {
						return nil, base.newFatal("Enum " + cd.Name + " cannot include properties")
					}
					if cd.IsInterface {
						return nil, base.newFatal("Interfaces may not include properties")
					}
					if seenProps[m.PropName] {
						return nil, base.newFatal(fmt.Sprintf("Cannot redeclare %s::$%s", ci.Name(), m.PropName))
					}
					seenProps[m.PropName] = true
					if err := base.validateType(m.PropType); err != nil {
						return nil, err
					}
					if bad := forbiddenPropType(m.PropType); bad {
						return nil, base.newFatal(fmt.Sprintf("Property %s::$%s cannot have type %s", ci.Name(), m.PropName, m.PropType))
					}
					readonly := m.Readonly || cd.IsReadonly
					if m.ReadonlyDup {
						return nil, base.newFatal("Multiple readonly modifiers are not allowed")
					}
					if m.Static && readonly {
						return nil, base.newFatal(fmt.Sprintf("Static property %s::$%s cannot be readonly", ci.Name(), m.PropName))
					}
					if readonly && m.PropType == "" {
						return nil, base.newFatal(fmt.Sprintf("Readonly property %s::$%s must have type", ci.Name(), m.PropName))
					}
					if readonly && m.Default != nil {
						return nil, base.newFatal(fmt.Sprintf("Readonly property %s::$%s cannot have default value", ci.Name(), m.PropName))
					}
					if m.Default != nil && m.PropType != "" {
						if k, ok := defaultKind(m.Default); ok {
							if k == "null" {
								if !typeNullable(m.PropType) {
									dt := displayType(m.PropType)
									if topLevelSplitPHP(m.PropType, '&') >= 0 {
										return nil, base.newFatal(fmt.Sprintf("Cannot use null as default value for property %s::$%s of type %s", ci.Name(), m.PropName, dt))
									}
									sugg := "?" + dt
									if strings.Contains(dt, "|") {
										sugg = dt + "|null"
									}
									return nil, base.newFatal(fmt.Sprintf("Default value for property of type %s may not be null. Use the nullable type %s to allow null default value", dt, sugg))
								}
							} else if !typeAllowsDefault(m.PropType, k) {
								return nil, base.newFatal(fmt.Sprintf("Cannot use %s as default value for property %s::$%s of type %s", k, ci.Name(), m.PropName, displayType(m.PropType)))
							}
						}
					}
					ci.SetPropMeta(m.PropName, m.PropType, readonly, m.Static, visOf(m.Vis))
					if !m.PromotedAttrs {
						if err := checkInternalAttributeTarget(m.Attrs, 8, m.Line); err != nil {
							return nil, err
						}
					}
					vm.SetPropAttrs(ci, m.PropName, makeAttrsInfo(m.Attrs, attrFileOf(cd.DefFile, base.file), cd.Strict, base.attrThunks(ci)))
					if m.Static {
						v, th, err := base.makeConstValue(ci, m.Default, ci.Name()+"::$"+m.PropName)
						if err != nil {
							return nil, err
						}
						if th != nil {
							ci.DefineLazyStatic(m.PropName, th)
						} else {
							ci.DefineStatic(m.PropName, coerceFloatDefaultValue(v, m.PropType))
						}
						if m.Default == nil && m.PropType != "" {
							ci.SetStaticUninit(m.PropName)
						}
					} else if m.Default != nil {
						v, th, err := base.makeConstValue(ci, m.Default, ci.Name()+"::$"+m.PropName)
						if err != nil {
							return nil, err
						}
						if th != nil {
							ci.DeclareLazyDefault(m.PropName, th)
						} else {
							ci.DeclareDefault(m.PropName, coerceFloatDefaultValue(v, m.PropType))
						}
					} else if m.PropType == "" && !readonly {
						// Объявленное нетипизированное свойство без значения
						// по умолчанию инициализируется null (isset → false).
						ci.DeclareDefault(m.PropName, vm.MakeNull())
					}
				case "const":
					if m.Readonly {
						return nil, base.newFatal("Cannot use the readonly modifier on a class constant")
					}
					if m.StaticKw {
						return nil, base.newFatal("Cannot use the static modifier on a class constant")
					}
					if m.Abstract {
						return nil, base.newFatal("Cannot use the abstract modifier on a class constant")
					}
					if cd.IsInterface && visOf(m.Vis) != vm.Public {
						return nil, base.newFatal(fmt.Sprintf("Access type for interface constant %s::%s must be public", ci.Name(), m.ConstName))
					}
					if strings.EqualFold(m.ConstName, "class") {
						return nil, base.newFatal("A class constant must not be called 'class'; it is reserved for class name fetching")
					}
					if seenConsts[strings.ToLower(m.ConstName)] {
						return nil, base.newFatal(fmt.Sprintf("Cannot redefine class constant %s::%s", ci.Name(), m.ConstName))
					}
					seenConsts[strings.ToLower(m.ConstName)] = true
					if err := base.validateType(m.ConstType); err != nil {
						return nil, err
					}
					if bad := forbiddenConstType(m.ConstType); bad != "" {
						return nil, base.newFatal("Class constant " + ci.Name() + "::" + m.ConstName + " cannot have type " + bad)
					}
					v, th, err := base.makeConstValue(ci, m.ConstVal, ci.Name()+"::"+m.ConstName)
					if err != nil {
						return nil, err
					}
					ci.SetConstVis(m.ConstName, visOf(m.Vis))
					ci.SetConstDoc(m.ConstName, m.Doc)
					vm.SetConstAttrs(ci, m.ConstName, makeAttrsInfo(m.Attrs, attrFileOf(cd.DefFile, base.file), cd.Strict, base.attrThunks(ci)))
					if th != nil {
						th.Label = ci.Name() + "::" + m.ConstName
						if m.ConstType != "" {
							th.TypeConstr = m.ConstType
						}
						ci.DefineLazyConst(m.ConstName, th)
					} else {
						if m.ConstType != "" {
							if k, ok := defaultKind(m.ConstVal); ok {
								allowed := false
								if k == "null" {
									allowed = typeNullable(m.ConstType)
								} else {
									allowed = typeAllowsDefault(m.ConstType, k)
								}
								if !allowed {
									return nil, base.newFatal(fmt.Sprintf("Cannot use %s as value for class constant %s::%s of type %s",
										k, ci.Name(), m.ConstName, displayType(m.ConstType)))
								}
							}
							// int-значение для float-константы расширяется до float.
							if defaultCoercesToFloat(m.ConstType) {
								if s, ok := v.(vm.Slot); ok && s.Type() == vm.IntType {
									v = vm.MakeFloat(float64(s.ToInt()))
								}
							}
						}
						ci.DefineConst(m.ConstName, v)
					}
				case "signature":
					if !m.Abstract && !cd.IsAbstract && !cd.IsInterface {
						// `function f();` без abstract в неабстрактном классе
						// (Zend: Non-abstract method ... must contain body).
						base.curLine = m.Line
						return nil, base.newFatal(fmt.Sprintf("Non-abstract method %s::%s() must contain body", ci.Name(), m.MName))
					}
					if m.Readonly {
						return nil, base.newFatal("Cannot use the readonly modifier on a method")
					}
					if m.Final {
						if cd.IsInterface {
							return nil, base.newFatal(fmt.Sprintf("Interface method %s::%s() must not be final", ci.Name(), m.MName))
						}
						return nil, base.newFatal("Cannot use the final modifier on an abstract method")
					}
					if cd.IsInterface && visOf(m.Vis) != vm.Public {
						return nil, base.newFatal(fmt.Sprintf("Access type for interface method %s::%s() must be public", ci.Name(), m.MName))
					}
					if cd.IsInterface && m.Abstract {
						return nil, base.newFatal(fmt.Sprintf("Interface method %s::%s() must not be abstract", ci.Name(), m.MName))
					}
					if err := base.checkMagicMethod(cd.Name, m); err != nil {
						return nil, err
					}
					// Контракт без тела (интерфейс / abstract function):
					// не вызываемая реализация, видна рефлексии.
					if len(m.Attrs) > 0 {
						if err := checkInternalAttributeTarget(m.Attrs, 4, m.Line); err != nil {
							return nil, err
						}
						vm.SetMethodAttrs(ci, m.MName, makeAttrsInfo(m.Attrs, attrFileOf(cd.DefFile, base.file), cd.Strict, base.attrThunks(ci)))
					}
					ci.AddSignature(m.MName, visOf(m.Vis))
					// Метаданные сигнатуры для рефлексии (RetType/параметры):
					// вызова она не создаёт — lookupMethod её не видит.
					sensS, sensVarS := sensitiveMask(m.Params)
					vm.SetSignatureFn(ci, m.MName, &vm.Function{
						Name: m.MName, NumArgs: funcNumArgs(m.Params),
						ByRefArgs: byrefMask(m.Params), ByRefReturn: m.ByRefReturn,
						ParamNames: paramNames(m.Params), ParamTypes: paramTypes(m.Params),
						NumRequired:     numRequired(m.Params),
						RetType:         normalizedType(m.RetType),
						SensitiveParams: sensS, SensitiveVariadic: sensVarS,
						VariadicByRef: variadicByRef(m.Params),
					})
				case "method":
					if m.Readonly {
						return nil, base.newFatal("Cannot use the readonly modifier on a method")
					}
					if cd.IsInterface {
						return nil, base.newFatal(fmt.Sprintf("Interface function %s::%s() cannot contain body", ci.Name(), m.MName))
					}
					if cd.IsEnum && enumForbiddenMagic[strings.ToLower(m.MName)] {
						return nil, base.newFatal("Enum " + cd.Name + " cannot include magic method " + m.MName)
					}
					if err := base.checkMagicMethod(cd.Name, m); err != nil {
						return nil, err
					}
					if magicMustBePublic[strings.ToLower(m.MName)] && visOf(m.Vis) != vm.Public {
						compilerWarnings = append(compilerWarnings,
							fmt.Sprintf("The magic method %s::%s() must have public visibility in %s on line %d", cd.Name, m.MName, file, m.Line))
					}
					if err := base.validateType(m.RetType); err != nil {
						return nil, err
					}
					if mask := byrefMask(m.Params); mask != nil {
						base.methodRefs[m.MName] = mergeMask(base.methodRefs[m.MName], mask)
					}
					savedStrict := base.strict
					base.strict = cd.Strict
					savedTicks := base.ticks
					base.ticks = m.Ticks
					cf, err := base.compileFuncBody(ci.Name()+"::"+m.MName, m.Params, m.Body, m.ByRefReturn, m.RetType, m.Line)
					base.strict = savedStrict
					base.ticks = savedTicks
					if err != nil {
						return nil, err
					}
					sens, sensVar := sensitiveMask(m.Params)
					fn := &vm.Function{Name: m.MName, NumArgs: funcNumArgs(m.Params),
						ByRefArgs: byrefMask(m.Params), ByRefReturn: m.ByRefReturn,
						ParamNames: paramNames(m.Params), ParamTypes: paramTypes(m.Params),
						NumRequired: numRequired(m.Params),
						RetType:     normalizedType(m.RetType), Executable: cf,
						SensitiveParams: sens, SensitiveVariadic: sensVar,
						VariadicByRef: variadicByRef(m.Params),
						ParamAttrs:    paramAttrsInfo(m.Params, attrFileOf(cd.DefFile, base.file), cd.Strict, base.attrThunks(ci))}
					// __toString неявно возвращает string (проверка как в Zend).
					if m.MName == "__toString" && fn.RetType == "" {
						fn.RetType = "string"
					}
					fn.IsFinal = m.Final
					fn.IsStatic = m.Static
					if m.Final && visOf(m.Vis) == vm.Private {
						compilerWarnings = append(compilerWarnings,
							fmt.Sprintf("Private methods cannot be final as they are never overridden by other classes in %s on line %d", file, m.Line))
					}
					cf.SetOwner(fn)
					if strings.HasPrefix(ci.Name(), "\x00anon#") {
						base := "class"
						if p := ci.Parent(); p != nil {
							base = p.Name()
						}
						fn.DisplayName = base + "@anonymous::" + m.MName
					}
					if cd.DefFile != "" {
						fn.DefFile = cd.DefFile
					} else {
						fn.DefFile = base.file
					}
					fn.DefLine = m.Line
					// Пустое тело без параметров: вызов можно пропустить,
					// вернув null (skip-call).
					if len(m.Body) == 0 && len(m.Params) == 0 && !m.ByRefReturn {
						fn.MarkTrivial()
					}
					if len(m.Attrs) > 0 {
						fn.Attrs = makeAttrsInfo(m.Attrs, attrFileOf(cd.DefFile, base.file), cd.Strict, base.attrThunks(ci))
					}
					if m.MName == "__construct" {
						ci.Constructor(fn, visOf(m.Vis))
					} else {
						ci.Method(m.MName, fn, visOf(m.Vis))
					}
					for pi, p := range m.Params {
						if len(p.Attrs) > 0 {
							if err := checkInternalAttributeTarget(p.Attrs, 32, m.Line); err != nil {
								return nil, err
							}
							vm.SetParamAttrs(ci, m.MName, pi, makeAttrsInfo(p.Attrs, attrFileOf(cd.DefFile, base.file), cd.Strict, base.attrThunks(ci)))
						}
					}
				}
			}
		}
	}
	declByName := map[string]*ClassDecl{}
	for _, cd := range classDecls {
		declByName[strings.ToLower(cd.Name)] = cd
	}
	// aliasFinal — методы, ставшие final через `use T { m as final; }`.
	aliasFinal := map[*ClassDecl]map[string]bool{}
	// constFinal — final-константы класса: собственные (final_const2) и
	// полученные из трейта.
	constFinal := map[*ClassDecl]map[string]bool{}
	for _, cd := range classDecls {
		for _, m := range cd.Members {
			if m.Kind == "const" && m.Final {
				if constFinal[cd] == nil {
					constFinal[cd] = map[string]bool{}
				}
				constFinal[cd][strings.ToLower(m.ConstName)] = true
			}
		}
	}

	// 2b. Примешиваем трейты (после компиляции всех тел) с учётом адаптаций.
classLoop:
	for i, cd := range classDecls {
		ci := classes[classBase+i]
		if cd.IsInterface && len(cd.Uses) > 0 {
			return nil, base.newFatal(fmt.Sprintf("Cannot use traits inside of interfaces. %s is used in %s", cd.Uses[0], cd.Name))
		}
		skip := map[string]map[string]bool{}
		for _, ta := range cd.TraitAdapt {
			for _, ex := range ta.Insteadof {
				el := strings.ToLower(ex)
				if skip[el] == nil {
					skip[el] = map[string]bool{}
				}
				skip[el][ta.Method] = true
				skip[el][strings.ToLower(ta.Method)] = true
			}
		}
		// Валидация адаптаций use (insteadof/as): ссылки на несуществующие
		// методы и несовместимые insteadof-правила.
		for _, ta := range cd.TraitAdapt {
			if !ta.Alias {
				continue
			}
			if ta.AliasStatic {
				return nil, base.newFatal(`Cannot use "static" as method modifier in trait alias`)
			}
			if ta.AliasAbstract {
				return nil, base.newFatal(`Cannot use "abstract" as method modifier in trait alias`)
			}
			if ta.Trait != "" {
				td := declByName0(classDecls, ta.Trait)
				if td == nil {
					return nil, base.newFatal(fmt.Sprintf("Could not find trait %s", ta.Trait))
				}
				if !td.IsTrait {
					return nil, base.newFatal(fmt.Sprintf("Class %s is not a trait, Only traits may be used in 'as' and 'insteadof' statements", ta.Trait))
				}
				if usePos(cd.Uses, ta.Trait) < 0 {
					return nil, base.newFatal(fmt.Sprintf("Required Trait %s wasn't added to %s", ta.Trait, cd.Name))
				}
				if !declOrTraitHasMethod(classDecls, td, ta.Method, map[string]bool{}) {
					return nil, base.newFatal(fmt.Sprintf("An alias was defined for %s::%s but this method does not exist", ta.Trait, ta.Method))
				}
				continue
			}
			if !declOrTraitHasMethod(classDecls, cd, ta.Method, map[string]bool{}) {
				if ta.AliasName == "" {
					return nil, base.newFatal(fmt.Sprintf("The modifiers of the trait method %s() are changed, but this method does not exist. Error", ta.Method))
				}
				return nil, base.newFatal(fmt.Sprintf("An alias (%s) was defined for method %s(), but this method does not exist", ta.AliasName, ta.Method))
			}
			// Метод есть в нескольких трейтах без insteadof-разрешения:
			// Zend запрещает alias и просит указать T::method (bug62069).
			if ta.AliasName != "" {
				var prov []string
				for _, tr := range cd.Uses {
					td2 := declByName0(classDecls, tr)
					if td2 == nil || !td2.IsTrait {
						continue
					}
					if skip[strings.ToLower(tr)][strings.ToLower(ta.Method)] {
						continue
					}
					if declOrTraitHasMethod(classDecls, td2, ta.Method, map[string]bool{}) {
						prov = append(prov, td2.Name)
					}
				}
				if len(prov) >= 2 {
					base.curLine = cd.Line
					return nil, base.newFatal(fmt.Sprintf(
						"An alias was defined for method %s(), which exists in both %s and %s. Use %s::%s or %s::%s to resolve the ambiguity",
						ta.Method, prov[0], prov[1], prov[0], ta.Method, prov[1], ta.Method))
				}
			}
		}
		for _, ta := range cd.TraitAdapt {
			if len(ta.Insteadof) == 0 {
				continue
			}
			if ta.Trait == "" {
				continue
			}
			td := declByName0(classDecls, ta.Trait)
			if td == nil {
				return nil, base.newFatal(fmt.Sprintf("Could not find trait %s", ta.Trait))
			}
			if !td.IsTrait {
				return nil, base.newFatal(fmt.Sprintf("Class %s is not a trait, Only traits may be used in 'as' and 'insteadof' statements", ta.Trait))
			}
			if usePos(cd.Uses, ta.Trait) < 0 {
				return nil, base.newFatal(fmt.Sprintf("Required Trait %s wasn't added to %s", ta.Trait, cd.Name))
			}
			for _, ex := range ta.Insteadof {
				if usePos(cd.Uses, ex) < 0 {
					return nil, base.newFatal(fmt.Sprintf("Required Trait %s wasn't added to %s", ex, cd.Name))
				}
			}
			if !declOrTraitHasMethod(classDecls, td, ta.Method, map[string]bool{}) {
				return nil, base.newFatal(fmt.Sprintf("A precedence rule was defined for %s::%s but this method does not exist", ta.Trait, ta.Method))
			}
			for _, ex := range ta.Insteadof {
				if strings.EqualFold(ex, ta.Trait) {
					return nil, base.newFatal(fmt.Sprintf("Inconsistent insteadof definition. The method %s is to be used from %s, but %s is also on the exclude list", ta.Method, ta.Trait, ta.Trait))
				}
			}
		}
		// Метод одного трейта не может быть исключён более одного раза.
		{
			type exKey struct{ t, m string }
			orig := map[exKey]exKey{}
			count := map[exKey]int{}
			for _, ta := range cd.TraitAdapt {
				for _, ex := range ta.Insteadof {
					k := exKey{strings.ToLower(ex), strings.ToLower(ta.Method)}
					count[k]++
					if _, ok := orig[k]; !ok {
						orig[k] = exKey{ex, ta.Method}
					}
				}
			}
			for k, n := range count {
				if n > 1 {
					o := orig[k]
					return nil, base.newFatal(fmt.Sprintf("Failed to evaluate a trait precedence (%s). Method of trait %s was defined to be excluded multiple times", o.m, o.t))
				}
			}
		}
		// Конфликт методов двух трейтов без разрешения insteadof. Метод с одним
		// и тем же первоисточником (например, оба трейта используют общий
		// базовый трейт) конфликтом не считается.
		if !cd.IsInterface {
			provided := map[string]string{} // нижний регистр имени метода → трейт
			origin := map[string]string{}   // нижний регистр имени метода → первоисточник
			for _, tn := range cd.Uses {
				td := declByName0(classDecls, tn)
				if td == nil {
					continue
				}
				skipMethods := skip[strings.ToLower(tn)]
				for _, mn := range traitEffectiveMethodNames(classDecls, td, nil, map[string]bool{}) {
					if classDeclaresMethod(cd, mn) {
						continue
					}
					if skipMethods != nil && (skipMethods[mn] || skipMethods[strings.ToLower(mn)]) {
						continue
					}
					key := strings.ToLower(mn)
					org := ""
					if od := methodProvidedDecl(td, mn, declByName); od != nil {
						org = strings.ToLower(od.Name)
					}
					if prev, ok := provided[key]; ok && !strings.EqualFold(prev, tn) {
						if prevOrg, ok2 := origin[key]; ok2 && org != "" && prevOrg == org {
							provided[key] = tn
							origin[key] = org
							continue
						}
						base.curLine = cd.Line
						return nil, base.newFatal(fmt.Sprintf("Trait method %s::%s has not been applied as %s::%s, because of collision with %s::%s",
							tn, mn, cd.Name, mn, prev, mn))
					}
					provided[key] = tn
					origin[key] = org
				}
			}
		}
		// Конфликты свойств между двумя трейтами (typed properties).
		for ai := 0; ai < len(cd.Uses); ai++ {
			ta := declByName0(classDecls, cd.Uses[ai])
			if ta == nil {
				continue
			}
			for bi := ai + 1; bi < len(cd.Uses); bi++ {
				tb := declByName0(classDecls, cd.Uses[bi])
				if tb == nil {
					continue
				}
				for _, pm := range ta.Members {
					if pm.Kind != "prop" {
						continue
					}
					for _, pn := range tb.Members {
						if pn.Kind != "prop" || pn.PropName != pm.PropName {
							continue
						}
						if propDefsEqual(pm, pn) {
							continue
						}
						base.curLine = cd.Line
						msg := fmt.Sprintf("%s and %s define the same property ($%s) in the composition of %s. However, the definition differs and is considered incompatible. Class was composed",
							ta.Name, tb.Name, pm.PropName, cd.Name)
						if cd.IsTrait || cd.IsInterface || cd.IsEnum {
							return nil, base.newFatal(msg)
						}
						if cd.RuntimeFatal == "" {
							cd.RuntimeFatal = msg
						}
						continue classLoop
					}
				}
			}
		}
		// Конфликт констант между двумя трейтами одного класса/трейта.
		for ai := 0; ai < len(cd.Uses); ai++ {
			ta := declByName0(classDecls, cd.Uses[ai])
			if ta == nil {
				continue
			}
			for bi := ai + 1; bi < len(cd.Uses); bi++ {
				tb := declByName0(classDecls, cd.Uses[bi])
				if tb == nil {
					continue
				}
				for _, pm := range ta.Members {
					if pm.Kind != "const" {
						continue
					}
					for _, pn := range tb.Members {
						if pn.Kind != "const" || !strings.EqualFold(pn.ConstName, pm.ConstName) {
							continue
						}
						if constDefsEqual(pm, pn) {
							continue
						}
						base.curLine = cd.Line
						msg := fmt.Sprintf("%s and %s define the same constant (%s) in the composition of %s. However, the definition differs and is considered incompatible. Class was composed",
							ta.Name, tb.Name, pm.ConstName, cd.Name)
						if cd.IsTrait || cd.IsInterface || cd.IsEnum {
							return nil, base.newFatal(msg)
						}
						if cd.RuntimeFatal == "" {
							cd.RuntimeFatal = msg
						}
						continue classLoop
					}
				}
			}
		}
		for _, tn := range cd.Uses {
			td := declByName0(classDecls, tn)
			if td != nil && !td.IsTrait {
				return nil, base.newFatal(fmt.Sprintf("%s cannot use %s - it is not a trait", cd.Name, tn))
			}
			ti, ok := classIdx[tn]
			if !ok {
				if td != nil {
					if di, ok2 := base.declIndexAll[td]; ok2 {
						ti, ok = di, true
					}
				}
			}
			if !ok {
				return nil, base.newFatal(fmt.Sprintf("Trait %q not found", tn))
			}
			// Конфликты свойств и констант между классом/трейтом и трейтом.
			if td != nil {
				for _, pm := range cd.Members {
					if pm.Kind != "prop" {
						continue
					}
					for _, pn := range td.Members {
						if pn.Kind != "prop" || !strings.EqualFold(pn.PropName, pm.PropName) {
							continue
						}
						if propDefsEqual(pm, pn) {
							continue
						}
						base.curLine = cd.Line
						msg := fmt.Sprintf("%s and %s define the same property ($%s) in the composition of %s. However, the definition differs and is considered incompatible. Class was composed",
							cd.Name, td.Name, pm.PropName, cd.Name)
						if cd.IsTrait || cd.IsInterface || cd.IsEnum {
							return nil, base.newFatal(msg)
						}
						if cd.RuntimeFatal == "" {
							cd.RuntimeFatal = msg
						}
						continue classLoop
					}
				}
			}
			if cd.IsEnum {
				for _, tc := range classDecls {
					if strings.EqualFold(tc.Name, tn) {
						for _, mm := range tc.Members {
							if mm.Kind == "prop" {
								return nil, base.newFatal("Enum " + cd.Name + " cannot include properties")
							}
							if mm.Kind == "method" && enumForbiddenMagic[mm.MName] {
								return nil, base.newFatal("Enum " + cd.Name + " cannot include magic method " + mm.MName)
							}
						}
					}
				}
			}
			if cd.IsReadonly {
				for _, tc := range classDecls {
					if strings.EqualFold(tc.Name, tn) {
						for _, mm := range tc.Members {
							if mm.Kind == "prop" && !mm.Readonly {
								return nil, base.newFatal(fmt.Sprintf("Readonly class %s cannot use trait with a non-readonly property %s::$%s", cd.Name, tc.Name, mm.PropName))
							}
						}
					}
				}
			}
			// Конфликт констант трейта и класса (typed class constants).
			for _, tc := range classDecls {
				if !strings.EqualFold(tc.Name, tn) {
					continue
				}
				for _, tm := range tc.Members {
					if tm.Kind != "const" {
						continue
					}
					if tm.Final {
						if constFinal[cd] == nil {
							constFinal[cd] = map[string]bool{}
						}
						constFinal[cd][strings.ToLower(tm.ConstName)] = true
					}
					for _, cm := range cd.Members {
						if cm.Kind != "const" || cm.ConstName != tm.ConstName {
							continue
						}
						if constDefsEqual(cm, tm) {
							continue
						}
						base.curLine = cd.Line
						msg := fmt.Sprintf("%s and %s define the same constant (%s) in the composition of %s. However, the definition differs and is considered incompatible. Class was composed",
							cd.Name, tc.Name, tm.ConstName, cd.Name)
						if cd.IsTrait || cd.IsInterface || cd.IsEnum {
							return nil, base.newFatal(msg)
						}
						if cd.RuntimeFatal == "" {
							cd.RuntimeFatal = msg
						}
						continue classLoop
					}
					// Конфликт с унаследованной константой родителя.
					if pd, pm := findInheritedPropConst(cd, tm.ConstName, declByName); pm != nil && pm.Kind == "const" && !strings.EqualFold(pm.Vis, "private") {
						if constDefsEqual(pm, tm) {
							continue
						}
						base.curLine = cd.Line
						msg := fmt.Sprintf("%s and %s define the same constant (%s) in the composition of %s. However, the definition differs and is considered incompatible. Class was composed",
							pd.Name, tc.Name, tm.ConstName, cd.Name)
						if cd.IsTrait || cd.IsInterface || cd.IsEnum {
							return nil, base.newFatal(msg)
						}
						if cd.RuntimeFatal == "" {
							cd.RuntimeFatal = msg
						}
						continue classLoop
					}
				}
			}
			ci.UseExcept(classes[ti], skip[strings.ToLower(tn)])
		}
		// Адаптации `as`: псевдонимы и смена видимости.
		for _, ta := range cd.TraitAdapt {
			if !ta.Alias {
				continue
			}
			if ta.AliasFinal {
				if aliasFinal[cd] == nil {
					aliasFinal[cd] = map[string]bool{}
				}
				aliasFinal[cd][strings.ToLower(ta.Method)] = true
				ci.SetMethodFinal(ta.Method)
				if ta.AliasName != "" {
					ci.SetMethodFinal(ta.AliasName)
				}
				if v, ok := ci.MethodVisibility(ta.Method); ok && v == vm.Private {
					compilerWarnings = append(compilerWarnings,
						fmt.Sprintf("Private methods cannot be final as they are never overridden by other classes in %s on line %d", file, cd.Line))
				}
			}
			if ta.AliasName != "" {
				// Источник псевдонима — метод конкретного трейта (если он
				// указан или его можно определить среди use этой секции), а не
				// уже скомпонованный метод класса: собственный метод класса
				// имеет приоритет над трейтом, но псевдоним всё равно должен
				// ссылаться на реализацию трейта.
				srcName := ta.Trait
				if srcName == "" {
					for _, tn := range cd.Uses {
						if td := declByName0(classDecls, tn); td != nil && declHasMethod(td, ta.Method) {
							srcName = tn
							break
						}
					}
				}
				var fn *vm.Function
				vis := visOf(ta.Vis)
				haveVis := ta.Vis != ""
				if srcName != "" {
					for j, td := range classDecls {
						if !strings.EqualFold(td.Name, srcName) {
							continue
						}
						tci := classes[classBase+j]
						if f, ok := tci.MethodFunction(ta.Method); ok {
							fn = f
							if !haveVis {
								if v, ok := tci.MethodVisibility(ta.Method); ok {
									vis, haveVis = v, true
								}
							}
						}
						break
					}
				}
				if fn == nil {
					if f, ok := ci.MethodFunction(ta.Method); ok {
						fn = f
						if !haveVis {
							if v, ok := ci.MethodVisibility(ta.Method); ok {
								vis = v
							}
						}
					}
				}
				// Псевдоним не перекрывает метод, объявленный в самом классе.
				if fn != nil && !classDeclaresMethod(cd, ta.AliasName) {
					// Коллизия псевдонима с методом другого трейта.
					if srcName != "" {
						if other := traitProvidingMethod(classDecls, cd, ta.AliasName, srcName); other != "" {
							posSrc, posOther := usePos(cd.Uses, srcName), usePos(cd.Uses, other)
							base.curLine = cd.Line
							if posSrc > posOther {
								return nil, base.newFatal(fmt.Sprintf("Trait method %s::%s has not been applied as %s::%s, because of collision with %s::%s",
									srcName, ta.Method, cd.Name, ta.AliasName, other, ta.AliasName))
							}
							return nil, base.newFatal(fmt.Sprintf("Trait method %s::%s has not been applied as %s::%s, because of collision with %s::%s",
								other, ta.AliasName, cd.Name, ta.AliasName, srcName, ta.AliasName))
						}
					}
					ci.AliasMethod(ta.AliasName, fn, vis)
				}
			} else if ta.Vis != "" {
				if !classDeclaresMethod(cd, ta.Method) {
					ci.SetMethodVis(ta.Method, visOf(ta.Vis))
				}
			}
		}
	}

	// 2b-quater. Совместимость методов, предоставленных трейтами: с
	// унаследованными методами родителя, методами интерфейсов и абстрактными
	// сигнатурами других трейтов, а также собственных методов класса с
	// абстрактными сигнатурами используемых трейтов.
	//
	// Позиции объявлений и база классов нужны уже здесь: недостающие в
	// сигнатурах классы откладываются до объявления класса (variance
	// obligations, bug79179).
	base.deferClassBase = classBase
	declPos := make(map[*ClassDecl]int, len(classDecls))
	for i, dc := range classDecls {
		declPos[dc] = i
	}
compatLoop:
	for classPos, cd := range classDecls {
		if cd.IsInterface {
			continue
		}
		if nestedSet[cd] {
			continue
		}
		base.availLimit = classBase + classPos
		type provM struct {
			name  string
			child *ClassDecl
		}
		var provs []provM
		addProv := func(name string, child *ClassDecl) {
			for i := range provs {
				if strings.EqualFold(provs[i].name, name) {
					return
				}
			}
			provs = append(provs, provM{name, child})
		}
		for _, m := range cd.Members {
			if m.Kind == "method" {
				addProv(m.MName, cd)
			}
		}
		for _, tn := range cd.Uses {
			td := declByName0(classDecls, tn)
			if td == nil {
				continue
			}
			for _, mn := range traitEffectiveMethodNames(classDecls, td, nil, map[string]bool{}) {
				child := methodProvidedDecl(td, mn, declByName)
				if child == nil {
					child = td
				}
				addProv(mn, child)
			}
		}
		for _, pr := range provs {
			base.errFile = ""
			childDecl := methodProvidedDecl(cd, pr.name, declByName)
			if childDecl == nil {
				childDecl = pr.child
			}
			childMember := findOwnMember(childDecl, pr.name, "method", "signature")
			if childMember == nil || childMember.Kind != "method" {
				continue
			}
			if childDecl == cd {
				// Собственный метод класса: контракты интерфейсов и родителей
				// уже проверены в 2b-bis. Здесь — абстрактные методы трейтов.
				for _, sg := range traitSignaturesFor(cd, pr.name, declByName, nil) {
					base.curLine = childMember.Line
					sgm := *sg.mem
					sgm.Vis = childMember.Vis
					resolveSelfTypes(&sgm, cd.Name, cd.Parent)
					if err := base.checkOverrideCompatible(cd, childMember, sg.decl, &sgm); err != nil {
						return nil, err
					}
				}
				continue
			}
			if childDecl.DefFile != "" {
				// Метод примешан трейтом: Zend привязывает диагностику к
				// файлу трейта (bug81192).
				base.errFile = childDecl.DefFile
			}
			if pd, pm := findParentClassMethod(cd, pr.name, declByName); pm != nil {
				base.curLine = childMember.Line
				if err := base.checkOverrideCompatible(childDecl, childMember, pd, pm); err != nil {
					if deferClassFatal(cd, err) {
						continue compatLoop
					}
					return nil, err
				}
			}
			if id, im := findIfaceMethod(cd, pr.name, declByName); im != nil {
				base.curLine = childMember.Line
				if err := base.checkOverrideCompatible(cd, childMember, id, im); err != nil {
					return nil, err
				}
			}
			for _, sg := range traitSignaturesFor(cd, pr.name, declByName, childDecl) {
				base.curLine = childMember.Line
				// Видимость абстрактного метода трейта не форсируется
				// (обратная совместимость): сравниваем без понижения видимости.
				sgm := *sg.mem
				sgm.Vis = childMember.Vis
				resolveSelfTypes(&sgm, cd.Name, cd.Parent)
				if err := base.checkOverrideCompatible(childDecl, childMember, sg.decl, &sgm); err != nil {
					if deferClassFatal(cd, err) {
						continue compatLoop
					}
					return nil, err
				}
			}
		}
		// Абстрактные сигнатуры трейтов против унаследованных методов
		// родителя: Zend проверяет родительский метод как «объявление», а
		// сигнатуру трейта — как прототип (gh14009).
		for _, sg := range traitSignaturesAll(cd, declByName) {
			name := sg.mem.MName
			if prov := methodProvidedDecl(cd, name, declByName); prov != nil {
				if m := findOwnMember(prov, name, "method"); m != nil {
					continue // конкретная реализация уже проверена выше
				}
			}
			pd, pm := findParentClassMethod(cd, name, declByName)
			if pm == nil {
				continue
			}
			base.curLine = pm.Line
			if pd.DefFile != "" {
				base.errFile = pd.DefFile
			}
			sgm := *sg.mem
			resolveSelfTypes(&sgm, cd.Name, cd.Parent)
			status, unavail, cerr := base.overrideCompat(pd, pm, sg.decl, &sgm)
			base.errFile = ""
			if cerr != nil {
				if deferClassFatal(cd, cerr) {
					continue compatLoop
				}
				return nil, cerr
			}
			switch status {
			case subYes:
			case subUnknown:
				if d := declByName[strings.ToLower(strings.TrimLeft(unavail, "\\"))]; d != nil && !nestedSet[d] && declPos[d] >= classPos {
					// Класс объявлен в этом же файле позже: при связывании
					// Zend его ещё не видит — прежняя диагностика.
					msg := "Could not check compatibility between " + methodSigStringSub(pd.Name, pm, pd.Name, pd.Parent) +
						" and " + methodSigStringSub(sg.decl.Name, &sgm, sg.decl.Name, sg.decl.Parent) + ", because class " + unavail + " is not available"
					return nil, base.newFatal(msg)
				}
				// Проверка не завершилась: Zend откладывает её до выполнения
				// объявления класса (variance obligations) — там недостающий
				// класс можно автозагрузить и повторить проверку (bug79179).
				base.deferVarianceCheck(cd, classPos, rtVarCheck{cd: pd, m: pm, pd: sg.decl, pm: &sgm})
			default:
				msg := "Declaration of " + methodSigStringSub(pd.Name, pm, pd.Name, pd.Parent) +
					" must be compatible with " + methodSigStringSub(sg.decl.Name, &sgm, sg.decl.Name, sg.decl.Parent)
				return nil, base.newFatal(msg)
			}
		}
	}
	base.availLimit = -1

	// 2b-quater. Неоднозначное наследование констант: если класс/интерфейс
	// получает одну и ту же константу из двух разных источников и не
	// переопределяет её — Zend считает это фатальной ошибкой
	// (final_const8/11/12).
	for _, cd := range classDecls {
		if cd.IsTrait {
			continue
		}
		own := map[string]bool{}
		var names []string
		seenName := map[string]bool{}
		addNames := func(d *ClassDecl) {
			if d == nil {
				return
			}
			for _, m := range d.Members {
				if m.Kind == "const" {
					k := strings.ToLower(m.ConstName)
					if !seenName[k] {
						seenName[k] = true
						names = append(names, k)
					}
				}
			}
		}
		for _, m := range cd.Members {
			if m.Kind == "const" {
				own[strings.ToLower(m.ConstName)] = true
			}
		}
		for p := declByName[strings.ToLower(strings.TrimLeft(cd.Parent, "\\"))]; p != nil; p = declByName[strings.ToLower(strings.TrimLeft(p.Parent, "\\"))] {
			addNames(p)
		}
		for _, in := range collectAllInterfaces(cd, declByName, map[string]bool{}) {
			addNames(in)
		}
		for _, name := range names {
			if own[name] {
				continue
			}
			srcs := inheritedConstDeclarers(cd, name, declByName)
			if len(srcs) > 1 {
				kind := "Class"
				if cd.IsInterface {
					kind = "Interface"
				}
				declName := func(d *ClassDecl) string {
					for _, m := range d.Members {
						if m.Kind == "const" && strings.EqualFold(m.ConstName, name) {
							return m.ConstName
						}
					}
					return name
				}
				base.curLine = cd.Line
				return nil, base.newFatal(fmt.Sprintf("%s %s inherits both %s::%s and %s::%s, which is ambiguous",
					kind, cd.Name, srcs[0].Name, declName(srcs[0]), srcs[1].Name, declName(srcs[1])))
			}
		}
	}

	// 2b-ter. Отложенные проверки совместимости для классов, объявляемых в
	// рантайме (OpDeclareClass): Zend проверяет сигнатуры в момент выполнения
	// объявления, когда предки разрешены (в т.ч. автозагрузкой), а недостающие
	// классы можно подгрузить и повторить проверку (variance obligations).
	for classPos, cd := range classDecls {
		if !nestedSet[cd] || cd.IsTrait {
			continue
		}
		if cd.IsEnum {
			// У enum проверки контрактов обрабатываются отдельными ветками
			// компилятора; не рискуем их дублировать.
			continue
		}
		plan := &rtVarPlan{file: cd.DefFile}
		if plan.file == "" {
			plan.file = file
		}
		addCheck := func(m *ClassMember) {
			pd, pm := findInheritedMethod(cd, m.MName, declByName, map[string]bool{})
			if isCtorOrDtorName(m.MName) && pm != nil && !pm.Abstract && !pd.IsInterface && !pm.Final {
				if ad, am := findAbstractAncestorMethod(cd, m.MName, declByName); am != nil {
					pd, pm = ad, am
				} else {
					pm = nil
				}
			}
			if pm == nil {
				return
			}
			plan.checks = append(plan.checks, rtVarCheck{cd: cd, m: m, pd: pd, pm: pm})
		}
		for _, m := range cd.Members {
			if m.Kind != "method" && m.Kind != "signature" {
				continue
			}
			addCheck(m)
		}
		// Конструктор проверяется против всех реализуемых интерфейсов.
		if !cd.IsInterface {
			if ctorm := findOwnMember(cd, "__construct", "method", "signature"); ctorm != nil {
				for _, i := range collectAllInterfaces(cd, declByName, map[string]bool{}) {
					sig := findOwnMember(i, "__construct", "signature", "method")
					if sig == nil {
						continue
					}
					plan.checks = append(plan.checks, rtVarCheck{cd: cd, m: ctorm, pd: i, pm: sig})
				}
			}
		}
		if len(plan.checks) == 0 {
			continue
		}
		if sc, ok := classes[classBase+classPos].(interface{ SetCheckData(any) }); ok {
			sc.SetCheckData(plan)
		}
	}

	// 2b-bis. Проверка совместимости сигнатур переопределённых методов
	// (ковариантность возврата, контравариантность параметров).
	for classPos, cd := range classDecls {
		if nestedSet[cd] {
			// Объявление выполняется в рантайме: совместимость сигнатур
			// проверяется (или откладывается) в момент выполнения, как Zend.
			continue
		}
		base.availLimit = classBase + classPos
		for _, m := range cd.Members {
			if m.Kind != "method" && m.Kind != "signature" {
				continue
			}
			// Метод предка, ставший final через `use T { m as final; }`.
			for cur := cd; cur != nil; {
				if af := aliasFinal[cur]; af != nil && af[strings.ToLower(m.MName)] {
					return nil, base.newFatal(fmt.Sprintf("Cannot override final method %s::%s()", cur.Name, m.MName))
				}
				if cur.Parent == "" {
					break
				}
				cur = declByName[strings.ToLower(cur.Parent)]
			}
			pd, pm := findInheritedMethod(cd, m.MName, declByName, map[string]bool{})
			// Конструкторы/деструкторы проверяются только против интерфейсов и
			// абстрактных объявлений предков: конкретный конструктор родителя
			// не образует контракт, и Zend сверяет потомка с ближайшим
			// абстрактным прототипом (а не с конкретной переопределяющей копией).
			if isCtorOrDtorName(m.MName) && pm != nil && !pm.Abstract && !pd.IsInterface && !pm.Final {
				if ad, am := findAbstractAncestorMethod(cd, m.MName, declByName); am != nil {
					pd, pm = ad, am
				} else {
					pm = nil
				}
			}
			if pm == nil {
				// Родитель может быть встроенным классом: проверяем против
				// сигнатуры встроенного метода (в т.ч. tentative-возврат).
				if m.Kind == "method" || cd.IsInterface {
					if np := base.nativeAncestor(cd, declByName, map[string]bool{}); np != nil {
						key := strings.ToLower(np.Name()) + "::" + strings.ToLower(m.MName)
						if sig, ok := nativeVarianceSigs[key]; ok {
							base.curLine = m.Line
							if err := base.checkNativeOverrideCompat(cd, m, np, sig); err != nil {
								return nil, err
							}
						}
					}
					for _, ni := range base.nativeIfaces(cd, declByName) {
						key := strings.ToLower(ni.Name()) + "::" + strings.ToLower(m.MName)
						sig, ok := nativeVarianceSigs[key]
						if !ok {
							continue
						}
						base.curLine = m.Line
						if err := base.checkNativeOverrideCompat(cd, m, ni, sig); err != nil {
							return nil, err
						}
						break
					}
				}
				// Предок объявлен в другом (рантайм-подключённом) юните —
				// сверяемся с его сигнатурой из таблицы VM (gh9407).
				if pd, pm := base.runtimeInheritedMethod(cd, m.MName, declByName); pm != nil {
					base.curLine = m.Line
					status, unavail, cerr := base.overrideCompat(cd, m, pd, pm)
					if cerr != nil {
						return nil, cerr
					}
					switch status {
					case subYes:
					case subUnknown:
						if unavail != "" {
							msg := "Could not check compatibility between " + methodSigStringSub(cd.Name, m, cd.Name, cd.Parent) +
								" and " + methodSigStringSub(pd.Name, pm, pd.Name, pd.Parent) + ", because class " + unavail + " is not available"
							return nil, base.newFatal(msg)
						}
					default:
						msg := "Declaration of " + methodSigStringSub(cd.Name, m, cd.Name, cd.Parent) +
							" must be compatible with " + methodSigStringSub(pd.Name, pm, pd.Name, pd.Parent)
						return nil, base.newFatal(msg)
					}
				}
				continue
			}
			base.curLine = m.Line
			status, unavail, cerr := base.overrideCompat(cd, m, pd, pm)
			if cerr != nil {
				return nil, cerr
			}
			switch status {
			case subYes:
			case subUnknown:
				if d := declByName[strings.ToLower(strings.TrimLeft(unavail, "\\"))]; d != nil && !nestedSet[d] && declPos[d] >= classPos {
					// Класс объявлен в этом же файле позже: Zend при
					// связывании его ещё не видит (позднее объявление не
					// спасает) — прежняя диагностика компиляции. Классы вне
					// верхнего уровня (nested) на этапе связывания ещё не
					// объявлены и могут быть автозагружены.
					msg := "Could not check compatibility between " + methodSigStringSub(cd.Name, m, cd.Name, cd.Parent) +
						" and " + methodSigStringSub(pd.Name, pm, pd.Name, pd.Parent) + ", because class " + unavail + " is not available"
					return nil, base.newFatal(msg)
				}
				// Zend не может связать класс на этапе компиляции
				// (early binding) и откладывает проверку до выполнения
				// объявления — там недостающие классы можно автозагрузить.
				base.deferTopVariance(cd, classPos, m, pd, pm)
			default:
				msg := "Declaration of " + methodSigStringSub(cd.Name, m, cd.Name, cd.Parent) + " must be compatible with " + methodSigStringSub(pd.Name, pm, pd.Name, pd.Parent)
				return nil, base.newFatal(msg)
			}
		}
		// Конструктор проверяется на совместимость со всеми реализуемыми
		// интерфейсами (в т.ч. унаследованными), а не только с ближайшим.
		if !cd.IsInterface && !cd.IsTrait {
			if ctorm := findOwnMember(cd, "__construct", "method", "signature"); ctorm != nil {
				for _, i := range collectAllInterfaces(cd, declByName, map[string]bool{}) {
					sig := findOwnMember(i, "__construct", "signature", "method")
					if sig == nil {
						continue
					}
					base.curLine = ctorm.Line
					if err := base.checkOverrideCompatible(cd, ctorm, i, sig); err != nil {
						return nil, err
					}
				}
			}
		}
		// Инвариантность типов свойств и типизированных констант.
		for _, m := range cd.Members {
			switch m.Kind {
			case "prop":
				if pd, pm := findInheritedPropConst(cd, m.PropName, declByName); pm != nil && pm.Kind == "prop" {
					base.curLine = cd.Line
					if err := base.checkPropCompatible(cd, m, pd, pm); err != nil {
						return nil, err
					}
				}
			case "const":
				if m.Final && strings.EqualFold(m.Vis, "private") {
					// Zend: приватная константа не может быть final — она не
					// видна другим классам (final_const3).
					base.curLine = m.Line
					return nil, base.newFatal(fmt.Sprintf("Private constant %s::%s cannot be final as it is not visible to other classes", cd.Name, m.ConstName))
				}
				// Переопределение final-константы предка.
				for cur := declByName[strings.ToLower(cd.Parent)]; cur != nil; {
					if cf := constFinal[cur]; cf != nil && cf[strings.ToLower(m.ConstName)] {
						return nil, base.newFatal(fmt.Sprintf("%s::%s cannot override final constant %s::%s", cd.Name, m.ConstName, cur.Name, m.ConstName))
					}
					if cur.Parent == "" {
						break
					}
					cur = declByName[strings.ToLower(cur.Parent)]
				}
				// Переопределение final-константы интерфейса (final_const5).
				for _, in := range collectAllInterfaces(cd, declByName, map[string]bool{}) {
					if cf := constFinal[in]; cf != nil && cf[strings.ToLower(m.ConstName)] {
						return nil, base.newFatal(fmt.Sprintf("%s::%s cannot override final constant %s::%s", cd.Name, m.ConstName, in.Name, m.ConstName))
					}
				}
				if pd, pm := findInheritedPropConst(cd, m.ConstName, declByName); pm != nil && pm.Kind == "const" && !strings.EqualFold(pm.Vis, "private") {
					base.curLine = cd.Line
					pv := strings.ToLower(pm.Vis)
					if pv == "" {
						pv = "public"
					}
					mv := strings.ToLower(m.Vis)
					if mv == "" {
						mv = "public"
					}
					if visRank(mv) < visRank(pv) {
						if pv == "protected" {
							return nil, base.newFatal(fmt.Sprintf("Access level to %s::%s must be protected (as in class %s) or weaker", cd.Name, m.ConstName, pd.Name))
						}
						return nil, base.newFatal(fmt.Sprintf("Access level to %s::%s must be %s (as in class %s)", cd.Name, m.ConstName, pv, pd.Name))
					}
					if pm.ConstType != "" {
						resolve := func(t string) string {
							switch strings.ToLower(t) {
							case "self", "static":
								return cd.Name
							case "parent":
								return cd.Parent
							}
							return t
						}
						if m.ConstType == "" || !base.typeSubtype(m.ConstType, pm.ConstType, resolve) {
							return nil, base.newFatal(fmt.Sprintf("Type of %s::%s must be compatible with %s::%s of type %s",
								cd.Name, m.ConstName, pd.Name, pm.ConstName, displayType(pm.ConstType)))
						}
					}
				}
			}
		}
	}
	base.availLimit = -1

	// 2b-ter. Встроенные методы enum: cases()/from()/tryFrom().
	for i, cd := range classDecls {
		if cd.IsEnum {
			for _, mm := range cd.Members {
				if mm.Kind == "method" && (mm.MName == "cases" || mm.MName == "from" || mm.MName == "tryFrom") {
					return nil, base.newFatal("Cannot redeclare " + cd.Name + "::" + mm.MName + "()")
				}
			}
			classes[classBase+i].SetupEnum()
		}
	}

	// 2c. Проверка реализации сигнатур: конкретный (не abstract) класс обязан
	// реализовать все унаследованные сигнатуры (методы интерфейсов и
	// abstract-методы родителя).
	for i, cd := range classDecls {
		if cd.IsInterface || cd.IsTrait {
			continue
		}
		if !cd.IsAbstract && !implementsIteratorAPI(cd, declByName) && declaresTraversable(cd, declByName) {
			base.curLine = cd.Line
			cd.RuntimeFatal = fmt.Sprintf("Class %s must implement interface Traversable as part of either Iterator or IteratorAggregate", cd.Name)
			continue
		}
		ci := classes[classBase+i]
		if cd.IsAbstract {
			// private abstract-метод обязан реализовать сам класс, даже
			// абстрактный (наследник не может его перекрыть).
			if missing := ci.UnimplementedPrivateAbstract(); len(missing) > 0 {
				plural := "methods"
				if len(missing) == 1 {
					plural = "method"
				}
				base.curLine = cd.Line
				return nil, base.newFatal(fmt.Sprintf("Class %s must implement %d abstract private %s (%s)",
					cd.Name, len(missing), plural, strings.Join(missing, ", ")))
			}
			continue
		}
		if missing := ci.UnimplementedAbstract(); len(missing) > 0 {
			plural := "methods"
			if len(missing) == 1 {
				plural = "method"
			}
			cd.RuntimeFatal = fmt.Sprintf("Class %s contains %d abstract %s and must therefore be declared abstract or implement the remaining methods (%s)",
				cd.Name, len(missing), plural, strings.Join(missing, ", "))
			continue
		}
	}

	// 2d. #[\Override]: метод обязан переопределять метод предка/интерфейса.
	// Zend проверяет это при линковке класса; у нас линковка отложена до
	// выполнения объявления, поэтому откладываем фатал туда же.
	for i, cd := range classDecls {
		if cd.IsTrait || cd.RuntimeFatal != "" {
			continue
		}
		if msg := vm.OverrideViolation(classes[classBase+i]); msg != "" {
			base.curLine = cd.Line
			cd.RuntimeFatal = msg
		}
	}

	// 3. Тела функций.
	base.curClass = nil
	base.noParentScope = false
	for _, fd := range decls {
		if ln, ok := base.stmtLines[fd]; ok && ln > 0 {
			base.curLine = ln
		} else if fd.DefLine > 0 {
			base.curLine = fd.DefLine
		}
		if err := base.validateType(fd.RetType); err != nil {
			return nil, err
		}
		if err := checkInternalAttributeTarget(fd.Attrs, 2, base.curLine); err != nil {
			return nil, err
		}
		for _, p := range fd.Params {
			if len(p.Attrs) > 0 {
				if err := checkInternalAttributeTarget(p.Attrs, 32, base.curLine); err != nil {
					return nil, err
				}
			}
		}
		if typeHasAtom(fd.RetType, "static") {
			return nil, base.newFatal(`Cannot use "static" when no class scope is active`)
		}
		if base.curClass == nil {
			if typeHasAtom(fd.RetType, "self") {
				return nil, base.newFatal(`Cannot use "self" when no class scope is active`)
			}
			if typeHasAtom(fd.RetType, "parent") {
				return nil, base.newFatal(`Cannot use "parent" when no class scope is active`)
			}
		}
		savedStrict := base.strict
		base.strict = fd.Strict
		savedTicks := base.ticks
		base.ticks = fd.Ticks
		cf, err := base.compileFuncBody(fd.Name, fd.Params, fd.Body, fd.ByRefReturn, fd.RetType, base.curLine)
		base.strict = savedStrict
		base.ticks = savedTicks
		if err != nil {
			return nil, err
		}
		funcs[idx[fd.Name]].Executable = cf
		defFile := fd.DefFile
		if defFile == "" {
			defFile = file
		}
		funcs[idx[fd.Name]].DefFile = defFile
		if ln, ok := base.stmtLines[fd]; ok && ln > 0 {
			funcs[idx[fd.Name]].DefLine = ln
		} else if fd.DefLine > 0 {
			funcs[idx[fd.Name]].DefLine = fd.DefLine
		}
	}

	// 4. Главная программа.
	mc := newCompiler(funcs, idx)
	mc.haltOffset = mainHaltOffset
	mc.isMain = true
	mc.file = file
	mc.strict = prog.StrictTypes
	mc.chunk.Strict = prog.StrictTypes
	mc.chunk.SetEmitStrict(prog.StrictTypes)
	mc.classes = classes
	mc.classIdx = classIdx
	mc.declIdx = base.declIdx
	mc.methodRefs = base.methodRefs
	mc.closureIdx = closureMap
	mc.anonNames = anonMap
	mc.classAlias = compileAliases
	mc.topVariance = base.topVariance
	mc.stmtLines = prog.Lines
	mc.exprLines = prog.ExprLines
	mc.chunk.File = file
	if linkBase != nil {
		// Подключаемый файл исполняется в той же VM, что и вызывающий код:
		// его top-level переменные должны попасть в общую таблицу globals,
		// поэтому промоушен в регистры кадра отключаем.
		mc.promote = map[string]bool{}
	} else {
		mc.promote = analyzeMainPromotion(prog.Stmts)
	}
	// static-переменные главной программы живут в таблице глобалов.
	for _, sv := range collectStatics(prog.Stmts) {
		mc.staticKeys[sv.Name] = "\x00main\x00" + sv.Name
		delete(mc.promote, sv.Name)
	}
	mc.prescan(prog.Stmts)
	for _, st := range prog.Stmts {
		switch s := st.(type) {
		case *FuncDecl:
			continue
		case *ClassDecl:
			// Конфликт с псевдонимом class_alias() — предупреждение в рантайме
			// (само объявление уже слинковано хойстом).
			if s.RuntimeFatal == "" {
				mc.curLine = s.Line
				mc.chunk.SetLine(s.Line)
				nm := mc.alloc()
				mc.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nm, C: int32(mc.chunk.AddConst(vm.StrConst(s.Name)))})
				mc.emitCall("__php_class_alias_conflict", []uint8{nm})
			}
			if s.MissingParent != "" {
				mc.curLine = s.Line
				mc.emitClassGuard(s.MissingParent, false)
			}
			if s.MissingInterface != "" {
				mc.curLine = s.Line
				mc.emitClassGuard(s.MissingInterface, true)
			}
			if s.IfaceNotInterface != "" {
				// «Интерфейс», оказавшийся классом: Zend фаталит при
				// связывании (после автозагрузки родителя).
				mc.curLine = s.Line
				mc.chunk.SetLine(s.Line)
				msg := fmt.Sprintf("%s cannot implement %s - it is not an interface", s.Name, s.IfaceNotInterface)
				mc.chunk.Emit(vm.Instruction{Op: vm.OpFatal, C: int32(mc.chunk.AddConst(vm.StrConst(msg)))})
			}
			if plan := mc.topVariance[s]; plan != nil {
				// Проверка сигнатур не завершилась на этапе компиляции:
				// выполняем её при объявлении (недостающие классы могут быть
				// автозагружены) — Zend variance obligations.
				mc.curLine = s.Line
				mc.chunk.SetLine(s.Line)
				idxReg := mc.alloc()
				mc.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: idxReg,
					C: int32(mc.chunk.AddConst(vm.IntConst(int64(plan.rtIdx))))})
				mc.emitCall("__php_variance_check", []uint8{idxReg})
			}
			if s.RuntimeFatal != "" {
				mc.curLine = s.Line
				mc.chunk.SetLine(s.Line)
				mc.chunk.Emit(vm.Instruction{Op: vm.OpFatal, C: int32(mc.chunk.AddConst(vm.StrConst(s.RuntimeFatal)))})
			}
			continue
		}
		if err := mc.stmt(st); err != nil {
			return nil, err
		}
		mc.emitStmtSweep()
		mc.emitStmtTick()
	}
	if err := checkFinallyJumps(prog.Stmts); err != nil {
		return nil, err
	}
	if err := mc.finalizeGotos(); err != nil {
		return nil, err
	}
	mc.chunk.Emit(vm.Instruction{Op: vm.OpHalt})
	mc.chunk.Optimize()
	// Карта имя→регистр главной программы (для подсчёта владельцев ссылок в
	// var_dump): строим и пинним именованные регистры, чтобы карта осталась
	// валидной после SSA-распределения.
	mc.chunk.SetNamedRegs(mc.namedRegsMap())
	mc.chunk.SetKeepParamsLive(true)
	mc.setVarRegs()
	mc.chunk.AllocateRegisters(int(mc.nvars), mc.allocEnv())
	if len(compilerDeprecations) > 0 {
		mc.chunk.SetStartup(compilerDeprecations)
	}
	if len(compilerWarnings) > 0 {
		mc.chunk.SetStartupWarnings(compilerWarnings)
	}
	if len(iniStartupWarnings) > 0 {
		// Диагностики --INI---настроек печатаются как стартовые (Zend:
		// "PHP Startup: ... in Unknown on line 0") — перед выводом скрипта.
		mc.chunk.SetStartupWarnings(append(append([]string{}, compilerWarnings...), iniStartupWarnings...))
	}
	// Связываем байткод с обёрткой *Function (нужно для stack trace генераторов
	// и других диагностик, где требуется имя/scope).
	for _, fn := range funcs {
		if cf, ok := fn.Executable.(*vm.CompiledFunction); ok && cf.Owner() == nil {
			cf.SetOwner(fn)
		}
	}
	return &Compiled{Main: mc.chunk, Funcs: funcs, Classes: classes, IncompleteIdx: incompleteIdx}, nil
}

// allocEnv builds the SSA allocator's callee-metadata environment.
func (c *compiler) allocEnv() *vm.RegAllocEnv {
	funcs := c.funcs
	return &vm.RegAllocEnv{
		FuncNumArgs: func(fi int) int {
			if fi >= 0 && fi < len(funcs) {
				return funcs[fi].NumArgs
			}
			return -1
		},
		FuncNumCaptures: func(fi int) int {
			if fi >= 0 && fi < len(funcs) {
				return funcs[fi].NumCaptures
			}
			return 0
		},
	}
}

// magicConst компилирует магическую константу (__LINE__ и т.п.).
func (c *compiler) magicConst(x *MagicExpr) uint8 {
	var s string
	switch x.Name {
	case "__LINE__":
		r := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: r, C: int32(c.chunk.AddConst(vm.IntConst(int64(x.Line))))})
		return r
	case "__FUNCTION__":
		s = c.funcName
		if i := strings.LastIndex(s, "::"); i >= 0 {
			s = s[i+2:]
		}
		if strings.HasPrefix(s, "{closure#") {
			s = "{closure}"
		}
	case "__METHOD__":
		s = c.funcName
		if strings.HasPrefix(s, "{closure#") {
			s = "{closure}"
		}
	case "__CLASS__":
		r := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpClassName, A: r, B: 0})
		return r
	case "__TRAIT__":
		// __TRAIT__ — имя трейта, в теле которого встречается константа
		// (пусто для обычных классов/функций). Тело метода трейта компилируется
		// в контексте трейта, поэтому имя фиксируется на этапе компиляции.
		if c.curClass != nil && c.curClass.IsTrait() {
			s = c.curClass.Name()
		}
	case "__FILE__":
		s = c.file
	case "__DIR__":
		if c.file != "" {
			s = filepath.Dir(c.file)
		}
	case "__NAMESPACE__":
		s = x.NS
	}
	r := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: r, C: int32(c.chunk.AddConst(vm.StrConst(s)))})
	return r
}

// classKeyword компилирует `Class::class` (PHP 8). Именованный класс — на
// этапе компиляции (существование не проверяется); self/static/parent —
// резолвятся в рантайме через OpClassName.
func (c *compiler) classKeyword(x *ClassKeywordExpr) (uint8, error) {
	if x.ClassExpr != nil {
		// `EXPR::class`: если EXPR — константное выражение-строка, имя
		// резолвится на компиляции (`"Foo"::class`, `("F"."oo")::class`);
		// иначе — рантайм: объект (имя класса) либо TypeError для строки
		// (класс-значение переменной использовать нельзя).
		if cv, cerr := constValueOf(x.ClassExpr); cerr == nil && cv.Type() == vm.StringType {
			r := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: r, C: int32(c.chunk.AddConst(vm.StrConst(cv.StringVal())))})
			return r, nil
		}
		v, err := c.expr(x.ClassExpr)
		if err != nil {
			return 0, err
		}
		return c.emitCall("__php_class_of", []uint8{v}), nil
	}
	switch x.Class {
	case "parent":
		// Zend проверяет наличие родителя на этапе компиляции:
		// `parent::class` в классе без extends — фатал
		// (class_name_as_scalar_error_002).
		if c.curClass != nil && c.curClass.Parent() == nil {
			return 0, &fatalError{msg: `Cannot use "parent" when current class scope has no parent`, line: c.exprLine(x)}
		}
		fallthrough
	case "self", "static":
		sel := uint8(0)
		switch x.Class {
		case "static":
			sel = 1
		case "parent":
			sel = 2
		}
		r := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpClassName, A: r, B: sel})
		return r, nil
	default:
		if strings.Contains(x.Class, "::") {
			// `Foo::Bar::class` — имя класса значения константы (enum-кейс).
			cv, err := c.expr(&ConstExpr{Name: x.Class})
			if err != nil {
				return 0, err
			}
			r := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpClassOf, A: r, B: cv})
			return r, nil
		}
		r := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: r, C: int32(c.chunk.AddConst(vm.StrConst(x.Class)))})
		return r, nil
	}
}

// castKind переводит имя типа из (int)$x в код vm.CastKind.
func castKind(t string) vm.CastKind {
	switch strings.ToLower(t) {
	case "int", "integer":
		return vm.CastInt
	case "float", "double", "real":
		return vm.CastFloat
	case "string", "binary":
		return vm.CastString
	case "bool", "boolean":
		return vm.CastBool
	case "array":
		return vm.CastArray
	case "object":
		return vm.CastObject
	case "unset":
		return vm.CastUnset
	}
	return vm.CastString
}

// topLevelSplitPHP возвращает индекс первого sep на верхнем уровне вложенности
// (учитывая круглые скобки DNF-типов).
func topLevelSplitPHP(s string, sep byte) int {
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
		i := topLevelSplitPHP(s, sep)
		if i < 0 {
			return append(out, s)
		}
		out = append(out, s[:i])
		s = s[i+1:]
	}
}

// isWrappedParens сообщает, что первый символ '(' закрывается ровно в конце
// строки (то есть вся строка — одна скобочная группа).
func isWrappedParens(t string) bool {
	if len(t) < 2 || t[0] != '(' || t[len(t)-1] != ')' {
		return false
	}
	depth := 0
	for i := 0; i < len(t); i++ {
		switch t[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i == len(t)-1
			}
		}
	}
	return false
}

// dnfAltKey строит канонический ключ варианта DNF (набор атомов пересечения,
// без учёта регистра и порядка).
func dnfAltKey(a string) string {
	a = strings.TrimSpace(a)
	if strings.HasPrefix(a, "(") && strings.HasSuffix(a, ")") {
		a = a[1 : len(a)-1]
	}
	parts := splitTopLevelAll(a, '&')
	var atoms []string
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" {
			atoms = append(atoms, p)
		}
	}
	sort.Strings(atoms)
	return strings.Join(atoms, "&")
}

// findOwnMember ищет член класса по имени (без регистра) среди заданных kind.
func findOwnMember(cd *ClassDecl, name string, kinds ...string) *ClassMember {
	for _, m := range cd.Members {
		for _, k := range kinds {
			if m.Kind == k && strings.EqualFold(memberName(m), name) {
				return m
			}
		}
	}
	return nil
}

func memberName(m *ClassMember) string {
	switch m.Kind {
	case "prop":
		return m.PropName
	case "const":
		return m.ConstName
	default:
		return m.MName
	}
}

// runtimeInheritedMethod ищет унаследованный метод у класса-предка, который
// объявлен ВНЕ компилируемого юнита (рантайм-include/eval: класс уже в таблице
// VM и его сигнатура хранится в *vm.Function). Возвращает синтетические
// объявление и член для диагностик (без дефолтов параметров — их VM не хранит).
func (c *compiler) runtimeInheritedMethod(cd *ClassDecl, name string, declByName map[string]*ClassDecl) (*ClassDecl, *ClassMember) {
	found := func(ci vm.ClassInfo) (*ClassDecl, *ClassMember) {
		f, _ := ci.MethodFunction(name)
		pd := &ClassDecl{Name: ci.Name(), IsInterface: ci.IsInterface()}
		return pd, classMemberFromRuntime(f, name)
	}
	runtimeUserClass := func(ci vm.ClassInfo) bool {
		if ci == nil {
			return false
		}
		f, ok := ci.MethodFunction(name)
		if !ok || f == nil {
			return false
		}
		// Только userland-методы: у нативных сигнатура неизвестна компилятору.
		_, userland := f.Executable.(*vm.CompiledFunction)
		return userland
	}
	seen := map[string]bool{}
	var walk func(d *ClassDecl) (*ClassDecl, *ClassMember)
	walk = func(d *ClassDecl) (*ClassDecl, *ClassMember) {
		if d == nil {
			return nil, nil
		}
		key := strings.ToLower(d.Name)
		if seen[key] {
			return nil, nil
		}
		seen[key] = true
		if d.Parent != "" {
			nk := strings.ToLower(strings.TrimLeft(d.Parent, "\\"))
			if p := declByName[nk]; p != nil {
				if pd, pm := walk(p); pm != nil {
					return pd, pm
				}
			} else if p := lookupClassInfoByName(c.classIdx, c.classes, c.availLimit, d.Parent); runtimeUserClass(p) {
				return found(p)
			}
		}
		for _, in := range d.Interfaces {
			nk := strings.ToLower(strings.TrimLeft(in, "\\"))
			if i := declByName[nk]; i != nil {
				if pd, pm := walk(i); pm != nil {
					return pd, pm
				}
			} else if i := lookupClassInfoByName(c.classIdx, c.classes, c.availLimit, in); runtimeUserClass(i) {
				return found(i)
			}
		}
		return nil, nil
	}
	return walk(cd)
}

// classMemberFromRuntime строит ClassMember из рантайм-функции (сигнатура).
func classMemberFromRuntime(f *vm.Function, name string) *ClassMember {
	m := &ClassMember{Kind: "method", MName: name, ByRefReturn: f.ByRefReturn, RetType: f.RetType, Static: f.IsStatic}
	n := len(f.ParamTypes)
	for i := 0; i < n; i++ {
		var p Param
		p.Type = f.ParamTypes[i]
		if i < len(f.ParamNames) {
			p.Name = f.ParamNames[i]
		}
		if i < len(f.ByRefArgs) {
			p.ByRef = f.ByRefArgs[i]
		}
		m.Params = append(m.Params, p)
	}
	if f.NumArgs < 0 && n > 0 {
		m.Params[n-1].Variadic = true
	}
	return m
}

// lookupClassInfoByName ищет класс в таблице юнита с учётом availLimit.
func lookupClassInfoByName(idx map[string]int, classes []vm.ClassInfo, availLimit int, name string) vm.ClassInfo {
	at := func(i int) vm.ClassInfo {
		if i < 0 || i >= len(classes) || (availLimit >= 0 && i > availLimit) {
			return nil
		}
		return classes[i]
	}
	if i, ok := idx[name]; ok {
		return at(i)
	}
	if i, ok := ciLookup(idx, name); ok {
		return at(i)
	}
	return nil
}

// findInheritedMethod ищет унаследованный метод (в родителях/интерфейсах).
// collectAllInterfaces собирает все интерфейсы, реализуемые классом прямо или
// косвенно (через родительские классы и extends интерфейсов).
// inheritedConstDeclarers возвращает объявления константы name в предках cd
// (родительская цепочка, затем интерфейсы) — по одному на объявивший тип,
// в порядке наследования Zend (final_const8/11/12).
func inheritedConstDeclarers(cd *ClassDecl, name string, byName map[string]*ClassDecl) []*ClassDecl {
	seen := map[string]bool{}
	var out []*ClassDecl
	var add func(d *ClassDecl)
	add = func(d *ClassDecl) {
		if d == nil {
			return
		}
		k := strings.ToLower(d.Name)
		if seen[k] {
			return
		}
		for _, m := range d.Members {
			if m.Kind == "const" && strings.EqualFold(m.ConstName, name) {
				seen[k] = true
				out = append(out, d)
				return
			}
		}
	}
	for p := byName[strings.ToLower(strings.TrimLeft(cd.Parent, "\\"))]; p != nil; p = byName[strings.ToLower(strings.TrimLeft(p.Parent, "\\"))] {
		add(p)
	}
	for _, in := range collectAllInterfaces(cd, byName, map[string]bool{}) {
		add(in)
	}
	return out
}

// collectAllInterfaces собирает все интерфейсы, реализуемые классом или
// наследуемые интерфейсом (транзитивно), в порядке объявления.
func collectAllInterfaces(cd *ClassDecl, byName map[string]*ClassDecl, seen map[string]bool) []*ClassDecl {
	if cd == nil {
		return nil
	}
	key := strings.ToLower(cd.Name)
	if seen[key] {
		return nil
	}
	seen[key] = true
	var out []*ClassDecl
	for _, in := range cd.Interfaces {
		i := byName[strings.ToLower(strings.TrimLeft(in, "\\"))]
		if i == nil {
			continue
		}
		out = append(out, i)
		out = append(out, collectAllInterfaces(i, byName, seen)...)
	}
	if cd.Parent != "" {
		out = append(out, collectAllInterfaces(byName[strings.ToLower(strings.TrimLeft(cd.Parent, "\\"))], byName, seen)...)
	}
	return out
}

// classInheritsInterface сообщает, реализует ли класс (или его предки) интерфейс
// name. Нужно, чтобы разрешить `extends DateTime implements DateTimeInterface`
// (интерфейс уже унаследован) и запретить чистую реализацию с нуля.
func classInheritsInterface(ci vm.ClassInfo, name string) bool {
	seen := map[string]bool{}
	var walk func(vm.ClassInfo) bool
	walk = func(c vm.ClassInfo) bool {
		if c == nil || seen[strings.ToLower(c.Name())] {
			return false
		}
		seen[strings.ToLower(c.Name())] = true
		for _, i := range c.Interfaces() {
			if i != nil && (strings.EqualFold(i.Name(), name) || walk(i)) {
				return true
			}
		}
		return walk(c.Parent())
	}
	return walk(ci)
}

// classDeclDisplayName — имя класса для диагностик компиляции: анонимные
// классы Zend печатает как Parent@anonymous / Iface@anonymous / class@anonymous.
func classDeclDisplayName(cd *ClassDecl) string {
	if cd == nil || !strings.HasPrefix(cd.Name, "\x00anon#") {
		if cd == nil {
			return ""
		}
		return cd.Name
	}
	if cd.Parent != "" {
		return strings.TrimLeft(cd.Parent, "\\") + "@anonymous"
	}
	if len(cd.Interfaces) > 0 {
		return strings.TrimLeft(cd.Interfaces[0], "\\") + "@anonymous"
	}
	return "class@anonymous"
}

// declImplementsSerializable сообщает, реализует ли класс интерфейс Serializable
// напрямую или через расширение интерфейсов.
func declImplementsSerializable(cd *ClassDecl, byName map[string]*ClassDecl) bool {
	seen := map[string]bool{}
	var walk func(*ClassDecl) bool
	walk = func(c *ClassDecl) bool {
		if c == nil || seen[strings.ToLower(c.Name)] {
			return false
		}
		seen[strings.ToLower(c.Name)] = true
		for _, in := range c.Interfaces {
			n := strings.ToLower(strings.TrimLeft(in, "\\"))
			if n == "serializable" {
				return true
			}
			if walk(byName[n]) {
				return true
			}
		}
		if c.Parent != "" {
			return walk(byName[strings.ToLower(strings.TrimLeft(c.Parent, "\\"))])
		}
		return false
	}
	return walk(cd)
}

// declaresTraversable сообщает, объявляет ли класс (в своей цепочке) интерфейс
// Traversable напрямую.
func declaresTraversable(cd *ClassDecl, byName map[string]*ClassDecl) bool {
	seen := map[string]bool{}
	var walk func(*ClassDecl) bool
	walk = func(c *ClassDecl) bool {
		if c == nil || seen[strings.ToLower(c.Name)] {
			return false
		}
		seen[strings.ToLower(c.Name)] = true
		for _, in := range c.Interfaces {
			n := strings.ToLower(strings.TrimLeft(in, "\\"))
			if n == "traversable" {
				return true
			}
		}
		if c.Parent != "" {
			return walk(byName[strings.ToLower(strings.TrimLeft(c.Parent, "\\"))])
		}
		return false
	}
	return walk(cd)
}

// implementsIteratorAPI сообщает, реализует ли класс (в своей цепочке)
// интерфейс Iterator или IteratorAggregate.
func implementsIteratorAPI(cd *ClassDecl, byName map[string]*ClassDecl) bool {
	seen := map[string]bool{}
	var walk func(*ClassDecl) bool
	walk = func(c *ClassDecl) bool {
		if c == nil || seen[strings.ToLower(c.Name)] {
			return false
		}
		seen[strings.ToLower(c.Name)] = true
		for _, in := range c.Interfaces {
			n := strings.ToLower(strings.TrimLeft(in, "\\"))
			if n == "iterator" || n == "iteratoraggregate" {
				return true
			}
			if sub := byName[n]; sub != nil && walk(sub) {
				return true
			}
		}
		if c.Parent != "" {
			return walk(byName[strings.ToLower(strings.TrimLeft(c.Parent, "\\"))])
		}
		return false
	}
	return walk(cd)
}

func findInheritedMethod(cd *ClassDecl, name string, byName map[string]*ClassDecl, seen map[string]bool) (*ClassDecl, *ClassMember) {
	if cd == nil {
		return nil, nil
	}
	key := strings.ToLower(cd.Name)
	if seen[key] {
		return nil, nil
	}
	seen[key] = true
	if cd.Parent != "" {
		if p := byName[strings.ToLower(cd.Parent)]; p != nil {
			if m := findOwnMember(p, name, "method", "signature"); m != nil {
				return p, m
			}
			// Метод, примешанный трейтом родителя: Zend называет его по
			// классу-потребителю (A::foo()), а не по трейту (bug62358).
			if td := methodProvidedDecl(p, name, byName); td != nil {
				if m := findOwnMember(td, name, "method", "signature"); m != nil {
					return p, m
				}
			}
			if pd, pm := findInheritedMethod(p, name, byName, seen); pm != nil {
				return pd, pm
			}
		}
	}
	for _, in := range cd.Interfaces {
		if i := byName[strings.ToLower(in)]; i != nil {
			if m := findOwnMember(i, name, "method", "signature"); m != nil {
				return i, m
			}
			if pd, pm := findInheritedMethod(i, name, byName, seen); pm != nil {
				return pd, pm
			}
		}
	}
	return nil, nil
}

// findInheritedPropConst ищет унаследованный член (свойство/константу) по
// родительской цепочке классов и интерфейсам.
func findInheritedPropConst(cd *ClassDecl, name string, byName map[string]*ClassDecl) (*ClassDecl, *ClassMember) {
	seen := map[string]bool{}
	var search func(*ClassDecl) (*ClassDecl, *ClassMember)
	search = func(c *ClassDecl) (*ClassDecl, *ClassMember) {
		if c == nil || seen[strings.ToLower(c.Name)] {
			return nil, nil
		}
		seen[strings.ToLower(c.Name)] = true
		if c.Parent != "" {
			if p := byName[strings.ToLower(c.Parent)]; p != nil {
				if m := findOwnMember(p, name, "prop", "const"); m != nil {
					return p, m
				}
				if d, mm := search(p); mm != nil {
					return d, mm
				}
			}
		}
		for _, in := range c.Interfaces {
			if i := byName[strings.ToLower(in)]; i != nil {
				if m := findOwnMember(i, name, "prop", "const"); m != nil {
					return i, m
				}
				if d, mm := search(i); mm != nil {
					return d, mm
				}
			}
		}
		return nil, nil
	}
	return search(cd)
}

// requiredAfter сообщает, что после параметра i есть обязательный параметр.
// Zend при объявлении такого дефолта сбрасывает значение (Deprecated
// «implicitly treated as a required parameter»), и в диагностиках дефолт
// не печатается (см. bug71428.3).
func requiredAfter(params []Param, i int) bool {
	for j := i + 1; j < len(params); j++ {
		if params[j].Default == nil && !params[j].Variadic {
			return true
		}
	}
	return false
}

// paramDefaultString форматирует значение параметра по умолчанию для
// сообщений о несовместимости сигнатур (как это делает Zend).
// self/parent — область объявления метода: для `self::class`/`parent::class`
// Zend печатает уже вычисленную строку ('Foo').
func paramDefaultString(e Expr, self, parent string) (string, bool) {
	switch x := e.(type) {
	case nil:
		return "", false
	case *IntLit:
		return strconv.FormatInt(x.V, 10), true
	case *FloatLit:
		// Zend: zval_get_tmp_string -> php_gcvt с precision=14.
		return floatGcvt(x.V), true
	case *StrLit:
		// Zend печатает строку в одинарных кавычках, но не длиннее 10
		// символов + "...".
		s := x.V
		if len(s) > 10 {
			s = s[:10] + "..."
		}
		return "'" + s + "'", true
	case *BoolLit:
		if x.V {
			return "true", true
		}
		return "false", true
	case *NullLit:
		return "null", true
	case *ArrayLit:
		if len(x.Elems) == 0 {
			return "[]", true
		}
		return "[...]", true
	case *ClassKeywordExpr:
		// `Class::class` в дефолте: Zend печатает строку с именем класса
		// ('Foo'), а динамическое выражение — `<expression>`.
		if x.ClassExpr != nil {
			return "<expression>", true
		}
		name := x.Class
		switch strings.ToLower(name) {
		case "self", "static":
			name = self
		case "parent":
			name = parent
		}
		name = strings.TrimLeft(name, "\\")
		if name == "" {
			return "", false
		}
		return "'" + name + "'", true
	case *ConstExpr:
		if x.NameExpr != nil || x.ClassExpr != nil {
			return "<expression>", true
		}
		return x.Name, true
	}
	return "", false
}

// floatGcvt повторяет php_gcvt(v, 14, '.', 'E') из Zend (zval_get_tmp_string):
// 14 значащих цифр, фиксированная запись при -4 < decpt <= 14, иначе
// экспоненциальная с 'E', знаком и точкой в мантиссе (1.0E+15).
func floatGcvt(v float64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	av := math.Abs(v)
	if math.IsInf(av, 0) {
		if neg {
			return "-INF"
		}
		return "INF"
	}
	if math.IsNaN(v) {
		return "NAN"
	}
	s := strconv.FormatFloat(av, 'E', 13, 64)
	mant, expStr, _ := strings.Cut(s, "E")
	exp, _ := strconv.Atoi(expStr)
	decpt := exp + 1 // позиция десятичной точки
	digs := strings.Replace(mant, ".", "", 1)
	var out string
	switch {
	case decpt <= -4 || decpt > 14:
		m := strings.TrimRight(mant, "0")
		if strings.HasSuffix(m, ".") {
			m += "0"
		}
		if !strings.Contains(m, ".") {
			m += ".0"
		}
		e := decpt - 1
		if e < 0 {
			out = m + "E" + strconv.Itoa(e)
		} else {
			out = m + "E+" + strconv.Itoa(e)
		}
	case decpt <= 0:
		out = "0." + strings.Repeat("0", -decpt) + strings.TrimRight(digs, "0")
		if out == "0." {
			out = "0"
		}
	case decpt >= len(digs):
		out = digs + strings.Repeat("0", decpt-len(digs))
	default:
		out = digs[:decpt] + "." + strings.TrimRight(digs[decpt:], "0")
		if strings.HasSuffix(out, ".") {
			out = out[:len(out)-1]
		}
	}
	if neg {
		out = "-" + out
	}
	return out
}

// methodSigString форматирует подпись метода так, как это делает Zend.
func methodSigString(cls string, m *ClassMember) string {
	return methodSigStringSub(cls, m, "", "")
}

// methodSigStringSub — methodSigString с разрешением self/static/parent в
// области объявления (для диагностик несовместимости подписей).
func methodSigStringSub(cls string, m *ClassMember, self, parent string) string {
	var b strings.Builder
	if m.ByRefReturn {
		// Zend: zend_get_function_declaration печатает "& " перед именем.
		b.WriteString("& ")
	}
	b.WriteString(cls + "::" + m.MName + "(")
	for i, p := range m.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		if p.Type != "" {
			typ := p.Type
			if self != "" {
				typ = substRelativeAtoms(typ, self, parent)
			}
			// `T $x = null` неявно делает параметр nullable; Zend печатает
			// такой тип как `?T` в диагностиках совместимости.
			if k, ok := defaultKind(p.Default); ok && k == "null" && !typeNullable(typ) {
				typ = "?" + typ
			}
			b.WriteString(displayType(typ) + " ")
		}
		if p.ByRef {
			b.WriteString("&")
		}
		if p.Variadic {
			b.WriteString("...")
		}
		b.WriteString("$" + strings.TrimPrefix(p.Name, "$"))
		if p.Variadic {
			continue
		}
		if !requiredAfter(m.Params, i) {
			if s, ok := paramDefaultString(p.Default, self, parent); ok {
				b.WriteString(" = " + s)
			}
		}
	}
	b.WriteString(")")
	if m.RetType != "" {
		typ := m.RetType
		if self != "" {
			typ = substRelativeAtoms(typ, self, parent)
		}
		b.WriteString(": " + displayType(typ))
	}
	return b.String()
}

// builtinTypeNames — встроенные имена типов (не классы).
var builtinTypeNames = map[string]bool{
	"int": true, "float": true, "string": true, "bool": true,
	"array": true, "object": true, "callable": true, "iterable": true,
	"void": true, "never": true, "null": true, "true": true, "false": true,
	"mixed": true, "void-": true,
}

// normalizeTypeAlts разворачивает тип в DNF: список альтернатив, каждая —
// набор атомов (пересечение). iterable -> array|Traversable, ?T -> T|null.
func normalizeTypeAlts(t string) [][]string {
	if t == "" {
		return [][]string{{"mixed"}}
	}
	if strings.HasPrefix(t, "?") {
		t = t[1:] + "|null"
	}
	var alts [][]string
	for _, alt := range splitTopLevelAll(t, '|') {
		alt = strings.TrimSpace(alt)
		if alt == "iterable" {
			alts = append(alts, []string{"array"}, []string{"Traversable"})
			continue
		}
		if strings.HasPrefix(alt, "(") && strings.HasSuffix(alt, ")") {
			alt = alt[1 : len(alt)-1]
		}
		var parts []string
		for _, p := range splitTopLevelAll(alt, '&') {
			parts = append(parts, strings.TrimSpace(p))
		}
		alts = append(alts, parts)
	}
	return alts
}

// typeSubtype сообщает, что тип child является подтипом parent (child ⊆ parent).
func (c *compiler) typeSubtype(child, parent string, resolve func(string) string) bool {
	ca := normalizeTypeAlts(child)
	pa := normalizeTypeAlts(parent)
	for _, cAlt := range ca {
		if !c.altSubtype(cAlt, pa, resolve) {
			return false
		}
	}
	return true
}

func (c *compiler) altSubtype(cAlt []string, pa [][]string, resolve func(string) string) bool {
	for _, pAlt := range pa {
		if c.interSubtype(cAlt, pAlt, resolve) {
			return true
		}
	}
	return false
}

func (c *compiler) interSubtype(cAlt, pAlt []string, resolve func(string) string) bool {
	for _, p := range pAlt {
		ok := false
		for _, ca := range cAlt {
			if c.atomSubtype(ca, p, resolve) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func (c *compiler) atomSubtype(ca, pa string, resolve func(string) string) bool {
	ca, pa = resolve(ca), resolve(pa)
	if strings.EqualFold(ca, pa) {
		return true
	}
	lc, lp := strings.ToLower(ca), strings.ToLower(pa)
	if lp == "mixed" || lc == "never" {
		return true
	}
	if (lc == "true" || lc == "false") && lp == "bool" {
		return true
	}
	if lc == "int" && lp == "float" {
		return true
	}
	if lc == "array" && lp == "iterable" {
		return true
	}
	if lc == "traversable" && lp == "iterable" {
		return true
	}
	// Любой класс/интерфейс (включая self/static/parent) является подтипом object.
	if lp == "object" && !builtinTypeNames[lc] {
		return true
	}
	if builtinTypeNames[lc] || builtinTypeNames[lp] {
		return false
	}
	ci := c.lookupClassInfo(ca)
	pi := c.lookupClassInfo(pa)
	if ci != nil && pi != nil {
		if c.isACompat(ci, pi) {
			return true
		}
	}
	// Любой класс с __toString неявно реализует Stringable.
	if lp == "stringable" && ci != nil {
		if _, ok := ci.MethodFunction("__toString"); ok {
			return true
		}
	}
	return false
}

// Статусы трёхзначной проверки совместимости: тип либо несовместим (subNo),
// либо совместим (subYes), либо совместимость не удаётся установить, потому
// что класс в типе недоступен (subUnknown).
const (
	subNo = iota
	subUnknown
	subYes
)

// typeSubtype3 — трёхзначный вариант typeSubtype: возвращает статус и, для
// subUnknown, имя первого недоступного класса, из-за которого проверка не
// завершилась.
func (c *compiler) typeSubtype3(child, parent string, resolve func(string) string) (int, string) {
	ca := normalizeTypeAlts(child)
	pa := normalizeTypeAlts(parent)
	overall := subYes
	unavail := ""
	for _, cAlt := range ca {
		st, un := c.altSubtype3(cAlt, pa, resolve)
		if un != "" && unavail == "" {
			unavail = un
		}
		overall = combineAll(overall, st)
	}
	if overall == subYes {
		return overall, ""
	}
	return overall, unavail
}

func (c *compiler) altSubtype3(cAlt []string, pa [][]string, resolve func(string) string) (int, string) {
	overall := subNo
	unavail := ""
	for _, pAlt := range pa {
		st, un := c.interSubtype3(cAlt, pAlt, resolve)
		if un != "" && unavail == "" {
			unavail = un
		}
		overall = combineAny(overall, st)
		if overall == subYes {
			return subYes, ""
		}
	}
	if overall == subYes {
		return overall, ""
	}
	return overall, unavail
}

func (c *compiler) interSubtype3(cAlt, pAlt []string, resolve func(string) string) (int, string) {
	// Спецслучай parent == object: пересечение классов совместимо с object,
	// если хотя бы один его член — известный класс.
	if len(pAlt) == 1 && strings.EqualFold(resolve(pAlt[0]), "object") && len(cAlt) > 1 {
		unknown := ""
		for _, ca := range cAlt {
			ra := resolve(ca)
			if builtinTypeNames[strings.ToLower(ra)] {
				continue
			}
			if c.lookupClassInfo(ra) != nil {
				return subYes, ""
			}
			if unknown == "" {
				unknown = ra
			}
		}
		if unknown != "" {
			return subUnknown, unknown
		}
		return subNo, ""
	}
	overall := subYes
	unavail := ""
	for _, p := range pAlt {
		st, un := c.anyAtomSubtype3(cAlt, p, resolve)
		if un != "" && unavail == "" {
			unavail = un
		}
		overall = combineAll(overall, st)
	}
	if overall == subYes {
		return overall, ""
	}
	return overall, unavail
}

func (c *compiler) anyAtomSubtype3(cAlt []string, p string, resolve func(string) string) (int, string) {
	overall := subNo
	unavail := ""
	for _, ca := range cAlt {
		st, un := c.atomSubtype3(ca, p, resolve)
		if un != "" && unavail == "" {
			unavail = un
		}
		overall = combineAny(overall, st)
		if overall == subYes {
			return subYes, ""
		}
	}
	if overall == subYes {
		return overall, ""
	}
	return overall, unavail
}

func (c *compiler) atomSubtype3(ca, pa string, resolve func(string) string) (int, string) {
	ca, pa = resolve(ca), resolve(pa)
	if strings.EqualFold(ca, pa) {
		return subYes, ""
	}
	lc, lp := strings.ToLower(ca), strings.ToLower(pa)
	// `static` — позднесвязанный тип. Родительский `static` требует от
	// потомка тоже `static`; `static` потомка уже родительского класса.
	// Исключение: `never` — нижний тип, совместим с любым объявлением.
	if lp == "static" {
		if lc == "never" {
			return subYes, ""
		}
		return subNo, ""
	}
	if lc == "static" {
		if lp == "object" || !builtinTypeNames[lp] {
			return subYes, ""
		}
		return subNo, ""
	}
	if lc == "void" && lp != "void" {
		return subNo, ""
	}
	if lp == "mixed" || lc == "never" {
		return subYes, ""
	}
	if (lc == "true" || lc == "false") && lp == "bool" {
		return subYes, ""
	}
	if lc == "int" && lp == "float" {
		return subYes, ""
	}
	if lc == "array" && lp == "iterable" {
		return subYes, ""
	}
	if lc == "traversable" && lp == "iterable" {
		return subYes, ""
	}
	if lp == "object" && !builtinTypeNames[lc] {
		ci := c.lookupClassInfo(ca)
		if ci != nil {
			return subYes, ""
		}
		return subUnknown, ca
	}
	if builtinTypeNames[lc] || builtinTypeNames[lp] {
		return subNo, ""
	}
	ci := c.lookupClassInfo(ca)
	pi := c.lookupClassInfo(pa)
	if ci != nil && pi != nil {
		if c.isACompat(ci, pi) {
			return subYes, ""
		}
		if lp == "stringable" {
			if _, ok := ci.MethodFunction("__toString"); ok {
				return subYes, ""
			}
		}
		return subNo, ""
	}
	if lp == "stringable" && ci != nil {
		if _, ok := ci.MethodFunction("__toString"); ok {
			return subYes, ""
		}
	}
	if ci == nil {
		return subUnknown, ca
	}
	return subUnknown, pa
}

// combineAll — конъюнкция статусов: No доминирует, затем Unknown.
func combineAll(a, b int) int {
	if a == subNo || b == subNo {
		return subNo
	}
	if a == subUnknown || b == subUnknown {
		return subUnknown
	}
	return subYes
}

// combineAny — дизъюнкция статусов: Yes доминирует, затем Unknown.
func combineAny(a, b int) int {
	if a == subYes || b == subYes {
		return subYes
	}
	if a == subUnknown || b == subUnknown {
		return subUnknown
	}
	return subNo
}

// isACompat — проверка «child является подтипом parent»: при компиляции через
// ClassInfo.IsA, в отложенной рантайм-проверке — через vm.UnlinkedIsA
// (учитывает классы, связывание которых ещё не завершено).
func (c *compiler) isACompat(child, parent vm.ClassInfo) bool {
	if c.rtIsA != nil {
		return c.rtIsA(child, parent)
	}
	return child.IsA(parent)
}

// lookupClassInfo ищет класс по имени среди известных компилятору.
func (c *compiler) lookupClassInfo(name string) vm.ClassInfo {
	if c.rtLookup != nil {
		return c.rtLookup(name)
	}
	name = strings.TrimLeft(name, "\\")
	if i, ok := c.classIdx[name]; ok && i < len(c.classes) {
		return c.classAt(i)
	}
	if i, ok := ciLookup(c.classIdx, name); ok && i < len(c.classes) {
		return c.classAt(i)
	}
	// Псевдоним класса (class_alias): резолвим цель.
	if tgt, ok := c.classAlias[strings.ToLower(name)]; ok {
		if i, ok := c.classIdx[tgt]; ok && i < len(c.classes) {
			return c.classAt(i)
		}
		if i, ok := ciLookup(c.classIdx, tgt); ok && i < len(c.classes) {
			return c.classAt(i)
		}
	}
	return nil
}

// classAt возвращает класс по индексу с учётом ограничения availLimit (см.
// поле): классы, объявленные позже текущего, недоступны при проверке сигнатур.
func (c *compiler) classAt(i int) vm.ClassInfo {
	if c.availLimit >= 0 && i > c.availLimit {
		return nil
	}
	return c.classes[i]
}

// nativeSig — сигнатура метода встроенного класса для проверки совместимости
// переопределения. Tentative отмечает «предварительный» тип возврата Zend
// (напр. DateTimeZone::listIdentifiers(): array): при несовместимом/отсутствующем
// возврате Zend выдаёт Deprecated, а не фатал.
type nativeSig struct {
	params    []Param
	ret       string
	tentative bool
}

// nativeVarianceSigs — сигнатуры методов встроенных классов, участвующих в
// проверке вариантности (ковариантность возврата). Типы хранятся БЕЗ неявной
// nullable-разности: `T $x = null` дополняется до `?T` при печати/проверке.
var nativeVarianceSigs = map[string]nativeSig{
	"datetimezone::listidentifiers": {
		params: []Param{
			{Type: "int", Name: "timezoneGroup", Default: &ConstExpr{Name: "DateTimeZone::ALL"}},
			{Type: "string", Name: "countryCode", Default: &NullLit{}},
		},
		ret:       "array",
		tentative: true,
	},
	"datetime::createfromformat": {
		params: []Param{
			{Type: "string", Name: "format"},
			{Type: "string", Name: "datetime"},
			{Type: "DateTimeZone", Name: "timezone", Default: &NullLit{}},
		},
		ret:       "DateTime|false",
		tentative: true,
	},
	"datetime::settime": {
		params: []Param{
			{Type: "int", Name: "hour"},
			{Type: "int", Name: "minute"},
			{Type: "int", Name: "second", Default: &IntLit{V: 0}},
			{Type: "int", Name: "microsecond", Default: &IntLit{V: 0}},
		},
		ret:       "DateTime",
		tentative: true,
	},
	"datetimezone::gettransitions": {
		params: []Param{
			{Type: "int", Name: "timestampBegin", Default: &ConstExpr{Name: "PHP_INT_MIN"}},
			{Type: "int", Name: "timestampEnd", Default: &ConstExpr{Name: "PHP_INT_MAX"}},
		},
		ret:       "array|false",
		tentative: true,
	},
	"datetimeinterface::diff": {
		params: []Param{
			{Type: "DateTimeInterface", Name: "targetObject"},
			{Type: "bool", Name: "absolute", Default: &BoolLit{V: false}},
		},
		ret:       "DateInterval",
		tentative: true,
	},
	"arrayaccess::offsetset": {
		params: []Param{
			{Type: "mixed", Name: "offset"},
			{Type: "mixed", Name: "value"},
		},
		ret:       "void",
		tentative: true,
	},
	"php_user_filter::filter": {
		params: []Param{
			{Name: "in"},
			{Name: "out"},
			{Name: "consumed", ByRef: true},
			{Type: "bool", Name: "closing"},
		},
		ret:       "int",
		tentative: true,
	},
	"php_user_filter::oncreate": {
		params:    []Param{},
		ret:       "bool",
		tentative: true,
	},
	"php_user_filter::onclose": {
		params:    []Param{},
		ret:       "void",
		tentative: true,
	},
}

// nativeMember строит ClassMember из сигнатуры встроенного метода (для печати
// сигнатуры в диагностиках).
func nativeMember(name string, sig nativeSig) *ClassMember {
	return &ClassMember{MName: name, Params: sig.params, RetType: sig.ret}
}

// nativeAncestor возвращает ближайший встроенный класс в цепочке наследования
// cd (или nil, если он отсутствует). Пользовательские предки пропускаются.
func (c *compiler) nativeAncestor(cd *ClassDecl, declByName map[string]*ClassDecl, seen map[string]bool) vm.ClassInfo {
	if cd == nil || cd.Parent == "" {
		return nil
	}
	key := strings.ToLower(cd.Parent)
	if seen[key] {
		return nil
	}
	seen[key] = true
	if p := declByName[key]; p != nil {
		return c.nativeAncestor(p, declByName, seen)
	}
	if i, ok := c.classIdx[cd.Parent]; ok && i >= 0 && i < len(c.classes) {
		return c.classes[i]
	}
	if i, ok := ciLookup(c.classIdx, cd.Parent); ok && i >= 0 && i < len(c.classes) {
		return c.classes[i]
	}
	return nil
}

// nativeIfaces собирает встроенные интерфейсы, реализуемые классом или
// интерфейсом (рекурсивно: собственные implements/extends, интерфейсы
// родителей). Пользовательские интерфейсы разворачиваются в их собственные
// implements, встроенные попадают в результат.
func (c *compiler) nativeIfaces(cd *ClassDecl, declByName map[string]*ClassDecl) []vm.ClassInfo {
	var out []vm.ClassInfo
	seen := map[string]bool{}
	var walk func(d *ClassDecl)
	walk = func(d *ClassDecl) {
		if d == nil {
			return
		}
		k := strings.ToLower(d.Name)
		if seen[k] {
			return
		}
		seen[k] = true
		for _, in := range d.Interfaces {
			nk := strings.ToLower(strings.TrimLeft(in, "\\"))
			if id := declByName[nk]; id != nil {
				walk(id)
				continue
			}
			if i, ok := c.classIdx[in]; ok && i >= 0 && i < len(c.classes) {
				out = append(out, c.classes[i])
				continue
			}
			if i, ok := ciLookup(c.classIdx, in); ok && i >= 0 && i < len(c.classes) {
				out = append(out, c.classes[i])
			}
		}
		if d.Parent != "" {
			walk(declByName[strings.ToLower(strings.TrimLeft(d.Parent, "\\"))])
		}
	}
	walk(cd)
	return out
}

// checkNativeOverrideCompat проверяет совместимость метода, переопределяющего
// метод встроенного класса. Учитывает «tentative» тип возврата Zend.
func (c *compiler) checkNativeOverrideCompat(cd *ClassDecl, m *ClassMember, np vm.ClassInfo, sig nativeSig) error {
	identity := func(t string) string { return t }
	// Приводим тип к эффективному (с неявной nullable от `= null`).
	effType := func(p Param) string {
		t := p.Type
		if k, ok := defaultKind(p.Default); ok && k == "null" && t != "" && !typeNullable(t) {
			t = "?" + t
		}
		return t
	}
	paramStatus, unavail := subYes, ""
	record := func(st int, un string) {
		if st == subNo {
			paramStatus = subNo
			return
		}
		if st == subUnknown {
			if paramStatus == subYes {
				paramStatus = subUnknown
			}
			if unavail == "" {
				unavail = un
			}
		}
	}
	// Контравариантность параметров (parent ⊆ child).
	if len(m.Params) < len(sig.params) {
		paramStatus = subNo
	} else {
		for i := range sig.params {
			childT := substRelativeAtoms(m.Params[i].Type, cd.Name, cd.Parent)
			if k, ok := defaultKind(m.Params[i].Default); ok && k == "null" && childT != "" && !typeNullable(childT) {
				childT = "?" + childT
			}
			parentT := effType(sig.params[i])
			st, un := c.typeSubtype3(parentT, childT, identity)
			record(st, un)
			if paramStatus == subNo {
				break
			}
			if m.Params[i].ByRef != sig.params[i].ByRef || m.Params[i].Variadic != sig.params[i].Variadic {
				paramStatus = subNo
				break
			}
		}
		if paramStatus != subNo {
			for i := len(sig.params); i < len(m.Params); i++ {
				if m.Params[i].Default == nil && !m.Params[i].Variadic {
					paramStatus = subNo
					break
				}
			}
		}
	}
	// Ковариантность возврата (child ⊆ parent).
	retStatus := subYes
	if sig.ret == "" {
		// нет возврата у родителя — ограничений нет
	} else if m.RetType == "" {
		retStatus = subNo
	} else {
		st, un := c.typeSubtype3(substRelativeAtoms(m.RetType, cd.Name, cd.Parent), sig.ret, identity)
		retStatus = st
		if un != "" && unavail == "" {
			unavail = un
		}
	}
	childSig := methodSigStringSub(cd.Name, m, cd.Name, cd.Parent)
	parentSig := methodSigStringSub(np.Name(), nativeMember(m.MName, sig), np.Name(), "")
	if paramStatus == subUnknown || retStatus == subUnknown {
		return c.newFatal("Could not check compatibility between " + childSig +
			" and " + parentSig + ", because class " + unavail + " is not available")
	}
	if paramStatus == subNo {
		return c.newFatal("Declaration of " + childSig + " must be compatible with " + parentSig)
	}
	if retStatus == subNo {
		if sig.tentative {
			compilerDeprecations = append(compilerDeprecations, fmt.Sprintf(
				"Return type of %s should either be compatible with %s, or the #[\\ReturnTypeWillChange] attribute should be used to temporarily suppress the notice in %s on line %d",
				childSig, parentSig, c.file, m.Line))
			return nil
		}
		return c.newFatal("Declaration of " + childSig + " must be compatible with " + parentSig)
	}
	return nil
}

// deferTopVariance откладывает проверку совместимости метода класса верхнего
// уровня до выполнения его объявления (Zend: класс не может быть early-bound,
// если проверка не завершилась). План прикрепляется к дескриптору класса, а на
// позиции объявления компилятор эмитит __php_variance_check.
func (c *compiler) deferTopVariance(cd *ClassDecl, classPos int, m *ClassMember, pd *ClassDecl, pm *ClassMember) {
	c.deferVarianceCheck(cd, classPos, rtVarCheck{cd: cd, m: m, pd: pd, pm: pm})
}

// deferVarianceCheck добавляет одну отложенную проверку владельцу плана
// (cd — класс, объявление которого выполняет проверку; сама проверка может
// относиться к методу предка, как в bug79179).
func (c *compiler) deferVarianceCheck(cd *ClassDecl, classPos int, chk rtVarCheck) {
	if c.topVariance == nil {
		c.topVariance = map[*ClassDecl]*rtVarPlan{}
	}
	plan := c.topVariance[cd]
	if plan == nil {
		plan = &rtVarPlan{file: cd.DefFile, rtIdx: c.deferClassBase + classPos}
		if plan.file == "" {
			plan.file = c.file
		}
		c.topVariance[cd] = plan
		if idx := plan.rtIdx; idx >= 0 && idx < len(c.classes) {
			if sc, ok := c.classes[idx].(interface{ SetCheckData(any) }); ok {
				sc.SetCheckData(plan)
			}
		}
	}
	plan.checks = append(plan.checks, chk)
}

// checkOverrideCompatible проверяет совместимость переопределения метода.
func (c *compiler) checkOverrideCompatible(cd *ClassDecl, m *ClassMember, pd *ClassDecl, pm *ClassMember) error {
	status, unavail, err := c.overrideCompat(cd, m, pd, pm)
	if err != nil {
		return err
	}
	switch status {
	case subYes:
		return nil
	case subUnknown:
		msg := "Could not check compatibility between " + methodSigStringSub(cd.Name, m, cd.Name, cd.Parent) +
			" and " + methodSigStringSub(pd.Name, pm, pd.Name, pd.Parent) + ", because class " + unavail + " is not available"
		return c.newFatal(msg)
	default:
		msg := "Declaration of " + methodSigStringSub(cd.Name, m, cd.Name, cd.Parent) + " must be compatible with " + methodSigStringSub(pd.Name, pm, pd.Name, pd.Parent)
		return c.newFatal(msg)
	}
}

// overrideCompat — проверка переопределения метода с трёхзначным результатом:
// final/static/видимость/приватность — ошибка (err), совместимость сигнатур —
// статус (subYes/subNo/subUnknown) и имя недоступного класса.
func (c *compiler) overrideCompat(cd *ClassDecl, m *ClassMember, pd *ClassDecl, pm *ClassMember) (int, string, error) {
	if strings.EqualFold(pm.Vis, "private") && pm.Kind != "signature" && !strings.EqualFold(pm.MName, "__construct") {
		// Приватные методы не наследуются: их невозможно переопределить,
		// поэтому проверка совместимости (в т.ч. final) не применяется.
		// Исключение — приватный final-конструктор: его переопределять нельзя.
		return subYes, "", nil
	}
	if pm.Final {
		return subNo, "", c.newFatal(fmt.Sprintf("Cannot override final method %s::%s()", pd.Name, pm.MName))
	}
	if pm.Static != m.Static {
		if pm.Static {
			return subNo, "", c.newFatal(fmt.Sprintf("Cannot make static method %s::%s() non static in class %s", pd.Name, m.MName, cd.Name))
		}
		return subNo, "", c.newFatal(fmt.Sprintf("Cannot make non static method %s::%s() static in class %s", pd.Name, m.MName, cd.Name))
	}
	// Понижение видимости переопределяемого метода недопустимо.
	{
		pv := strings.ToLower(pm.Vis)
		if pv == "" {
			pv = "public"
		}
		mv := strings.ToLower(m.Vis)
		if mv == "" {
			mv = "public"
		}
		if visRank(mv) < visRank(pv) {
			// Магические методы, обязанные быть public, понижение видимости
			// не фаталит: Zend печатает только Warning (см. ветку компиляции
			// метода). Остальные методы — фатал.
			if !magicMustBePublic[strings.ToLower(m.MName)] {
				if pv == "protected" {
					return subNo, "", c.newFatal(fmt.Sprintf("Access level to %s::%s() must be protected (as in class %s) or weaker", cd.Name, m.MName, pd.Name))
				}
				return subNo, "", c.newFatal(fmt.Sprintf("Access level to %s::%s() must be %s (as in class %s)", cd.Name, m.MName, pv, pd.Name))
			}
		}
	}
	// Конструкторы (и деструкторы) не проверяются на совместимость подписи
	// при наследовании конкретного класса, но проверяются при реализации
	// интерфейса и при переопределении абстрактного конструктора/деструктора
	// (как в Zend).
	if (strings.EqualFold(m.MName, "__construct") || strings.EqualFold(m.MName, "__destruct")) && !pd.IsInterface && !pm.Abstract {
		return subYes, "", nil
	}
	status, unavail := c.implCheckCompat(cd, m, pd, pm)
	return status, unavail, nil
}

// implCheckCompat — ядро проверки совместимости сигнатур (ковариантность
// возврата, контравариантность параметров) без final/static/видимости.
// Возвращает трёхзначный статус и имя первого недоступного класса (subUnknown).
// Используется и при компиляции, и в отложенных рантайм-проверках, где
// c.rtLookup/rtIsA смотрят в таблицу классов VM.
func (c *compiler) implCheckCompat(cd *ClassDecl, m *ClassMember, pd *ClassDecl, pm *ClassMember) (int, string) {
	// Конструкторы (и деструкторы) не проверяются на совместимость подписи
	// при наследовании конкретного класса, но проверяются при реализации
	// интерфейса и при переопределении абстрактного конструктора/деструктора
	// (как в Zend).
	if (strings.EqualFold(m.MName, "__construct") || strings.EqualFold(m.MName, "__destruct")) && !pd.IsInterface && !pm.Abstract {
		return subYes, ""
	}
	// Относительные имена типов разрешаются в области объявления того метода,
	// где они записаны (child — cd, parent — pd), поэтому подставляем их до
	// сравнения, а сам subtype-механизм работает с конкретными именами.
	identity := func(t string) string { return t }
	status := subYes
	unavail := ""
	record := func(st int, un string) {
		if st == subNo {
			status = subNo
			return
		}
		if st == subUnknown {
			if status == subYes {
				status = subUnknown
			}
			if unavail == "" {
				unavail = un
			}
		}
	}
	// Контравариантность параметров — по алгоритму Zend
	// (zend_do_perform_implementation_check): вариативный параметр
	// «повторяется» для недостающих позиций с обеих сторон.
	if numRequired(pm.Params) < numRequired(m.Params) {
		status = subNo
	}
	if status != subNo {
		protoVar := len(pm.Params) > 0 && pm.Params[len(pm.Params)-1].Variadic
		childVar := len(m.Params) > 0 && m.Params[len(m.Params)-1].Variadic
		if protoVar && !childVar {
			status = subNo
		}
		if status != subNo {
			protoNum := len(pm.Params)
			if protoVar {
				protoNum++
			}
			childNum := len(m.Params)
			if childVar {
				childNum++
			}
			n := protoNum
			if childNum > n {
				n = childNum
			}
			for i := 0; i < n; i++ {
				var protoArg, childArg *Param
				if i < len(pm.Params) {
					protoArg = &pm.Params[i]
				} else if protoVar {
					protoArg = &pm.Params[len(pm.Params)-1]
				}
				if i < len(m.Params) {
					childArg = &m.Params[i]
				} else if childVar {
					childArg = &m.Params[len(m.Params)-1]
				}
				if protoArg == nil {
					// Добавлен новый (необязательный) параметр — допустимо.
					continue
				}
				if childArg == nil {
					status = subNo
					break
				}
				childT := substRelativeAtoms(childArg.Type, cd.Name, cd.Parent)
				parentT := substRelativeAtoms(protoArg.Type, pd.Name, pd.Parent)
				if k, ok := defaultKind(childArg.Default); ok && k == "null" && childT != "" && !typeNullable(childT) {
					childT = "?" + childT
				}
				if k, ok := defaultKind(protoArg.Default); ok && k == "null" && parentT != "" && !typeNullable(parentT) {
					parentT = "?" + parentT
				}
				st, un := c.typeSubtype3(parentT, childT, identity)
				record(st, un)
				if status == subNo {
					break
				}
				if childArg.ByRef != protoArg.ByRef {
					status = subNo
					break
				}
			}
		}
	}
	// Ковариантность возврата.
	if status != subNo {
		// Возврат по ссылке: потомок обязан сохранить `&` (обратное —
		// потомок может добавить `&`, когда родитель возвращает по значению).
		if pm.ByRefReturn && !m.ByRefReturn {
			status = subNo
		} else if pm.RetType != "" && m.RetType == "" {
			// Родитель объявил тип, потомок — нет: несовместимо.
			status = subNo
		} else if pm.RetType == "" {
			// Родитель без типа: потомок может добавить любой.
		} else {
			st, un := c.typeSubtype3(
				substRelativeAtoms(m.RetType, cd.Name, cd.Parent),
				substRelativeAtoms(pm.RetType, pd.Name, pd.Parent), identity)
			record(st, un)
		}
	}
	return status, unavail
}

// constDefsEqual сообщает, что определения константы совпадают (тип и значение) —
// тогда конфликт трейта и класса не является ошибкой.
func constDefsEqual(a, b *ClassMember) bool {
	if a.Final != b.Final {
		return false
	}
	if !strings.EqualFold(normVisStr(a.Vis), normVisStr(b.Vis)) {
		return false
	}
	if !strings.EqualFold(a.ConstType, b.ConstType) {
		return false
	}
	return constExprEqual(a.ConstVal, b.ConstVal)
}

func normVisStr(v string) string {
	if v == "" {
		return "public"
	}
	return strings.ToLower(v)
}

// propDefsEqual сообщает, что определения свойства из двух трейтов совпадают.
func propDefsEqual(a, b *ClassMember) bool {
	if !strings.EqualFold(a.PropType, b.PropType) || a.Static != b.Static ||
		a.Readonly != b.Readonly || !strings.EqualFold(a.Vis, b.Vis) {
		return false
	}
	return constExprEqual(a.Default, b.Default)
}

// declHasMethod сообщает, что объявление класса/трейта содержит метод.
func declHasMethod(cd *ClassDecl, name string) bool {
	if cd == nil {
		return false
	}
	for _, m := range cd.Members {
		if m.Kind == "method" && strings.EqualFold(m.MName, name) {
			return true
		}
	}
	return false
}

// classDeclaresMethod сообщает, что метод объявлен непосредственно в теле класса
// (а не примешан из трейта): такие методы имеют приоритет над трейтовыми.
func classDeclaresMethod(cd *ClassDecl, name string) bool {
	return declHasMethod(cd, name)
}

// declOrTraitHasMethod рекурсивно ищет метод в объявлении класса/трейта и во
// всех примешиваемых им трейтах (nested traits). seen защищает от циклов.
func declOrTraitHasMethod(decls []*ClassDecl, cd *ClassDecl, name string, seen map[string]bool) bool {
	if cd == nil {
		return false
	}
	key := strings.ToLower(cd.Name)
	if seen[key] {
		return false
	}
	seen[key] = true
	if declHasMethod(cd, name) {
		return true
	}
	for _, tn := range cd.Uses {
		if declOrTraitHasMethod(decls, declByName0(decls, tn), name, seen) {
			return true
		}
	}
	return false
}

// traitEffectiveMethodNames возвращает имена методов, фактически предоставляемых
// трейтом: собственные методы, затем (в порядке) методы примешиваемых трейтов,
// не перекрытые собственными. Порядок — как в methodOrder скомпонованного трейта.
func traitEffectiveMethodNames(decls []*ClassDecl, cd *ClassDecl, out *[]string, seen map[string]bool) []string {
	if cd == nil {
		return nil
	}
	if out == nil {
		out = &[]string{}
	}
	key := strings.ToLower(cd.Name)
	if seen[key] {
		return *out
	}
	seen[key] = true
	if out == nil {
		out = &[]string{}
	}
	add := func(name string) {
		for _, x := range *out {
			if strings.EqualFold(x, name) {
				return
			}
		}
		*out = append(*out, name)
	}
	for _, m := range cd.Members {
		if m.Kind == "method" {
			add(m.MName)
		}
	}
	for _, tn := range cd.Uses {
		nested := traitEffectiveMethodNames(decls, declByName0(decls, tn), nil, seen)
		for _, m := range nested {
			add(m)
		}
	}
	return *out
}

// usePos возвращает индекс трейта в списке use или -1.
func usePos(uses []string, name string) int {
	for i, u := range uses {
		if strings.EqualFold(u, name) {
			return i
		}
	}
	return -1
}

// traitProvidingMethod ищет используемый трейт (кроме exclude), предоставляющий
// конкретный метод name.
func traitProvidingMethod(decls []*ClassDecl, cd *ClassDecl, name, exclude string) string {
	for _, tn := range cd.Uses {
		if strings.EqualFold(tn, exclude) {
			continue
		}
		td := declByName0(decls, tn)
		if td == nil {
			continue
		}
		for _, mn := range traitEffectiveMethodNames(decls, td, nil, map[string]bool{}) {
			if strings.EqualFold(mn, name) {
				return tn
			}
		}
	}
	return ""
}

// resolveSelfTypes заменяет типы self/static/parent в сигнатуре трейта на
// фактический класс-потребитель (для сообщений о несовместимости).
func resolveSelfTypes(m *ClassMember, clsName, parentName string) {
	res := func(t string) string {
		switch strings.ToLower(t) {
		case "self", "static":
			return clsName
		case "parent":
			return parentName
		}
		return t
	}
	for i := range m.Params {
		m.Params[i].Type = res(m.Params[i].Type)
	}
	m.RetType = res(m.RetType)
}

// deferClassFatal превращает фатальную ошибку совместимости трейтового метода
// в отложенную (RuntimeFatal): PHP печатает её при выполнении объявления класса
// в main, после уже выведенного ранее текста. Для трейтов/интерфейсов ошибка
// остаётся compile-time (у них нет позиции в main).
func deferClassFatal(cd *ClassDecl, err error) bool {
	if cd == nil || cd.IsTrait || cd.IsInterface || cd.IsEnum {
		return false
	}
	fe, ok := err.(*fatalError)
	if !ok {
		return false
	}
	if fe.file != "" && cd.DefFile != "" && fe.file != cd.DefFile {
		// Диагностика привязана к другому файлу (метод из трейта в другом
		// файле): отложить её в точку объявления класса нельзя — потеряется
		// файл/строка (bug81192).
		return false
	}
	if cd.RuntimeFatal == "" {
		cd.RuntimeFatal = fe.msg
	}
	return true
}

// sigRef — ссылка на абстрактную сигнатуру трейта/интерфейса.
type sigRef struct {
	decl *ClassDecl
	mem  *ClassMember
}

// methodProvidedDecl возвращает объявление (класс или трейт), которое реально
// предоставляет метод name классу cd (собственный член или рекурсивно из трейта).
func methodProvidedDecl(cd *ClassDecl, name string, byName map[string]*ClassDecl) *ClassDecl {
	seen := map[string]bool{}
	var find func(c *ClassDecl) *ClassDecl
	find = func(c *ClassDecl) *ClassDecl {
		if c == nil {
			return nil
		}
		key := strings.ToLower(c.Name)
		if seen[key] {
			return nil
		}
		seen[key] = true
		if declHasMethod(c, name) {
			return c
		}
		for _, tn := range c.Uses {
			if d := find(byName[strings.ToLower(tn)]); d != nil {
				return d
			}
		}
		return nil
	}
	return find(cd)
}

// findParentClassMethod ищет метод name в цепочке классов-родителей.
func findParentClassMethod(cd *ClassDecl, name string, byName map[string]*ClassDecl) (*ClassDecl, *ClassMember) {
	seen := map[string]bool{}
	cur := cd
	for cur != nil && cur.Parent != "" {
		key := strings.ToLower(cur.Parent)
		if seen[key] {
			break
		}
		seen[key] = true
		p := byName[key]
		if p == nil {
			break
		}
		if m := findOwnMember(p, name, "method", "signature"); m != nil {
			return p, m
		}
		// Метод родителя, примешанный трейтом (Zend называет его по классу).
		if td := methodProvidedDecl(p, name, byName); td != nil {
			if m := findOwnMember(td, name, "method", "signature"); m != nil {
				return p, m
			}
		}
		cur = p
	}
	return nil, nil
}

// isCtorOrDtorName сообщает, что имя — __construct или __destruct.
func isCtorOrDtorName(name string) bool {
	return strings.EqualFold(name, "__construct") || strings.EqualFold(name, "__destruct")
}

// findAbstractAncestorMethod ищет ближайшее абстрактное объявление метода по
// родительской цепочке (прототип для конструкторов/деструкторов).
func findAbstractAncestorMethod(cd *ClassDecl, name string, byName map[string]*ClassDecl) (*ClassDecl, *ClassMember) {
	seen := map[string]bool{}
	cur := cd
	for cur != nil && cur.Parent != "" {
		key := strings.ToLower(cur.Parent)
		if seen[key] {
			break
		}
		seen[key] = true
		p := byName[key]
		if p == nil {
			break
		}
		if m := findOwnMember(p, name, "method", "signature"); m != nil && m.Abstract {
			return p, m
		}
		cur = p
	}
	return nil, nil
}

// findIfaceMethod ищет метод name среди интерфейсов класса и его родителей.
func findIfaceMethod(cd *ClassDecl, name string, byName map[string]*ClassDecl) (*ClassDecl, *ClassMember) {
	seen := map[string]bool{}
	var search func(c *ClassDecl) (*ClassDecl, *ClassMember)
	search = func(c *ClassDecl) (*ClassDecl, *ClassMember) {
		if c == nil {
			return nil, nil
		}
		for _, in := range c.Interfaces {
			i := byName[strings.ToLower(in)]
			if i == nil {
				continue
			}
			if seen[strings.ToLower(i.Name)] {
				continue
			}
			seen[strings.ToLower(i.Name)] = true
			if m := findOwnMember(i, name, "method", "signature"); m != nil {
				return i, m
			}
			if d, mm := search(i); mm != nil {
				return d, mm
			}
		}
		if c.Parent != "" {
			if p := byName[strings.ToLower(c.Parent)]; p != nil {
				if d, mm := search(p); mm != nil {
					return d, mm
				}
			}
		}
		return nil, nil
	}
	return search(cd)
}

// traitSignaturesFor собирает абстрактные сигнатуры метода name из всех
// используемых cd трейтов (рекурсивно), исключая объявление exclude.
func traitSignaturesFor(cd *ClassDecl, name string, byName map[string]*ClassDecl, exclude *ClassDecl) []sigRef {
	var out []sigRef
	seen := map[string]bool{}
	var walk func(c *ClassDecl)
	walk = func(c *ClassDecl) {
		if c == nil {
			return
		}
		key := strings.ToLower(c.Name)
		if seen[key] {
			return
		}
		seen[key] = true
		for _, tn := range c.Uses {
			if t := byName[strings.ToLower(tn)]; t != nil {
				walk(t)
			}
		}
		for _, m := range c.Members {
			if m.Kind == "signature" && strings.EqualFold(m.MName, name) {
				if exclude != nil && strings.EqualFold(c.Name, exclude.Name) {
					continue
				}
				out = append(out, sigRef{c, m})
			}
		}
	}
	for _, tn := range cd.Uses {
		if t := byName[strings.ToLower(tn)]; t != nil {
			walk(t)
		}
	}
	return out
}

// traitSignaturesAll собирает все абстрактные сигнатуры методов (Kind
// "signature") из трейтов, используемых cd (рекурсивно).
func traitSignaturesAll(cd *ClassDecl, byName map[string]*ClassDecl) []sigRef {
	var out []sigRef
	seen := map[string]bool{}
	var walk func(c *ClassDecl)
	walk = func(c *ClassDecl) {
		if c == nil {
			return
		}
		key := strings.ToLower(c.Name)
		if seen[key] {
			return
		}
		seen[key] = true
		for _, tn := range c.Uses {
			if t := byName[strings.ToLower(tn)]; t != nil {
				walk(t)
			}
		}
		for _, m := range c.Members {
			if m.Kind == "signature" {
				out = append(out, sigRef{c, m})
			}
		}
	}
	for _, tn := range cd.Uses {
		if t := byName[strings.ToLower(tn)]; t != nil {
			walk(t)
		}
	}
	return out
}

// declByName0 ищет объявление класса/трейта по имени (регистронезависимо).
func declByName0(decls []*ClassDecl, name string) *ClassDecl {
	for _, d := range decls {
		if strings.EqualFold(d.Name, name) {
			return d
		}
	}
	return nil
}

// constExprEqual — структурное сравнение константных выражений. Для неизвестных
// узлов считает значения совпадающими (осторожная стратегия: не выдавать ложный
// фатал на валидных программах).
func constExprEqual(a, b Expr) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	// Выражения, зависящие от констант, которые могут быть определены в
	// рантайме (define()), не сравниваются на этапе компиляции.
	if exprDependsOnRuntime(a) || exprDependsOnRuntime(b) {
		return true
	}
	switch x := a.(type) {
	case *IntLit:
		y, ok := b.(*IntLit)
		return ok && x.V == y.V
	case *FloatLit:
		y, ok := b.(*FloatLit)
		return ok && x.V == y.V
	case *StrLit:
		y, ok := b.(*StrLit)
		return ok && x.V == y.V
	case *BoolLit:
		y, ok := b.(*BoolLit)
		return ok && x.V == y.V
	case *NullLit:
		_, ok := b.(*NullLit)
		return ok
	case *ArrayLit:
		y, ok := b.(*ArrayLit)
		if !ok || len(x.Elems) != len(y.Elems) {
			return false
		}
		for i := range x.Elems {
			if x.Elems[i].ByRef != y.Elems[i].ByRef || x.Elems[i].Spread != y.Elems[i].Spread {
				return false
			}
			if !constExprEqual(x.Elems[i].Key, y.Elems[i].Key) {
				return false
			}
			if !constExprEqual(x.Elems[i].Val, y.Elems[i].Val) {
				return false
			}
		}
		return true
	}
	return true
}

// exprDependsOnRuntime сообщает, что константное выражение содержит обращение к
// глобальной константе (может быть определена в рантайме через define()).
func exprDependsOnRuntime(e Expr) bool {
	switch x := e.(type) {
	case nil:
		return false
	case *ConstExpr:
		return !strings.Contains(x.Name, "::")
	case *BinExpr:
		return exprDependsOnRuntime(x.L) || exprDependsOnRuntime(x.R)
	case *LogicalExpr:
		return exprDependsOnRuntime(x.L) || exprDependsOnRuntime(x.R)
	case *UnaryExpr:
		return exprDependsOnRuntime(x.X)
	case *CastExpr:
		return exprDependsOnRuntime(x.X)
	case *TernaryExpr:
		return exprDependsOnRuntime(x.Cond) || exprDependsOnRuntime(x.Then) || exprDependsOnRuntime(x.Else)
	case *CoalesceExpr:
		return exprDependsOnRuntime(x.L) || exprDependsOnRuntime(x.R)
	case *ArrayLit:
		for _, el := range x.Elems {
			if exprDependsOnRuntime(el.Key) || exprDependsOnRuntime(el.Val) {
				return true
			}
		}
	}
	return false
}

// visRank возвращает ранг видимости: private=0, protected=1, public=2.
func visRank(v string) int {
	switch strings.ToLower(v) {
	case "private":
		return 0
	case "protected":
		return 1
	}
	return 2
}

// checkPropCompatible проверяет инвариантность типа свойства относительно
// родителя.
func (c *compiler) checkPropCompatible(cd *ClassDecl, m *ClassMember, pd *ClassDecl, pm *ClassMember) error {
	if strings.EqualFold(pm.Vis, "private") {
		return nil
	}
	// Статичность свойства должна совпадать с родительским (проверяется
	// раньше readonly и видимости).
	if pm.Static != m.Static {
		if pm.Static {
			return c.newFatal(fmt.Sprintf("Cannot redeclare static %s::$%s as non static %s::$%s", pd.Name, m.PropName, cd.Name, m.PropName))
		}
		return c.newFatal(fmt.Sprintf("Cannot redeclare non static %s::$%s as static %s::$%s", pd.Name, m.PropName, cd.Name, m.PropName))
	}
	mReadonly := m.Readonly || cd.IsReadonly
	pmReadonly := pm.Readonly || pd.IsReadonly
	if mReadonly != pmReadonly {
		if pmReadonly {
			return c.newFatal(fmt.Sprintf("Cannot redeclare readonly property %s::$%s as non-readonly %s::$%s", pd.Name, m.PropName, cd.Name, m.PropName))
		}
		return c.newFatal(fmt.Sprintf("Cannot redeclare non-readonly property %s::$%s as readonly %s::$%s", pd.Name, m.PropName, cd.Name, m.PropName))
	}
	// Понижение видимости унаследованного свойства недопустимо.
	mVis := m.Vis
	if mVis == "" {
		mVis = "public"
	}
	pv := strings.ToLower(pm.Vis)
	if pv == "" {
		pv = "public"
	}
	mv := strings.ToLower(mVis)
	if visRank(mv) < visRank(pv) {
		if pv == "protected" {
			return c.newFatal(fmt.Sprintf("Access level to %s::$%s must be protected (as in class %s) or weaker", cd.Name, m.PropName, pd.Name))
		}
		return c.newFatal(fmt.Sprintf("Access level to %s::$%s must be %s (as in class %s)", cd.Name, m.PropName, pv, pd.Name))
	}
	if m.PropType == pm.PropType {
		return nil
	}
	// Инвариантность: типы эквивалентны, если являются подтипами друг друга
	// (учитывает коммутативность пересечений и их редукцию). Пустой тип и
	// `mixed` несводимы друг к другу — обрабатываются отдельными сообщениями.
	if m.PropType != "" && pm.PropType != "" {
		resolve := func(t string) string {
			switch strings.ToLower(t) {
			case "self", "static":
				return cd.Name
			case "parent":
				return cd.Parent
			}
			return t
		}
		if c.typeSubtype(m.PropType, pm.PropType, resolve) && c.typeSubtype(pm.PropType, m.PropType, resolve) {
			return nil
		}
	}
	if pm.PropType == "" {
		return c.newFatal(fmt.Sprintf("Type of %s::$%s must not be defined (as in class %s)", cd.Name, m.PropName, pd.Name))
	}
	return c.newFatal(fmt.Sprintf("Type of %s::$%s must be %s (as in class %s)", cd.Name, m.PropName, displayType(pm.PropType), pd.Name))
}

// enumForbiddenMagic — магические методы, запрещённые в enum.
var enumForbiddenMagic = map[string]bool{
	"__construct": true, "__destruct": true, "__clone": true,
	"__get": true, "__set": true, "__isset": true, "__unset": true,
	"__sleep": true, "__wakeup": true, "__set_state": true,
	"__serialize": true, "__unserialize": true,
	"__debuginfo": true, "__tostring": true,
}

// reservedTypeNames — имена, зарезервированные под типы (нельзя объявлять
// класс/интерфейс/трейт).
var reservedTypeNames = map[string]bool{
	"int": true, "float": true, "bool": true, "string": true,
	"true": true, "false": true, "null": true, "void": true,
	"iterable": true, "object": true, "mixed": true, "never": true,
	"array": true, "callable": true, "self": true, "parent": true, "static": true,
}

// reservedClassName проверяет, не является ли короткое имя класса
// зарезервированным под тип.
func (c *compiler) reservedClassName(name string) error {
	short := name
	if i := strings.LastIndex(short, "\\"); i >= 0 {
		short = short[i+1:]
	}
	if reservedTypeNames[strings.ToLower(short)] {
		return c.newFatal(fmt.Sprintf("Cannot use '%s' as class name as it is reserved", short))
	}
	return nil
}

// reservedClassRefFatal — Zend запрещает self/parent/static в роли родителя,
// интерфейса и трейта (fatal на строке объявления).
func reservedClassRefFatal(cd *ClassDecl) error {
	bad := func(n string) bool {
		switch strings.ToLower(n) {
		case "self", "parent", "static":
			return true
		}
		return false
	}
	if bad(cd.Parent) {
		return &fatalError{msg: fmt.Sprintf("Cannot use '%s' as class name, as it is reserved", cd.Parent), line: cd.Line}
	}
	for _, n := range cd.Interfaces {
		if bad(n) {
			return &fatalError{msg: fmt.Sprintf("Cannot use '%s' as interface name, as it is reserved", n), line: cd.Line}
		}
	}
	traitFatal := func(n string) error {
		line := cd.UsesLine
		if line == 0 {
			line = cd.Line
		}
		return &fatalError{msg: fmt.Sprintf("Cannot use '%s' as trait name, as it is reserved", n), line: line}
	}
	for _, n := range cd.Uses {
		if bad(n) {
			return traitFatal(n)
		}
	}
	for _, ta := range cd.TraitAdapt {
		if bad(ta.Trait) {
			return traitFatal(ta.Trait)
		}
		for _, n := range ta.Insteadof {
			if bad(n) {
				return traitFatal(n)
			}
		}
	}
	return nil
}

// validateType проверяет синтаксическую корректность объявленного типа
// (declare-time fatal, как в Zend).
func (c *compiler) validateType(t string) error {
	if t == "" {
		return nil
	}
	if c.noParentScope && typeHasAtom(t, "parent") {
		return c.newFatal(`Cannot use "parent" when current class scope has no parent`)
	}
	if err := c.checkReservedRelativeTypeName(t); err != nil {
		return err
	}
	if isWrappedParens(t) {
		return c.validateType(t[1 : len(t)-1])
	}
	if strings.HasPrefix(t, "?") {
		inner := t[1:]
		switch inner {
		case "mixed":
			return c.newFatal("Type mixed cannot be marked as nullable since mixed already includes null")
		case "void":
			return c.newFatal("Void can only be used as a standalone type")
		case "never":
			return c.newFatal("never can only be used as a standalone type")
		case "null":
			return c.newFatal("null cannot be marked as nullable")
		}
		return c.validateType(inner)
	}
	alts := splitTopLevelAll(t, '|')
	if len(alts) > 1 {
		for _, a := range alts {
			switch a {
			case "mixed":
				return c.newFatal("Type mixed can only be used as a standalone type")
			case "void":
				return c.newFatal("Void can only be used as a standalone type")
			case "never":
				return c.newFatal("never can only be used as a standalone type")
			}
		}
		hasTrue, hasFalse, hasBool := false, false, false
		for _, a := range alts {
			switch strings.ToLower(a) {
			case "true":
				hasTrue = true
			case "false":
				hasFalse = true
			case "bool":
				hasBool = true
			}
		}
		if hasTrue && hasFalse {
			return c.newFatal("Type contains both true and false, bool should be used instead")
		}
		if hasBool && hasTrue {
			return c.newFatal("Duplicate type true is redundant")
		}
		if hasBool && hasFalse {
			return c.newFatal("Duplicate type false is redundant")
		}
		seenAlt := map[string]bool{}
		for _, a := range alts {
			if topLevelSplitPHP(a, '&') >= 0 || isWrappedParens(a) {
				continue
			}
			for _, e := range expandIterableAlt(a) {
				key := strings.ToLower(e)
				if seenAlt[key] {
					return c.newFatal("Duplicate type " + typeDupName(e) + " is redundant")
				}
				seenAlt[key] = true
			}
		}
		// Идентичные варианты DNF (после раскрытия use-алиасов).
		seenDNF := map[string]string{}
		for _, a := range alts {
			key := dnfAltKey(a)
			if key == "" {
				continue
			}
			if prev, ok := seenDNF[key]; ok {
				return c.newFatal("Type " + displayType(a) + " is redundant with type " + displayType(prev))
			}
			seenDNF[key] = a
		}
		if _, ok := unionObjectClassRedundant(alts); ok {
			return c.newFatal("Type " + displayType(t) + " contains both object and a class type, which is redundant")
		}
		if mor, less, ok := unionDNFRestrictive(alts); ok {
			return c.newFatal("Type " + mor + " is redundant as it is more restrictive than type " + less)
		}
		for _, a := range alts {
			if err := c.validateType(a); err != nil {
				return err
			}
		}
		return nil
	}
	// пересечение на верхнем уровне
	if topLevelSplitPHP(t, '&') >= 0 {
		seenIx := map[string]bool{}
		for _, a := range splitTopLevelAll(t, '&') {
			if seenIx[strings.ToLower(a)] {
				return c.newFatal("Duplicate type " + a + " is redundant")
			}
			seenIx[strings.ToLower(a)] = true
		}
		for _, a := range splitTopLevelAll(t, '&') {
			switch a {
			case "mixed":
				return c.newFatal("Type mixed cannot be part of an intersection type")
			case "void":
				return c.newFatal("Type void cannot be part of an intersection type")
			case "never":
				return c.newFatal("Type never cannot be part of an intersection type")
			case "true":
				return c.newFatal("Type true cannot be part of an intersection type")
			case "false":
				return c.newFatal("Type false cannot be part of an intersection type")
			case "null":
				return c.newFatal("Type null cannot be part of an intersection type")
			case "self":
				return c.newFatal("Type self cannot be part of an intersection type")
			case "static":
				return c.newFatal("Type static cannot be part of an intersection type")
			case "parent":
				return c.newFatal("Type parent cannot be part of an intersection type")
			case "int", "float", "string", "bool", "array", "object", "callable":
				return c.newFatal("Type " + a + " cannot be part of an intersection type")
			case "iterable":
				return c.newFatal("Type Traversable|array cannot be part of an intersection type")
			}
		}
	}
	return nil
}

// checkReservedRelativeTypeName отклоняет относительные имена типов, последний
// сегмент которых зарезервирован под скалярный тип (например `bar\int`).
func (c *compiler) checkReservedRelativeTypeName(t string) error {
	for _, alt := range splitTopLevelAll(t, '|') {
		body := strings.TrimSpace(alt)
		if strings.HasPrefix(body, "(") && strings.HasSuffix(body, ")") {
			body = body[1 : len(body)-1]
		}
		for _, a := range splitTopLevelAll(body, '&') {
			a = strings.TrimSpace(a)
			if !strings.Contains(a, "\\") {
				continue
			}
			seg := a[strings.LastIndex(a, "\\")+1:]
			if reservedTypeNames[strings.ToLower(seg)] {
				return c.newFatal("Cannot use '" + a + "' as class name as it is reserved")
			}
		}
	}
	return nil
}

// typeHasAtom сообщает, содержит ли тип атом с указанным именем (без учёта
// скобок/union/пересечений).
// substRelativeAtoms заменяет относительные имена типов (self/static/parent)
// на конкретные имена классов в области объявления self/parent.
func substRelativeAtoms(t, self, parent string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return t
	}
	if strings.HasPrefix(t, "?") {
		return "?" + substRelativeAtoms(t[1:], self, parent)
	}
	if topLevelSplitPHP(t, '|') >= 0 {
		parts := splitTopLevelAll(t, '|')
		for i, p := range parts {
			parts[i] = substRelativeAtoms(p, self, parent)
		}
		return strings.Join(parts, "|")
	}
	if topLevelSplitPHP(t, '&') >= 0 {
		parts := splitTopLevelAll(t, '&')
		for i, p := range parts {
			parts[i] = substRelativeAtoms(p, self, parent)
		}
		return strings.Join(parts, "&")
	}
	if isWrappedParens(t) {
		return "(" + substRelativeAtoms(t[1:len(t)-1], self, parent) + ")"
	}
	switch strings.ToLower(t) {
	case "self":
		return self
	case "static":
		// static остаётся позднесвязанным типом: он не равен self при
		// проверке ковариантности (см. atomSubtype3).
		return "static"
	case "parent":
		return parent
	}
	return t
}

func typeHasAtom(t, atom string) bool {
	for _, alt := range splitTopLevelAll(t, '|') {
		body := strings.TrimSpace(alt)
		if strings.HasPrefix(body, "(") && strings.HasSuffix(body, ")") {
			body = body[1 : len(body)-1]
		}
		for _, a := range splitTopLevelAll(body, '&') {
			if strings.EqualFold(strings.TrimSpace(a), atom) {
				return true
			}
		}
	}
	return false
}

// forbiddenPropType сообщает, что тип недопустим для свойства
// (callable/void/never).
func forbiddenPropType(t string) bool {
	t = strings.TrimSpace(strings.TrimPrefix(t, "?"))
	switch strings.ToLower(t) {
	case "callable", "void", "never":
		return true
	}
	return false
}

// forbiddenConstType возвращает имя типа, который нельзя использовать в
// типизированной константе класса (callable/void/never).
func forbiddenConstType(t string) string {
	t = strings.TrimSpace(t)
	t = strings.TrimPrefix(t, "?")
	switch strings.ToLower(t) {
	case "callable":
		return "callable"
	case "void":
		return "void"
	case "never":
		return "never"
	}
	return ""
}

// checkMagicMethod проверяет объявленный тип возврата магических методов
// (declare-time fatal, как в Zend).
// magicMustBePublic — магические методы, которые Zend требует объявлять public
// (иначе выдаёт Warning при объявлении класса).
var magicMustBePublic = map[string]bool{
	"__call": true, "__callstatic": true, "__get": true, "__set": true,
	"__isset": true, "__unset": true, "__tostring": true, "__invoke": true,
	"__debuginfo": true, "__serialize": true, "__unserialize": true,
	"__sleep": true, "__wakeup": true,
}

// magicMethodNames — магические методы (в нижнем регистре), к которым Zend
// применяет правила статичности/передачи по ссылке.
var magicMethodNames = map[string]bool{
	"__construct": true, "__destruct": true, "__call": true, "__callstatic": true,
	"__get": true, "__set": true, "__isset": true, "__unset": true,
	"__sleep": true, "__wakeup": true, "__serialize": true, "__unserialize": true,
	"__tostring": true, "__invoke": true, "__set_state": true, "__clone": true,
	"__debuginfo": true,
}

func (c *compiler) checkMagicMethod(cls string, m *ClassMember) error { // Проверка арности/типов магических методов на этапе объявления.
	n := len(m.Params)
	lc := strings.ToLower(m.MName)
	isMagic := magicMethodNames[lc]
	exact := func(want int) error {
		if n == want {
			return nil
		}
		plural := "arguments"
		if want == 1 {
			plural = "argument"
		}
		return c.newFatal(fmt.Sprintf("Method %s::%s() must take exactly %d %s", cls, m.MName, want, plural))
	}
	noArgs := func() error {
		if n == 0 {
			return nil
		}
		return c.newFatal(fmt.Sprintf("Method %s::%s() cannot take arguments", cls, m.MName))
	}
	typeCheck := func(idx int, want string) error {
		if idx >= len(m.Params) {
			return nil
		}
		pt := normalizedType(m.Params[idx].Type)
		if pt == "" || pt == "mixed" || typeIncludes(pt, want) {
			return nil
		}
		pname := strings.TrimPrefix(m.Params[idx].Name, "$")
		return c.newFatal(fmt.Sprintf("%s::%s(): Parameter #%d ($%s) must be of type %s when declared",
			cls, m.MName, idx+1, pname, want))
	}
	switch lc {
	case "__call", "__callstatic":
		if err := exact(2); err != nil {
			return err
		}
		if err := typeCheck(0, "string"); err != nil {
			return err
		}
		if err := typeCheck(1, "array"); err != nil {
			return err
		}
	case "__set":
		if err := exact(2); err != nil {
			return err
		}
		if err := typeCheck(0, "string"); err != nil {
			return err
		}
	case "__get", "__isset", "__unset":
		if err := exact(1); err != nil {
			return err
		}
		if err := typeCheck(0, "string"); err != nil {
			return err
		}
	case "__set_state", "__unserialize":
		if err := exact(1); err != nil {
			return err
		}
		if err := typeCheck(0, "array"); err != nil {
			return err
		}
	case "__tostring", "__clone", "__destruct", "__wakeup", "__serialize", "__sleep", "__debuginfo":
		if err := noArgs(); err != nil {
			return err
		}
	}
	// Параметры по ссылке запрещены (кроме __construct/__invoke).
	if isMagic && lc != "__construct" && lc != "__invoke" {
		for _, p := range m.Params {
			if p.ByRef {
				return c.newFatal(fmt.Sprintf("Method %s::%s() cannot take arguments by reference", cls, m.MName))
			}
		}
	}
	// Статичность: все магические методы, кроме __callStatic и __set_state,
	// не могут быть статическими; __set_state обязан быть статическим.
	if isMagic {
		if lc == "__set_state" {
			if !m.Static {
				return c.newFatal(fmt.Sprintf("Method %s::%s() must be static", cls, m.MName))
			}
		} else if lc == "__callstatic" {
			if !m.Static {
				return c.newFatal(fmt.Sprintf("Method %s::%s() must be static", cls, m.MName))
			}
		} else if m.Static {
			return c.newFatal(fmt.Sprintf("Method %s::%s() cannot be static", cls, m.MName))
		}
	}
	rt := normalizedType(m.RetType)
	switch lc {
	case "__construct", "__destruct":
		if rt != "" {
			return c.newFatal(fmt.Sprintf("Method %s::%s() cannot declare a return type", cls, m.MName))
		}
	case "__clone", "__set", "__unset", "__wakeup", "__unserialize":
		if rt != "" && rt != "void" {
			return c.newFatal(fmt.Sprintf("%s::%s(): Return type must be void when declared", cls, m.MName))
		}
	case "__isset":
		if rt != "" && rt != "bool" {
			return c.newFatal(fmt.Sprintf("%s::%s(): Return type must be bool when declared", cls, m.MName))
		}
	case "__tostring":
		if rt != "" && rt != "string" && rt != "never" {
			return c.newFatal(fmt.Sprintf("%s::%s(): Return type must be string when declared", cls, m.MName))
		}
	case "__debuginfo":
		if rt != "" && !isArrayOrNullableArrayType(rt) {
			return c.newFatal(fmt.Sprintf("%s::%s(): Return type must be ?array when declared", cls, m.MName))
		}
	case "__serialize", "__sleep":
		if rt != "" && rt != "array" {
			return c.newFatal(fmt.Sprintf("%s::%s(): Return type must be array when declared", cls, m.MName))
		}
	case "__set_state":
		// Допустимы object, self/static/parent и имена классов (Zend требует
		// лишь нескалярный объектный тип).
		if rt != "" && !typeIsObjectLike(rt) {
			return c.newFatal(fmt.Sprintf("%s::%s(): Return type must be object when declared", cls, m.MName))
		}
	}
	return nil
}

// typeIsObjectLike сообщает, что объявленный тип — объектный (object, self,
// static, parent, имя класса или union из них).
func typeIsObjectLike(rt string) bool {
	t := strings.TrimPrefix(rt, "?")
	for _, alt := range splitTopLevelAllLocal(t, '|') {
		a := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(alt, "?")))
		switch a {
		case "int", "float", "string", "bool", "array", "callable", "iterable",
			"void", "never", "null", "false", "true", "mixed":
			continue
		}
		return true
	}
	return false
}

// typeIncludes сообщает, что объявленный тип (возможно union/DNF/nullable)
// включает требуемый тип (Zend требует совместимости параметра магического
// метода: например, int|string включает string, iterable включает array).
func typeIncludes(decl, want string) bool {
	t := strings.TrimPrefix(normalizedType(decl), "?")
	for _, alt := range splitTopLevelAllLocal(t, '|') {
		alt = strings.TrimPrefix(strings.TrimSpace(alt), "?")
		if alt == want || alt == "mixed" {
			return true
		}
		// iterable включает array; object включает array? нет.
		if want == "array" && alt == "iterable" {
			return true
		}
	}
	return false
}

// splitTopLevelAllLocal разбивает тип по разделителю верхнего уровня
// (без учёта вложенности DNF-скобок).
func splitTopLevelAllLocal(s string, sep byte) []string {
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		case sep:
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// visOf переводит текстовую видимость из парсера в vm.Visibility.
func visOf(s string) vm.Visibility {
	switch s {
	case "private":
		return vm.Private
	case "protected":
		return vm.Protected
	default:
		return vm.Public
	}
}

// compileFuncBody компилирует тело функции/метода. name используется для
// уникальности ключей static-переменных.
func (c *compiler) compileFuncBody(name string, params []Param, body []Stmt, byrefReturn bool, retType string, line int) (*vm.CompiledFunction, error) {
	return c.compileBody(name, params, nil, body, byrefReturn, retType, line)
}

// compileBody компилирует тело функции. captures — захваченные замыканием
// переменные (use / неявные для стрелочных функций): их регистры идут сразу
// за регистрами параметров, а значения VM кладёт при вызове замыкания.
func (c *compiler) compileBody(name string, params []Param, captures []Param, body []Stmt, byrefReturn bool, retType string, line int) (*vm.CompiledFunction, error) {
	if err := checkFinallyJumps(body); err != nil {
		return nil, err
	}
	cc := newCompiler(c.funcs, c.idx)
	cc.file = c.file
	// Текущая строка в начале тела — строка объявления функции: синтезированные
	// операторы (return стрелочной функции, неявный возврат) ссылаются на неё,
	// как Zend.
	cc.curLine = line
	cc.classes = c.classes
	cc.classIdx = c.classIdx
	cc.curClass = c.curClass
	cc.noParentScope = c.noParentScope
	cc.methodRefs = c.methodRefs
	cc.closureIdx = c.closureIdx
	cc.anonNames = c.anonNames
	cc.declIdx = c.declIdx
	cc.classAlias = c.classAlias
	cc.stmtLines = c.stmtLines
	cc.exprLines = c.exprLines
	cc.byrefReturn = byrefReturn
	cc.closureBody = c.closureBody
	cc.strict = c.strict
	cc.ticks = c.ticks
	cc.tickCount = 0
	cc.chunk.Strict = c.strict
	cc.chunk.SetEmitStrict(c.strict)
	cc.curRetType = retType
	seenParamNames := map[string]bool{}
	for i, p := range params {
		pn := strings.TrimPrefix(p.Name, "$")
		if seenParamNames[pn] {
			c.curLine = line
			return nil, c.newFatal("Redefinition of parameter $" + pn)
		}
		seenParamNames[pn] = true
		if strings.TrimPrefix(p.Name, "$") == "this" {
			return nil, c.newFatal("Cannot use $this as parameter")
		}
		if p.Variadic && i != len(params)-1 {
			return nil, c.newFatal("Only the last parameter can be variadic")
		}
		if p.Variadic && p.Default != nil {
			return nil, c.newFatal("Variadic parameter cannot have a default value")
		}
		if err := c.validateType(p.Type); err != nil {
			return nil, err
		}
		if typeHasAtom(p.Type, "static") {
			return nil, c.newFatal("Cannot use the static modifier on a parameter")
		}
		// Замыкания могут получить scope позже (bindTo) — self/parent в их
		// сигнатуре допустимы.
		if c.curClass == nil && !c.closureBody {
			if typeHasAtom(p.Type, "self") {
				return nil, c.newFatal(`Cannot use "self" when no class scope is active`)
			}
			if typeHasAtom(p.Type, "parent") {
				return nil, c.newFatal(`Cannot use "parent" when no class scope is active`)
			}
		}
		switch normalizedType(p.Type) {
		case "void":
			return nil, c.newFatal("void cannot be used as a parameter type")
		case "never":
			return nil, c.newFatal("never cannot be used as a parameter type")
		}
		if p.Default != nil && containsStaticClassKeyword(p.Default) {
			return nil, c.newFatal("static::class cannot be used for compile-time class name resolution")
		}
		if p.Default != nil && p.Type != "" {
			if k, ok := defaultKind(p.Default); ok && k != "null" && !typeAllowsDefault(p.Type, k) {
				return nil, c.newFatal(fmt.Sprintf("Cannot use %s as default value for parameter $%s of type %s",
					k, strings.TrimPrefix(p.Name, "$"), defaultTypeDisplay(p.Type)))
			}
		}
	}
	// Необязательный параметр перед обязательным: PHP 8.3 печатает Deprecated
	// для каждого необязательного параметра, стоящего до ПОСЛЕДНЕГО
	// обязательного (вариативный обязательным не считается). Параметр с типом,
	// не допускающим null, и дефолтом ровно null необязательным не считается.
	if len(params) > 0 {
		lastRequired := -1
		for i, p := range params {
			if p.Default == nil && !p.Variadic {
				lastRequired = i
			}
		}
		if lastRequired > 0 {
			reqName := strings.TrimPrefix(params[lastRequired].Name, "$")
			for i := 0; i < lastRequired; i++ {
				if params[i].Default != nil && !paramImplicitlyNullable(params[i]) {
					compilerDeprecations = append(compilerDeprecations, fmt.Sprintf(
						"Optional parameter $%s declared before required parameter $%s is implicitly treated as a required parameter in %s on line %d",
						strings.TrimPrefix(params[i].Name, "$"), reqName, c.file, line))
				}
			}
		}
	}
	// Возврат по ссылке из void-функции: Deprecated на объявлении (Zend
	// печатает его как startup-диагностику компиляции).
	if byrefReturn && normalizedType(retType) == "void" {
		compilerDeprecations = append(compilerDeprecations, fmt.Sprintf(
			"Returning by reference from a void function is deprecated in %s on line %d", c.file, line))
	}
	for i, p := range params {
		cc.vars[p.Name] = uint8(i)
		if p.ByRef {
			cc.refs[p.Name] = uint8(i)
		}
	}
	nparams := len(params)
	for i, u := range captures {
		reg := uint8(nparams + i)
		cc.vars[u.Name] = reg
		// По ссылке — общая Ref-ячейка; по значению — обычный слот-копия,
		// которая обновляется из замыкания при каждом вызове (не переживает
		// вызов, как в PHP).
		if u.ByRef {
			cc.refs[u.Name] = reg
		}
	}
	cc.nvars = uint8(nparams + len(captures))
	cc.nvars0 = cc.nvars
	cc.tmp = cc.nvars
	cc.max = cc.nvars
	// `global ${expr}` / `global $$x`: имя привязки неизвестно до выполнения —
	// все переменные тела (кроме параметров и захватов) идут через глобалы.
	if hasDynamicGlobal(body) {
		cc.allVarsGlobal = true
		cc.localNames = map[string]bool{"$this": true, "this": true}
		for _, p := range params {
			cc.localNames[p.Name] = true
		}
		for _, p := range captures {
			cc.localNames[p.Name] = true
		}
		for _, sv := range collectStatics(body) {
			cc.localNames[sv.Name] = true
		}
	}
	// Функция/замыкание с параметрами или захватами принимает значения в
	// регистры кадра (в т.ч. объекты/массивы): периодические точки времени
	// жизни нужны даже если тело состоит из скалярных операций.
	if cc.nvars > 0 {
		cc.chunk.SetMayTrack()
	}
	cc.funcName = name
	for _, g := range collectGlobals(body) {
		cc.globals[g] = true
	}
	statics := collectStatics(body)
	{
		seen := map[string]bool{}
		for _, sv := range statics {
			if seen[sv.Name] {
				cc.chunk.Emit(vm.Instruction{Op: vm.OpFatal,
					C: int32(cc.chunk.AddConst(vm.StrConst("Duplicate declaration of static variable " + sv.Name)))})
			}
			seen[sv.Name] = true
			if sv.Conditional {
				// Ленивое связывание: до выполнения объявления это обычная
				// локальная переменная; инициализируем в точке объявления.
				if cc.localStatics == nil {
					cc.localStatics = map[string]bool{}
				}
				cc.localStatics[sv.Name] = true
				continue
			}
			cc.staticKeys[sv.Name] = name + "\x00" + sv.Name
		}
	}
	// pre-проход: назначить регистры всем локальным переменным, пока нет врем.
	cc.writes = map[string]bool{}
	for i := range params {
		cc.writes[params[i].Name] = true
	}
	for i := range captures {
		cc.writes[captures[i].Name] = true
	}
	cc.prescan(body)
	// Пролог: переменные с чтением до записи стартуют как UNDEF (флаг true);
	// запись опускает флаг (clearUndef), чтение при флаге печатает
	// «Undefined variable» и сбрасывает флаг.
	for _, reg := range cc.undefInit {
		cc.chunk.Emit(vm.Instruction{Op: vm.OpTrue, A: reg})
	}
	if len(cc.defRegs) > 0 {
		dr := make(map[string]int, len(cc.defRegs))
		for n, r := range cc.defRegs {
			dr[n] = int(r)
			if t := strings.TrimPrefix(n, "$"); t != n {
				dr[t] = int(r)
			}
		}
		cc.chunk.SetDefRegs(dr)
	}
	for _, sv := range statics {
		if sv.Conditional {
			continue
		}
		key := cc.staticKeys[sv.Name]
		iss := cc.alloc()
		cc.chunk.Emit(vm.Instruction{Op: vm.OpGlobalIsset, A: iss, C: int32(cc.chunk.AddConst(vm.StrConst(key)))})
		jf := cc.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfTrue, A: iss})
		if sv.Default != nil {
			r, err := cc.expr(sv.Default)
			if err != nil {
				return nil, err
			}
			cc.chunk.Emit(vm.Instruction{Op: vm.OpGlobalSet, A: r, C: int32(cc.chunk.AddConst(vm.StrConst(key)))})
		} else {
			n := cc.alloc()
			cc.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: n})
			cc.chunk.Emit(vm.Instruction{Op: vm.OpGlobalSet, A: n, C: int32(cc.chunk.AddConst(vm.StrConst(key)))})
		}
		cc.setJump(jf, cc.chunk.Len())
	}
	for i, p := range params {
		if p.Default == nil {
			continue
		}
		cmp := cc.alloc()
		var cur uint8
		if p.ByRef {
			cur = cc.alloc()
			cc.chunk.Emit(vm.Instruction{Op: vm.OpRefGet, A: cur, B: uint8(i)})
		} else {
			cur = uint8(i)
		}
		emitNullIdent(cc, cmp, cur)
		jf := cc.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfFalse, A: cmp})
		if lit, ok := p.Default.(*IntLit); ok && defaultCoercesToFloat(p.Type) {
			p.Default = &FloatLit{V: float64(lit.V)}
		}
		// Значение по умолчанию вычисляется как константное выражение
		// (zend_ast_evaluate): чтение свойств не-enum объектов запрещено.
		cc.constEval++
		r, err := cc.expr(p.Default)
		cc.constEval--
		if err != nil {
			return nil, err
		}
		// Литеральные дефолты проверены на этапе компиляции; значения
		// констант/константных выражений Zend проверяет при вызове (ошибка
		// аргумента с локацией вызова: scalar_constant_defaults).
		if p.Type != "" {
			if _, isLit := defaultKind(p.Default); !isLit {
				n := cc.alloc()
				cc.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: n,
					C: int32(cc.chunk.AddConst(vm.IntConst(int64(i + 1))))})
				r = cc.emitCall("__php_default_check", []uint8{r, n})
			}
		}
		cc.storeVar(p.Name, r)
		cc.setJump(jf, cc.chunk.Len())
	}
	if err := cc.stmts(body); err != nil {
		return nil, err
	}
	if err := cc.finalizeGotos(); err != nil {
		return nil, err
	}
	r := cc.alloc()
	if byrefReturn && !cc.hasYield {
		// Неявный возврат NULL из by-ref функции: Notice как у `return;`
		// (Zend печатает его в момент выхода, строка — конец тела).
		cc.emitLine(line)
		cc.emitRefNotice("Only variable references should be returned by reference")
	}
	cc.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: r})
	cc.chunk.Emit(vm.Instruction{Op: vm.OpRet, A: r, C: 1})
	{
		mask := make([]bool, len(params))
		for i := range params {
			mask[i] = params[i].ByRef
		}
		cc.chunk.SetByRefArgs(mask)
	}
	// Функция читает аргументы через func_get_arg(s)/func_num_args() — SSA-аллокатор
	// должен сохранить регистры параметров (см. usesFuncArg).
	if cc.usesFuncArg {
		cc.chunk.SetKeepParamsLive(true)
	}
	pinned := int(cc.nvars0)
	if cc.usesSymtab {
		cc.buildSymtab(true)
	}
	// Регистры именованных переменных функции закрепляются: Zend держит
	// переменную до перезаписи/unset или конца области, а SSA-переиспользование
	// её регистра временным значением освобождало бы объект раньше срока
	// (например, локальная переменная файбера при suspend — gh9735). Карта
	// имя→регистр остаётся валидной и без usesSymtab.
	cc.chunk.SetKeepParamsLive(true)
	pinned = int(cc.nvars)
	// Число привязываемых позиций (параметры + захваты) известно до
	// SSA-раскраски: аллокатор использует его, чтобы не заносить параметры в
	// список зануляемых на входе регистров.
	cc.chunk.SetNumParams(len(params) + len(captures))
	cc.chunk.Optimize()
	cc.setVarRegs()
	cc.chunk.AllocateRegisters(pinned, cc.allocEnv())
	cf := vm.NewCompiledFunction(cc.chunk)
	if cc.usesThisVar {
		cf.SetUsesThisVar()
	}
	cf.SetNumParams(len(params))
	if fixed, ok := variadicFixed(params); ok {
		cf.SetVariadic(fixed)
	}
	if cc.hasYield {
		if nrt := normalizedType(retType); nrt != "" && !generatorRetAllowed(nrt) {
			return nil, cc.newFatal("Generator return type must be a supertype of Generator, " + displayType(retType) + " given")
		}
		cf.SetGenerator()
	} else if cc.pendingRetFatal != nil {
		return nil, cc.pendingRetFatal
	}
	if normalizedType(retType) == "never" && !cc.hasYield {
		if st := findReturnStmt(body); st != nil {
			line := cc.stmtLineOf(st)
			return nil, &fatalError{msg: "A never-returning function must not return", line: line}
		}
	}
	return cf, nil
}

// generatorRetAllowed сообщает, что объявленный тип возврата генератора —
// супертип Generator (Zend: Generator, Iterator, Traversable, object, mixed,
// iterable или их объединение).
func generatorRetAllowed(t string) bool {
	for _, alt := range strings.Split(t, "|") {
		alt = strings.TrimPrefix(strings.TrimSpace(alt), "?")
		alt = strings.TrimLeft(alt, "\\")
		switch strings.ToLower(alt) {
		case "generator", "iterator", "traversable", "object", "mixed", "iterable":
			return true
		}
	}
	return false
}

// findReturnStmt ищет `return` в теле функции (не заходя во вложенные
// функции/замыкания) — проверка never на этапе компиляции.
func findReturnStmt(stmts []Stmt) Stmt {
	for _, st := range stmts {
		switch x := st.(type) {
		case *ReturnStmt:
			return st
		case *BlockStmt:
			if s := findReturnStmt(x.Body); s != nil {
				return s
			}
		case *IfStmt:
			if s := findReturnStmt(x.Then); s != nil {
				return s
			}
			if s := findReturnStmt(x.Else); s != nil {
				return s
			}
		case *WhileStmt:
			if s := findReturnStmt(x.Body); s != nil {
				return s
			}
		case *DoWhileStmt:
			if s := findReturnStmt(x.Body); s != nil {
				return s
			}
		case *ForStmt:
			if s := findReturnStmt(x.Body); s != nil {
				return s
			}
		case *ForeachStmt:
			if s := findReturnStmt(x.Body); s != nil {
				return s
			}
		case *SwitchStmt:
			for _, cs := range x.Cases {
				if s := findReturnStmt(cs.Body); s != nil {
					return s
				}
			}
		case *TryStmt:
			if s := findReturnStmt(x.Body); s != nil {
				return s
			}
			for _, cc2 := range x.Catches {
				if s := findReturnStmt(cc2.Body); s != nil {
					return s
				}
			}
			if s := findReturnStmt(x.Finally); s != nil {
				return s
			}
		}
	}
	return nil
}

// stmtLineOf возвращает строку инструкции (для позиций фаталов).
func (c *compiler) stmtLineOf(st Stmt) int {
	if x, ok := st.(*ReturnStmt); ok && x.X != nil {
		if l := c.exprLine(x.X); l > 0 {
			return l
		}
	}
	if c.curLine > 0 {
		return c.curLine
	}
	return 1
}

// compileClosureBody компилирует тело анонимной/стрелочной функции. Для
// стрелочной тело — выражение, оборачиваемое в `return expr;`.
func (c *compiler) compileClosureBody(ci *closureInfo) (*vm.CompiledFunction, error) {
	var captures []Param
	var body []Stmt
	if ci.isArrow {
		for _, n := range ci.free {
			captures = append(captures, Param{Name: n})
		}
		body = []Stmt{&ReturnStmt{X: ci.expr}}
	} else {
		captures = ci.uses
		body = ci.body
	}
	savedTicks, savedCount := c.ticks, c.tickCount
	c.ticks, c.tickCount = ci.ticks, 0
	fn, err := c.compileBody(fmt.Sprintf("{closure#%d}", ci.idx), ci.params, captures, body, ci.byrefRet, ci.retType, ci.line)
	c.ticks, c.tickCount = savedTicks, savedCount
	return fn, err
}

// collectClosures рекурсивно собирает анонимные и стрелочные функции всех тел
// программы (включая вложенные) и присваивает им индексы (позже — в общем пуле
// функций). Для стрелочных вычисляется список неявно захватываемых переменных.
func collectClosures(prog *Program, classes []vm.ClassInfo, classIdx map[string]int) ([]*closureInfo, map[Expr]*closureInfo) {
	var infos []*closureInfo
	cmap := map[Expr]*closureInfo{}
	resolve := func(name string) vm.ClassInfo {
		if name == "" {
			return nil
		}
		if i, ok := classIdx[name]; ok {
			return classes[i]
		}
		return nil
	}
	mk := func(e Expr, cls string) *closureInfo {
		ci := &closureInfo{cls: resolve(cls)}
		switch x := e.(type) {
		case *ClosureExpr:
			ci.params = x.Params
			ci.uses = x.Uses
			ci.byrefRet = x.ByRefReturn
			ci.static = x.Static
			ci.retType = x.RetType
			ci.body = x.Body
			ci.attrs = x.Attrs
			ci.line = x.Line
			ci.ticks = x.Ticks
		case *ArrowFnExpr:
			ci.params = x.Params
			ci.isArrow = true
			ci.static = x.Static
			ci.byrefRet = x.ByRefReturn
			ci.retType = x.RetType
			ci.expr = x.Body
			ci.attrs = x.Attrs
			ci.free = arrowFreeVars(x.Body, x.Params)
			ci.line = x.Line
			ci.ticks = x.Ticks
		}
		infos = append(infos, ci)
		cmap[e] = ci
		return ci
	}
	var walkE func(e Expr, cls string)
	var walkS func(s Stmt, cls string)
	walkE = func(e Expr, cls string) {
		switch x := e.(type) {
		case nil:
		case *ClosureExpr:
			mk(x, cls)
			for _, st := range x.Body {
				walkS(st, cls)
			}
		case *ArrowFnExpr:
			mk(x, cls)
			walkE(x.Body, cls)
		case *IndexExpr:
			walkE(x.Arr, cls)
			walkE(x.Key, cls)
		case *PropExpr:
			walkE(x.Recv, cls)
		case *BinExpr:
			walkE(x.L, cls)
			walkE(x.R, cls)
		case *LogicalExpr:
			walkE(x.L, cls)
			walkE(x.R, cls)
		case *UnaryExpr:
			walkE(x.X, cls)
		case *IncDecExpr:
			walkE(x.X, cls)
		case *AssignExpr:
			walkE(x.Target, cls)
			walkE(x.Value, cls)
		case *TernaryExpr:
			walkE(x.Cond, cls)
			walkE(x.Then, cls)
			walkE(x.Else, cls)
		case *CoalesceExpr:
			walkE(x.L, cls)
			walkE(x.R, cls)
		case *YieldExpr:
			walkE(x.Key, cls)
			walkE(x.Val, cls)
		case *CallExpr:
			for _, a := range x.Args {
				walkE(a, cls)
			}
		case *NamedArgExpr:
			walkE(x.X, cls)
		case *MethodCallExpr:
			walkE(x.Recv, cls)
			for _, a := range x.Args {
				walkE(a, cls)
			}
		case *StaticCallExpr:
			for _, a := range x.Args {
				walkE(a, cls)
			}
		case *NewExpr:
			walkE(x.ClassExpr, cls)
			for _, a := range x.Args {
				walkE(a, cls)
			}
		case *NewAnonClassExpr:
			for _, a := range x.Args {
				walkE(a, cls)
			}
			for _, mem := range x.Decl.Members {
				for _, st := range mem.Body {
					walkS(st, x.Decl.Name)
				}
			}
		case *DynCallExpr:
			walkE(x.Name, cls)
			for _, a := range x.Args {
				walkE(a, cls)
			}
		case *ArrayLit:
			for _, el := range x.Elems {
				walkE(el.Key, cls)
				walkE(el.Val, cls)
			}
		case *MatchExpr:
			walkE(x.Subject, cls)
			for _, arm := range x.Arms {
				for _, cond := range arm.Conds {
					walkE(cond, cls)
				}
				walkE(arm.Body, cls)
			}
		case *NullsafeExpr:
			walkE(x.Recv, cls)
			walkE(x.Body, cls)
		case *NullsafeBase:
		case *CastExpr:
			walkE(x.X, cls)
		case *ListExpr:
			for _, el := range x.Elems {
				walkE(el.Key, cls)
				walkE(el.Target, cls)
			}
		case *IssetExpr:
			for _, a := range x.Args {
				walkE(a, cls)
			}
		case *EmptyExpr:
			walkE(x.X, cls)
		case *PrintExpr:
			walkE(x.X, cls)
		case *ExitExpr:
			walkE(x.X, cls)
		case *CloneExpr:
			walkE(x.X, cls)
		case *ThrowExpr:
			walkE(x.X, cls)
		case *InstanceOfExpr:
			walkE(x.X, cls)
			if x.ClassExpr != nil {
				walkE(x.ClassExpr, cls)
			}
		}
	}
	walkS = func(s Stmt, cls string) {
		switch x := s.(type) {
		case nil:
		case *BlockStmt:
			for _, st := range x.Body {
				walkS(st, cls)
			}
		case *EchoStmt:
			for _, a := range x.Args {
				walkE(a, cls)
			}
		case *ExprStmt:
			walkE(x.X, cls)
		case *IfStmt:
			walkE(x.Cond, cls)
			for _, st := range x.Then {
				walkS(st, cls)
			}
			for _, st := range x.Else {
				walkS(st, cls)
			}
		case *WhileStmt:
			walkE(x.Cond, cls)
			for _, st := range x.Body {
				walkS(st, cls)
			}
		case *DoWhileStmt:
			for _, st := range x.Body {
				walkS(st, cls)
			}
			walkE(x.Cond, cls)
		case *ForStmt:
			for _, e := range x.Init {
				walkE(e, cls)
			}
			for _, e := range x.Cond {
				walkE(e, cls)
			}
			for _, e := range x.Post {
				walkE(e, cls)
			}
			for _, st := range x.Body {
				walkS(st, cls)
			}
		case *ForeachStmt:
			walkE(x.Expr, cls)
			for _, st := range x.Body {
				walkS(st, cls)
			}
		case *SwitchStmt:
			walkE(x.Subject, cls)
			for _, cs := range x.Cases {
				walkE(cs.Cond, cls)
				for _, st := range cs.Body {
					walkS(st, cls)
				}
			}
		case *ReturnStmt:
			walkE(x.X, cls)
		case *UnsetStmt:
			for _, a := range x.Args {
				walkE(a, cls)
			}
		case *ThrowStmt:
			walkE(x.X, cls)
		case *TryStmt:
			for _, st := range x.Body {
				walkS(st, cls)
			}
			for _, cc := range x.Catches {
				for _, st := range cc.Body {
					walkS(st, cls)
				}
			}
			for _, st := range x.Finally {
				walkS(st, cls)
			}
		case *StaticVarStmt:
			for _, e := range x.Default {
				walkE(e, cls)
			}
		case *FuncDecl:
			for _, st := range x.Body {
				walkS(st, "")
			}
		case *ClassDecl:
			for _, m := range x.Members {
				if m.Kind != "method" {
					continue
				}
				for _, st := range m.Body {
					walkS(st, x.Name)
				}
			}
		}
	}
	for _, st := range prog.Stmts {
		walkS(st, "")
	}
	return infos, cmap
}

// anonClassInfo — собранный анонимный класс с присвоенным внутренним именем.
type anonClassInfo struct {
	decl *ClassDecl
	name string
}

// collectAnonClasses обходит AST и присваивает каждому анонимному классу
// внутреннее имя, чтобы зарегистрировать его в таблице классов как обычный.
func collectAnonClasses(prog *Program) ([]anonClassInfo, map[Expr]string, []*ClassDecl, map[string]string) {
	var infos []anonClassInfo
	m := map[Expr]string{}
	// aliases — class_alias('Target', 'Alias') с литеральными аргументами:
	// учитываются на этапе компиляции при сравнении типов, как Zend до рантайма.
	aliases := map[string]string{}
	// topLevel — объявления прямо в prog.Stmts: они хойстятся компилятором и
	// уже собраны как обычные; остальные (внутри функций/замыканий/блоков)
	// объявляются в рантайме (OpDeclareClass).
	topLevel := map[*ClassDecl]bool{}
	for _, st := range prog.Stmts {
		if cd, ok := st.(*ClassDecl); ok {
			topLevel[cd] = true
		}
	}
	var nested []*ClassDecl
	var walkE func(Expr)
	var walkS func(Stmt)
	add := func(x *NewAnonClassExpr) {
		if _, ok := m[x]; ok {
			return
		}
		name := fmt.Sprintf("\x00anon#%d", len(infos))
		x.Decl.Name = name
		infos = append(infos, anonClassInfo{decl: x.Decl, name: name})
		m[x] = name
	}
	walkE = func(e Expr) {
		switch x := e.(type) {
		case nil:
		case *VarExpr:
			walkE(x.NameExpr)
		case *AssignExpr:
			walkE(x.Target)
			walkE(x.Value)
		case *IndexExpr:
			walkE(x.Arr)
			walkE(x.Key)
		case *BinExpr:
			walkE(x.L)
			walkE(x.R)
		case *LogicalExpr:
			walkE(x.L)
			walkE(x.R)
		case *UnaryExpr:
			walkE(x.X)
		case *IncDecExpr:
			walkE(x.X)
		case *TernaryExpr:
			walkE(x.Cond)
			walkE(x.Then)
			walkE(x.Else)
		case *CoalesceExpr:
			walkE(x.L)
			walkE(x.R)
		case *YieldExpr:
			walkE(x.Key)
			walkE(x.Val)
		case *CallExpr:
			if strings.EqualFold(strings.TrimLeft(x.Name, "\\"), "class_alias") && len(x.Args) >= 2 {
				if t, ok1 := x.Args[0].(*StrLit); ok1 {
					if a, ok2 := x.Args[1].(*StrLit); ok2 {
						aliases[strings.ToLower(strings.TrimLeft(a.V, "\\"))] = strings.TrimLeft(t.V, "\\")
					}
				}
			}
			for _, a := range x.Args {
				walkE(a)
			}
		case *NamedArgExpr:
			walkE(x.X)
		case *MethodCallExpr:
			walkE(x.Recv)
			walkE(x.NameExpr)
			for _, a := range x.Args {
				walkE(a)
			}
		case *StaticCallExpr:
			walkE(x.NameExpr)
			for _, a := range x.Args {
				walkE(a)
			}
		case *NewExpr:
			walkE(x.ClassExpr)
			for _, a := range x.Args {
				walkE(a)
			}
		case *NewAnonClassExpr:
			add(x)
			for _, a := range x.Args {
				walkE(a)
			}
			for _, mem := range x.Decl.Members {
				for _, st := range mem.Body {
					walkS(st)
				}
			}
		case *DynCallExpr:
			walkE(x.Name)
			for _, a := range x.Args {
				walkE(a)
			}
		case *PropExpr:
			walkE(x.Recv)
			walkE(x.NameExpr)
		case *StaticPropExpr:
			walkE(x.NameExpr)
		case *ArrayLit:
			for _, el := range x.Elems {
				walkE(el.Key)
				walkE(el.Val)
			}
		case *MatchExpr:
			walkE(x.Subject)
			for _, arm := range x.Arms {
				for _, cond := range arm.Conds {
					walkE(cond)
				}
				walkE(arm.Body)
			}
		case *NullsafeExpr:
			walkE(x.Recv)
			walkE(x.Body)
		case *NullsafeBase:
		case *CastExpr:
			walkE(x.X)
		case *ListExpr:
			for _, el := range x.Elems {
				walkE(el.Key)
				walkE(el.Target)
			}
		case *IssetExpr:
			for _, a := range x.Args {
				walkE(a)
			}
		case *EmptyExpr:
			walkE(x.X)
		case *PrintExpr:
			walkE(x.X)
		case *ExitExpr:
			walkE(x.X)
		case *CloneExpr:
			walkE(x.X)
		case *ThrowExpr:
			walkE(x.X)
		case *InstanceOfExpr:
			walkE(x.X)
			if x.ClassExpr != nil {
				walkE(x.ClassExpr)
			}
		case *IncludeExpr:
			walkE(x.Path)
		case *ClosureExpr:
			for _, st := range x.Body {
				walkS(st)
			}
		case *ArrowFnExpr:
			walkE(x.Body)
		}
	}
	walkS = func(st Stmt) {
		switch x := st.(type) {
		case nil:
		case *BlockStmt:
			for _, s := range x.Body {
				walkS(s)
			}
		case *EchoStmt:
			for _, a := range x.Args {
				walkE(a)
			}
		case *ExprStmt:
			walkE(x.X)
		case *IfStmt:
			walkE(x.Cond)
			for _, s := range x.Then {
				walkS(s)
			}
			for _, s := range x.Else {
				walkS(s)
			}
		case *WhileStmt:
			walkE(x.Cond)
			for _, s := range x.Body {
				walkS(s)
			}
		case *DoWhileStmt:
			for _, s := range x.Body {
				walkS(s)
			}
			walkE(x.Cond)
		case *ForStmt:
			for _, e := range x.Init {
				walkE(e)
			}
			for _, e := range x.Cond {
				walkE(e)
			}
			for _, e := range x.Post {
				walkE(e)
			}
			for _, s := range x.Body {
				walkS(s)
			}
		case *ForeachStmt:
			walkE(x.Expr)
			walkE(x.KeyTarget)
			walkE(x.ValTarget)
			for _, s := range x.Body {
				walkS(s)
			}
		case *SwitchStmt:
			walkE(x.Subject)
			for _, cs := range x.Cases {
				walkE(cs.Cond)
				for _, s := range cs.Body {
					walkS(s)
				}
			}
		case *ReturnStmt:
			walkE(x.X)
		case *ThrowStmt:
			walkE(x.X)
		case *TryStmt:
			for _, s := range x.Body {
				walkS(s)
			}
			for _, cc := range x.Catches {
				for _, s := range cc.Body {
					walkS(s)
				}
			}
			for _, s := range x.Finally {
				walkS(s)
			}
		case *UnsetStmt:
			for _, a := range x.Args {
				walkE(a)
			}
		case *StaticVarStmt:
			for _, e := range x.Default {
				walkE(e)
			}
		case *FuncDecl:
			for _, s := range x.Body {
				walkS(s)
			}
		case *ClassDecl:
			if !topLevel[x] {
				nested = append(nested, x)
			}
			for _, mem := range x.Members {
				for _, s := range mem.Body {
					walkS(s)
				}
			}
		}
	}
	for _, st := range prog.Stmts {
		walkS(st)
	}
	return infos, m, nested, aliases
}

// arrowFreeVars возвращает имена внешних переменных, неявно захватываемых
// стрелочной функцией (body) по значению. Собственные параметры и `$this`
// исключаются. Во вложенные анонимные функции не заглядываем (они захватывают
// только через use), а во вложенные стрелочные — заглядываем, исключая их
// собственные параметры.
func arrowFreeVars(body Expr, params []Param) []string {
	bound := map[string]bool{}
	for _, p := range params {
		bound[p.Name] = true
	}
	var out []string
	seen := map[string]bool{}
	add := func(n string) {
		if n == "" || n == "$this" || n == "this" || bound[n] || seen[n] {
			return
		}
		seen[n] = true
		out = append(out, n)
	}
	var walk func(e Expr)
	walk = func(e Expr) {
		switch x := e.(type) {
		case nil:
		case *VarExpr:
			add(x.Name)
		case *ClosureExpr:
			for _, u := range x.Uses {
				add(u.Name)
			}
		case *ArrowFnExpr:
			inner := map[string]bool{}
			for _, p := range x.Params {
				inner[p.Name] = true
			}
			save := bound
			merged := map[string]bool{}
			for k := range bound {
				merged[k] = true
			}
			for k := range inner {
				merged[k] = true
			}
			bound = merged
			walk(x.Body)
			bound = save
		case *IndexExpr:
			walk(x.Arr)
			walk(x.Key)
		case *PropExpr:
			walk(x.Recv)
		case *BinExpr:
			walk(x.L)
			walk(x.R)
		case *LogicalExpr:
			walk(x.L)
			walk(x.R)
		case *UnaryExpr:
			walk(x.X)
		case *IncDecExpr:
			walk(x.X)
		case *AssignExpr:
			walk(x.Target)
			walk(x.Value)
		case *TernaryExpr:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		case *CoalesceExpr:
			walk(x.L)
			walk(x.R)
		case *YieldExpr:
			walk(x.Key)
			walk(x.Val)
		case *CallExpr:
			for _, a := range x.Args {
				walk(a)
			}
		case *MethodCallExpr:
			walk(x.Recv)
			for _, a := range x.Args {
				walk(a)
			}
		case *StaticCallExpr:
			for _, a := range x.Args {
				walk(a)
			}
		case *NewExpr:
			walk(x.ClassExpr)
			for _, a := range x.Args {
				walk(a)
			}
		case *DynCallExpr:
			walk(x.Name)
			for _, a := range x.Args {
				walk(a)
			}
		case *ArrayLit:
			for _, el := range x.Elems {
				walk(el.Key)
				walk(el.Val)
			}
		case *MatchExpr:
			walk(x.Subject)
			for _, arm := range x.Arms {
				for _, cond := range arm.Conds {
					walk(cond)
				}
				walk(arm.Body)
			}
		case *NullsafeExpr:
			walk(x.Recv)
			walk(x.Body)
		case *NullsafeBase:
		case *CastExpr:
			walk(x.X)
		case *ListExpr:
			for _, el := range x.Elems {
				walk(el.Key)
				walk(el.Target)
			}
		case *IssetExpr:
			for _, a := range x.Args {
				walk(a)
			}
		case *EmptyExpr:
			walk(x.X)
		case *PrintExpr:
			walk(x.X)
		case *ExitExpr:
			walk(x.X)
		case *CloneExpr:
			walk(x.X)
		case *ThrowExpr:
			walk(x.X)
		case *InstanceOfExpr:
			walk(x.X)
			if x.ClassExpr != nil {
				walk(x.ClassExpr)
			}
		}
	}
	walk(body)
	return out
}

// unionDNFRestrictive ищет в DNF-объединении пересечение, которое является
// подмножеством другого варианта (более ограничивающее → избыточно).
func unionDNFRestrictive(alts []string) (more, less string, ok bool) {
	parts := func(a string) []string {
		a = strings.TrimSpace(a)
		if strings.HasPrefix(a, "(") && strings.HasSuffix(a, ")") {
			return splitTopLevelAll(a[1:len(a)-1], '&')
		}
		return []string{a}
	}
	containsAll := func(hay, needed []string) bool {
		for _, n := range needed {
			found := false
			for _, h := range hay {
				if strings.EqualFold(strings.TrimSpace(h), strings.TrimSpace(n)) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	strip := func(a string) string {
		a = strings.TrimSpace(a)
		if strings.HasPrefix(a, "(") && strings.HasSuffix(a, ")") {
			return a[1 : len(a)-1]
		}
		return a
	}
	for _, a := range alts {
		ha := parts(a)
		if len(ha) <= 1 {
			continue
		}
		for _, b := range alts {
			if a == b {
				continue
			}
			hb := parts(b)
			if len(ha) > len(hb) && containsAll(ha, hb) {
				return strip(a), strip(b), true
			}
		}
	}
	return "", "", false
}

// unionObjectClassRedundant сообщает, что union содержит и `object`, и
// класс-тип (кроме object). Возвращает тип для сообщения с `object`,
// переставленным сразу после последнего класс-типа.
func unionObjectClassRedundant(alts []string) (string, bool) {
	hasObject := false
	hasClass := false
	var flat []string
	for _, a := range alts {
		orig := strings.TrimSpace(a)
		for _, e := range expandIterableAlt(orig) {
			e = strings.TrimSpace(e)
			flat = append(flat, e)
		}
		// object определяется по исходному варианту; iterable не считается
		// класс-типом (его Traversable покрывается object, но array — нет).
		base := strings.TrimPrefix(strings.ToLower(orig), "?")
		switch base {
		case "object":
			hasObject = true
		case "int", "float", "string", "bool", "array", "null", "mixed",
			"void", "never", "callable", "false", "true", "iterable":
		default:
			hasClass = true
		}
	}
	if !hasObject || !hasClass {
		return "", false
	}
	// Переставляем object сразу после последнего класс-типа.
	var rest []string
	objPos := -1
	for i, e := range flat {
		if strings.TrimPrefix(strings.ToLower(e), "?") == "object" {
			objPos = i
			continue
		}
		rest = append(rest, e)
	}
	insert := 0
	lastClass := -1
	for i, e := range rest {
		base := strings.TrimPrefix(strings.ToLower(e), "?")
		switch base {
		case "int", "float", "string", "bool", "array", "null", "mixed",
			"void", "never", "callable", "false", "true":
		default:
			lastClass = i
		}
	}
	if lastClass >= 0 {
		insert = lastClass + 1
	} else if objPos >= 0 {
		insert = objPos
	}
	out := make([]string, 0, len(rest)+1)
	out = append(out, rest[:insert]...)
	out = append(out, "object")
	out = append(out, rest[insert:]...)
	return strings.Join(out, "|"), true
}

// expandIterableAlt expands `iterable`/`?iterable` into its alias parts.
func expandIterableAlt(t string) []string {
	t = strings.TrimSpace(t)
	if t == "iterable" {
		return []string{"array", "Traversable"}
	}
	if t == "?iterable" {
		return []string{"?array", "?Traversable"}
	}
	return []string{t}
}

// typeDupName выбирает написание типа для сообщения о дубликате.
func typeDupName(t string) string {
	base := strings.TrimPrefix(strings.ToLower(t), "?")
	switch base {
	case "int", "float", "string", "bool", "array", "null", "mixed",
		"void", "never", "object", "callable", "false", "true", "iterable":
		return base
	}
	return t
}

// defaultKind классифицирует литеральное значение по умолчанию.
func defaultKind(e Expr) (string, bool) {
	switch x := e.(type) {
	case *NullLit:
		return "null", true
	case *BoolLit:
		return "bool", true
	case *IntLit:
		return "int", true
	case *FloatLit:
		return "float", true
	case *StrLit:
		return "string", true
	case *ArrayLit:
		return "array", true
	case *UnaryExpr:
		if x.Op == "-" {
			switch x.X.(type) {
			case *IntLit:
				return "int", true
			case *FloatLit:
				return "float", true
			}
		}
	}
	return "", false
}

// displayType рендерит тип в каноническом виде Zend для диагностик.
func displayType(t string) string {
	return vm.TypeString(t)
}

// defaultTypeDisplay — тип для диагностики «Cannot use X as default value for
// parameter ... of type T»: встроенные имена Zend печатает в нижнем регистре
// (`String` -> `string`), классы — как написаны.
func defaultTypeDisplay(t string) string {
	s := displayType(t)
	if !strings.ContainsAny(s, "|&?") {
		switch strings.ToLower(s) {
		case "int", "float", "string", "bool", "array", "object", "callable",
			"iterable", "mixed", "void", "never", "null", "false", "true":
			return strings.ToLower(s)
		}
	}
	return s
}

// canonicalUnionType приводит union-тип к каноническому порядку Zend для
// сообщений об ошибках (напр. `int|string` -> `string|int`). Порядок частей
// соответствует zend_type_to_string: встроенные типы в фиксированном порядке
// (array, string, int, float, bool, null/остальные — в конце).
func canonicalUnionType(t string) string {
	if !strings.ContainsAny(t, "|&?") {
		return t
	}
	parts := splitTopLevelAll(t, '|')
	if len(parts) <= 1 {
		return t
	}
	rank := func(s string) int {
		switch strings.TrimSpace(strings.TrimPrefix(s, "?")) {
		case "array":
			return 0
		case "string":
			return 1
		case "int":
			return 2
		case "float":
			return 3
		case "bool", "false", "true":
			return 4
		case "null":
			return 5
		default:
			return 6
		}
	}
	reordered := append([]string(nil), parts...)
	for i := 1; i < len(reordered); i++ {
		for j := i; j > 0 && rank(reordered[j-1]) > rank(reordered[j]); j-- {
			reordered[j-1], reordered[j] = reordered[j], reordered[j-1]
		}
	}
	return strings.Join(reordered, "|")
}

// typeNullable сообщает, включает ли тип null (nullable/mixed).
func typeNullable(typ string) bool {
	typ = strings.TrimSpace(typ)
	if typ == "mixed" {
		return true
	}
	for _, a := range splitTopLevelAll(typ, '|') {
		a = strings.TrimSpace(a)
		if a == "null" || strings.HasPrefix(a, "?") {
			return true
		}
	}
	return false
}

// typeAllowsDefault проверяет, допустимо ли значение вида kind как дефолт
// объявленного типа typ (без учёта null — он проверяется отдельно).
func typeAllowsDefault(typ, kind string) bool {
	typ = strings.TrimSpace(typ)
	if typ == "" || typ == "mixed" {
		return true
	}
	for _, raw := range splitTopLevelAll(typ, '|') {
		a := strings.TrimSpace(raw)
		a = strings.TrimPrefix(a, "?")
		switch a {
		case "mixed":
			return true
		case "int":
			if kind == "int" {
				return true
			}
		case "float":
			if kind == "float" || kind == "int" {
				return true
			}
		case "string":
			if kind == "string" {
				return true
			}
		case "bool", "true", "false":
			if kind == "bool" {
				return true
			}
		case "array":
			if kind == "array" {
				return true
			}
		case "iterable":
			if kind == "array" {
				return true
			}
		}
	}
	return false
}

// constValueOf вычисляет константное выражение (дефолт свойства/константа).
func constValueOf(e Expr) (vm.Slot, error) {
	switch x := e.(type) {
	case nil:
		return vm.MakeNull(), nil
	case *IntLit:
		return vm.MakeInt(x.V), nil
	case *FloatLit:
		return vm.MakeFloat(x.V), nil
	case *StrLit:
		return vm.MakeString(x.V), nil
	case *BoolLit:
		return vm.MakeBool(x.V), nil
	case *NullLit:
		return vm.MakeNull(), nil
	case *UnaryExpr:
		if x.Op == "-" {
			switch y := x.X.(type) {
			case *IntLit:
				return vm.MakeInt(-y.V), nil
			case *FloatLit:
				return vm.MakeFloat(-y.V), nil
			}
		}
	case *ArrayLit:
		a := vm.NewArray()
		for _, el := range x.Elems {
			v, err := constValueOf(el.Val)
			if err != nil {
				return vm.Slot{}, err
			}
			if el.Key == nil {
				if _, err := a.Append(v); err != nil {
					return vm.Slot{}, err
				}
				continue
			}
			switch k := el.Key.(type) {
			case *IntLit:
				if err := a.SetInt(k.V, v); err != nil {
					return vm.Slot{}, err
				}
			case *StrLit:
				if err := a.SetStr(k.V, v); err != nil {
					return vm.Slot{}, err
				}
			default:
				return vm.Slot{}, fmt.Errorf("компилятор: непостоянный ключ массива")
			}
		}
		return vm.MakeArray(a), nil
	case *BinExpr:
		l, err := constValueOf(x.L)
		if err != nil {
			return vm.Slot{}, err
		}
		r, err := constValueOf(x.R)
		if err != nil {
			return vm.Slot{}, err
		}
		return constBinop(x.Op, l, r)
	}
	return vm.Slot{}, fmt.Errorf("компилятор: непостоянное значение по умолчанию")
}

// constBinop вычисляет константное бинарное выражение.
func constBinop(op string, l, r vm.Slot) (vm.Slot, error) {
	li, lIsInt := constInt(l)
	ri, rIsInt := constInt(r)
	if lIsInt && rIsInt {
		switch op {
		case "+":
			return vm.MakeInt(li + ri), nil
		case "-":
			return vm.MakeInt(li - ri), nil
		case "*":
			return vm.MakeInt(li * ri), nil
		case "/":
			if ri == 0 {
				break
			}
			return vm.MakeInt(li / ri), nil
		case "%":
			if ri == 0 {
				break
			}
			return vm.MakeInt(li % ri), nil
		case "<<":
			return vm.MakeInt(li << uint(ri)), nil
		case ">>":
			return vm.MakeInt(li >> uint(ri)), nil
		case "&":
			return vm.MakeInt(li & ri), nil
		case "|":
			return vm.MakeInt(li | ri), nil
		case "^":
			return vm.MakeInt(li ^ ri), nil
		}
	}
	switch op {
	case ".":
		return vm.MakeString(l.ToString() + r.ToString()), nil
	case "+", "-", "*", "/", "%":
		lf, rf := l.ToFloat(), r.ToFloat()
		switch op {
		case "+":
			return vm.MakeFloat(lf + rf), nil
		case "-":
			return vm.MakeFloat(lf - rf), nil
		case "*":
			return vm.MakeFloat(lf * rf), nil
		case "/":
			if rf == 0 {
				return vm.Slot{}, fmt.Errorf("компилятор: деление на ноль в константном выражении")
			}
			return vm.MakeFloat(lf / rf), nil
		}
	}
	return vm.Slot{}, fmt.Errorf("компилятор: непостоянное значение по умолчанию")
}

// constInt возвращает целое значение слота и признак, что это int.
func constInt(s vm.Slot) (int64, bool) {
	if s.Type() == vm.IntType {
		return s.ToInt(), true
	}
	return 0, false
}

// isConstExpr сообщает, что выражение допустимо в константном выражении PHP
// (может быть отложено и вычислено лениво). new/вызовы/переменные/присваивания
// недопустимы.
func isConstExpr(e Expr) bool {
	switch x := e.(type) {
	case nil, *IntLit, *FloatLit, *StrLit, *BoolLit, *NullLit, *MagicExpr, *ClassKeywordExpr, *ConstExpr:
		return true
	case *UnaryExpr:
		switch x.Op {
		case "+", "-", "!", "~":
			return isConstExpr(x.X)
		}
		return false
	case *BinExpr:
		return isConstExpr(x.L) && isConstExpr(x.R)
	case *LogicalExpr:
		return isConstExpr(x.L) && isConstExpr(x.R)
	case *TernaryExpr:
		cond := x.Cond
		if cond == nil {
			cond = x.Then
		}
		if !isConstExpr(cond) {
			return false
		}
		if x.Then != nil && !isConstExpr(x.Then) {
			return false
		}
		return isConstExpr(x.Else)
	case *CoalesceExpr:
		return isConstExpr(x.L) && isConstExpr(x.R)
	case *ArrayLit:
		for _, el := range x.Elems {
			if !isConstExpr(el.Val) {
				return false
			}
			if el.Key != nil && !isConstExpr(el.Key) {
				return false
			}
		}
		return true
	case *IndexExpr:
		if x.Key == nil {
			return false
		}
		return isConstExpr(x.Arr) && isConstExpr(x.Key)
	case *CastExpr:
		return isConstExpr(x.X)
	case *PropExpr:
		// PHP 8.3: чтение свойства допустимо в константном выражении;
		// ограничение (только объекты-enum) проверяется в рантайме,
		// см. zend_ast_evaluate.
		if x.NameExpr != nil && !isConstExpr(x.NameExpr) {
			return false
		}
		if _, ok := x.Recv.(*NullsafeBase); ok {
			return true
		}
		return isConstExpr(x.Recv)
	case *NullsafeExpr:
		if !isConstExpr(x.Recv) {
			return false
		}
		return isConstExpr(x.Body)
	case *NullsafeBase:
		return true
	}
	return false
}

// usesNewExpr сообщает, содержит ли выражение `new` (для точного сообщения
// «New expressions are not supported in this context»).
func usesNewExpr(e Expr) bool {
	switch x := e.(type) {
	case *NewExpr, *NewAnonClassExpr:
		return true
	case *UnaryExpr:
		return usesNewExpr(x.X)
	case *BinExpr:
		return usesNewExpr(x.L) || usesNewExpr(x.R)
	case *LogicalExpr:
		return usesNewExpr(x.L) || usesNewExpr(x.R)
	case *TernaryExpr:
		return usesNewExpr(x.Cond) || usesNewExpr(x.Then) || usesNewExpr(x.Else)
	case *CoalesceExpr:
		return usesNewExpr(x.L) || usesNewExpr(x.R)
	case *ArrayLit:
		for _, el := range x.Elems {
			if usesNewExpr(el.Val) || (el.Key != nil && usesNewExpr(el.Key)) {
				return true
			}
		}
	case *IndexExpr:
		return usesNewExpr(x.Arr) || (x.Key != nil && usesNewExpr(x.Key))
	case *CastExpr:
		return usesNewExpr(x.X)
	}
	return false
}

// compileConstThunk компилирует константное выражение в функцию без аргументов
// (значение возвращается через OpRet). Класс объявления cls становится scope
// (self) вычисления.
func (c *compiler) compileConstThunk(cls vm.ClassInfo, e Expr) (*vm.Function, error) {
	cc := newCompiler(c.funcs, c.idx)
	cc.file = c.file
	cc.classes = c.classes
	cc.classIdx = c.classIdx
	cc.curClass = cls
	cc.methodRefs = c.methodRefs
	cc.closureIdx = c.closureIdx
	cc.anonNames = c.anonNames
	cc.stmtLines = c.stmtLines
	cc.exprLines = c.exprLines
	cc.constCtx = true
	cc.haltOffset = c.haltOffset
	cc.strict = c.strict
	cc.chunk.Strict = c.strict
	cc.chunk.SetEmitStrict(c.strict)
	cc.nvars0 = 0
	cc.tmp = 0
	cc.max = 0
	r, err := cc.expr(e)
	if err != nil {
		return nil, err
	}
	cc.chunk.Emit(vm.Instruction{Op: vm.OpRet, A: r, C: 1})
	cc.chunk.SetConstExpr()
	cc.chunk.Optimize()
	cc.chunk.AllocateRegisters(0, cc.allocEnv())
	cf := vm.NewCompiledFunction(cc.chunk)
	return &vm.Function{Name: "[constant expression]", DisplayName: "[constant expression]", NumArgs: 0, Executable: cf, Scope: cls}, nil
}

// makeConstValue вычисляет константное выражение немедленно, если это
// возможно; иначе компилирует отложенное выражение (vm.Thunk). Недопустимые
// выражения дают фатал на этапе компиляции. Ровно один из (v, th) не nil.
// makeAttrs конвертирует разобранные атрибуты в vm.AttributeInfo. Аргументы
// компилируются в отложенные выражения (build) — они вычисляются при
// обращении к атрибуту в области объявления, что поддерживает self::,
// forward-константы и классы. Если отложенная компиляция недоступна или
// выражение не константное, аргументы вычисляются сразу (простые литералы и
// массивы); невычислимые дают null (прежнее поведение).
// attrFileOf — файл объявления класса/функции для атрибутов: при inline
// include объявление принадлежит включаемому файлу, а не включателю (030).
func attrFileOf(defFile, fallback string) string {
	if defFile != "" {
		return defFile
	}
	return fallback
}

func makeAttrsInfo(decls []AttrDecl, file string, strict bool, build func(Expr) (*vm.Function, error)) []vm.AttributeInfo {
	var out []vm.AttributeInfo
	for _, d := range decls {
		ai := vm.AttributeInfo{Name: d.Name, File: file, Line: d.Line, Strict: strict}
		// Пробуем собрать все аргументы как отложенные выражения: смешение
		// ленивых и немедленных ломает порядок позиционных аргументов.
		if build != nil {
			ok := true
			for _, a := range d.Args {
				if a.Val == nil {
					continue
				}
				if fn, err := build(a.Val); err != nil || fn == nil {
					ok = false
					break
				}
			}
			if ok {
				for _, a := range d.Args {
					if a.Val == nil {
						fn, _ := build(&NullLit{})
						if a.Name != "" {
							ai.NamedArgFns = append(ai.NamedArgFns, vm.NamedAttrFn{Name: a.Name, Fn: fn})
						} else {
							ai.ArgFns = append(ai.ArgFns, fn)
						}
						continue
					}
					fn, _ := build(a.Val)
					if a.Name != "" {
						ai.NamedArgFns = append(ai.NamedArgFns, vm.NamedAttrFn{Name: a.Name, Fn: fn})
					} else {
						ai.ArgFns = append(ai.ArgFns, fn)
					}
				}
				out = append(out, ai)
				continue
			}
		}
		for _, a := range d.Args {
			v := vm.MakeNull()
			if a.Val != nil {
				if s, err := constValueOf(a.Val); err == nil {
					v = s
				}
			}
			if a.Name != "" {
				ai.Named = append(ai.Named, vm.NamedAttrArg{Name: a.Name, Val: v})
			} else {
				ai.Args = append(ai.Args, v)
			}
		}
		out = append(out, ai)
	}
	return out
}

// attrThunks — построитель отложенных выражений аргументов атрибутов в
// области класса cls (self). nil cls — глобальная область.
func (c *compiler) attrThunks(cls vm.ClassInfo) func(Expr) (*vm.Function, error) {
	return func(e Expr) (*vm.Function, error) {
		if e == nil {
			return nil, nil
		}
		return c.compileConstThunk(cls, e)
	}
}

// splitCompilerWarning разбирает запись compilerWarnings вида
// "<msg> in <file> on line <N>" обратно на части.
func splitCompilerWarning(w string) (msg, file string, line int) {
	i := strings.LastIndex(w, " in ")
	j := strings.LastIndex(w, " on line ")
	if i < 0 || j < i {
		return w, "", 0
	}
	n, err := strconv.Atoi(w[j+len(" on line "):])
	if err != nil {
		return w, "", 0
	}
	return w[:i], w[i+4 : j], n
}

// containsStaticClassKeyword ищет `static::class` (нельзя вычислить на этапе
// компиляции в константном выражении).
func containsStaticClassKeyword(e Expr) bool {
	switch x := e.(type) {
	case *ClassKeywordExpr:
		return x.Class == "static"
	case *BinExpr:
		return containsStaticClassKeyword(x.L) || containsStaticClassKeyword(x.R)
	case *UnaryExpr:
		return containsStaticClassKeyword(x.X)
	case *TernaryExpr:
		return containsStaticClassKeyword(x.Cond) || containsStaticClassKeyword(x.Then) || containsStaticClassKeyword(x.Else)
	case *CoalesceExpr:
		return containsStaticClassKeyword(x.L) || containsStaticClassKeyword(x.R)
	case *ArrayLit:
		for _, el := range x.Elems {
			if containsStaticClassKeyword(el.Val) || containsStaticClassKeyword(el.Key) {
				return true
			}
		}
	}
	return false
}

// emptyIndexRead ищет чтение с пустым индексом `[]` (IndexExpr с Key == nil):
// Zend запрещает его на компиляции («Cannot use [] for reading»), в том числе
// внутри константных выражений (bug70912).
func emptyIndexRead(e Expr) Expr {
	switch x := e.(type) {
	case *IndexExpr:
		if x.Key == nil {
			return x
		}
		if r := emptyIndexRead(x.Arr); r != nil {
			return r
		}
		return emptyIndexRead(x.Key)
	case *BinExpr:
		if r := emptyIndexRead(x.L); r != nil {
			return r
		}
		return emptyIndexRead(x.R)
	case *UnaryExpr:
		return emptyIndexRead(x.X)
	case *TernaryExpr:
		if r := emptyIndexRead(x.Cond); r != nil {
			return r
		}
		if x.Then != nil {
			if r := emptyIndexRead(x.Then); r != nil {
				return r
			}
		}
		return emptyIndexRead(x.Else)
	case *CoalesceExpr:
		if r := emptyIndexRead(x.L); r != nil {
			return r
		}
		return emptyIndexRead(x.R)
	case *ArrayLit:
		for _, el := range x.Elems {
			if el.Key != nil {
				if r := emptyIndexRead(el.Key); r != nil {
					return r
				}
			}
			if r := emptyIndexRead(el.Val); r != nil {
				return r
			}
		}
	}
	return nil
}

func (c *compiler) makeConstValue(cls vm.ClassInfo, e Expr, name string) (vm.Value, *vm.Thunk, error) {
	if containsStaticClassKeyword(e) {
		return nil, nil, c.newFatal("static::class cannot be used for compile-time class name resolution")
	}
	if idx := emptyIndexRead(e); idx != nil {
		return nil, nil, &fatalError{msg: "Cannot use [] for reading", line: c.exprLine(idx)}
	}
	if v, err := constValueOf(e); err == nil {
		return v, nil, nil
	}
	if !isConstExpr(e) {
		if usesNewExpr(e) {
			return nil, nil, c.newFatal("New expressions are not supported in this context")
		}
		return nil, nil, c.newFatal("Constant expression contains invalid operations")
	}
	fn, err := c.compileConstThunk(cls, e)
	if err != nil {
		return nil, nil, err
	}
	return nil, vm.NewThunk(fn, cls), nil
}

func emitNullIdent(c *compiler, dst uint8, reg uint8) {
	n := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: n})
	c.chunk.Emit(vm.Instruction{Op: vm.OpIdentical, A: dst, B: reg, C: int32(n)})
}

func (c *compiler) stmts(list []Stmt) error {
	for _, st := range list {
		if err := c.stmt(st); err != nil {
			return err
		}
		// self/parent/static в теле функции без области класса: Zend
		// останавливает компиляцию (флаг ставит classRef/classIndex).
		if c.scopeFatal != nil {
			return c.scopeFatal
		}
		c.emitStmtSweep()
		c.emitStmtTick()
	}
	return nil
}

// emitStmtTick эмитит вызов __php_ticks после каждого N-го оператора в
// области declare(ticks=N) (Zend: ZEND_TICKS-опкоды в op_array). В чанках без
// declare(ticks) не делает ничего.
func (c *compiler) emitStmtTick() {
	if c.ticks <= 0 {
		return
	}
	c.tickCount++
	if c.tickCount%c.ticks != 0 {
		return
	}
	c.emitCall("__php_ticks", nil)
}

// emitStmtSweep ставит точку сбора сборщика времени жизни (lifetime.go):
// между операторами вызываются __destruct объектов, потерявших последнюю
// ссылку, и переиспользуются PHP-хендлы. После безусловных терминаторов
// точка недостижима и не эмитится.
func (c *compiler) emitStmtSweep() {
	// Чисто скалярные чанки не нуждаются в периодических проходах времени
	// жизни: учитываемые значения в регистрах там появиться не могут.
	if !c.chunk.MayTrack() {
		return
	}
	if op, ok := c.chunk.LastOpcode(); ok && vm.IsUnconditionalTerminator(op) {
		return
	}
	c.chunk.Emit(vm.Instruction{Op: vm.OpSweep})
}

func (c *compiler) stmt(st Stmt) error {
	c.tmp = c.nvars
	if st != nil && c.stmtLines != nil {
		if ln, ok := c.stmtLines[st]; ok && ln > 0 {
			c.curLine = ln
		} else if ln := c.inlineLine(st); ln > 0 {
			c.curLine = ln
		}
	} else if st != nil {
		if ln := c.inlineLine(st); ln > 0 {
			c.curLine = ln
		}
	}
	switch s := st.(type) {
	case nil:
		return nil
	case *BlockStmt:
		if s.Ticks > 0 {
			savedTicks, savedCount := c.ticks, c.tickCount
			c.ticks, c.tickCount = s.Ticks, 0
			err := c.stmts(s.Body)
			c.ticks, c.tickCount = savedTicks, savedCount
			return err
		}
		return c.stmts(s.Body)
	case *TicksStmt:
		// Файловая форма `declare(ticks=N);`: действует до конца текущей
		// лексической области (для тел функций область своя — компилятор
		// тела отдельный).
		c.ticks = s.N
		return nil
	case *EchoStmt:
		for _, x := range s.Args {
			c.emitLine(c.exprLine(x))
			r, err := c.expr(x)
			if err != nil {
				return err
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpEcho, A: r})
		}
	case *ExprStmt:
		if c.stmtLines != nil {
			if ln, ok := c.stmtLines[s]; ok {
				c.emitLine(ln)
			} else if ln := c.inlineLine(s); ln > 0 {
				c.emitLine(ln)
			}
		} else if ln := c.inlineLine(s); ln > 0 {
			c.emitLine(ln)
		}
		// `$x;` — Zend не выполняет чтение вовсе (недиагностируемый no-op).
		if v, ok := s.X.(*VarExpr); ok && v.NameExpr == nil {
			return nil
		}
		// `$x++;` как отдельный оператор: результат отбрасывается, поэтому
		// не сохраняем старое значение.
		if id, ok := s.X.(*IncDecExpr); ok {
			if err := c.incDecStmt(id); err != nil {
				return err
			}
			return nil
		}
		discard := false
		if _, ok := s.X.(*AssignExpr); ok {
			// Результат присваивания-оператора не используется: не
			// материализуем его (важно для строковых offset'ов).
			c.discardResult = true
			discard = true
		}
		_, err := c.expr(s.X)
		if discard {
			c.discardResult = false
		}
		if err != nil {
			return err
		}
	case *IfStmt:
		return c.ifStmt(s)
	case *WhileStmt:
		return c.whileStmt(s)
	case *DoWhileStmt:
		return c.doWhileStmt(s)
	case *ForStmt:
		return c.forStmt(s)
	case *ForeachStmt:
		return c.foreachStmt(s)
	case *SwitchStmt:
		return c.switchStmt(s)
	case *ReturnStmt:
		c.emitLine(c.curLine)
		if s.X != nil && c.byrefReturn && containsNullsafe(s.X) {
			return c.newFatal("Cannot take reference of a nullsafe chain")
		}
		if s.X == nil {
			if c.byrefReturn && !c.hasYield {
				// Zend: возврат NULL из by-ref функции печатает Notice
				// (RETURN_BY_REF с не-ссылкой).
				c.emitRefNotice("Only variable references should be returned by reference")
			}
			if c.curRetType != "" && c.curRetType != "void" && c.curRetType != "never" {
				if !c.hasYield {
					if typeNullable(c.curRetType) {
						c.pendingRetFatal = c.newFatal(`A function with return type must return a value (did you mean "return null;" instead of "return;"?)`)
					} else {
						c.pendingRetFatal = c.newFatal("A function with return type must return a value")
					}
				}
			}
		} else if c.curRetType == "void" {
			if _, isNull := s.X.(*NullLit); isNull {
				return c.newFatal(`A void function must not return a value (did you mean "return;" instead of "return null;"?)`)
			}
			return c.newFatal("A void function must not return a value")
		} else if c.curRetType == "never" {
			return c.newFatal("A never-returning function must not return")
		}
		var r uint8
		if s.X == nil {
			r = c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: r})
		} else if c.byrefReturn {
			if v, ok := s.X.(*VarExpr); ok && v.Name != "$this" && v.Name != "this" {
				r = c.refCell(v.Name)
			} else if isRefLValue(s.X) {
				cell, err := c.refCellForExpr(s.X)
				if err != nil {
					return err
				}
				r = cell
			} else if !c.hasYield && isCallExpr(s.X) {
				// Возврат результата вызова в by-ref функции: ссылка
				// сохраняется, если вызов вернул ссылку; иначе Notice.
				valR, err := c.expr(s.X)
				if err != nil {
					return err
				}
				msg := c.refMsgReg("Only variable references should be returned by reference")
				wrap := c.alloc()
				c.chunk.Emit(vm.Instruction{Op: vm.OpTrue, A: wrap})
				r = c.emitCall("__php_ref_assign_check", []uint8{valR, msg, wrap})
			} else {
				// У генератора `function &` относится к yield, а не к return:
				// возвращать литерал/выражение разрешено без notice.
				if !c.hasYield && !c.yieldByRefOK(s.X) {
					if rt := c.curRetType; rt != "" {
						// Zend проверяет объявленный тип возврата раньше notice:
						// при несовпадении вызывающий бросает TypeError, а notice
						// о не-ссылке не печатается.
						valR, err := c.expr(s.X)
						if err != nil {
							return err
						}
						msgReg := c.refMsgReg("Only variable references should be returned by reference")
						typeReg := c.alloc()
						c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: typeReg,
							C: int32(c.chunk.AddConst(vm.StrConst(rt)))})
						stReg := c.alloc()
						if c.chunk.Strict {
							c.chunk.Emit(vm.Instruction{Op: vm.OpTrue, A: stReg})
						} else {
							c.chunk.Emit(vm.Instruction{Op: vm.OpFalse, A: stReg})
						}
						c.emitCall("__php_ref_ret_notice", []uint8{valR, msgReg, typeReg, stReg})
						r = c.alloc()
						c.chunk.Emit(vm.Instruction{Op: vm.OpNewRef, A: r, B: valR})
					} else {
						c.emitRefNotice("Only variable references should be returned by reference")
						valR, err := c.expr(s.X)
						if err != nil {
							return err
						}
						r = c.alloc()
						c.chunk.Emit(vm.Instruction{Op: vm.OpNewRef, A: r, B: valR})
					}
				} else {
					valR, err := c.expr(s.X)
					if err != nil {
						return err
					}
					r = c.alloc()
					c.chunk.Emit(vm.Instruction{Op: vm.OpNewRef, A: r, B: valR})
				}
			}
		} else {
			var err error
			r, err = c.expr(s.X)
			if err != nil {
				return err
			}
		}
		r = c.saveAcrossUnwind(r)
		if err := c.unwindToRegions(0); err != nil {
			return err
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpRet, A: r})
	case *BreakStmt:
		return c.breakStmt(s.Level, s.OpKind)
	case *ContinueStmt:
		return c.continueStmt(s.Level, s.OpKind)
	case *UnsetStmt:
		c.emitLine(c.curLine)
		for _, a := range s.Args {
			if containsNullsafe(a) {
				return c.newFatal("Can't use nullsafe operator in write context")
			}
			switch v := a.(type) {
			case *IndexExpr:
				if v.Key == nil {
					return c.newFatal("Cannot use [] for unsetting")
				}
				if isTemporaryExpr(v.Arr) {
					return c.newFatal("Cannot use temporary expression in write context")
				}
				c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
				arr, wb, err := c.indexBase(v, false)
				c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
				if err != nil {
					return err
				}
				// `unset($x[0])` при неопределённом $x: Zend читает $x (и
				// печатает «Undefined variable»), но запись не выполняется.
				if ve, ok := v.Arr.(*VarExpr); ok && ve.NameExpr == nil {
					c.emitUndefReadWarn(ve.Name, c.exprLine(v))
				}
				k, err := c.expr(v.Key)
				if err != nil {
					return err
				}
				c.chunk.Emit(vm.Instruction{Op: vm.OpUnset, A: arr, B: k})
				if wb != nil {
					wb(arr)
				}
			case *PropExpr:
				if isTemporaryExpr(v.Recv) {
					return c.newFatal("Cannot use temporary expression in write context")
				}
				// База unset-цепочки читается в IS-режиме: неинициализированное
				// типизированное свойство не диагностируется (variation_nested).
				obj, err := c.exprIssetBase(v.Recv)
				if err != nil {
					return err
				}
				if v.NameExpr != nil {
					ne, err := c.expr(v.NameExpr)
					if err != nil {
						return err
					}
					c.chunk.Emit(vm.Instruction{Op: vm.OpPropUnsetDyn, A: obj, C: int32(ne)})
					break
				}
				name := int32(c.chunk.AddConst(vm.StrConst(v.Name)))
				c.chunk.Emit(vm.Instruction{Op: vm.OpPropUnset, A: obj, C: name})
			case *StaticPropExpr:
				if v.ClassExpr != nil {
					cls, err := c.staticClassReg(v.Class, v.ClassExpr)
					if err != nil {
						return err
					}
					name, err := c.staticNameReg(v.Name, v.NameExpr)
					if err != nil {
						return err
					}
					n := c.alloc()
					c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: n})
					c.emitCall("__php_dyn_static_set", []uint8{cls, name, n})
					break
				}
				sel, err := c.classRef(v.Class)
				if err != nil {
					cls, cerr := c.staticClassReg(v.Class, nil)
					if cerr != nil {
						return cerr
					}
					name, cerr := c.staticNameReg(v.Name, v.NameExpr)
					if cerr != nil {
						return cerr
					}
					n := c.alloc()
					c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: n})
					c.emitCall("__php_dyn_static_set", []uint8{cls, name, n})
					break
				}
				if v.NameExpr != nil {
					ne, err := c.expr(v.NameExpr)
					if err != nil {
						return err
					}
					n := c.alloc()
					c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: n})
					c.chunk.Emit(vm.Instruction{Op: vm.OpStaticSetDyn, A: n, B: sel, C: int32(ne)})
					break
				}
				name := int32(c.chunk.AddConst(vm.StrConst(v.Name)))
				n := c.alloc()
				c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: n})
				c.chunk.Emit(vm.Instruction{Op: vm.OpStaticSet, A: n, B: sel, C: name})
			case *VarExpr:
				if v.NameExpr == nil && (v.Name == "$GLOBALS" || v.Name == "GLOBALS") {
					return c.newFatal(globalsModifyMsg)
				}
				// unset() незавершённого генератора закрывает его (finally).
				// Глобальную переменную закрывает сам OpGlobalUnset (значение
				// уже в таблице глобалов): лишняя загрузка в регистр оставляла
				// бы в нём копию значения (и лишнюю ссылку на объект, из-за
				// которой __destruct не вызывался в точке unset).
				if v.NameExpr == nil && !c.isRef(v.Name) && !c.useGlobal(v.Name) &&
					v.Name != "$this" && v.Name != "this" {
					c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
					cur := c.loadVar(v.Name)
					c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
					c.emitCall("__php_close_gen", []uint8{cur})
				}
				if v.NameExpr == nil && c.useGlobal(v.Name) {
					c.chunk.Emit(vm.Instruction{Op: vm.OpGlobalUnset, C: int32(c.chunk.AddConst(vm.StrConst(c.globalKey(v.Name))))})
					break
				}
				// unset локальной переменной-ссылки разрывает alias: регистр
				// переменной становится plain-Null, общая Ref-ячейка цела.
				if v.NameExpr == nil && c.isRef(v.Name) && !c.useGlobal(v.Name) {
					c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: c.refs[v.Name]})
					break
				}
				n := c.alloc()
				c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: n})
				if v.NameExpr != nil {
					ne, err := c.expr(v.NameExpr)
					if err != nil {
						return err
					}
					c.emitCall("__php_unset_dyn", []uint8{ne})
				} else {
					c.storeVar(v.Name, n)
					c.setUndef(v.Name)
				}
			case *DynCallExpr, *CallExpr:
				// Zend требует «переменную» в unset(): прямой вызов функции —
				// ошибка компиляции (bug70240).
				return c.newFatal("Can't use function return value in write context")
			case *MethodCallExpr, *StaticCallExpr:
				return c.newFatal("Can't use method return value in write context")
			}
		}
	case *StaticVarStmt:
		if c.isMain {
			return c.mainStaticStmt(s)
		}
		// Условные static: инициализируем локальную переменную в точке
		// объявления (ленивое связывание, как ZEND_BIND_STATIC).
		for i, n := range s.Names {
			if !c.localStatics[n] {
				continue
			}
			var def Expr = &NullLit{}
			if i < len(s.Default) && s.Default[i] != nil {
				def = s.Default[i]
			}
			r, err := c.expr(def)
			if err != nil {
				return err
			}
			c.storeVar(n, r)
		}
	case *GlobalStmt:
		// статические имена: привязка учтена пресканом (c.globals/no-op).
		// Динамические (`global $$x` / `global ${expr}`) — вычисляем имя в
		// точке оператора и привязываем в рантайме.
		for _, e := range s.Dyn {
			nr, err := c.expr(e)
			if err != nil {
				return err
			}
			c.emitCall("__php_global_bind", []uint8{nr})
		}
	case *LabelStmt:
		return c.defineLabel(s.Name, s.Line)
	case *GotoStmt:
		return c.gotoStmt(s.Label)
	case *StrictStmt:
		if s.Push {
			callFile, callLine := c.file, c.curLine
			if s.CallNode != nil {
				if ln := c.exprLine(s.CallNode); ln > 0 {
					callLine = ln
				}
			}
			c.pushStrict(s.Strict)
			c.pushFile(s.File)
			if s.File != "" {
				parent := int32(-1)
				if n := len(c.inlineStack); n > 0 {
					parent = c.inlineStack[n-1].region
				}
				region := c.chunk.AddInlineRegion(s.File, callFile, callLine, s.CallName, parent)
				c.inlineStack = append(c.inlineStack, inlineCtx{lines: s.Lines, exprs: s.ExprLines, region: region})
			}
		} else {
			c.popStrict()
			c.popFile()
			if n := len(c.inlineStack); n > 0 {
				c.chunk.CloseInlineRegion(c.inlineStack[n-1].region)
				c.inlineStack = c.inlineStack[:n-1]
			}
		}
	case *FuncDecl:
		// декларация уже учтена при hoisting
	case *ClassDecl:
		// Класс, объявленный в теле метода, — ошибка компиляции Zend
		// (в теле обычной функции объявления разрешены).
		if c.curClass != nil {
			c.curLine = s.Line
			return c.newFatal("Class declarations may not be nested")
		}
		// Объявление вне верхнего уровня: выполняем в точке потока.
		if idx, ok := c.declIdx[s]; ok {
			c.curLine = s.Line
			c.chunk.SetLine(s.Line)
			c.chunk.Emit(vm.Instruction{Op: vm.OpDeclareClass, C: int32(idx)})
			if s.RuntimeFatal != "" {
				c.chunk.Emit(vm.Instruction{Op: vm.OpFatal, C: int32(c.chunk.AddConst(vm.StrConst(s.RuntimeFatal)))})
			}
			break
		}
		// объявление класса уже учтено при hoisting (для верхнего уровня).
		// Конфликт с псевдонимом class_alias() — предупреждение в рантайме.
		{
			nm := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nm, C: int32(c.chunk.AddConst(vm.StrConst(s.Name)))})
			c.emitCall("__php_class_alias_conflict", []uint8{nm})
		}
		if s.RuntimeFatal != "" {
			c.curLine = s.Line
			c.chunk.SetLine(s.Line)
			c.chunk.Emit(vm.Instruction{Op: vm.OpFatal, C: int32(c.chunk.AddConst(vm.StrConst(s.RuntimeFatal)))})
		}
		if s.MissingParent != "" {
			c.curLine = s.Line
			c.emitClassGuard(s.MissingParent, false)
		}
		if s.MissingInterface != "" {
			c.curLine = s.Line
			c.emitClassGuard(s.MissingInterface, true)
		}
		if s.IfaceNotInterface != "" {
			// Zend сообщает об «интерфейсе»-классе при связывании, уже после
			// автозагрузки родителя (см. ветку компиляции интерфейсов).
			c.curLine = s.Line
			c.chunk.SetLine(s.Line)
			msg := fmt.Sprintf("%s cannot implement %s - it is not an interface", s.Name, s.IfaceNotInterface)
			c.chunk.Emit(vm.Instruction{Op: vm.OpFatal, C: int32(c.chunk.AddConst(vm.StrConst(msg)))})
		}
	case *ThrowStmt:
		return c.throwStmt(s)
	case *TryStmt:
		return c.tryStmt(s)
	default:
		return fmt.Errorf("компилятор: неизвестный оператор %T", st)
	}
	return nil
}

// isTemporaryExpr сообщает, что выражение — временное (не переменная) в
// терминологии write-контекста Zend: литералы, `new`, константы, арифметика и
// т.п. Функции/методы и элементы/свойства переменных считаются переменными.
func isTemporaryExpr(e Expr) bool {
	switch v := e.(type) {
	case nil:
		return false
	case *VarExpr, *CallExpr, *MethodCallExpr, *StaticCallExpr, *DynCallExpr, *StaticPropExpr:
		return false
	case *PropExpr:
		return isTemporaryExpr(v.Recv)
	case *IndexExpr:
		return isTemporaryExpr(v.Arr)
	}
	return true
}

// cloneIntMap копирует map[string]int (пространство меток компилятора).
func cloneIntMap(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// cloneRegionMap копирует снимки стеков регионов для меток (тело foreach
// компилируется дважды, метки второго прохода восстанавливаются).
func cloneRegionMap(m map[string][]regionRef) map[string][]regionRef {
	out := make(map[string][]regionRef, len(m))
	for k, v := range m {
		out[k] = append([]regionRef(nil), v...)
	}
	return out
}

// gotoStmt компилирует `goto label;`: безусловный переход. Прямые метки
// патчатся сразу, forward-метки — при определении.
// checkFinallyJumps запрещает goto через границы finally-блоков (Zend:
// "jump into/out of a finally block is disallowed"). Возвращает ошибку с
// локацией самого goto.
func checkFinallyJumps(body []Stmt) error {
	type finInfo struct {
		fin int
	}
	labels := map[string]finInfo{}
	type gotoInfo struct {
		fin   int
		line  int
		label string
	}
	var gotos []gotoInfo
	var finStack []int
	// regStack: 1 — цикл/switch, 2 — finally. Нужен для break/continue,
	// выходящих из finally.
	var regStack []int
	var breakErr error
	nextFin := 1
	var walkS func(stmts []Stmt)
	walkS = func(stmts []Stmt) {
		pushRegion := func(kind int) { regStack = append(regStack, kind) }
		popRegion := func() { regStack = regStack[:len(regStack)-1] }
		_ = pushRegion
		_ = popRegion
		for _, st := range stmts {
			switch s := st.(type) {
			case *LabelStmt:
				f := -1
				if n := len(finStack); n > 0 {
					f = finStack[n-1]
				}
				labels[s.Name] = finInfo{f}
			case *GotoStmt:
				f := -1
				if n := len(finStack); n > 0 {
					f = finStack[n-1]
				}
				gotos = append(gotos, gotoInfo{f, s.Line, s.Label})
			case *BreakStmt:
				if breakErr == nil {
					need := s.Level
					if need < 1 {
						need = 1
					}
					for i := len(regStack) - 1; i >= 0; i-- {
						if regStack[i] == 2 {
							breakErr = &fatalError{msg: "jump out of a finally block is disallowed"}
							break
						}
						need--
						if need == 0 {
							break
						}
					}
				}
			case *ContinueStmt:
				if breakErr == nil {
					need := s.Level
					if need < 1 {
						need = 1
					}
					for i := len(regStack) - 1; i >= 0; i-- {
						if regStack[i] == 2 {
							breakErr = &fatalError{msg: "jump out of a finally block is disallowed"}
							break
						}
						need--
						if need == 0 {
							break
						}
					}
				}
			case *IfStmt:
				walkS(s.Then)
				walkS(s.Else)
			case *WhileStmt:
				pushRegion(1)
				walkS(s.Body)
				popRegion()
			case *DoWhileStmt:
				pushRegion(1)
				walkS(s.Body)
				popRegion()
			case *ForStmt:
				pushRegion(1)
				walkS(s.Body)
				popRegion()
			case *ForeachStmt:
				pushRegion(1)
				walkS(s.Body)
				popRegion()
			case *SwitchStmt:
				pushRegion(1)
				for _, cse := range s.Cases {
					walkS(cse.Body)
				}
				popRegion()
			case *BlockStmt:
				walkS(s.Body)
			case *TryStmt:
				walkS(s.Body)
				for _, cc := range s.Catches {
					walkS(cc.Body)
				}
				if len(s.Finally) > 0 {
					id := nextFin
					nextFin++
					finStack = append(finStack, id)
					regStack = append(regStack, 2)
					walkS(s.Finally)
					regStack = regStack[:len(regStack)-1]
					finStack = finStack[:len(finStack)-1]
				}
			}
		}
	}
	walkS(body)
	if breakErr != nil {
		return breakErr
	}
	for _, g := range gotos {
		l, ok := labels[g.label]
		if !ok {
			continue
		}
		if g.fin == -1 && l.fin != -1 {
			return &fatalError{msg: "jump into a finally block is disallowed", line: g.line}
		}
		if g.fin != -1 && l.fin != g.fin {
			return &fatalError{msg: "jump out of a finally block is disallowed", line: g.line}
		}
	}
	return nil
}

// containsLabelGotos сообщает, есть ли в списке операторов метки или goto
// (свёртка константного if тогда запрещена: метка из отброшенной ветви
// обязана резолвиться на этапе компиляции, как в Zend).
func containsLabelGotos(list []Stmt) bool {
	for _, st := range list {
		if containsLabelGoto(st) {
			return true
		}
	}
	return false
}

func containsLabelGoto(st Stmt) bool {
	switch x := st.(type) {
	case *BlockStmt:
		return containsLabelGotos(x.Body)
	case *LabelStmt, *GotoStmt:
		return true
	case *IfStmt:
		return containsLabelGotos(x.Then) || containsLabelGotos(x.Else)
	case *WhileStmt:
		return containsLabelGotos(x.Body)
	case *DoWhileStmt:
		return containsLabelGotos(x.Body)
	case *ForStmt:
		return containsLabelGotos(x.Body)
	case *ForeachStmt:
		return containsLabelGotos(x.Body)
	case *SwitchStmt:
		for _, cs := range x.Cases {
			if containsLabelGotos(cs.Body) {
				return true
			}
		}
	case *TryStmt:
		if containsLabelGotos(x.Body) || containsLabelGotos(x.Finally) {
			return true
		}
		for _, cc := range x.Catches {
			if containsLabelGotos(cc.Body) {
				return true
			}
		}
	}
	return false
}

// commonRegionPrefix возвращает длину общего префикса двух стеков регионов
// (сравнение по идентичности try-регионов и циклов).
func commonRegionPrefix(a, b []regionRef) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i].try == b[i].try && a[i].loop == b[i].loop {
		i++
	}
	return i
}

func (c *compiler) gotoStmt(label string) error {
	line := c.curLine
	if pc, ok := c.labels[label]; ok {
		if c.labelDepth[label] > c.loopDepth {
			return &fatalError{msg: "'goto' into loop or switch statement is disallowed", line: line}
		}
		// Обратный goto: метка уже определена — раскручиваем регионы,
		// которые покидает переход (Zend: zend_handle_loops_and_finally).
		keep := commonRegionPrefix(c.labelRegions[label], c.regions)
		if err := c.unwindToRegions(keep); err != nil {
			return err
		}
		at := c.chunk.Emit(vm.Instruction{Op: vm.OpJump, C: -1})
		c.setJump(at, pc)
		return nil
	}
	// Прямой goto: цель ещё не известна. Эмитим полную цепочку раскрутки
	// (как Zend) и запоминаем её части; defineLabel погасит в NOP те из них,
	// чьи регионы переход не покидает.
	regions := append([]regionRef(nil), c.regions...)
	parts, err := c.emitUnwindChain()
	if err != nil {
		return err
	}
	at := c.chunk.Emit(vm.Instruction{Op: vm.OpJump, C: -1})
	c.pendingGotos[label] = append(c.pendingGotos[label], pendingGoto{
		at: at, depth: c.loopDepth, line: line, regions: regions, parts: parts})
	return nil
}

// emitUnwindChain эмитит раскрутку всех активных регионов (от внутренних к
// внешним) и возвращает диапазон инструкций каждой части в порядке эмиссии
// (parts[len-1] — самый внешний регион). Стек регионов восстанавливается.
func (c *compiler) emitUnwindChain() ([]unwindPart, error) {
	saved := c.regions
	var parts []unwindPart
	for len(c.regions) > 0 {
		r := c.regions[len(c.regions)-1]
		c.regions = c.regions[:len(c.regions)-1]
		start := c.chunk.Len()
		if r.try != nil {
			if r.try.hasFinally {
				c.chunk.Emit(vm.Instruction{Op: vm.OpEnterFinally, C: int32(r.try.tiIdx)})
				if err := c.stmts(r.try.finally); err != nil {
					c.regions = saved
					return parts, err
				}
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpPopTry})
		} else if r.loop != nil {
			c.emitLoopReleases([]*loopCtx{r.loop})
		}
		parts = append(parts, unwindPart{start: start, end: c.chunk.Len()})
	}
	c.regions = saved
	return parts, nil
}

// defineLabel фиксирует метку `label:` на текущей позиции и патчит ожидающие
// переходы.
func (c *compiler) defineLabel(name string, line int) error {
	if _, ok := c.labels[name]; ok {
		return &fatalError{msg: "Label '" + name + "' already defined", line: line}
	}
	pc := c.chunk.Len()
	c.labels[name] = pc
	c.labelDepth[name] = c.loopDepth
	c.labelRegions[name] = append([]regionRef(nil), c.regions...)
	for _, g := range c.pendingGotos[name] {
		if c.loopDepth > g.depth {
			return &fatalError{msg: "'goto' into loop or switch statement is disallowed", line: g.line}
		}
		if n := len(g.parts); n > 0 {
			keep := commonRegionPrefix(g.regions, c.regions)
			if keep > 0 {
				// Невышедшие регионы — самые внешние, их части эмитированы
				// последними; гасим их (Zend: MAKE_NOP в
				// zend_resolve_goto_label).
				for _, p := range g.parts[n-keep:] {
					c.chunk.NopRange(p.start, p.end)
				}
			}
		}
		c.setJump(g.at, pc)
	}
	delete(c.pendingGotos, name)
	return nil
}

// finalizeGotos проверяет, что все goto нашли метку.
func (c *compiler) finalizeGotos() error {
	for name, gs := range c.pendingGotos {
		if len(gs) > 0 {
			return &fatalError{msg: "'goto' to undefined label '" + name + "'", line: gs[0].line}
		}
	}
	return nil
}

// throwStmt компилирует `throw $x;`.
func (c *compiler) throwStmt(s *ThrowStmt) error {
	c.emitLine(c.curLine)
	r, err := c.expr(s.X)
	if err != nil {
		return err
	}
	c.chunk.Emit(vm.Instruction{Op: vm.OpThrow, A: r})
	return nil
}

// tryStmt компилирует try/catch/finally.
//
// Раскладка байткода:
//
//	PUSH_TRY ti
//	<try body>
//	POP_TRY
//	JUMP lf
//	<c0>: storeVar($e); <catch0 body>; POP_TRY; JUMP lf
//	<c1>: ... (аналогично)
//	lf: <finally body>          (если есть)
//	FINALLY_END ti             (если есть finally)
//	end:
//
// VM переходит сразу на <ci>, кладя объект исключения в ExcReg.
func (c *compiler) tryStmt(s *TryStmt) error {
	if len(s.Catches) == 0 && !s.HasFinally {
		return &fatalError{msg: "Cannot use try without catch or finally", line: c.curLine}
	}
	// Регистр объекта исключения — временный, не именованный: именованные
	// регистры main «закреплены» (всегда корни в масках живости), и временное
	// значение, случайно раскрашенное в тот же регистр до входа в catch,
	// удерживалось бы маской до конца попытки (Zend освобождает его раньше,
	// от этого зависит нумерация #N). SSA-seed механизм всё равно обновит
	// TryCatch.Var до фактического цвета значения.
	excReg := c.alloc()
	sc := &tryScope{finally: s.Finally, hasFinally: s.HasFinally}
	tiIdx := c.chunk.AddTryInfo(vm.TryInfo{FinallyPC: -1})
	sc.tiIdx = tiIdx

	c.chunk.Emit(vm.Instruction{Op: vm.OpPushTry, C: int32(tiIdx)})

	// Циклы, созданные при компиляции тела: исключение из тела покидает их,
	// поэтому на входе catch/finally их темпы освобождаются (Zend:
	// cleanup_live_vars до перехода в обработчик).
	logMark := len(c.loopLog)
	depth := len(c.regions)
	c.regions = append(c.regions, regionRef{try: sc})
	if err := c.stmts(s.Body); err != nil {
		return err
	}
	c.regions = c.regions[:depth]
	bodyLoops := c.collectLoops(logMark)

	// нормальное завершение try-тела
	if sc.hasFinally {
		// Обработчик остаётся активным до FINALLY_END, чтобы throw внутри
		// finally не перезапустил его повторно.
		jNormal := c.chunk.Emit(vm.Instruction{Op: vm.OpJump})
		c.setJump(jNormal, -1) // заполним ниже (lf)
		_ = jNormal
	} else {
		c.chunk.Emit(vm.Instruction{Op: vm.OpPopTry})
		jNormal := c.chunk.Emit(vm.Instruction{Op: vm.OpJump})
		c.setJump(jNormal, -1)
		_ = jNormal
	}
	// jumps на нормальный конец заполним после вычисления end.
	normalJump := c.chunk.Len() - 1

	catches := make([]vm.TryCatch, 0, len(s.Catches))
	var jumps []int
	for _, cc := range s.Catches {
		if cc.Var == "$this" || cc.Var == "this" {
			return &fatalError{msg: "Cannot re-assign $this", line: cc.Line}
		}
		pc := c.chunk.Len()
		if cc.Var != "" {
			if c.isRef(cc.Var) {
				// ZEND_CATCH: привязка всегда СТРОГАЯ («we should not permit
				// coercion to string here»): запись в ref-переменную не
				// приводит объект исключения (напр. Stringable) к типу
				// свойства, держащего ссылку.
				var refReg uint8
				if c.useGlobal(cc.Var) {
					refReg = c.alloc()
					c.chunk.Emit(vm.Instruction{Op: vm.OpGlobalRefAddr, A: refReg,
						C: int32(c.chunk.AddConst(vm.StrConst(c.globalKey(cc.Var))))})
				} else {
					refReg = c.refs[cc.Var]
				}
				c.emitCall("__php_catch_bind", []uint8{refReg, excReg})
			} else {
				c.storeVar(cc.Var, excReg)
			}
			// Объект исключения теперь принадлежит переменной catch: регистр
			// \x00excN дальше не держит его (иначе хендл #N не возвращался бы
			// в free-list при переприсваивании переменной — Zend перемещает
			// исключение, а не копирует).
			c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: excReg})
		} else {
			// catch без переменной: Zend освобождает неиспользуемый объект
			// исключения сразу на входе (ZEND_CATCH: OBJ_RELEASE), поэтому
			// его деструктор срабатывает до тела catch и может выбросить
			// новое исключение. Чтение is_object нужно, чтобы диспетчер не
			// списал объект раньше этой точки (живость на входе catch), не
			// удерживая лишней ссылки; OpNull снимает его владение, а
			// ближайший OpSweep выполняет деструктор (исключение — из этой
			// инструкции, error-протокол).
			keep := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpIsObject, A: keep, B: excReg})
			c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: excReg})
			c.emitStmtSweep()
		}
		// Освобождение темпов циклов тела (Zend: cleanup_live_vars перед
		// переходом в обработчик) — ПОСЛЕ привязки catch-переменной: диспетчер
		// кладёт объект исключения в регистр до перехода, и запись блоком
		// раньше затёрла бы его (переменная catch оказалась бы null).
		c.emitLoopReleases(bodyLoops)
		depth = len(c.regions)
		c.regions = append(c.regions, regionRef{try: sc})
		if err := c.stmts(cc.Body); err != nil {
			return err
		}
		c.regions = c.regions[:depth]
		if !sc.hasFinally {
			c.chunk.Emit(vm.Instruction{Op: vm.OpPopTry})
		}
		jumps = append(jumps, c.chunk.Emit(vm.Instruction{Op: vm.OpJump}))
		catches = append(catches, vm.TryCatch{Classes: cc.Classes, Var: excReg, PC: int32(pc)})
	}

	lf := c.chunk.Len()
	finPC := int32(-1)
	if sc.hasFinally {
		finPC = int32(lf)
		// Нормальный вход в finally: темпы циклов тела уже освобождены
		// обычным выходом, поэтому блок — no-op; при входе по исключению он
		// освобождает покинутые циклы до тела finally.
		c.emitLoopReleases(bodyLoops)
		c.chunk.Emit(vm.Instruction{Op: vm.OpEnterFinally, C: int32(tiIdx)})
		if err := c.stmts(s.Finally); err != nil {
			return err
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpFinallyEnd, C: int32(tiIdx)})
	}
	end := c.chunk.Len()
	c.setJump(normalJump, lf)
	for _, j := range jumps {
		c.setJump(j, lf)
	}
	c.chunk.SetTryInfo(tiIdx, vm.TryInfo{Catches: catches, FinallyPC: finPC, EndPC: int32(end)})
	return nil
}

func (c *compiler) breakStmt(level, opKind int) error {
	// Операнд проверяется до контекста (как в Zend).
	if opKind == 1 {
		return c.newFatal("'break' operator accepts only positive integers")
	}
	if opKind == 2 {
		return c.newFatal("'break' operator with non-integer operand is no longer supported")
	}
	if level < 1 {
		return c.newFatal("'break' operator accepts only positive integers")
	}
	if len(c.loops) == 0 {
		return c.newFatal("'break' not in the 'loop' or 'switch' context")
	}
	if level > len(c.loops) {
		plural := ""
		if level != 1 {
			plural = "s"
		}
		return c.newFatal(fmt.Sprintf("Cannot 'break' %d level%s", level, plural))
	}
	idx := len(c.loops) - level
	if err := c.unwindToRegions(c.loops[idx].regDepth); err != nil {
		return err
	}
	j := c.chunk.Emit(vm.Instruction{Op: vm.OpJump})
	c.loops[idx].breakJumps = append(c.loops[idx].breakJumps, j)
	return nil
}

func (c *compiler) continueStmt(level, opKind int) error {
	// Операнд проверяется до контекста (как в Zend).
	if opKind == 1 {
		return c.newFatal("'continue' operator accepts only positive integers")
	}
	if opKind == 2 {
		return c.newFatal("'continue' operator with non-integer operand is no longer supported")
	}
	if level < 1 {
		return c.newFatal("'continue' operator accepts only positive integers")
	}
	if len(c.loops) == 0 {
		return c.newFatal("'continue' not in the 'loop' or 'switch' context")
	}
	if level > len(c.loops) {
		plural := ""
		if level != 1 {
			plural = "s"
		}
		return c.newFatal(fmt.Sprintf("Cannot 'continue' %d level%s", level, plural))
	}
	idx := len(c.loops) - level
	if err := c.unwindToRegions(c.loops[idx].regDepth); err != nil {
		return err
	}
	// continue, нацеленный на switch, в PHP 7.3+ эквивалентен break:
	// Zend печатает Warning с подсказкой continue N+1, если снаружи есть цикл.
	if c.loops[idx].isSwitch {
		lvl := ""
		if level > 1 {
			lvl = fmt.Sprintf(" %d", level)
		}
		msg := fmt.Sprintf(`"continue%s" targeting switch is equivalent to "break%s"`, lvl, lvl)
		if level < len(c.loops) && !c.loops[idx-1].isSwitch {
			msg += fmt.Sprintf(`. Did you mean to use "continue %d"?`, level+1)
		}
		compilerWarnings = append(compilerWarnings, fmt.Sprintf("%s in %s on line %d", msg, c.file, c.curLine))
	}
	j := c.chunk.Emit(vm.Instruction{Op: vm.OpJump})
	c.loops[idx].continueJumps = append(c.loops[idx].continueJumps, j)
	return nil
}

// collectLoops возвращает (без дублей) foreach-циклы, зарегистрированные в
// c.loopLog начиная с отметки mark.
func (c *compiler) collectLoops(mark int) []*loopCtx {
	if mark >= len(c.loopLog) {
		return nil
	}
	var out []*loopCtx
	for _, lc := range c.loopLog[mark:] {
		if len(lc.freeRegs) == 0 {
			continue
		}
		dup := false
		for _, prev := range out {
			if prev == lc {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, lc)
		}
	}
	return out
}

// emitLoopReleases освобождает временные регистры покинутых foreach-циклов:
// OpNull снимает удерживаемые значения, ближайший OpSweep выполняет
// деструкторы (исключение деструктора — из этой точки, error-протокол).
func (c *compiler) emitLoopReleases(loops []*loopCtx) {
	if len(loops) == 0 {
		return
	}
	n := 0
	for _, lc := range loops {
		for _, reg := range lc.freeRegs {
			c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: reg})
			n++
		}
	}
	if n > 0 {
		c.emitStmtSweep()
	}
}

// unwindToRegions прогоняет раскрутку активных регионов (foreach-циклов и
// try-регионов) от внутренних к внешним, пока не останется keep регионов:
// у try с finally выполняется копия finally-тела (и снимается обработчик), у
// цикла освобождаются временные регистры (Zend: zend_handle_loops_and_finally
// эмитит FAST_CALL/DISCARD_EXCEPTION/FREE в этом же порядке).
func (c *compiler) unwindToRegions(keep int) error {
	// Стек восстанавливается: код после return/break/continue в том же теле
	// всё ещё компилируется (пусть и недостижимый), и он должен видеть те же
	// активные регионы. Без восстановления, например, массивная ветка
	// foreach, скомпилированная после return в объектной ветке, потеряла бы
	// свой finally.
	saved := c.regions
	for len(c.regions) > keep {
		r := c.regions[len(c.regions)-1]
		c.regions = c.regions[:len(c.regions)-1]
		if r.try != nil {
			if r.try.hasFinally {
				c.chunk.Emit(vm.Instruction{Op: vm.OpEnterFinally, C: int32(r.try.tiIdx)})
				if err := c.stmts(r.try.finally); err != nil {
					c.regions = saved
					return err
				}
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpPopTry})
		} else if r.loop != nil {
			c.emitLoopReleases([]*loopCtx{r.loop})
		}
	}
	c.regions = saved
	return nil
}

// saveAcrossUnwind сохраняет значение r в стабильный регистр, если предстоит
// прогон finally-тела (чтобы временные регистры finally не затёрли его).
func (c *compiler) saveAcrossUnwind(r uint8) uint8 {
	if len(c.regions) == 0 {
		return r
	}
	v := c.varReg(c.internalName("save"))
	c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: v, B: r})
	return v
}

func (c *compiler) pushLoop() *loopCtx {
	lc := &loopCtx{}
	c.loops = append(c.loops, lc)
	c.loopDepth++
	c.regions = append(c.regions, regionRef{loop: lc})
	lc.regDepth = len(c.regions)
	c.loopLog = append(c.loopLog, lc)
	return lc
}

func (c *compiler) popLoop(lc *loopCtx) {
	if n := len(c.regions); n > 0 && c.regions[n-1].loop == lc {
		c.regions = c.regions[:n-1]
	}
	c.loopDepth--
	c.loops = c.loops[:len(c.loops)-1]
	for _, j := range lc.breakJumps {
		c.setJump(j, c.chunk.Len())
	}
}

func (c *compiler) ifStmt(s *IfStmt) error {
	// if с константным условием: компилируем только выбранную ветвь.
	// Ветвь с `yield` не выбрасываем: наличие yield делает функцию
	// генератором даже в недостижимом коде (синтаксическое свойство PHP).
	if cv, ok := foldConstExpr(s.Cond); ok &&
		!containsLabelGotos(s.Then) && !containsLabelGotos(s.Else) {
		if foldTruthy(cv) && !containsYieldStmts(s.Else) {
			return c.stmts(s.Then)
		}
		if !foldTruthy(cv) && !containsYieldStmts(s.Then) {
			if len(s.Else) > 0 {
				return c.stmts(s.Else)
			}
			return nil
		}
	}
	jf, err := c.emitCondJFalse(s.Cond)
	if err != nil {
		return err
	}
	if err := c.stmts(s.Then); err != nil {
		return err
	}
	if len(s.Else) == 0 {
		c.setJump(jf, c.chunk.Len())
		return nil
	}
	jend := c.chunk.Emit(vm.Instruction{Op: vm.OpJump})
	c.setJump(jf, c.chunk.Len())
	if err := c.stmts(s.Else); err != nil {
		return err
	}
	c.setJump(jend, c.chunk.Len())
	return nil
}

func (c *compiler) whileStmt(s *WhileStmt) error {
	start := c.chunk.Len()
	c.tmp = c.nvars
	jf, err := c.emitCondJFalse(s.Cond)
	if err != nil {
		return err
	}
	lc := c.pushLoop()
	if err := c.stmts(s.Body); err != nil {
		return err
	}
	for _, j := range lc.continueJumps {
		c.setJump(j, start)
	}
	c.chunk.Emit(vm.Instruction{Op: vm.OpJump, C: int32(start)})
	c.setJump(jf, c.chunk.Len())
	c.popLoop(lc)
	return nil
}

func (c *compiler) doWhileStmt(s *DoWhileStmt) error {
	start := c.chunk.Len()
	lc := c.pushLoop()
	if err := c.stmts(s.Body); err != nil {
		return err
	}
	cont := c.chunk.Len()
	for _, j := range lc.continueJumps {
		c.setJump(j, cont)
	}
	c.tmp = c.nvars
	r, err := c.expr(s.Cond)
	if err != nil {
		return err
	}
	c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfTrue, A: r, C: int32(start)})
	c.popLoop(lc)
	return nil
}

func (c *compiler) forStmt(s *ForStmt) error {
	for _, e := range s.Init {
		if _, err := c.expr(e); err != nil {
			return err
		}
	}
	start := c.chunk.Len()
	var jf = -1
	if len(s.Cond) == 1 {
		c.tmp = c.nvars
		j, err := c.emitCondJFalse(s.Cond[0])
		if err != nil {
			return err
		}
		jf = j
	} else if len(s.Cond) > 1 {
		c.tmp = c.nvars
		var cond uint8
		for i, e := range s.Cond {
			r, err := c.expr(e)
			if err != nil {
				return err
			}
			if i == 0 {
				cond = r
			} else {
				d := c.alloc()
				c.chunk.Emit(vm.Instruction{Op: vm.OpLogicalAnd, A: d, B: cond, C: int32(r)})
				cond = d
			}
		}
		jf = c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfFalse, A: cond})
	}
	lc := c.pushLoop()
	if err := c.stmts(s.Body); err != nil {
		return err
	}
	cont := c.chunk.Len()
	for _, j := range lc.continueJumps {
		c.setJump(j, cont)
	}
	c.tmp = c.nvars
	for _, e := range s.Post {
		if id, ok := e.(*IncDecExpr); ok {
			if err := c.incDecStmt(id); err != nil {
				return err
			}
			continue
		}
		if _, err := c.expr(e); err != nil {
			return err
		}
	}
	c.chunk.Emit(vm.Instruction{Op: vm.OpJump, C: int32(start)})
	if jf >= 0 {
		c.setJump(jf, c.chunk.Len())
	}
	c.popLoop(lc)
	return nil
}

// storeForeachTarget записывает значение из регистра r в цель цикла foreach:
// именованную переменную либо деструктуризацию (list/[]).
func (c *compiler) storeForeachTarget(name string, target Expr, r uint8) error {
	if target == nil {
		c.storeVar(name, r)
		return nil
	}
	_, err := c.assign(target, "=", &RegExpr{Reg: r}, false)
	return err
}

func (c *compiler) foreachStmt(s *ForeachStmt) error {
	// Ключом foreach не может быть деструктуризация list(...)/[...] (Zend:
	// "Cannot use list as key element").
	if s.KeyTarget != nil {
		// Ключом foreach не может быть деструктуризация list(...)/[...]
		// (Zend: "Cannot use list as key element"); свойства и элементы
		// массива — допустимые цели ключа.
		if _, isList := asListTarget(s.KeyTarget).(*ListExpr); isList {
			return c.newFatal("Cannot use list as key element")
		}
	}
	if s.KeyRef {
		c.emitLine(c.curLine)
		c.chunk.Emit(vm.Instruction{Op: vm.OpFatal, C: int32(c.chunk.AddConst(vm.StrConst("Key element cannot be a reference")))})
		return nil
	}
	// $GLOBALS — только чтение: цели foreach (в т.ч. по ссылке и в
	// деструктуризации) не могут быть $GLOBALS.
	if s.Val == "$GLOBALS" || s.Val == "GLOBALS" || s.Key == "$GLOBALS" || s.Key == "GLOBALS" ||
		isGlobalsVar(s.ValTarget) || isGlobalsVar(s.KeyTarget) {
		return c.newFatal(globalsModifyMsg)
	}
	// $this нельзя переприсваивать (в т.ч. как переменную цикла).
	if s.Val == "$this" || s.Val == "this" || s.Key == "$this" || s.Key == "this" ||
		isThisVar(s.ValTarget) || isThisVar(s.KeyTarget) {
		return c.newFatal("Cannot re-assign $this")
	}
	if containsNullsafe(s.ValTarget) || containsNullsafe(s.KeyTarget) {
		return c.newFatal("Can't use nullsafe operator in write context")
	}
	// Ссылочный список в значении foreach делает цикл «по ссылке» (Zend
	// zend_compile_foreach: propagate_list_refs → by_ref=1): FE_RESET_RW/
	// FE_FETCH_RW раздают элементы цикла ссылками, а деструктуризация
	// выполняется от этой ячейки.
	refListTarget := false
	if !s.ByRef {
		if le, ok := s.ValTarget.(*ListExpr); ok && listHasRefsDeep(le) {
			refListTarget = true
		}
	}
	rwIter := s.ByRef || refListTarget
	// Сначала резервируем все внутренние регистры (они должны пережить тело),
	// и только потом вычисляем выражение и тело — иначе временные регистры
	// могли бы перекрыть состояние итератора.
	src := c.varReg(c.internalName("src"))
	keys := c.varReg(c.internalName("keys"))
	i := c.varReg(c.internalName("i"))
	limit := c.varReg(c.internalName("limit"))

	// Источник RW-обхода (foreach по ссылке или ссылочная деструктуризация
	// значения) — ячейка-ссылка источника (Zend FE_RESET_RW делает переменную
	// ссылкой и итерирует её содержимое): записи в источник и через цель цикла
	// идут в одни и те же данные, а сам массив не считается разделённым, пока
	// на него не появится второй владелец. Для временных выражений ячейка не
	// нужна — обходится вычисленное значение.
	srcIsCell := false
	srcFreeable := true
	if rwIter {
		switch e := s.Expr.(type) {
		case *VarExpr:
			if e.NameExpr == nil &&
				e.Name != "$this" && e.Name != "this" && e.Name != "$GLOBALS" && e.Name != "GLOBALS" {
				switch {
				case c.isRef(e.Name):
					src = c.refCell(e.Name)
					srcIsCell = true
					if !c.useGlobal(e.Name) {
						srcFreeable = false
					}
				case !c.useGlobal(e.Name) && (c.isMain || c.writes[e.Name]):
					// Локальная/промотированная переменная-регистр: заводим
					// ячейку в её регистре (после проверки unset).
					c.emitUndefCheck(e.Name, c.exprLine(e))
					src = c.refCell(e.Name)
					srcIsCell = true
					srcFreeable = false
				case c.useGlobal(e.Name) && (c.isMain || c.globals[e.Name] || c.staticKeys[e.Name] != ""):
					// Переменная в таблице глобалов (в т.ч. main без
					// промоушена): сначала обычное чтение ради диагностики
					// "Undefined variable", затем итерируем ячейку.
					if _, err := c.expr(e); err != nil {
						return err
					}
					src = c.refCell(e.Name)
					srcIsCell = true
				}
			}
		case *IndexExpr, *PropExpr, *StaticPropExpr:
			// lvalue-источник: ячейка элемента/свойства — записи в источник из
			// тела цикла идут туда же (как FETCH_W в Zend).
			c.emitLine(c.exprLine(e))
			cell, err := c.refCellForExpr(e)
			if err != nil {
				return err
			}
			src = cell
			srcIsCell = true
		}
	}
	if !srcIsCell {
		r, err := c.expr(s.Expr)
		if err != nil {
			return err
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: src, B: r})
	}

	// Постоянные регистры итерации (переживают тело) — для by-ref и
	// ссылочной деструктуризации значения.
	var keyR, valR, cell, lim1 uint8
	var objPrevKey, objHavePrev uint8
	if rwIter {
		keyR = c.varReg(c.internalName("fkey"))
		valR = c.varReg(c.internalName("fval"))
		cell = c.varReg(c.internalName("fcell"))
		lim1 = c.varReg(c.internalName("flim1"))
		objPrevKey = c.varReg(c.internalName("fokey"))
		objHavePrev = c.varReg(c.internalName("fohp"))
	}

	// Если источник — объект, обходим его через Iterator/IteratorAggregate
	// (или по публичным свойствам). Иначе — прежний быстрый путь по массиву.
	iterState := c.varReg(c.internalName("iter"))
	c.tmp = c.nvars
	isobj := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpIsObject, A: isobj, B: src})
	jfObj := c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfFalse, A: isobj})

	// --- объектный цикл ---
	initC := int32(0)
	if rwIter {
		// C=1: iterInit должен проверить допустимость обхода по ссылке уже
		// после разрешения IteratorAggregate->getIterator().
		c.emitLine(c.curLine)
		initC = 1
	}
	c.chunk.Emit(vm.Instruction{Op: vm.OpIterInit, A: iterState, B: src, C: initC})
	if rwIter {
		c.chunk.Emit(vm.Instruction{Op: vm.OpFalse, A: objHavePrev})
	}
	objStart := c.chunk.Len()
	objCond := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpIterValid, A: objCond, B: iterState})
	objJF := c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfFalse, A: objCond})
	if rwIter {
		// Снять ссылку с предыдущего свойства (PHP оставляет ссылкой только
		// последнее обойдённое свойство).
		jnoprev := c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfFalse, A: objHavePrev})
		c.chunk.Emit(vm.Instruction{Op: vm.OpPropUnrefDyn, A: src, B: objPrevKey})
		c.setJump(jnoprev, c.chunk.Len())
	}
	objVal := c.alloc()
	if rwIter {
		c.chunk.Emit(vm.Instruction{Op: vm.OpIterCurrentRef, A: cell, B: iterState})
		objVal = cell
	} else {
		c.chunk.Emit(vm.Instruction{Op: vm.OpIterCurrent, A: objVal, B: iterState})
	}
	// PHP вызывает current() раньше key(); key() вызывается только если
	// ключ реально нужен в foreach.
	needKey := s.Key != "" || s.KeyTarget != nil || rwIter
	var objKey uint8
	if needKey {
		objKey = c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpIterKey, A: objKey, B: iterState})
	}
	if needKey && (s.Key != "" || s.KeyTarget != nil) {
		if err := c.storeForeachTarget(s.Key, s.KeyTarget, objKey); err != nil {
			return err
		}
	}
	if rwIter {
		c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: objPrevKey, B: objKey})
		c.chunk.Emit(vm.Instruction{Op: vm.OpTrue, A: objHavePrev})
		if refListTarget {
			if _, err := c.assignListCell(s.ValTarget.(*ListExpr), cell); err != nil {
				return err
			}
		} else if err := c.bindRefTarget(s.Val, s.ValTarget, cell); err != nil {
			return err
		}
	} else if err := c.storeForeachTarget(s.Val, s.ValTarget, objVal); err != nil {
		return err
	}
	// Тело компилируется дважды (объектный и массивный пути). Метки/goto в теле
	// должны принадлежать своей копии кода, иначе вторая копия даст
	// "Label ... already defined", а goto может перепрыгнуть в чужую ветку.
	// Снимок пространства меток до тела восстанавливается перед массивным путём.
	var savedLabels map[string]int
	var savedLabelDepth map[string]int
	var savedLabelRegions map[string][]regionRef
	if containsLabelGotos(s.Body) {
		savedLabels = cloneIntMap(c.labels)
		savedLabelDepth = cloneIntMap(c.labelDepth)
		savedLabelRegions = cloneRegionMap(c.labelRegions)
	}

	// Регистры цикла известны до компиляции тела: goto/return/break внутри
	// тела должны уметь их освободить (Zend знает FE_FREE на входе в цикл).
	// Только долгоживущие регистры итератора (varReg): временные регистры
	// текущего элемента массивного пути переиспользуются операторами тела и
	// в точке выхода могут держать живые значения — их не трогаем.
	freeObj := []uint8{src, iterState, keys, i, limit}
	if !srcFreeable {
		// src — регистр самой переменной-источника (ячейка): не сбрасываем.
		freeObj = freeObj[1:]
	}
	if rwIter {
		freeObj = append(freeObj, cell, lim1, objPrevKey, objHavePrev)
	}

	objLC := c.pushLoop()
	objLC.freeRegs = freeObj
	if err := c.stmts(s.Body); err != nil {
		return err
	}
	objCont := c.chunk.Len()
	for _, j := range objLC.continueJumps {
		c.setJump(j, objCont)
	}
	c.chunk.Emit(vm.Instruction{Op: vm.OpIterNext, B: iterState})
	c.chunk.Emit(vm.Instruction{Op: vm.OpJump, C: int32(objStart)})
	c.setJump(objJF, c.chunk.Len())
	c.popLoop(objLC)
	jEnd := c.chunk.Emit(vm.Instruction{Op: vm.OpJump})

	// --- массивный путь ---
	if savedLabels != nil {
		c.labels = savedLabels
		c.labelDepth = savedLabelDepth
		c.labelRegions = savedLabelRegions
	}
	c.setJump(jfObj, c.chunk.Len())

	c.emitLine(c.exprLine(s.Expr))
	arrOK := c.emitCall("__php_foreach_check", []uint8{src})
	jfNotArr := c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfFalse, A: arrOK})

	var arrLC *loopCtx
	if rwIter {
		// --- массив, RW-обход (foreach по ссылке или ссылочная
		// деструктуризация значения): позиционный обход живого массива ---
		// Текущий элемент становится настоящей ссылкой в массиве (см.
		// iterInitArrayRef/iterArrayRefCurrentRef): изменение массива в теле
		// видно итератору, запись через цель цикла — запись в элемент.
		c.chunk.Emit(vm.Instruction{Op: vm.OpIterInit, A: iterState, B: src, C: 2})
		start := c.chunk.Len()
		c.tmp = c.nvars
		cond := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpIterValid, A: cond, B: iterState})
		jf := c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfFalse, A: cond})
		c.chunk.Emit(vm.Instruction{Op: vm.OpIterKey, A: keyR, B: iterState})
		if s.Key != "" || s.KeyTarget != nil {
			if err := c.storeForeachTarget(s.Key, s.KeyTarget, keyR); err != nil {
				return err
			}
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpIterCurrentRef, A: cell, B: iterState})
		if refListTarget {
			if _, err := c.assignListCell(s.ValTarget.(*ListExpr), cell); err != nil {
				return err
			}
		} else if err := c.bindRefTarget(s.Val, s.ValTarget, cell); err != nil {
			return err
		}
		lc := c.pushLoop()
		arrLC = lc
		if rwIter {
			lc.freeRegs = append(append([]uint8{}, freeObj...), keyR, valR)
		} else {
			lc.freeRegs = append([]uint8{}, freeObj...)
		}
		if err := c.stmts(s.Body); err != nil {
			return err
		}
		cont := c.chunk.Len()
		for _, j := range lc.continueJumps {
			c.setJump(j, cont)
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpIterNext, B: iterState})
		c.chunk.Emit(vm.Instruction{Op: vm.OpJump, C: int32(start)})
		c.setJump(jf, c.chunk.Len())
		c.popLoop(lc)
	} else {
		// keys = array_keys(src)
		kr := c.emitCall("array_keys", []uint8{src})
		c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: keys, B: kr})

		zero := c.chunk.AddConst(vm.IntConst(0))
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: i, C: int32(zero)})
		c.chunk.Emit(vm.Instruction{Op: vm.OpLen, A: limit, B: keys})

		start := c.chunk.Len()
		c.tmp = c.nvars
		cond := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpLessInt, A: cond, B: i, C: int32(limit)})
		jf := c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfFalse, A: cond})

		keyR = c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpIndex, A: keyR, B: keys, C: int32(i)})
		valR = c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpIndex, A: valR, B: src, C: int32(keyR)})
		if s.Key != "" || s.KeyTarget != nil {
			if err := c.storeForeachTarget(s.Key, s.KeyTarget, keyR); err != nil {
				return err
			}
		}
		if err := c.storeForeachTarget(s.Val, s.ValTarget, valR); err != nil {
			return err
		}

		lc := c.pushLoop()
		arrLC = lc
		if rwIter {
			lc.freeRegs = append(append([]uint8{}, freeObj...), keyR, valR)
		} else {
			lc.freeRegs = append([]uint8{}, freeObj...)
		}
		if err := c.stmts(s.Body); err != nil {
			return err
		}
		cont := c.chunk.Len()
		for _, j := range lc.continueJumps {
			c.setJump(j, cont)
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpAddIntImm, A: i, B: i, C: 1})
		c.chunk.Emit(vm.Instruction{Op: vm.OpJump, C: int32(start)})
		c.setJump(jf, c.chunk.Len())
		c.popLoop(lc)
	}
	c.setJump(jEnd, c.chunk.Len())
	c.setJump(jfNotArr, c.chunk.Len())
	// Конец цикла: Zend на выходе (в т.ч. по break) освобождает итератор
	// (FE_FREE) — источник и его состояние не должны удерживать объект
	// (генератор, Iterator) до конца области. Сбрасываем регистры цикла
	// в null: для отслеживаемых значений это списание ссылок, и цикл по
	// генератору становится собираемым (Zend/tests/generators/gh9750-*).
	free := []uint8{src, iterState, keys, i, limit, keyR, valR}
	if !srcFreeable {
		// src — регистр самой переменной-источника (ячейка): не сбрасываем.
		free = free[1:]
	}
	if rwIter {
		free = append(free, cell, lim1, objPrevKey, objHavePrev)
	}
	for _, reg := range free {
		c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: reg})
	}
	_ = arrLC
	return nil
}

func (c *compiler) switchStmt(s *SwitchStmt) error {
	subj, err := c.expr(s.Subject)
	if err != nil {
		return err
	}
	// Регистр subj должен дожить до конца сравнений: не сбрасываем tmp ниже.
	base := c.tmp
	lc := c.pushLoop()
	lc.isSwitch = true
	n := len(s.Cases)
	jumps := make([]int, n)
	defIdx := -1
	for i, cs := range s.Cases {
		if cs.Default {
			defIdx = i
			jumps[i] = -1
			continue
		}
		c.tmp = base
		cond, err := c.expr(cs.Cond)
		if err != nil {
			return err
		}
		cmp := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpEqual, A: cmp, B: subj, C: int32(cond)})
		jumps[i] = c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfTrue, A: cmp})
	}
	var noMatch = -1
	if defIdx >= 0 {
		jumps[defIdx] = c.chunk.Emit(vm.Instruction{Op: vm.OpJump})
	} else {
		noMatch = c.chunk.Emit(vm.Instruction{Op: vm.OpJump})
	}
	for i, cs := range s.Cases {
		if jumps[i] >= 0 {
			c.setJump(jumps[i], c.chunk.Len())
		}
		if err := c.stmts(cs.Body); err != nil {
			return err
		}
	}
	end := c.chunk.Len()
	if noMatch >= 0 {
		c.setJump(noMatch, end)
	}
	c.popLoop(lc)
	return nil
}

// emitCall генерирует OpCall с аргументами в регистрах argRegs.
func (c *compiler) emitCall(name string, argRegs []uint8) uint8 {
	i, ok := c.idx[name]
	if !ok {
		panic("unknown builtin " + name)
	}
	return c.emitCallRegs(i, argRegs, nil)
}

// emitClassGuard эмитит вызов __php_require_class(name, isIface): запускает
// автозагрузку для неизвестного родителя/интерфейса и бросает Error, если
// класс так и не был определён.
func (c *compiler) emitClassGuard(name string, isIface bool) {
	nameReg := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nameReg,
		C: int32(c.chunk.AddConst(vm.StrConst(name)))})
	ifaceReg := c.alloc()
	if isIface {
		c.chunk.Emit(vm.Instruction{Op: vm.OpTrue, A: ifaceReg})
	} else {
		c.chunk.Emit(vm.Instruction{Op: vm.OpFalse, A: ifaceReg})
	}
	c.emitCall("__php_require_class", []uint8{nameReg, ifaceReg})
}

// yieldExpr компилирует выражение генератора в опкод OpYield: значение
// вычисляется при исполнении тела (лениво), ключ — из x.Key либо авто-ключ
// (B = YieldNoKey). Результат yield-выражения (значение send/next) попадёт в
// свежий регистр dst при resume. Наличие yield помечает функцию генератором.
func (c *compiler) yieldExpr(x *YieldExpr) (uint8, error) {
	if c.isMain {
		return 0, c.newFatal(`The "yield" expression can only be used inside a function`)
	}
	c.hasYield = true
	if x.From {
		if c.byrefReturn {
			return 0, c.newFatal(`Cannot use "yield from" inside a by-reference generator`)
		}
		return c.yieldFromExpr(x.Val)
	}
	val := x.Val
	// Генератор, объявленный `function &`, может yield только переменные;
	// иначе Zend выдаёт Notice (значение всё равно yield-ится как обычно).
	// Голый `yield;` (null-значение без выражения) notice не даёт.
	explicit := val != nil
	if c.byrefReturn && explicit && containsNullsafe(val) {
		// `yield $obj?->x` в by-ref генераторе: цепочку нельзя взять по ссылке.
		return 0, c.newFatal("Cannot take reference of a nullsafe chain")
	}
	if val == nil {
		val = &NullLit{}
	}
	// Ключ вычисляется раньше значения (влияет на порядок создания объектов).
	var kr uint8
	hasKey := x.Key != nil
	if hasKey {
		var err error
		kr, err = c.expr(x.Key)
		if err != nil {
			return 0, err
		}
	}
	byRefValue := c.byrefReturn && explicit && c.yieldByRefOK(val)
	var vr uint8
	var err error
	if byRefValue {
		// yield-by-ref: отдаём Ref-ячейку переменной/элемента, чтобы запись
		// через foreach по ссылке у вызывающего меняла исходную переменную.
		vr, err = c.refCellForExpr(val)
	} else {
		if c.byrefReturn && explicit && !c.yieldByRefOK(val) {
			c.emitRefNotice("Only variable references should be yielded by reference")
		}
		vr, err = c.expr(val)
	}
	if err != nil {
		return 0, err
	}
	dst := c.alloc()
	if hasKey {
		c.chunk.Emit(vm.Instruction{Op: vm.OpYield, A: vr, B: kr, C: int32(dst)})
	} else {
		c.chunk.Emit(vm.Instruction{Op: vm.OpYield, A: vr, B: vm.YieldNoKey, C: int32(dst)})
	}
	return dst, nil
}

// isRefLValue сообщает, что выражение является lvalue, для которого можно
// получить ссылку (элемент массива, свойство).
func isRefLValue(e Expr) bool {
	switch e.(type) {
	case *IndexExpr, *PropExpr, *StaticPropExpr:
		return true
	}
	return false
}

// yieldByRefOK сообщает, что выражение можно yield-ить/возвращать по ссылке:
// переменная, её элемент/свойство или вызов функции, объявленной `function &`.
func (c *compiler) yieldByRefOK(e Expr) bool {
	switch v := e.(type) {
	case *VarExpr, *IndexExpr, *PropExpr, *StaticPropExpr:
		return true
	case *CallExpr:
		if i, ok := c.idx[v.Name]; ok && c.byRefNonGen(i) {
			return true
		}
		if v.Fallback != "" {
			if i, ok := c.idx[v.Fallback]; ok && c.byRefNonGen(i) {
				return true
			}
		}
	case *MethodCallExpr, *StaticCallExpr:
		// Вызов метода, объявленного как `function &` — резолвится на рантайме;
		// чтобы не терять yield-by-ref, notice здесь не выдаём.
		return true
	}
	return false
}

// byRefNonGen сообщает, что функция по индексу i объявлена `function &` и не
// является генератором (вызов генератора всегда возвращает объект, а не
// ссылку, поэтому для by-ref return/yield к нему не применим).
func (c *compiler) byRefNonGen(i int) bool {
	if i < 0 || i >= len(c.funcs) || !c.funcs[i].ByRefReturn {
		return false
	}
	if cf, ok := c.funcs[i].Executable.(*vm.CompiledFunction); ok {
		return !cf.IsGenerator()
	}
	return true
}

// emitRefNotice эмитит Notice с заданным текстом (by-ref yield/return).
func (c *compiler) emitRefNotice(msg string) {
	c.emitCall("__php_ref_notice", []uint8{c.refMsgReg(msg)})
}

// refMsgReg кладёт текст диагностики в регистр.
func (c *compiler) refMsgReg(msg string) uint8 {
	mr := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: mr,
		C: int32(c.chunk.AddConst(vm.StrConst(msg)))})
	return mr
}

// isCallExpr сообщает, что выражение — вызов функции/метода.
func isCallExpr(e Expr) bool {
	switch e.(type) {
	case *CallExpr, *MethodCallExpr, *StaticCallExpr, *DynCallExpr:
		return true
	}
	return false
}

// yieldFromExpr компилирует `yield from $it`: разворачивается в цикл обхода
// итерируемого с OpYield на каждом шаге (с флагом YieldFromBit, чтобы ключи
// внутреннего не сдвигали авто-ключ внешнего генератора). Значение выражения —
// return внутреннего генератора (для массивов/итераторов NULL).
func (c *compiler) yieldFromExpr(srcExpr Expr) (uint8, error) {
	src := c.varReg(c.internalName("yfsrc"))
	r, err := c.expr(srcExpr)
	if err != nil {
		return 0, err
	}
	c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: src, B: r})

	res := c.varReg(c.internalName("yfres"))
	c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: res})

	isobj := c.varReg(c.internalName("yfobj"))
	c.chunk.Emit(vm.Instruction{Op: vm.OpIsObject, A: isobj, B: src})
	jfObj := c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfFalse, A: isobj})

	// --- объектный путь: Iterator/IteratorAggregate, включая Generator ---
	iterState := c.varReg(c.internalName("yfiter"))
	objKey := c.varReg(c.internalName("yfkey"))
	objVal := c.varReg(c.internalName("yfval"))
	objDst := c.varReg(c.internalName("yfdst"))
	objCond := c.varReg(c.internalName("yfcond"))

	sr := c.emitCall("__php_yield_from_begin", []uint8{src})
	c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: iterState, B: sr})
	objLoop := c.chunk.Len()
	c.chunk.Emit(vm.Instruction{Op: vm.OpIterValid, A: objCond, B: iterState})
	objEnd := c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfFalse, A: objCond})
	c.chunk.Emit(vm.Instruction{Op: vm.OpIterCurrent, A: objVal, B: iterState})
	c.chunk.Emit(vm.Instruction{Op: vm.OpIterKey, A: objKey, B: iterState})
	c.emitCall("__php_yield_from_set", []uint8{src})
	c.chunk.Emit(vm.Instruction{Op: vm.OpYield, A: objVal, B: objKey, C: int32(objDst) | vm.YieldFromBit})
	c.chunk.Emit(vm.Instruction{Op: vm.OpIterNext, B: iterState})
	c.chunk.Emit(vm.Instruction{Op: vm.OpJump, C: int32(objLoop)})
	c.setJump(objEnd, c.chunk.Len())
	jEnd := c.chunk.Emit(vm.Instruction{Op: vm.OpJump})

	// --- массивный путь ---
	c.setJump(jfObj, c.chunk.Len())
	keys := c.varReg(c.internalName("yfkeys"))
	kr := c.emitCall("array_keys", []uint8{src})
	c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: keys, B: kr})
	i := c.varReg(c.internalName("yfi"))
	limit := c.varReg(c.internalName("yflim"))
	arrKey := c.varReg(c.internalName("yfak"))
	arrVal := c.varReg(c.internalName("yfav"))
	arrDst := c.varReg(c.internalName("yfad"))
	arrCond := c.varReg(c.internalName("yfac"))
	zero := c.chunk.AddConst(vm.IntConst(0))
	c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: i, C: int32(zero)})
	c.chunk.Emit(vm.Instruction{Op: vm.OpLen, A: limit, B: keys})
	arrLoop := c.chunk.Len()
	c.chunk.Emit(vm.Instruction{Op: vm.OpLessInt, A: arrCond, B: i, C: int32(limit)})
	arrEnd := c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfFalse, A: arrCond})
	c.chunk.Emit(vm.Instruction{Op: vm.OpIndex, A: arrKey, B: keys, C: int32(i)})
	c.chunk.Emit(vm.Instruction{Op: vm.OpIndex, A: arrVal, B: src, C: int32(arrKey)})
	c.emitCall("__php_yield_from_set", []uint8{src})
	c.chunk.Emit(vm.Instruction{Op: vm.OpYield, A: arrVal, B: arrKey, C: int32(arrDst) | vm.YieldFromBit})
	c.chunk.Emit(vm.Instruction{Op: vm.OpAddIntImm, A: i, B: i, C: 1})
	c.chunk.Emit(vm.Instruction{Op: vm.OpJump, C: int32(arrLoop)})
	c.setJump(arrEnd, c.chunk.Len())
	c.setJump(jEnd, c.chunk.Len())

	ret := c.emitCall("__php_yield_from_ret", []uint8{src})
	c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: res, B: ret})
	return res, nil
}

// evalCall компилирует динамический eval(EXPR): исходный код вычисляется во
// время работы и исполняется вложенной VM.
func (c *compiler) evalCall(x *CallExpr) (uint8, error) {
	if len(x.Args) != 1 {
		return 0, fmt.Errorf("компилятор: eval() ожидает один аргумент")
	}
	r, err := c.expr(x.Args[0])
	if err != nil {
		return 0, err
	}
	fileReg := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: fileReg, C: int32(c.chunk.AddConst(vm.StrConst(c.file)))})
	lineReg := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: lineReg, C: int32(c.chunk.AddConst(vm.IntConst(int64(c.exprLine(x)))))})
	return c.emitCall("__php_eval", []uint8{r, fileReg, lineReg}), nil
}

// includeExpr компилирует include/require с динамическим путём: путь
// вычисляется во время работы, файл читается и исполняется вложенной VM.
// В arg2 упакованы флаги: бит 0 — *_once, бит 1 — require.
func (c *compiler) includeExpr(x *IncludeExpr) (uint8, error) {
	p, err := c.expr(x.Path)
	if err != nil {
		return 0, err
	}
	fileReg := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: fileReg, C: int32(c.chunk.AddConst(vm.StrConst(c.file)))})
	flagReg := c.alloc()
	flags := 0
	if x.Once {
		flags |= 1
	}
	if x.Require {
		flags |= 2
	}
	c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: flagReg, C: int32(c.chunk.AddConst(vm.IntConst(int64(flags))))})
	return c.emitCall("__php_include", []uint8{p, fileReg, flagReg}), nil
}

// emitCallRegs генерирует OpCall по индексу функции, размещая аргументы в
// непрерывном окне и выполняя writeback-и by-ref аргументов после вызова.
func (c *compiler) emitCallRegs(fi int, argRegs []uint8, wbs []func()) uint8 {
	want := c.funcs[fi].NumArgs
	nreg := len(argRegs)
	if want > nreg {
		nreg = want
	}
	if nreg < 1 {
		nreg = 1
	}
	base := c.alloc()
	for k := 1; k < nreg; k++ {
		c.alloc()
	}
	for k, a := range argRegs {
		dst := base + uint8(k)
		if dst != a {
			c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: dst, B: a})
		}
	}
	// Недостающие позиции фиксированной арности заполняем null, чтобы VM не
	// читала «мусорные» регистры за окном аргументов (важно для аллокатора).
	if want > len(argRegs) {
		for k := len(argRegs); k < want; k++ {
			c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: base + uint8(k)})
		}
	}
	packed := int32(fi) | int32(len(argRegs)+1)<<24
	c.chunk.Emit(vm.Instruction{Op: vm.OpCall, A: base, B: base, C: packed})
	c.tmp = base + 1
	for _, wb := range wbs {
		wb()
	}
	return base
}

// maxRegCallArgs — порог числа аргументов, выше которого вызов компилируется
// через упаковку аргументов в массив (OpCallArr). Регистровый вызов хранит
// аргументы в непрерывном окне, а индексы регистров и упакованный argc —
// 8-битные, поэтому очень длинные списки аргументов нельзя разместить в
// регистровом файле (иначе переполнение индексов и паника в VM).
const maxRegCallArgs = 64

// emitCallArr компилирует вызов с упаковкой аргументов в массив. Каждый
// аргумент вычисляется и добавляется в массив; временные регистры
// освобождаются после каждого аргумента, поэтому размер кадра не зависит от
// длины списка. By-ref аргументы сохраняют свои Ref-ячейки до writeback.
func (c *compiler) emitCallArr(fi int, args []Expr, mask []bool, info byRefInfo) (uint8, error) {
	arrReg := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpArray, A: arrReg})
	c.tmp = arrReg + 1
	cellMax := arrReg
	var wbs []func()
	for i, a := range args {
		var r uint8
		var wb func()
		var err error
		if i < len(mask) && mask[i] {
			r, wb, err = c.byRefArgInfo(a, info, i)
			// Ref-ячейки by-ref аргументов должны дожить до writeback после
			// вызова, поэтому их регистры не переиспользуются.
			if r > cellMax {
				cellMax = r
			}
		} else {
			r, err = c.expr(a)
		}
		if err != nil {
			return 0, err
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpAppend, A: arrReg, B: r})
		if wb != nil {
			wbs = append(wbs, wb)
		}
		c.tmp = cellMax + 1
	}
	dst := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpCallArr, A: dst, B: arrReg, C: int32(fi)})
	c.tmp = dst + 1
	for _, wb := range wbs {
		wb()
	}
	return dst, nil
}

// hasNamedArg сообщает, есть ли в списке аргументов именованный аргумент.
func hasNamedArg(args []Expr) bool {
	for _, a := range args {
		if _, ok := a.(*NamedArgExpr); ok {
			return true
		}
	}
	return false
}

// hasArgSpread сообщает, есть ли в списке аргументов распаковка `...`.
func hasArgSpread(args []Expr) bool {
	for _, a := range args {
		if _, ok := a.(*ArgSpreadExpr); ok {
			return true
		}
	}
	return false
}

// argOrderError проверяет порядок аргументов (Zend, zend_compile_args):
// после именованного аргумента позиционные и распаковка запрещены.
func argOrderError(args []Expr) string {
	namedSeen := false
	for _, a := range args {
		switch a.(type) {
		case *NamedArgExpr:
			namedSeen = true
		case *ArgSpreadExpr:
			if namedSeen {
				return "Cannot use argument unpacking after named arguments"
			}
		default:
			if namedSeen {
				return "Cannot use positional argument after named argument"
			}
		}
	}
	return ""
}

// namedByRefInfo описывает by-ref параметры известной на этапе компиляции
// функции: именованный аргумент в такую позицию Zend оборачивает в ссылку на
// вызове (SEND_VAR_EX), поэтому в массиве аргументов лежит Ref-ячейка.
type namedByRefInfo struct {
	names    []string
	byRef    []bool
	variadic bool
	varByRef bool
	display  string
}

func namedByRefInfoFor(fn *vm.Function) *namedByRefInfo {
	if fn == nil || len(fn.ByRefArgs) == 0 {
		return nil
	}
	disp := fn.Name
	if fn.DisplayName != "" {
		disp = fn.DisplayName
	} else if fn.Scope != nil && disp != "" && !strings.Contains(disp, "::") &&
		!strings.HasPrefix(disp, "{closure}") {
		disp = vm.ClassDisplayName(fn.Scope.Name()) + "::" + disp
	}
	return &namedByRefInfo{names: fn.ParamNames, byRef: fn.ByRefArgs,
		variadic: fn.NumArgs < 0, varByRef: fn.VariadicByRef, display: disp}
}

// byRefPos сообщает, что позиция i (0-based) — by-ref параметр.
func (n *namedByRefInfo) byRefPos(i int) bool {
	if n == nil {
		return false
	}
	fixed := len(n.byRef)
	if n.variadic {
		fixed--
	}
	if i < fixed {
		return n.byRef[i]
	}
	return n.variadic && n.varByRef
}

// isByRef сообщает, попадает ли именованный аргумент в by-ref позицию.
func (n *namedByRefInfo) isByRef(name string) bool {
	if n == nil {
		return false
	}
	for i, p := range n.names {
		if p == name {
			return i < len(n.byRef) && n.byRef[i]
		}
	}
	return n.variadic && n.varByRef
}

// namedArgRefValue вычисляет именованный аргумент для by-ref позиции: для
// переменной это её Ref-ячейка (алиас, как SEND_VAR_EX), иначе — значение.
func (c *compiler) namedArgRefValue(e Expr) (uint8, error) {
	if v, ok := e.(*VarExpr); ok && v.NameExpr == nil &&
		v.Name != "$this" && v.Name != "this" && !isGlobalsVar(v) {
		return c.refCell(v.Name), nil
	}
	return c.expr(e)
}

// buildArgArray собирает аргументы вызова в массив, раскрывая `...$args`
// (позиционные — по порядку, именованные — по строковому ключу). nbi != nil
// для известной функции — именованные аргументы by-ref позиций оборачиваются
// в ссылки.
func (c *compiler) buildArgArray(args []Expr, argMode bool, nbi *namedByRefInfo) (uint8, error) {
	arr := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpArray, A: arr})
	c.tmp = arr + 1
	spreadName := "__php_spread"
	if argMode {
		spreadName = "__php_spread_args"
	}
	posIdx := 0      // число уже упакованных позиционных аргументов
	posKnown := true // после распаковки неизвестной длины — false
	for _, a := range args {
		switch x := a.(type) {
		case *ArgSpreadExpr:
			if nbi != nil && argMode {
				// Маска by-ref позиций относительна началу распаковки (бит j —
				// позиция start+j); start известен компилятору, если до
				// распаковки не было других распаковок, иначе (-1) считается
				// рантаймом по числу позиционных аргументов.
				var mask uint64
				for j := 0; j < 64; j++ {
					if nbi.byRefPos(j) {
						mask |= 1 << uint(j)
					}
				}
				if mask != 0 || nbi.varByRef {
					var src uint8
					var err error
					switch x.X.(type) {
					case *VarExpr, *IndexExpr, *PropExpr, *StaticPropExpr:
						src, err = c.refCellForExpr(x.X)
					default:
						v, verr := c.expr(x.X)
						if verr != nil {
							return 0, verr
						}
						src = c.alloc()
						c.chunk.Emit(vm.Instruction{Op: vm.OpNewRef, A: src, B: v})
					}
					if err != nil {
						return 0, err
					}
					startPos := -1
					if posKnown {
						startPos = posIdx
					}
					start := c.alloc()
					c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: start,
						C: int32(c.chunk.AddConst(vm.IntConst(int64(startPos))))})
					maskReg := c.alloc()
					c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: maskReg,
						C: int32(c.chunk.AddConst(vm.IntConst(int64(mask))))})
					varByRef := c.alloc()
					if nbi.varByRef {
						c.chunk.Emit(vm.Instruction{Op: vm.OpTrue, A: varByRef})
					} else {
						c.chunk.Emit(vm.Instruction{Op: vm.OpFalse, A: varByRef})
					}
					nameReg := c.alloc()
					c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nameReg,
						C: int32(c.chunk.AddConst(vm.StrConst(nbi.display)))})
					namesReg := c.alloc()
					c.chunk.Emit(vm.Instruction{Op: vm.OpArray, A: namesReg})
					for _, pn := range nbi.names {
						pr := c.alloc()
						c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: pr,
							C: int32(c.chunk.AddConst(vm.StrConst(pn)))})
						c.chunk.Emit(vm.Instruction{Op: vm.OpAppend, A: namesReg, B: pr})
					}
					byrefsReg := c.alloc()
					c.chunk.Emit(vm.Instruction{Op: vm.OpArray, A: byrefsReg})
					for _, br := range nbi.byRef {
						pr := c.alloc()
						if br {
							c.chunk.Emit(vm.Instruction{Op: vm.OpTrue, A: pr})
						} else {
							c.chunk.Emit(vm.Instruction{Op: vm.OpFalse, A: pr})
						}
						c.chunk.Emit(vm.Instruction{Op: vm.OpAppend, A: byrefsReg, B: pr})
					}
					arr = c.emitCall(spreadName, []uint8{arr, src, start, maskReg, varByRef, nameReg, namesReg, byrefsReg})
					posKnown = false
					continue
				}
			}
			src, err := c.expr(x.X)
			if err != nil {
				return 0, err
			}
			arr = c.emitCall(spreadName, []uint8{arr, src})
			posKnown = false
		case *NamedArgExpr:
			var v uint8
			var err error
			if nbi.isByRef(x.Name) {
				v, err = c.namedArgRefValue(x.X)
			} else {
				v, err = c.expr(x.X)
			}
			if err != nil {
				return 0, err
			}
			kr := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: kr, C: int32(c.chunk.AddConst(vm.StrConst(x.Name)))})
			// Повтор имени (в т.ч. поверх ключа из распаковки) — ошибка Zend
			// «Named parameter $x overwrites previous argument».
			c.emitCall("__php_named_arg_check", []uint8{arr, kr})
			c.chunk.Emit(vm.Instruction{Op: vm.OpIndexSet, A: arr, B: kr, C: int32(v)})
		default:
			var v uint8
			var err error
			if nbi.byRefPos(posIdx) {
				// Позиционный аргумент в by-ref позиции: ссылка (SEND_VAR_EX).
				v, err = c.namedArgRefValue(a)
			} else {
				v, err = c.expr(a)
			}
			if err != nil {
				return 0, err
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpAppend, A: arr, B: v})
			if posKnown {
				posIdx++
			}
		}
	}
	return arr, nil
}

// callArrWithSpread вызывает функцию fi, собрав аргументы в массив.
func (c *compiler) callArrWithSpread(fi int, args []Expr) (uint8, error) {
	if msg := argOrderError(args); msg != "" {
		return 0, &fatalError{msg: msg, line: c.exprLine(args[len(args)-1])}
	}
	var fn *vm.Function
	if fi >= 0 && fi < len(c.funcs) {
		fn = c.funcs[fi]
	}
	arr, err := c.buildArgArray(args, true, namedByRefInfoFor(fn))
	if err != nil {
		return 0, err
	}
	dst := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpCallArr, A: dst, B: arr, C: int32(fi)})
	c.tmp = dst + 1
	return dst, nil
}

// evalArgs вычисляет аргументы вызова, обрабатывая by-ref позиции (mask).
// Возвращает регистры аргументов и writeback-и, которые нужно выполнить
// после вызова.
// dynCallByName компилирует вызов функции, имя которой неизвестно на этапе
// компиляции: имя кладётся в регистр, OpCallDyn разрешает его в рантайме.
func (c *compiler) dynCallByName(name string, args []Expr) (uint8, error) {
	return c.dynCallByNameRef(name, args, nil)
}

// dynCallByNameRef — dynCallByName с информацией о by-ref параметрах известной
// функции (для именованных аргументов).
func (c *compiler) dynCallByNameRef(name string, args []Expr, nbi *namedByRefInfo) (uint8, error) {
	if hasNamedArg(args) {
		if msg := argOrderError(args); msg != "" {
			return 0, &fatalError{msg: msg, line: c.exprLine(args[len(args)-1])}
		}
		nameReg, err := c.expr(&StrLit{V: name})
		if err != nil {
			return 0, err
		}
		arr, err := c.buildArgArray(args, true, nbi)
		if err != nil {
			return 0, err
		}
		return c.emitCall("__php_call_named", []uint8{nameReg, arr}), nil
	}
	// Целевая функция неизвестна на этапе компиляции (обычно объявлена во
	// включаемом файле). Передаём переменные-аргументы Ref-ячейками: для
	// by-ref параметров это обеспечивает writeback, для by-value вызываемая
	// функция разыменует значение. Встроенные функции сюда не попадают (они
	// всегда есть в таблице), поэтому ABI нативных вызовов не затрагивается.
	argRegs := make([]uint8, len(args))
	var wbs []func()
	for i, a := range args {
		r, wb, err := c.byRefArgInfo(a, byRefInfo{silent: true}, -1)
		if err != nil {
			return 0, err
		}
		argRegs[i] = r
		if wb != nil {
			wbs = append(wbs, wb)
		}
	}
	if len(argRegs) >= 250 {
		return 0, fmt.Errorf("компилятор: слишком много аргументов для динамического вызова %q", name)
	}
	base := c.alloc()
	for range argRegs {
		c.alloc()
	}
	c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: base, C: int32(c.chunk.AddConst(vm.StrConst(name)))})
	for k, a := range argRegs {
		dst := base + 1 + uint8(k)
		if dst != a {
			c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: dst, B: a})
		}
	}
	d := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpCallDyn, A: d, B: base, C: int32(len(argRegs))})
	c.tmp = d + 1
	for _, wb := range wbs {
		wb()
	}
	return d, nil
}

// wrapVarDump оборачивает встроенный var_dump: enum-кейсы печатаются как
// `enum(Class::Case)`, прочие значения делегируются исходной функции.
func wrapVarDump(orig *vm.Function) *vm.Function {
	fn := vm.Register("var_dump", func(ctx *vm.Context) vm.Value {
		args := ctx.Args()
		if len(args) == 1 {
			if s, ok := ctx.VM().EnumDump(args[0]); ok {
				ctx.Print(s + "\n")
				return vm.MakeNull()
			}
		}
		res, err := orig.Executable.Call(ctx.VM(), ctx, args)
		if err != nil {
			// Фатальные ошибки (Fatal) сохраняют оригинальный Throwable —
			// иначе теряется формат "Fatal error:" без Uncaught.
			if th, ok := err.(*vm.Throwable); ok {
				ctx.Throw(th)
			} else {
				ctx.Throw(vm.NewThrowable("Error", err.Error()))
			}
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		return res
	})
	fn.NumArgs = orig.NumArgs
	fn.ParamNames = orig.ParamNames
	// Обёртка должна сохранять все ABI-флаги оригинала (иначе именованные
	// аргументы/арность ведут себя иначе).
	fn.MinArgs = orig.MinArgs
	fn.MaxArgs = orig.MaxArgs
	fn.NativeVariadic = orig.NativeVariadic
	fn.HasNativeSig = orig.HasNativeSig
	fn.ByRefArgs = orig.ByRefArgs
	fn.AcceptsExtraNamed = orig.AcceptsExtraNamed
	return fn
}

// byRefInfo описывает вызов, для которого готовятся by-ref аргументы: имя
// функции для диагностики и имена параметров (без `$`).
type byRefInfo struct {
	name   string
	params []string
	// variadic — функция вариативна: у аргументов, собранных в variadic-хвост,
	// Zend не печатает имя параметра в диагностиках (arg_num > num_args).
	variadic bool
	// forWrite — сигнатура неизвестна: переменные/элементы/свойства передаются
	// «для возможной записи» (SEND_VAR_EX), без диагностик (как в dynCall).
	forWrite bool
	// silent — не эмитить диагностики by-ref (для вызовов функций, чья
	// сигнатура неизвестна на компиляции: by-ref-ность устанавливается лишь
	// в рантайме, поэтому компилятор не вправе судить о передаче).
	silent bool
}

func (c *compiler) evalArgs(mask []bool, args []Expr, info byRefInfo) ([]uint8, []func(), error) {
	regs := make([]uint8, len(args))
	var wbs []func()
	for i, a := range args {
		byRefPos := i < len(mask) && mask[i]
		if !byRefPos && info.variadic && len(mask) > 0 && mask[len(mask)-1] && i >= len(mask)-1 {
			// Вариативный by-ref параметр: все «лишние» аргументы Zend тоже
			// передаёт ссылками (ARG_MAY_BE_SENT_BY_REF).
			byRefPos = true
		}
		if byRefPos {
			r, wb, err := c.byRefArgInfo(a, info, i)
			if err != nil {
				return nil, nil, err
			}
			regs[i] = r
			if wb != nil {
				wbs = append(wbs, wb)
			}
			continue
		}
		if info.forWrite {
			switch a.(type) {
			case *VarExpr, *PropExpr, *IndexExpr:
				r, wb, err := c.byRefArgInfo(a, info, -1)
				if err != nil {
					return nil, nil, err
				}
				regs[i] = r
				if wb != nil {
					wbs = append(wbs, wb)
				}
				continue
			}
		}
		r, err := c.expr(a)
		if err != nil {
			return nil, nil, err
		}
		regs[i] = r
	}
	return regs, wbs, nil
}

// methodArgInfo описывает передачу аргументов метода: при неизвестной
// сигнатуре переменные уходят «для возможной записи» (SEND_VAR_EX).
func (c *compiler) methodArgInfo(name string) byRefInfo {
	if c.methodRefs[name] == nil {
		return byRefInfo{silent: true, forWrite: true}
	}
	return byRefInfo{}
}

// byRefArg подготавливает аргумент для by-ref параметра без контекста вызова.
func (c *compiler) byRefArg(e Expr) (uint8, func(), error) {
	return c.byRefArgInfo(e, byRefInfo{}, -1)
}

// byRefArgInfo подготавливает аргумент для by-ref параметра: переменная —
// её ячейка-ссылка (или временная с writeback), элемент массива/свойство —
// постоянная ячейка в контейнере, выражение — временная ячейка.
//
// Для не-переменных выражений Zend различает два случая:
//   - результат вызова/new — "Only variables should be passed by reference"
//     (Notice), значение передаётся в временную ячейку;
//   - константа/вычисленное выражение (в т.ч. $GLOBALS) — фатальная
//     "could not be passed by reference".
func (c *compiler) byRefArgInfo(e Expr, info byRefInfo, pos int) (uint8, func(), error) {
	// Опущенный (при именованных аргументах) или явный null в by-ref позиции
	// Zend передаёт во временную ячейку без диагностики.
	if _, ok := e.(*NullLit); ok {
		valReg, err := c.expr(e)
		if err != nil {
			return 0, nil, err
		}
		cell := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpNewRef, A: cell, B: valReg})
		return cell, nil, nil
	}
	if a, ok := e.(*VarExpr); ok {
		if (a.Name == "$GLOBALS" || a.Name == "GLOBALS") && !info.silent {
			c.emitByRefError(info, pos)
		}
		if c.isRef(a.Name) {
			return c.refCell(a.Name), nil, nil
		}
		// Чтение значения для Ref-ячейки: неопределённая переменная при
		// передаче по ссылке не порождает предупреждение (как в PHP).
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
		valReg, err := c.expr(a)
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
		if err != nil {
			return 0, nil, err
		}
		cell := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpNewRef, A: cell, B: valReg})
		var wb func()
		if a.Name != "$GLOBALS" && a.Name != "GLOBALS" {
			wb = func() {
				inner := c.alloc()
				c.chunk.Emit(vm.Instruction{Op: vm.OpRefGet, A: inner, B: cell})
				c.storeVar(a.Name, inner)
			}
		}
		return cell, wb, nil
	}
	if ae, ok := e.(*AssignExpr); ok && ae.ByRef && ae.Op == "=" {
		// `f($b =& $a)`: присваивание-ссылка даёт ссылку, которую можно
		// передать в by-ref параметр.
		if tv, ok := ae.Target.(*VarExpr); ok && tv.NameExpr == nil {
			cell, err := c.refCellForExpr(ae.Value)
			if err != nil {
				return 0, nil, err
			}
			c.bindRefCell(tv.Name, cell)
			return cell, nil, nil
		}
	}
	if ix, ok := e.(*IndexExpr); ok && ix.Key == nil {
		// `f($arr[])` для by-ref параметра (Zend FETCH_DIM_W): элемент
		// создаётся и передаётся ссылкой. OpIndexRef с C=-1 — append-режим.
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
		arr, wb, err := c.indexBase(ix, true)
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
		if err != nil {
			return 0, nil, err
		}
		cell := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpIndexRef, A: cell, B: arr, C: -1})
		if wb != nil {
			wb(arr)
		}
		return cell, nil, nil
	}
	if ix, ok := e.(*IndexExpr); ok && ix.Key != nil {
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
		cell, err := c.refCellForExpr(ix)
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
		if err != nil {
			return 0, nil, err
		}
		return cell, nil, nil
	}
	if pe, ok := e.(*PropExpr); ok {
		cell, err := c.refCellForExpr(pe)
		if err != nil {
			return 0, nil, err
		}
		return cell, nil, nil
	}
	if !info.silent && refableExpr(e) && info.name != "array_multisort" {
		// Вызов/new в by-ref позиции: если результат — ссылка, она
		// пробрасывается; иначе Notice и временная ячейка.
		// array_multisort — исключение: он не эмитит Notice для временных
		// значений (см. ниже), поэтому идёт общим путём.
		valR, err := c.expr(e)
		if err != nil {
			return 0, nil, err
		}
		c.emitLine(c.exprLine(e))
		msg := c.refMsgReg("Only variables should be passed by reference")
		wrap := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpTrue, A: wrap})
		cell := c.emitCall("__php_ref_assign_check", []uint8{valR, msg, wrap})
		return cell, nil, nil
	}
	valReg, err := c.expr(e)
	if err != nil {
		return 0, nil, err
	}
	c.emitLine(c.exprLine(e))
	// array_multisort помечает все аргументы by-ref (позиции массивов заранее
	// неизвестны), включая скаляры-флаги, которые в Zend by-value: для него
	// не эмитим диагностику передачи не-переменной.
	if info.name != "array_multisort" && !info.silent {
		if refableExpr(e) {
			c.emitRefNotice("Only variables should be passed by reference")
		} else {
			c.emitByRefError(info, pos)
		}
	}
	cell := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpNewRef, A: cell, B: valReg})
	return cell, nil, nil
}

// indexChainHasProp сообщает, что в цепочке индексов есть обращение к
// свойству (нужен RW-режим чтения базы для записи).
func indexChainHasProp(e Expr) bool {
	switch x := e.(type) {
	case *PropExpr:
		return true
	case *IndexExpr:
		return indexChainHasProp(x.Arr)
	}
	return false
}

// refableExpr сообщает, что выражение даёт временную переменную, которую Zend
// передаёт в by-ref параметр с Notice (а не с фатальной ошибкой).
func refableExpr(e Expr) bool {
	switch e.(type) {
	case *CallExpr, *MethodCallExpr, *StaticCallExpr, *NewExpr, *DynCallExpr:
		return true
	}
	return false
}

// emitByRefError эмитит фатальную ошибку "could not be passed by reference".
func (c *compiler) emitByRefError(info byRefInfo, pos int) {
	if info.name == "" || pos < 0 {
		return
	}
	param := ""
	if pos < len(info.params) && info.params[pos] != "" && !(info.variadic && pos >= len(info.params)-1) {
		param = " ($" + info.params[pos] + ")"
	}
	msg := fmt.Sprintf("%s(): Argument #%d%s could not be passed by reference", info.name, pos+1, param)
	mr := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: mr,
		C: int32(c.chunk.AddConst(vm.StrConst(msg)))})
	c.emitCall("__php_byref_error", []uint8{mr})
}

// emitNamedArgError эмитит вызов рантайм-помощника, бросающего исключение
// класса class с сообщением msg (ошибки именованных аргументов).
func (c *compiler) emitNamedArgError(class, msg string) uint8 {
	cr := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: cr, C: int32(c.chunk.AddConst(vm.StrConst(class)))})
	mr := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: mr, C: int32(c.chunk.AddConst(vm.StrConst(msg)))})
	r := c.emitCall("__php_named_arg_error", []uint8{cr, mr})
	return r
}

func (c *compiler) expr(e Expr) (uint8, error) {
	if foldableShape(e) {
		if v, ok := foldConstExpr(e); ok {
			return c.emitCVal(v), nil
		}
	}
	switch x := e.(type) {
	case *IntLit:
		r := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: r, C: int32(c.chunk.AddConst(vm.IntConst(x.V)))})
		return r, nil
	case *FloatLit:
		r := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: r, C: int32(c.chunk.AddConst(vm.FloatConst(x.V)))})
		return r, nil
	case *StrLit:
		r := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: r, C: int32(c.chunk.AddConst(vm.StrConst(x.V)))})
		return r, nil
	case *BoolLit:
		r := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: r, C: int32(c.chunk.AddConst(vm.BoolConst(x.V)))})
		return r, nil
	case *NullLit:
		r := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: r})
		return r, nil
	case *MagicExpr:
		return c.magicConst(x), nil
	case *ClassKeywordExpr:
		return c.classKeyword(x)
	case *NamedArgExpr:
		return c.expr(x.X)
	case *RegExpr:
		return x.Reg, nil
	case *VarExpr:
		if x.NameExpr != nil {
			c.emitLine(c.exprLine(x))
			return c.loadVarDyn(x.NameExpr)
		}
		if x.Name == "$this" || x.Name == "this" {
			d := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpThis, A: d})
			return d, nil
		}
		// LINE нужен только перед чтением из VM.globals (может дать
		// "Undefined variable"); промотированная main-переменная живёт в
		// регистре и предупреждать не может.
		if !c.isMain || c.useGlobal(x.Name) {
			c.emitLine(c.exprLine(x))
		}
		return c.loadVarChecked(x.Name, c.exprLine(x)), nil
	case *ConstExpr:
		if x.NameExpr != nil {
			// `Class::{expr}` — динамическое имя константы класса.
			var clsReg uint8
			if x.ClassExpr != nil {
				r, err := c.expr(x.ClassExpr)
				if err != nil {
					return 0, err
				}
				clsReg = r
			} else if idx, err := c.classIndex(x.Name); err == nil && idx >= 0 && idx < len(c.classes) {
				// Класс известен на этапе компиляции (в т.ч. self/parent в
				// объявлении класса) — подставляем его имя.
				clsReg = c.alloc()
				c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: clsReg,
					C: int32(c.chunk.AddConst(vm.StrConst(c.classes[idx].Name())))})
			} else {
				r, err := c.staticClassReg(x.Name, nil)
				if err != nil {
					return 0, err
				}
				clsReg = r
			}
			nm, err := c.expr(x.NameExpr)
			if err != nil {
				return 0, err
			}
			c.emitLine(c.exprLine(x))
			return c.emitCall("__php_dyn_class_const", []uint8{clsReg, nm}), nil
		}
		if x.ClassExpr != nil {
			// `EXPR::CONST` — класс, заданный выражением.
			clsReg, err := c.expr(x.ClassExpr)
			if err != nil {
				return 0, err
			}
			nm := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nm, C: int32(c.chunk.AddConst(vm.StrConst(x.Name)))})
			return c.emitCall("__php_dyn_class_const", []uint8{clsReg, nm}), nil
		}
		if c.constCtx || c.constEval > 0 {
			if i := strings.Index(x.Name, "::"); i >= 0 && strings.EqualFold(strings.TrimLeft(x.Name[:i], "\\"), "static") {
				return 0, &fatalError{msg: `"static::" is not allowed in compile-time constants`, line: c.exprLine(x)}
			}
		}
		// Магическая константа __COMPILER_HALT_OFFSET__: значение известно,
		// если файл содержит __halt_compiler() (zend_compile_const).
		if c.haltOffset >= 0 && !strings.HasPrefix(x.Name, "::") &&
			(x.Name == "__COMPILER_HALT_OFFSET__" || x.Fallback == "__COMPILER_HALT_OFFSET__") {
			r := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: r, C: int32(c.chunk.AddConst(vm.IntConst(c.haltOffset)))})
			return r, nil
		}
		if i := strings.Index(x.Name, "::"); i >= 0 {
			clsPart := strings.TrimLeft(x.Name[:i], "\\")
			if strings.HasPrefix(clsPart, "$") {
				return c.dynClassConst(clsPart, x.Name[i+2:])
			}
			sel, err := c.classRef(x.Name[:i])
			if err != nil {
				// Класс ещё не известен на этапе компиляции — откладываем
				// до рантайма (PHP: Class "X" not found / Undefined constant).
				constFn := "__php_const_lookup"
				if c.constCtx {
					constFn = "__const_expr"
				}
				if _, ok := c.idx[constFn]; ok {
					nameReg := c.alloc()
					c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nameReg, C: int32(c.chunk.AddConst(vm.StrConst(x.Name)))})
					return c.emitCall(constFn, []uint8{nameReg}), nil
				}
				return 0, err
			}
			d := c.alloc()
			c.emitLine(c.exprLine(x))
			c.chunk.Emit(vm.Instruction{Op: vm.OpConstGet, A: d, B: sel, C: int32(c.chunk.AddConst(vm.StrConst(x.Name[i+2:])))})
			return d, nil
		}
		if cv, ok := constValue(x.Name); ok {
			r := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: r, C: int32(c.chunk.AddConst(cv))})
			return r, nil
		}
		if x.Fallback != "" {
			if cv, ok := constValue(x.Fallback); ok {
				r := c.alloc()
				c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: r, C: int32(c.chunk.AddConst(cv))})
				return r, nil
			}
		}
		// неизвестная на этапе компиляции константа (define() и т.п.) — ищем в
		// рантайме: сначала NAME (с пространством имён), затем глобальный
		// fallback (PHP-семантика для неквалифицированного имени).
		constFn := "__php_const_lookup"
		if c.constCtx {
			constFn = "__const_expr"
		}
		if _, ok := c.idx[constFn]; ok {
			nameReg := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nameReg, C: int32(c.chunk.AddConst(vm.StrConst(x.Name)))})
			fbReg := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: fbReg, C: int32(c.chunk.AddConst(vm.StrConst(x.Fallback)))})
			return c.emitCall(constFn, []uint8{nameReg, fbReg}), nil
		}
		return 0, fmt.Errorf("компилятор: неизвестная константа %q", x.Name)
	case *ArrayLit:
		return c.arrayLit(x)
	case *MatchExpr:
		return c.matchExpr(x)
	case *NullsafeBase:
		if !c.hasBase {
			return 0, fmt.Errorf("компилятор: NullsafeBase вне цепочки ?->")
		}
		return c.nullsafeBase, nil
	case *NullsafeExpr:
		return c.nullsafeExpr(x)
	case *IndexExpr:
		arr, err := c.expr(x.Arr)
		if err != nil {
			return 0, err
		}
		if x.Key == nil {
			return 0, &fatalError{msg: "Cannot use [] for reading", line: c.exprLine(x)}
		}
		k, err := c.expr(x.Key)
		if err != nil {
			return 0, err
		}
		d := c.alloc()
		c.emitLine(c.exprLine(x))
		c.chunk.Emit(vm.Instruction{Op: vm.OpIndex, A: d, B: arr, C: int32(k)})
		return d, nil
	case *AssignExpr:
		return c.assign(x.Target, x.Op, x.Value, x.ByRef)
	case *UnaryExpr:
		if x.Op == "@" {
			c.emitLine(c.exprLine(x))
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
			r, err := c.expr(x.X)
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
			if err != nil {
				return 0, err
			}
			return r, nil
		}
		r, err := c.expr(x.X)
		if err != nil {
			return 0, err
		}
		d := c.alloc()
		switch x.Op {
		case "-":
			c.chunk.Emit(vm.Instruction{Op: vm.OpNeg, A: d, B: r})
		case "!":
			c.chunk.Emit(vm.Instruction{Op: vm.OpNot, A: d, B: r})
		case "~":
			c.chunk.Emit(vm.Instruction{Op: vm.OpBitNot, A: d, B: r})
		case "+":
			return r, nil
		default:
			return 0, fmt.Errorf("компилятор: неизвестный унарный оператор %q", x.Op)
		}
		return d, nil
	case *CastExpr:
		r, err := c.expr(x.X)
		if err != nil {
			return 0, err
		}
		d := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpCast, A: d, B: r, C: int32(castKind(x.Type))})
		return d, nil
	case *IncDecExpr:
		return c.incDec(x)
	case *BinExpr:
		return c.binExpr(x)
	case *LogicalExpr:
		return c.logical(x)
	case *TernaryExpr:
		return c.ternary(x)
	case *CoalesceExpr:
		return c.coalesce(x)
	case *CallExpr:
		if x.ConstStmt {
			// `const NAME = expr;`: значение вычисляется в контексте
			// константного выражения (проверка свойств не-enum объектов).
			c.constEval++
			defer func() { c.constEval-- }()
		}
		if isFCCArgs(x.Args) {
			return c.funcFCCCall(x)
		}
		if x.Name == "__php_dollar_brace_deprecation" || x.Name == "__php_dollar_brace_expr_deprecation" {
			// PHP выводит предупреждение об устаревшей форме ${var}/${expr} на
			// этапе компиляции (до выполнения) — копим его в стартовых
			// диагностиках.
			msg := "Using ${var} in strings is deprecated, use {$var} instead"
			if x.Name == "__php_dollar_brace_expr_deprecation" {
				msg = "Using ${expr} (variable variables) in strings is deprecated, use {${expr}} instead"
			}
			compilerDeprecations = append(compilerDeprecations, fmt.Sprintf(
				"%s in %s on line %d", msg, c.file, c.exprLine(x)))
			r := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: r, C: int32(c.chunk.AddConst(vm.StrConst("")))})
			return r, nil
		}
		if x.Name == "eval" {
			return c.evalCall(x)
		}
		// count/sizeof/is_countable перехватываются VM-реализацией с поддержкой
		// Countable (builtin count не знает о методах).
		mapName := func(n string) string {
			switch n {
			case "count":
				return "__php_count"
			case "sizeof":
				return "__php_sizeof"
			case "is_countable":
				return "__php_is_countable"
			case "error_reporting":
				return "__php_error_reporting"
			}
			return n
		}
		// func_get_arg()/func_get_args()/func_num_args() читают регистры
		// параметров кадра — помечаем функцию, чтобы распределитель регистров
		// не переиспользовал параметры как временные.
		switch x.Name {
		case "func_get_arg", "func_get_args", "func_num_args":
			c.usesFuncArg = true
		}
		if x.Fallback != "" {
			switch x.Fallback {
			case "func_get_arg", "func_get_args", "func_num_args":
				c.usesFuncArg = true
			}
		}
		// compact()/extract()/get_defined_vars() работают с локальной таблицей
		// символов вызывающего кадра: помечаем функцию, чтобы закрепить
		// регистры переменных и записать карту имя→регистр в чанк.
		for _, n := range []string{x.Name, x.Fallback} {
			switch strings.TrimLeft(n, "\\") {
			case "compact", "extract", "get_defined_vars":
				c.usesSymtab = true
			}
		}
		// assert(): Zend передаёт исходный текст проверки в диагностику.
		// Вызов с одним аргументом компилируем в __php_assert1(cond, "assert(...)"),
		// с двумя — дописываем текст третьим служебным аргументом.
		if x.AssertText != "" && (isAssertFuncName(x.Name) || isAssertFuncName(x.Fallback)) {
			if hasNamedArg(x.Args) {
				// assert(assertion: ..., description: ...): раскладываем в
				// позиционные для __php_assert1/2; неполный набор —
				// Zend-ошибка (assert() подставляет свой первый аргумент).
				var assertion, description Expr
				allNamed := true
				for _, a := range x.Args {
					na, isNa := a.(*NamedArgExpr)
					if !isNa {
						allNamed = false
						break
					}
					switch strings.ToLower(na.Name) {
					case "assertion":
						assertion = na.X
					case "description":
						description = na.X
					default:
						allNamed = false
					}
				}
				if allNamed && assertion == nil {
					return c.emitNamedArgError("Error", "Named parameter $description overwrites previous argument"), nil
				}
				if allNamed {
					nc := *x
					nc.Args = []Expr{assertion}
					if description != nil {
						nc.Args = append(nc.Args, description)
					} else {
						nc.Name = "__php_assert1"
						nc.Fallback = ""
					}
					nc.Args = append(nc.Args, &StrLit{V: x.AssertText})
					x = &nc
				}
			} else {
				switch len(x.Args) {
				case 1:
					nc := *x
					nc.Name = "__php_assert1"
					nc.Fallback = ""
					nc.Args = append(append([]Expr{}, x.Args...), &StrLit{V: x.AssertText})
					x = &nc
				case 2:
					nc := *x
					nc.Args = append(append([]Expr{}, x.Args...), &StrLit{V: x.AssertText})
					x = &nc
				}
			}
		}
		fi, ok := c.idx[mapName(x.Name)]
		if !ok && x.Fallback != "" {
			fi, ok = c.idx[mapName(x.Fallback)]
		}
		if !ok {
			// Функция неизвестна на этапе компиляции (автозагрузка, define в
			// рантайме, встроенная, которой нет в таблице) — разрешаем имя в
			// рантайме через OpCallDyn.
			return c.dynCallByName(x.Name, x.Args)
		}
		if hasNamedArg(x.Args) {
			switch mapName(x.Name) {
			case "call_user_func":
				cb, err := c.expr(x.Args[0])
				if err != nil {
					return 0, err
				}
				arr, err := c.buildArgArray(x.Args[1:], true, nil)
				if err != nil {
					return 0, err
				}
				return c.emitCall("__php_call_cb_named", []uint8{cb, arr}), nil
			case "forward_static_call":
				cb, err := c.expr(x.Args[0])
				if err != nil {
					return 0, err
				}
				arr, err := c.buildArgArray(x.Args[1:], true, nil)
				if err != nil {
					return 0, err
				}
				return c.emitCall("__php_forward_cb_named", []uint8{cb, arr}), nil
			}
		}
		if hasArgSpread(x.Args) {
			return c.callArrWithSpread(fi, x.Args)
		}
		if len(x.Args) > maxRegCallArgs {
			return c.emitCallArr(fi, reorderNamedArgs(x.Args, c.funcs[fi].ParamNames), c.funcs[fi].ByRefArgs, byRefInfo{name: c.funcs[fi].Name, params: c.funcs[fi].ParamNames, silent: c.funcs[fi].Name == "extract"})
		}
		{
			fn := c.funcs[fi]
			disp := fn.Name
			if fn.DisplayName != "" {
				disp = fn.DisplayName
			} else if fn.Scope != nil && disp != "" && !strings.Contains(disp, "::") &&
				!strings.HasPrefix(disp, "{closure}") {
				disp = vm.ClassDisplayName(fn.Scope.Name()) + "::" + disp
			}
			variadic := fn.NumArgs < 0
			var sig *intrinsicSig
			if fn.HasNativeSig {
				if s, ok := intrinsicArity[fn.Name]; ok {
					cp := s
					sig = &cp
					variadic = cp.Variadic
					if cp.Required > 0 {
						fn.NumRequired = cp.Required
					}
				}
			}
			na := reorderNamedArgsChecked(x.Args, fn.ParamNames, fn.NumRequired, disp, variadic, sig)
			if na.namedRuntime {
				// Вариативная функция + лишние именованные: ключи должен
				// сохранить рантайм-биндинг.
				return c.dynCallByNameRef(fn.Name, x.Args, namedByRefInfoFor(fn))
			}
			if na.fatal != "" {
				return 0, c.newFatal(na.fatal)
			}
			// Аргументы вычисляются до диагностики (Zend: проверки by-ref
			// срабатывают раньше «not passed»).
			argRegs, wbs, err := c.evalArgs(fn.ByRefArgs, na.args, byRefInfo{name: fn.Name, params: fn.ParamNames, variadic: fn.VariadicByRef, silent: fn.Name == "extract"})
			if err != nil {
				return 0, err
			}
			if na.countMsg != "" {
				return c.emitNamedArgError("ArgumentCountError", na.countMsg), nil
			}
			if na.throwMsg != "" {
				return c.emitNamedArgError("Error", na.throwMsg), nil
			}
			return c.emitCallRegs(fi, argRegs, wbs), nil
		}
	case *YieldExpr:
		return c.yieldExpr(x)
	case *IncludeExpr:
		return c.includeExpr(x)
	case *DynCallExpr:
		return c.dynCall(x)
	case *PrintExpr:
		c.emitLine(c.exprLine(x))
		r, err := c.expr(x.X)
		if err != nil {
			return 0, err
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpEcho, A: r})
		d := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: d, C: int32(c.chunk.AddConst(vm.IntConst(1)))})
		return d, nil
	case *ExitExpr:
		r := c.alloc()
		if x.X != nil {
			var err error
			r, err = c.expr(x.X)
			if err != nil {
				return 0, err
			}
		} else {
			c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: r})
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpExit, A: r})
		return r, nil
	case *IssetExpr:
		return c.isset(x)
	case *EmptyExpr:
		c.emitLine(c.exprLine(x))
		if ix, ok := x.X.(*IndexExpr); ok && ix.Key != nil {
			// empty($a[$k]) — семантика через isset: для ArrayAccess
			// сначала offsetExists (без offsetGet, если элемента нет).
			arr, err := c.exprIssetBase(ix.Arr)
			if err != nil {
				return 0, err
			}
			k, err := c.expr(ix.Key)
			if err != nil {
				return 0, err
			}
			ir := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpIndexIsset, A: ir, B: arr, C: int32(k)})
			d := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpTrue, A: d})
			jf := c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfFalse, A: ir})
			val := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpIndex, A: val, B: arr, C: int32(k)})
			c.chunk.Emit(vm.Instruction{Op: vm.OpNot, A: d, B: val})
			c.setJump(jf, c.chunk.Len())
			return d, nil
		}
		if isThisVar(x.X) {
			r := c.emitCall("__php_isset_this", nil)
			d := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpNot, A: d, B: r})
			return d, nil
		}
		if pe, ok := x.X.(*PropExpr); ok {
			// empty($o->p) — через PropIsset (__isset) без __get, если нет.
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
			obj, err := c.expr(pe.Recv)
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
			if err != nil {
				return 0, err
			}
			ir := c.alloc()
			var nameReg uint8
			if pe.NameExpr != nil {
				nameReg, err = c.expr(pe.NameExpr)
				if err != nil {
					return 0, err
				}
				c.chunk.Emit(vm.Instruction{Op: vm.OpPropIssetDyn, A: ir, B: obj, C: int32(nameReg)})
			} else {
				c.chunk.Emit(vm.Instruction{Op: vm.OpPropIsset, A: ir, B: obj,
					C: int32(c.chunk.AddConst(vm.StrConst(pe.Name)))})
			}
			d := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpTrue, A: d})
			jf := c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfFalse, A: ir})
			val := c.alloc()
			if pe.NameExpr != nil {
				c.chunk.Emit(vm.Instruction{Op: vm.OpPropGetDyn, A: val, B: obj, C: int32(nameReg)})
			} else {
				c.chunk.Emit(vm.Instruction{Op: vm.OpPropGet, A: val, B: obj,
					C: int32(c.chunk.AddConst(vm.StrConst(pe.Name)))})
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpNot, A: d, B: val})
			c.setJump(jf, c.chunk.Len())
			return d, nil
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
		r, err := c.expr(x.X)
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
		if err != nil {
			return 0, err
		}
		d := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpNot, A: d, B: r})
		return d, nil
	case *CloneExpr:
		r, err := c.expr(x.X)
		if err != nil {
			return 0, err
		}
		d := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpClone, A: d, B: r})
		return d, nil
	case *ThrowExpr:
		c.emitLine(c.exprLine(x))
		r, err := c.expr(x.X)
		if err != nil {
			return 0, err
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpThrow, A: r})
		return r, nil
	case *ClosureExpr:
		return c.closureExpr(x)
	case *ArrowFnExpr:
		return c.closureExpr(x)
	case *NewExpr:
		return c.newExpr(x)
	case *NewAnonClassExpr:
		return c.anonNew(x)
	case *PropExpr:
		recv, err := c.expr(x.Recv)
		if err != nil {
			return 0, err
		}
		d := c.alloc()
		if x.NameExpr != nil {
			ne, err := c.expr(x.NameExpr)
			if err != nil {
				return 0, err
			}
			if c.constCtx || c.constEval > 0 {
				// Константное выражение: RHS вычисляется и приводится к
				// строке до проверки, затем чтение свойства не-enum объекта
				// запрещено (zend_ast_evaluate) — Error в рантайме.
				ne = c.emitCall("__php_const_prop_name", []uint8{ne})
				c.emitCall("__php_const_prop_check", []uint8{recv})
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpPropGetDyn, A: d, B: recv, C: int32(ne)})
		} else {
			if c.constCtx || c.constEval > 0 {
				c.emitCall("__php_const_prop_check", []uint8{recv})
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpPropGet, A: d, B: recv, C: int32(c.chunk.AddConst(vm.StrConst(x.Name)))})
		}
		return d, nil
	case *StaticPropExpr:
		if x.ClassExpr != nil {
			return c.dynStaticGetExpr(x.ClassExpr, x.Name, x.NameExpr)
		}
		if dyn := strings.TrimLeft(x.Class, "\\"); strings.HasPrefix(dyn, "$") {
			return c.dynStaticGet(dyn, x.Name, x.NameExpr)
		}
		sel, err := c.classRef(x.Class)
		if err != nil {
			// Класс ещё не известен (будет найден через автозагрузку или
			// объявлен в подключаемом файле) — резолвим по имени в рантайме.
			return c.dynStaticGetExpr(&StrLit{V: strings.TrimLeft(x.Class, "\\")}, x.Name, x.NameExpr)
		}
		c.emitLine(c.exprLine(x))
		d := c.alloc()
		if x.NameExpr != nil {
			ne, err := c.expr(x.NameExpr)
			if err != nil {
				return 0, err
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpStaticGetDyn, A: d, B: sel, C: int32(ne)})
		} else {
			c.chunk.Emit(vm.Instruction{Op: vm.OpStaticGet, A: d, B: sel, C: int32(c.chunk.AddConst(vm.StrConst(x.Name)))})
		}
		return d, nil
	case *MethodCallExpr:
		if isFCCArgs(x.Args) {
			if containsNullsafe(x.Recv) {
				return 0, c.newFatal("Cannot combine nullsafe operator with Closure creation")
			}
			if x.NameExpr == nil {
				return c.memberFCCCall(2, x.Recv, x.Name)
			}
			// Динамическое имя метода: `$obj->$m(...)`.
			return c.expr(&CallExpr{Name: "__php_fcc", Args: []Expr{&IntLit{V: 2}, x.Recv, x.NameExpr}})
		}
		return c.methodCall(x.Recv, x.Name, x.NameExpr, x.Args)
	case *StaticCallExpr:
		if x.ClassExpr != nil {
			if isFCCArgs(x.Args) {
				var nm Expr = &StrLit{V: x.Name}
				if x.NameExpr != nil {
					nm = x.NameExpr
				}
				return c.expr(&CallExpr{Name: "__php_fcc", Args: []Expr{&IntLit{V: 3}, x.ClassExpr, nm}})
			}
			return c.staticCallExpr(x.ClassExpr, x.Name, x.NameExpr, x.Args)
		}
		if isFCCArgs(x.Args) {
			if strings.HasPrefix(strings.TrimLeft(x.Class, "\\"), "$") {
				// `$cls::m(...)` — класс вычисляется в рантайме.
				clsExpr := Expr(&VarExpr{Name: strings.TrimLeft(x.Class, "\\")})
				var nm Expr = &StrLit{V: x.Name}
				if x.NameExpr != nil {
					nm = x.NameExpr
				}
				return c.expr(&CallExpr{Name: "__php_fcc", Args: []Expr{&IntLit{V: 3}, clsExpr, nm}})
			}
			if x.NameExpr == nil {
				return c.memberFCCCall(3, nil, x.Name, x.Class)
			}
			// `A::$m(...)` — динамическое имя статического метода.
			return c.expr(&CallExpr{Name: "__php_fcc", Args: []Expr{&IntLit{V: 3}, &ClassKeywordExpr{Class: x.Class}, x.NameExpr}})
		}
		return c.staticCall(x.Class, x.Name, x.NameExpr, x.Args)
	case *InstanceOfExpr:
		obj, err := c.expr(x.X)
		if err != nil {
			return 0, err
		}
		// Динамическое имя класса: `$x instanceof $cls`, `instanceof (expr)`.
		if x.ClassExpr != nil {
			nameReg, err := c.expr(x.ClassExpr)
			if err != nil {
				return 0, err
			}
			return c.emitCall("__php_instanceof", []uint8{obj, nameReg}), nil
		}
		if strings.HasPrefix(x.Class, "$") || strings.HasPrefix(x.Class, "\\$") {
			nameReg, err := c.expr(&VarExpr{Name: strings.TrimPrefix(x.Class, "\\")})
			if err != nil {
				return 0, err
			}
			return c.emitCall("__php_instanceof", []uint8{obj, nameReg}), nil
		}
		idx, err := c.classIndex(x.Class)
		if err != nil {
			// Класс неизвестен на этапе компиляции: резолвим по имени в
			// рантайме (может быть объявлен в подключаемом в рантайме файле).
			nameReg := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nameReg,
				C: int32(c.chunk.AddConst(vm.StrConst(strings.TrimLeft(x.Class, "\\"))))})
			return c.emitCall("__php_instanceof", []uint8{obj, nameReg}), nil
		}
		d := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpInstanceOf, A: d, B: obj, C: int32(idx)})
		return d, nil
	}
	return 0, fmt.Errorf("компилятор: неизвестное выражение %T", e)
}

// classRef возвращает селектор класса для self/static/parent (0/1/2) или
// явного имени (3+индекс в vm.classes).
// exprForWrite вычисляет выражение как базу для записи: для PropExpr
// используется OpPropGetRW (косвенная модификация), иначе — обычный expr.
func (c *compiler) exprForWrite(e Expr) (uint8, error) {
	if pe, ok := e.(*PropExpr); ok {
		return c.propGetRW(pe)
	}
	if ve, ok := e.(*VarExpr); ok {
		// Zend читает переменную в write-контексте без предупреждения:
		// попытка записи в null сама бросает Error (bug75921), а warning
		// «Undefined variable» в этой позиции не печатается. Для статически
		// известной переменной подавление не нужно (не плодим опкоды).
		if ve.NameExpr == nil {
			if _, known := c.vars[ve.Name]; !known {
				if _, isRef := c.refs[ve.Name]; !isRef && ve.Name != "$this" && ve.Name != "this" {
					c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
					r, err := c.expr(e)
					c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
					return r, err
				}
			}
		} else {
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
			r, err := c.expr(e)
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
			return r, err
		}
	}
	if ie, ok := e.(*IndexExpr); ok && ie.Key != nil && indexChainHasProp(ie) {
		// Цепочка записи через индекс с промежуточным свойством: база
		// читается в RW-режиме (FETCH_DIM_RW/FETCH_OBJ_W), поэтому
		// промежуточное свойство null даёт Error «Attempt to modify property»
		// (bug75921). Для индексов от переменных/статики RW-режим не нужен и
		// менял бы семантику копий (foreachLoop.016).
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
		arr, wb, err := c.indexBase(ie, false)
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
		if err != nil {
			return 0, err
		}
		k, err := c.expr(ie.Key)
		if err != nil {
			return 0, err
		}
		d := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpIndexRW, A: d, B: arr, C: int32(k)})
		if wb != nil {
			wb(arr)
		}
		return d, nil
	}
	return c.expr(e)
}

// propGetRW компилирует чтение свойства в режиме «база для записи».
func (c *compiler) propGetRW(x *PropExpr) (uint8, error) {
	recv, err := c.exprForWrite(x.Recv)
	if err != nil {
		return 0, err
	}
	d := c.alloc()
	if x.NameExpr != nil {
		ne, err := c.expr(x.NameExpr)
		if err != nil {
			return 0, err
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpPropGetRWDyn, A: d, B: recv, C: int32(ne)})
	} else {
		c.chunk.Emit(vm.Instruction{Op: vm.OpPropGetRW, A: d, B: recv, C: int32(c.chunk.AddConst(vm.StrConst(x.Name)))})
	}
	return d, nil
}

// exprChainRecv вычисляет объект-приёмник промежуточного звена цепочки
// записи (`$o->p->q = ...`, `$o->p[] = ...`): свойство читается в режиме
// OpPropGetRWInterm (инициализированное readonly допустимо), остальные
// приёмники — как база записи.
func (c *compiler) exprChainRecv(e Expr) (uint8, error) {
	if pe, ok := e.(*PropExpr); ok {
		recv, err := c.exprChainRecv(pe.Recv)
		if err != nil {
			return 0, err
		}
		d := c.alloc()
		if pe.NameExpr != nil {
			ne, err := c.expr(pe.NameExpr)
			if err != nil {
				return 0, err
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpPropGetRWDyn, A: d, B: recv, C: int32(ne)})
		} else {
			c.chunk.Emit(vm.Instruction{Op: vm.OpPropGetRWInterm, A: d, B: recv, C: int32(c.chunk.AddConst(vm.StrConst(pe.Name)))})
		}
		return d, nil
	}
	return c.exprForWrite(e)
}

func (c *compiler) classRef(name string) (uint8, error) {
	name = strings.TrimLeft(name, "\\")
	switch name {
	case "self":
		if err := c.checkNoClassScope("self"); err != nil {
			return 0, err
		}
		return 0, nil
	case "static":
		if err := c.checkNoClassScope("static"); err != nil {
			return 0, err
		}
		return 1, nil
	case "parent":
		if err := c.checkNoClassScope("parent"); err != nil {
			return 0, err
		}
		return 2, nil
	}
	i, ok := c.classIdx[name]
	if !ok {
		i, ok = ciLookup(c.classIdx, name)
	}
	if !ok {
		return 0, fmt.Errorf("компилятор: неизвестный класс %q", name)
	}
	if i > 251 {
		return 0, fmt.Errorf("компилятор: слишком много классов")
	}
	return uint8(3 + i), nil
}

// checkNoClassScope — Zend отвергает self/parent/static в теле функции, у
// которой заведомо нет области класса (замыкание может получить её позже
// через bindTo, а главный чанк резолвит self в рантайме). Ошибка запоминается
// в scopeFatal: classRef вызывается и там, где ошибка означает «класс пока
// неизвестен, разберёмся в рантайме».
func (c *compiler) checkNoClassScope(kw string) error {
	if c.curClass != nil || c.closureBody || c.isMain {
		return nil
	}
	err := c.newFatal(`Cannot use "` + kw + `" when no class scope is active`)
	if c.scopeFatal == nil {
		c.scopeFatal = err
	}
	return err
}

// ciLookup ищет класс по имени без учёта регистра (PHP-семантика).
func ciLookup(m map[string]int, name string) (int, bool) {
	if i, ok := m[name]; ok {
		return i, true
	}
	for k, i := range m {
		if strings.EqualFold(k, name) {
			return i, true
		}
	}
	return 0, false
}

// classIndex возвращает индекс класса в vm.classes для new/instanceof.
func (c *compiler) classIndex(name string) (int, error) {
	name = strings.TrimLeft(name, "\\")
	switch name {
	case "self", "static":
		if c.curClass != nil {
			if i, ok := c.classIdx[c.curClass.Name()]; ok {
				return i, nil
			}
		}
		if err := c.checkNoClassScope(strings.ToLower(name)); err != nil {
			return 0, err
		}
	case "parent":
		if c.curClass != nil && c.curClass.Parent() != nil {
			if i, ok := c.classIdx[c.curClass.Parent().Name()]; ok {
				return i, nil
			}
		}
		if err := c.checkNoClassScope("parent"); err != nil {
			return 0, err
		}
	}
	if i, ok := c.classIdx[name]; ok {
		return i, nil
	}
	if i, ok := ciLookup(c.classIdx, name); ok {
		return i, nil
	}
	return 0, fmt.Errorf("компилятор: неизвестный класс %q", name)
}

// newExpr компилирует `new Class(args)`.
func (c *compiler) newExpr(x *NewExpr) (uint8, error) {
	if isFCCArgs(x.Args) {
		return 0, &fatalError{msg: "Cannot create Closure for new expression", line: c.exprLine(x)}
	}
	if x.ClassExpr != nil {
		return c.dynNew(x)
	}
	if strings.EqualFold(x.Class, "static") && c.curClass != nil {
		return c.newStatic(x.Args)
	}
	// Без области класса Zend резолвит self/parent/static в рантайме и бросает
	// перехватываемый Error (bug70918), а не «Class "static" not found».
	if c.curClass == nil {
		switch strings.ToLower(strings.TrimLeft(x.Class, "\\")) {
		case "self":
			return c.newScopeSel(0, x.Args)
		case "static":
			return c.newScopeSel(1, x.Args)
		case "parent":
			return c.newScopeSel(2, x.Args)
		}
	}
	return c.newNamed(x.Class, x.Args)
}

// newStatic компилирует `new static` с поздним статическим связыванием:
// класс-получатель берётся в рантайме (OpClassName с селектором called).
func (c *compiler) newStatic(args []Expr) (uint8, error) {
	return c.newScopeSel(1, args)
}

// newScopeSel — `new self/static/parent`: класс резолвится в рантайме по
// селектору (0/1/2); без области класса OpClassName бросает Error.
func (c *compiler) newScopeSel(sel uint8, args []Expr) (uint8, error) {
	if hasNamedArg(args) {
		if msg := argOrderError(args); msg != "" {
			return 0, &fatalError{msg: msg, line: c.exprLine(args[len(args)-1])}
		}
		nameReg := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpClassName, A: nameReg, B: sel, C: 1})
		arr, err := c.buildArgArray(args, true, nil)
		if err != nil {
			return 0, err
		}
		return c.emitCall("__php_new_named", []uint8{nameReg, arr}), nil
	}
	nameReg := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpClassName, A: nameReg, B: sel, C: 1})
	argRegs, wbs, err := c.evalArgs(c.methodRefs["__construct"], args, byRefInfo{})
	if err != nil {
		return 0, err
	}
	base := c.alloc()
	for range argRegs {
		c.alloc()
	}
	if base != nameReg {
		c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: base, B: nameReg})
	}
	for k, a := range argRegs {
		dst := base + 1 + uint8(k)
		if dst != a {
			c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: dst, B: a})
		}
	}
	d := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpNewDyn, A: d, B: base, C: int32(len(argRegs))})
	c.tmp = d + 1
	for _, wb := range wbs {
		wb()
	}
	return d, nil
}

// newByClassName создаёт объект класса, имя которого неизвестно на этапе
// компиляции: имя грузится в регистр, а сам объект создаётся OpNewDyn
// (резолв по имени в vm.classes в рантайме).
func (c *compiler) newByClassName(name string, args []Expr) (uint8, error) {
	nameReg := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nameReg,
		C: int32(c.chunk.AddConst(vm.StrConst(strings.TrimLeft(name, "\\"))))})
	argRegs, wbs, err := c.evalArgs(c.methodRefs["__construct"], args, byRefInfo{})
	if err != nil {
		return 0, err
	}
	base := c.alloc()
	for range argRegs {
		c.alloc()
	}
	if base != nameReg {
		c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: base, B: nameReg})
	}
	for k, a := range argRegs {
		dst := base + 1 + uint8(k)
		if dst != a {
			c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: dst, B: a})
		}
	}
	d := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpNewDyn, A: d, B: base, C: int32(len(argRegs))})
	c.tmp = d + 1
	for _, wb := range wbs {
		wb()
	}
	return d, nil
}

// newNamed создаёт объект именованного класса с аргументами.
func (c *compiler) newNamed(name string, args []Expr) (uint8, error) {
	idx, err := c.classIndex(name)
	if err != nil {
		// Класс неизвестен на этапе компиляции (например, объявлен в
		// подключаемом в рантайме файле): откладываем резолв по имени до
		// рантайма через OpNewDyn.
		if hasNamedArg(args) {
			if msg := argOrderError(args); msg != "" {
				return 0, &fatalError{msg: msg, line: c.exprLine(args[len(args)-1])}
			}
			nameReg := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nameReg,
				C: int32(c.chunk.AddConst(vm.StrConst(strings.TrimLeft(name, "\\"))))})
			arr, err := c.buildArgArray(args, true, nil)
			if err != nil {
				return 0, err
			}
			return c.emitCall("__php_new_named", []uint8{nameReg, arr}), nil
		}
		if hasArgSpread(args) {
			return 0, err
		}
		return c.newByClassName(name, args)
	}
	if hasNamedArg(args) {
		if msg := argOrderError(args); msg != "" {
			return 0, &fatalError{msg: msg, line: c.exprLine(args[len(args)-1])}
		}
		idxReg := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: idxReg, C: int32(c.chunk.AddConst(vm.IntConst(int64(idx))))})
		arr, err := c.buildArgArray(args, true, nil)
		if err != nil {
			return 0, err
		}
		return c.emitCall("__php_new_named", []uint8{idxReg, arr}), nil
	}
	if hasArgSpread(args) {
		idxReg := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: idxReg, C: int32(c.chunk.AddConst(vm.IntConst(int64(idx))))})
		arr, err := c.buildArgArray(args, true, nil)
		if err != nil {
			return 0, err
		}
		return c.emitCall("__php_new_spread", []uint8{idxReg, arr}), nil
	}
	argRegs, wbs, err := c.evalArgs(c.methodRefs["__construct"], args, byRefInfo{})
	if err != nil {
		return 0, err
	}
	d := c.alloc()
	base := c.alloc()
	for range argRegs {
		c.alloc()
	}
	for k, a := range argRegs {
		dst := base + uint8(k)
		if dst != a {
			c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: dst, B: a})
		}
	}
	c.chunk.Emit(vm.Instruction{Op: vm.OpNew, A: d, B: base, C: int32(idx) | int32(len(argRegs)+1)<<16})
	c.tmp = base + uint8(len(argRegs))
	for _, wb := range wbs {
		wb()
	}
	return d, nil
}

// anonNew компилирует `new class ... {...}`: класс уже зарегистрирован в
// таблице под внутренним именем (см. collectAnonClasses).
func (c *compiler) anonNew(x *NewAnonClassExpr) (uint8, error) {
	if isFCCArgs(x.Args) {
		return 0, &fatalError{msg: "Cannot create Closure for new expression", line: c.exprLine(x)}
	}
	name, ok := c.anonNames[x]
	if !ok {
		return 0, fmt.Errorf("компилятор: анонимный класс не зарегистрирован")
	}
	// Фатальные ошибки линковки анонимного класса (напр. #[\Override] без
	// предка) PHP поднимает в точке выполнения выражения `new class`.
	if x.Decl != nil && x.Decl.RuntimeFatal != "" {
		c.chunk.SetLine(x.Decl.Line)
		c.chunk.Emit(vm.Instruction{Op: vm.OpFatal, C: int32(c.chunk.AddConst(vm.StrConst(x.Decl.RuntimeFatal)))})
	}
	if x.Decl != nil && x.Decl.IsReadonly {
		for _, at := range x.Decl.Attrs {
			if strings.EqualFold(strings.TrimLeft(at.Name, "\\"), "AllowDynamicProperties") {
				c.chunk.SetLine(x.Decl.Line)
				c.chunk.Emit(vm.Instruction{Op: vm.OpFatal, C: int32(c.chunk.AddConst(vm.StrConst("Cannot apply #[AllowDynamicProperties] to readonly class class@anonymous")))})
			}
		}
	}
	return c.newNamed(name, x.Args)
}

// closureExpr компилирует создание замыкания: вычисляет захваты (по значению
// — копия слота, по ссылке — общая Ref-ячейка) и кладёт их в регистры
// base..base+n-1, после чего OpClosure создаёт значение.
func (c *compiler) closureExpr(e Expr) (uint8, error) {
	ci, ok := c.closureIdx[e]
	if !ok || ci.idx < 0 {
		return 0, fmt.Errorf("компилятор: замыкание не зарегистрировано")
	}
	type capSpec struct {
		name  string
		byref bool
	}
	var specs []capSpec
	if ci.isArrow {
		for _, n := range ci.free {
			specs = append(specs, capSpec{name: n})
		}
	} else {
		for _, u := range ci.uses {
			specs = append(specs, capSpec{name: u.Name, byref: u.ByRef})
		}
	}
	base := c.alloc()
	for range specs {
		c.alloc()
	}
	for i, sp := range specs {
		var r uint8
		if sp.byref {
			r = c.refCell(sp.name)
		} else {
			r = c.loadVar(sp.name)
		}
		if dst := base + uint8(i); dst != r {
			c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: dst, B: r})
		}
	}
	d := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpClosure, A: d, B: base, C: int32(ci.idx)})
	c.tmp = d + 1
	return d, nil
}

// dynCall компилирует `$f(args)`: имя функции — динамическое (строка из
// регистра), аргументы идут следом за ним.
// evalDynArgs вычисляет аргументы динамического вызова. Простые переменные
// передаются как Ref-ячейки с writeback, чтобы работали by-ref параметры
// замыканий и функций, вызываемых по динамическому имени (маска на этапе
// компиляции неизвестна). Для by-value параметров это прозрачно: слот-ссылка
// разыменовывается при чтении.
func (c *compiler) evalDynArgs(args []Expr) ([]uint8, []func(), error) {
	regs := make([]uint8, len(args))
	var wbs []func()
	for i, a := range args {
		if v, ok := a.(*VarExpr); ok && v.Name != "$this" && v.Name != "this" {
			r, wb, err := c.byRefArg(v)
			if err != nil {
				return nil, nil, err
			}
			regs[i] = r
			if wb != nil {
				wbs = append(wbs, wb)
			}
			continue
		}
		// Свойства и элементы массивов при динамическом вызове передаются
		// «для возможной записи»: если параметр окажется by-ref, ссылка должна
		// указывать на свойство (иначе Zend выдаёт warning и теряет запись).
		// Сигнатура неизвестна, поэтому диагностики не эмитим.
		switch a.(type) {
		case *PropExpr, *IndexExpr:
			r, wb, err := c.byRefArgInfo(a, byRefInfo{silent: true}, -1)
			if err != nil {
				return nil, nil, err
			}
			regs[i] = r
			if wb != nil {
				wbs = append(wbs, wb)
			}
			continue
		}
		r, err := c.expr(a)
		if err != nil {
			return nil, nil, err
		}
		regs[i] = r
	}
	return regs, wbs, nil
}

func (c *compiler) dynCall(x *DynCallExpr) (uint8, error) {
	if isFCCArgs(x.Args) {
		// First-class callable от произвольного выражения: `$f(...)`.
		return c.expr(&CallExpr{Name: "__php_fcc", Args: []Expr{&IntLit{V: 5}, x.Name}})
	}
	if hasNamedArg(x.Args) {
		callee, err := c.expr(x.Name)
		if err != nil {
			return 0, err
		}
		arr, err := c.buildArgArray(x.Args, true, nil)
		if err != nil {
			return 0, err
		}
		return c.emitCall("__php_call_named", []uint8{callee, arr}), nil
	}
	if hasArgSpread(x.Args) {
		nameReg, err := c.expr(x.Name)
		if err != nil {
			return 0, err
		}
		arr, err := c.buildArgArray(x.Args, true, nil)
		if err != nil {
			return 0, err
		}
		return c.emitCall("call_user_func_array", []uint8{nameReg, arr}), nil
	}
	nameReg, err := c.expr(x.Name)
	if err != nil {
		return 0, err
	}
	argRegs, wbs, err := c.evalDynArgs(x.Args)
	if err != nil {
		return 0, err
	}
	base := c.alloc()
	for range argRegs {
		c.alloc()
	}
	if base != nameReg {
		c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: base, B: nameReg})
	}
	for k, a := range argRegs {
		dst := base + 1 + uint8(k)
		if dst != a {
			c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: dst, B: a})
		}
	}
	d := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpCallDyn, A: d, B: base, C: int32(len(argRegs))})
	c.tmp = d + 1
	for _, wb := range wbs {
		wb()
	}
	return d, nil
}

// dynNew компилирует `new $class(args)`.
func (c *compiler) dynNew(x *NewExpr) (uint8, error) {
	nameReg, err := c.expr(x.ClassExpr)
	if err != nil {
		return 0, err
	}
	if hasNamedArg(x.Args) {
		if msg := argOrderError(x.Args); msg != "" {
			return 0, &fatalError{msg: msg, line: c.exprLine(x.Args[len(x.Args)-1])}
		}
		arr, err := c.buildArgArray(x.Args, true, nil)
		if err != nil {
			return 0, err
		}
		return c.emitCall("__php_new_named", []uint8{nameReg, arr}), nil
	}
	argRegs, wbs, err := c.evalArgs(c.methodRefs["__construct"], x.Args, byRefInfo{})
	if err != nil {
		return 0, err
	}
	base := c.alloc()
	for range argRegs {
		c.alloc()
	}
	if base != nameReg {
		c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: base, B: nameReg})
	}
	for k, a := range argRegs {
		dst := base + 1 + uint8(k)
		if dst != a {
			c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: dst, B: a})
		}
	}
	d := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpNewDyn, A: d, B: base, C: int32(len(argRegs))})
	c.tmp = d + 1
	for _, wb := range wbs {
		wb()
	}
	return d, nil
}

// methodCall компилирует `$recv->name(args)` или `$recv->$expr(args)`.
func (c *compiler) methodCall(recv Expr, name string, nameExpr Expr, args []Expr) (uint8, error) {
	if hasNamedArg(args) {
		if msg := argOrderError(args); msg != "" {
			return 0, &fatalError{msg: msg, line: c.exprLine(args[len(args)-1])}
		}
		recvReg, err := c.expr(recv)
		if err != nil {
			return 0, err
		}
		nm := c.alloc()
		if nameExpr != nil {
			r, err := c.expr(nameExpr)
			if err != nil {
				return 0, err
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: nm, B: r})
		} else {
			c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nm, C: int32(c.chunk.AddConst(vm.StrConst(name)))})
		}
		arr, err := c.buildArgArray(args, true, nil)
		if err != nil {
			return 0, err
		}
		return c.emitCall("__php_method_call_named", []uint8{recvReg, nm, arr}), nil
	}
	if hasArgSpread(args) {
		recvReg, err := c.expr(recv)
		if err != nil {
			return 0, err
		}
		cb := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpArray, A: cb})
		c.chunk.Emit(vm.Instruction{Op: vm.OpAppend, A: cb, B: recvReg})
		nm := c.alloc()
		if nameExpr != nil {
			r, err := c.expr(nameExpr)
			if err != nil {
				return 0, err
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: nm, B: r})
		} else {
			c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nm, C: int32(c.chunk.AddConst(vm.StrConst(name)))})
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpAppend, A: cb, B: nm})
		arr, err := c.buildArgArray(args, true, nil)
		if err != nil {
			return 0, err
		}
		return c.emitCall("call_user_func_array", []uint8{cb, arr}), nil
	}
	recvReg, err := c.expr(recv)
	if err != nil {
		return 0, err
	}
	var nameReg uint8
	if nameExpr != nil {
		nameReg, err = c.expr(nameExpr)
		if err != nil {
			return 0, err
		}
	}
	argRegs, wbs, err := c.evalArgs(c.methodRefs[name], args, c.methodArgInfo(name))
	if err != nil {
		return 0, err
	}
	// Без аргументов окно вызова не нужно: получатель читается прямо из
	// своего регистра, и копия в temp (с учётом/списанием ссылки) не
	// эмитится — METHOD_CALL читает B до записи результата в A, а A всегда
	// свежий temp (не совпадает с recvReg).
	base := recvReg
	if len(argRegs) > 0 {
		base = c.alloc()
		for range argRegs {
			c.alloc()
		}
		if base != recvReg {
			c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: base, B: recvReg})
		}
		for k, a := range argRegs {
			dst := base + 1 + uint8(k)
			if dst != a {
				c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: dst, B: a})
			}
		}
	}
	d := c.alloc()
	if nameExpr != nil {
		c.chunk.Emit(vm.Instruction{Op: vm.OpMethodCallDyn, A: d, B: base,
			C: int32(nameReg) | int32(len(argRegs))<<16})
	} else {
		c.chunk.Emit(vm.Instruction{Op: vm.OpMethodCall, A: d, B: base,
			C: int32(c.chunk.AddConst(vm.StrConst(name))) | int32(len(argRegs))<<16})
	}
	c.tmp = d + 1
	for _, wb := range wbs {
		wb()
	}
	return d, nil
}

// staticCall компилирует `Class::name(args)` (в т.ч. self/static/parent) или
// динамическое имя `Class::$m(...)`.
// classRefUnresolved сообщает, что имя класса не разрешается на этапе
// компиляции и требует рантайм-резолва (автозагрузка/подключаемый файл).
func (c *compiler) classRefUnresolved(name string) bool {
	_, err := c.classRef(name)
	return err != nil
}

// staticClassReg возвращает регистр со значением класса для операций над
// статическими членами класса, заданного выражением (EXPR::$p) или именем,
// неизвестным на этапе компиляции (резолв/автозагрузка в рантайме).
func (c *compiler) staticClassReg(className string, classExpr Expr) (uint8, error) {
	if classExpr != nil {
		return c.expr(classExpr)
	}
	dyn := strings.TrimLeft(className, "\\")
	if strings.HasPrefix(dyn, "$") {
		// Динамический класс: `$cls::$p` (парсер хранит имя переменной).
		return c.expr(&VarExpr{Name: dyn})
	}
	r := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: r, C: int32(c.chunk.AddConst(vm.StrConst(dyn)))})
	return r, nil
}

// staticNameReg возвращает регистр с именем статического члена.
func (c *compiler) staticNameReg(name string, nameExpr Expr) (uint8, error) {
	if nameExpr != nil {
		return c.expr(nameExpr)
	}
	r := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: r, C: int32(c.chunk.AddConst(vm.StrConst(name)))})
	return r, nil
}

// assignDynStatic компилирует запись/составное присваивание в статическое
// свойство класса, определяемого в рантайме: `EXPR::$p = v`, `EXPR::$p += v`.
func (c *compiler) assignDynStatic(t *StaticPropExpr, op string, value Expr) (uint8, error) {
	c.emitLine(c.exprLine(t))
	cls, err := c.staticClassReg(t.Class, t.ClassExpr)
	if err != nil {
		return 0, err
	}
	name, err := c.staticNameReg(t.Name, t.NameExpr)
	if err != nil {
		return 0, err
	}
	if op == "=" {
		r, err := c.expr(value)
		if err != nil {
			return 0, err
		}
		return c.emitCall("__php_dyn_static_set", []uint8{cls, name, r}), nil
	}
	cur := c.emitCall("__php_dyn_static_get", []uint8{cls, name})
	bc, ok := binOp(strings.TrimSuffix(op, "="))
	if !ok {
		return 0, fmt.Errorf("компилятор: неизвестный оператор %q", op)
	}
	r, err := c.expr(value)
	if err != nil {
		return 0, err
	}
	d := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: compoundOp(bc), A: d, B: cur, C: int32(r)})
	return c.emitCall("__php_dyn_static_set", []uint8{cls, name, d}), nil
}

// dynStaticGetExpr компилирует чтение статического свойства класса,
// заданного выражением: `('A' . ”)::$prop` / `$a[0]::$prop`.
func (c *compiler) dynStaticGetExpr(classExpr Expr, name string, nameExpr Expr) (uint8, error) {
	clsReg, err := c.expr(classExpr)
	if err != nil {
		return 0, err
	}
	nm := c.alloc()
	if nameExpr != nil {
		r, err := c.expr(nameExpr)
		if err != nil {
			return 0, err
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: nm, B: r})
	} else {
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nm, C: int32(c.chunk.AddConst(vm.StrConst(name)))})
	}
	return c.emitCall("__php_dyn_static_get", []uint8{clsReg, nm}), nil
}

// staticCallExpr компилирует статический вызов метода класса, заданного
// выражением: `('A' . ”)::m(args)` / `EXPR::$m(args)`.
func (c *compiler) staticCallExpr(classExpr Expr, name string, nameExpr Expr, args []Expr) (uint8, error) {
	clsReg, err := c.expr(classExpr)
	if err != nil {
		return 0, err
	}
	// Zend проверяет тип класса до вычисления имени метода: невалидный
	// класс (`$a::$b()` с неопределённым $a) — ошибка "Class name must be
	// a valid object or a string" без побочных эффектов от $b.
	if nameExpr != nil {
		c.emitCall("__php_check_dyn_class", []uint8{clsReg})
	}
	if hasNamedArg(args) {
		if msg := argOrderError(args); msg != "" {
			return 0, &fatalError{msg: msg, line: c.exprLine(args[len(args)-1])}
		}
		nm := c.alloc()
		if nameExpr != nil {
			r, err := c.expr(nameExpr)
			if err != nil {
				return 0, err
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: nm, B: r})
		} else {
			c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nm, C: int32(c.chunk.AddConst(vm.StrConst(name)))})
		}
		arr, err := c.buildArgArray(args, true, nil)
		if err != nil {
			return 0, err
		}
		return c.emitCall("__php_static_call_named", []uint8{clsReg, nm, arr}), nil
	}
	nm := c.alloc()
	if nameExpr != nil {
		r, err := c.expr(nameExpr)
		if err != nil {
			return 0, err
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: nm, B: r})
	} else {
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nm, C: int32(c.chunk.AddConst(vm.StrConst(name)))})
	}
	arr, err := c.buildArgArray(args, true, nil)
	if err != nil {
		return 0, err
	}
	return c.emitCall("__php_dyn_static_call", []uint8{clsReg, nm, arr}), nil
}

// dynStaticGet компилирует чтение статического свойства класса, заданного
// динамически: `$cls::$prop` / `$cls::${expr}`.
func (c *compiler) dynStaticGet(classVar, name string, nameExpr Expr) (uint8, error) {
	clsReg, err := c.expr(&VarExpr{Name: classVar})
	if err != nil {
		return 0, err
	}
	nm := c.alloc()
	if nameExpr != nil {
		r, err := c.expr(nameExpr)
		if err != nil {
			return 0, err
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: nm, B: r})
	} else {
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nm, C: int32(c.chunk.AddConst(vm.StrConst(name)))})
	}
	return c.emitCall("__php_dyn_static_get", []uint8{clsReg, nm}), nil
}

// dynClassConst компилирует чтение константы класса, заданного динамически:
// `$cls::CONST`.
func (c *compiler) dynClassConst(classVar, name string) (uint8, error) {
	clsReg, err := c.expr(&VarExpr{Name: classVar})
	if err != nil {
		return 0, err
	}
	nm := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nm, C: int32(c.chunk.AddConst(vm.StrConst(name)))})
	return c.emitCall("__php_dyn_class_const", []uint8{clsReg, nm}), nil
}

func (c *compiler) staticCall(cls, name string, nameExpr Expr, args []Expr) (uint8, error) {
	// Динамическое имя класса: `$class::method(...)` (PHP 8). Резолвим класс в
	// рантайме хелпером __php_dyn_static_call (сообщение об ошибке — как у
	// обычного статического вызова: Error "Class \"X\" not found").
	if dyn := strings.TrimLeft(cls, "\\"); strings.HasPrefix(dyn, "$") {
		clsReg, err := c.expr(&VarExpr{Name: dyn})
		if err != nil {
			return 0, err
		}
		// Zend проверяет тип класса до вычисления имени метода: невалидный
		// класс (`$a::$b()` с неопределённым $a) — ошибка "Class name must be
		// a valid object or a string" без побочных эффектов от $b.
		if nameExpr != nil {
			c.emitCall("__php_check_dyn_class", []uint8{clsReg})
		}
		if hasNamedArg(args) {
			if msg := argOrderError(args); msg != "" {
				return 0, &fatalError{msg: msg, line: c.exprLine(args[len(args)-1])}
			}
			nm := c.alloc()
			if nameExpr != nil {
				r, err := c.expr(nameExpr)
				if err != nil {
					return 0, err
				}
				c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: nm, B: r})
			} else {
				c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nm, C: int32(c.chunk.AddConst(vm.StrConst(name)))})
			}
			arr, err := c.buildArgArray(args, true, nil)
			if err != nil {
				return 0, err
			}
			return c.emitCall("__php_static_call_named", []uint8{clsReg, nm, arr}), nil
		}
		nm := c.alloc()
		if nameExpr != nil {
			r, err := c.expr(nameExpr)
			if err != nil {
				return 0, err
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: nm, B: r})
		} else {
			c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nm, C: int32(c.chunk.AddConst(vm.StrConst(name)))})
		}
		arr, err := c.buildArgArray(args, true, nil)
		if err != nil {
			return 0, err
		}
		return c.emitCall("__php_dyn_static_call", []uint8{clsReg, nm, arr}), nil
	}
	if hasNamedArg(args) {
		// Именованные аргументы: раскладку выполняет рантайм
		// (__php_static_call_named), чтобы работали __callStatic и
		// extra named args.
		if msg := argOrderError(args); msg != "" {
			return 0, &fatalError{msg: msg, line: c.exprLine(args[len(args)-1])}
		}
		clsName := strings.TrimLeft(cls, "\\")
		clsReg := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: clsReg, C: int32(c.chunk.AddConst(vm.StrConst(clsName)))})
		nm := c.alloc()
		if nameExpr != nil {
			r, err := c.expr(nameExpr)
			if err != nil {
				return 0, err
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: nm, B: r})
		} else {
			c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nm, C: int32(c.chunk.AddConst(vm.StrConst(name)))})
		}
		arr, err := c.buildArgArray(args, true, nil)
		if err != nil {
			return 0, err
		}
		return c.emitCall("__php_static_call_named", []uint8{clsReg, nm, arr}), nil
	}
	if hasArgSpread(args) {
		if nameExpr == nil {
			clsName := strings.TrimLeft(cls, "\\")
			switch clsName {
			case "self":
				if c.curClass != nil {
					clsName = c.curClass.Name()
				} else {
					clsName = ""
				}
			case "static", "parent":
				clsName = ""
			}
			if clsName != "" {
				fnReg := c.alloc()
				c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: fnReg, C: int32(c.chunk.AddConst(vm.StrConst(clsName + "::" + name)))})
				arr, err := c.buildArgArray(args, true, nil)
				if err != nil {
					return 0, err
				}
				return c.emitCall("call_user_func_array", []uint8{fnReg, arr}), nil
			}
		}
		return 0, fmt.Errorf("компилятор: распаковка аргументов не поддерживается для статического вызова")
	}
	sel, err := c.classRef(cls)
	if err != nil {
		// Класс неизвестен на этапе компиляции (объявлен в подключаемом в
		// рантайме файле): маршрутизируем через call_user_func_array, который
		// резолвит "Class::method" по имени в рантайме.
		return c.staticCallUnknown(cls, name, nameExpr, args)
	}
	var nameReg uint8
	if nameExpr != nil {
		nameReg, err = c.expr(nameExpr)
		if err != nil {
			return 0, err
		}
	}
	argRegs, wbs, err := c.evalArgs(c.methodRefs[name], args, c.methodArgInfo(name))
	if err != nil {
		return 0, err
	}
	d := c.alloc()
	for range argRegs {
		c.alloc()
	}
	for k, a := range argRegs {
		dst := d + 1 + uint8(k)
		if dst != a {
			c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: dst, B: a})
		}
	}
	if nameExpr != nil {
		c.chunk.Emit(vm.Instruction{Op: vm.OpStaticCallDyn, A: d, B: sel,
			C: int32(nameReg) | int32(len(argRegs))<<16})
	} else {
		c.chunk.Emit(vm.Instruction{Op: vm.OpStaticCall, A: d, B: sel,
			C: int32(c.chunk.AddConst(vm.StrConst(name))) | int32(len(argRegs))<<16})
	}
	c.tmp = d + 1 + uint8(len(argRegs))
	for _, wb := range wbs {
		wb()
	}
	return d, nil
}

// staticCallUnknown компилирует статический вызов метода класса, неизвестного
// на этапе компиляции (например, объявленного в подключаемом в рантайме
// файле или находимого автозагрузкой). Резолв и автозагрузка — в рантайме,
// через __php_dyn_static_call (INIT_STATIC_METHOD_CALL в Zend).
func (c *compiler) staticCallUnknown(cls, name string, nameExpr Expr, args []Expr) (uint8, error) {
	clsName := strings.TrimLeft(cls, "\\")
	switch clsName {
	case "self":
		if c.curClass != nil {
			clsName = c.curClass.Name()
		}
	case "static", "parent":
		if c.curClass == nil {
			return 0, fmt.Errorf("компилятор: неизвестный класс %q", strings.TrimLeft(cls, "\\"))
		}
	}
	clsReg := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: clsReg,
		C: int32(c.chunk.AddConst(vm.StrConst(clsName)))})
	if hasNamedArg(args) {
		if msg := argOrderError(args); msg != "" {
			return 0, &fatalError{msg: msg, line: c.exprLine(args[len(args)-1])}
		}
		nm := c.alloc()
		if nameExpr != nil {
			r, err := c.expr(nameExpr)
			if err != nil {
				return 0, err
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: nm, B: r})
		} else {
			c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nm,
				C: int32(c.chunk.AddConst(vm.StrConst(name)))})
		}
		arr, err := c.buildArgArray(args, true, nil)
		if err != nil {
			return 0, err
		}
		return c.emitCall("__php_static_call_named", []uint8{clsReg, nm, arr}), nil
	}
	nm := c.alloc()
	if nameExpr != nil {
		r, err := c.expr(nameExpr)
		if err != nil {
			return 0, err
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: nm, B: r})
	} else {
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nm,
			C: int32(c.chunk.AddConst(vm.StrConst(name)))})
	}
	arr, err := c.buildArgArray(args, true, nil)
	if err != nil {
		return 0, err
	}
	return c.emitCall("__php_dyn_static_call", []uint8{clsReg, nm, arr}), nil
}

// matchExpr компилирует выражение match в цепочку строгих сравнений с
// переходами. Тело выбранной ветви вычисляется после всех проверок, поэтому
// default-ветвь может стоять в любом месте.
func (c *compiler) matchExpr(x *MatchExpr) (uint8, error) {
	c.emitLine(c.exprLine(x))
	res := c.alloc()
	subjVal, err := c.expr(x.Subject)
	if err != nil {
		return 0, err
	}
	// Стабильная копия subject: условия могут менять те же переменные.
	subj := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: subj, B: subjVal})

	defaultIdx := -1
	for i := range x.Arms {
		if x.Arms[i].Default {
			if defaultIdx >= 0 {
				line := x.Arms[i].Line
				if line <= 0 {
					line = c.exprLine(x)
				}
				return 0, &fatalError{msg: "Match expressions may only contain one default arm", line: line}
			}
			defaultIdx = i
		}
	}
	jmps := make(map[int][]int)
	for i := range x.Arms {
		arm := &x.Arms[i]
		if arm.Default {
			continue
		}
		for _, cond := range arm.Conds {
			cr, err := c.expr(cond)
			if err != nil {
				return 0, err
			}
			d := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpIdentical, A: d, B: subj, C: int32(cr)})
			j := c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfTrue, A: d})
			jmps[i] = append(jmps[i], j)
		}
	}
	if defaultIdx >= 0 {
		j := c.chunk.Emit(vm.Instruction{Op: vm.OpJump})
		jmps[defaultIdx] = append(jmps[defaultIdx], j)
	} else {
		c.emitCall("__php_unhandled_match", []uint8{subj})
	}
	var endJumps []int
	for i := range x.Arms {
		pc := c.chunk.Len()
		for _, j := range jmps[i] {
			c.setJump(j, pc)
		}
		br, err := c.expr(x.Arms[i].Body)
		if err != nil {
			return 0, err
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: res, B: br})
		endJumps = append(endJumps, c.chunk.Emit(vm.Instruction{Op: vm.OpJump}))
	}
	end := c.chunk.Len()
	for _, j := range endJumps {
		c.setJump(j, end)
	}
	return res, nil
}

// nullsafeExpr компилирует цепочку `?->`: если базовое значение null,
// результатом становится null и цепочка Body не выполняется.
func (c *compiler) nullsafeExpr(x *NullsafeExpr) (uint8, error) {
	c.emitLine(c.exprLine(x))
	// `$a?->b(...)` / `$a?->b->c(...)` — FCC в nullsafe-цепочке запрещён.
	if fccInChain(x.Body) {
		return 0, c.newFatal("Cannot combine nullsafe operator with Closure creation")
	}
	recv, err := c.expr(x.Recv)
	if err != nil {
		return 0, err
	}
	base := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: base, B: recv})
	cmp := c.alloc()
	emitNullIdent(c, cmp, base)
	jnull := c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfTrue, A: cmp})
	res := c.alloc()
	saveReg, saveHas := c.nullsafeBase, c.hasBase
	c.nullsafeBase, c.hasBase = base, true
	br, err := c.expr(x.Body)
	c.nullsafeBase, c.hasBase = saveReg, saveHas
	if err != nil {
		return 0, err
	}
	c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: res, B: br})
	jend := c.chunk.Emit(vm.Instruction{Op: vm.OpJump})
	c.setJump(jnull, c.chunk.Len())
	c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: res})
	c.setJump(jend, c.chunk.Len())
	return res, nil
}

// byRefThisExpr сообщает, что выражение — `$this`: ссылка на $this в Zend не
// создаётся (элементы [&$this] и т.п. хранят объект по значению).
func byRefThisExpr(e Expr) bool {
	v, ok := e.(*VarExpr)
	return ok && v.NameExpr == nil && (v.Name == "$this" || v.Name == "this")
}

func (c *compiler) arrayLit(x *ArrayLit) (uint8, error) {
	for _, el := range x.Elems {
		if el.Val == nil {
			line := el.Line
			if line <= 0 {
				line = c.exprLine(x)
			}
			return 0, &fatalError{msg: "Cannot use empty array elements in arrays", line: line}
		}
	}
	r := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpArray, A: r})
	for _, el := range x.Elems {
		// Временные регистры предыдущего элемента освобождаются, иначе
		// длинные литералы переполняют 8-битный счётчик регистров.
		c.tmp = r + 1
		if el.Spread {
			// Литеральная не-массивная распаковка — фатал компиляции
			// (Zend: zend_compile_array → "Only arrays and Traversables can
			// be unpacked"); переменные и вызовы остаются рантайм-ошибками.
			switch el.Val.(type) {
			case *IntLit, *FloatLit, *StrLit, *BoolLit, *NullLit:
				return 0, &fatalError{msg: "Only arrays and Traversables can be unpacked", line: el.Line}
			}
			src, err := c.expr(el.Val)
			if err != nil {
				return 0, err
			}
			if c.constEval > 0 || c.constCtx {
				// Константное выражение допускает распаковку только массива
				// (Zend zend_ast_evaluate: "Only arrays can be unpacked in
				// constant expression").
				src = c.emitCall("__php_const_spread_check", []uint8{src})
			}
			r = c.emitCall("__php_spread", []uint8{r, src})
			continue
		}
		if el.Key == nil {
			var v uint8
			var err error
			if el.ByRef && !byRefThisExpr(el.Val) {
				c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
				v, err = c.refCellForExpr(el.Val)
				c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
			} else {
				v, err = c.expr(el.Val)
			}
			if err != nil {
				return 0, err
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpAppend, A: r, B: v})
			continue
		}
		k, err := c.expr(el.Key)
		if err != nil {
			return 0, err
		}
		var v uint8
		if el.ByRef && !byRefThisExpr(el.Val) {
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
			v, err = c.refCellForExpr(el.Val)
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
		} else {
			v, err = c.expr(el.Val)
		}
		if err != nil {
			return 0, err
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpIndexSet, A: r, B: k, C: int32(v)})
	}
	return r, nil
}

// fccInChain ищет first-class callable `f(...)` в цепочке приёмников
// (`$a?->b(...)`, `$a?->b->c(...)`): Zend запрещает Closure-создание в
// nullsafe-цепочке.
func fccInChain(e Expr) bool {
	for e != nil {
		switch v := e.(type) {
		case *MethodCallExpr:
			if isFCCArgs(v.Args) {
				return true
			}
			e = v.Recv
		case *IndexExpr:
			e = v.Arr
		case *PropExpr:
			e = v.Recv
		case *NullsafeExpr:
			e = v.Body
		default:
			return false
		}
	}
	return false
}

// containsNullsafe сообщает, что выражение содержит nullsafe-цепочку (`?->`).
// Zend запрещает использовать её в контексте записи (write/ref/foreach/unset).
func containsNullsafe(e Expr) bool {
	switch x := e.(type) {
	case nil:
		return false
	case *NullsafeExpr:
		return true
	case *PropExpr:
		return containsNullsafe(x.Recv) || containsNullsafe(x.NameExpr)
	case *IndexExpr:
		return containsNullsafe(x.Arr) || containsNullsafe(x.Key)
	case *StaticPropExpr:
		return containsNullsafe(x.NameExpr)
	case *MethodCallExpr:
		return containsNullsafe(x.Recv) || containsNullsafe(x.NameExpr)
	case *StaticCallExpr:
		return containsNullsafe(x.NameExpr)
	case *DynCallExpr:
		return containsNullsafe(x.Name)
	case *NewExpr:
		return containsNullsafe(x.ClassExpr)
	case *CastExpr:
		return containsNullsafe(x.X)
	case *UnaryExpr:
		return containsNullsafe(x.X)
	case *BinExpr:
		return containsNullsafe(x.L) || containsNullsafe(x.R)
	case *AssignExpr:
		return containsNullsafe(x.Target) || containsNullsafe(x.Value)
	case *ListExpr:
		for _, el := range x.Elems {
			if containsNullsafe(el.Target) || containsNullsafe(el.Key) {
				return true
			}
		}
	}
	return false
}

// compoundOp — опкод составного присваивания. Для `.=` используется
// OpConcatApp: строка накапливается в буфере с геометрическим ростом, а не
// пересобирается целиком на каждой записи (устраняет O(n^2)).
func compoundOp(bc vm.Opcode) vm.Opcode {
	if bc == vm.OpConcat {
		return vm.OpConcatApp
	}
	return bc
}

func (c *compiler) assign(target Expr, op string, value Expr, byref bool) (uint8, error) {
	discardResult := c.discardResult
	c.discardResult = false
	if _, isList := target.(*ListExpr); !isList && containsNullsafe(target) {
		return 0, c.newFatal("Can't use nullsafe operator in write context")
	}
	if isGlobalsVar(target) {
		return 0, c.newFatal(globalsModifyMsg)
	}
	if op == "??=" {
		return c.assignCoalesce(target, value)
	}
	switch t := target.(type) {
	case *VarExpr:
		if t.NameExpr == nil && (t.Name == "$this" || t.Name == "this") {
			return 0, c.newFatal("Cannot re-assign $this")
		}
		if op == "=" && byref {
			if t.NameExpr != nil {
				// $$x = &$y — не поддерживаем alias для динамического имени.
				return 0, fmt.Errorf("компилятор: ссылка в переменную переменную не поддерживается")
			}
			return c.assignRef(t.Name, value)
		}
		if op == "=" {
			// Прямая запись в регистр переменной (без промежуточного MOVE),
			// когда переменная локальна/промотирована и не является ссылкой.
			if t.NameExpr == nil && !c.isRef(t.Name) && !c.useGlobal(t.Name) && !c.usesSymtab {
				r, err := c.exprInto(value, c.varReg(t.Name))
				if err != nil {
					return 0, err
				}
				// Присваивание определяет переменную: снимаем флаг unset-а.
				// Без этого чтение после `unset($x); $x = ...` давало ложное
				// «Undefined variable», и семантика зависела от состояния
				// регистра-флага на входе кадра.
				c.clearUndef(t.Name)
				return r, nil
			}
			if t.NameExpr != nil {
				// Динамическое имя (${expr} = v): Zend вычисляет имя
				// (левую часть) до правого операнда.
				ne, err := c.expr(t.NameExpr)
				if err != nil {
					return 0, err
				}
				r, err := c.expr(value)
				if err != nil {
					return 0, err
				}
				c.chunk.Emit(vm.Instruction{Op: vm.OpGlobalSetDyn, A: r, C: int32(ne)})
				return r, nil
			}
			r, err := c.expr(value)
			if err != nil {
				return 0, err
			}
			c.storeVar(t.Name, r)
			return r, nil
		}
		bc, ok := binOp(strings.TrimSuffix(op, "="))
		if !ok {
			return 0, fmt.Errorf("компилятор: неизвестный оператор %q", op)
		}
		// Составное присваивание в локальную/промотированную переменную:
		// считаем прямо в её регистр.
		if t.NameExpr == nil && !c.isRef(t.Name) && !c.useGlobal(t.Name) && !c.usesSymtab {
			v := c.varReg(t.Name)
			cur := c.loadVarChecked(t.Name, c.exprLine(t))
			if k, ok := numConst(value); ok {
				if kop, ok := kFormOp(bc, k); ok {
					c.chunk.Emit(vm.Instruction{Op: kop, A: v, B: cur, C: int32(c.chunk.AddConst(k))})
					return v, nil
				}
			}
			r, err := c.expr(value)
			if err != nil {
				return 0, err
			}
			c.chunk.Emit(vm.Instruction{Op: specOp(compoundOp(bc)), A: v, B: cur, C: int32(r)})
			return v, nil
		}
		var cur uint8
		var err error
		if t.NameExpr != nil {
			cur, err = c.loadVarDyn(t.NameExpr)
		} else {
			cur = c.loadVarChecked(t.Name, c.exprLine(t))
		}
		if err != nil {
			return 0, err
		}
		var r uint8
		if t.NameExpr == nil && c.deferLeftGlobal(t, value) {
			// Глобальная переменная: читаем её после правого операнда
			// (Zend читает CV в момент выполнения ASSIGN_OP).
			r, err = c.expr(value)
			if err != nil {
				return 0, err
			}
			cur = c.loadVarChecked(t.Name, c.exprLine(t))
		} else {
			r, err = c.expr(value)
			if err != nil {
				return 0, err
			}
		}
		// Для переменной (в т.ч. by-ref параметра) считаем прямо в регистр
		// текущего значения: лишний временный регистр здесь не нужен и может
		// конфликтовать с регистрами ссылочных опкодов.
		if t.NameExpr == nil {
			c.chunk.Emit(vm.Instruction{Op: compoundOp(bc), A: cur, B: cur, C: int32(r)})
			c.storeVar(t.Name, cur)
			return cur, nil
		}
		d := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: compoundOp(bc), A: d, B: cur, C: int32(r)})
		if err := c.storeVarDyn(t.NameExpr, d); err != nil {
			return 0, err
		}
		return d, nil
	case *IndexExpr:
		if op == "=" && byref {
			return c.assignIndexRef(t, value)
		}
		return c.assignIndex(t, op, value, discardResult)
	case *PropExpr:
		if op == "=" && byref {
			return c.assignPropRef(t, value)
		}
		var obj uint8
		var err error
		if op == "=" {
			// Плоское присваивание вложенному свойству: приёмник —
			// промежуточное звено (инициализированное readonly-свойство
			// допустимо, variation_nested).
			obj, err = c.exprChainRecv(t.Recv)
		} else {
			// Составное присваивание сначала читает приёмник (обычное
			// чтение): неинициализированное типизированное свойство даёт
			// ошибку доступа (variation_nested).
			obj, err = c.exprChainRead(t.Recv)
		}
		if err != nil {
			return 0, err
		}
		var nameReg uint8
		if t.NameExpr != nil {
			nameReg, err = c.expr(t.NameExpr)
			if err != nil {
				return 0, err
			}
		}
		name := int32(c.chunk.AddConst(vm.StrConst(t.Name)))
		setProp := func(v uint8) {
			if t.NameExpr != nil {
				c.chunk.Emit(vm.Instruction{Op: vm.OpPropSetDyn, A: obj, B: v, C: int32(nameReg)})
			} else {
				c.chunk.Emit(vm.Instruction{Op: vm.OpPropSet, A: obj, B: v, C: name})
			}
		}
		if op == "=" {
			r, err := c.expr(value)
			if err != nil {
				return 0, err
			}
			setProp(r)
			return r, nil
		}
		bc, ok := binOp(strings.TrimSuffix(op, "="))
		if !ok {
			return 0, fmt.Errorf("компилятор: неизвестный оператор %q", op)
		}
		// Составное присваивание читает свойство в write-контексте
		// (FETCH_OBJ_RW): запись в свойство null — Error, а не Warning
		// (bug75241); readonly-свойство диагностику не даёт на чтении —
		// её выдаёт запись (readonly_clone_error4), неинициализированное
		// типизированное — ошибка доступа (variation.phpt).
		cur := c.alloc()
		if t.NameExpr != nil {
			c.chunk.Emit(vm.Instruction{Op: vm.OpPropGetRWDyn, A: cur, B: obj, C: int32(nameReg)})
		} else {
			c.chunk.Emit(vm.Instruction{Op: vm.OpPropGetRWRead, A: cur, B: obj, C: name})
		}
		r, err := c.expr(value)
		if err != nil {
			return 0, err
		}
		d := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: compoundOp(bc), A: d, B: cur, C: int32(r)})
		setProp(d)
		return d, nil
	case *StaticPropExpr:
		if op == "=" && byref {
			return c.assignStaticPropRef(t, value)
		}
		sel, err := c.classRef(t.Class)
		if err != nil || t.ClassExpr != nil {
			return c.assignDynStatic(t, op, value)
		}
		var nameReg uint8
		if t.NameExpr != nil {
			nameReg, err = c.expr(t.NameExpr)
			if err != nil {
				return 0, err
			}
		}
		name := int32(c.chunk.AddConst(vm.StrConst(t.Name)))
		setStatic := func(v uint8) {
			if t.NameExpr != nil {
				c.chunk.Emit(vm.Instruction{Op: vm.OpStaticSetDyn, A: v, B: sel, C: int32(nameReg)})
			} else {
				c.chunk.Emit(vm.Instruction{Op: vm.OpStaticSet, A: v, B: sel, C: name})
			}
		}
		getStatic := func() uint8 {
			cur := c.alloc()
			if t.NameExpr != nil {
				c.chunk.Emit(vm.Instruction{Op: vm.OpStaticGetDyn, A: cur, B: sel, C: int32(nameReg)})
			} else {
				c.chunk.Emit(vm.Instruction{Op: vm.OpStaticGet, A: cur, B: sel, C: name})
			}
			return cur
		}
		if op == "=" {
			r, err := c.expr(value)
			if err != nil {
				return 0, err
			}
			setStatic(r)
			return r, nil
		}
		bc, ok := binOp(strings.TrimSuffix(op, "="))
		if !ok {
			return 0, fmt.Errorf("компилятор: неизвестный оператор %q", op)
		}
		cur := getStatic()
		r, err := c.expr(value)
		if err != nil {
			return 0, err
		}
		d := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: compoundOp(bc), A: d, B: cur, C: int32(r)})
		setStatic(d)
		return d, nil
	case *ListExpr:
		return c.assignList(t, value)
	}
	return 0, fmt.Errorf("компилятор: выражение %T не является lvalue", target)
}

// writeCtxBuiltinFatal — фатал Zend при записи в результат встроенной
// функции (в т.ч. цепочки индексов от него).
func (c *compiler) writeCtxBuiltinFatal(e Expr) error {
	return &fatalError{msg: "Cannot use result of built-in function in write context", line: c.exprLine(e)}
}

// specialFuncNames — встроенные функции, которые Zend компилирует в
// специализированные опкоды с временным результатом (IS_TMP_VAR). Запись в
// результат такой функции (в т.ч. в его индекс) — компайл-фатал
// "Cannot use result of built-in function in write context".
var specialFuncNames = map[string]bool{
	"strlen": true, "is_null": true, "is_bool": true,
	"is_long": true, "is_int": true, "is_integer": true,
	"is_float": true, "is_double": true, "is_string": true,
	"is_array": true, "is_object": true, "is_resource": true, "is_scalar": true,
	"boolval": true, "intval": true, "floatval": true, "doubleval": true,
	"strval": true, "defined": true, "chr": true, "ord": true,
	"call_user_func_array": true, "call_user_func": true, "in_array": true,
	"count": true, "sizeof": true,
	"get_class": true, "get_called_class": true, "gettype": true,
	"func_num_args": true, "func_get_args": true,
	"array_slice": true, "array_key_exists": true,
}

// builtinCallBase сообщает, что выражение — цепочка индексов от вызова
// специализированной встроенной функции (`strlen("x")[0]`). Zend отвергает
// запись в такой результат: "Cannot use result of built-in function in write
// context" (для обычных функций временный массив изменить можно).
func (c *compiler) builtinCallBase(e Expr) bool {
	for {
		switch t := e.(type) {
		case *IndexExpr:
			e = t.Arr
		case *CallExpr:
			for _, name := range []string{t.Name, t.Fallback} {
				if name == "" {
					continue
				}
				if specialFuncNames[strings.ToLower(strings.TrimPrefix(name, "\\"))] {
					return true
				}
			}
			return false
		default:
			return false
		}
	}
}

// callLikeBase сообщает, что база write-цепочки — вызов функции/метода
// (в т.ч. через промежуточные индексы). Подавление диагностик на такой базе
// недопустимо: Zend не глушит код вызываемой функции, когда запись идёт в
// результат вызова (`$foo->c()[0] = 1` — диагностики внутри c() видны).
func callLikeBase(e Expr) bool {
	for {
		switch t := e.(type) {
		case *IndexExpr:
			e = t.Arr
		case *CallExpr, *MethodCallExpr, *StaticCallExpr, *DynCallExpr, *NewExpr:
			return true
		default:
			return false
		}
	}
}

// assignCoalesce компилирует `target ??= value`: если текущее значение
// существует и не null, возвращается оно; иначе value вычисляется один раз и
// записывается в target.
func (c *compiler) assignCoalesce(target Expr, value Expr) (uint8, error) {
	d := c.alloc()
	var setter func(uint8) error
	switch t := target.(type) {
	case *VarExpr:
		if t.NameExpr == nil && (t.Name == "$this" || t.Name == "this") {
			return 0, c.newFatal("Cannot re-assign $this")
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
		var cur uint8
		var err error
		if t.NameExpr != nil {
			ne, nerr := c.expr(t.NameExpr)
			if nerr != nil {
				c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
				return 0, nerr
			}
			cur, err = c.loadVarDyn(&RegExpr{Reg: ne})
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
			if err == nil {
				setter = func(r uint8) error { return c.storeVarDyn(&RegExpr{Reg: ne}, r) }
			}
		} else {
			cur = c.loadVar(t.Name)
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
			setter = func(r uint8) error {
				c.storeVar(t.Name, r)
				return nil
			}
		}
		if err != nil {
			return 0, err
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: d, B: cur})
	case *IndexExpr:
		if t.Key == nil {
			return 0, &fatalError{msg: "Cannot use [] for reading", line: c.exprLine(t)}
		}
		if c.builtinCallBase(t.Arr) {
			return 0, c.writeCtxBuiltinFatal(t)
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
		arr, wb, err := c.indexBase(t, true)
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
		if err != nil {
			return 0, err
		}
		k, err := c.expr(t.Key)
		if err != nil {
			return 0, err
		}
		c.emitLine(c.exprLine(t))
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
		c.chunk.Emit(vm.Instruction{Op: vm.OpIndex, A: d, B: arr, C: int32(k)})
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
		setter = func(r uint8) error {
			c.chunk.Emit(vm.Instruction{Op: vm.OpIndexSet, A: arr, B: k, C: int32(r)})
			if wb != nil {
				wb(arr)
			}
			return nil
		}
	case *PropExpr:
		obj, err := c.exprForWrite(t.Recv)
		if err != nil {
			return 0, err
		}
		var nameReg uint8
		if t.NameExpr != nil {
			nameReg, err = c.expr(t.NameExpr)
			if err != nil {
				return 0, err
			}
		}
		name := int32(c.chunk.AddConst(vm.StrConst(t.Name)))
		c.emitLine(c.exprLine(t))
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
		if t.NameExpr != nil {
			c.chunk.Emit(vm.Instruction{Op: vm.OpPropGetDyn, A: d, B: obj, C: int32(nameReg)})
		} else {
			c.chunk.Emit(vm.Instruction{Op: vm.OpPropGet, A: d, B: obj, C: name})
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
		setter = func(r uint8) error {
			if t.NameExpr != nil {
				c.chunk.Emit(vm.Instruction{Op: vm.OpPropSetDyn, A: obj, B: r, C: int32(nameReg)})
			} else {
				c.chunk.Emit(vm.Instruction{Op: vm.OpPropSet, A: obj, B: r, C: name})
			}
			return nil
		}
	case *StaticPropExpr:
		if t.ClassExpr != nil {
			cls, cerr := c.staticClassReg(t.Class, t.ClassExpr)
			if cerr != nil {
				return 0, cerr
			}
			nmr, cerr := c.staticNameReg(t.Name, t.NameExpr)
			if cerr != nil {
				return 0, cerr
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
			c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: d, B: c.emitCall("__php_dyn_static_get", []uint8{cls, nmr})})
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
			setter = func(r uint8) error {
				c.emitCall("__php_dyn_static_set", []uint8{cls, nmr, r})
				return nil
			}
			break
		}
		sel, err := c.classRef(t.Class)
		if err != nil {
			cls, cerr := c.staticClassReg(t.Class, nil)
			if cerr != nil {
				return 0, cerr
			}
			nmr, cerr := c.staticNameReg(t.Name, t.NameExpr)
			if cerr != nil {
				return 0, cerr
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
			c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: d, B: c.emitCall("__php_dyn_static_get", []uint8{cls, nmr})})
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
			setter = func(r uint8) error {
				c.emitCall("__php_dyn_static_set", []uint8{cls, nmr, r})
				return nil
			}
			break
		}
		var nameReg uint8
		if t.NameExpr != nil {
			nameReg, err = c.expr(t.NameExpr)
			if err != nil {
				return 0, err
			}
		}
		name := int32(c.chunk.AddConst(vm.StrConst(t.Name)))
		c.emitLine(c.exprLine(t))
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
		if t.NameExpr != nil {
			c.chunk.Emit(vm.Instruction{Op: vm.OpStaticGetDyn, A: d, B: sel, C: int32(nameReg)})
		} else {
			c.chunk.Emit(vm.Instruction{Op: vm.OpStaticGet, A: d, B: sel, C: name})
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
		setter = func(r uint8) error {
			if t.NameExpr != nil {
				c.chunk.Emit(vm.Instruction{Op: vm.OpStaticSetDyn, A: r, B: sel, C: int32(nameReg)})
			} else {
				c.chunk.Emit(vm.Instruction{Op: vm.OpStaticSet, A: r, B: sel, C: name})
			}
			return nil
		}
	default:
		return 0, c.newFatal("Cannot use temporary expression in write context")
	}
	n := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: n})
	isn := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpIdentical, A: isn, B: d, C: int32(n)})
	jf := c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfFalse, A: isn})
	r, err := c.expr(value)
	if err != nil {
		return 0, err
	}
	if err := setter(r); err != nil {
		return 0, err
	}
	c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: d, B: r})
	c.setJump(jf, c.chunk.Len())
	return d, nil
}

func (c *compiler) assignList(t *ListExpr, value Expr) (uint8, error) {
	return c.assignListW(t, value, nil)
}

// isListRefRHS сообщает, что выражение допустимо в правой части ссылочной
// деструктуризации (Zend zend_is_variable_or_call: переменные и вызовы).
func isListRefRHS(e Expr) bool {
	switch e.(type) {
	case *VarExpr, *IndexExpr, *PropExpr, *StaticPropExpr,
		*CallExpr, *MethodCallExpr, *StaticCallExpr, *DynCallExpr:
		return true
	}
	return false
}

// listHasRefsDeep — есть ли в деструктуризации ссылочные элементы, включая
// вложенные списки (Zend zend_propagate_list_refs: ссылка на любом уровне
// делает элемент внешнего списка ссылочным).
func listHasRefsDeep(t *ListExpr) bool {
	for _, el := range t.Elems {
		if el.ByRef {
			return true
		}
		if nested, ok := el.Target.(*ListExpr); ok && listHasRefsDeep(nested) {
			return true
		}
	}
	return false
}

// verifyListShape проверяет форму деструктуризации (Zend
// zend_compile_list_assign/zend_verify_list_assign_target): пустые элементы в
// keyed-списке, смешение keyed/unkeyed, стили вложенных списков и
// записываемость целей.
func (c *compiler) verifyListShape(t *ListExpr) error {
	for _, el := range t.Elems {
		if el.Spread {
			return &fatalError{msg: "Spread operator is not supported in assignments", line: c.exprLine(t)}
		}
	}
	isKeyed := false
	for _, el := range t.Elems {
		if el.Target != nil {
			isKeyed = el.Key != nil
			break
		}
	}
	has := false
	for _, el := range t.Elems {
		if el.Target == nil {
			if isKeyed {
				return &fatalError{msg: "Cannot use empty array entries in keyed array assignment", line: c.exprLine(t)}
			}
			continue
		}
		has = true
		if isKeyed != (el.Key != nil) {
			return &fatalError{msg: "Cannot mix keyed and unkeyed array entries in assignments", line: c.exprLine(t)}
		}
		if err := c.verifyListTarget(el.Target, t.Style); err != nil {
			return err
		}
	}
	if !has {
		return &fatalError{msg: "Cannot use empty list", line: c.exprLine(t)}
	}
	return nil
}

// verifyListTarget — допустима ли цель элемента деструктуризации.
func (c *compiler) verifyListTarget(target Expr, style int) error {
	switch v := target.(type) {
	case *ListExpr:
		if v.Style == listStyleArray {
			return &fatalError{msg: "Cannot assign to array(), use [] instead", line: c.exprLine(target)}
		}
		if v.Style != style {
			return &fatalError{msg: "Cannot mix [] and list()", line: c.exprLine(target)}
		}
	case *VarExpr, *IndexExpr, *PropExpr, *StaticPropExpr:
		// writable
	default:
		return &fatalError{msg: "Assignments can only happen to writable values", line: c.exprLine(target)}
	}
	return nil
}

// assignListW — компилирует деструктуризацию. listWB сохраняется для
// совместимости вложенных вызовов (см. assignListCell); при ссылочной
// деструктуризации база вычисляется один раз как Ref-ячейка (Zend MAKE_REF),
// и все элементы читаются «на месте» (OpListFetchRef), поэтому внешний
// writeback больше не нужен.
func (c *compiler) assignListW(t *ListExpr, value Expr, listWB func(uint8)) (uint8, error) {
	if err := c.verifyListShape(t); err != nil {
		return 0, err
	}
	hasRefs := listHasRefsDeep(t)
	if hasRefs {
		if containsNullsafe(value) {
			return 0, &fatalError{msg: "Cannot take reference of a nullsafe chain", line: c.exprLine(value)}
		}
		if !isListRefRHS(value) {
			return 0, &fatalError{msg: "Cannot assign reference to non referenceable value", line: c.exprLine(value)}
		}
		base, err := c.listRefBase(value)
		if err != nil {
			return 0, err
		}
		return c.assignListCell(t, base)
	}
	r, err := c.expr(value)
	if err != nil {
		return 0, err
	}
	src := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: src, B: r})
	if _, err := c.assignListCell(t, src); err != nil {
		return 0, err
	}
	return src, nil
}

// listRefBase вычисляет базу ссылочной деструктуризации как Ref-ячейку
// (Zend zend_compile_var BP_VAR_W + ZEND_MAKE_REF). Переменная, ставшая
// ссылкой, запоминается компилятором (как в Zend: CV становится IS_REFERENCE).
// Для вызовов результат может не быть ссылкой — тогда OpListFetchRef сам
// напечатает Notice и вернёт копию.
func (c *compiler) listRefBase(value Expr) (uint8, error) {
	switch v := value.(type) {
	case *VarExpr:
		if v.NameExpr != nil {
			return c.loadVarDyn(v.NameExpr)
		}
		if v.Name == "$this" || v.Name == "this" {
			return 0, c.newFatal("Cannot re-assign $this")
		}
		if isGlobalsVar(v) {
			return 0, c.newFatal(globalsModifyMsg)
		}
		cell := c.refCell(v.Name)
		// Ячейку передаём в отдельном регистре: цель деструктуризации может
		// переприсвоить саму переменную (list(&$a, &$b) = $a), но выборка
		// оставшихся элементов должна идти через исходную ячейку (Zend).
		base := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpRefBind, A: base, B: cell})
		return base, nil
	case *IndexExpr, *PropExpr, *StaticPropExpr:
		return c.refCellForExpr(value)
	}
	return c.expr(value)
}

// assignListCell — элементы деструктуризации для уже вычисленной базы:
// base — Ref-ячейка (ссылочные элементы идут «на месте») либо обычное
// значение (тогда OpListFetchRef даст Notice и копию).
func (c *compiler) assignListCell(t *ListExpr, base uint8) (uint8, error) {
	if err := c.verifyListShape(t); err != nil {
		return 0, err
	}
	var err error
	for i, el := range t.Elems {
		if el.Target == nil {
			continue
		}
		if containsNullsafe(el.Target) {
			return 0, c.newFatal("Assignments can only happen to writable values")
		}
		var keyReg uint8
		if el.Key != nil {
			keyReg, err = c.expr(el.Key)
			if err != nil {
				return 0, err
			}
		} else {
			keyReg = c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: keyReg, C: int32(c.chunk.AddConst(vm.IntConst(int64(i))))})
		}
		elByRef := el.ByRef
		var nested *ListExpr
		if n, ok := el.Target.(*ListExpr); ok {
			nested = n
			if listHasRefsDeep(n) {
				elByRef = true
			}
		}
		if elByRef {
			// Ссылочная выборка элемента (Zend FETCH_LIST_W + MAKE_REF):
			// база — Ref-ячейка → запись на месте (массив разделяется по
			// COW и возвращается в ячейку), элемент становится ссылкой.
			// Иначе — Notice и копия значения.
			cell := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpListFetchRef, A: cell, B: base, C: int32(keyReg)})
			if nested != nil {
				if _, err := c.assignListCell(nested, cell); err != nil {
					return 0, err
				}
				continue
			}
			// Цель-переменная получает алиас ячейки; значение из read-ветки
			// (не ссылка) оборачивается в новую ячейку (Zend ASSIGN_REF).
			c.chunk.Emit(vm.Instruction{Op: vm.OpRefEnsure, A: cell})
			if v, ok := el.Target.(*VarExpr); ok && v.NameExpr == nil && v.Name != "$this" && v.Name != "this" {
				c.bindRefCell(v.Name, cell)
			} else if _, err := c.assign(el.Target, "=", &RegExpr{Reg: cell}, true); err != nil {
				return 0, err
			}
			continue
		}
		// Element list() читается через __php_list_index: массив с
		// отсутствующим ключом даёт Warning, строка/скаляр — null без
		// диагностик (Zend FETCH_LIST_R); объект с ArrayAccess — offsetGet.
		v := c.emitCall("__php_list_index", []uint8{base, keyReg})
		if _, err := c.assign(el.Target, "=", &RegExpr{Reg: v}, false); err != nil {
			return 0, err
		}
	}
	// Результат выражения присваивания — текущее значение базы (Zend
	// возвращает RHS; ячейка могла быть разделена по COW при ссылочной
	// выборке — читаем её актуальное состояние).
	out := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpRefGet, A: out, B: base})
	return out, nil
}

// assignRef компилирует `$name = &value` (alias-присваивание).
func (c *compiler) assignRef(name string, value Expr) (uint8, error) {
	if containsNullsafe(value) {
		return 0, c.newFatal("Cannot take reference of a nullsafe chain")
	}
	if c.refAssignNoticeNeeded(value) {
		if c.callReturnsByValue(value) {
			// Известная функция без `function &`: Zend выдаёт Notice
			// (zend_wrong_assign_to_variable_reference) и выполняет ОБЫЧНОЕ
			// присваивание значения — alias не создаётся. Для static/global
			// это пишет значение в таблицу (повторный вызов видит его через
			// isset), для ref-параметра — сквозь ячейку.
			c.emitLine(c.exprLine(value))
			r, err := c.expr(value)
			if err != nil {
				return 0, err
			}
			c.emitRefNotice("Only variables should be assigned by reference")
			// Присваивается ЗНАЧЕНИЕ (возвращённая ссылка разыменовывается,
			// Zend zend_assign_to_variable_ex).
			rv := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpRefGet, A: rv, B: r})
			c.storeVar(name, rv)
			c.emitRefSweep()
			return c.loadVar(name), nil
		}
		// `$b =& <временное>`: Notice выдаётся в рантайме, только если
		// результат вызова/выражения не является ссылкой; иначе ссылка
		// сохраняется.
		c.emitLine(c.exprLine(value))
		r, err := c.expr(value)
		if err != nil {
			return 0, err
		}
		chk := c.emitCall("__php_ref_assign_check", []uint8{r, c.refMsgReg("Only variables should be assigned by reference")})
		c.bindRefCell(name, chk)
		c.emitRefSweep()
		return c.loadVar(name), nil
	}
	srcCell, err := c.refCellForExpr(value)
	if err != nil {
		return 0, err
	}
	c.bindRefCell(name, srcCell)
	c.emitRefSweep()
	return c.loadVar(name), nil
}

// emitRefSweep ставит точку времени жизни сразу после привязки ссылки:
// Zend в ASSIGN_REF уничтожает старое значение цели ТУТ ЖЕ (GC_DTOR после
// подстановки ссылки), поэтому его __destruct может успеть изменить саму
// ячейку до чтения результата присваивания (GH-10168: var_dump($a = &$tmp)
// с деструктором, обнуляющим $a). Кандидат появляется в момент привязки
// (noteDropped/wRegSlow), поэтому проход здесь точечный.
func (c *compiler) emitRefSweep() {
	// Как и периодические точки: проход нужен только там, где вообще могут
	// появиться учитываемые значения (после привязки ссылки MayTrack уже
	// истинно, но условие оставляем явным).
	if !c.chunk.MayTrack() {
		return
	}
	c.chunk.Emit(vm.Instruction{Op: vm.OpSweep})
}

// callReturnsByValue сообщает, что значение — вызов известной функции,
// которая не объявлена `function &` (поэтому `=&` даёт Notice и копию).
func (c *compiler) callReturnsByValue(e Expr) bool {
	ce, ok := e.(*CallExpr)
	if !ok {
		return false
	}
	if i, ok := c.idx[ce.Name]; ok {
		return !c.byRefNonGen(i)
	}
	if ce.Fallback != "" {
		if i, ok := c.idx[ce.Fallback]; ok {
			return !c.byRefNonGen(i)
		}
	}
	return false
}

// refCellForExpr возвращает регистр с Ref-ячейкой для lvalue-выражения
// (переменная, элемент массива, свойство), создавая ячейку/ссылку при
// необходимости. Для не-lvalue вычисляет значение как есть.
func (c *compiler) refCellForExpr(value Expr) (uint8, error) {
	switch v := value.(type) {
	case *VarExpr:
		if v.NameExpr == nil {
			if v.Name == "$this" || v.Name == "this" {
				// `=& $this`: ячейка с текущим объектом.
				obj := c.alloc()
				c.chunk.Emit(vm.Instruction{Op: vm.OpThis, A: obj})
				cell := c.alloc()
				c.chunk.Emit(vm.Instruction{Op: vm.OpNewRef, A: cell, B: obj})
				return cell, nil
			}
			if v.Name == "$GLOBALS" || v.Name == "GLOBALS" {
				return 0, c.newFatal("Cannot acquire reference to $GLOBALS")
			}
			return c.refCell(v.Name), nil
		}
	case *IndexExpr:
		if v.Key == nil {
			break
		}
		arr, wb, err := c.indexBase(v, true)
		if err != nil {
			return 0, err
		}
		k, err := c.expr(v.Key)
		if err != nil {
			return 0, err
		}
		cell := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpIndexRef, A: cell, B: arr, C: int32(k)})
		if wb != nil {
			wb(arr)
		}
		return cell, nil
	case *PropExpr:
		obj, err := c.exprForWrite(v.Recv)
		if err != nil {
			return 0, err
		}
		cell := c.alloc()
		if v.NameExpr != nil {
			nameReg, err := c.expr(v.NameExpr)
			if err != nil {
				return 0, err
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpPropRefDyn, A: cell, B: obj, C: int32(nameReg)})
		} else {
			name := int32(c.chunk.AddConst(vm.StrConst(v.Name)))
			c.chunk.Emit(vm.Instruction{Op: vm.OpPropRef, A: cell, B: obj, C: name})
		}
		return cell, nil
	case *StaticPropExpr:
		if v.ClassExpr != nil || c.classRefUnresolved(v.Class) {
			cls, cerr := c.staticClassReg(v.Class, v.ClassExpr)
			if cerr != nil {
				return 0, cerr
			}
			nmr, cerr := c.staticNameReg(v.Name, v.NameExpr)
			if cerr != nil {
				return 0, cerr
			}
			return c.emitCall("__php_dyn_static_ref", []uint8{cls, nmr}), nil
		}
		sel, err := c.classRef(v.Class)
		if err != nil {
			return 0, err
		}
		cell := c.alloc()
		if v.NameExpr != nil {
			nameReg, err := c.expr(v.NameExpr)
			if err != nil {
				return 0, err
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpStaticRefDyn, A: cell, B: sel, C: int32(nameReg)})
		} else {
			name := int32(c.chunk.AddConst(vm.StrConst(v.Name)))
			c.chunk.Emit(vm.Instruction{Op: vm.OpStaticRef, A: cell, B: sel, C: name})
		}
		return cell, nil
	}
	if c.refAssignNoticeNeeded(value) {
		c.emitLine(c.exprLine(value))
		c.emitRefNotice("Only variables should be assigned by reference")
	}
	return c.expr(value)
}

// refAssignNoticeNeeded сообщает, что `=&` с не-lvalue выражением требует
// Notice "Only variables should be assigned by reference": результат вызова,
// new и т.п., если функция не объявлена `function &`.
func (c *compiler) refAssignNoticeNeeded(e Expr) bool {
	switch v := e.(type) {
	case *CallExpr:
		if i, ok := c.idx[v.Name]; ok && c.byRefNonGen(i) {
			return false
		}
		if v.Fallback != "" {
			if i, ok := c.idx[v.Fallback]; ok && c.byRefNonGen(i) {
				return false
			}
		}
		return true
	case *MethodCallExpr, *StaticCallExpr, *NewExpr, *DynCallExpr:
		return true
	}
	return false
}

// bindRefCell делает ячейку переменной name алиасом srcCell (или копией
// значения, если srcCell — не Ref).
// bindRefTarget привязывает цель цикла foreach-by-ref к ячейке srcCell:
// простую переменную — через bindRefCell, свойство/элемент — через
// ссылочное присваивание (что позволяет поймать запрет на запись readonly
// свойства, напр. enum-кейса).
func (c *compiler) bindRefTarget(name string, target Expr, srcCell uint8) error {
	if target == nil {
		c.bindRefCell(name, srcCell)
		return nil
	}
	_, err := c.assign(target, "=", &RegExpr{Reg: srcCell}, true)
	return err
}

func (c *compiler) bindRefCell(name string, srcCell uint8) {
	if c.isMain {
		if c.useGlobal(name) {
			c.chunk.Emit(vm.Instruction{Op: vm.OpGlobalRefBind, A: srcCell, C: int32(c.chunk.AddConst(vm.StrConst(c.globalKey(name))))})
			return
		}
		dst, ok := c.refs[name]
		if !ok {
			dst = c.ensureRefReg(name)
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpRefBind, A: dst, B: srcCell})
		return
	}
	if c.useGlobal(name) {
		// Внутри функции `$x = &$v` отвязывает CV от статической/глобальной
		// таблицы: переменная в этом вызове живёт в локальном ref-регистре,
		// запись таблицы сохраняет прежнее значение (следующий вызов
		// статической функции снова свяжется с таблицей).
		if c.detached == nil {
			c.detached = map[string]bool{}
		}
		c.detached[name] = true
		dst, ok := c.refs[name]
		if !ok || dst == 0xFF {
			dst = c.varReg(name)
			c.refs[name] = dst
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpRefBind, A: dst, B: srcCell})
		return
	}
	dst, ok := c.refs[name]
	if !ok {
		dst = c.ensureRefReg(name)
	}
	c.chunk.Emit(vm.Instruction{Op: vm.OpRefBind, A: dst, B: srcCell})
}

// evalIndexKeys вычисляет ключи цепочки $a[k1][k2]... в порядке Zend —
// слева направо, то есть от самого глубокого уровня к внешнему (Zend:
// INIT_FCALL k1; DO_UCALL; INIT_FCALL k2; ...; FETCH_DIM_W $a k1;
// ASSIGN_DIM k2). Append-уровни ($a[][k]) помечаются 0xFF: у них ключа нет.
func (c *compiler) evalIndexKeys(t *IndexExpr) ([]uint8, error) {
	var keys []uint8
	var walk func(e *IndexExpr) error
	walk = func(e *IndexExpr) error {
		if base, ok := e.Arr.(*IndexExpr); ok {
			if err := walk(base); err != nil {
				return err
			}
		}
		if e.Key == nil {
			keys = append(keys, 0xFF)
			return nil
		}
		k, err := c.expr(e.Key)
		if err != nil {
			return err
		}
		keys = append(keys, k)
		return nil
	}
	if err := walk(t); err != nil {
		return nil, err
	}
	return keys, nil
}

// indexBase вычисляет массив-приёмник для lvalue вида $a[...] / $this->p[...]
// / C::$p[...] и возвращает функцию записи изменённого массива обратно в
// источник (nil, если писать некуда).
func (c *compiler) indexBase(t *IndexExpr, dimW bool) (uint8, func(uint8), error) {
	return c.indexBaseKeys(t, nil, dimW)
}

// exprChainRead читает промежуточное звено цепочки записи
// ($a->b->c[] = ...): без предупреждений об неопределённых переменных и
// свойствах (Zend в write-контексте их не печатает) и без RW-режима: W-фетч
// нужен только контейнеру, в который идёт запись (readonly-семантика:
// variation_nested; объектные цепочки не авто-вивифицируются).
func (c *compiler) exprChainRead(e Expr) (uint8, error) {
	c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
	r, err := c.expr(e)
	c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
	return r, err
}

// indexBaseKeys — как indexBase, но ключи цепочки уже вычислены (keys —
// регистры в порядке вычисления Zend: keys[0] — ключ самого глубокого
// уровня, keys[len-1] — ключ t). Сам ключ t использует вызывающий;
// внутренние уровни берут свои ключи отсюда (keys[len-2] — ключ t.Arr).
// keys == nil — ключи вычисляются на месте (прежнее поведение).
func (c *compiler) indexBaseKeys(t *IndexExpr, keys []uint8, dimW bool) (uint8, func(uint8), error) {
	switch base := t.Arr.(type) {
	case *VarExpr:
		if base.NameExpr != nil {
			arr, err := c.loadVarDyn(base.NameExpr)
			if err != nil {
				return 0, nil, err
			}
			return arr, func(r uint8) { _ = c.storeVarDyn(base.NameExpr, r) }, nil
		}
		// Переменная-ссылка: база ЗАПИСИ читается и пишется напрямую через
		// Ref-ячейку (OpRefGetRaw/OpRefSetRaw) без shareSlot-раундтрипа: иначе
		// RefGet/RefSet помечают массив разделённым, и Unshare в
		// OpAppend/OpIndexSet копирует его целиком на каждой записи — O(n^2).
		// Ячейка здесь либо Ref (alias/by-ref параметр), либо self-регистр
		// ref-переменной; в обоих случаях значение возвращается туда же.
		if c.isRef(base.Name) && base.Name != "$GLOBALS" && base.Name != "GLOBALS" {
			var ref uint8
			if c.useGlobal(base.Name) {
				ref = c.alloc()
				c.chunk.Emit(vm.Instruction{Op: vm.OpGlobalRefAddr, A: ref,
					C: int32(c.chunk.AddConst(vm.StrConst(c.globalKey(base.Name))))})
			} else {
				ref = c.refs[base.Name]
			}
			arr := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpRefGetRaw, A: arr, B: ref})
			return arr, func(r uint8) {
				c.chunk.Emit(vm.Instruction{Op: vm.OpRefSetRaw, A: ref, B: r})
			}, nil
		}
		return c.loadVar(base.Name), func(r uint8) { c.storeVar(base.Name, r) }, nil
	case *PropExpr:
		obj, err := c.exprChainRecv(base.Recv)
		if err != nil {
			return 0, nil, err
		}
		var nameReg uint8
		if base.NameExpr != nil {
			nameReg, err = c.expr(base.NameExpr)
			if err != nil {
				return 0, nil, err
			}
		}
		name := int32(c.chunk.AddConst(vm.StrConst(base.Name)))
		arr := c.alloc()
		if base.NameExpr != nil {
			if dimW {
				c.chunk.Emit(vm.Instruction{Op: vm.OpPropGetRWArrDyn, A: arr, B: obj, C: int32(nameReg)})
			} else {
				c.chunk.Emit(vm.Instruction{Op: vm.OpPropGetRWDyn, A: arr, B: obj, C: int32(nameReg)})
			}
		} else {
			if dimW {
				c.chunk.Emit(vm.Instruction{Op: vm.OpPropGetRWArr, A: arr, B: obj, C: name})
			} else {
				c.chunk.Emit(vm.Instruction{Op: vm.OpPropGetRW, A: arr, B: obj, C: name})
			}
		}
		return arr, func(r uint8) {
			// Свойство могло быть получено через __get: OpPropSetOverload
			// сам решает (Notice без эффекта либо обычная запись).
			if base.NameExpr != nil {
				c.chunk.Emit(vm.Instruction{Op: vm.OpPropSetOverloadDyn, A: obj, B: r, C: int32(nameReg)})
			} else {
				c.chunk.Emit(vm.Instruction{Op: vm.OpPropSetOverload, A: obj, B: r, C: name})
			}
		}, nil
	case *StaticPropExpr:
		if base.ClassExpr != nil || c.classRefUnresolved(base.Class) {
			cls, cerr := c.staticClassReg(base.Class, base.ClassExpr)
			if cerr != nil {
				return 0, nil, cerr
			}
			nmr, cerr := c.staticNameReg(base.Name, base.NameExpr)
			if cerr != nil {
				return 0, nil, cerr
			}
			arr := c.emitCall("__php_dyn_static_get", []uint8{cls, nmr})
			return arr, func(r uint8) {
				c.emitCall("__php_dyn_static_set", []uint8{cls, nmr, r})
			}, nil
		}
		sel, err := c.classRef(base.Class)
		if err != nil {
			return 0, nil, err
		}
		var nameReg uint8
		if base.NameExpr != nil {
			nameReg, err = c.expr(base.NameExpr)
			if err != nil {
				return 0, nil, err
			}
		}
		name := int32(c.chunk.AddConst(vm.StrConst(base.Name)))
		arr := c.alloc()
		if base.NameExpr != nil {
			if dimW {
				c.chunk.Emit(vm.Instruction{Op: vm.OpStaticGetRWDyn, A: arr, B: sel, C: int32(nameReg)})
			} else {
				c.chunk.Emit(vm.Instruction{Op: vm.OpStaticGetDyn, A: arr, B: sel, C: int32(nameReg)})
			}
		} else {
			if dimW {
				c.chunk.Emit(vm.Instruction{Op: vm.OpStaticGetRW, A: arr, B: sel, C: name})
			} else {
				c.chunk.Emit(vm.Instruction{Op: vm.OpStaticGet, A: arr, B: sel, C: name})
			}
		}
		return arr, func(r uint8) {
			if base.NameExpr != nil {
				c.chunk.Emit(vm.Instruction{Op: vm.OpStaticSetDyn, A: r, B: sel, C: int32(nameReg)})
			} else {
				c.chunk.Emit(vm.Instruction{Op: vm.OpStaticSet, A: r, B: sel, C: name})
			}
		}, nil
	case *IndexExpr:
		// Вложенный приёмник: $a[x][y] — база $a[x]; после записи нужно
		// вернуть изменённый вложенный массив в $a[x] (и далее в $a).
		var inner uint8
		var wbInner func(uint8)
		var err error
		if keys != nil {
			// Уровни ниже уже вычислены: keys[:len-1] — ключи базы и глубже.
			inner, wbInner, err = c.indexBaseKeys(base, keys[:len(keys)-1], dimW)
		} else {
			inner, wbInner, err = c.indexBase(base, dimW)
		}
		if err != nil {
			return 0, nil, err
		}
		if base.Key == nil {
			// $a[][y] = ...: приёмник — новый элемент $a[] (append), который
			// создаётся до вычисления значения (Zend: indirect append).
			// Для классов с запретом append (WeakMap) — Error без offsetGet.
			nReg := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpLen, A: nReg, B: inner})
			arr := c.alloc()
			// Чтение контейнера-приёмника без пометки разделённости: ниже он
			// мутируется и возвращается в тот же элемент (wb), второй владелец
			// не появляется.
			c.chunk.Emit(vm.Instruction{Op: vm.OpIndexAppendRaw, A: arr, B: inner, C: int32(nReg)})
			return arr, func(r uint8) {
				// Writeback контейнера: без shareSlot значения (OpIndexSetRaw) —
				// иначе элемент помечался бы разделённым на каждой записи и
				// следующий append копировал бы его целиком (O(n^2)).
				c.chunk.Emit(vm.Instruction{Op: vm.OpIndexSetRaw, A: inner, B: nReg, C: int32(r)})
				if wbInner != nil {
					wbInner(inner)
				}
			}, nil
		}
		var k uint8
		if keys != nil {
			k = keys[len(keys)-2]
		} else {
			k, err = c.expr(base.Key)
			if err != nil {
				return 0, nil, err
			}
		}
		arr := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpIndexRaw, A: arr, B: inner, C: int32(k)})
		return arr, func(r uint8) {
			c.chunk.Emit(vm.Instruction{Op: vm.OpIndexSetOverloadRaw, A: inner, B: k, C: int32(r)})
			if wbInner != nil {
				wbInner(inner)
			}
		}, nil
	}
	a, err := c.expr(t.Arr)
	if err != nil {
		return 0, nil, err
	}
	return a, nil, nil
}

func (c *compiler) assignIndex(t *IndexExpr, op string, value Expr, discardResult bool) (uint8, error) {
	if t.Key == nil && isGlobalsVar(t.Arr) {
		return 0, c.newFatal("Cannot append to $GLOBALS")
	}
	// Запись в элемент временного выражения (`[0, 1][0] = 1`): Zend отвергает
	// на компиляции.
	if isTemporaryExpr(t.Arr) && !c.builtinCallBase(t.Arr) {
		return 0, c.newFatal("Cannot use temporary expression in write context")
	}
	if c.builtinCallBase(t.Arr) {
		return 0, c.writeCtxBuiltinFatal(t)
	}
	// Быстрый путь для глобальных массивов: append/index-set прямо в таблице
	// глобальных переменных — иначе каждый $a[] = ... копирует массив (O(n^2)).
	// Для `=` c используемым результатом идём общим путём: результат
	// присваивания в строковый offset — записанный символ (OpIndexSetRes).
	if v, ok := t.Arr.(*VarExpr); ok && v.NameExpr == nil && c.useGlobal(v.Name) && !c.isRef(v.Name) {
		keyConst := int32(c.chunk.AddConst(vm.StrConst(c.globalKey(v.Name))))
		if t.Key == nil && op == "=" {
			r, err := c.expr(value)
			if err != nil {
				return 0, err
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpGlobalAppend, A: r, C: keyConst})
			return r, nil
		}
		if t.Key != nil && op == "=" && discardResult {
			k, err := c.expr(t.Key)
			if err != nil {
				return 0, err
			}
			r, err := c.expr(value)
			if err != nil {
				return 0, err
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpGlobalIndexSet, A: k, B: r, C: keyConst})
			return r, nil
		}
	}
	// Простая переменная-база (глобальная или из символьной таблицы) и нужен
	// результат присваивания: ключ и значение вычисляются раньше, а база
	// читается в момент записи — как CV в Zend (пишем в текущее значение
	// переменной, а не в копию до правого операнда).
	if !discardResult && op == "=" && t.Key != nil {
		if v, ok := t.Arr.(*VarExpr); ok && v.NameExpr == nil && !c.isRef(v.Name) && (c.useGlobal(v.Name) || c.usesSymtab) {
			k, err := c.expr(t.Key)
			if err != nil {
				return 0, err
			}
			r, err := c.expr(value)
			if err != nil {
				return 0, err
			}
			rc := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: rc, B: r})
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
			arr := c.loadVar(v.Name)
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
			c.chunk.Emit(vm.Instruction{Op: vm.OpIndexSetRes, A: arr, B: k, C: int32(rc)})
			c.storeVar(v.Name, arr)
			return rc, nil
		}
	}
	// Простое присваивание `$a[k1][k2] = v`: Zend вычисляет ключи (внешний
	// первым) и значение ДО выборки базы — запись идёт в массив, который
	// переменная содержит в момент записи (движущееся основание W-цепочки;
	// engine_assignExecutionOrder_002).
	var keys []uint8
	var err error
	if op == "=" {
		keys, err = c.evalIndexKeys(t)
		if err != nil {
			return 0, err
		}
	}
	valReg := uint8(0)
	if op == "=" {
		valReg, err = c.expr(value)
		if err != nil {
			return 0, err
		}
	}
	silentBase := (op == "=" || t.Key == nil) && !callLikeBase(t.Arr)
	if silentBase {
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
	}
	arr, wb, err := c.indexBaseKeys(t, keys, op == "=")
	if silentBase {
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
	}
	if err != nil {
		return 0, err
	}
	if t.Key == nil {
		r := valReg
		if op != "=" {
			r, err = c.expr(value)
			if err != nil {
				return 0, err
			}
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpAppend, A: arr, B: r})
		if wb != nil {
			wb(arr)
		}
		return r, nil
	}
	var k uint8
	if op == "=" {
		k = keys[len(keys)-1]
	} else {
		k, err = c.expr(t.Key)
		if err != nil {
			return 0, err
		}
	}
	if op == "=" {
		r := valReg
		if discardResult {
			c.chunk.Emit(vm.Instruction{Op: vm.OpIndexSet, A: arr, B: k, C: int32(r)})
			if wb != nil {
				wb(arr)
			}
			return r, nil
		}
		// Значение копируется в отдельный регистр: он же получит результат
		// присваивания (для строкового offset — записанный символ), не
		// задевая исходную переменную-значение.
		rc := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: rc, B: r})
		c.chunk.Emit(vm.Instruction{Op: vm.OpIndexSetRes, A: arr, B: k, C: int32(rc)})
		if wb != nil {
			wb(arr)
		}
		return rc, nil
	}
	bc, ok := binOp(strings.TrimSuffix(op, "="))
	if !ok {
		return 0, fmt.Errorf("компилятор: неизвестный оператор %q", op)
	}
	cur := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpIndexRW, A: cur, B: arr, C: int32(k)})
	r, err := c.expr(value)
	if err != nil {
		return 0, err
	}
	d := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: compoundOp(bc), A: d, B: cur, C: int32(r)})
	c.chunk.Emit(vm.Instruction{Op: vm.OpIndexSet, A: arr, B: k, C: int32(d)})
	if wb != nil {
		wb(arr)
	}
	return d, nil
}

// assignIndexRef компилирует `$a[$k] = &$x` / `$a[] = &$x`: элемент массива
// становится той же ссылкой, что и источник.
func (c *compiler) assignIndexRef(t *IndexExpr, value Expr) (uint8, error) {
	if t.Key == nil {
		// Zend выполняет FETCH_DIM_W первым: ошибка занятого авто-ключа
		// имеет приоритет над диагностиками RHS. Но ref-режим цели/ячейки
		// включается на компиляции (refCellForExpr); если базу загрузить
		// раньше этой пометки, она уйдёт по не-ref пути и ссылка не будет
		// писаться насквозь (array_map_variation2, array_splice_variation1).
		// Для «простых ячеек» RHS (переменная; элемент массива от
		// переменной) диагностик нет — безопасен порядок «ячейка, затем
		// база+проверка».
		simpleCell := false
		switch v := value.(type) {
		case *VarExpr:
			simpleCell = v.NameExpr == nil && v.Name != "$this" && v.Name != "this" &&
				v.Name != "$GLOBALS" && v.Name != "GLOBALS"
		case *IndexExpr:
			if v.Key != nil {
				if b, ok := v.Arr.(*VarExpr); ok {
					simpleCell = b.NameExpr == nil && b.Name != "$this" && b.Name != "this" &&
						b.Name != "$GLOBALS" && b.Name != "GLOBALS"
				}
			}
		}
		if simpleCell {
			srcCell, err := c.refCellForExpr(value)
			if err != nil {
				return 0, err
			}
			arr, wb, err := c.indexBase(t, true)
			if err != nil {
				return 0, err
			}
			c.emitCall("__php_append_ref_check", []uint8{arr})
			c.chunk.Emit(vm.Instruction{Op: vm.OpAppend, A: arr, B: srcCell})
			if wb != nil {
				wb(arr)
			}
			c.emitRefSweep()
			return srcCell, nil
		}
		arr, wb, err := c.indexBase(t, true)
		if err != nil {
			return 0, err
		}
		c.emitCall("__php_append_ref_check", []uint8{arr})
		srcCell, err := c.refCellForExpr(value)
		if err != nil {
			return 0, err
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpAppend, A: arr, B: srcCell})
		if wb != nil {
			wb(arr)
		}
		c.emitRefSweep()
		return srcCell, nil
	}
	srcCell, err := c.refCellForExpr(value)
	if err != nil {
		return 0, err
	}
	if base, ok := t.Arr.(*VarExpr); ok && base.NameExpr == nil &&
		(base.Name == "$GLOBALS" || base.Name == "GLOBALS") && t.Key != nil {
		k, err := c.expr(t.Key)
		if err != nil {
			return 0, err
		}
		c.emitCall("__php_globals_set_ref", []uint8{k, srcCell})
		c.emitRefSweep()
		return srcCell, nil
	}
	arr, wb, err := c.indexBase(t, true)
	if err != nil {
		return 0, err
	}
	k, err := c.expr(t.Key)
	if err != nil {
		return 0, err
	}
	c.emitCall("__php_index_ref_check", []uint8{arr})
	c.chunk.Emit(vm.Instruction{Op: vm.OpIndexSet, A: arr, B: k, C: int32(srcCell)})
	if wb != nil {
		wb(arr)
	}
	c.emitRefSweep()
	return srcCell, nil
}

// decoupleCallRef реализует поведение Zend при `$ref =& call()` для
// *свойства*: результат функции по ссылке не алиасит, а копируется в новую
// ячейку-ссылку (в отличие от присваивания переменной).
func (c *compiler) decoupleCallRef(value Expr, srcCell uint8) uint8 {
	return srcCell
}

// assignPropRef компилирует `$o->p = &$x` / `$o->$n = &$x`.
func (c *compiler) assignPropRef(t *PropExpr, value Expr) (uint8, error) {
	obj, err := c.exprForWrite(t.Recv)
	if err != nil {
		return 0, err
	}
	srcCell, err := c.refCellForExpr(value)
	if err != nil {
		return 0, err
	}
	srcCell = c.decoupleCallRef(value, srcCell)
	if t.NameExpr != nil {
		nameReg, err := c.expr(t.NameExpr)
		if err != nil {
			return 0, err
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpPropSetRefDyn, A: obj, B: srcCell, C: int32(nameReg)})
	} else {
		name := int32(c.chunk.AddConst(vm.StrConst(t.Name)))
		c.chunk.Emit(vm.Instruction{Op: vm.OpPropSetRef, A: obj, B: srcCell, C: name})
	}
	c.emitRefSweep()
	return srcCell, nil
}

// assignStaticPropRef компилирует `C::$p =& value`.
func (c *compiler) assignStaticPropRef(t *StaticPropExpr, value Expr) (uint8, error) {
	if t.ClassExpr != nil {
		cls, err := c.staticClassReg(t.Class, t.ClassExpr)
		if err != nil {
			return 0, err
		}
		name, err := c.staticNameReg(t.Name, t.NameExpr)
		if err != nil {
			return 0, err
		}
		cell := c.emitCall("__php_dyn_static_ref", []uint8{cls, name})
		srcCell, err := c.refCellForExpr(value)
		if err != nil {
			return 0, err
		}
		srcCell = c.decoupleCallRef(value, srcCell)
		c.chunk.Emit(vm.Instruction{Op: vm.OpRefSet, A: cell, B: srcCell})
		c.emitRefSweep()
		return srcCell, nil
	}
	sel, err := c.classRef(t.Class)
	if err != nil {
		cls, cerr := c.staticClassReg(t.Class, nil)
		if cerr != nil {
			return 0, cerr
		}
		name, cerr := c.staticNameReg(t.Name, t.NameExpr)
		if cerr != nil {
			return 0, cerr
		}
		cell := c.emitCall("__php_dyn_static_ref", []uint8{cls, name})
		srcCell, cerr := c.refCellForExpr(value)
		if cerr != nil {
			return 0, cerr
		}
		srcCell = c.decoupleCallRef(value, srcCell)
		c.chunk.Emit(vm.Instruction{Op: vm.OpRefSet, A: cell, B: srcCell})
		c.emitRefSweep()
		return srcCell, nil
	}
	srcCell, err := c.refCellForExpr(value)
	if err != nil {
		return 0, err
	}
	srcCell = c.decoupleCallRef(value, srcCell)
	if t.NameExpr != nil {
		nameReg, err := c.expr(t.NameExpr)
		if err != nil {
			return 0, err
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpStaticSetRefDyn, A: srcCell, B: sel, C: int32(nameReg)})
	} else {
		name := int32(c.chunk.AddConst(vm.StrConst(t.Name)))
		c.chunk.Emit(vm.Instruction{Op: vm.OpStaticSetRef, A: srcCell, B: sel, C: name})
	}
	c.emitRefSweep()
	return srcCell, nil
}

// incDecStmt компилирует `$x++;`/`$x--;` как самостоятельный оператор:
// значение не используется, инкремент делается прямо в регистре переменной.
func (c *compiler) incDecStmt(x *IncDecExpr) error {
	if containsNullsafe(x.X) {
		return c.newFatal("Can't use nullsafe operator in write context")
	}
	if t, ok := x.X.(*VarExpr); ok && t.NameExpr == nil && !c.isRef(t.Name) && !c.useGlobal(t.Name) {
		c.emitIncDecReg(x.Op, c.varReg(t.Name))
		return nil
	}
	_, err := c.expr(x)
	return err
}

// incDecFlagReg выделяет регистр с флагом декремента (true = "--").
func (c *compiler) incDecFlagReg(op string) uint8 {
	r := c.alloc()
	if op == "++" {
		c.chunk.Emit(vm.Instruction{Op: vm.OpFalse, A: r})
	} else {
		c.chunk.Emit(vm.Instruction{Op: vm.OpTrue, A: r})
	}
	return r
}

// emitIncDecReg эмитит ++/-- над регистром in-place (семантика PHP для чисел и
// строк). Инлайн-опкод, без нативного вызова.
func (c *compiler) emitIncDecReg(op string, reg uint8) {
	c.chunk.Emit(vm.Instruction{Op: vm.OpIncDec, A: reg, B: reg, C: incDecFlag(op)})
}

// emitIncDecDst эмитит ++/-- в отдельный регистр dst из src (src не меняется).
func (c *compiler) emitIncDecDst(op string, dst, src uint8) {
	c.chunk.Emit(vm.Instruction{Op: vm.OpIncDec, A: dst, B: src, C: incDecFlag(op)})
}

// incDecFlag возвращает C-флаг для OpIncDec: 0 — "++", 1 — "--".
func incDecFlag(op string) int32 {
	if op == "--" {
		return 1
	}
	return 0
}

func (c *compiler) incDec(x *IncDecExpr) (uint8, error) {
	if containsNullsafe(x.X) {
		return 0, c.newFatal("Can't use nullsafe operator in write context")
	}
	switch t := x.X.(type) {
	case *VarExpr:
		// Инкремент локальной/промотированной переменной прямо в её регистре.
		if t.NameExpr == nil && !c.isRef(t.Name) && !c.useGlobal(t.Name) {
			v := c.varReg(t.Name)
			// ++/-- читает переменную: при флаге «не определена» Zend печатает
			// «Undefined variable» (null -> 1), затем переменная определена.
			c.emitUndefCheck(t.Name, c.exprLine(x))
			if x.Pre {
				c.emitIncDecReg(x.Op, v)
				return v, nil
			}
			old := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: old, B: v})
			c.emitIncDecReg(x.Op, v)
			return old, nil
		}
		// Переменная-ссылка (возможно, guarded типизированным свойством):
		// ++/-- должна уважать guard переполнения.
		if t.NameExpr == nil && c.isRef(t.Name) && !c.useGlobal(t.Name) {
			refReg := c.refs[t.Name]
			var old uint8
			if !x.Pre {
				old = c.alloc()
				c.chunk.Emit(vm.Instruction{Op: vm.OpRefGet, A: old, B: refReg})
			}
			dec := c.incDecFlagReg(x.Op)
			newv := c.emitCall("__php_incdec_ref", []uint8{refReg, dec})
			if x.Pre {
				return newv, nil
			}
			return old, nil
		}
		// Переменная-ссылка в глобальной таблице (напр. `$_ = &$obj->p`).
		if t.NameExpr == nil && c.isRef(t.Name) && c.useGlobal(t.Name) {
			key := c.globalKey(t.Name)
			keyConst := int32(c.chunk.AddConst(vm.StrConst(key)))
			keyReg := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: keyReg, C: keyConst})
			var old uint8
			if !x.Pre {
				old = c.alloc()
				c.chunk.Emit(vm.Instruction{Op: vm.OpGlobalRefGet, A: old, C: keyConst})
			}
			dec := c.incDecFlagReg(x.Op)
			newv := c.emitCall("__php_incdec_global", []uint8{keyReg, dec})
			if x.Pre {
				return newv, nil
			}
			return old, nil
		}
		var cur uint8
		var err error
		if t.NameExpr != nil {
			cur, err = c.loadVarDyn(t.NameExpr)
			if err != nil {
				return 0, err
			}
		} else {
			cur = c.loadVarChecked(t.Name, c.exprLine(t))
		}
		d := c.alloc()
		c.emitIncDecDst(x.Op, d, cur)
		var old uint8
		if !x.Pre {
			// cur может совпадать с регистром переменной: сохраняем старое
			// значение до записи нового.
			old = c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: old, B: cur})
		}
		if t.NameExpr != nil {
			if err := c.storeVarDyn(t.NameExpr, d); err != nil {
				return 0, err
			}
		} else {
			c.storeVar(t.Name, d)
		}
		if x.Pre {
			return d, nil
		}
		return old, nil
	case *IndexExpr:
		arr, wb, err := c.indexBase(t, false)
		if err != nil {
			return 0, err
		}
		if t.Key == nil {
			// `$a[]++` / `$a[]--`: Zend добавляет null-элемент (ошибки
			// переполнения ключа/строковой базы — здесь же), затем
			// модифицирует его. Значение — старое (null) или новое.
			n := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: n})
			c.chunk.Emit(vm.Instruction{Op: vm.OpAppend, A: arr, B: n})
			if wb != nil {
				wb(arr)
			}
			d := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: d})
			if x.Pre {
				c.emitIncDecReg(x.Op, d)
			}
			return d, nil
		}
		k, err := c.expr(t.Key)
		if err != nil {
			return 0, err
		}
		cur := c.alloc()
		// RAW-чтение: inc/dec обязан видеть элемент-ссылку именно ссылкой,
		// иначе теряются границы типизированных свойств (typed_properties_064).
		c.chunk.Emit(vm.Instruction{Op: vm.OpIndexRaw, A: cur, B: arr, C: int32(k)})
		var old uint8
		if !x.Pre {
			old = c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpRefGet, A: old, B: cur})
		}
		dec := c.incDecFlagReg(x.Op)
		newv := c.emitCall("__php_incdec_ref", []uint8{cur, dec})
		c.chunk.Emit(vm.Instruction{Op: vm.OpIndexSetOverload, A: arr, B: k, C: int32(newv)})
		if wb != nil {
			wb(arr)
		}
		if x.Pre {
			return newv, nil
		}
		return old, nil
	case *PropExpr:
		obj, err := c.expr(t.Recv)
		if err != nil {
			return 0, err
		}
		var nameReg uint8
		if t.NameExpr != nil {
			nameReg, err = c.expr(t.NameExpr)
			if err != nil {
				return 0, err
			}
		}
		// ++/-- над типизированным свойством имеет особую семантику
		// переполнения int (Zend: "past its maximal/minimal value"), поэтому
		// выносим её в рантайм-помощник __php_incdec_prop.
		if t.NameExpr == nil {
			nameReg = c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: nameReg,
				C: int32(c.chunk.AddConst(vm.StrConst(t.Name)))})
		}
		cur := c.alloc()
		if t.NameExpr != nil {
			c.chunk.Emit(vm.Instruction{Op: vm.OpPropGetDyn, A: cur, B: obj, C: int32(nameReg)})
		} else {
			c.chunk.Emit(vm.Instruction{Op: vm.OpPropGet, A: cur, B: obj,
				C: int32(c.chunk.AddConst(vm.StrConst(t.Name)))})
		}
		if x.Pre {
			dec := c.incDecFlagReg(x.Op)
			newv := c.emitCall("__php_incdec_prop", []uint8{obj, nameReg, cur, dec})
			return newv, nil
		}
		oldv := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpRefGet, A: oldv, B: cur})
		dec := c.incDecFlagReg(x.Op)
		c.emitCall("__php_incdec_prop", []uint8{obj, nameReg, cur, dec})
		return oldv, nil
	case *StaticPropExpr:
		if t.ClassExpr != nil || c.classRefUnresolved(t.Class) {
			cls, cerr := c.staticClassReg(t.Class, t.ClassExpr)
			if cerr != nil {
				return 0, cerr
			}
			nmr, cerr := c.staticNameReg(t.Name, t.NameExpr)
			if cerr != nil {
				return 0, cerr
			}
			cur := c.emitCall("__php_dyn_static_get", []uint8{cls, nmr})
			d := c.alloc()
			c.emitIncDecDst(x.Op, d, cur)
			c.emitCall("__php_dyn_static_set", []uint8{cls, nmr, d})
			if x.Pre {
				return d, nil
			}
			old := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: old, B: cur})
			return old, nil
		}
		sel, err := c.classRef(t.Class)
		if err != nil {
			return 0, err
		}
		var nameReg uint8
		if t.NameExpr != nil {
			nameReg, err = c.expr(t.NameExpr)
			if err != nil {
				return 0, err
			}
		}
		name := int32(c.chunk.AddConst(vm.StrConst(t.Name)))
		cur := c.alloc()
		if t.NameExpr != nil {
			c.chunk.Emit(vm.Instruction{Op: vm.OpStaticGetDyn, A: cur, B: sel, C: int32(nameReg)})
		} else {
			c.chunk.Emit(vm.Instruction{Op: vm.OpStaticGet, A: cur, B: sel, C: name})
		}
		d := c.alloc()
		c.emitIncDecDst(x.Op, d, cur)
		if t.NameExpr != nil {
			c.chunk.Emit(vm.Instruction{Op: vm.OpStaticSetDyn, A: d, B: sel, C: int32(nameReg)})
		} else {
			c.chunk.Emit(vm.Instruction{Op: vm.OpStaticSet, A: d, B: sel, C: name})
		}
		if x.Pre {
			return d, nil
		}
		return cur, nil
	case *CallExpr, *MethodCallExpr, *StaticCallExpr, *NewExpr, *NewAnonClassExpr:
		return 0, &fatalError{msg: "Can't use function return value in write context", line: c.curLine}
	}
	return 0, &fatalError{msg: "Cannot use temporary expression in write context", line: c.curLine}
}

// collectGlobals собирает имена из global-деклараций (рекурсивно).
func collectGlobals(list []Stmt) []string {
	var out []string
	for _, st := range list {
		switch s := st.(type) {
		case *GlobalStmt:
			out = append(out, s.Names...)
		case *BlockStmt:
			out = append(out, collectGlobals(s.Body)...)
		case *IfStmt:
			out = append(out, collectGlobals(s.Then)...)
			out = append(out, collectGlobals(s.Else)...)
		case *WhileStmt:
			out = append(out, collectGlobals(s.Body)...)
		case *DoWhileStmt:
			out = append(out, collectGlobals(s.Body)...)
		case *ForStmt:
			out = append(out, collectGlobals(s.Body)...)
		case *ForeachStmt:
			out = append(out, collectGlobals(s.Body)...)
		}
	}
	return out
}

type staticVar struct {
	Name    string
	Default Expr
	// Conditional — объявление `static` внутри условного блока (if/switch/
	// цикл/try). Zend связывает такую переменную со статическим слотом
	// лениво (BIND_STATIC), поэтому до выполнения объявления это обычная
	// локальная переменная, а не глобальная.
	Conditional bool
}

// mainStaticStmt компилирует static-декларацию на верхнем уровне: инициализация
// по OpGlobalIsset (один раз) и фатальная ошибка при повторном объявлении.
func (c *compiler) mainStaticStmt(s *StaticVarStmt) error {
	if c.mainStaticSeen == nil {
		c.mainStaticSeen = map[string]bool{}
	}
	for i, n := range s.Names {
		var def Expr
		if i < len(s.Default) {
			def = s.Default[i]
		}
		key := c.staticKeys[n]
		if c.mainStaticSeen[n] {
			c.chunk.Emit(vm.Instruction{Op: vm.OpFatal,
				C: int32(c.chunk.AddConst(vm.StrConst("Duplicate declaration of static variable " + n)))})
			return nil
		}
		c.mainStaticSeen[n] = true
		iss := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpGlobalIsset, A: iss, C: int32(c.chunk.AddConst(vm.StrConst(key)))})
		jf := c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfTrue, A: iss})
		if def != nil {
			r, err := c.expr(def)
			if err != nil {
				return err
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpGlobalSet, A: r, C: int32(c.chunk.AddConst(vm.StrConst(key)))})
		} else {
			nn := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: nn})
			c.chunk.Emit(vm.Instruction{Op: vm.OpGlobalSet, A: nn, C: int32(c.chunk.AddConst(vm.StrConst(key)))})
		}
		c.setJump(jf, c.chunk.Len())
	}
	return nil
}

// collectFuncDecls собирает все объявления функций, включая вложенные
// (внутри функций и блоков). Вложенные функции в PHP — глобальные, поэтому
// регистрируются заранее (hoisting).
func collectFuncDecls(list []Stmt) []*FuncDecl {
	var out []*FuncDecl
	for _, st := range list {
		switch s := st.(type) {
		case *FuncDecl:
			out = append(out, s)
			out = append(out, collectFuncDecls(s.Body)...)
		case *ClassDecl:
			for _, m := range s.Members {
				if len(m.Body) > 0 {
					out = append(out, collectFuncDecls(m.Body)...)
				}
			}
		case *BlockStmt:
			out = append(out, collectFuncDecls(s.Body)...)
		case *IfStmt:
			out = append(out, collectFuncDecls(s.Then)...)
			out = append(out, collectFuncDecls(s.Else)...)
		case *WhileStmt:
			out = append(out, collectFuncDecls(s.Body)...)
		case *DoWhileStmt:
			out = append(out, collectFuncDecls(s.Body)...)
		case *ForStmt:
			out = append(out, collectFuncDecls(s.Body)...)
		case *ForeachStmt:
			out = append(out, collectFuncDecls(s.Body)...)
		case *TryStmt:
			out = append(out, collectFuncDecls(s.Body)...)
			for _, cc := range s.Catches {
				out = append(out, collectFuncDecls(cc.Body)...)
			}
			out = append(out, collectFuncDecls(s.Finally)...)
		case *SwitchStmt:
			for _, cs := range s.Cases {
				out = append(out, collectFuncDecls(cs.Body)...)
			}
		}
	}
	return out
}

// collectStatics собирает static-декларации переменных (рекурсивно).
// Декларации, встреченные внутри условных блоков, помечаются Conditional.
func collectStatics(list []Stmt) []staticVar {
	return collectStaticsIn(list, false)
}

func collectStaticsIn(list []Stmt, cond bool) []staticVar {
	var out []staticVar
	emit := func(n string, def Expr) {
		out = append(out, staticVar{Name: n, Default: def, Conditional: cond})
	}
	for _, st := range list {
		switch s := st.(type) {
		case *StaticVarStmt:
			for i, n := range s.Names {
				var def Expr
				if i < len(s.Default) {
					def = s.Default[i]
				}
				emit(n, def)
			}
		case *BlockStmt:
			out = append(out, collectStaticsIn(s.Body, cond)...)
		case *IfStmt:
			out = append(out, collectStaticsIn(s.Then, true)...)
			out = append(out, collectStaticsIn(s.Else, true)...)
		case *WhileStmt:
			out = append(out, collectStaticsIn(s.Body, true)...)
		case *DoWhileStmt:
			out = append(out, collectStaticsIn(s.Body, true)...)
		case *ForStmt:
			out = append(out, collectStaticsIn(s.Body, true)...)
		case *ForeachStmt:
			out = append(out, collectStaticsIn(s.Body, true)...)
		case *SwitchStmt:
			for _, cs := range s.Cases {
				out = append(out, collectStaticsIn(cs.Body, true)...)
			}
		case *TryStmt:
			out = append(out, collectStaticsIn(s.Body, true)...)
			for _, cc := range s.Catches {
				out = append(out, collectStaticsIn(cc.Body, true)...)
			}
			out = append(out, collectStaticsIn(s.Finally, true)...)
		}
	}
	return out
}

func (c *compiler) binExpr(x *BinExpr) (uint8, error) {
	d := c.alloc()
	return c.binExprTo(x, d)
}

// binExprTo компилирует бинарное выражение, кладя результат в регистр dst.
// Специализирует generic-опкоды в Op*Int и сворачивает правый числовой
// литерал в K-форму.
func (c *compiler) binExprTo(x *BinExpr, dst uint8) (uint8, error) {
	op, ok := binOp(x.Op)
	if !ok {
		return 0, fmt.Errorf("компилятор: неизвестный оператор %q", x.Op)
	}
	if op == vm.OpConcat {
		c.emitLine(c.exprLine(x))
		if r, done, cerr := c.tryConcatN(x, dst); cerr != nil {
			return 0, cerr
		} else if done {
			return r, nil
		}
	}
	// K-форма: правый операнд — числовой литерал.
	if k, ok := numConst(x.R); ok {
		if kop, ok := kFormOp(op, k); ok {
			// Лево-вложенные цепочки ($a + 1 + 1 + ...) аккумулируют в одном
			// регистре dst — иначе длинные цепочки переполняют uint8-нумерацию
			// регистров (256). Для прочих левых операндов (переменная, вызов)
			// сохраняем прежний путь без лишнего MOVE.
			var l uint8
			var err error
			if _, isBin := x.L.(*BinExpr); isBin || foldableShape(x.L) {
				l, err = c.exprInto(x.L, dst)
			} else {
				l, err = c.expr(x.L)
			}
			if err != nil {
				return 0, err
			}
			// Immediate-форма: целочисленный литерал, влезающий в int32,
			// кодируется прямо в C — без обращения к пулу констант.
			if il, ok := x.R.(*IntLit); ok && il.V >= math.MinInt32 && il.V <= math.MaxInt32 {
				c.chunk.Emit(vm.Instruction{Op: immFormOp(kop), A: dst, B: l, C: int32(il.V)})
				return dst, nil
			}
			c.chunk.Emit(vm.Instruction{Op: kop, A: dst, B: l, C: int32(c.chunk.AddConst(k))})
			return dst, nil
		}
	}
	// Отложенное чтение левого операнда-глобальной переменной: при побочных
	// эффектах в правом операнде Zend читает CV после него (см. deferLeftGlobal).
	if c.deferLeftGlobal(x.L, x.R) {
		r, err := c.binOperand(x.R)
		if err != nil {
			return 0, err
		}
		l, err := c.binOperand(x.L)
		if err != nil {
			return 0, err
		}
		if s := specOp(op); s != op {
			op = s
		}
		c.chunk.Emit(vm.Instruction{Op: op, A: dst, B: l, C: int32(r)})
		return dst, nil
	}
	l, err := c.binOperand(x.L)
	if err != nil {
		return 0, err
	}
	r, err := c.binOperand(x.R)
	if err != nil {
		return 0, err
	}
	if s := specOp(op); s != op {
		op = s
	}
	c.chunk.Emit(vm.Instruction{Op: op, A: dst, B: l, C: int32(r)})
	return dst, nil
}

// binOperand компилирует операнд бинарной операции как значение: чтение
// свойства снимается в момент чтения (Zend FETCH_OBJ_R разыменовывает
// IS_REFERENCE), поэтому запись через ту же ссылку позже не меняет уже
// прочитанный операнд (bug72543_3/5). Для элементов массива снимок делает
// сам OpIndex.
func (c *compiler) binOperand(e Expr) (uint8, error) {
	r, err := c.expr(e)
	if err != nil {
		return 0, err
	}
	if _, ok := e.(*PropExpr); ok {
		d := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpRefGet, A: d, B: r})
		return d, nil
	}
	return r, nil
}

// exprWithSideEffects сообщает, что вычисление выражения может иметь
// наблюдаемые эффекты (вызовы, присваивания, инкременты, __toString и т.п.).
// Используется, чтобы отложить чтение левого операнда-переменной, как это
// делает Zend (значение CV читается в момент выполнения операции).
func exprWithSideEffects(e Expr) bool {
	switch x := e.(type) {
	case *StrLit, *IntLit, *FloatLit, *BoolLit, *NullLit, *ConstExpr, *MagicExpr, *ClassKeywordExpr:
		return false
	case *VarExpr:
		return false
	case *BinExpr:
		return exprWithSideEffects(x.L) || exprWithSideEffects(x.R)
	case *UnaryExpr:
		return exprWithSideEffects(x.X)
	case *CastExpr:
		return exprWithSideEffects(x.X)
	case *IndexExpr:
		if x.Key == nil {
			return true
		}
		return exprWithSideEffects(x.Arr) || exprWithSideEffects(x.Key)
	case *PropExpr:
		// Чтение свойства может вызвать __get/__isset.
		return true
	case *StaticPropExpr:
		return true
	case *TernaryExpr:
		return exprWithSideEffects(x.Cond) || (x.Then != nil && exprWithSideEffects(x.Then)) || exprWithSideEffects(x.Else)
	case *CoalesceExpr:
		return exprWithSideEffects(x.L) || exprWithSideEffects(x.R)
	case *LogicalExpr:
		return exprWithSideEffects(x.L) || exprWithSideEffects(x.R)
	}
	return true
}

// deferLeftGlobal сообщает, что левый операнд — глобальная переменная, а
// правый имеет побочные эффекты: Zend прочитает переменную в момент
// выполнения операции, т.е. после правого операнда.
func (c *compiler) deferLeftGlobal(l, r Expr) bool {
	v, ok := l.(*VarExpr)
	if !ok || v.NameExpr != nil || v.Name == "$this" || v.Name == "this" {
		return false
	}
	if c.isRef(v.Name) || !c.useGlobal(v.Name) {
		return false
	}
	return exprWithSideEffects(r)
}

// concatChainParts разворачивает дерево конкатенаций в плоский список частей
// в порядке вычисления (слева направо). Список живёт только на время
// компиляции выражения (в байткоде не хранится).
func concatChainParts(e Expr) []Expr {
	var out []Expr
	var walk func(Expr)
	walk = func(e Expr) {
		if b, ok := e.(*BinExpr); ok {
			if op, ok := binOp(b.Op); ok && op == vm.OpConcat {
				walk(b.L)
				walk(b.R)
				return
			}
		}
		out = append(out, e)
	}
	walk(e)
	return out
}

// concatPartInert сообщает, что вычисление части цепочки не имеет наблюдаемых
// эффектов и не может бросить исключение: это литерал либо чтение простой
// локальной переменной (промотированный main / параметр / записанная
// переменная без unset/global/ref). Для таких частей OpConcatN может отложить
// приведение к строке: все наблюдаемые эффекты (предупреждение о массиве,
// __toString, ошибка) исходят от приведения и происходят слева направо — так
// же, как у вложенных OpConcat.
func (c *compiler) concatPartInert(e Expr) bool {
	switch x := e.(type) {
	case *StrLit, *IntLit, *FloatLit, *BoolLit, *NullLit:
		return true
	case *VarExpr:
		if x.NameExpr != nil || x.Name == "$this" || x.Name == "this" {
			return false
		}
		if c.isRef(x.Name) || c.useGlobal(x.Name) || c.usesSymtab {
			return false
		}
		r, ok := c.vars[x.Name]
		if !ok {
			return false
		}
		if _, undef := c.defRegs[x.Name]; undef {
			return false
		}
		return c.writes[x.Name] || int(r) < int(c.nvars0)
	}
	return false
}

// tryConcatN компилирует цепочку конкатенации (>= 3 частей) одной инструкцией
// OpConcatN: части вычисляются в подряд идущие регистры, суммарная длина
// считается в VM, данные копируются в один []byte.
// Возвращает (dst, true, nil) при успехе, (0, false, nil) — если оптимизация
// неприменима (цепочка короче, части не инертны или строки частей разные).
func (c *compiler) tryConcatN(x *BinExpr, dst uint8) (uint8, bool, error) {
	parts := concatChainParts(x)
	n := len(parts)
	if n < 3 || n > 255 {
		return 0, false, nil
	}
	line := c.exprLine(x)
	for _, p := range parts {
		if c.exprLine(p) != line || !c.concatPartInert(p) {
			return 0, false, nil
		}
	}
	base := c.alloc()
	if base == dst {
		base = c.alloc()
	}
	for i := 1; i < n; i++ {
		c.alloc()
	}
	if int(base)+n > 256 {
		return 0, false, nil
	}
	for i, p := range parts {
		if _, err := c.exprInto(p, base+uint8(i)); err != nil {
			return 0, false, err
		}
	}
	c.emitLine(line)
	c.chunk.Emit(vm.Instruction{Op: vm.OpConcatN, A: dst, B: base, C: int32(n)})
	return dst, true, nil
}

// exprInto компилирует выражение с записью результата в dst (когда это просто).
func (c *compiler) exprInto(e Expr, dst uint8) (uint8, error) {
	if foldableShape(e) {
		if v, ok := foldConstExpr(e); ok {
			c.emitCValInto(v, dst)
			return dst, nil
		}
	}
	if b, ok := e.(*BinExpr); ok {
		return c.binExprTo(b, dst)
	}
	// Литералы пишем прямо в dst, без промежуточного регистра/MOVE.
	switch x := e.(type) {
	case *IntLit:
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: dst, C: int32(c.chunk.AddConst(vm.IntConst(x.V)))})
		return dst, nil
	case *FloatLit:
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: dst, C: int32(c.chunk.AddConst(vm.FloatConst(x.V)))})
		return dst, nil
	case *StrLit:
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: dst, C: int32(c.chunk.AddConst(vm.StrConst(x.V)))})
		return dst, nil
	case *BoolLit:
		c.chunk.Emit(vm.Instruction{Op: vm.OpLoadK, A: dst, C: int32(c.chunk.AddConst(vm.BoolConst(x.V)))})
		return dst, nil
	case *NullLit:
		c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: dst})
		return dst, nil
	}
	r, err := c.expr(e)
	if err != nil {
		return 0, err
	}
	if r != dst {
		c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: dst, B: r})
	}
	return dst, nil
}

// numConst возвращает числовую константу для K-формы, если правый операнд —
// целый или вещественный литерал.
func numConst(e Expr) (vm.Const, bool) {
	switch x := e.(type) {
	case *IntLit:
		return vm.IntConst(x.V), true
	case *FloatLit:
		return vm.FloatConst(x.V), true
	}
	return vm.Const{}, false
}

// specOp отображает generic-операцию в специализированный опкод (быстрый
// int/float-путь с fallback на полную семантику).
func specOp(op vm.Opcode) vm.Opcode {
	switch op {
	case vm.OpAdd:
		return vm.OpAddInt
	case vm.OpSub:
		return vm.OpSubInt
	case vm.OpMul:
		return vm.OpMulInt
	case vm.OpDiv:
		return vm.OpDivInt
	case vm.OpLess:
		return vm.OpLessInt
	case vm.OpLessOrEqual:
		return vm.OpLessOrEqualInt
	case vm.OpGreater:
		return vm.OpGreaterInt
	case vm.OpGreaterEqual:
		return vm.OpGreaterEqualInt
	case vm.OpEqual:
		return vm.OpEqualInt
	}
	return op
}

// kFormOp возвращает K-форму операции для числовой константы k.
//
// Используются только целочисленные K-формы (они при несовпадении типов
// откатываются к полной семантике) и OpAddFloatK для `+` (семантика совпадает
// с generic add: numeric conversion). Сравнения с float-константой НЕ
// сворачиваются — float-семантика OpLessFloatK отличается от PHP-сравнения
// строки с числом.
func kFormOp(op vm.Opcode, k vm.Const) (vm.Opcode, bool) {
	isInt := k.Type() == vm.IntType
	switch op {
	case vm.OpAdd:
		if isInt {
			return vm.OpAddIntK, true
		}
		return vm.OpAddFloatK, true
	case vm.OpSub:
		if isInt {
			return vm.OpSubIntK, true
		}
		return 0, false
	case vm.OpMul:
		if isInt {
			return vm.OpMulIntK, true
		}
		return 0, false
	case vm.OpLess:
		if isInt {
			return vm.OpLessIntK, true
		}
		return 0, false
	case vm.OpLessOrEqual:
		if isInt {
			return vm.OpLessOrEqualIntK, true
		}
		return 0, false
	case vm.OpGreater:
		if isInt {
			return vm.OpGreaterIntK, true
		}
		return 0, false
	case vm.OpGreaterEqual:
		if isInt {
			return vm.OpGreaterEqualIntK, true
		}
		return 0, false
	case vm.OpEqual:
		if isInt {
			return vm.OpEqualIntK, true
		}
		return 0, false
	}
	return 0, false
}

// immFormOp отображает IntK-форму в Immediate-форму (C = значение int32,
// без пула констант).
func immFormOp(kop vm.Opcode) vm.Opcode {
	switch kop {
	case vm.OpAddIntK:
		return vm.OpAddIntImm
	case vm.OpSubIntK:
		return vm.OpSubIntImm
	case vm.OpMulIntK:
		return vm.OpMulIntImm
	case vm.OpLessIntK:
		return vm.OpLessIntImm
	case vm.OpLessOrEqualIntK:
		return vm.OpLessOrEqualIntImm
	case vm.OpGreaterIntK:
		return vm.OpGreaterIntImm
	case vm.OpGreaterEqualIntK:
		return vm.OpGreaterEqualIntImm
	case vm.OpEqualIntK:
		return vm.OpEqualIntImm
	}
	return 0
}

func (c *compiler) logical(x *LogicalExpr) (uint8, error) {
	if x.Op == "xor" {
		l, err := c.expr(x.L)
		if err != nil {
			return 0, err
		}
		r, err := c.expr(x.R)
		if err != nil {
			return 0, err
		}
		nl := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpNot, A: nl, B: l})
		nr := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpNot, A: nr, B: r})
		d := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpNotEqual, A: d, B: nl, C: int32(nr)})
		return d, nil
	}
	isAnd := x.Op == "&&" || x.Op == "and"
	// Короткое замыкание по константному левому операнду: правая часть не
	// вычисляется (и не компилируется), как в PHP.
	if lv, ok := foldConstExpr(x.L); ok && !containsYieldExpr(x.R) {
		lb := foldTruthy(lv)
		if isAnd && !lb {
			return c.emitCVal(cval{k: ckBool, b: false}), nil
		}
		if !isAnd && lb {
			return c.emitCVal(cval{k: ckBool, b: true}), nil
		}
	}
	l, err := c.expr(x.L)
	if err != nil {
		return 0, err
	}
	if isAnd {
		jf := c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfFalse, A: l})
		r, err := c.expr(x.R)
		if err != nil {
			return 0, err
		}
		n1 := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpNot, A: n1, B: r})
		d := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpNot, A: d, B: n1})
		jend := c.chunk.Emit(vm.Instruction{Op: vm.OpJump})
		c.setJump(jf, c.chunk.Len())
		c.chunk.Emit(vm.Instruction{Op: vm.OpFalse, A: d})
		c.setJump(jend, c.chunk.Len())
		return d, nil
	}
	jt := c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfTrue, A: l})
	r, err := c.expr(x.R)
	if err != nil {
		return 0, err
	}
	n1 := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpNot, A: n1, B: r})
	d := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpNot, A: d, B: n1})
	jend := c.chunk.Emit(vm.Instruction{Op: vm.OpJump})
	c.setJump(jt, c.chunk.Len())
	c.chunk.Emit(vm.Instruction{Op: vm.OpTrue, A: d})
	c.setJump(jend, c.chunk.Len())
	return d, nil
}

func (c *compiler) ternary(x *TernaryExpr) (uint8, error) {
	// Тернарник с константным условием: компилируем только выбранную ветвь
	// (PHP не выполняет вторую). Ветвь с `yield` не выбрасываем — иначе
	// функция перестала бы считаться генератором.
	if cv, ok := foldConstExpr(x.Cond); ok {
		if foldTruthy(cv) && !containsYieldExpr(x.Else) {
			if x.Then == nil {
				return c.emitCVal(cv), nil
			}
			return c.expr(x.Then)
		}
		if !foldTruthy(cv) && (x.Then == nil || !containsYieldExpr(x.Then)) {
			return c.expr(x.Else)
		}
	}
	cond, err := c.expr(x.Cond)
	if err != nil {
		return 0, err
	}
	d := c.alloc()
	jf := c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfFalse, A: cond})
	if x.Then == nil {
		// Короткая форма `$a ?: $b`: при истинном условии результат — само
		// условие (повторно не вычисляется).
		c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: d, B: cond})
	} else {
		t, err := c.expr(x.Then)
		if err != nil {
			return 0, err
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: d, B: t})
	}
	jend := c.chunk.Emit(vm.Instruction{Op: vm.OpJump})
	c.setJump(jf, c.chunk.Len())
	e, err := c.expr(x.Else)
	if err != nil {
		return 0, err
	}
	c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: d, B: e})
	c.setJump(jend, c.chunk.Len())
	return d, nil
}

func (c *compiler) coalesce(x *CoalesceExpr) (uint8, error) {
	c.emitLine(c.exprLine(x))
	l, err := c.exprCoalesceBase(x.L)
	if err != nil {
		return 0, err
	}
	d := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: d, B: l})
	n := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: n})
	isn := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpIdentical, A: isn, B: d, C: int32(n)})
	jf := c.chunk.Emit(vm.Instruction{Op: vm.OpJumpIfFalse, A: isn})
	r, err := c.expr(x.R)
	if err != nil {
		return 0, err
	}
	c.chunk.Emit(vm.Instruction{Op: vm.OpMove, A: d, B: r})
	c.setJump(jf, c.chunk.Len())
	return d, nil
}

// exprCoalesceBase компилирует левую часть `??` в IS-режиме: вложенные
// индексы не порождают исключений (OpIndexSafeWarn), отсутствующие значения
// дают null, а ведуще-числовые строковые offset'ы предупреждают и берутся
// как значение (Zend FETCH_DIM_IS).
func (c *compiler) exprCoalesceBase(a Expr) (uint8, error) {
	if ix, ok := a.(*IndexExpr); ok {
		if ix.Key == nil {
			return 0, c.newFatal("Cannot use [] for reading")
		}
		base, err := c.exprCoalesceBase(ix.Arr)
		if err != nil {
			return 0, err
		}
		k, err := c.expr(ix.Key)
		if err != nil {
			return 0, err
		}
		r := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpIndexSafeWarn, A: r, B: base, C: int32(k)})
		return r, nil
	}
	if pe, ok := a.(*PropExpr); ok {
		return c.propGetSafe(pe)
	}
	c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
	r, err := c.expr(a)
	c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
	if err != nil {
		return 0, err
	}
	return r, nil
}

// exprIssetBase компилирует базу цепочки isset()/empty(): вложенные индексы
// читаются в IS-режиме (OpIndexSafe) — отсутствующие ключи и нечисловые
// строковые offset'ы дают null без диагностик и исключений (Zend BP_VAR_IS).
func (c *compiler) exprIssetBase(a Expr) (uint8, error) {
	if ix, ok := a.(*IndexExpr); ok {
		if ix.Key == nil {
			return 0, c.newFatal("Cannot use [] for reading")
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
		base, err := c.exprIssetBase(ix.Arr)
		if err == nil {
			var k uint8
			k, err = c.expr(ix.Key)
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
			if err != nil {
				return 0, err
			}
			r := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpIndexSafe, A: r, B: base, C: int32(k)})
			return r, nil
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
		return 0, err
	}
	if pe, ok := a.(*PropExpr); ok {
		return c.propGetSafe(pe)
	}
	c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
	r, err := c.expr(a)
	c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
	if err != nil {
		return 0, err
	}
	return r, nil
}

// propGetSafe компилирует чтение свойства в IS-режиме (промежуточные звенья
// цепочек isset/??): OpPropGetSafe/OpPropGetSafeDyn не диагностируют
// отсутствующие и неинициализированные свойства.
func (c *compiler) propGetSafe(pe *PropExpr) (uint8, error) {
	base, err := c.exprIssetBase(pe.Recv)
	if err != nil {
		return 0, err
	}
	r := c.alloc()
	if pe.NameExpr != nil {
		ne, err := c.expr(pe.NameExpr)
		if err != nil {
			return 0, err
		}
		c.chunk.Emit(vm.Instruction{Op: vm.OpPropGetSafeDyn, A: r, B: base, C: int32(ne)})
	} else {
		c.chunk.Emit(vm.Instruction{Op: vm.OpPropGetSafe, A: r, B: base, C: int32(c.chunk.AddConst(vm.StrConst(pe.Name)))})
	}
	return r, nil
}

func (c *compiler) isset(x *IssetExpr) (uint8, error) {
	d := c.alloc()
	c.chunk.Emit(vm.Instruction{Op: vm.OpTrue, A: d})
	for _, a := range x.Args {
		var r uint8
		var err error
		if pe, ok := a.(*PropExpr); ok {
			obj, err2 := c.exprIssetBase(pe.Recv)
			if err2 != nil {
				return 0, err2
			}
			r = c.alloc()
			if pe.NameExpr != nil {
				ne, err2 := c.expr(pe.NameExpr)
				if err2 != nil {
					return 0, err2
				}
				c.chunk.Emit(vm.Instruction{Op: vm.OpPropIssetDyn, A: r, B: obj, C: int32(ne)})
			} else {
				c.chunk.Emit(vm.Instruction{Op: vm.OpPropIsset, A: r, B: obj, C: int32(c.chunk.AddConst(vm.StrConst(pe.Name)))})
			}
			// OpPropIsset сам реализует isset-семантику (наличие и не-null),
			// поэтому сравнение с null ниже не нужно.
			nd := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpLogicalAnd, A: nd, B: d, C: int32(r)})
			d = nd
			continue
		} else if ix, ok := a.(*IndexExpr); ok {
			if ix.Key == nil {
				return 0, c.newFatal("Cannot use [] for reading")
			}
			arr, err2 := c.exprIssetBase(ix.Arr)
			if err2 != nil {
				return 0, err2
			}
			var k uint8
			if ix.Key != nil {
				k, err2 = c.expr(ix.Key)
				if err2 != nil {
					return 0, err2
				}
			}
			r = c.alloc()
			// OpIndexIsset сам реализует isset-семантику: для массивов/строк
			// проверяет наличие и не-null, для ArrayAccess вызывает
			// offsetExists. Поэтому отдельное сравнение с null не нужно.
			c.chunk.Emit(vm.Instruction{Op: vm.OpIndexIsset, A: r, B: arr, C: int32(k)})
			nd := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpLogicalAnd, A: nd, B: d, C: int32(r)})
			d = nd
			continue
		} else if spe, ok := a.(*StaticPropExpr); ok && spe.NameExpr == nil {
			if spe.ClassExpr != nil {
				cls, err2 := c.staticClassReg(spe.Class, spe.ClassExpr)
				if err2 != nil {
					return 0, err2
				}
				nmr, err2 := c.staticNameReg(spe.Name, nil)
				if err2 != nil {
					return 0, err2
				}
				r = c.emitCall("__php_dyn_static_isset", []uint8{cls, nmr})
				nd := c.alloc()
				c.chunk.Emit(vm.Instruction{Op: vm.OpLogicalAnd, A: nd, B: d, C: int32(r)})
				d = nd
				continue
			}
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
			sel, err2 := c.classRef(spe.Class)
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
			if err2 != nil {
				cls, cerr := c.staticClassReg(spe.Class, nil)
				if cerr != nil {
					return 0, cerr
				}
				nmr, cerr := c.staticNameReg(spe.Name, nil)
				if cerr != nil {
					return 0, cerr
				}
				r = c.emitCall("__php_dyn_static_isset", []uint8{cls, nmr})
				nd := c.alloc()
				c.chunk.Emit(vm.Instruction{Op: vm.OpLogicalAnd, A: nd, B: d, C: int32(r)})
				d = nd
				continue
			}
			r = c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpStaticIsset, A: r, B: sel,
				C: int32(c.chunk.AddConst(vm.StrConst(spe.Name)))})
			nd := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpLogicalAnd, A: nd, B: d, C: int32(r)})
			d = nd
			continue
		} else if isThisVar(a) {
			// isset($this) в статическом/несвязанном контексте — false, без
			// ошибки "Using $this when not in object context".
			c.usesThisVar = true
			r = c.emitCall("__php_isset_this", nil)
			nd := c.alloc()
			c.chunk.Emit(vm.Instruction{Op: vm.OpLogicalAnd, A: nd, B: d, C: int32(r)})
			d = nd
			continue
		} else {
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOn})
			r, err = c.expr(a)
			c.chunk.Emit(vm.Instruction{Op: vm.OpSuppressOff})
			if err != nil {
				return 0, err
			}
		}
		n := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpNull, A: n})
		t := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpNotIdentical, A: t, B: r, C: int32(n)})
		nd := c.alloc()
		c.chunk.Emit(vm.Instruction{Op: vm.OpLogicalAnd, A: nd, B: d, C: int32(t)})
		d = nd
	}
	return d, nil
}

// binOp отображает текстовый оператор в опкод.
func binOp(op string) (vm.Opcode, bool) {
	switch op {
	case "+":
		return vm.OpAdd, true
	case "-":
		return vm.OpSub, true
	case "*":
		return vm.OpMul, true
	case "/":
		return vm.OpDiv, true
	case "%":
		return vm.OpMod, true
	case "**":
		return vm.OpPow, true
	case ".":
		return vm.OpConcat, true
	case "&":
		return vm.OpBitAnd, true
	case "|":
		return vm.OpBitOr, true
	case "^":
		return vm.OpBitXor, true
	case "<<":
		return vm.OpShl, true
	case ">>":
		return vm.OpShr, true
	case "==":
		return vm.OpEqual, true
	case "!=", "<>":
		return vm.OpNotEqual, true
	case "===":
		return vm.OpIdentical, true
	case "!==":
		return vm.OpNotIdentical, true
	case "<":
		return vm.OpLess, true
	case "<=":
		return vm.OpLessOrEqual, true
	case ">":
		return vm.OpGreater, true
	case ">=":
		return vm.OpGreaterEqual, true
	case "<=>":
		return vm.OpSpaceship, true
	}
	return 0, false
}

func constValue(name string) (vm.Const, bool) {
	switch strings.ToUpper(name) {
	case "TRUE":
		return vm.BoolConst(true), true
	case "FALSE":
		return vm.BoolConst(false), true
	case "NULL":
		return vm.NullConst(), true
	case "PHP_EOL":
		return vm.StrConst("\n"), true
	case "PHP_VERSION":
		return vm.StrConst("8.3.6"), true
	case "PASSWORD_BCRYPT", "PASSWORD_DEFAULT":
		return vm.StrConst(passwordBcryptAlgo), true
	case "PASSWORD_BCRYPT_DEFAULT_COST":
		return vm.IntConst(passwordBcryptDefaultCost), true
	case "PASSWORD_ARGON2I":
		return vm.StrConst("argon2i"), true
	case "PASSWORD_ARGON2ID":
		return vm.StrConst("argon2id"), true
	case "PHP_VERSION_ID":
		return vm.IntConst(80306), true
	case "PHP_OS":
		return vm.StrConst("Linux"), true
	case "DIRECTORY_SEPARATOR":
		return vm.StrConst("/"), true
	case "PATH_SEPARATOR":
		return vm.StrConst(":"), true
	case "PHP_INT_MAX":
		return vm.IntConst(math.MaxInt64), true
	case "PHP_INT_MIN":
		return vm.IntConst(math.MinInt64), true
	case "PHP_INT_SIZE":
		return vm.IntConst(8), true
	case "PHP_MAXPATHLEN":
		return vm.IntConst(4096), true
	case "M_PI":
		return vm.FloatConst(math.Pi), true
	case "M_E":
		return vm.FloatConst(math.E), true
	case "INF":
		return vm.FloatConst(math.Inf(1)), true
	case "NAN":
		return vm.FloatConst(math.NaN()), true
	case "PHP_FLOAT_EPSILON":
		return vm.FloatConst(math.Nextafter(1, 2) - 1), true
	}
	return vm.Const{}, false
}
