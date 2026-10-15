package vm

import "reflect"

// Этот файл реализует поддержку стандартных интерфейсов PHP:
// Iterator, IteratorAggregate, ArrayAccess, Countable, а также обход
// произвольного объекта в foreach (по публичным свойствам).
//
// Поддержка «по имени методов», а не по формальному implements — так
// работает пользовательский код, где методы есть, а интерфейс мог быть
// объявлен только в документации.

// NewBuiltinInterfaces возвращает дескрипторы стандартных интерфейсов PHP,
// чтобы `class X implements Iterator` (и т.п.) компилировался. Методы в
// интерфейсах намеренно не объявлены: проверка наличия методов — на рантайме
// (как в этой VM и устроена поддержка остальных интерфейсов).
func NewBuiltinInterfaces() []ClassInfo {
	traversable := NewInterface("Traversable")
	iterator := NewInterface("Iterator", traversable)
	aggregate := NewInterface("IteratorAggregate", traversable)
	arrayAccess := NewInterface("ArrayAccess")
	countable := NewInterface("Countable")
	stringable := NewInterface("Stringable")
	jsonSerializable := NewInterface("JsonSerializable")
	serializable := NewInterface("Serializable")
	unitEnum := NewInterface("UnitEnum")
	backedEnum := NewInterface("BackedEnum", unitEnum)
	generator := newGeneratorClass(iterator)
	fiber := newFiberClass()
	return []ClassInfo{
		traversable, iterator, aggregate, arrayAccess, countable,
		stringable, jsonSerializable, serializable, unitEnum, backedEnum,
		generator,
		fiber,
	}
}

// newGeneratorClass создаёт дескриптор класса Generator, реализующего Iterator.
// Данные такого объекта — встроенная в *Generator шапка Object.
func newGeneratorClass(iterator ClassInfo) *runtimeClass {
	ci := NewClass("Generator", nil)
	ci.SetFinal()
	ci.SetNoDynamicProps()
	ci.Implements(iterator)
	// Нативный тип данных нужен сборщику времени жизни (lifetime.go): при
	// обходе достижимости он должен видеть сохранённые кадры (regs), вложения
	// yield from (inner) и аргументы генератора.
	ci.nativeType = reflect.TypeOf(Generator{})
	registerGeneratorMembers(ci)
	return ci
}

// isObjectSlot сообщает, что слот (после разыменования) — объект.
func isObjectSlot(s Slot) bool { return deref(s).typ == ObjectType }

// --- Состояние итерации ---

// iterState — PHP-массив с полями:
//
//	"o"    — объект-итератор (или исходный объект для обхода свойств);
//	"mode" — 0: методы Iterator; 1: публичные свойства;
//	"keys" — массив имён свойств (только mode 1);
//	"idx"  — текущая позиция (только mode 1).
const (
	iterModeMethods  = 0
	iterModeProps    = 1
	iterModeArrayRef = 2 // позиционный обход массива по ссылке
	iterModeWeakMap  = 3 // обход пар WeakMap (снимок ключей)
)

func stateGet(a *Array, key string) Slot {
	v, _ := a.Get(MakeStrKey(key))
	return v
}

// iterInit готовит состояние обхода для источника src.
func (vm *VM) iterInit(src Slot) (Slot, error) { return vm.iterInitMode(src, false) }

// iterInitByRef как iterInit, но для foreach по ссылке: дополнительно проверяет,
// что обход источника по ссылке допустим (генератор — только объявленный
// `function &`; обычный Iterator/IteratorAggregate — нельзя).
func (vm *VM) iterInitByRef(src Slot) (Slot, error) { return vm.iterInitMode(src, true) }

const errIterByRefGeneric = "An iterator cannot be used with foreach by reference"
const errGenByRef = "You can only iterate a generator by-reference if it declared that it yields by-reference"

func (vm *VM) iterInitMode(src Slot, byref bool) (Slot, error) {
	state := newArray()
	state.SetStr("mode", MakeInt(iterModeMethods))
	src = deref(src)
	if src.typ != ObjectType {
		// Не объект: пустое состояние (foreach по не-массиву ничего не делает).
		state.SetStr("mode", MakeInt(iterModeProps))
		state.SetStr("keys", MakeArray(newArray()))
		state.SetStr("idx", MakeInt(0))
		return MakeArray(state), nil
	}
	ci := vm.classInfo(src)
	if ci == nil {
		return MakeArray(state), nil
	}
	// WeakMap: обход пар «объект-ключ => значение» (в т.ч. по ссылке).
	// Спец-режим идёт до getIterator: WeakMap::getIterator возвращает только
	// ключи и не даёт by-ref записи значений.
	if ci.Name() == "WeakMap" {
		state.SetStr("mode", MakeInt(iterModeWeakMap))
		state.SetStr("o", src)
		keys := newArray()
		if d := objectAs[weakMapData](vm.objectOf(src), ci); d != nil {
			for _, e := range d.entries {
				_, _ = keys.Append(e.obj)
			}
		}
		state.SetStr("keys", MakeArray(keys))
		state.SetStr("idx", MakeInt(0))
		return MakeArray(state), nil
	}
	// foreach по ссылке: генератор допустим только при by-reference yield.
	if byref {
		if g := vm.genOfSlot(src); g != nil {
			if g.fn == nil || !g.fn.ByRefReturn {
				return Slot{}, NewThrowable("Exception", errGenByRef)
			}
		}
	}
	// IteratorAggregate: getIterator() -> Traversable, обходим его (возможны
	// вложенные IteratorAggregate).
	if m := ci.lookupMethod("getIterator"); m != nil {
		inner := src
		for {
			res, err := vm.invokeMethod(m, inner, vm.classInfo(inner), nil)
			if err != nil {
				return Slot{}, err
			}
			inner = deref(res)
			bad := inner.typ != ObjectType
			var ici ClassInfo
			if !bad {
				ici = vm.classInfo(inner)
				if ici == nil {
					bad = true
				} else if tr := vm.findClass("Traversable"); tr != nil && !ici.isA(tr) {
					bad = true
				}
			}
			if bad {
				return Slot{}, NewThrowable("Exception",
					"Objects returned by "+ci.Name()+"::getIterator() must be traversable or implement interface Iterator")
			}
			if nm := ici.lookupMethod("getIterator"); nm != nil {
				m = nm
				continue
			}
			break
		}
		if byref {
			if g := vm.genOfSlot(inner); g != nil {
				if g.fn == nil || !g.fn.ByRefReturn {
					return Slot{}, NewThrowable("Exception", errGenByRef)
				}
			} else if st, ok := vm.iterInitArrayIterRef(inner); ok {
				return st, nil
			} else {
				return Slot{}, NewThrowable("Error", errIterByRefGeneric)
			}
		}
		state.SetStr("o", inner)
		if err := vm.iterInvoke(inner, "rewind", nil); err != nil {
			return Slot{}, err
		}
		return MakeArray(state), nil
	}
	// Iterator: rewind/valid/current (key опционален).
	if ci.lookupMethod("rewind") != nil && ci.lookupMethod("valid") != nil &&
		ci.lookupMethod("current") != nil {
		// Генератор по ссылке уже отфильтрован выше; ArrayIterator хранит
		// собственное хранилище/объект-источник и допускает обход по ссылке
		// (Zend spl_array_it), прочие Iterator — нет.
		if byref {
			if vm.genOfSlot(src) == nil {
				if st, ok := vm.iterInitArrayIterRef(src); ok {
					return st, nil
				}
				return Slot{}, NewThrowable("Error", errIterByRefGeneric)
			}
		}
		state.SetStr("o", src)
		if err := vm.iterInvoke(src, "rewind", nil); err != nil {
			return Slot{}, err
		}
		return MakeArray(state), nil
	}
	return vm.iterInitPropsMode(src), nil
}

// iterInitPropsMode готовит состояние обхода публичных свойств объекта.
func (vm *VM) iterInitPropsMode(o Slot) Slot {
	state := newArray()
	state.SetStr("mode", MakeInt(iterModeProps))
	state.SetStr("o", o)
	keys := newArray()
	for _, n := range vm.objectPropNames(o) {
		_, _ = keys.Append(MakeString(n))
	}
	state.SetStr("keys", MakeArray(keys))
	state.SetStr("idx", MakeInt(0))
	if ob := vm.objectOf(o); ob != nil {
		state.SetStr("n", MakeInt(int64(len(ob.props))))
	}
	return MakeArray(state)
}

// iterInitArrayIterRef — состояние foreach по ссылке для ArrayIterator.
// Если итератор построен от объекта, обход идёт по ЖИВЫМ свойствам этого
// объекта (как foreach по самому объекту; Zend spl_array_it хранит объект-
// источник), поэтому запись через ссылку видна в объекте и подчиняется типам
// свойств. Иначе обход позиционный по ссылке по собственному массиву
// итератора: запись видна через сам ArrayIterator.
func (vm *VM) iterInitArrayIterRef(it Slot) (Slot, bool) {
	ci := vm.classInfo(it)
	if ci == nil {
		return Slot{}, false
	}
	ai := vm.findClass("ArrayIterator")
	if ai == nil || !ci.isA(ai) {
		return Slot{}, false
	}
	d := objectAs[splArrayData](vm.objectOf(it), ci)
	if d == nil {
		return Slot{}, false
	}
	if s := deref(d.srcObj); s.typ == ObjectType {
		return vm.iterInitPropsMode(s), true
	}
	if d.arr == nil {
		d.arr = newArray()
	}
	// Ячейки-ссылки создаются прямо в хранилище итератора: до начала обхода
	// снимаем COW-копию, чтобы записи не ушли в разделённую копию.
	d.arr = d.arr.Unshare()
	return vm.iterInitArrayRef(MakeArray(d.arr)), true
}

// CheckForeachByRef отклоняет foreach по ссылке по Iterator/IteratorAggregate
// ("An iterator cannot be used with foreach by reference").
func (vm *VM) CheckForeachByRef(obj Slot) error {
	ci := vm.classInfo(deref(obj))
	if ci == nil {
		return nil
	}
	if ci.lookupMethod("getIterator") != nil {
		return NewThrowable("Error", "An iterator cannot be used with foreach by reference")
	}
	if ci.lookupMethod("rewind") != nil && ci.lookupMethod("valid") != nil && ci.lookupMethod("current") != nil {
		return NewThrowable("Error", "An iterator cannot be used with foreach by reference")
	}
	return nil
}

// iterInvoke вызывает метод итератора name без аргументов.
func (vm *VM) iterInvoke(obj Slot, name string, args []Slot) error {
	obj = deref(obj)
	if obj.typ != ObjectType {
		return NewThrowable("Error", "Iterator value is not an object")
	}
	ci := vm.classInfo(obj)
	if ci == nil {
		return NewThrowable("Error", "Iterator value is not an object")
	}
	m := ci.lookupMethod(name)
	if m == nil {
		return NewThrowable("Error", "Call to undefined method "+ci.Name()+"::"+name+"()")
	}
	vm.suppressNextFrame = true
	_, err := vm.invokeMethod(m, obj, ci, args)
	return err
}

// iterCall вызывает метод итератора и возвращает результат.
func (vm *VM) iterCall(obj Slot, name string) (Slot, error) {
	obj = deref(obj)
	if obj.typ != ObjectType {
		return MakeNull(), NewThrowable("Error", "Iterator value is not an object")
	}
	ci := vm.classInfo(obj)
	if ci == nil {
		return MakeNull(), NewThrowable("Error", "Iterator value is not an object")
	}
	m := ci.lookupMethod(name)
	if m == nil {
		return MakeNull(), NewThrowable("Error", "Call to undefined method "+ci.Name()+"::"+name+"()")
	}
	vm.suppressNextFrame = true
	return vm.invokeMethod(m, obj, ci, nil)
}

// iterValid — valid()/есть ли ещё свойство.
func (vm *VM) iterValid(state Slot) (bool, error) {
	a := deref(state).AsArray()
	switch deref(stateGet(a, "mode")).Int() {
	case iterModeArrayRef:
		return iterArrayRefValid(state), nil
	case iterModeProps:
		return vm.iterPropsValid(a), nil
	case iterModeWeakMap:
		return vm.iterWeakMapValid(state), nil
	}
	res, err := vm.iterCall(stateGet(a, "o"), "valid")
	if err != nil {
		return false, err
	}
	return deref(res).ToBool(), nil
}

// iterKey — key()/имя свойства.
func (vm *VM) iterKey(state Slot) (Slot, error) {
	a := deref(state).AsArray()
	switch deref(stateGet(a, "mode")).Int() {
	case iterModeArrayRef:
		return iterArrayRefKey(state), nil
	case iterModeWeakMap:
		return vm.iterWeakMapKey(state), nil
	case iterModeProps:
		keys := deref(stateGet(a, "keys")).AsArray()
		idx := deref(stateGet(a, "idx")).Int()
		if idx < 0 || idx >= int64(keys.Len()) {
			return MakeNull(), nil
		}
		k, _ := keys.Get(intKey(idx))
		name := k.ToString()
		// Динамическое свойство с манглированным именем (`\0A\0b`) выводится
		// в foreach под расманглированным именем (Zend unmangle).
		if n, _, ok := splitMangledProp(name); ok {
			name = n
		}
		return MakeString(name), nil
	}
	return vm.iterCallOptional(stateGet(a, "o"), "key")
}

// iterCallOptional как iterCall, но отсутствие метода даёт null (например,
// key() у итератора, где ключ не используется).
func (vm *VM) iterCallOptional(obj Slot, name string) (Slot, error) {
	obj = deref(obj)
	if obj.typ != ObjectType {
		return MakeNull(), nil
	}
	ci := vm.classInfo(obj)
	if ci == nil || ci.lookupMethod(name) == nil {
		return MakeNull(), nil
	}
	return vm.iterCall(obj, name)
}

// propKeyForSlot — ключ хранения свойства name над объектом o с учётом
// текущей области видимости VM (private — манглированное имя).
func (vm *VM) propKeyForSlot(o Slot, name string) string {
	ci := vm.classInfo(o)
	if ci == nil {
		return name
	}
	return propKeyForScope(vm.ctx.self, ci, name)
}

// iterCurrent — current()/значение свойства.
func (vm *VM) iterCurrent(state Slot) (Slot, error) {
	a := deref(state).AsArray()
	switch deref(stateGet(a, "mode")).Int() {
	case iterModeWeakMap:
		return vm.iterWeakMapCurrent(state, false)
	case iterModeProps:
		keys := deref(stateGet(a, "keys")).AsArray()
		idx := deref(stateGet(a, "idx")).Int()
		if idx < 0 || idx >= int64(keys.Len()) {
			return MakeNull(), nil
		}
		k, _ := keys.Get(intKey(idx))
		o := deref(stateGet(a, "o"))
		if o.typ != ObjectType {
			return MakeNull(), nil
		}
		if v, ok := vm.getProp(o, vm.propKeyForSlot(o, k.ToString())); ok {
			var s Slot
			SlotOf(&s, v)
			return s, nil
		}
		return MakeNull(), nil
	}
	return vm.iterCall(stateGet(a, "o"), "current")
}

// iterCurrentRef — текущее значение обхода как ячейка-ссылка (foreach по
// ссылке). Ссылки поддерживаются только в режиме обхода публичных свойств; для
// Iterator/IteratorAggregate возвращается значение (PHP не позволяет итерировать
// итератор по ссылке).
func (vm *VM) iterCurrentRef(state Slot) (Slot, error) {
	a := deref(state).AsArray()
	switch deref(stateGet(a, "mode")).Int() {
	case iterModeArrayRef:
		return vm.iterArrayRefCurrentRef(state)
	case iterModeProps:
		return vm.iterCurrentRefProps(a)
	case iterModeWeakMap:
		return vm.iterWeakMapCurrent(state, true)
	}
	it := stateGet(a, "o")
	if g := vm.genOfSlot(it); g != nil {
		// Итерировать генератор по ссылке можно, только если он объявлен
		// `function &` (by-reference yield).
		if g.fn == nil || !g.fn.ByRefReturn {
			return MakeNull(), NewThrowable("Exception", "You can only iterate a generator by-reference if it declared that it yields by-reference")
		}
		if err := vm.ensureStarted(g); err != nil {
			return MakeNull(), err
		}
		if g.done || !g.hasCur {
			return MakeRef(&Ref{v: MakeNull()}), nil
		}
		if g.cur.typ == RefType {
			return g.cur, nil
		}
		return MakeRef(&Ref{v: g.cur}), nil
	}
	return vm.iterCall(it, "current")
}

// iterCurrentRefProps — текущее свойство объекта как ячейка-ссылка (foreach по
// ссылке по публичным свойствам).
func (vm *VM) iterCurrentRefProps(a *Array) (Slot, error) {
	keys := deref(stateGet(a, "keys")).AsArray()
	idx := deref(stateGet(a, "idx")).Int()
	if idx < 0 || idx >= int64(keys.Len()) {
		return MakeRef(&Ref{v: MakeNull()}), nil
	}
	k, _ := keys.Get(intKey(idx))
	o := deref(stateGet(a, "o"))
	if o.typ != ObjectType {
		return MakeRef(&Ref{v: MakeNull()}), nil
	}
	name := k.ToString()
	if pd, decl, ok := vm.scopedProp(vm.ctx.self, vm.classInfo(o), name); ok && pd != nil && pd.readonly {
		return MakeNull(), NewThrowable("Error", "Cannot acquire reference to readonly property "+classDisplayName(decl)+"::$"+name)
	}
	return vm.propRefCell(o, name)
}

// iterPropsValid — есть ли ещё видимое «живое» свойство: обход идёт по позиции
// снимка имён, снятые unset()/удалённые свойства пропускаются, добавленные в
// теле цикла свойства дописываются в снимок (Zend обходит таблицу свойств по
// позиции).
func (vm *VM) iterPropsValid(a *Array) bool {
	o := deref(stateGet(a, "o"))
	if o.typ != ObjectType {
		return false
	}
	keys := deref(stateGet(a, "keys")).AsArray()
	if ob := vm.objectOf(o); ob != nil && int64(len(ob.props)) != stateGet(a, "n").Int() {
		seen := map[string]bool{}
		for _, v := range keys.All() {
			seen[v.ToString()] = true
		}
		for _, n := range vm.objectPropNames(o) {
			if !seen[n] {
				_, _ = keys.Append(MakeString(n))
			}
		}
		a.SetStr("n", MakeInt(int64(len(ob.props))))
	}
	idx := stateGet(a, "idx").Int()
	for idx < int64(keys.Len()) {
		k, _ := keys.Get(intKey(idx))
		if vm.iterPropNameLive(o, k.ToString()) {
			a.SetStr("idx", MakeInt(idx))
			return true
		}
		idx++
	}
	a.SetStr("idx", MakeInt(idx))
	return false
}

// iterPropNameLive — свойство видимо и инициализировано: снятые unset() и
// неинициализированные типизированные свойства foreach пропускает.
func (vm *VM) iterPropNameLive(o Slot, name string) bool {
	if vm.objectOf(o) == nil {
		return false
	}
	_, ok := vm.getProp(o, vm.propKeyForSlot(o, name))
	return ok
}

// --- Обход WeakMap ---

// iterWeakMapStateKeys возвращает данные текущего состояния режима 3:
// объект-карту, снимок ключей и позицию.
func (vm *VM) iterWeakMapStateKeys(state Slot) (*weakMapData, *Array, int64, bool) {
	a := deref(state).AsArray()
	if a == nil {
		return nil, nil, 0, false
	}
	o := deref(stateGet(a, "o"))
	if o.typ != ObjectType {
		return nil, nil, 0, false
	}
	d := objectAs[weakMapData](vm.objectOf(o), vm.classInfo(o))
	ks := stateGet(a, "keys")
	if deref(ks).typ != ArrayType {
		return nil, nil, 0, false
	}
	return d, deref(ks).AsArray(), stateGet(a, "idx").Int(), true
}

// iterWeakMapValid — есть ли ещё пара, чей ключ всё ещё в карте (записи
// уничтоженных ключей пропускаем).
func (vm *VM) iterWeakMapValid(state Slot) bool {
	a := deref(state).AsArray()
	d, keys, idx, ok := vm.iterWeakMapStateKeys(state)
	if !ok || d == nil || keys == nil {
		return false
	}
	for idx < int64(keys.Len()) {
		k, _ := keys.Get(intKey(idx))
		if ko := vm.objectOf(k); ko != nil && weakMapFind(d, ko) >= 0 {
			a.SetStr("idx", MakeInt(idx))
			return true
		}
		idx++
	}
	a.SetStr("idx", MakeInt(idx))
	return false
}

// iterWeakMapKey — объект-ключ текущей пары.
func (vm *VM) iterWeakMapKey(state Slot) Slot {
	_, keys, idx, ok := vm.iterWeakMapStateKeys(state)
	if !ok || keys == nil || idx < 0 || idx >= int64(keys.Len()) {
		return MakeNull()
	}
	k, _ := keys.Get(intKey(idx))
	return k
}

// iterWeakMapCurrent — значение текущей пары; asRef — как ячейка-ссылка в самой
// записи карты (foreach по ссылке пишет значение в WeakMap).
func (vm *VM) iterWeakMapCurrent(state Slot, asRef bool) (Slot, error) {
	d, keys, idx, ok := vm.iterWeakMapStateKeys(state)
	if !ok || d == nil || keys == nil || idx < 0 || idx >= int64(keys.Len()) {
		return MakeNull(), nil
	}
	k, _ := keys.Get(intKey(idx))
	ko := vm.objectOf(k)
	if ko == nil {
		return MakeNull(), nil
	}
	i := weakMapFind(d, ko)
	if i < 0 {
		return MakeNull(), nil
	}
	if !asRef {
		return d.entries[i].val, nil
	}
	cur := d.entries[i].val
	if cur.typ == RefType {
		return cur, nil
	}
	ref := MakeRef(&Ref{v: cur})
	d.entries[i].val = ref
	return ref, nil
}

// iterNext — next()/переход к следующему свойству.
func (vm *VM) iterNext(state Slot) error {
	a := deref(state).AsArray()
	switch deref(stateGet(a, "mode")).Int() {
	case iterModeArrayRef:
		a, _, arr := iterArrayRefParts(state)
		if a == nil || arr == nil {
			if a != nil {
				iterArrayRefMarkDone(a)
			}
			return nil
		}
		if iterArrayRefSync(a, arr) {
			// Ячейка указывает на другой массив: курсор уже сброшен на 0,
			// следующая выборка (valid) начнётся с первого элемента.
			return nil
		}
		// Курсор Zend после выборки стоит на pos+1: запоминаем, был ли там
		// живой элемент — удаление под курсором в теле цикла продвинуло бы
		// курсор (см. iterCursor в iterremap.go).
		pos, _ := iterStatePz(a)
		iterStateSetPz(a, pos+1, int(b2i(arr.liveAt(pos+1))))
		return nil
	case iterModeProps:
		idx := deref(stateGet(a, "idx")).Int()
		a.SetStr("idx", MakeInt(idx+1))
		return nil
	case iterModeWeakMap:
		idx := deref(stateGet(a, "idx")).Int()
		a.SetStr("idx", MakeInt(idx+1))
		return nil
	}
	return vm.iterInvoke(stateGet(a, "o"), "next", nil)
}

// --- Позиционный обход массива по ссылке (foreach by reference) ---
//
// В отличие от обхода по значению (снимок ключей), итерация идёт по ЖИВОЙ
// хеш-позиции массива: элементы, добавленные/удалённые в теле цикла, видны
// итератору. Текущий элемент при выборке превращается в настоящую Ref-ячейку
// внутри массива (Zend FE_FETCH_RW), поэтому запись через цель цикла и запись
// в массив — одни и те же данные. Состояние: "cell" — Ref-ячейка источника
// (или сам массив для временного источника), "aid" — идентичность массива,
// iterPz — позиция хранения и признак живости следующего элемента.

// iterStatePz распаковывает позицию курсора и признак "в pos+1 был живой
// элемент" (pos*2 + zlive) из поля состояния.
func iterStatePz(a *Array) (pos, zlive int) {
	p := int(a.iterPz)
	return p >> 1, p & 1
}

// iterStateSetPz упаковывает pz.
func iterStateSetPz(a *Array, pos, zlive int) {
	a.iterPz = int32(pos*2 + zlive)
}

// iterInitArrayRef готовит состояние позиционного обхода массива по ссылке.
func (vm *VM) iterInitArrayRef(src Slot) Slot {
	state := newArray()
	state.iterState = true
	state.SetStr("mode", MakeInt(iterModeArrayRef))
	state.SetStr("cell", src)
	iterStateSetPz(state, 0, 0)
	if v := deref(src); v.typ == ArrayType {
		if arr := v.AsArray(); arr != nil {
			state.SetStr("aid", MakeInt(int64(arr.ensureIterID())))
			iterStateSetPz(state, 0, int(b2i(arr.liveAt(0))))
		}
	}
	return MakeArray(state)
}

// iterArrayRefMarkDone помечает завершённый обход: перестройки массива больше
// не пересчитывают его курсор (состояние может до конца кадра лежать в
// регистре, но цикл уже вышел).
func iterArrayRefMarkDone(a *Array) {
	if a != nil && a.iterState {
		a.iterState = false
	}
}

// iterArrayRefParts разбирает состояние режима 2: PHP-массив состояния,
// слот-ячейку источника и текущий массив (nil, если источник уже не массив).
// Смену объекта массива в ячейке проверяет iterArrayRefSync.
func iterArrayRefParts(state Slot) (*Array, Slot, *Array) {
	a := deref(state).AsArray()
	if a == nil {
		return nil, Slot{}, nil
	}
	cell := stateGet(a, "cell")
	av := deref(cell)
	if av.typ != ArrayType {
		return a, cell, nil
	}
	return a, cell, av.AsArray()
}

// iterArrayRefSync проверяет, что ячейка итератора всё ещё указывает на тот же
// объект массива, что и при выборке. При смене (переприсваивание источника
// вида `$a = ...`) Zend пересинхронизирует хеш-итератор на текущий внутренний
// указатель массива, т.е. следующая выборка идёт с нуля. Возвращает true,
// если позиция была сброшена.
func iterArrayRefSync(a *Array, arr *Array) bool {
	if arr.id == 0 {
		arr.id = newArrayID()
	}
	if ai, ok := a.mStr["aid"]; ok && int(ai) < len(a.entries) {
		if v := a.entries[ai].val; v.typ == IntType && uint64(v.Int()) == arr.id {
			return false
		}
	}
	iterStateSetPz(a, 0, 0)
	a.SetStr("aid", MakeInt(int64(arr.id)))
	return true
}

// iterArrayRefFix разворачивает разделённый (COW) массив ячейки: копия
// записывается обратно в ячейку (Zend SEPARATE_ARRAY перед позиционной
// итерацией по ссылке), чтобы записи из тела шли в тот же массив.
func iterArrayRefFix(state *Array, cell Slot, a *Array) *Array {
	if a == nil || !a.Shared() {
		return a
	}
	na := a.Unshare()
	if cell.typ == RefType {
		if r := cell.Ref(); r != nil {
			r.v = MakeArray(na)
		}
	} else {
		state.SetStr("cell", MakeArray(na))
	}
	return na
}

// iterArrayRefValid — есть ли живой элемент на текущей (или следующей живой)
// позиции; позиция сдвигается за tombstone-ы (mixed после unset). Ложный
// результат означает завершение обхода — состояние помечается завершённым.
func iterArrayRefValid(state Slot) bool {
	a, _, arr := iterArrayRefParts(state)
	if a == nil || arr == nil {
		iterArrayRefMarkDone(a)
		return false
	}
	iterArrayRefSync(a, arr)
	pos, zl := iterStatePz(a)
	n := arr.storeLen()
	start := pos
	for pos < n && !arr.liveAt(pos) {
		pos++
	}
	if pos < n {
		if pos != start {
			iterStateSetPz(a, pos, zl)
		}
		return true
	}
	iterArrayRefMarkDone(a)
	return false
}

// iterArrayRefKey — ключ текущего элемента.
func iterArrayRefKey(state Slot) Slot {
	a, _, arr := iterArrayRefParts(state)
	if a == nil || arr == nil {
		return MakeNull()
	}
	pos, _ := iterStatePz(a)
	k, ok := arr.keyAt(pos)
	if !ok {
		return MakeNull()
	}
	return arrayKeyToSlot(k)
}

// iterArrayRefCurrentRef — текущий элемент как Ref-ячейка: если элемент ещё не
// ссылка, он становится ссылкой прямо в массиве (последняя выбранная цель
// цикла остаётся ссылкой в массиве — как в Zend).
func (vm *VM) iterArrayRefCurrentRef(state Slot) (Slot, error) {
	a, cell, arr := iterArrayRefParts(state)
	if a == nil || arr == nil {
		return MakeRef(&Ref{v: MakeNull()}), nil
	}
	arr = iterArrayRefFix(a, cell, arr)
	pos, _ := iterStatePz(a)
	cur, ok := arr.elementAt(pos)
	if !ok {
		return MakeRef(&Ref{v: MakeNull()}), nil
	}
	if cur.typ == RefType {
		return cur, nil
	}
	ref := MakeRef(&Ref{v: cur})
	arr.storeRefAt(pos, ref)
	return ref, nil
}

// arrayKeyToSlot упаковывает ключ массива в слот (ключ foreach/current).
func arrayKeyToSlot(k ArrayKey) Slot {
	if k.str {
		return MakeString(k.s)
	}
	return MakeInt(k.i)
}

// objectPropNames возвращает имена свойств объекта в порядке PropertyNames.
func (vm *VM) objectPropNames(s Slot) []string {
	o := vm.objectOf(s)
	if o == nil {
		return nil
	}
	all := o.PropertyNames()
	ci := vm.classInfo(s)
	if ci == nil {
		return all
	}
	rc, ok := ci.(*runtimeClass)
	if !ok {
		return all
	}
	scope := vm.ctx.self
	out := all[:0:0]
	for _, n := range all {
		if pd, decl, ok := propInfoForScope(scope, rc, n); ok && pd != nil {
			switch pd.vis {
			case Private:
				if scope != decl {
					continue
				}
			case Protected:
				if !canAccess(Protected, decl, scope) {
					continue
				}
			}
		} else if pd2, decl2 := rc.propDeclOf(n); pd2 != nil {
			// Имя объявлено, но private предка скрыт от scope: foreach его
			// не выводит. Для обычного private, найденного на самом классе
			// объекта, propInfoForScope выше вернул бы ok.
			if pd2.vis == Private && scope != decl2 {
				continue
			}
			if pd2.vis == Protected && !canAccess(Protected, decl2, scope) {
				continue
			}
		}
		out = append(out, n)
	}
	return out
}

// --- Countable ---

// CountBuiltin — реализация count()/sizeof() с поддержкой Countable.
func CountBuiltin(ctx *Context) Slot { return countBuiltin(ctx, "count") }

// SizeofBuiltin — count() под псевдонимом sizeof() (диагностики с именем sizeof).
func SizeofBuiltin(ctx *Context) Slot { return countBuiltin(ctx, "sizeof") }

func countBuiltin(ctx *Context, fn string) Slot {
	args := ctx.Args()
	if len(args) == 0 {
		ctx.Throw(NewThrowable("ArgumentCountError", ArgTypeError(fn, 1, "value", "Countable|array", "none", "", 0)))
		if ctx.HasThrown() {
			return *new(Slot)
		}
		return MakeNull()
	}
	s := deref(args[0])
	mode := int64(0)
	if len(args) >= 2 {
		mode = deref(args[1]).ToInt()
		if mode != 0 && mode != 1 {
			ctx.Throw(NewThrowable("ValueError", fn+"(): Argument #2 ($mode) must be either COUNT_NORMAL or COUNT_RECURSIVE"))
			if ctx.HasThrown() {
				return *new(Slot)
			}
			return MakeNull()
		}
	}
	if s.typ == ObjectType {
		vm := ctx.vm
		if ci := vm.classInfo(s); ci != nil {
			if m := ci.lookupMethod("count"); m != nil {
				res, err := vm.invokeMethod(m, s, ci, nil)
				if err != nil {
					if t, ok := err.(*Throwable); ok {
						ctx.Throw(t)
						if ctx.HasThrown() {
							return *new(Slot)
						}
					}
					ctx.Throw(NewThrowable("Error", err.Error()))
					if ctx.HasThrown() {
						return *new(Slot)
					}
					return MakeNull()
				}
				return MakeInt(deref(res).ToInt())
			}
		}
		ctx.Throw(vm.argTypeError(fn, 1, "value", "Countable|array", vm.objectTypeName(s), "", 0))
		if ctx.HasThrown() {
			return *new(Slot)
		}
		return MakeNull()
	}
	switch s.typ {
	case ArrayType:
		if mode == 1 {
			rec := false
			n := countRecursive(s.AsArray(), map[*Array]bool{}, &rec)
			if rec {
				ctx.vm.Warning(fn + "(): Recursion detected")
			}
			return MakeInt(n)
		}
		return MakeInt(int64(s.AsArray().Len()))
	default:
		ctx.Throw(NewThrowable("TypeError", ArgTypeError(fn, 1, "value", "Countable|array", countValueName(s), "", 0)))
		if ctx.HasThrown() {
			return *new(Slot)
		}
		return MakeNull()
	}
}

// countValueName — имя значения для сообщений count(): bool как true/false.
func countValueName(s Slot) string {
	if s.typ == BoolType {
		if s.ToBool() {
			return "true"
		}
		return "false"
	}
	return s.typ.String()
}

// countRecursive повторяет php_count_recursive: считает элементы верхнего
// уровня и рекурсивно вложенные массивы. Повторный вход в уже считаемый массив
// даёт 0 (защита от бесконечной рекурсии).
func countRecursive(a *Array, visiting map[*Array]bool, rec *bool) int64 {
	if visiting[a] {
		*rec = true
		return 0
	}
	visiting[a] = true
	defer delete(visiting, a)
	cnt := int64(a.Len())
	for _, v := range a.Values() {
		v = deref(v)
		if v.typ == ArrayType {
			cnt += countRecursive(v.AsArray(), visiting, rec)
		}
	}
	return cnt
}

// IsCountableBuiltin — реализация is_countable().
func IsCountableBuiltin(ctx *Context) Slot {
	args := ctx.Args()
	if len(args) == 0 {
		return MakeBool(false)
	}
	s := deref(args[0])
	if s.typ == ArrayType {
		return MakeBool(true)
	}
	if s.typ == ObjectType {
		if ci := ctx.vm.classInfo(s); ci != nil && ci.lookupMethod("count") != nil {
			return MakeBool(true)
		}
	}
	return MakeBool(false)
}
