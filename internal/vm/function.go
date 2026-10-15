package vm

// Function — вызываемая сущность. Может быть:
//   - байткод-функцией (Executable = *CompiledFunction),
//   - нативной Go-функцией (Executable = *NativeFunction),
//   - позже — методом/замыканием (BoundMethod/Closure).
//
// Scope (self/static/this) заложен в Frame, а не в Function: одна и та же
// функция может вызываться в разных scope (наследование, late static binding).
type Function struct {
	Name       string
	NumArgs    int
	NumRegs    int
	Executable Executable
	// Disabled — внутренняя функция, отключённая через INI disable_functions:
	// Zend удаляет её из таблицы функций (function_exists — false, вызов —
	// "Call to undefined function"). Пользовательская функция с тем же именем
	// не блокируется.
	Disabled bool
	// Scope — класс объявления (self). Позже заполнится для методов.
	Scope ClassInfo
	// AcceptsExtraNamed — функция принимает extra named params (Zend
	// ZEND_CALL_HAS_EXTRA_NAMED_PARAMS: Reflection::invoke и подобные
	// нативные varargs, собирающие имена вручную). У ZPP-функций неизвестные
	// имена дают ArgumentCountError.
	AcceptsExtraNamed bool

	// ByRefArgs[i] — параметр i объявлен как &$param. VM кладёт в регистр
	// параметра Ref-ячейку вместо значения.
	ByRefArgs []bool
	// ParamNames — имена параметров (без `$`) для привязки именованных
	// аргументов. Может быть nil для встроенных функций.
	ParamNames []string
	// ByRefReturn — функция объявлена как `function &name()`. Возврат может
	// нести Ref-ячейку.
	ByRefReturn bool
	// NumCaptures — число захваченных переменных (для замыканий). Их слоты
	// идут в регистрах NumArgs..NumArgs+NumCaptures-1.
	NumCaptures int
	// CaptureNames — имена захваченных переменных (use(...)/стрелочные) в
	// порядке захвата, без `$`: для var_dump/print_r замыканий.
	CaptureNames []string
	// IsStatic — статическое замыкание (`static function`): не связывает $this.
	IsStatic bool
	// VariadicByRef — последний (вариативный) параметр объявлен `&...$args`:
	// все собранные в него аргументы Zend молча оборачивает в ссылки.
	VariadicByRef bool
	// UsesThis — тело замыкания обращается к $this (аналог ZEND_ACC_USES_THIS:
	// запрещает отвязку $this через Closure::bindTo(null)).
	UsesThis bool
	// RetType — объявленный тип возврата (`: T`), "" — не объявлен.
	RetType string
	// DefFile/DefLine — место объявления функции (для диагностик проверки
	// аргументов, как в Zend: ошибка привязывается к сигнатуре).
	DefFile string
	DefLine int
	// ParamTypes[i] — объявленный тип параметра i, "" — не объявлен.
	ParamTypes []string
	// NumRequired — число обязательных параметров (до первого с default).
	// Позволяет отличать «аргумент не передан» (null-заглушка) от явного null.
	NumRequired int
	// MinArgs/MaxArgs — границы арности встроенной функции из таблицы сигнатур
	// (0 — неизвестно). NativeVariadic — верхней границы нет.
	MinArgs        int
	MaxArgs        int
	NativeVariadic bool
	// HasNativeSig — MinArgs/ParamNames заполнены из таблицы встроенных функций.
	HasNativeSig bool
	// DisplayName — имя для диагностик ("Class::method"), если отличается от
	// вычисленного по Scope/Name (например, анонимные классы).
	DisplayName string
	// trivial — тело метода пусто и нет параметров: вызов не имеет побочных
	// эффектов кроме возврата null, кадр можно не устанавливать (skip-call).
	trivial bool
	// SensitiveParams — маска параметров с #[SensitiveParameter]; их значения
	// скрываются в трейсах (подменяются на SensitiveParameterValue).
	SensitiveParams []bool
	// SensitiveVariadic — вариативный параметр помечен #[SensitiveParameter]:
	// все собранные в него аргументы скрываются.
	SensitiveVariadic bool
	// IsFinal — метод объявлен `final` (для ReflectionMethod::isFinal).
	IsFinal bool
	// Attrs — атрибуты `#[...]` объявления (для ReflectionFunction/Method).
	Attrs []AttributeInfo
	// ParamAttrs[idx] — атрибуты параметра idx (для ReflectionParameter).
	ParamAttrs [][]AttributeInfo
}

// MarkTrivial помечает функцию как «пустышку» (пустое тело, без параметров):
// вызов можно схлопнуть в null без установки кадра.
func (f *Function) MarkTrivial() { f.trivial = true }

// IsTrivial сообщает, что вызов функции можно пропустить, вернув null.
func (f *Function) IsTrivial() bool { return f != nil && f.trivial }

// Executable — то, что реально исполняет вызов.
type Executable interface {
	// Call запускает функцию в переданном кадре. vm — для доступа к heap,
	// стеку кадров и глобальным функциям.
	Call(vm *VM, ctx *Context, args []Slot) (Slot, error)
}

// CompiledFunction — функция, скомпилированная в байткод.
type CompiledFunction struct {
	chunk *Chunk
	// isGenerator — функция содержит `yield`; вызов НЕ исполняет тело, а
	// создаёт ленивый объект Generator (см. vm.makeGenerator, vm.advance).
	isGenerator bool
	// variadic — последний параметр объявлен как ...$rest; numFixed — число
	// фиксированных параметров перед ним. При вызове «хвостовые» аргументы
	// упаковываются в массив и кладутся в регистр numFixed.
	variadic bool
	numFixed int
	// owner — обёртка *Function, которой принадлежит этот байткод (нужна
	// для построения stack trace генераторов).
	owner *Function
	// numParams — число объявленных параметров (без захваченных переменных).
	// Используется func_get_args()/func_get_arg() для различения объявленных
	// параметров и лишних переданных аргументов.
	numParams int
	// usesThisVar — тело обращается к $this не через OpThis (isset($this));
	// см. UsesThis.
	usesThisVar bool
}

// SetNumParams задаёт число объявленных параметров.
func (f *CompiledFunction) SetNumParams(n int) { f.numParams = n }

// CodeLen возвращает число инструкций тела функции (диагностика
// оптимизатора/бенчмарки).
func (f *CompiledFunction) CodeLen() int { return f.chunk.Len() }

// NumParams возвращает число объявленных параметров.
func (f *CompiledFunction) NumParams() int { return f.numParams }

// UsesThis сообщает, что тело функции обращается к $this (нужно для
// диагностик Closure::bindTo(null) — аналог ZEND_ACC_USES_THIS).
func (f *CompiledFunction) UsesThis() bool {
	if f.usesThisVar {
		return true
	}
	for i := 0; i < f.chunk.Len(); i++ {
		if f.chunk.At(i).Op == OpThis {
			return true
		}
	}
	return false
}

// SetUsesThisVar помечает, что тело обращается к $this не через OpThis
// (isset($this) компилируется в вызов __php_isset_this).
func (f *CompiledFunction) SetUsesThisVar() { f.usesThisVar = true }

// SetOwner связывает байткод с его *Function (для generator stack trace).
func (f *CompiledFunction) SetOwner(o *Function) { f.owner = o }

// Owner возвращает обёртку *Function (может быть nil).
func (f *CompiledFunction) Owner() *Function { return f.owner }

// SetVariadic помечает функцию как вариативную с fixed фиксированными
// параметрами перед ...$rest.
func (f *CompiledFunction) SetVariadic(fixed int) {
	f.variadic = true
	f.numFixed = fixed
}

// packVariadic упаковывает хвостовые аргументы в массив для ...$rest.
func (f *CompiledFunction) packVariadic(args []Slot) []Slot {
	fixed := f.numFixed
	if len(args) <= fixed {
		out := make([]Slot, fixed+1)
		copy(out, args)
		out[fixed] = MakeArray(NewArray())
		return out
	}
	arr := NewArray()
	for _, v := range args[fixed:] {
		arr.Append(v)
	}
	out := make([]Slot, fixed+1)
	copy(out, args[:fixed])
	out[fixed] = MakeArray(arr)
	return out
}

// SetGenerator помечает функцию как функцию-генератор (вызывается
// компилятором, если тело содержало yield).
func (f *CompiledFunction) SetGenerator() { f.isGenerator = true }

// IsGenerator сообщает, что функция-генератор.
func (f *CompiledFunction) IsGenerator() bool { return f.isGenerator }

func (f *CompiledFunction) Call(vm *VM, ctx *Context, args []Slot) (Slot, error) {
	if f.isGenerator {
		return vm.makeGenerator(f, ctx, args)
	}
	return vm.execCompiled(f.chunk, args)
}

// PackVariadic упаковывает хвостовые аргументы в массив, если функция
// вариативная; иначе возвращает args без изменений. namedTail (если не nil) —
// именованные аргументы, попавшие в вариативный параметр (строковые ключи).
func (f *CompiledFunction) PackVariadic(args []Slot) []Slot {
	if !f.variadic {
		return args
	}
	out := f.packVariadic(args)
	return out
}

// NativeFunction — нативная (Go) функция. Это ABI для расширений.
//
// Низкоуровневый вариант: принимает контекст и сырые слоты, возвращает слот.
// Для удобства объявления есть типизированные обёртки (см. native.go).
type NativeFunction struct {
	fn func(ctx *Context, args []Slot) (Slot, error)
}

func (f *NativeFunction) Call(vm *VM, ctx *Context, args []Slot) (Slot, error) {
	res, err := f.fn(ctx, args)
	// Нативная функция могла не вернуть error напрямую, а выставить
	// отложенное исключение через ctx.Throw: приоритет у него.
	if t := ctx.takeThrown(); t != nil {
		return Slot{}, t
	}
	// Вложенный eval/include мог напечатать фатальную ошибку или выполнить
	// exit(): останавливаем весь скрипт.
	if vm != nil && vm.haltRequested {
		return Slot{}, exitSignal{}
	}
	return res, err
}

// NewCompiledFunction создаёт байткод-функцию из чанка (для компилятора).
func NewCompiledFunction(chunk *Chunk) *CompiledFunction {
	return &CompiledFunction{chunk: chunk}
}
