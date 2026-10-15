package vm

import (
	"reflect"
	"sort"
	"strings"
	"unsafe"
)

// ClassInfo — интерфейс-дескриптор класса (аналог zend_class_entry): описание
// констрейнтов класса (имя, родитель, интерфейсы), операции над экземплярами
// (New/GetProp/SetProp/Call), константы/статические свойства и служебные
// lookup'ы. Класс не хранит данных экземпляра — только описание.
//
// Реализуется универсальным runtimeClass (userland и старые нативные) и
// специализированными нативными дескрипторами (dateTimeClass), которые
// приводят *Object к конкретному Go-типу.
type ClassInfo interface {
	Name() string
	Parent() ClassInfo
	Interfaces() []ClassInfo
	New(ctx *Context, args []Slot) *Object
	GetProp(o *Object, name string) (Value, bool)
	SetProp(o *Object, name string, v Value)
	Call(o *Object, ctx *Context, name string, args []Slot) (Slot, error)
	Const(name string) (Value, bool)
	Static(name string) (Slot, bool)

	// Конструирование дескриптора (компилятор и нативные классы).
	SetParent(parent ClassInfo)
	Implements(is ...ClassInfo) ClassInfo
	Use(t ClassInfo) ClassInfo
	// UseExcept примешивает трейт, пропуская методы из skip (insteadof).
	UseExcept(t ClassInfo, skip map[string]bool) ClassInfo
	// MethodFunction возвращает метод (реализацию) по имени.
	MethodFunction(name string) (*Function, bool)
	// AliasMethod регистрирует псевдоним метода (trait `as`).
	AliasMethod(alias string, fn *Function, vis Visibility) ClassInfo
	// SetMethodVis меняет видимость существующего метода (trait `as`).
	SetMethodVis(name string, vis Visibility) ClassInfo
	// IsEnum/EnumBacking — поддержка перечислений.
	IsEnum() bool
	EnumBacking() string
	// SetEnumBacking помечает класс как enum.
	SetEnumBacking(backing string)
	// AddEnumCase добавляет case перечисления (создаёт объект-синглтон).
	AddEnumCase(name string, value Value, hasValue bool, classIdx int) ClassInfo
	// AddLazyEnumCase добавляет backed-enum кейс с отложенным значением.
	AddLazyEnumCase(name string, th *Thunk, classIdx int) ClassInfo
	// EnumCases возвращает слоты case-ов в порядке объявления.
	EnumCases() []Slot
	// SetupEnum регистрирует встроенные методы enum (cases/from/tryFrom).
	SetupEnum() ClassInfo
	Declare(name string, get func(*Object) Value, set func(*Object, Value)) ClassInfo
	DeclareDefault(name string, v Value) ClassInfo
	// SetPropMeta регистрирует метаданные объявленного свойства (тип,
	// readonly, видимость). static-свойства не попадают в propOrder.
	SetPropMeta(name, typ string, readonly, static bool, vis Visibility) ClassInfo
	// SetReadonlyClass помечает класс как `readonly`.
	SetReadonlyClass() ClassInfo
	// SetNotSerializable помечает класс как запрещённый к (де)сериализации.
	SetNotSerializable() ClassInfo
	// IsNotSerializable сообщает, что (де)сериализация класса запрещена.
	IsNotSerializable() bool
	// SetMethodFinal помечает метод final (reflection).
	SetMethodFinal(name string)
	Method(name string, fn *Function, vis ...Visibility) ClassInfo
	Constructor(fn *Function, vis ...Visibility) ClassInfo
	// IsAttributeClass/MarkAttributeClass — класс объявлен `#[Attribute]`.
	IsAttributeClass() bool
	MarkAttributeClass(flags uint32) ClassInfo
	// MarkAttributeClassDynamic — флаги `#[Attribute(expr)]` вычисляются
	// лениво (thunk) при первом newInstance.
	MarkAttributeClassDynamic(th *Thunk) ClassInfo
	// CtorInfo возвращает конструктор с объявившим классом и видимостью.
	CtorInfo() (*Function, ClassInfo, Visibility)
	// AddSignature регистрирует сигнатуру метода (интерфейс / abstract function):
	// контракт без тела, видимый рефлексии, но не вызываемый как реализация.
	AddSignature(name string, vis Visibility) ClassInfo
	// SetAbstract помечает класс как abstract (нельзя инстанцировать;
	// не обязана реализовывать все унаследованные сигнатуры).
	SetAbstract()
	IsAbstract() bool
	// UnimplementedAbstract возвращает нереализованные сигнатуры (ближайшее
	// объявление в иерархии — сигнатура, конкретной реализации нет).
	UnimplementedAbstract() []string
	// UnimplementedPrivateAbstract возвращает нереализованные private
	// abstract-методы.
	UnimplementedPrivateAbstract() []string
	// hasAbstract сообщает, что ближайшее объявление метода name в иерархии
	// (родители + интерфейсы) — сигнатура, а не реализация.
	hasAbstract(name string) bool
	DefineConst(name string, v Value) ClassInfo
	// SetConstVis задаёт видимость константы (для проверок доступа).
	SetConstVis(name string, vis Visibility) ClassInfo
	// SetConstDoc задаёт doc-комментарий константы (рефлексия).
	SetConstDoc(name, doc string) ClassInfo
	DefineStatic(name string, init Value) *Slot
	// SetStaticUninit помечает типизированное статическое свойство без
	// значения как неинициализированное (чтение даёт Error, как в Zend).
	SetStaticUninit(name string)
	// Ленивые (отложенные) константные выражения; значения вычисляются при
	// первом обращении.
	DefineLazyConst(name string, th *Thunk) ClassInfo
	DeclareLazyDefault(name string, th *Thunk) ClassInfo
	DefineLazyStatic(name string, th *Thunk) *Slot

	// Диспетчеризация (внутреннее).
	lookupMethod(name string) *Function
	methodInfo(name string) (*Function, ClassInfo, Visibility)
	lookupCtor() *Function
	lookupStatic(name string) *Slot
	staticIsUninit(name string) bool
	clearStaticUninit(name string)
	lookupConst(name string) (Value, bool)
	isA(target ClassInfo) bool
	cloneObject(o *Object) *Object
	objectSlot(o *Object, idx int) Slot
	propNames(o *Object) []string
	link()
	materialize()

	// SetDeclaredTrait помечает дескриптор как объявленный через `trait`.
	SetDeclaredTrait() ClassInfo

	// Рефлексия (используется builtins пакета php).
	IsInterface() bool
	IsTrait() bool
	ParentName() string
	HasMethod(name string) bool
	MethodVisibility(name string) (Visibility, bool)
	// MethodDecl возвращает класс объявления и видимость метода name (с учётом
	// наследования) — для проверок доступа из builtins: private-метод родителя
	// не должен считаться доступным в наследнике (bug29210).
	MethodDecl(name string) (ClassInfo, Visibility, bool)
	MethodNames(caller ClassInfo) []string
	AllMethodNames() []string
	ConstNames() []string
	HasConst(name string) bool
	ConstValue(name string) (Value, bool)
	InterfaceConstValue(name string) (Value, bool)
	InstancePropertyNames() []string
	StaticPropertyNames() []string
	HasProperty(name string) bool
	DefaultValue(name string) (Value, bool)
	StaticValue(name string) (Value, bool)
	DirectInterfaces() []ClassInfo
	InterfaceNames() []string
	ParentNames() []string
	TraitNames() []string
	IsA(target ClassInfo) bool
}

// flatMethod — запись плоской (flattened) таблицы методов.
type flatMethod struct {
	fn   *Function
	decl ClassInfo
	vis  Visibility
	// proto — класс корневого прототипа метода (zend_get_function_root_class):
	// класс первого объявления в цепочке переопределений. Нужен проверке
	// protected-доступа (GH-14009): siblings через общего предка-прототип.
	proto ClassInfo
}

// propDecl — метаданные объявленного свойства (тип/readonly/видимость).
type propDecl struct {
	typ      string
	readonly bool
	vis      Visibility
	hasType  bool
	static   bool
}

// propSlotEntry — объявленное нестатическое свойство в раскладке класса.
type propSlotEntry struct {
	key  string // ключ хранения: имя или манглированный private-ключ
	pd   *propDecl
	decl *runtimeClass
}

// classSlots — раскладка слотов объявленных нестатических свойств класса.
// Индексы стабильны по иерархии: раскладка наследника = копия родительской +
// собственные свойства; перекрытие (redeclare) сохраняет родительский индекс.
type classSlots struct {
	entries []propSlotEntry
	byKey   map[string]int
	// defaults — шаблон начальных значений по индексу слота (uninit для
	// объявленных без дефолта). Массивы в шаблоне помечены shared.
	defaults []Slot
	// defErr — отложенная ошибка типа дефолта (Zend: Error при создании).
	defErr error
	// done — раскладка собрана.
	done bool
}

// layout возвращает раскладку слотов класса (строит при первом обращении).
func (c *runtimeClass) layout() *classSlots {
	if c == nil {
		return &classSlots{}
	}
	if c.layoutCache != nil && c.layoutCache.done {
		return c.layoutCache
	}
	return c.buildLayout()
}

// buildLayout собирает раскладку: слоты родителя, затем собственные
// объявленные свойства в порядке propOrder. Enum-классы исключены: их кейсы —
// синглтоны, хранящие name/value напрямую в props.
func (c *runtimeClass) buildLayout() *classSlots {
	cs := &classSlots{byKey: map[string]int{}}
	if p := c.parent; p != nil {
		ps := p.layout()
		cs.entries = append(cs.entries, ps.entries...)
		cs.defaults = append(cs.defaults, ps.defaults...)
		cs.defErr = ps.defErr
		for k, v := range ps.byKey {
			cs.byKey[k] = v
		}
	}
	if !c.isEnum {
		for _, name := range c.propOrder {
			pd := c.ownProps[name]
			if pd != nil && pd.static {
				// Статическое свойство: не слот экземпляра.
				continue
			}
			if pd == nil {
				// Свойство без метаданных (DeclareDefault у нативных классов,
				// напр. php_user_filter::$stream): слот нужен, только если есть
				// дефолт; иначе это нативный мост (Declare, Go-поле).
				_, hasDef := c.defaults[name]
				_, hasLazy := c.lazyDefaults[name]
				if !hasDef && !hasLazy {
					continue
				}
			}
			key := name
			if pd != nil && pd.vis == Private {
				key = mangledPropKey(c, name)
			}
			def := makeUninit()
			if v, ok := c.defaults[name]; ok {
				var derr error
				def, derr = c.defaultSlotValue(pd, name, v)
				if derr != nil && cs.defErr == nil {
					cs.defErr = derr
				}
			}
			if idx, ok := cs.byKey[key]; ok {
				// Перекрытие объявления предка: pd/дефолт — потомка, слот — тот же.
				cs.entries[idx].pd = pd
				cs.entries[idx].decl = c
				cs.defaults[idx] = def
				continue
			}
			idx := len(cs.entries)
			cs.byKey[key] = idx
			cs.entries = append(cs.entries, propSlotEntry{key: key, pd: pd, decl: c})
			cs.defaults = append(cs.defaults, def)
		}
	}
	cs.done = true
	c.layoutCache = cs
	return cs
}

// defaultSlotValue готовит значение дефолта для шаблона раскладки: приводит к
// объявленному типу (семантика applyDefault, strict) и помечает массивы shared.
func (c *runtimeClass) defaultSlotValue(pd *propDecl, name string, v Value) (Slot, error) {
	s := valueSlot(v)
	if c.vm != nil && pd != nil && pd.hasType {
		out, err := c.vm.coercePropValue(pd, c, name, s, true, false)
		if err != nil {
			return makeUninit(), err
		}
		s = out
	}
	if s.typ == ArrayType {
		s.AsArray().MarkShared()
	}
	return s, nil
}

// runtimeClass — универсальный дескриптор класса (userland и нативные классы,
// чьи данные описаны generic-типом RegisterClass). Поля данных экземпляра
// здесь нет: они живут в самом Go-типе объекта.
type runtimeClass struct {
	name   string
	parent *runtimeClass

	methods map[string]*Function
	ctor    *Function
	ctorVis Visibility

	staticProps map[string]*Slot
	// staticUninit — статические свойства, объявленные типом без значения
	// (чтение до присваивания — Error).
	staticUninit map[string]bool
	consts       map[string]Value
	constVis     map[string]Visibility

	newObj   func(ctx *Context, args []Slot) *Object
	cloneObj func(o *Object) *Object

	// nativeNew/nativeClone — фабрики нативного хранилища (Go-структуры T,
	// встраивающей Object). Наследуются подклассами: экземпляр userland-класса,
	// унаследованного от нативного (напр. ArrayObject), обязан выделять ту же
	// самую T, иначе unsafe-приведение `ctx.This[T]()` читает за пределами
	// аллокации. class — фактический (возможно, подкласс) дескриптор.
	nativeNew   func(class *runtimeClass) *Object
	nativeClone func(class *runtimeClass, from *Object) *Object
	// nativeType — Go-тип нативного хранилища T (структура со встроенным
	// Object первым полем). Нужен сборщику времени жизни (lifetime.go),
	// чтобы рефлексией обойти слоты внутри нативных данных и не потерять
	// ссылки на объекты. nil у чисто userland-классов.
	nativeType reflect.Type
	// hasDtor — у класса (или предка) есть __destruct; кэш для сборщика.
	// dtorChecked — значение вычислено.
	hasDtor     bool
	dtorChecked bool

	// Мост PHP-свойство <-> Go-поле: объявленное свойство имеет безопасные
	// доступоры.
	propGet map[string]func(o *Object) Value
	propSet map[string]func(o *Object, v Value)

	// ООП: интерфейсы, трейты, видимость.
	iface   bool        // это интерфейс, а не класс
	ifaces  []ClassInfo // реализуемые интерфейсы (у класса) / родительские (у интерфейса)
	methVis map[string]Visibility

	// defaults — дефолтные значения объявленных свойств.
	defaults map[string]Value

	// Ленивые (отложенные) константные выражения: константы, дефолты свойств и
	// статических свойств, вычисляемые при первом обращении в контексте класса
	// объявления. Значения кэшируются в самом Thunk.
	lazyConsts   map[string]*Thunk
	lazyDefaults map[string]*Thunk
	lazyStatics  map[string]*Thunk
	// lazyConstsResolved — все отложенные константы уже материализованы
	// (PHP вычисляет их при первом использовании класса).
	lazyConstsResolved bool

	// vm — виртуальная машина, которой принадлежит дескриптор (для вычисления
	// ленивых выражений из reflection/builtins). Устанавливается в Init.
	vm *VM

	// ownProps — метаданные свойств, объявленных непосредственно в этом классе
	// (тип, readonly, видимость). Нужны для проверок readonly/типизации.
	ownProps map[string]*propDecl
	// layoutCache — раскладка слотов объявленных нестатических свойств
	// (см. classSlots). Строится при первом создании объекта класса.
	layoutCache *classSlots
	// privKeys — предвычисленные ключи хранения private-свойств этого класса
	// ("\x00Класс\x00имя"). Сборка строки на каждом обращении к свойству
	// аллоцировала (2M чтений $this->x -> 2M аллокаций), поэтому ключи считаются
	// один раз при линковке класса.
	privKeys map[string]string
	// readonlyClass — класс объявлен `readonly` (все свойства readonly,
	// динамические свойства запрещены).
	readonlyClass bool

	// notSerializable — (де)сериализация запрещена (SplFileInfo, анонимные
	// классы, Generator и т.п.). Наследуется подклассами.
	notSerializable bool

	// Порядок объявления членов (карты Go неупорядочены).
	methodOrder []string
	constOrder  []string
	propOrder   []string
	staticOrder []string

	// usedTraits — трейты, примешанные через Use (для class_uses).
	usedTraits []ClassInfo

	// isTrait — дескриптор был примешан как трейт хотя бы одним классом.
	isTrait bool
	// declTrait — дескриптор объявлен ключевым словом `trait`.
	declTrait bool

	// hidden — дескриптор построен компилятором для объявления, находящегося
	// вне верхнего уровня (в теле функции/замыкания, под условием и т.п.).
	// Такой класс не виден по имени, пока не выполнится OpDeclareClass.
	// Позволяет отложить объявление до точки выполнения.
	hidden bool
	// declaring — объявление класса сейчас выполняется (OpDeclareClass):
	// для отложенных проверок совместимости такой класс виден как
	// «почти слинкованный» (Zend ALLOW_UNLINKED).
	declaring bool
	// checkData — план отложенных проверок совместимости сигнатур (php-пакет).
	checkData any
	// variancePending — у класса остались неразрешённые проверки
	// совместимости (Zend ZEND_ACC_UNRESOLVED_VARIANCE/NEARLY_LINKED).
	variancePending bool
	// deps — родитель/интерфейсы, у которых на момент объявления этого класса
	// оставались неразрешённые проверки (Zend dependency obligation): их
	// проверки разрешаются в конце объявления потомка.
	deps []ClassInfo
	// missingParent — имя родителя, неизвестного на этапе компиляции; при
	// рантайм-объявлении разрешается (с автозагрузкой) и линкуется.
	missingParent string
	// missingIfaces — имена интерфейсов, неизвестных на этапе компиляции;
	// разрешаются при рантайм-объявлении.
	missingIfaces []string

	// abstract — класс объявлен `abstract` (не инстанцируется).
	abstract bool

	// final — класс объявлен `final` или является нативным final-классом
	// (напр. Generator): его нельзя наследовать.
	final bool
	// noDynProps — класс запрещает создание динамических свойств
	// (ZEND_ACC_NO_DYNAMIC_PROPERTIES).
	noDynProps bool
	// noAppend — класс запрещает append (`$x[] = v` / `$x[][k] = v`);
	// сообщение ошибки — "Cannot append to <Class>" (WeakMap).
	noAppend bool

	// --- Рефлексия ---
	// internal — встроенный (нативный) класс, а не объявленный в userland
	// (ReflectionClass::isInternal/isUserDefined).
	internal bool
	// defFile/defStart/defEnd — расположение объявления класса (для
	// ReflectionClass::getFileName/getStartLine/getEndLine). Для встроенных пусты.
	defFile  string
	defStart int
	defEnd   int
	// docComment — doc-комментарий перед объявлением класса.
	docComment string
	// constDoc — doc-комментарии констант по именам (для getDocComment()).
	constDoc map[string]string
	// methFinal — методы, помеченные final (в т.ч. через `as final`).
	methFinal map[string]bool
	// attrs — атрибуты (#[...]), объявленные на классе.
	attrs []AttributeInfo
	// attrClass — класс помечен `#[Attribute]`; attrFlags — допустимые цели
	// (ZEND_ATTRIBUTE_TARGET_*), IS_REPEATABLE хранится отдельным битом.
	attrClass      bool
	attrFlags      uint32
	attrFlagsThunk *Thunk
	// constAttrs/methAttrs/propAttrs — атрибуты членов по имени.
	constAttrs map[string][]AttributeInfo
	methAttrs  map[string][]AttributeInfo
	propAttrs  map[string][]AttributeInfo
	// paramAttrs — атрибуты параметров методов: метод -> индекс -> атрибуты.
	paramAttrs map[string]map[int][]AttributeInfo

	// --- Enum ---
	isEnum        bool
	enumBacking   string
	enumCaseSlots []Slot
	enumByName    map[string]Slot
	enumByValue   map[string]Slot
	enumCaseLazy  map[string]*Thunk
	// enumCaseErrType/enumCaseErrMsg — отложенная ошибка enum-кейса
	// (дубликат значения или несоответствие backing-типу), которая
	// поднимается при прямом обращении к кейсу и в from()/tryFrom() (Zend).
	enumCaseErrType string
	enumCaseErrMsg  string

	// sigs — сигнатуры методов без тела (интерфейс / abstract function):
	// контракт, видимый рефлексии, но не вызываемый. sigOrder — порядок
	// объявления (для get_class_methods).
	sigs     map[string]Visibility
	sigOrder []string
	// sigFns — метаданные сигнатур (параметры/тип возврата) для рефлексии.
	sigFns map[string]*Function

	// Плоские таблицы, собранные link() (этап рефакторинга: убрать обход
	// Parent() в рантайме). nil, пока класс не слинкован.
	linked     bool
	allMethods map[string]flatMethod
	allConsts  map[string]Value
	allStatics map[string]*Slot
	// Плоские таблицы ленивых констант/статиков (значения вычисляются при
	// первом обращении).
	allLazyConsts  map[string]*Thunk
	allLazyStatics map[string]*Thunk
	allConstVis    map[string]Visibility
	allConstDecl   map[string]ClassInfo
	ancestors      map[ClassInfo]bool
}

var _ ClassInfo = (*runtimeClass)(nil)

// HideClass помечает дескриптор как объявляемый в рантайме (OpDeclareClass):
// до выполнения объявления он не виден по имени и не участвует в автозагрузке.
func HideClass(ci ClassInfo) {
	if rc, ok := ci.(*runtimeClass); ok {
		rc.hidden = true
	}
}

// ClassHidden сообщает, что дескриптор ещё не объявлен в рантайме.
func ClassHidden(ci ClassInfo) bool {
	if rc, ok := ci.(*runtimeClass); ok {
		return rc.hidden
	}
	return false
}

// SetDisabledClasses задаёт список INI disable_classes (имена классов,
// регистр не важен). Пустой список снимает запрет.
func (vm *VM) SetDisabledClasses(names []string) {
	if len(names) == 0 {
		vm.disabledClasses = nil
		return
	}
	m := make(map[string]bool, len(names))
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			m[strings.ToLower(n)] = true
		}
	}
	if len(m) == 0 {
		vm.disabledClasses = nil
		return
	}
	vm.disabledClasses = m
}

// SetDisabledFunctions применяет INI disable_functions: Zend удаляет такие
// внутренние функции из таблицы (вызов даёт Error "Call to undefined
// function", function_exists/is_callable — false). Пользовательские функции с
// теми же именами не затрагиваются.
func (vm *VM) SetDisabledFunctions(names []string) {
	if len(names) == 0 {
		return
	}
	set := map[string]bool{}
	for _, n := range names {
		if n = strings.ToLower(strings.TrimSpace(n)); n != "" {
			set[n] = true
		}
	}
	if len(set) == 0 {
		return
	}
	for _, f := range vm.functions {
		if f == nil || f.Disabled {
			continue
		}
		name := strings.ToLower(f.Name)
		if !set[name] {
			continue
		}
		if _, isNative := f.Executable.(*NativeFunction); !isNative {
			// Пользовательская функция с именем отключённой встроенной.
			continue
		}
		f.Disabled = true
		display := f.Name
		f.NumArgs = 0
		f.Executable = &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			return Slot{}, NewThrowable("Error", "Call to undefined function "+display+"()")
		}}
	}
	vm.funcIndex = nil
}

// classDisabled сообщает, что класс (или его предок) в disable_classes.
func (vm *VM) classDisabled(ci ClassInfo) bool {
	if len(vm.disabledClasses) == 0 || ci == nil {
		return false
	}
	for c := ci; c != nil; c = c.Parent() {
		if vm.disabledClasses[strings.ToLower(c.Name())] {
			return true
		}
	}
	return false
}

// warnDisabledClass печатает предупреждение Zend при new запрещённого класса
// (объект всё равно создаётся обычным образом).
func (vm *VM) warnDisabledClass(ci ClassInfo) {
	if vm.classDisabled(ci) {
		vm.Warning(ci.Name() + "() has been disabled for security reasons")
	}
}

// SetClassMissingParent запоминает имя родителя, который должен быть разрешён
// при рантайм-объявлении класса (с автозагрузкой).
func SetClassMissingParent(ci ClassInfo, name string) {
	if rc, ok := ci.(*runtimeClass); ok {
		rc.missingParent = name
	}
}

// AddClassMissingInterface запоминает имя интерфейса, который должен быть
// разрешён при рантайм-объявлении класса.
func AddClassMissingInterface(ci ClassInfo, name string) {
	if rc, ok := ci.(*runtimeClass); ok {
		rc.missingIfaces = append(rc.missingIfaces, name)
	}
}

// Thunk — отложенное константное выражение: скомпилированная функция без
// аргументов (fn) вместе с классом объявления (self). Вычисляется лениво при
// первом обращении и кэширует результат. busy защищает от бесконечной
// рекурсии (self-referencing constant).
type Thunk struct {
	fn   *Function
	self ClassInfo
	done bool
	busy bool
	val  Value
	err  error
	// TypeConstr/Label — для типизированных констант класса: проверка типа
	// вычисленного значения при доступе.
	TypeConstr string
	Label      string
}

// NewThunk создаёт отложенное выражение. self — класс объявления (scope self).
func NewThunk(fn *Function, self ClassInfo) *Thunk {
	return &Thunk{fn: fn, self: self}
}

// Scope возвращает класс объявления (для перепривязки при mixin трейтов).
func (t *Thunk) Scope() ClassInfo { return t.self }

// SetScope перепривязывает класс объявления (используется при копировании
// членов трейта в использующий класс).
func (t *Thunk) SetScope(self ClassInfo) { t.self = self }

// valueSlot конвертирует Value (ABI-границу) в Slot.
func valueSlot(v Value) Slot {
	if v == nil {
		return MakeNull()
	}
	if s, ok := v.(Slot); ok {
		return s
	}
	var dst Slot
	SlotOf(&dst, v)
	return dst
}

// appendUnique добавляет name в *dst, если его там ещё нет.
func appendUnique(dst *[]string, name string) {
	for _, s := range *dst {
		if s == name {
			return
		}
	}
	*dst = append(*dst, name)
}

func newClassInfo(name string, parent *runtimeClass) *runtimeClass {
	return &runtimeClass{
		name:    name,
		parent:  parent,
		methods: map[string]*Function{},
		consts:  map[string]Value{},
	}
}

// NewClass создаёт дескриптор PHP-класса (данные класса пусты).
func NewClass(name string, parent ClassInfo) *runtimeClass {
	p, _ := parent.(*runtimeClass)
	ci := newClassInfo(name, p)
	// Наследуем нативное хранилище ближайшего нативного предка: подкласс
	// нативного класса (ArrayObject, DateTime, …) должен выделять ту же
	// Go-структуру T, иначе `ctx.This[T]()` небезопасен.
	if p != nil {
		ci.nativeNew = p.nativeNew
		ci.nativeClone = p.nativeClone
		ci.nativeType = p.nativeType
	}
	ci.newObj = func(ctx *Context, args []Slot) *Object {
		var o *Object
		if ci.nativeNew != nil {
			o = ci.nativeNew(ci)
		} else {
			o = &Object{class: ci}
		}
		o.incomplete = ci.Name() == inactiveClassName
		ci.applyDefaults(o)
		// Исключения фиксируют file/line в момент создания объекта (Zend):
		// даже если пользовательский __construct не вызывает parent.
		if ctx != nil && ctx.vm != nil && isThrowableClass(ci) {
			o.excFile = ctx.vm.curFile
			o.excLine = ctx.vm.line()
		}
		return o
	}
	ci.cloneObj = func(o *Object) *Object {
		if o == nil {
			return nil
		}
		var d *Object
		if ci.nativeClone != nil {
			d = ci.nativeClone(ci, o)
		} else {
			d = &Object{class: ci}
		}
		d.incomplete = o.incomplete
		d.slots = cloneSlots(o.slots)
		d.props = cloneProps(o.props)
		d.unset = cloneBoolMap(o.unset)
		markReadonlyReinit(ci, d)
		return d
	}
	return ci
}

// isThrowableClass сообщает, что класс — Throwable (Exception/Error и потомки).
func isThrowableClass(ci ClassInfo) bool {
	for k := ci; k != nil; k = k.Parent() {
		switch k.Name() {
		case "Throwable", "Exception", "Error":
			return true
		}
	}
	return false
}

// markReadonlyReinit помечает readonly-свойства клона как допускающие ровно
// одну переинициализацию (PHP 8.3: в __clone первая запись в readonly
// свойство разрешена).
func markReadonlyReinit(c *runtimeClass, d *Object) {
	if c == nil || d == nil {
		return
	}
	for k := c; k != nil; k = k.parent {
		for name, pd := range k.ownProps {
			if pd.readonly {
				key := name
				if pd.vis == Private {
					key = mangledPropKey(k, name)
				}
				if d.declaredInitialized(key) {
					d.AllowReadonlyReinit(key)
				}
			}
		}
	}
}

// cloneProps делает поверхностную копию карты свойств (для клонирования
// объектов и разделения props между экземплярами).
func cloneProps(src map[string]Value) map[string]Value {
	if src == nil {
		return nil
	}
	dst := make(map[string]Value, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// cloneSlots копирует слоты объявленных свойств (клонирование объекта):
// значения общие (ссылки/массивы COW), слайс — собственный.
func cloneSlots(src []Slot) []Slot {
	if len(src) == 0 {
		return nil
	}
	dst := make([]Slot, len(src))
	copy(dst, src)
	return dst
}

// cloneBoolMap копирует карту меток unset.
func cloneBoolMap(src map[string]bool) map[string]bool {
	if src == nil {
		return nil
	}
	dst := make(map[string]bool, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// RegisterClass регистрирует нативный класс, данные экземпляра которого — Go-
// структура T, встраивающая Object первым полем (как DateTime и Spl*). При
// наследовании нативные типы встраивают друг друга, поэтому шапка Object и
// старшие поля по-прежнему лежат по offset 0.
func RegisterClass[T any](name string, parent ClassInfo) *runtimeClass {
	p, _ := parent.(*runtimeClass)
	ci := newClassInfo(name, p)
	ci.nativeType = reflect.TypeOf((*T)(nil)).Elem()
	ci.nativeNew = func(class *runtimeClass) *Object {
		t := new(T)
		o := (*Object)(unsafe.Pointer(t))
		o.class = class
		return o
	}
	ci.nativeClone = func(class *runtimeClass, from *Object) *Object {
		src := (*T)(unsafe.Pointer(from))
		nd := new(T)
		*nd = *src
		no := (*Object)(unsafe.Pointer(nd))
		no.class = class
		return no
	}
	ci.newObj = func(ctx *Context, args []Slot) *Object {
		return ci.nativeNew(ci)
	}
	ci.cloneObj = func(o *Object) *Object {
		if o == nil {
			return nil
		}
		no := ci.nativeClone(ci, o)
		no.slots = cloneSlots(o.slots)
		no.props = cloneProps(o.props)
		no.unset = cloneBoolMap(o.unset)
		markReadonlyReinit(ci, no)
		return no
	}
	return ci
}

// --- Реализация интерфейса ClassInfo ---

func (c *runtimeClass) Name() string { return c.name }

// SetFinal помечает класс как final.
func (c *runtimeClass) SetFinal() { c.final = true }

// IsFinalClass сообщает, что дескриптор — final-класс (userland `final` или
// нативный final, напр. Generator).
func IsFinalClass(ci ClassInfo) bool {
	if rc, ok := ci.(*runtimeClass); ok {
		return rc.final
	}
	return false
}

func (c *runtimeClass) Parent() ClassInfo {
	if c.parent == nil {
		return nil
	}
	return c.parent
}

func (c *runtimeClass) Interfaces() []ClassInfo {
	return append([]ClassInfo(nil), c.ifaces...)
}

func (c *runtimeClass) New(ctx *Context, args []Slot) *Object {
	if c.newObj == nil {
		return nil
	}
	return c.newObj(ctx, args)
}

func (c *runtimeClass) objectSlot(o *Object, idx int) Slot {
	if o == nil {
		return MakeNull()
	}
	// Кейсы enum — объекты-синглтоны: в Zend они не занимают PHP-хендл в момент
	// объявления перечисления, а получают #N лишь при первом обращении. Поэтому
	// здесь хендл не выдаётся (см. markEnumCaseVal / markAllEnumCases).
	if c.vm != nil && !c.isEnum {
		c.vm.markObj(o)
	}
	return Slot{typ: ObjectType, num: uint64(idx), ptr: unsafe.Pointer(o)}
}

// markObj присваивает объекту PHP-хендл (#N), если он ещё не задан.
// Хендл берётся из free-list уничтоженных объектов (как в Zend) либо
// выделяется следующим номером.
func (vm *VM) markObj(o *Object) {
	if o == nil || o.objID != 0 || o.dead {
		return
	}
	if n := len(vm.freeObjIDs); n > 0 {
		o.objID = vm.freeObjIDs[n-1]
		vm.freeObjIDs = vm.freeObjIDs[:n-1]
	} else {
		vm.nextObjID++
		o.objID = vm.nextObjID
	}
	if o.refs <= 0 {
		vm.zeroObjs++
	}
	vm.liveObjs = append(vm.liveObjs, o)
}

// UnmarkNoDestruct снимает пометку MarkNoDestruct (успешно построенный
// объект: конструктор не бросил исключение).
func (vm *VM) UnmarkNoDestruct(o *Object) {
	if o == nil {
		return
	}
	delete(vm.noDtor, o)
}

// MarkNoDestruct помечает объект как не подлежащий вызову __destruct при
// завершении скрипта (объект, оставшийся от неуспешного unserialize()).
func (vm *VM) MarkNoDestruct(o *Object) {
	if o == nil {
		return
	}
	if vm.noDtor == nil {
		vm.noDtor = map[*Object]bool{}
	}
	vm.noDtor[o] = true
}

// markEnumCaseVal выдаёт PHP-хендл объекту-кейсу enum, если переданное значение
// — такой кейс и хендл ещё не выдан (ленивая материализация при обращении).
func (vm *VM) markEnumCaseVal(v Value) {
	s, ok := v.(Slot)
	if !ok {
		return
	}
	o := vm.objectOf(s)
	if o == nil {
		return
	}
	if rc, ok := o.class.(*runtimeClass); ok && rc.isEnum {
		vm.markObj(o)
	}
}

// markAllEnumCases выдаёт хендлы всем кейсам перечисления в порядке объявления
// (Zend материализует их при вызове cases()/from()/tryFrom()/рефлексии).
func (c *runtimeClass) markAllEnumCases() {
	if c == nil || c.vm == nil {
		return
	}
	for _, s := range c.enumCaseSlots {
		if o := s.obj(); o != nil {
			c.vm.markObj(o)
		}
	}
}

// markClosure присваивает замыканию PHP-хендл (#N), если он ещё не задан.
// Замыкания — объекты в Zend, поэтому занимают общий счётчик хендлов.
func (vm *VM) markClosure(c *Closure) {
	if c == nil || c.objID != 0 {
		return
	}
	vm.nextObjID++
	c.objID = vm.nextObjID
}

func (c *runtimeClass) cloneObject(o *Object) *Object {
	if o == nil || c.cloneObj == nil {
		return nil
	}
	return c.cloneObj(o)
}

// GetProp — handlers чтения: объявленное свойство (нативный мост или слот),
// иначе props.
func (c *runtimeClass) GetProp(o *Object, name string) (Value, bool) {
	if f := c.findPropGet(name); f != nil {
		return f(o), true
	}
	if o == nil {
		return nil, false
	}
	if s, ok := o.slotValue(name); ok {
		return s, true
	}
	if _, declared := o.slotIndex(name); declared {
		// Объявленное, но неинициализированное/снятое unset(): как отсутствие
		// в props-модели (вызывающий уходит в __get/uninit-диагностику).
		return nil, false
	}
	if v, ok := o.props[name]; ok {
		return v, ok
	}
	// Отложенное значение backed-enum кейса.
	if name == "value" && c.isEnum {
		if v, ok := c.resolveEnumValue(o); ok {
			return v, true
		}
	}
	return nil, false
}

// SetProp — handlers записи: нативный мост, объявленный слот, иначе props.
func (c *runtimeClass) SetProp(o *Object, name string, v Value) {
	if f := c.findPropSet(name); f != nil {
		f(o, v)
		return
	}
	if o == nil {
		return
	}
	if idx, ok := o.slotIndex(name); ok {
		if idx >= len(o.slots) {
			o.ensureSlots(c)
		}
		s := valueSlot(v)
		old := Slot{}
		if idx < len(o.slots) {
			old = o.slots[idx]
		}
		if old.typ != slotUninit && c.vm != nil {
			c.vm.noteDropped(old)
			c.vm.noteStored(s)
		} else if c.vm != nil {
			c.vm.noteStored(s)
		}
		o.storeDeclaredSlot(idx, s)
		return
	}
	if o.props == nil {
		o.props = map[string]Value{}
	}
	if old, exists := o.props[name]; exists {
		if c.vm != nil {
			c.vm.noteDroppedValue(old)
			c.vm.noteStoredValue(v)
		}
	} else {
		o.dynOrder = append(o.dynOrder, name)
		if c.vm != nil {
			c.vm.noteStoredValue(v)
		}
	}
	o.props[name] = v
}

func (c *runtimeClass) Call(o *Object, ctx *Context, name string, args []Slot) (Slot, error) {
	m, decl, vis := c.methodInfo(name)
	if m == nil {
		return Slot{}, errUndefinedMethod
	}
	if !canAccess(vis, decl, ctx.self) {
		return Slot{}, errAccess
	}
	if ctx.vm == nil {
		return Slot{}, errNotObject
	}
	return ctx.vm.invokeMethod(m, ctx.vm.slotOf(o), c, args)
}

// Const возвращает константу с учётом наследования.
func (c *runtimeClass) Const(name string) (Value, bool) {
	return c.lookupConst(name)
}

// Static возвращает слот статического свойства с учётом наследования.
func (c *runtimeClass) Static(name string) (Slot, bool) {
	if s := c.lookupStatic(name); s != nil {
		return *s, true
	}
	return Slot{}, false
}

func (c *runtimeClass) SetParent(parent ClassInfo) {
	p, _ := parent.(*runtimeClass)
	c.parent = p
	// Наследуем нативное хранилище ближайшего нативного предка (компилятор
	// создаёт дескриптор до назначения родителя, поэтому делаем это здесь).
	if p != nil && c.nativeNew == nil {
		c.nativeNew = p.nativeNew
		c.nativeClone = p.nativeClone
	}
}

func (c *runtimeClass) materialize() {
	for k := c; k != nil; k = k.parent {
		for _, m := range k.methods {
			if cf, ok := m.Executable.(*CompiledFunction); ok {
				cf.chunk.materialize()
			}
		}
		if k.ctor != nil {
			if cf, ok := k.ctor.Executable.(*CompiledFunction); ok {
				cf.chunk.materialize()
			}
		}
		for _, th := range k.lazyConsts {
			th.materialize()
		}
		for _, th := range k.lazyDefaults {
			th.materialize()
		}
		for _, th := range k.lazyStatics {
			th.materialize()
		}
		for _, th := range k.enumCaseLazy {
			th.materialize()
		}
	}
}

// materialize подготавливает чанк отложенного выражения к исполнению.
func (t *Thunk) materialize() {
	if t == nil || t.fn == nil {
		return
	}
	if cf, ok := t.fn.Executable.(*CompiledFunction); ok {
		cf.chunk.materialize()
	}
}

// link собирает плоские таблицы (методы/константы/статики/предки) рекурсивно,
// родитель до потомка. После link рантайм-поиск — O(1), без обхода Parent().
func (c *runtimeClass) link() {
	if c == nil || c.linked {
		return
	}
	c.linked = true
	if c.parent != nil {
		c.parent.link()
	}
	for _, i := range c.ifaces {
		if r, ok := i.(*runtimeClass); ok {
			r.link()
		}
	}

	am := map[string]flatMethod{}
	ac := map[string]Value{}
	acv := map[string]Visibility{}
	acd := map[string]ClassInfo{}
	as := map[string]*Slot{}
	alc := map[string]*Thunk{}
	als := map[string]*Thunk{}
	anc := map[ClassInfo]bool{}
	if c.parent != nil {
		for k, v := range c.parent.allMethods {
			am[k] = v
		}
		for k, v := range c.parent.allConsts {
			// Приватные константы родителя не наследуются.
			if c.parent.allConstVis[k] == Private {
				continue
			}
			ac[k] = v
			acv[k] = c.parent.allConstVis[k]
			acd[k] = c.parent.allConstDecl[k]
		}
		for k, v := range c.parent.allStatics {
			as[k] = v
		}
		for k, v := range c.parent.allLazyConsts {
			if c.parent.allConstVis[k] == Private {
				continue
			}
			alc[k] = v
		}
		for k, v := range c.parent.allLazyStatics {
			als[k] = v
		}
		anc[c.parent] = true
		for k := range c.parent.ancestors {
			anc[k] = true
		}
	}
	for _, i := range c.ifaces {
		anc[i] = true
		if r, ok := i.(*runtimeClass); ok {
			for k := range r.ancestors {
				anc[k] = true
			}
			// Константы интерфейса наследуются реализующим классом
			// (class C implements I { } -> C::I_CONST).
			for k, v := range r.allConsts {
				if _, exists := ac[k]; !exists {
					ac[k] = v
					acv[k] = r.allConstVis[k]
					acd[k] = r.allConstDecl[k]
				}
			}
			for k, v := range r.allLazyConsts {
				if _, exists := alc[k]; !exists {
					alc[k] = v
				}
			}
		}
	}
	for name, fn := range c.methods {
		vis := Public
		if c.methVis != nil {
			if v, ok := c.methVis[name]; ok {
				vis = v
			}
		}
		fm := flatMethod{fn: fn, decl: c, vis: vis}
		// Переопределение унаследованного метода: запоминаем корневой
		// прототип (Zend GH-14009 — проверка protected-доступа идёт по нему).
		if old, ok := am[name]; ok && old.fn != fn {
			if old.proto != nil {
				fm.proto = old.proto
			} else {
				fm.proto = old.decl
			}
		} else if ln := strings.ToLower(name); ln != name {
			if old, ok := am[ln]; ok && old.fn != fn {
				if old.proto != nil {
					fm.proto = old.proto
				} else {
					fm.proto = old.decl
				}
			}
		}
		am[name] = fm
	}
	for k, v := range c.consts {
		ac[k] = v
		acv[k] = c.constVis[k]
		acd[k] = c
	}
	for k, v := range c.lazyConsts {
		alc[k] = v
		acv[k] = c.constVis[k]
		acd[k] = c
	}
	for k, v := range c.staticProps {
		as[k] = v
	}
	for k, v := range c.lazyStatics {
		als[k] = v
	}
	c.allMethods = am
	c.allConsts = ac
	c.allStatics = as
	c.allLazyConsts = alc
	c.allLazyStatics = als
	c.allConstVis = acv
	c.allConstDecl = acd
	c.ancestors = anc
	c.buildPrivKeys()
}

// buildPrivKeys предвычисляет манглированные ключи private-свойств класса
// ("\x00Класс\x00имя"): иначе строка собирается на каждом обращении к свойству
// (2M чтений $this->x давали 2M аллокаций — регрессия MethodGetter2M).
func (c *runtimeClass) buildPrivKeys() {
	if c == nil {
		return
	}
	for name, pd := range c.ownProps {
		if pd == nil || pd.vis != Private || pd.static {
			continue
		}
		if c.privKeys == nil {
			c.privKeys = make(map[string]string, len(c.ownProps))
		}
		c.privKeys[name] = "\x00" + c.name + "\x00" + name
	}
}

// Declare объявляет свойство класса с доступорами к данным (мост PHP <-> Go).
func (c *runtimeClass) Declare(name string, get func(o *Object) Value, set func(o *Object, v Value)) ClassInfo {
	if c.propGet == nil {
		c.propGet = map[string]func(*Object) Value{}
		c.propSet = map[string]func(*Object, Value){}
	}
	if get != nil {
		c.propGet[name] = get
	}
	if set != nil {
		c.propSet[name] = set
	}
	appendUnique(&c.propOrder, name)
	return c
}

// DeclareDefault объявляет свойство с дефолтным значением (public $x = 1).
func (c *runtimeClass) DeclareDefault(name string, v Value) ClassInfo {
	if c.defaults == nil {
		c.defaults = map[string]Value{}
	}
	c.defaults[name] = v
	appendUnique(&c.propOrder, name)
	return c
}

// SetCheckData прикрепляет план отложенных проверок совместимости сигнатур
// (variance obligations), подготовленный компилятором.
func (c *runtimeClass) SetCheckData(v any) { c.checkData = v }

// SetPropMeta регистрирует метаданные объявленного свойства.
func (c *runtimeClass) SetPropMeta(name, typ string, readonly, static bool, vis Visibility) ClassInfo {
	if c.ownProps == nil {
		c.ownProps = map[string]*propDecl{}
	}
	c.ownProps[name] = &propDecl{typ: typ, readonly: readonly, vis: vis, hasType: typ != "", static: static}
	if !static {
		appendUnique(&c.propOrder, name)
	}
	return c
}

// SetPropTypeOnly регистрирует метаданные типа свойства, НЕ добавляя его в
// propOrder (используется для встроенных классов, чьи свойства моделируются
// динамически, но должны проверяться на тип при unserialize).
func (c *runtimeClass) SetPropTypeOnly(name, typ string, vis Visibility) {
	if c.ownProps == nil {
		c.ownProps = map[string]*propDecl{}
	}
	c.ownProps[name] = &propDecl{typ: typ, vis: vis, hasType: typ != ""}
}

// SetReadonlyClass помечает класс как `readonly`.
func (c *runtimeClass) SetReadonlyClass() ClassInfo {
	c.readonlyClass = true
	return c
}

// SetNotSerializable помечает класс как запрещённый к (де)сериализации.
func (c *runtimeClass) SetNotSerializable() ClassInfo {
	c.notSerializable = true
	return c
}

// SetNoDynamicProps помечает класс как запрещающий динамические свойства
// (ZEND_ACC_NO_DYNAMIC_PROPERTIES): запись необъявленного свойства — Error,
// а не Deprecated (Closure, Generator, Fiber, WeakMap, SensitiveParameterValue).
func (c *runtimeClass) SetNoDynamicProps() ClassInfo {
	c.noDynProps = true
	return c
}

// SetNoAppend помечает класс как запрещающий append к себе (WeakMap):
// `$x[] = v` и `$x[][k] = v` дают "Cannot append to <Class>".
func (c *runtimeClass) SetNoAppend() ClassInfo {
	c.noAppend = true
	return c
}

// NoDynamicProps сообщает, что класс (или предок) запрещает динамические
// свойства.
func (c *runtimeClass) NoDynamicProps() bool {
	for k := c; k != nil; k = k.parent {
		if k.noDynProps {
			return true
		}
	}
	return false
}

// IsNotSerializable сообщает, что (де)сериализация класса запрещена (флаг
// наследуется по цепочке родителей).
func (c *runtimeClass) IsNotSerializable() bool {
	for k := c; k != nil; k = k.parent {
		if k.notSerializable {
			return true
		}
	}
	return false
}

// propInfoForScope находит метаданные свойства name для операции из scope
// над объектом класса obj. Поиск начинается со scope (compile-time cache
// Zend), затем — с класса объекта. Приватное объявление предка НЕ видно его
// потомкам (Zend: доступ уходит в динамический путь, «Undefined property»).
// Возвращает дескриптор и класс объявления.
func propInfoForScope(scope, obj ClassInfo, name string) (*propDecl, ClassInfo, bool) {
	// Объявления scope-класса учитываются, только если объект — его экземпляр
	// (compile-time предположение Zend для $this/типизированного приёмника).
	// Иначе имя динамическое и ищется в классе объекта: метод класса Id не
	// «объявляет» свои private-свойства на чужом stdClass (Bug #24499).
	if rc, ok := asRuntimeClass(scope); ok && rc != nil && obj != nil {
		if oc, ok2 := asRuntimeClass(obj); !ok2 || !oc.isA(rc) {
			scope = nil
		}
	}
	if rc, ok := scope.(*runtimeClass); ok && rc != nil {
		for k := rc; k != nil; k = k.parent {
			if pd, ok := k.ownProps[name]; ok && !pd.static {
				if pd.vis == Private && k != rc {
					continue
				}
				return pd, k, true
			}
		}
	}
	if rc, ok := asRuntimeClass(obj); ok && rc != nil {
		for k := rc; k != nil; k = k.parent {
			if pd, ok := k.ownProps[name]; ok && !pd.static {
				if pd.vis == Private && k != rc {
					continue
				}
				return pd, k, true
			}
		}
	}
	return nil, nil, false
}

// mangledPropKey — ключ хранения private-свойства: "\x00Класс\x00имя"
// (формат совпадает с splitMangledProp и сериализацией Zend). Ключ
// предвычислен при линковке класса (runtimeClass.privKeys); для незнакомых
// имён — та же сборка строки, что и раньше.
func mangledPropKey(decl ClassInfo, name string) string {
	if rc, ok := asRuntimeClass(decl); ok && rc != nil {
		if k, ok := rc.privKeys[name]; ok {
			return k
		}
	}
	return "\x00" + decl.Name() + "\x00" + name
}

// scopedProp — кэширующий резолв свойства (scope,obj,name)->(pd,decl,ok).
// Горячий доступ к $this->x в цикле иначе каждый раз обходит иерархию классов
// (isA по ancestors + поиск в ownProps).
func (vm *VM) scopedProp(scope, obj ClassInfo, name string) (*propDecl, ClassInfo, bool) {
	if vm.scopedValid && vm.scopedScope == scope && vm.scopedObj == obj && vm.scopedName == name {
		return vm.scopedPd, vm.scopedDecl, vm.scopedOk
	}
	pd, decl, ok := propInfoForScope(scope, obj, name)
	vm.scopedValid = true
	vm.scopedScope, vm.scopedObj, vm.scopedName = scope, obj, name
	vm.scopedPd, vm.scopedDecl, vm.scopedOk = pd, decl, ok
	vm.scopedIdx = -1
	if ok && pd != nil && !pd.static {
		key := name
		if pd.vis == Private {
			key = mangledPropKey(decl, name)
		}
		vm.scopedKey = key
		if rc, ok2 := asRuntimeClass(decl); ok2 && rc != nil {
			if i, ok3 := rc.layout().byKey[key]; ok3 {
				vm.scopedIdx = i
			}
		}
	}
	return pd, decl, ok
}

// scopedSlot возвращает результаты резолва свойства вместе с ключом хранения и
// индексом слота (idx = -1 — свойство хранится не в слоте: динамическое,
// нативный мост, enum). Использует кэш scopedProp.
func (vm *VM) scopedSlot(ci ClassInfo, name string) (*propDecl, ClassInfo, string, int, bool) {
	pd, decl, ok := vm.scopedProp(vm.ctx.self, ci, name)
	if !ok || pd == nil || pd.static {
		return pd, decl, "", -1, false
	}
	return pd, decl, vm.scopedKey, vm.scopedIdx, true
}

// propKey возвращает ключ хранения свойства: для private — предвычисленный
// манглированный ключ, иначе — обычное имя. Последний ключ кэшируется в VM:
// доступ к свойству в цикле не должен ни собирать строку, ни ходить в map.
func (vm *VM) propKey(pd *propDecl, decl ClassInfo, name string) string {
	if pd == nil || pd.vis != Private || decl == nil {
		return name
	}
	if vm.propKeyDecl == decl && vm.propKeyName == name {
		return vm.propKeyVal
	}
	k := mangledPropKey(decl, name)
	vm.propKeyDecl, vm.propKeyName, vm.propKeyVal = decl, name, k
	return k
}

// propKeyForScope возвращает ключ хранения свойства name над объектом obj из
// области видимости scope: для private — манглированный по классу объявления,
// иначе — обычное имя.
func propKeyForScope(scope, obj ClassInfo, name string) string {
	if pd, decl, ok := propInfoForScope(scope, obj, name); ok && pd != nil && pd.vis == Private && decl != nil {
		return mangledPropKey(decl, name)
	}
	return name
}

// asRuntimeClass извлекает *runtimeClass из дескриптора (учёт нативных
// обёрток, встраивающих runtimeClass).
func asRuntimeClass(ci ClassInfo) (*runtimeClass, bool) {
	switch c := ci.(type) {
	case *runtimeClass:
		return c, c != nil
	case runtimeCarrier:
		return c.embeddedRuntimeClass(), true
	}
	return nil, false
}

// runtimeCarrier — дескриптор-обёртка (DateTime/DateTimeImmutable) поверх
// runtimeClass: рефлексия должна видеть методы/константы вложенного класса
// (029_reflect_internal_symbols: DateTime::__construct).
type runtimeCarrier interface {
	embeddedRuntimeClass() *runtimeClass
}

// PropVisibility возвращает видимость объявленного свойства name в ci и
// признак «свойство объявлено» (внутренним потребителям, напр. mechanics
// $this->stream у stream-фильтров, GH-20370).
func (vm *VM) PropVisibility(ci ClassInfo, name string) (Visibility, bool) {
	pd, _, ok := propInfoForScope(nil, ci, name)
	if !ok || pd == nil {
		return Public, false
	}
	return pd.vis, true
}

// isReadonlyClass сообщает, помечен ли класс (или его предок) как readonly.
func isReadonlyClass(ci ClassInfo) bool {
	for rc, ok := asRuntimeClass(ci); ok && rc != nil; rc = rc.parent {
		if rc.readonlyClass {
			return true
		}
	}
	return false
}

// applyDefaults заполняет слоты дефолтами с учётом наследования. Раскладка
// класса (classSlots) уже включает родительские слоты; ленивые дефолты
// разрешаются тут (значения кэшируются в раскладке класса объявления).
func (c *runtimeClass) applyDefaults(o *Object) {
	cs := c.layout()
	if o == nil {
		return
	}
	if n := len(cs.entries); n > 0 {
		o.slots = make([]Slot, n)
		copy(o.slots, cs.defaults)
	}
	if cs.defErr != nil && c.vm != nil {
		c.vm.setPendingDefaultError(cs.defErr)
	}
	for k := c; k != nil; k = k.parent {
		if len(k.lazyDefaults) == 0 {
			continue
		}
		kcs := k.layout()
		for _, name := range k.propOrder {
			th, ok := k.lazyDefaults[name]
			if !ok {
				continue
			}
			pd := k.ownProps[name]
			if pd != nil && pd.static {
				continue
			}
			key := name
			if pd != nil && pd.vis == Private {
				key = mangledPropKey(k, name)
			}
			idx, ok2 := cs.byKey[key]
			if !ok2 || o.slots == nil {
				continue
			}
			v, err := k.resolveThunk(th)
			if err != nil {
				if k.vm != nil {
					k.vm.setPendingDefaultError(err)
				}
				continue
			}
			s, cerr := k.defaultSlotValue(pd, name, v)
			if cerr != nil {
				if k.vm != nil {
					k.vm.setPendingDefaultError(cerr)
				}
				continue
			}
			o.slots[idx] = s
			if idx < len(kcs.defaults) {
				kcs.defaults[idx] = s
			}
		}
	}
}

// propDeclOf находит объявление свойства name в цепочке наследования.
func (c *runtimeClass) propDeclOf(name string) (*propDecl, *runtimeClass) {
	for k := c; k != nil; k = k.parent {
		if pd, ok := k.ownProps[name]; ok {
			return pd, k
		}
	}
	return nil, nil
}

// staticShadowInfo ищет объявленное статическое свойство name в цепочке
// наследования (для диагностики "Accessing static property ... as non static").
func (c *runtimeClass) staticShadowInfo(name string) (*propDecl, *runtimeClass, bool) {
	for k := c; k != nil; k = k.parent {
		if pd, ok := k.ownProps[name]; ok && pd.static {
			return pd, k, true
		}
	}
	return nil, nil, false
}

func (c *runtimeClass) findPropGet(name string) func(*Object) Value {
	for k := c; k != nil; k = k.parent {
		if f, ok := k.propGet[name]; ok {
			return f
		}
	}
	return nil
}

func (c *runtimeClass) findPropSet(name string) func(*Object, Value) {
	for k := c; k != nil; k = k.parent {
		if f, ok := k.propSet[name]; ok {
			return f
		}
	}
	return nil
}

// propNames возвращает свойства объекта в порядке PHP: объявленные (от корня к
// листу, порядок объявления), затем динамические (отсортированы).
// DumpEntry — элемент вывода var_dump для свойства объекта.
type DumpEntry struct {
	Key    string
	Val    Slot
	Uninit bool
	Type   string
	// KeyInt — ключ __debugInfo()-массива был целым (в var_dump печатается
	// без кавычек: [0]=>, а не ["0"]=>).
	KeyInt bool
	// Decl — класс объявления свойства (для scope-зависимых потребителей:
	// http_build_query и т.п.).
	Decl string
	// Suffix — маркер видимости, дописываемый к имени свойства внутри var_dump,
	// напр. `:protected` или `:"Class":private` ("" для public).
	Suffix string
}

// dumpVisSuffix формирует маркер видимости свойства для var_dump.
func dumpVisSuffix(v Visibility, decl string) string {
	switch v {
	case Protected:
		return ":protected"
	case Private:
		return ":\"" + decl + "\":private"
	}
	return ""
}

// ClassExtendsOrIs сообщает, что ci совпадает с other или наследует его
// (по указателю или имени, без учёта регистра).
func ClassExtendsOrIs(ci, other ClassInfo) bool {
	if ci == nil || other == nil {
		return false
	}
	for k := ci; k != nil; k = k.Parent() {
		if k == other || strings.EqualFold(k.Name(), other.Name()) {
			return true
		}
	}
	return false
}

// IsPlainObject сообщает, что объект — экземпляр userland-класса без нативного
// хранилища (для var_dump можно перечислять объявленные свойства напрямую).
func (vm *VM) IsPlainObject(o *Object) bool {
	rc, ok := asRuntimeClass(o.Class())
	return ok && rc != nil && rc.nativeNew == nil
}

// IsDumpPlainObject сообщает, что свойства объекта для var_dump/print_r/
// debug_zval_dump перечисляются через ObjectDumpEntries. Нативные SPL-контейнеры
// (ArrayObject/ArrayIterator/SplObjectStorage) тоже попадают сюда: их debug-info
// — приватное свойство storage (см. ObjectDumpEntries). Для приведения к массиву
// (casts.go) используется строгий IsPlainObject.
func (vm *VM) IsDumpPlainObject(o *Object) bool {
	if vm.IsPlainObject(o) {
		return true
	}
	rc, ok := asRuntimeClass(o.Class())
	if !ok || rc == nil {
		return false
	}
	switch rc.Name() {
	case "ArrayObject", "ArrayIterator", "SplObjectStorage":
		return true
	}
	return false
}

// splitMangledProp разбирает внутреннее имя необъявленного свойства:
// "\x00*\x00name" -> (name, ":protected"); "\x00Cls\x00name" -> (name, `:"Cls":private`).
func splitMangledProp(name string) (string, string, bool) {
	if len(name) == 0 || name[0] != 0 {
		return "", "", false
	}
	if strings.HasPrefix(name, "\x00*\x00") {
		return name[3:], ":protected", true
	}
	if i := strings.IndexByte(name[1:], 0); i >= 0 {
		cls := name[1 : 1+i]
		pn := name[1+i+1:]
		return pn, `:"` + cls + `":private`, true
	}
	return "", "", false
}

// DebugInfoProps возвращает свойства объекта для var_dump/print_r/
// debug_zval_dump с учётом магического __debugInfo(). magic=true, если метод
// вызван (в т.ч. вернувший null — пустой список свойств). При не-массиве —
// фатальная ошибка Zend "__debuginfo() must return an array".
func (vm *VM) DebugInfoProps(o *Object) (entries []DumpEntry, magic bool, err error) {
	rc, ok := asRuntimeClass(o.Class())
	if !ok || rc == nil {
		return nil, false, nil
	}
	m := rc.lookupMethod("__debugInfo")
	if m == nil {
		return nil, false, nil
	}
	res, err := vm.invokeMethod(m, MakeObjectOf(vm, o), rc, nil)
	if err != nil {
		return nil, true, err
	}
	res = deref(res)
	if res.typ == NullType {
		return []DumpEntry{}, true, nil
	}
	if res.typ != ArrayType {
		return nil, true, NewFatal("__debuginfo() must return an array")
	}
	a := res.AsArray()
	out := make([]DumpEntry, 0, a.Len())
	for k, v := range a.All() {
		name := k.PHPString()
		suffix := ""
		if mangled, sfx, ok2 := splitMangledProp(name); ok2 {
			name, suffix = mangled, sfx
		}
		out = append(out, DumpEntry{Key: name, Val: v, Suffix: suffix, KeyInt: !k.IsStr()})
	}
	return out, true, nil
}

// ObjectDumpEntries перечисляет свойства объекта в порядке объявления, включая
// неинициализированные типизированные свойства (для var_dump).
func (vm *VM) ObjectDumpEntries(o *Object) []DumpEntry {
	rc, ok := asRuntimeClass(o.Class())
	if !ok || rc == nil {
		return nil
	}
	var chain []*runtimeClass
	for k := rc; k != nil; k = k.parent {
		chain = append(chain, k)
	}
	var out []DumpEntry
	seen := map[string]bool{}
	// effectiveVis/effDecl — видимость и класс объявления свойства с учётом
	// перекрытия в наследнике (берём самое производное объявление).
	effectiveVis := map[string]Visibility{}
	effDecl := map[string]string{}
	for k := rc; k != nil; k = k.parent {
		for _, name := range k.propOrder {
			if _, done := effectiveVis[name]; done {
				continue
			}
			if pd, ok := k.ownProps[name]; ok {
				effectiveVis[name] = pd.vis
				effDecl[name] = k.name
			} else {
				effectiveVis[name] = Public
				effDecl[name] = k.name
			}
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		c := chain[i]
		for _, name := range c.propOrder {
			pd, hasPd := c.ownProps[name]
			// Private-свойство хранится под манглированным именем и печатается
			// отдельной строкой с маркером объявившего класса (Zend показывает
			// и «тень» предка, и перекрывающее свойство потомка).
			if hasPd && pd.vis == Private {
				key := mangledPropKey(c, name)
				seen[key] = true
				suffix := `:"` + c.name + `":private`
				if v, ok := o.propValueRaw(key); ok {
					out = append(out, DumpEntry{Key: name, Val: v, Suffix: suffix, Decl: c.name})
					continue
				}
				if o.IsUnset(key) {
					continue
				}
				if pd.hasType {
					out = append(out, DumpEntry{Key: name, Uninit: true, Type: pd.typ, Suffix: suffix, Decl: c.name})
				}
				continue
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			suffix := ""
			uninitType := ""
			isTyped := false
			if hasPd {
				suffix = dumpVisSuffix(effectiveVis[name], effDecl[name])
				isTyped = pd.hasType
				uninitType = pd.typ
			}
			if v, ok := o.propValueRaw(name); ok {
				out = append(out, DumpEntry{Key: name, Val: v, Suffix: suffix, Decl: effDecl[name]})
				continue
			}
			if isTyped {
				out = append(out, DumpEntry{Key: name, Uninit: true, Type: uninitType, Suffix: suffix, Decl: effDecl[name]})
				continue
			}
			if o.IsUnset(name) {
				continue
			}
			if _, ok := c.propGet[name]; ok {
				if v, ok := o.GetProp(name); ok {
					out = append(out, DumpEntry{Key: name, Val: valueSlot(v), Suffix: suffix, Decl: effDecl[name]})
				}
			}
		}
	}
	var extra []string
	extraSeen := map[string]bool{}
	for _, name := range o.dynOrder {
		if !seen[name] {
			if _, ok := o.props[name]; ok {
				extra = append(extra, name)
				extraSeen[name] = true
			}
		}
	}
	var rest []string
	for name := range o.props {
		if !seen[name] && !extraSeen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	extra = append(extra, rest...)
	for _, name := range extra {
		if key, suffix, ok := splitMangledProp(name); ok {
			out = append(out, DumpEntry{Key: key, Val: valueSlot(o.props[name]), Suffix: suffix})
			continue
		}
		out = append(out, DumpEntry{Key: name, Val: valueSlot(o.props[name])})
	}
	// Нативные SPL-контейнеры показывают внутреннее хранилище как private
	// свойство storage (как Zend debug-info).
	switch rc.Name() {
	case "ArrayObject", "ArrayIterator":
		d := (*splArrayData)(unsafe.Pointer(o))
		arr := d.arr
		if arr == nil {
			arr = NewArray()
		}
		out = append(out, DumpEntry{Key: "storage", Val: MakeArray(arr), Suffix: `:"` + rc.Name() + `":private`})
	case "SplObjectStorage":
		d := (*splObjectStorageData)(unsafe.Pointer(o))
		arr := NewArray()
		for i, e := range d.entries {
			pair := NewArray()
			pair.Set(MakeStrKey("obj"), e.obj)
			pair.Set(MakeStrKey("inf"), e.info)
			arr.Set(MakeIntKey(int64(i)), MakeArray(pair))
		}
		out = append(out, DumpEntry{Key: "storage", Val: MakeArray(arr), Suffix: `:"` + rc.Name() + `":private`})
	}
	return out
}

// propNames возвращает имена свойств объекта в порядке PHP.
func (c *runtimeClass) propNames(o *Object) []string {
	if o == nil {
		return nil
	}
	var chain []*runtimeClass
	for k := c; k != nil; k = k.parent {
		chain = append(chain, k)
	}
	var out []string
	seen := map[string]bool{}
	var scope ClassInfo
	if c.vm != nil {
		scope = c.vm.ctx.self
	}
	for i := len(chain) - 1; i >= 0; i-- {
		c := chain[i]
		for _, name := range c.propOrder {
			if pd, ok := c.ownProps[name]; ok && pd.vis == Private {
				// Приватное свойство выводится только из области объявившего
				// класса (Zend: foreach проверяет доступ к манглированному
				// слоту; Bug #33171 — имя печатается один раз).
				seen[mangledPropKey(c, name)] = true
				if scope != ClassInfo(c) {
					continue
				}
				if seen[name] {
					continue
				}
				seen[name] = true
				if _, ok := o.propValueRaw(mangledPropKey(c, name)); ok {
					out = append(out, name)
				}
				continue
			}
			if seen[name] {
				continue
			}
			if _, ok := o.propValueRaw(name); ok {
				seen[name] = true
				out = append(out, name)
				continue
			}
			if _, ok := c.propGet[name]; ok {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	var extra []string
	extraSeen := map[string]bool{}
	for _, name := range o.dynOrder {
		if !seen[name] {
			if _, ok := o.props[name]; ok {
				extra = append(extra, name)
				extraSeen[name] = true
			}
		}
	}
	var rest []string
	for name := range o.props {
		if !seen[name] && !extraSeen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	var restOut []string
	for _, name := range rest {
		if pn, _, ok := splitMangledProp(name); ok {
			restOut = append(restOut, pn)
			continue
		}
		restOut = append(restOut, name)
	}
	return append(out, append(extra, restOut...)...)
}

// Method регистрирует метод (Scope проставляется в этот класс).
func (c *runtimeClass) Method(name string, fn *Function, vis ...Visibility) ClassInfo {
	fn.Scope = c
	c.methods[name] = fn
	appendUnique(&c.methodOrder, name)
	if len(vis) > 0 {
		c.setMethodVis(name, vis[0])
	}
	return c
}

// Constructor регистрирует конструктор.
func (c *runtimeClass) Constructor(fn *Function, vis ...Visibility) ClassInfo {
	fn.Scope = c
	c.ctor = fn
	if len(vis) > 0 {
		c.ctorVis = vis[0]
	}
	appendUnique(&c.methodOrder, fn.Name)
	return c
}

// DefineStatic объявляет/возвращает статическое свойство класса.
func (c *runtimeClass) DefineStatic(name string, init Value) *Slot {
	if c.staticProps == nil {
		c.staticProps = map[string]*Slot{}
	}
	s := new(Slot)
	SlotOf(s, init)
	c.staticProps[name] = s
	appendUnique(&c.staticOrder, name)
	return s
}

// SetStaticUninit помечает статическое свойство как неинициализированное.
func (c *runtimeClass) SetStaticUninit(name string) {
	if c.staticUninit == nil {
		c.staticUninit = map[string]bool{}
	}
	c.staticUninit[name] = true
}

// staticIsUninit сообщает, что статическое свойство не инициализировано.
func (c *runtimeClass) staticIsUninit(name string) bool {
	for k := c; k != nil; k = k.parent {
		if k.staticUninit[name] {
			return true
		}
	}
	return false
}

// clearStaticUninit снимает пометку неинициализированности (после присваивания).
func (c *runtimeClass) clearStaticUninit(name string) {
	for k := c; k != nil; k = k.parent {
		if k.staticUninit[name] {
			delete(k.staticUninit, name)
		}
	}
}

// DefineConst объявляет константу класса.
func (c *runtimeClass) DefineConst(name string, v Value) ClassInfo {
	if c.consts == nil {
		c.consts = map[string]Value{}
	}
	c.consts[name] = v
	appendUnique(&c.constOrder, name)
	return c
}

// SetConstVis задаёт видимость константы класса (для проверок доступа).
func (c *runtimeClass) SetConstVis(name string, vis Visibility) ClassInfo {
	if c.constVis == nil {
		c.constVis = map[string]Visibility{}
	}
	c.constVis[name] = vis
	return c
}

// SetConstDoc задаёт doc-комментарий константы класса (рефлексия).
func (c *runtimeClass) SetConstDoc(name, doc string) ClassInfo {
	if c.constDoc == nil {
		c.constDoc = map[string]string{}
	}
	c.constDoc[name] = doc
	return c
}

// DefineLazyConst объявляет константу, значение которой вычисляется лениво.
func (c *runtimeClass) DefineLazyConst(name string, th *Thunk) ClassInfo {
	if c.lazyConsts == nil {
		c.lazyConsts = map[string]*Thunk{}
	}
	c.lazyConsts[name] = th
	appendUnique(&c.constOrder, name)
	return c
}

// DeclareLazyDefault объявляет свойство с ленивым дефолтным значением.
func (c *runtimeClass) DeclareLazyDefault(name string, th *Thunk) ClassInfo {
	if c.lazyDefaults == nil {
		c.lazyDefaults = map[string]*Thunk{}
	}
	c.lazyDefaults[name] = th
	appendUnique(&c.propOrder, name)
	return c
}

// DefineLazyStatic объявляет статическое свойство с ленивым значением.
func (c *runtimeClass) DefineLazyStatic(name string, th *Thunk) *Slot {
	if c.lazyStatics == nil {
		c.lazyStatics = map[string]*Thunk{}
	}
	c.lazyStatics[name] = th
	appendUnique(&c.staticOrder, name)
	return nil
}

// lookupStatic ищет статическое свойство (плоская таблица, иначе обход).
// Если значение ленивое — материализует его при первом обращении.
func (c *runtimeClass) lookupStatic(name string) *Slot {
	if c.allStatics != nil {
		if s, ok := c.allStatics[name]; ok {
			return s
		}
	} else {
		for k := c; k != nil; k = k.parent {
			if s, ok := k.staticProps[name]; ok {
				return s
			}
		}
	}
	return c.materializeLazyStatic(name)
}

// findLazyStatic возвращает ленивый статик с учётом наследования.
func (c *runtimeClass) findLazyStatic(name string) (*Thunk, *runtimeClass) {
	if c.allLazyStatics != nil {
		if th, ok := c.allLazyStatics[name]; ok {
			return th, c.lazyOwnerStatic(name)
		}
		return nil, nil
	}
	for k := c; k != nil; k = k.parent {
		if th, ok := k.lazyStatics[name]; ok {
			return th, k
		}
	}
	return nil, nil
}

// lazyOwnerStatic ищет класс-владелец ленивого статика (для материализации в
// его таблицу).
func (c *runtimeClass) lazyOwnerStatic(name string) *runtimeClass {
	for k := c; k != nil; k = k.parent {
		if _, ok := k.lazyStatics[name]; ok {
			return k
		}
	}
	return c
}

// materializeLazyStatic вычисляет ленивое статическое свойство и заменяет его
// обычным слотом (чтобы последующие записи шли в ту же ячейку).
func (c *runtimeClass) materializeLazyStatic(name string) *Slot {
	th, owner := c.findLazyStatic(name)
	if th == nil || owner == nil {
		return nil
	}
	v, err := c.resolveThunk(th)
	if err != nil {
		return nil
	}
	s := new(Slot)
	SlotOf(s, v)
	if owner.staticProps == nil {
		owner.staticProps = map[string]*Slot{}
	}
	owner.staticProps[name] = s
	delete(owner.lazyStatics, name)
	if owner.allStatics != nil {
		owner.allStatics[name] = s
	}
	if c.allStatics != nil {
		c.allStatics[name] = s
	}
	return s
}

// resolveThunk вычисляет ленивое выражение через VM класса.
func (c *runtimeClass) resolveThunk(th *Thunk) (Value, error) {
	if c != nil && c.vm != nil {
		s, err := c.vm.evalThunk(th)
		if err != nil {
			return s, err
		}
		if th.TypeConstr != "" {
			out, cerr := c.vm.coerceClassConst(th.TypeConstr, th.Label, s, th.self, thunkStrict(th))
			if cerr != nil {
				return nil, cerr
			}
			return out, nil
		}
		return s, err
	}
	if th.done {
		return th.val, th.err
	}
	return nil, NewThrowable("Error", "Cannot evaluate constant expression")
}

// coerceClassConst проверяет/приводит значение типизированной константы класса
// при её вычислении в рантайме.
func (vm *VM) coerceClassConst(typ, label string, v Value, scope ClassInfo, strict bool) (Value, error) {
	s, ok := v.(Slot)
	if !ok {
		return v, nil
	}
	rt := resolveTypeScope(typ, scope)
	if ok, out := vm.coerceReturnModeEx(rt, s, strict, false); ok {
		return out, nil
	}
	return v, NewThrowable("TypeError", "Cannot assign "+valueTypeName(vm, s)+" to class constant "+label+" of type "+TypeString(typ))
}

// thunkStrict сообщает, что константное выражение объявлено в strict-файле.
func thunkStrict(th *Thunk) bool {
	if th == nil || th.fn == nil {
		return false
	}
	if cf, ok := th.fn.Executable.(*CompiledFunction); ok && cf.chunk != nil {
		return cf.chunk.Strict
	}
	return false
}

// lookupConst ищет константу (плоская таблица, иначе обход). Ленивые константы
// вычисляются и кэшируются.
func (c *runtimeClass) lookupConst(name string) (Value, bool) {
	v, ok, err := c.lookupConstErr(name)
	if err != nil {
		return nil, false
	}
	return v, ok
}

// lookupConstAccess возвращает константу с проверкой видимости относительно
// области scope (как Zend при доступе к константе класса).
func (c *runtimeClass) lookupConstAccess(name string, scope ClassInfo) (Value, bool, error) {
	if c.allConsts != nil {
		v, ok := c.allConsts[name]
		if !ok {
			// Ленивые константы (в т.ч. унаследованные).
			if th := c.findLazyConst(name); th != nil {
				s, err := c.resolveThunk(th)
				if err != nil {
					return nil, false, err
				}
				return s, true, nil
			}
			return nil, false, nil
		}
		vis := c.allConstVis[name]
		decl := c.allConstDecl[name]
		if decl == nil {
			decl = c
		}
		if !scopeCanAccess(scope, decl, vis) {
			return nil, false, NewThrowable("Error", "Cannot access "+visibilityWord(vis)+" constant "+decl.Name()+"::"+name)
		}
		return v, true, nil
	}
	for k := c; k != nil; k = k.parent {
		if v, ok := k.consts[name]; ok {
			vis := k.constVis[name]
			if vis == Private && scope != k {
				if c != k {
					return nil, false, nil
				}
				return nil, false, NewThrowable("Error", "Cannot access private constant "+k.Name()+"::"+name)
			}
			if !scopeCanAccess(scope, k, vis) {
				return nil, false, NewThrowable("Error", "Cannot access "+visibilityWord(vis)+" constant "+k.Name()+"::"+name)
			}
			return v, true, nil
		}
	}
	if th := c.findLazyConst(name); th != nil {
		s, err := c.resolveThunk(th)
		if err != nil {
			return nil, false, err
		}
		return s, true, nil
	}
	return nil, false, nil
}

// scopeCanAccess сообщает, доступна ли константа из области scope.
func scopeCanAccess(scope, decl ClassInfo, vis Visibility) bool {
	if vis == Public {
		return true
	}
	if scope == nil || decl == nil {
		return false
	}
	if scope == decl {
		return true
	}
	if vis == Private {
		return false
	}
	// protected: доступ из класса-объявления и его наследников.
	return isSubclassOf(scope, decl)
}

// isSubclassOf сообщает, что sub — это decl или наследник decl.
func isSubclassOf(sub, decl ClassInfo) bool {
	for k := sub; k != nil; k = k.Parent() {
		if k == decl {
			return true
		}
	}
	if r, ok := sub.(*runtimeClass); ok {
		return r.ancestors[decl]
	}
	return false
}

func visibilityWord(vis Visibility) string {
	switch vis {
	case Private:
		return "private"
	case Protected:
		return "protected"
	}
	return "public"
}

// lookupConstErr — как lookupConst, но возвращает ошибку вычисления ленивого
// выражения (PHP-фатал/исключение).
func (c *runtimeClass) lookupConstErr(name string) (Value, bool, error) {
	if c.allConsts != nil {
		if v, ok := c.allConsts[name]; ok {
			return v, true, nil
		}
	} else {
		for k := c; k != nil; k = k.parent {
			if v, ok := k.consts[name]; ok {
				return v, true, nil
			}
		}
	}
	if th := c.findLazyConst(name); th != nil {
		s, err := c.resolveThunk(th)
		if err != nil {
			return nil, false, err
		}
		return s, true, nil
	}
	return nil, false, nil
}

// findLazyConst возвращает ленивую константу с учётом наследования.
func (c *runtimeClass) findLazyConst(name string) *Thunk {
	if c.allLazyConsts != nil {
		return c.allLazyConsts[name]
	}
	for k := c; k != nil; k = k.parent {
		if th, ok := k.lazyConsts[name]; ok {
			return th
		}
	}
	return nil
}

// lookupMethod ищет метод (плоская таблица, иначе обход).
func (c *runtimeClass) lookupMethod(name string) *Function {
	if c == nil {
		return nil
	}
	if m, ok := c.allMethods[name]; ok {
		return m.fn
	}
	// Имена методов в PHP регистронезависимы: пробуем сложенное имя, если
	// прямое совпадение не найдено.
	if ln := strings.ToLower(name); ln != name {
		if m, ok := c.allMethods[ln]; ok {
			return m.fn
		}
		for k := c; k != nil; k = k.parent {
			if m, ok := k.methods[ln]; ok {
				return m
			}
		}
	}
	for k := c; k != nil; k = k.parent {
		if m, ok := k.methods[name]; ok {
			return m
		}
	}
	return c.methodFold(name)
}

// methodFold ищет метод в цепочке классов без учёта регистра имени (PHP
// регистронезависим; объявление может быть в любом регистре).
func (c *runtimeClass) methodFold(name string) *Function {
	for k := c; k != nil; k = k.parent {
		for mname, m := range k.methods {
			if strings.EqualFold(mname, name) {
				return m
			}
		}
	}
	return nil
}

// lookupCtor ищет конструктор с учётом наследования.
func (c *runtimeClass) lookupCtor() *Function {
	for k := c; k != nil; k = k.parent {
		if k.ctor != nil {
			return k.ctor
		}
	}
	return nil
}

// ctorInfo ищет конструктор, возвращая объявивший класс и видимость.
func (c *runtimeClass) ctorInfo() (*Function, *runtimeClass, Visibility) {
	for k := c; k != nil; k = k.parent {
		if k.ctor != nil {
			vis := Public
			if k.ctorVis != 0 {
				vis = k.ctorVis
			} else if k.methVis != nil {
				if v, ok := k.methVis["__construct"]; ok {
					vis = v
				}
			}
			return k.ctor, k, vis
		}
	}
	return nil, nil, Public
}

// CtorInfo — экспорт для интерфейса ClassInfo.
func (c *runtimeClass) CtorInfo() (*Function, ClassInfo, Visibility) {
	f, d, v := c.ctorInfo()
	if d == nil {
		return f, nil, v
	}
	return f, d, v
}

// IsSubclassOf сообщает, что c — это c2 или его наследник.
func (c *runtimeClass) IsSubclassOf(c2 *runtimeClass) bool {
	for k := c; k != nil; k = k.parent {
		if k == c2 {
			return true
		}
	}
	return false
}

// --- Автозагрузка (spl_autoload_*). ---

// RegisterAutoloader добавляет callable в список автозагрузчиков.
// prepend=true ставит его в начало списка.
func (vm *VM) RegisterAutoloader(cb Slot, prepend bool) {
	if prepend {
		vm.autoloaders = append([]Slot{cb}, vm.autoloaders...)
		return
	}
	vm.autoloaders = append(vm.autoloaders, cb)
}

// UnregisterAutoloader удаляет автозагрузчик, равный cb. Возвращает true,
// если он был найден.
func (vm *VM) UnregisterAutoloader(cb Slot) bool {
	for i, x := range vm.autoloaders {
		if slotCallableEqual(vm, x, cb) {
			vm.autoloaders = append(vm.autoloaders[:i], vm.autoloaders[i+1:]...)
			return true
		}
	}
	return false
}

// Autoloaders возвращает копию списка зарегистрированных автозагрузчиков.
func (vm *VM) Autoloaders() []Slot {
	return append([]Slot(nil), vm.autoloaders...)
}

// Autoload последовательно вызывает зарегистрированные автозагрузчики, пока
// класс name не будет определён. Возвращает true, если класс появился.
func (vm *VM) Autoload(ctx *Context, name string) (bool, error) {
	raw := strings.TrimLeft(name, "\\")
	if raw == "" {
		return false, nil
	}
	if vm.findClass(raw) != nil {
		return true, nil
	}
	key := strings.ToLower(raw)
	if vm.autoloading == nil {
		vm.autoloading = map[string]bool{}
	}
	if vm.autoloading[key] {
		return false, nil
	}
	vm.autoloading[key] = true
	defer delete(vm.autoloading, key)
	if ctx == nil {
		ctx = &vm.ctx
	}
	pc := 0
	if vm.ip > 0 {
		pc = vm.ip - 1
	}
	for _, cb := range vm.autoloaders {
		if vm.findClass(raw) != nil {
			return true, nil
		}
		// Zend вызывает автозагрузчик из точки обращения к классу: кадр в
		// трейсе имеет файл/строку ссылки, а не "[internal function]".
		if _, err := vm.CallCallableAt(ctx, cb, []Slot{MakeString(raw)}, vm.curFile, vm.chunk, pc, vm.line()); err != nil {
			return false, err
		}
	}
	return vm.findClass(raw) != nil, nil
}

// slotCallableEqual сравнивает два callable-слота для spl_autoload_unregister.
func slotCallableEqual(vm *VM, a, b Slot) bool {
	a, b = deref(a), deref(b)
	if a.typ != b.typ {
		return false
	}
	switch a.typ {
	case StringType:
		return strings.EqualFold(a.StringVal(), b.StringVal())
	case ObjectType:
		return a.ptr == b.ptr
	case ArrayType:
		av, bv := a.AsArray(), b.AsArray()
		if av == nil || bv == nil || av.Len() != 2 || bv.Len() != 2 {
			return false
		}
		aa, ba := av.Values(), bv.Values()
		if deref(aa[0]).typ == ObjectType || deref(ba[0]).typ == ObjectType {
			if deref(aa[0]).ptr != deref(ba[0]).ptr {
				return false
			}
		} else if !strings.EqualFold(deref(aa[0]).ToString(), deref(ba[0]).ToString()) {
			return false
		}
		return strings.EqualFold(deref(aa[1]).ToString(), deref(ba[1]).ToString())
	case ClosureType:
		return a.ptr == b.ptr
	}
	return false
}
