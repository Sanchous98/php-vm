package vm

import "strconv"

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
	// Variadic — последний параметр объявлен `...$rest` (для
	// ReflectionParameter::isVariadic; VariadicByRef не покрывает случай `...$x`).
	Variadic bool
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
	// typesChecked/hasTypes — кэш «среди ParamTypes есть непустой тип»:
	// если типов нет, checkParams при вызове ничего не делает.
	typesChecked bool
	hasTypes     bool
	// IsFinal — метод объявлен `final` (для ReflectionMethod::isFinal).
	IsFinal bool
	// Attrs — атрибуты `#[...]` объявления (для ReflectionFunction/Method).
	Attrs []AttributeInfo
	// ParamAttrs[idx] — атрибуты параметра idx (для ReflectionParameter).
	ParamAttrs [][]AttributeInfo
	// StaticNames/StaticKeys — объявленные статические переменные функции
	// (имена без `$`) и ключи их ячеек в таблице globals. Нужны для
	// var_dump/ReflectionFunction::getStaticVariables (gh8083).
	StaticNames []string
	StaticKeys  []string
	// StaticCopyKeys — ключи ячеек для копии метода (trait-миксин, `as`):
	// PHP хранит статические переменные каждой копии отдельно, поэтому
	// копия получает собственные ячейки, а StaticRemap перенаправляет
	// константные ключи инструкций чанка (они остаются исходными).
	StaticCopyKeys []string
	StaticRemap    map[string]string
	// StaticDefaults — значения статических переменных по умолчанию (Zend
	// хранит их в static_variables и печатает var_dump замыкания).
	StaticDefaults []Slot
}

// StaticKeyAt возвращает ключ ячейки i-й статической переменной функции:
// у копий методов (trait-миксин, `as`) он собственный (StaticCopyKeys), а
// константные ключи инструкций чанка перенаправляет StaticRemap.
func (f *Function) StaticKeyAt(i int) string {
	if f == nil {
		return ""
	}
	if f.StaticCopyKeys != nil && i < len(f.StaticCopyKeys) {
		return f.StaticCopyKeys[i]
	}
	if i < len(f.StaticKeys) {
		return f.StaticKeys[i]
	}
	return ""
}

// staticCopySeq нумерует копии методов для уникальных ключей статических ячеек.
var staticCopySeq uint64

// RebindStaticKeys выдаёт копии функции собственные ячейки статических
// переменных: PHP 8.3 хранит статические переменные трейт-метода отдельно для
// каждого использующего класса и каждого псевдонима `as`, но разделяет их при
// обычном наследовании (там копия функции не создаётся).
func (f *Function) RebindStaticKeys() {
	if f == nil || len(f.StaticKeys) == 0 {
		return
	}
	staticCopySeq++
	tok := "\x00copy" + strconv.FormatUint(staticCopySeq, 10)
	if f.StaticRemap == nil {
		f.StaticRemap = make(map[string]string, len(f.StaticKeys))
	}
	f.StaticCopyKeys = make([]string, len(f.StaticKeys))
	for i, k := range f.StaticKeys {
		nk := k + tok
		f.StaticRemap[k] = nk
		f.StaticCopyKeys[i] = nk
	}
}

// HasParamCoercion сообщает, что хотя бы у одного параметра объявлен тип
// (при вызове нужен checkParams). Пустые строки в ParamTypes типов не задают.
func (f *Function) HasParamCoercion() bool {
	if f == nil {
		return false
	}
	if !f.typesChecked {
		any := false
		for _, t := range f.ParamTypes {
			if t != "" {
				any = true
				break
			}
		}
		f.typesChecked, f.hasTypes = true, any
	}
	return f.hasTypes
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
	// staticVars — собственные static-переменные функции (имя + ключ таблицы
	// глобалов): Zend включает их в debug-информацию замыкания (bug79778).
	staticVars []StaticVarInfo
}

// StaticVarInfo описывает static-переменную функции для дампа замыкания.
type StaticVarInfo struct {
	Name string // имя без `$`
	Key  string // ключ в таблице глобалов
}

// SetStaticVars сохраняет список static-переменных функции.
func (f *CompiledFunction) SetStaticVars(v []StaticVarInfo) { f.staticVars = v }

// StaticVars возвращает static-переменные функции (для дампа замыкания).
func (f *CompiledFunction) StaticVars() []StaticVarInfo { return f.staticVars }

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

// Statics возвращает объявленные статические переменные тела функции.
func (f *CompiledFunction) Statics() ([]string, []string, []Slot) {
	if f == nil || f.chunk == nil {
		return nil, nil, nil
	}
	return f.chunk.Statics()
}
