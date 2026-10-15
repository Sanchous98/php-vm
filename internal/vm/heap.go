package vm

// Heap-значения VM.
//
// Слот адресует heap-значение указателем в ptr (unsafe.Pointer): *Array,
// объект. Строка живёт прямо в слоте (ptr — данные, num — длина, см. slot.go).

import (
	"strings"
	"unsafe"
)

// Object — плоская шапка любого экземпляра класса (аналог zend_object):
//
//   - class — дескриптор класса (как ce в zend_object);
//   - props — свойства (объявленные и динамические).
//
// Данные нативного объекта — конкретные Go-поля на самом типе объекта
// (см. DateTime, Spl*), а не data any. Шапка Object встроена первым полем в
// корень цепочки наследования, поэтому *Object-указатель == указатель на
// объект (offset 0), и приведение к конкретному типу — одно unsafe в
// дескрипторе (objectAs).
type Object struct {
	class ClassInfo
	// incomplete — класс объекта это __PHP_Incomplete_Class (быстрая проверка
	// на горячем пути вызова метода вместо сравнения имени класса).
	incomplete bool
	// slots — объявленные нестатические свойства по раскладке класса
	// (runtimeClass.layout): запись/чтение без боксинга Value-интерфейса.
	// Индекс слота стабилен для всей иерархии (наследник копирует родительские
	// слоты). Неинициализированное типизированное свойство и свойство, снятое
	// unset(), лежат как uninitSlot. Динамические свойства — в props.
	slots []Slot
	props map[string]Value
	// unset — имена объявленных свойств, явно снятых unset(). После unset
	// доступ идёт через __get (PHP-семантика «unset state»).
	unset map[string]bool
	// readonlyReinit — readonly-свойства, которые можно переинициализировать
	// ровно один раз (состояние после clone, внутри __clone).
	readonlyReinit map[string]bool
	// objID — PHP-хендл объекта (#N в var_dump), присваивается при создании
	// (порядковый номер в рамках запуска VM).
	objID int
	// lazyID — объект-обёртка рефлексии (Zend lazy object): хендл выдаётся не
	// при создании, а при материализации (первый дамп/использование).
	lazyID bool
	// bareObj — объект класса из disable_classes: объявленных свойств нет,
	// чтение/запись идут как для динамических (bug77494).
	bareObj bool
	// dead — объект уничтожен сборщиком (lifetime.go): __destruct вызван,
	// хендл возвращён в free-list. Повторный markObj хендл не выдаёт.
	dead bool
	// gcPending — объект отложен до gc_collect_cycles (циклический мусор:
	// refcount не падает до нуля, как в Zend). Хендл и память сохраняются.
	gcPending bool
	// refs — оценка числа известных ссылок для сборщика (lifetime.go):
	// создание/проход сбрасывают её в 1, запись в контейнеры (props/массивы/
	// статики/ссылки) увеличивает, потеря ссылки уменьшает. Значение <= 0 —
	// кандидат на проверку достижимости; сама проверка всегда точная, поэтому
	// счётчик влияет только на момент запуска прохода.
	refs int32
	// dynOrder — порядок появления динамических свойств (как в PHP).
	dynOrder []string
	// excTrace/excFile/excLine — stack trace и локация исключения, снятые в
	// момент конструирования (Throwable::__toString/getTraceAsString).
	excTrace []callFrameInfo
	excFile  string
	excLine  int
	// excDefined — локация исключения — место объявления функции, поэтому
	// __toString печатает "and defined in file:line" (ошибки типов аргументов).
	excDefined bool
	excSet     bool
	// pos — внутренний указатель объекта для reset/current/next/prev/end/key
	// (не связан с foreach). iterSet — указатель валиден (до первого
	// reset/end/next/prev он «NULL»: key() -> null, current() -> false).
	pos     int
	iterSet bool
	// typedRefs — в свойствах объекта есть ссылка, «удерживаемая» типизированным
	// свойством (Ref.sources): при обнулении оценки ссылок объект становится
	// urgent-кандидатом, чтобы к ближайшей точке OpSweep снять источники
	// (Zend zend_object_std_dtor делает это при refcount == 0).
	typedRefs bool
	// gcNested — объект потерял последнюю оценку ссылок ВНУТРИ выполняющегося
	// __destruct (каскад уничтожения по refcount). Zend уничтожает такие узлы
	// немедленно, минуя подсчёт gc_collect_cycles, поэтому сборщик не включает
	// их в возвращаемое число собранных (gc_017/gc_028: деструктор снимает
	// ссылку на соседа по циклу).
	gcNested bool
}

// MarkTypedRef отмечает объект как держащий типизированную ссылку.
func (o *Object) MarkTypedRef() {
	if o != nil {
		o.typedRefs = true
	}
}

// TypedRefs сообщает, держит ли объект типизированные ссылки.
func (o *Object) TypedRefs() bool { return o != nil && o.typedRefs }

// IterPos возвращает внутренний указатель объекта (для reset/current/...).
func (o *Object) IterPos() int {
	if o == nil {
		return 0
	}
	return o.pos
}

// IterValid сообщает, что внутренний указатель объекта установлен.
func (o *Object) IterValid() bool { return o != nil && o.iterSet }

// SetIterPos задаёт внутренний указатель объекта и делает его валидным.
func (o *Object) SetIterPos(p int) {
	if o != nil {
		o.pos = p
		o.iterSet = true
	}
}

// ClearIterPos делает внутренний указатель объекта невалидным.
func (o *Object) ClearIterPos() {
	if o != nil {
		o.pos = 0
		o.iterSet = false
	}
}

// SetDynOrder задаёт порядок динамических свойств (для makeStdClass).
func (o *Object) SetDynOrder(names []string) {
	if o == nil {
		return
	}
	o.dynOrder = append(o.dynOrder[:0], names...)
}

// ID возвращает PHP-хендл объекта (#N) или 0, если он ещё не присвоен.
func (o *Object) ID() int {
	if o == nil {
		return 0
	}
	return o.objID
}

// Class возвращает дескриптор класса объекта.
func (o *Object) Class() ClassInfo {
	if o == nil {
		return nil
	}
	return o.class
}

// ClassName возвращает имя класса экземпляра (для get_class и т.п.).
func (o *Object) ClassName() string {
	if o == nil || o.class == nil {
		return ""
	}
	return o.class.Name()
}

// uninitType — внутренний тег слота «объявленное свойство не инициализировано»
// (типизированное без дефолта) или «снято unset()». Наружу (Value/диагностики/
// дампы) такой слот не выходит: потребители проверяют isUninit.
const slotUninit Type = 0xFF

// makeUninit — слот неинициализированного объявленного свойства.
func makeUninit() Slot { return Slot{typ: slotUninit} }

// isUninit сообщает, что слот — маркер неинициализированного свойства.
func isUninit(s Slot) bool { return s.typ == slotUninit }

// slotIndex возвращает индекс слота объявленного свойства по ключу хранения
// (имя или манглированный ключ private). ok=false для динамических свойств,
// нативных мостов (Go-поля) и объектов без раскладки.
func (o *Object) slotIndex(key string) (int, bool) {
	if o == nil {
		return 0, false
	}
	rc, ok := asRuntimeClass(o.class)
	if !ok || rc == nil {
		return 0, false
	}
	cs := rc.layout()
	if len(cs.entries) == 0 {
		return 0, false
	}
	idx, ok := cs.byKey[key]
	return idx, ok
}

// slotValue читает объявленное свойство по ключу хранения (без боксинга).
// ok=false также для неинициализированного/снятого unset() слота.
func (o *Object) slotValue(key string) (Slot, bool) {
	idx, ok := o.slotIndex(key)
	if !ok || idx >= len(o.slots) {
		return Slot{}, false
	}
	s := o.slots[idx]
	return s, s.typ != slotUninit
}

// propValueRaw читает свойство (объявленное или динамическое) по ключу
// хранения без боксинга Value. Нативные мосты (Go-поля) конвертируются.
// Наличие объявленного, но неинициализированного свойства неотличимо от
// отсутствующего — как в props-модели.
func (o *Object) propValueRaw(key string) (Slot, bool) {
	if o == nil || o.class == nil {
		return Slot{}, false
	}
	if rc, ok := asRuntimeClass(o.class); ok && rc != nil {
		if f := rc.findPropGet(key); f != nil {
			return valueSlot(f(o)), true
		}
	}
	if s, ok := o.slotValue(key); ok {
		return s, true
	}
	if o.hasClassSlot(key) {
		// Свойство объявлено (слот существует), но пусто: динамическую
		// запись под тем же ключом props-модель не создавала.
		return Slot{}, false
	}
	if v, ok := o.props[key]; ok {
		return valueSlot(v), true
	}
	return Slot{}, false
}

// hasClassSlot сообщает, что ключ — объявленное нестатическое свойство класса
// (слот есть, значение может быть неинициализировано).
func (o *Object) hasClassSlot(key string) bool {
	_, ok := o.slotIndex(key)
	return ok
}

// declaredInitialized сообщает, что объявленное свойство инициализировано
// (используется проверками readonly/uninit).
func (o *Object) declaredInitialized(key string) bool {
	_, ok := o.slotValue(key)
	return ok
}

// storeDeclaredSlot записывает значение в слот объявленного свойства.
// Учёт ссылок (noteStored/noteDropped) делает вызывающий (как в map-пути).
func (o *Object) storeDeclaredSlot(idx int, s Slot) {
	if o == nil || idx < 0 || idx >= len(o.slots) {
		return
	}
	o.slots[idx] = s
}

// propCount возвращает число «живых» мест хранения свойств (инициализированные
// слоты + записи props): используется foreach-состоянием для детекции
// изменений свойств во время обхода.
func (o *Object) propCount() int {
	if o == nil {
		return 0
	}
	n := len(o.props)
	for i := range o.slots {
		if o.slots[i].typ != slotUninit {
			n++
		}
	}
	return n
}

// forEachPropValue вызывает f на каждом инициализированном слоте свойств
// (объявленные слоты + записи props). Порядок не определён (для обхода ссылок).
func (o *Object) forEachPropValue(f func(Slot)) {
	if o == nil {
		return
	}
	for i := range o.slots {
		if s := o.slots[i]; s.typ != slotUninit {
			f(s)
		}
	}
	for _, v := range o.props {
		f(valueSlot(v))
	}
}

// ensureSlots материализует слоты объекта по раскладке класса (защитный путь
// для объектов, созданных в обход newObj: слоты выделяются при первой записи).
func (o *Object) ensureSlots(c *runtimeClass) {
	if o == nil || c == nil {
		return
	}
	cs := c.layout()
	if len(cs.entries) == 0 || len(o.slots) >= len(cs.entries) {
		return
	}
	ns := make([]Slot, len(cs.entries))
	copy(ns, cs.defaults)
	copy(ns, o.slots)
	o.slots = ns
}

// GetProp/SetProp — доступ к свойствам через handlers класса.
func (o *Object) GetProp(name string) (Value, bool) {
	if o == nil || o.class == nil {
		return nil, false
	}
	return o.class.GetProp(o, name)
}

func (o *Object) SetProp(name string, v Value) {
	if o == nil || o.class == nil {
		return
	}
	o.class.SetProp(o, name, v)
}

// UnsetProp удаляет свойство (unset($o->x)): объявленное — слот становится
// неинициализированным (значение отпускается), динамическое — удаляется из props.
func (o *Object) UnsetProp(name string) {
	if idx, ok := o.slotIndex(name); ok {
		if idx < len(o.slots) {
			if old := o.slots[idx]; old.typ != slotUninit {
				if rc, okr := asRuntimeClass(o.class); okr && rc != nil && rc.vm != nil {
					rc.vm.noteDropped(old)
				}
			}
			o.slots[idx] = makeUninit()
		}
		return
	}
	if o.props != nil {
		delete(o.props, name)
	}
}

// MarkUnset помечает объявленное свойство как снятое через unset().
func (o *Object) MarkUnset(name string) {
	if o.unset == nil {
		o.unset = map[string]bool{}
	}
	o.unset[name] = true
}

// IsUnset сообщает, что свойство было снято через unset().
func (o *Object) IsUnset(name string) bool {
	return o != nil && o.unset != nil && o.unset[name]
}

// ClearUnset снимает метку unset при повторной инициализации свойства.
func (o *Object) ClearUnset(name string) {
	if o != nil && o.unset != nil {
		delete(o.unset, name)
	}
}

// AllowReadonlyReinit/ConsumeReadonlyReinit — одноразовая переинициализация
// readonly-свойства (состояние после clone).
func (o *Object) AllowReadonlyReinit(name string) {
	if o.readonlyReinit == nil {
		o.readonlyReinit = map[string]bool{}
	}
	o.readonlyReinit[name] = true
}

func (o *Object) ConsumeReadonlyReinit(name string) bool {
	if o == nil || o.readonlyReinit == nil || !o.readonlyReinit[name] {
		return false
	}
	delete(o.readonlyReinit, name)
	return true
}

// PropertyNames возвращает свойства объекта в порядке PHP.
func (o *Object) PropertyNames() []string {
	if o == nil || o.class == nil {
		return nil
	}
	return o.class.propNames(o)
}

// objectAs приводит *Object к конкретному нативному типу T (шапка Object —
// первое поле T) с проверкой, что o — экземпляр owner (или его наследник).
func objectAs[T any](o *Object, owner ClassInfo) *T {
	if o == nil || o.class == nil {
		return nil
	}
	if owner != nil && !o.class.isA(owner) {
		return nil
	}
	return (*T)(unsafe.Pointer(o))
}

// nativeObject создаёт нативный объект класса ci с данными типа T (T встраивает
// Object первым полем) и упаковывает его в слот с индексом класса. t может быть
// nil — тогда создаётся нулевая T.
func nativeObject[T any](vm *VM, ci ClassInfo, t *T) Slot {
	if ci == nil {
		return MakeNull()
	}
	if t == nil {
		t = new(T)
	}
	o := (*Object)(unsafe.Pointer(t))
	o.class = ci
	idx := vm.classIndex(ci)
	if idx < 0 {
		return MakeNull()
	}
	return ci.objectSlot(o, idx)
}

// Closure — значение-замыкание: байткод-функция вместе с захваченным
// окружением. Захваченные переменные хранятся в Captures в порядке,
// соответствующем регистрам NumArgs..NumArgs+NumCaptures-1 кадра функции.
//
// BoundThis — $this, связанный при создании замыкания (nil-слот, если
// замыкание создано вне метода). Scope — класс, в котором было определено
// замыкание (self/static).
type Closure struct {
	Fn        *Function
	Captures  []Slot
	BoundThis Slot
	Scope     ClassInfo // self:: замыкания
	Called    ClassInfo // static:: замыкания (late static binding на момент создания)
	// Fake — замыкание создано из callable/метода (ZEND_ACC_FAKE_CLOSURE), а не
	// литералом `function(){}`. Влияет на диагностики Closure::bindTo.
	Fake bool
	// MagicName — имя метода для fake-замыкания, диспетчеризуемого через
	// __call (Closure::fromCallable([$obj, "missing"]) при наличии __call).
	MagicName string
	// objID — PHP-хендл объекта замыкания (#N в var_dump), как у zend_object.
	objID int
	// capUndef — неявные захваты стрелочной функции, не определённые в точке
	// создания: Zend печатает "Undefined variable" при чтении в теле (у нас —
	// при первом вызове), bug81076.
	capUndef []bool
}

// ID возвращает PHP-хендл замыкания (#N) или 0, если он ещё не присвоен.
func (c *Closure) ID() int {
	if c == nil {
		return 0
	}
	return c.objID
}

// MakeClosure боксит замыкание в слот. nil превращается в null.
func MakeClosure(c *Closure) Slot {
	if c == nil {
		return MakeNull()
	}
	return Slot{typ: ClosureType, ptr: unsafe.Pointer(c)}
}

// asClosure возвращает *Closure из слота-замыкания.
func (s Slot) asClosure() *Closure { return (*Closure)(deref(s).ptr) }

// IsClosure сообщает, что слот содержит замыкание.
func (s Slot) IsClosure() bool { return deref(s).typ == ClosureType }

// ClosureID возвращает PHP-хендл замыкания (#N), 0 — если слот не замыкание.
func (s Slot) ClosureID() int {
	s = deref(s)
	if s.typ != ClosureType {
		return 0
	}
	return (*Closure)(s.ptr).objID
}

// asClosureOpt возвращает замыкание и true, если слот — замыкание.
func (s Slot) asClosureOpt() (*Closure, bool) {
	s = deref(s)
	if s.typ != ClosureType {
		return nil, false
	}
	return (*Closure)(s.ptr), true
}

// AsClosure возвращает замыкание слота (nil, если слот — не замыкание).
func (s Slot) AsClosure() *Closure {
	c, _ := s.asClosureOpt()
	return c
}

// Ref — heap-ячейка ссылки: слот-значение, на которое может указывать несколько
// слотов-переменных (alias-переменные, by-ref параметры/возврат). Ссылки
// прозрачны для чтения: deref разворачивает цепочку до значения.
//
// pt — необязательная привязка к типизированному свойству: если ссылка «held by
// property», все записи через ячейку проверяются/приводятся к объявленному типу
// (PHP-семантика reference held by typed property).
type Ref struct {
	v Slot
	// sources — типизированные свойства, «держащие» эту ссылку (Zend
	// zend_reference.sources): запись через ссылку проверяется против каждого
	// из них. Порядок добавления сохраняется — первое свойство называется в
	// диагностиках как prop1 (zend_verify_ref_assignable_zval).
	sources []*propTypeGuard
	// counted — ссылка создана unserialize'ом; маркер `&` в var_dump зависит от
	// числа её вхождений (как Z_REFCOUNT), а не всегда печатается.
	counted bool
}

// MarkCounted помечает ссылку как созданную unserialize (var_dump учитывает
// число вхождений).
func (r *Ref) MarkCounted() { r.counted = true }

// Counted сообщает, что для ссылки нужен подсчёт вхождений при var_dump.
func (r *Ref) Counted() bool { return r != nil && r.counted }

// propTypeGuard описывает типизированное свойство, через ссылку на которое идёт
// запись.
type propTypeGuard struct {
	typ  string
	decl ClassInfo
	name string
}

// addSource добавляет источник (идемпотентно по паре объявление+имя): одна
// ссылка может «удерживаться» несколькими типизированными свойствами.
func (r *Ref) addSource(g *propTypeGuard) {
	if r == nil || g == nil || g.decl == nil {
		return
	}
	for _, s := range r.sources {
		if s.decl == g.decl && s.name == g.name {
			return
		}
	}
	r.sources = append(r.sources, g)
}

// addSourceDup добавляет источник без дедупликации: клонирование объекта
// добавляет новое вхождение того же prop_info (Zend zend_objects_clone_members),
// поэтому уничтожение оригинала не снимает защиту клона.
func (r *Ref) addSourceDup(g *propTypeGuard) {
	if r == nil || g == nil || g.decl == nil {
		return
	}
	r.sources = append(r.sources, g)
}

// delSource снимает один источник по паре объявление+имя (одно вхождение).
func (r *Ref) delSource(decl ClassInfo, name string) {
	if r == nil || decl == nil {
		return
	}
	for i, s := range r.sources {
		if s.decl == decl && s.name == name {
			r.sources = append(r.sources[:i], r.sources[i+1:]...)
			return
		}
	}
}

// delSourceGuard снимает источник по указателю (для случая, когда источник
// известен точно).
func (r *Ref) delSourceGuard(g *propTypeGuard) {
	if r == nil || g == nil {
		return
	}
	for i, s := range r.sources {
		if s == g {
			r.sources = append(r.sources[:i], r.sources[i+1:]...)
			return
		}
	}
}

// guard возвращает первый источник ссылки (ZEND_REF_FIRST_SOURCE) или nil.
func (r *Ref) guard() *propTypeGuard {
	if r == nil || len(r.sources) == 0 {
		return nil
	}
	return r.sources[0]
}

// hasSources сообщает, «удерживается» ли ссылка типизированным свойством.
func (r *Ref) hasSources() bool { return r != nil && len(r.sources) > 0 }

// guardFor собирает guard для свойства pd/decl/name; nil, если тип mixed —
// такая ссылка ничем не ограничена (Zend не создаёт type source для mixed).
func guardFor(pd *propDecl, decl ClassInfo, name string) *propTypeGuard {
	if pd == nil || !pd.hasType || strings.EqualFold(pd.typ, "mixed") {
		return nil
	}
	return &propTypeGuard{typ: pd.typ, decl: decl, name: name}
}

// MakeRef боксит ячейку ссылки в слот. nil превращается в null.
func MakeRef(r *Ref) Slot {
	if r == nil {
		return MakeNull()
	}
	return Slot{typ: RefType, ptr: unsafe.Pointer(r)}
}

// Ref возвращает *Ref из слота-ссылки. Только для typ == RefType.
func (s Slot) Ref() *Ref { return (*Ref)(s.ptr) }

// Deref экспорт разворачивает ссылку до значения (для встроенных функций,
// которым нужен базовый слот без Ref-обёртки).
func Deref(s Slot) Slot { return deref(s) }

// deref разворачивает ссылку до значения (рекурсивно, на случай цепочек).
// Для не-ссылок возвращает слот как есть. Безопасен для всех типов, включая
// NULL-ptr при RefType.
func deref(s Slot) Slot {
	for i := 0; s.typ == RefType && i < 1000; i++ {
		r := s.Ref()
		if r == nil {
			return MakeNull()
		}
		s = r.v
	}
	return s
}

// obj возвращает *Object из объектного слота (шапка по offset 0).
func (s Slot) obj() *Object { return (*Object)(deref(s).ptr) }

// objectOf возвращает *Object из объектного слота (универсальная точка).
func (vm *VM) objectOf(s Slot) *Object {
	s = deref(s)
	if s.typ != ObjectType {
		return nil
	}
	return (*Object)(s.ptr)
}

// SlotObject возвращает *Object из объектного слота (экспорт для builtins).
func (vm *VM) SlotObject(s Slot) *Object { return vm.objectOf(s) }

// classIndex возвращает индекс класса ci в vm.classes (или -1).
func (vm *VM) classIndex(ci ClassInfo) int {
	for i, c := range vm.classes {
		if c == ci {
			return i
		}
	}
	return -1
}

// slotOf упаковывает *Object в слот, выставляя индекс класса.
func (vm *VM) slotOf(o *Object) Slot {
	if o == nil || o.class == nil {
		return MakeNull()
	}
	idx := vm.classIndex(o.class)
	if idx < 0 {
		idx = 0
	}
	return Slot{typ: ObjectType, num: uint64(idx), ptr: unsafe.Pointer(o)}
}

// ObjectPropMap возвращает сырую карту свойств объекта (для builtins).
// Объявленные слоты добавляются как Slot-значения (props-модель).
func (vm *VM) ObjectPropMap(o *Object) map[string]Value {
	if o == nil {
		return nil
	}
	if len(o.slots) == 0 {
		return o.props
	}
	out := make(map[string]Value, len(o.props)+len(o.slots))
	for k, v := range o.props {
		out[k] = v
	}
	rc, ok := asRuntimeClass(o.class)
	if !ok || rc == nil {
		return out
	}
	cs := rc.layout()
	for i, e := range cs.entries {
		if i >= len(o.slots) || o.slots[i].typ == slotUninit {
			continue
		}
		out[e.key] = o.slots[i]
	}
	return out
}

// ObjectPropPairs возвращает инициализированные свойства объекта парами
// (ключ хранения, значение): объявленные слоты + динамические записи.
func (vm *VM) ObjectPropPairs(o *Object) []PropPair {
	if o == nil {
		return nil
	}
	out := make([]PropPair, 0, len(o.slots)+len(o.props))
	if rc, ok := asRuntimeClass(o.class); ok && rc != nil {
		cs := rc.layout()
		for i, e := range cs.entries {
			if i >= len(o.slots) || o.slots[i].typ == slotUninit {
				continue
			}
			out = append(out, PropPair{Key: e.key, Val: o.slots[i]})
		}
	}
	for k, v := range o.props {
		out = append(out, PropPair{Key: k, Val: valueSlot(v)})
	}
	return out
}

// PropPair — пара (ключ хранения свойства, значение) для builtins.
type PropPair struct {
	Key string
	Val Slot
}

// NewObjectOf создаёт объект класса ci без вызова конструктора.
func (vm *VM) NewObjectOf(ci ClassInfo) (Slot, *Object) {
	if ci == nil {
		return MakeNull(), nil
	}
	o := ci.New(&vm.ctx, nil)
	if o == nil {
		return MakeNull(), nil
	}
	idx := vm.classIndex(ci)
	if idx < 0 {
		idx = 0
	}
	vm.markObj(o)
	return ci.objectSlot(o, idx), o
}

// NewObjectOfIndex создаёт объект класса ci и упаковывает с явным индексом.
func (vm *VM) NewObjectOfIndex(ci ClassInfo, idx int) (Slot, *Object) {
	if ci == nil {
		return MakeNull(), nil
	}
	o := ci.New(&vm.ctx, nil)
	if o == nil {
		return MakeNull(), nil
	}
	vm.markObj(o)
	return ci.objectSlot(o, idx), o
}

func (vm *VM) objectTypeName(s Slot) string {
	if ci := vm.classInfo(s); ci != nil {
		return ClassDisplayName(ci.Name())
	}
	return "object"
}

// anonSiteHolder — дескриптор с сайтом объявления анонимного класса.
type anonSiteHolder interface{ anonSiteOf() string }

// SetAnonSite запоминает сайт (file:line) объявления анонимного класса:
// get_class() печатает `Parent@anonymous\x00<file>:<line>$<n>`.
func SetAnonSite(ci ClassInfo, site string) {
	if rc, ok := ci.(*runtimeClass); ok && rc != nil {
		rc.anonSite = site
	}
}

// GetClassNameOf — имя класса для get_class()/ReflectionClass::getName().
// Для анонимных классов Zend печатает `Parent@anonymous\x00<file>:<line>$<n>`.
func GetClassNameOf(ci ClassInfo) string {
	if ci == nil {
		return ""
	}
	name := ci.Name()
	if strings.HasPrefix(name, "\x00anon#") {
		num := strings.TrimPrefix(name, "\x00anon#")
		site := ""
		if h, ok := ci.(anonSiteHolder); ok {
			site = h.anonSiteOf()
		}
		return classDisplayName(ci) + "\x00" + site + "$" + num
	}
	return name
}

// ClassDisplayName преобразует внутреннее имя анонимного класса в
// `class@anonymous` для диагностик и var_dump.
func ClassDisplayName(name string) string {
	if strings.HasPrefix(name, "\x00anon#") {
		return "class@anonymous"
	}
	return name
}

// classDisplayName — обёртка для ClassInfo. Для анонимных классов Zend
// использует имя предка/первого интерфейса: `Parent@anonymous` /
// `Iface@anonymous`, а при их отсутствии — `class@anonymous`.
func classDisplayName(ci ClassInfo) string {
	if ci == nil {
		return ""
	}
	if strings.HasPrefix(ci.Name(), "\x00anon#") {
		if p := ci.Parent(); p != nil {
			return p.Name() + "@anonymous"
		}
		if ifaces := ci.Interfaces(); len(ifaces) > 0 {
			return ifaces[0].Name() + "@anonymous"
		}
		return "class@anonymous"
	}
	return ci.Name()
}

// DisplayClassNameOf — имя класса для дампа/диагностик с учётом анонимных
// классов (предок/первый интерфейс + @anonymous).
func (vm *VM) DisplayClassNameOf(ci ClassInfo) string {
	return classDisplayName(ci)
}

// getProp/setProp/unsetProp — безопасный доступ к свойствам объекта.
func (vm *VM) getProp(s Slot, name string) (Value, bool) {
	if o := vm.objectOf(s); o != nil {
		return o.GetProp(name)
	}
	return nil, false
}

func (vm *VM) setProp(s Slot, name string, v Value) {
	if o := vm.objectOf(s); o != nil {
		o.SetProp(name, v)
	}
}

func (vm *VM) unsetProp(s Slot, name string) {
	if o := vm.objectOf(s); o != nil {
		if _, declared := o.slotIndex(name); !declared {
			if old, ok := o.props[name]; ok {
				vm.noteDroppedValue(old)
			}
		}
		o.UnsetProp(name)
	}
}

// Refs возвращает оценку числа ссылок объекта (lifetime.go): используется
// debug_zval_dump для печати refcount.
func (o *Object) Refs() int32 {
	if o == nil {
		return 0
	}
	return o.refs
}
