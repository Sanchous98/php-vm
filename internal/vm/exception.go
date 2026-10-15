package vm

import (
	"fmt"
	"sort"
	"strings"
)

// Builtin-классы исключений PHP. Иерархия:
//
//	Throwable
//	  Error
//	    TypeError
//	      ArgumentCountError
//	    DivisionByZeroError
//	  Exception
//	    RuntimeException
//
// Данные исключения (message/code/previous) хранятся в свойствах объекта,
// поэтому пользовательские классы-наследники (class MyEx extends Exception)
// работают без специальной поддержки data-типа.
func NewExceptionClasses() []ClassInfo {
	throwable := NewClass("Throwable", nil)
	errClass := NewClass("Error", throwable)
	arithErr := NewClass("ArithmeticError", errClass)
	divZero := NewClass("DivisionByZeroError", arithErr)
	typeErr := NewClass("TypeError", errClass)
	argErr := NewClass("ArgumentCountError", typeErr)
	valueErr := NewClass("ValueError", errClass)
	unhandledMatchErr := NewClass("UnhandledMatchError", errClass)
	exClass := NewClass("Exception", throwable)
	runtimeErr := NewClass("RuntimeException", exClass)
	compileErr := NewClass("CompileError", errClass)
	parseErr := NewClass("ParseError", compileErr)
	stdClass := NewClass("stdClass", nil)
	directory := NewClass("Directory", nil)

	// Распространённая SPL/стандартная иерархия исключений.
	assertErr := NewClass("AssertionError", errClass)
	logicErr := NewClass("LogicException", exClass)
	badFunc := NewClass("BadFunctionCallException", logicErr)
	badMethod := NewClass("BadMethodCallException", badFunc)
	domainErr := NewClass("DomainException", logicErr)
	invalidArg := NewClass("InvalidArgumentException", logicErr)
	lengthErr := NewClass("LengthException", logicErr)
	outOfRange := NewClass("OutOfRangeException", logicErr)
	outOfBounds := NewClass("OutOfBoundsException", runtimeErr)
	overflowErr := NewClass("OverflowException", runtimeErr)
	rangeErr := NewClass("RangeException", runtimeErr)
	underflowErr := NewClass("UnderflowException", runtimeErr)
	unexpectedVal := NewClass("UnexpectedValueException", runtimeErr)
	jsonErr := NewClass("JsonException", exClass)
	reflErr := NewClass("ReflectionException", exClass)
	closedGenErr := NewClass("ClosedGeneratorException", runtimeErr)
	fiberErr := NewClass("FiberError", errClass)
	errExc := NewClass("ErrorException", exClass)

	all := []ClassInfo{
		throwable, errClass, arithErr, divZero, typeErr, argErr, valueErr,
		unhandledMatchErr,
		exClass, runtimeErr, parseErr, compileErr,
		assertErr, logicErr, badFunc, badMethod, domainErr, invalidArg, lengthErr,
		outOfRange, outOfBounds, overflowErr, rangeErr, underflowErr, unexpectedVal, jsonErr, reflErr,
		closedGenErr,
		fiberErr,
		errExc,
	}
	for _, ci := range all {
		registerExceptionMembers(ci)
	}
	return append(all, stdClass, directory)
}

// exceptionCtorDecl — конструктор Throwable с проверкой типа message.
// declName — класс, объявивший конструктор: у наследников без своего
// конструктора в диагностике стоит исходный класс (Exception/Error/...).
func exceptionCtorDecl(ctx *Context, args []Slot, declName string) (Slot, error) {
	if len(args) > 0 {
		// Первый параметр — string $message: объекты/массивы/ресурсы дают
		// TypeError (int/float/bool приводятся к строке, как в coercive-режиме).
		switch a := deref(args[0]); a.typ {
		case ObjectType, ArrayType, ResourceType:
			ctx.Throw(NewThrowable("TypeError", declName+"::__construct(): Argument #1 ($message) must be of type string, "+
				valueTypeName(ctx.vm, a)+" given"))
			if ctx.HasThrown() {
				return *new(Slot), nil
			}
		}
	}
	return exceptionCtor(ctx, args)
}

// exceptionCtor — базовый конструктор Throwable: сохраняет message/code/previous
// и снимает локацию/трейс в момент конструирования.
func exceptionCtor(ctx *Context, args []Slot) (Slot, error) {
	o := ctx.ThisObj()
	if o == nil {
		return MakeNull(), nil
	}
	// Захватываем стек и локацию в момент конструирования (кроме
	// повторного вызова __construct у уже инициализированного объекта).
	if !o.excSet && ctx.vm != nil {
		tr := ctx.vm.captureExceptionTrace()
		if n := len(tr); n > 0 {
			f := tr[n-1] // кадр самого __construct
			o.excFile = f.callerFile
			o.excLine = f.callerLine
			tr = tr[:n-1]
			// Кадры конструктора самого исключения (переопределённый
			// __construct + родительские) в trace не входят. Кадры
			// конструкторов других классов (напр. атрибут, из которого
			// создано исключение) остаются.
			for len(tr) > 0 {
				last := tr[len(tr)-1]
				if last.callee != nil && last.callee.Name == "__construct" &&
					last.callerFile != "[internal function]" &&
					last.callee.Scope != nil && o.class != nil && o.class.IsA(last.callee.Scope) {
					tr = tr[:len(tr)-1]
					continue
				}
				break
			}
			o.excTrace = tr
		} else {
			o.excFile = ctx.vm.curFile
			o.excLine = ctx.vm.line()
		}
		o.excSet = true
	}
	if len(args) > 0 {
		o.SetProp("message", MakeString(args[0].ToString()))
	}
	if len(args) > 1 {
		o.SetProp("code", MakeInt(args[1].ToInt()))
	}
	if len(args) > 2 {
		o.SetProp("previous", args[2])
	}
	return MakeNull(), nil
}

// registerExceptionMembers добавляет конструктор и методы Throwable.
func registerExceptionMembers(ci ClassInfo) {
	if rc, ok := asRuntimeClass(ci); ok && rc != nil {
		for _, p := range []struct{ name, typ string }{
			{"message", ""}, {"code", ""}, {"file", ""},
			{"line", ""}, {"trace", "array"}, {"previous", "?Throwable"},
		} {
			rc.SetPropTypeOnly(p.name, p.typ, Protected)
		}
	}
	declName := ci.Name()
	ci.Constructor(&Function{Name: "__construct", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			return exceptionCtorDecl(ctx, args, declName)
		},
	}})
	// errorExceptionCtor — конструктор ErrorException: как базовый, но с
	// дополнительными severity/filename/line.
	if rc, ok := ci.(*runtimeClass); ok && rc != nil && rc.name == "ErrorException" {
		rc.ctor = &Function{Name: "__construct", NumArgs: 6, Scope: ci, Executable: &NativeFunction{
			fn: func(ctx *Context, args []Slot) (Slot, error) {
				if _, err := exceptionCtorDecl(ctx, args, "ErrorException"); err != nil {
					return Slot{}, err
				}
				o := ctx.ThisObj()
				if o == nil {
					return MakeNull(), nil
				}
				if len(args) > 2 {
					o.SetProp("severity", MakeInt(args[2].ToInt()))
				} else {
					o.SetProp("severity", MakeInt(1)) // E_ERROR
				}
				if len(args) > 3 {
					a := deref(args[3])
					if a.typ != NullType {
						o.excFile = a.ToString()
						o.excLine = 0
					}
				}
				if len(args) > 4 {
					a := deref(args[4])
					if a.typ != NullType {
						o.excLine = int(a.ToInt())
					}
				}
				return MakeNull(), nil
			},
		}}
		rc.Method("getSeverity", &Function{Name: "getSeverity", NumArgs: 0, Executable: &NativeFunction{
			fn: func(ctx *Context, args []Slot) (Slot, error) {
				if o := ctx.ThisObj(); o != nil {
					if v, ok := o.GetProp("severity"); ok {
						return MakeInt(v.ToInt()), nil
					}
				}
				return MakeInt(0), nil
			},
		}})
	}
	ci.Method("getMessage", &Function{Name: "getMessage", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			return MakeString(exceptionMessage(ctx.ThisObj())), nil
		},
	}})
	ci.Method("getCode", &Function{Name: "getCode", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			return MakeInt(exceptionCode(ctx.ThisObj())), nil
		},
	}})
	ci.Method("getFile", &Function{Name: "getFile", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			if o := ctx.ThisObj(); o != nil {
				// Свойство file может быть переписано пользовательским
				// конструктором (Zend: file/line — обычные свойства).
				if v, ok := o.GetProp("file"); ok {
					return MakeString(v.ToString()), nil
				}
				if o.excFile != "" {
					return MakeString(o.excFile), nil
				}
			}
			return MakeString(""), nil
		},
	}})
	ci.Method("getLine", &Function{Name: "getLine", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			if o := ctx.ThisObj(); o != nil {
				if v, ok := o.GetProp("line"); ok {
					return MakeInt(int64(v.ToInt())), nil
				}
				return MakeInt(int64(o.excLine)), nil
			}
			return MakeInt(0), nil
		},
	}})
	ci.Method("getTrace", &Function{Name: "getTrace", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			o := ctx.ThisObj()
			if o == nil || ctx.vm == nil {
				return MakeArray(NewArray()), nil
			}
			return ctx.vm.FramesArray(o.excTrace, 0, 0), nil
		},
	}})
	ci.Method("getPrevious", &Function{Name: "getPrevious", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			if o := ctx.ThisObj(); o != nil {
				if v, ok := o.GetProp("previous"); ok {
					var s Slot
					SlotOf(&s, v)
					return s, nil
				}
			}
			return MakeNull(), nil
		},
	}})
	ci.Method("__toString", &Function{Name: "__toString", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			o := ctx.ThisObj()
			if o == nil {
				return MakeString(""), nil
			}
			// Zend 8.3 строит строку по цепочке previous, добавляя каждое
			// следующее (более старое) исключение В НАЧАЛО с разделителем
			// "\n\nNext " — сначала печатается самое глубокое previous,
			// последним — текущее (zend_exceptions.c: Exception::__toString).
			var b strings.Builder
			for cur, depth := o, 0; cur != nil && depth < 100; depth++ {
				prev := b.String()
				b.Reset()
				msg := exceptionMessage(cur)
				if msg != "" {
					b.WriteString(cur.ClassName())
					b.WriteString(": ")
					b.WriteString(msg)
				} else {
					b.WriteString(cur.ClassName())
				}
				file := cur.excFile
				line := cur.excLine
				if file != "" && cur.excDefined {
					fmt.Fprintf(&b, " and defined in %s:%d", file, line)
				} else if file != "" {
					fmt.Fprintf(&b, " in %s:%d", file, line)
				} else if file == "" && line > 0 {
					fmt.Fprintf(&b, " in :%d", line)
				}
				b.WriteString("\nStack trace:\n")
				if ctx.vm != nil {
					b.WriteString(ctx.vm.traceString(cur.excTrace, file, line))
				} else {
					b.WriteString("#0 {main}")
				}
				if prev != "" {
					b.WriteString("\n\nNext ")
					b.WriteString(prev)
				}
				cur = exceptionPreviousObj(cur)
			}
			return MakeString(b.String()), nil
		},
	}})
	ci.Method("getTraceAsString", &Function{Name: "getTraceAsString", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			o := ctx.ThisObj()
			if o == nil || ctx.vm == nil {
				return MakeString("#0 {main}"), nil
			}
			return MakeString(ctx.vm.traceString(o.excTrace, o.excFile, o.excLine)), nil
		},
	}})
}

func exceptionMessage(o *Object) string {
	if o == nil {
		return ""
	}
	if v, ok := o.GetProp("message"); ok {
		return v.ToString()
	}
	return ""
}

// exceptionPreviousObj возвращает previous-исключение объекта (nil, если нет).
func exceptionPreviousObj(o *Object) *Object {
	if o == nil {
		return nil
	}
	v, ok := o.GetProp("previous")
	if !ok {
		return nil
	}
	if s, ok := v.(Slot); ok && deref(s).typ == ObjectType {
		return s.obj()
	}
	return nil
}

func exceptionCode(o *Object) int64 {
	if o == nil {
		return 0
	}
	if v, ok := o.GetProp("code"); ok {
		return v.ToInt()
	}
	return 0
}

// findClass ищет класс по имени среди зарегистрированных в VM.
func (vm *VM) findClass(name string) ClassInfo {
	for _, ci := range vm.classes {
		if ClassHidden(ci) {
			continue
		}
		if ci.Name() == name {
			return ci
		}
	}
	if ci, ok := vm.classAliases[strings.ToLower(strings.TrimLeft(name, "\\"))]; ok {
		return ci
	}
	return nil
}

// AliasConflictFor возвращает вид и признак наличия псевдонима class_alias с
// именем name (для диагностики "Cannot declare ... already in use").
func (vm *VM) AliasConflictFor(name string) (string, bool) {
	ci, ok := vm.classAliases[strings.ToLower(strings.TrimLeft(name, "\\"))]
	if !ok || ci == nil {
		return "", false
	}
	switch {
	case ci.IsInterface():
		return "interface", true
	case ci.IsTrait():
		return "trait", true
	case ci.IsEnum():
		return "enum", true
	}
	return "class", true
}

// AddClassAlias регистрирует псевдоним имени класса (class_alias).
func (vm *VM) AddClassAlias(alias string, ci ClassInfo) {
	if vm.classAliases == nil {
		vm.classAliases = map[string]ClassInfo{}
	}
	key := strings.ToLower(strings.TrimLeft(alias, "\\"))
	if _, exists := vm.classAliases[key]; !exists {
		// Порядок создания псевдонимов — как у Zend в get_declared_classes
		// (псевдонимы идут после объявленных классов; bug62343).
		vm.classAliasOrder = append(vm.classAliasOrder, alias)
	}
	vm.classAliases[key] = ci
}

// ClassAliasOrder возвращает имена псевдонимов классов в порядке создания.
func (vm *VM) ClassAliasOrder() []string { return vm.classAliasOrder }

// ClassAliasTargets возвращает псевдонимы классов (нижний регистр имени →
// имя целевого класса) — для проверок типов при рантайм-компиляции
// eval/include (bug54624).
func (vm *VM) ClassAliasTargets() map[string]string {
	if len(vm.classAliases) == 0 {
		return nil
	}
	out := make(map[string]string, len(vm.classAliases))
	for name, ci := range vm.classAliases {
		if ci != nil {
			out[name] = ci.Name()
		}
	}
	return out
}

// TraitAliasNames возвращает имена псевдонимов трейтов (class_alias) в
// алфавитном порядке.
func (vm *VM) TraitAliasNames() []string {
	var out []string
	for name, ci := range vm.classAliases {
		if ci != nil && ci.IsTrait() {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// throwableClass возвращает класс объекта-исключения (или класс по имени для
// нативных ошибок).
func (vm *VM) throwableClass(th *Throwable) ClassInfo {
	if th != nil && th.Value.typ == ObjectType {
		if ci := vm.classInfo(th.Value); ci != nil {
			return ci
		}
	}
	if th != nil && th.Class != "" {
		return vm.findClass(th.Class)
	}
	return nil
}

// throwableObject возвращает объект-исключение для catch-переменной. Для
// нативных ошибок объект материализуется из класса.
func (vm *VM) throwableObject(th *Throwable, ci ClassInfo) Slot {
	if th.Value.typ == ObjectType {
		return th.Value
	}
	if ci == nil {
		return MakeNull()
	}
	o := ci.New(&vm.ctx, nil)
	if o == nil {
		return MakeNull()
	}
	// Zend присваивает объекту-исключению PHP-хендл (#N) уже при выбросе —
	// даже если он создан не `new`, а внутренней ошибкой (TypeError и т.п.).
	vm.markObj(o)
	o.SetProp("message", MakeString(th.Message))
	o.SetProp("code", MakeInt(th.Code))
	if !o.excSet {
		o.excTrace = deepCopyExceptionFrames(th.Trace)
		o.excFile = th.LocFile
		o.excLine = th.LocLine
		o.excDefined = th.DefinedIn
		o.excSet = true
	}
	return vm.slotOf(o)
}

// ThrowableFromValue превращает объект-исключение в error так же, как OpThrow:
// трейс/локация берутся из объекта (зафиксированы при конструировании).
func (vm *VM) ThrowableFromValue(v Slot) error { return vm.makeThrowable(v) }

// BailOut — нелокальное завершение скрипта (assert.bail): без диагностики,
// как zend_bailout. Использует служебный fatalSignal, который Run превращает
// в штатное завершение.
func (vm *VM) BailOut() { panic(fatalSignal{}) }

// makeThrowable превращает значение из OpThrow в ошибку. Бросать можно только
// объекты, реализующие Throwable.
func (vm *VM) makeThrowable(v Slot) error {
	if v.typ != ObjectType {
		return NewThrowable("Error", "Can only throw objects")
	}
	ci := vm.classInfo(v)
	throwable := vm.findClass("Throwable")
	if ci == nil || throwable == nil || !ci.isA(throwable) {
		return NewThrowable("Error", "Cannot throw objects that do not implement Throwable")
	}
	o := vm.objectOf(v)
	th := &Throwable{
		Class:   ci.Name(),
		Message: exceptionMessage(o),
		Code:    exceptionCode(o),
		Value:   v,
	}
	// Трейс/локация исключения фиксируются при конструировании (Zend); если
	// объект их не сохранил (нативная ошибка без __construct) — на момент throw.
	if o != nil && o.excSet {
		th.Trace = o.excTrace
		th.LocFile = o.excFile
		th.LocLine = o.excLine
	} else {
		th.Trace = vm.captureExceptionTrace()
		if o != nil {
			// Локация, зафиксированная при создании объекта, затем возможные
			// переопределения пользовательским конструктором.
			th.LocFile = o.excFile
			th.LocLine = o.excLine
			if v, ok := o.GetProp("file"); ok {
				th.LocFile = v.ToString()
				th.LocOverridden = true
			}
			if v, ok := o.GetProp("line"); ok {
				th.LocLine = int(v.ToInt())
				th.LocOverridden = true
			}
		}
	}
	return th
}

// asThrowable нормализует error к *Throwable, если это PHP-исключение
// (включая известные нативные ошибки).
func asThrowable(err error) (*Throwable, bool) {
	if e, ok := err.(*Throwable); ok {
		return e, true
	}
	switch err {
	case errDivByZero:
		return NewThrowable("DivisionByZeroError", "Division by zero"), true
	case errModByZero:
		return NewThrowable("DivisionByZeroError", "Modulo by zero"), true
	case errIllegalOffset:
		return NewThrowable("TypeError", "Illegal offset type"), true
	}
	return nil, false
}

// handleThrow пытается найти подходящий try-обработчик в текущем кадре.
// Возвращает true, если исключение обработано (vm.ip выставлен на catch или
// finally) — тогда exec продолжает цикл. Иначе возвращает false, и ошибку
// нужно вернуть наружу.
func (vm *VM) handleThrow(chunk *Chunk, err error) bool {
	// dispTh — «эффективное» исключение этой раскрутки (см. закрытие
	// генератора): сбрасывается в начале, заполняется на неуспешном выходе.
	vm.dispTh = nil
	// Пока исключение раскручивается, его объект — корень сборщика (Zend
	// держит его в EG(exception)): промежуточные точки времени жизни в
	// диспетчере не должны запускать __destruct объекта (catch_novar_2 — там
	// деструктор обязан сработать на входе catch). Корень ставится ДО
	// списания мёртвых операндов упавшей инструкции: сам брошенный объект
	// уже потерял регистр и иначе уничтожился бы прямо в раскрутке.
	prevTh := vm.inFlightTh
	if th0, ok := asThrowable(err); ok {
		vm.inFlightTh = th0
	}
	defer func() { vm.inFlightTh = prevTh }()
	// Аварийное завершение опкода: временные значения, мёртвые по SSA,
	// уничтожаются немедленно (Zend освобождает операнды до раскрутки) —
	// деструктор может выбросить исключение, которое заменяет текущее
	// (gh20183_001, bug73338).
	if chunk != nil && vm.chunk == chunk {
		// Живость берётся в точке СЛЕДУЮЩЕЙ инструкции: временный операнд
		// упавшего вызова там мёртв (Zend освобождает его сразу).
		pc := vm.ip
		if pc >= chunk.Len() {
			pc = chunk.Len() - 1
		}
		if pc >= 0 {
			vm.dropDeadRegs(chunk, pc)
			// Если при списании умер объект с деструктором, уничтожаем его
			// прямо здесь (Zend освобождает операнды до раскрутки): ошибка
			// деструктора заменяет текущую (gh20183_001, bug73338).
			if vm.lifeUrgent && !vm.sweepBusy {
				out := vm.sweepLifetime(false, chunk, pc)
				if out.err != nil {
					if nth, ok := asThrowable(out.err); ok {
						err = nth
						vm.inFlightTh = nth
					}
				}
			}
			if vm.pendingDestroyErr != nil {
				if nth, ok := asThrowable(vm.pendingDestroyErr); ok {
					err = nth
				}
				vm.pendingDestroyErr = nil
			}
		}
	}
	th, ok := asThrowable(err)
	if !ok {
		return false
	}
	// Исключение разматывает стек мимо END_SILENCE: Zend восстанавливает
	// изменённые INI-директивы, поэтому `@`-подавление снимается.
	if n := len(vm.suppressStack); n > 0 {
		saved := vm.suppressStack[0]
		if onlyFatalErrors(vm.errReporting) && !onlyFatalErrors(saved) {
			vm.errReporting = saved
		}
		vm.suppressStack = vm.suppressStack[:0]
		vm.suppress = 0
	}
	// Служебный сигнал закрытия генератора не фиксирует трейс/локацию.
	if th.close {
		if th.Trace == nil {
			th.Trace = []callFrameInfo{}
		}
	} else if th.Trace == nil {
		th.Trace = vm.captureExceptionTrace()
		if th.LocLine == 0 && !th.LocOverridden {
			th.LocFile = vm.curFile
			if l := vm.line(); l > 0 {
				th.LocLine = l
			}
		}
	}
	ci := vm.throwableClass(th)
	for i := len(vm.tries) - 1; i >= 0; i-- {
		h := &vm.tries[i]
		if h.fp != vm.fp {
			continue
		}
		ti := chunk.TryAt(h.tiIdx)
		if h.inFinally {
			// finally сам выбросил исключение: сцепляем previous с
			// отложенным и уходим наружу. Сигнал закрытия генератора сам
			// исключением не является: отложенное реальное исключение
			// продолжает раскрутку вместо него (bug71604: «закрываемый»
			// генератор дожигает finally, а наружу выходит ранее брошенное).
			if h.savedErr != nil {
				if th.close && !h.savedErr.close {
					th = h.savedErr
					if vm.inFlightTh != nil {
						vm.inFlightTh = th
					}
				} else if !th.close {
					vm.chainPrevious(th, h.savedErr)
				}
			}
			vm.tries = vm.tries[:i]
			continue
		}
		if !h.inCatch {
			for _, ce := range ti.Catches {
				if vm.catchMatches(ci, ce.Classes) {
					vm.tries = vm.tries[:i+1]
					obj := vm.throwableObject(th, ci)
					vm.tries[i].inCatch = true
					// Отложенные записи-обратно прерванного оператора
					// отменяются вместе с ним.
					vm.wbSkip = vm.wbSkip[:0]
					// Прерванный оператор уже потерял своё временное значение:
					// Zend освобождает его при раскручивании (FREE) до тела
					// catch. Объект выдаётся ДО списания (порядок #N как в
					// Zend: хендл исключения занят раньше освобождения мёртвых
					// операндов), а запись в ce.Var — ПОСЛЕ (иначе списание
					// снимает только что выставленный учёт).
					vm.dropDeadRegs(chunk, int(ce.PC))
					if ce.Var != 0xFF {
						{
							old := vm.cur.regs[ce.Var]
							wv := obj
							vm.cur.regs[ce.Var] = wv
							if trackedType(old.typ) || trackedType(wv.typ) {
								vm.wRegSlow(int(ce.Var), old)
							}
						}
						shareSlot(obj)
					}
					if vm.lifeCandidate || vm.zeroObjs > 0 {
						if res := vm.sweepLifetime(false, chunk, int(ce.PC)); res.err != nil && vm.pendingDestroyErr == nil {
							vm.pendingDestroyErr = res.err
						}
					}
					vm.ip = int(ce.PC)
					return true
				}
			}
		}
		if ti.FinallyPC >= 0 {
			vm.tries = vm.tries[:i+1]
			vm.tries[i].savedErr = th
			vm.tries[i].inFinally = true
			vm.wbSkip = vm.wbSkip[:0]
			vm.ip = int(ti.FinallyPC)
			return true
		}
		vm.tries = vm.tries[:i]
	}
	vm.dispTh = th
	return false
}

// chainPrevious связывает новое исключение с отложенным: $new->previous =
// $old (как PHP при исключении из finally). Если $new уже присутствует в
// цепочке previous у $old, связь не создаётся — иначе цепочка зациклилась бы
// (try_finally_recursive_previous: повторный throw того же $e из finally).
func (vm *VM) chainPrevious(newTh, oldTh *Throwable) {
	if newTh == nil || oldTh == nil || newTh == oldTh || newTh.Value.typ != ObjectType {
		return
	}
	no := vm.objectOf(newTh.Value)
	cur := oldTh
	for depth := 0; cur != nil && depth < 1000; depth++ {
		if cur.Value.typ != ObjectType {
			break
		}
		co := vm.objectOf(cur.Value)
		if co == nil {
			break
		}
		if co == no {
			// new уже в цепочке previous — не создаём цикл.
			return
		}
		v, ok := co.GetProp("previous")
		if !ok {
			break
		}
		s := valueSlot(v)
		if s.typ != ObjectType {
			break
		}
		co2 := vm.objectOf(s)
		if co2 == nil {
			break
		}
		cur = &Throwable{Value: s, Class: co2.ClassName(), Message: exceptionMessage(co2),
			Trace: co2.excTrace, LocFile: co2.excFile, LocLine: co2.excLine}
	}
	prev := vm.throwableObject(oldTh, vm.throwableClass(oldTh))
	if prev.typ != ObjectType {
		return
	}
	vm.setProp(newTh.Value, "previous", prev)
}

// catchMatches сообщает, что объект класса ci подходит хотя бы под один из
// перечисленных классов catch-ветки (по имени, с учётом наследования).
func (vm *VM) catchMatches(ci ClassInfo, classes []string) bool {
	if ci == nil {
		return false
	}
	for _, name := range classes {
		if n := strings.TrimLeft(name, "\\"); n != "" {
			if target := vm.FindClass(n); target != nil {
				if ci.isA(target) {
					return true
				}
				continue
			}
		}
		for k := ci; k != nil; k = k.Parent() {
			if k.Name() == name {
				return true
			}
		}
	}
	return false
}

// popTriesForFrame убирает обработчики, принадлежащие кадру fp (и глубже).
// Вызывается при выходе из функции, чтобы не оставлять «висящие» try.
func (vm *VM) popTriesForFrame(fp int) {
	for len(vm.tries) > 0 && vm.tries[len(vm.tries)-1].fp >= fp {
		vm.tries = vm.tries[:len(vm.tries)-1]
	}
}
