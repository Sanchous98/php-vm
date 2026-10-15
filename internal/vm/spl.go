package vm

// SPL-классы (стандартная библиотека PHP): ArrayObject, ArrayIterator,
// SplDoublyLinkedList (и его наследники SplStack/SplQueue), SplObjectStorage,
// SplFixedArray.
//
// Все они реализованы как нативные классы: данные объектов — собственные
// Go-структуры, встраивающие Object (splArrayData, SplDoublyLinkedList и т.п.),
// методы — NativeFunction, читающие данные через ctx.This[T](). Интерфейсы
// (ArrayAccess/Countable/Iterator/
// IteratorAggregate) уже проверяются рантаймом по именам методов, поэтому
// здесь важно лишь объявить Implements для instanceof.

import (
	"fmt"
	"sort"
	"strconv"
	"unsafe"
)

// --- Данные объектов ---

// splArrayData — обёртка над PHP-массивом (ArrayObject и ArrayIterator).
// pos — позиция собственного итератора ArrayIterator (по живым элементам).
// Встраивает Object первым полем: шапка и данные — один адрес.
type splArrayData struct {
	Object
	arr   *Array
	flags int64
	pos   int
}

// SplDoublyLinkedList — двусвязный список. Нативные наследники (SplStack,
// SplQueue) встраивают его, поэтому старшие поля остаются по offset 0.
type SplDoublyLinkedList struct {
	Object
	items []Slot
	pos   int
	mode  int64
}

type SplStack struct{ SplDoublyLinkedList }
type SplQueue struct{ SplDoublyLinkedList }

// splObjectEntry — запись SplObjectStorage: объект и связанные данные.
type splObjectEntry struct {
	obj  Slot
	info Slot
}

type splObjectStorageData struct {
	Object
	entries []splObjectEntry
	pos     int
}

// SplFixedArray — SplFixedArray: массив фиксированного размера.
type SplFixedArray struct {
	Object
	items []Slot
}

// --- Вспомогательные ---

func splIfacesByName(ifaces []ClassInfo, name string) ClassInfo {
	for _, c := range ifaces {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

// splKeySlot приводит ключ массива к слоту (int|string).
func splKeySlot(k ArrayKey) Slot {
	if k.IsStr() {
		return MakeString(k.StrVal())
	}
	return MakeInt(k.IntVal())
}

// splArrayEntryAt возвращает pos-й живой элемент массива в порядке следования.
func splArrayEntryAt(a *Array, pos int) (ArrayKey, Slot, bool) {
	if a == nil || pos < 0 {
		return ArrayKey{}, Slot{}, false
	}
	i := 0
	for k, v := range a.All() {
		if i == pos {
			return k, v, true
		}
		i++
	}
	return ArrayKey{}, Slot{}, false
}

// splCopyArray делает независимую (COW-безопасную) копию массива.
func splCopyArray(a *Array) *Array {
	out := newArray()
	if a == nil {
		return out
	}
	for k, v := range a.All() {
		_ = out.Set(k, v)
		if deref(v).typ == ArrayType {
			shareSlot(v)
		}
	}
	return out
}

// splArgObj возвращает Object из аргумента или nil.
func (vm *VM) splArgObj(args []Slot, i int) *Object {
	if i < 0 || i >= len(args) {
		return nil
	}
	s := deref(args[i])
	if s.typ != ObjectType {
		return nil
	}
	return vm.objectOf(s)
}

// --- Регистрация ---

// NewSplClasses создаёт дескрипторы SPL-классов. builtin — уже зарегистрированные
// стандартные интерфейсы (Iterator/ArrayAccess/Countable/...), чтобы использовать
// те же самые дескрипторы (иначе instanceof не совпадёт).
func NewSplClasses(builtin []ClassInfo) []ClassInfo {
	iterator := splIfacesByName(builtin, "Iterator")
	aggregate := splIfacesByName(builtin, "IteratorAggregate")
	arrayAccess := splIfacesByName(builtin, "ArrayAccess")
	countable := splIfacesByName(builtin, "Countable")
	serializable := splIfacesByName(builtin, "Serializable")
	jsonSerializable := splIfacesByName(builtin, "JsonSerializable")

	seekable := NewInterface("SeekableIterator", iterator)

	arrayObject := RegisterClass[splArrayData]("ArrayObject", nil)
	arrayIterator := RegisterClass[splArrayData]("ArrayIterator", nil)
	objStorage := RegisterClass[splObjectStorageData]("SplObjectStorage", nil)
	weakMap := RegisterClass[weakMapData]("WeakMap", nil)
	weakMap.SetNoDynamicProps()
	weakMap.SetNoAppend()
	weakMap.SetNotSerializable()
	weakRef := RegisterClass[weakReferenceData]("WeakReference", nil)
	weakRef.SetNoDynamicProps()
	weakRef.SetFinal()
	weakRef.SetNoDynamicProps()
	weakRef.SetNotSerializable()
	multiIter := RegisterClass[multipleIteratorData]("MultipleIterator", nil)
	emptyIter := NewClass("EmptyIterator", nil)
	emptyIter.Implements(iterator)
	emptyIter.Method("current", &Function{Name: "current", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			return Slot{}, NewThrowable("BadMethodCallException", "Accessing the value of an EmptyIterator")
		}}})
	emptyIter.Method("key", &Function{Name: "key", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			return Slot{}, NewThrowable("BadMethodCallException", "Accessing the key of an EmptyIterator")
		}}})
	emptyIter.Method("next", &Function{Name: "next", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) { return MakeNull(), nil }}})
	emptyIter.Method("rewind", &Function{Name: "rewind", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) { return MakeNull(), nil }}})
	emptyIter.Method("valid", &Function{Name: "valid", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) { return MakeBool(false), nil }}})
	fixedArray := RegisterClass[SplFixedArray]("SplFixedArray", nil)
	dll := RegisterClass[SplDoublyLinkedList]("SplDoublyLinkedList", nil)
	stack := RegisterClass[SplStack]("SplStack", dll)
	queue := RegisterClass[SplQueue]("SplQueue", dll)
	fileInfo := NewClass("SplFileInfo", nil)
	fileObject := NewClass("SplFileObject", fileInfo)
	fileInfo.SetNotSerializable()
	fileObject.SetNotSerializable()
	registerSplFileMembers(fileInfo, fileObject)
	// RecursiveFilterIterator — абстрактный SPL-класс (нужен, в частности,
	// чтобы unserialize() сообщал "Cannot instantiate abstract class").
	recFilterIter := NewClass("RecursiveFilterIterator", nil)
	recFilterIter.SetAbstract()

	arrayObject.Implements(aggregate, arrayAccess, countable, serializable)
	arrayIterator.Implements(seekable, arrayAccess, countable, serializable)
	objStorage.Implements(iterator, countable, serializable, arrayAccess)
	weakMap.Implements(aggregate, countable, arrayAccess)
	multiIter.Implements(iterator)
	registerMultipleIteratorMembers(multiIter)
	fixedArray.Implements(aggregate, arrayAccess, countable, jsonSerializable)
	dll.Implements(iterator, arrayAccess, countable, serializable)

	registerSplArrayMembers(arrayObject, false)
	registerSplArrayMembers(arrayIterator, true)
	registerSplArraySerialize(arrayObject)
	registerSplArraySerialize(arrayIterator)
	registerSplListMembers(dll)
	registerSplListMembers(stack)
	registerSplListMembers(queue)
	declareSplListConsts(dll)
	declareSplListConsts(stack)
	declareSplListConsts(queue)
	registerSplStackMembers(stack)
	registerSplQueueMembers(queue)
	registerSplObjectStorageMembers(objStorage)
	registerSplObjectStorageSerialize(objStorage)
	registerSplFixedArrayMembers(fixedArray)
	registerWeakMapMembers(weakMap)
	registerWeakReferenceMembers(weakRef)

	// Порядок: сначала интерфейс, затем классы.
	// SplObserver/SplSubject — маркерные интерфейсы SPL (реализуются
	// пользовательскими классами; собственных методов нет).
	observer := NewInterface("SplObserver", nil)
	subject := NewInterface("SplSubject", nil)

	return []ClassInfo{seekable, arrayObject, arrayIterator, objStorage, multiIter, emptyIter, fixedArray, dll, stack, queue, fileInfo, fileObject, weakMap, weakRef, recFilterIter, observer, subject}
}

// registerSplObjectStorageSerialize реализует нативные __serialize/__unserialize
// для SplObjectStorage: [плоский список obj/info, пустой массив].
func registerSplObjectStorageSerialize(ci *runtimeClass) {
	ci.Method("__serialize", &Function{Name: "__serialize", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			ret := NewArray()
			pairs := NewArray()
			if d := ctx.This[splObjectStorageData](); d != nil {
				var idx int64
				for _, e := range d.entries {
					pairs.Set(MakeIntKey(idx), e.obj)
					idx++
					pairs.Set(MakeIntKey(idx), e.info)
					idx++
				}
			}
			ret.Set(MakeIntKey(0), MakeArray(pairs))
			ret.Set(MakeIntKey(1), MakeArray(NewArray()))
			return MakeArray(ret), nil
		},
	}})
	ci.Method("__unserialize", &Function{Name: "__unserialize", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			if len(args) == 0 {
				return MakeNull(), nil
			}
			d := ctx.This[splObjectStorageData]()
			if d == nil {
				return MakeNull(), nil
			}
			data := args[0].AsArray()
			d.entries = nil
			d.pos = 0
			if data == nil {
				return MakeNull(), nil
			}
			var pairs *Array
			if v, ok := data.Get(MakeIntKey(0)); ok {
				pairs = v.AsArray()
			}
			if pairs == nil {
				return MakeNull(), nil
			}
			vals := pairs.Values()
			for i := 0; i+1 < len(vals); i += 2 {
				e := splObjectEntry{obj: vals[i], info: vals[i+1]}
				if e.info.Type() == NullType {
					e.info = MakeNull()
				}
				d.entries = append(d.entries, e)
			}
			return MakeNull(), nil
		},
	}})
}

// registerSplArraySerialize реализует нативные __serialize/__unserialize для
func registerSplArraySerialize(ci *runtimeClass) {
	ci.Method("__serialize", &Function{Name: "__serialize", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			ret := NewArray()
			var flags int64
			var storage *Array
			if d := ctx.This[splArrayData](); d != nil {
				flags = d.flags
				storage = d.arr
			}
			ret.Set(MakeIntKey(0), MakeInt(flags))
			if storage == nil {
				storage = NewArray()
			}
			ret.Set(MakeIntKey(1), MakeArray(storage))
			props := NewArray()
			if o := ctx.ThisObj(); o != nil {
				for k, v := range o.props {
					var s Slot
					SlotOf(&s, v)
					props.Set(MakeStrKey(k), s)
				}
			}
			ret.Set(MakeIntKey(2), MakeArray(props))
			ret.Set(MakeIntKey(3), MakeNull())
			return MakeArray(ret), nil
		},
	}})
	ci.Method("__unserialize", &Function{Name: "__unserialize", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			if len(args) == 0 {
				return MakeNull(), nil
			}
			data := args[0].AsArray()
			if data == nil {
				return Slot{}, NewThrowable("UnexpectedValueException", "Incomplete or ill-typed serialization data")
			}
			// Структура, которую принимает Zend spl_array_unserialize:
			// [0 => int flags, 1 => array storage, 2 => array props, 3 => ...].
			k0, ok0 := data.Get(MakeIntKey(0))
			k1, ok1 := data.Get(MakeIntKey(1))
			k2, ok2 := data.Get(MakeIntKey(2))
			if !ok0 || !ok1 || !ok2 || k0.Type() != IntType || k1.AsArray() == nil || k2.AsArray() == nil {
				return Slot{}, NewThrowable("UnexpectedValueException", "Incomplete or ill-typed serialization data")
			}
			o := ctx.ThisObj()
			if d := ctx.This[splArrayData](); d != nil {
				d.flags = k0.ToInt()
				d.arr = k1.AsArray()
			}
			if o != nil {
				for k, val := range k2.AsArray().All() {
					if k.IsStr() {
						o.SetProp(k.StrVal(), val)
					} else {
						o.SetProp(strconv.FormatInt(k.IntVal(), 10), val)
					}
				}
			}
			return MakeNull(), nil
		},
	}})
}

// registerSplFileMembers регистрирует минимальные конструкторы SplFileInfo/
func registerSplFileMembers(fileInfo, fileObject *runtimeClass) {
	ctor := func(ctx *Context, args []Slot) (Slot, error) {
		if o := ctx.ThisObj(); o != nil && len(args) > 0 {
			o.SetProp("path", args[0])
		}
		return MakeNull(), nil
	}
	fileInfo.Constructor(&Function{Name: "__construct", NumArgs: 1, Executable: &NativeFunction{fn: ctor}})
	fileObject.Constructor(&Function{Name: "__construct", NumArgs: 1, Executable: &NativeFunction{fn: ctor}})
	fileInfo.Method("getPathname", &Function{Name: "getPathname", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			if o := ctx.ThisObj(); o != nil {
				if v, ok := o.GetProp("path"); ok {
					var s Slot
					SlotOf(&s, v)
					return s, nil
				}
			}
			return MakeString(""), nil
		},
	}})
}

// --- MultipleIterator ---

type multipleIteratorData struct {
	Object
	iterators []Slot
	infos     []Slot
	flags     int64
	started   bool
}

func registerMultipleIteratorMembers(ci *runtimeClass) {
	const (
		mitNeedAny   = 0
		mitNeedAll   = 1
		mitKeysAssoc = 2
	)
	ci.Constructor(&Function{Name: "__construct", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[multipleIteratorData]()
			if d == nil {
				return MakeNull(), nil
			}
			d.flags = mitNeedAll
			if len(args) > 0 {
				d.flags = args[0].ToInt()
			}
			return MakeNull(), nil
		},
	}})
	ci.Method("attachIterator", &Function{Name: "attachIterator", NumArgs: 2, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[multipleIteratorData]()
			if d == nil || len(args) == 0 {
				return MakeNull(), nil
			}
			d.iterators = append(d.iterators, args[0])
			var info Slot = MakeNull()
			if len(args) > 1 {
				info = args[1]
			}
			d.infos = append(d.infos, info)
			return MakeNull(), nil
		},
	}})
	ci.Method("count", &Function{Name: "count", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[multipleIteratorData]()
			if d == nil {
				return MakeInt(0), nil
			}
			return MakeInt(int64(len(d.iterators))), nil
		},
	}})
	ci.Method("rewind", &Function{Name: "rewind", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[multipleIteratorData]()
			if d == nil {
				return MakeNull(), nil
			}
			for _, it := range d.iterators {
				if err := ctx.vm.iterInvoke(it, "rewind", nil); err != nil {
					return Slot{}, err
				}
			}
			d.started = true
			return MakeNull(), nil
		},
	}})
	ci.Method("valid", &Function{Name: "valid", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[multipleIteratorData]()
			if d == nil {
				return MakeBool(false), nil
			}
			all := true
			any := false
			for _, it := range d.iterators {
				ok, err := ctx.vm.iterValid2(it)
				if err != nil {
					return Slot{}, err
				}
				if ok {
					any = true
				} else {
					all = false
				}
			}
			if d.flags&mitNeedAll != 0 {
				return MakeBool(all), nil
			}
			return MakeBool(any), nil
		},
	}})
	ci.Method("current", &Function{Name: "current", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[multipleIteratorData]()
			if d == nil {
				return MakeNull(), nil
			}
			out := newArray()
			for _, it := range d.iterators {
				v, err := ctx.vm.iterCall(it, "current")
				if err != nil {
					return Slot{}, err
				}
				_, _ = out.Append(v)
			}
			return MakeArray(out), nil
		},
	}})
	ci.Method("key", &Function{Name: "key", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[multipleIteratorData]()
			if d == nil {
				return MakeNull(), nil
			}
			out := newArray()
			for i, it := range d.iterators {
				k, err := ctx.vm.iterCallOptional(it, "key")
				if err != nil {
					return Slot{}, err
				}
				if d.flags&mitKeysAssoc != 0 && i < len(d.infos) && deref(d.infos[i]).typ != NullType {
					_ = out.SetStr(deref(d.infos[i]).ToString(), k)
					continue
				}
				_, _ = out.Append(k)
			}
			return MakeArray(out), nil
		},
	}})
	ci.Method("next", &Function{Name: "next", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[multipleIteratorData]()
			if d == nil {
				return MakeNull(), nil
			}
			for _, it := range d.iterators {
				if err := ctx.vm.iterInvoke(it, "next", nil); err != nil {
					return Slot{}, err
				}
			}
			return MakeNull(), nil
		},
	}})
}

// iterValid2 — valid() для объекта-итератора (как iterValid, но для слота).
func (vm *VM) iterValid2(it Slot) (bool, error) {
	res, err := vm.iterCall(it, "valid")
	if err != nil {
		return false, err
	}
	return deref(res).ToBool(), nil
}

// --- ArrayObject / ArrayIterator ---

// SplArrayStorage возвращает внутренний массив ArrayObject/ArrayIterator (для
// var_export: get_properties_for этих классов отдаёт storage-массив). ok=false
// для прочих объектов.
func (vm *VM) SplArrayStorage(o *Object) (*Array, bool) {
	if o == nil {
		return nil, false
	}
	rc, ok := asRuntimeClass(o.Class())
	if !ok || rc == nil {
		return nil, false
	}
	for k := rc; k != nil; k = k.parent {
		switch k.name {
		case "ArrayObject", "ArrayIterator":
			d := (*splArrayData)(unsafe.Pointer(o))
			if d.arr == nil {
				return newArray(), true
			}
			return d.arr, true
		}
	}
	return nil, false
}

func registerSplArrayMembers(ci *runtimeClass, isIterator bool) {
	ci.DefineConst("STD_PROP_LIST", MakeInt(1))
	ci.DefineConst("ARRAY_AS_PROPS", MakeInt(2))

	ci.Constructor(&Function{Name: "__construct", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splArrayData]()
			if d == nil {
				return MakeNull(), nil
			}
			if len(args) > 1 && !deref(args[1]).IsNull() {
				d.flags = args[1].ToInt()
			}
			if len(args) > 0 {
				d.arr = ctx.vm.splArrayFromArg(args[0])
			} else {
				d.arr = newArray()
			}
			d.pos = 0
			return MakeNull(), nil
		},
	}})

	ci.Method("offsetExists", &Function{Name: "offsetExists", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splArrayData]()
			if d == nil || d.arr == nil || len(args) == 0 {
				return MakeBool(false), nil
			}
			k, ok := ctx.vm.KeyOfWarn(args[0])
			if !ok {
				return MakeBool(false), nil
			}
			v, ok := d.arr.Get(k)
			return MakeBool(ok && deref(v).typ != NullType), nil
		},
	}})

	ci.Method("offsetGet", &Function{Name: "offsetGet", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splArrayData]()
			if d == nil || d.arr == nil || len(args) == 0 {
				return MakeNull(), nil
			}
			k, ok := ctx.vm.KeyOfWarn(args[0])
			if !ok {
				return MakeNull(), nil
			}
			v, _ := d.arr.Get(k)
			return v, nil
		},
	}})

	ci.Method("offsetSet", &Function{Name: "offsetSet", NumArgs: 2, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splArrayData]()
			if d == nil || len(args) < 2 {
				return MakeNull(), nil
			}
			if d.arr == nil {
				d.arr = newArray()
			}
			a := d.arr.Unshare()
			if deref(args[0]).IsNull() {
				_, _ = a.Append(args[1])
			} else if k, ok := ctx.vm.KeyOfWarn(args[0]); ok {
				_ = a.Set(k, args[1])
			}
			d.arr = a
			return MakeNull(), nil
		},
	}})

	ci.Method("offsetUnset", &Function{Name: "offsetUnset", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splArrayData]()
			if d == nil || d.arr == nil || len(args) == 0 {
				return MakeNull(), nil
			}
			if k, ok := ctx.vm.KeyOfWarn(args[0]); ok {
				a := d.arr.Unshare()
				a.Unset(k)
				d.arr = a
			}
			return MakeNull(), nil
		},
	}})

	ci.Method("count", &Function{Name: "count", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splArrayData]()
			if d == nil || d.arr == nil {
				return MakeInt(0), nil
			}
			return MakeInt(int64(d.arr.Len())), nil
		},
	}})

	ci.Method("append", &Function{Name: "append", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splArrayData]()
			if d == nil || len(args) == 0 {
				return MakeNull(), nil
			}
			if d.arr == nil {
				d.arr = newArray()
			}
			a := d.arr.Unshare()
			_, _ = a.Append(args[0])
			d.arr = a
			return MakeNull(), nil
		},
	}})

	ci.Method("getArrayCopy", &Function{Name: "getArrayCopy", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splArrayData]()
			if d == nil {
				return MakeArray(newArray()), nil
			}
			if d.arr == nil {
				d.arr = newArray()
			}
			d.arr.MarkShared()
			return MakeArray(d.arr), nil
		},
	}})

	ci.Method("exchangeArray", &Function{Name: "exchangeArray", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splArrayData]()
			if d == nil {
				return MakeNull(), nil
			}
			old := d.arr
			if old == nil {
				old = newArray()
			}
			if len(args) > 0 {
				d.arr = ctx.vm.splArrayFromArg(args[0])
			} else {
				d.arr = newArray()
			}
			d.pos = 0
			return MakeArray(old), nil
		},
	}})

	ci.Method("asort", &Function{Name: "asort", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splArrayData]()
			if d == nil || d.arr == nil {
				return MakeBool(true), nil
			}
			d.arr = splSortArray(ctx.vm, d.arr, true)
			d.pos = 0
			return MakeBool(true), nil
		},
	}})

	ci.Method("ksort", &Function{Name: "ksort", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splArrayData]()
			if d == nil || d.arr == nil {
				return MakeBool(true), nil
			}
			d.arr = splSortArray(ctx.vm, d.arr, false)
			d.pos = 0
			return MakeBool(true), nil
		},
	}})

	ci.Method("getFlags", &Function{Name: "getFlags", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splArrayData]()
			if d == nil {
				return MakeInt(0), nil
			}
			return MakeInt(d.flags), nil
		},
	}})

	ci.Method("setFlags", &Function{Name: "setFlags", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splArrayData]()
			if d == nil || len(args) == 0 {
				return MakeNull(), nil
			}
			d.flags = args[0].ToInt()
			return MakeNull(), nil
		},
	}})

	if isIterator {
		ci.Method("rewind", &Function{Name: "rewind", NumArgs: 0, Executable: &NativeFunction{
			fn: func(ctx *Context, args []Slot) (Slot, error) {
				if d := ctx.This[splArrayData](); d != nil {
					d.pos = 0
				}
				return MakeNull(), nil
			},
		}})
		ci.Method("valid", &Function{Name: "valid", NumArgs: 0, Executable: &NativeFunction{
			fn: func(ctx *Context, args []Slot) (Slot, error) {
				d := ctx.This[splArrayData]()
				if d == nil || d.arr == nil {
					return MakeBool(false), nil
				}
				return MakeBool(d.pos >= 0 && d.pos < d.arr.Len()), nil
			},
		}})
		ci.Method("current", &Function{Name: "current", NumArgs: 0, Executable: &NativeFunction{
			fn: func(ctx *Context, args []Slot) (Slot, error) {
				d := ctx.This[splArrayData]()
				if d == nil {
					return MakeNull(), nil
				}
				_, v, ok := splArrayEntryAt(d.arr, d.pos)
				if !ok {
					return MakeNull(), nil
				}
				return v, nil
			},
		}})
		ci.Method("key", &Function{Name: "key", NumArgs: 0, Executable: &NativeFunction{
			fn: func(ctx *Context, args []Slot) (Slot, error) {
				d := ctx.This[splArrayData]()
				if d == nil {
					return MakeNull(), nil
				}
				k, _, ok := splArrayEntryAt(d.arr, d.pos)
				if !ok {
					return MakeNull(), nil
				}
				return splKeySlot(k), nil
			},
		}})
		ci.Method("next", &Function{Name: "next", NumArgs: 0, Executable: &NativeFunction{
			fn: func(ctx *Context, args []Slot) (Slot, error) {
				if d := ctx.This[splArrayData](); d != nil {
					d.pos++
				}
				return MakeNull(), nil
			},
		}})
		ci.Method("seek", &Function{Name: "seek", NumArgs: 1, Executable: &NativeFunction{
			fn: func(ctx *Context, args []Slot) (Slot, error) {
				d := ctx.This[splArrayData]()
				if d == nil || len(args) == 0 {
					return MakeNull(), nil
				}
				p := int(args[0].ToInt())
				if d.arr != nil && (p < 0 || p >= d.arr.Len()) {
					ctx.Throw(NewThrowable("OutOfBoundsException", "Seek position "+fmt.Sprint(p)+" is out of range"))
					if ctx.HasThrown() {
						return *new(Slot), *new(error)
					}
					return MakeNull(), nil
				}
				d.pos = p
				return MakeNull(), nil
			},
		}})
	} else {
		// ArrayObject: IteratorAggregate.
		ci.Method("getIterator", &Function{Name: "getIterator", NumArgs: 0, Executable: &NativeFunction{
			fn: func(ctx *Context, args []Slot) (Slot, error) {
				d := ctx.This[splArrayData]()
				itClass := ctx.vm.findClass("ArrayIterator")
				if d == nil || itClass == nil {
					return MakeNull(), nil
				}
				return nativeObject(ctx.vm, itClass, &splArrayData{arr: d.arr, flags: d.flags}), nil
			},
		}})
		ci.Method("getIteratorClass", &Function{Name: "getIteratorClass", NumArgs: 0, Executable: &NativeFunction{
			fn: func(ctx *Context, args []Slot) (Slot, error) {
				return MakeString("ArrayIterator"), nil
			},
		}})
		ci.Method("setIteratorClass", &Function{Name: "setIteratorClass", NumArgs: 1, Executable: &NativeFunction{
			fn: func(ctx *Context, args []Slot) (Slot, error) {
				return MakeNull(), nil
			},
		}})
	}
}

// splArrayFromArg строит хранимый массив из аргумента конструктора/обмена:
// массив копируется (COW), объект — по публичным свойствам, иначе пустой.
func (vm *VM) splArrayFromArg(s Slot) *Array {
	s = deref(s)
	switch s.typ {
	case ArrayType:
		src := s.AsArray()
		src.MarkShared()
		return src.Unshare()
	case ObjectType:
		arr := newArray()
		o := vm.objectOf(s)
		if o != nil {
			for _, n := range o.PropertyNames() {
				if v, ok := o.GetProp(n); ok {
					var sl Slot
					SlotOf(&sl, v)
					_ = arr.Set(stringKey(n), sl)
				}
			}
		}
		return arr
	}
	return newArray()
}

// splSortArray сортирует массив, сохраняя ключи: byValue — asort, иначе ksort.
func splSortArray(vm *VM, a *Array, byValue bool) *Array {
	type ent struct {
		k ArrayKey
		v Slot
	}
	var ents []ent
	for k, v := range a.All() {
		ents = append(ents, ent{k, v})
	}
	sort.SliceStable(ents, func(i, j int) bool {
		if byValue {
			return compareSlots(vm, ents[i].v, ents[j].v) < 0
		}
		return compareSlots(vm, splKeySlot(ents[i].k), splKeySlot(ents[j].k)) < 0
	})
	out := newArray()
	for _, e := range ents {
		_ = out.Set(e.k, e.v)
	}
	return out
}

// --- SplDoublyLinkedList / SplStack / SplQueue ---

func declareSplListConsts(ci *runtimeClass) {
	ci.DefineConst("IT_MODE_LIFO", MakeInt(2))
	ci.DefineConst("IT_MODE_FIFO", MakeInt(0))
	ci.DefineConst("IT_MODE_DELETE", MakeInt(1))
	ci.DefineConst("IT_MODE_KEEP", MakeInt(0))
}

func registerSplListMembers(ci *runtimeClass) {
	ci.Method("push", &Function{Name: "push", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplDoublyLinkedList]()
			if d == nil || len(args) == 0 {
				return MakeNull(), nil
			}
			d.items = append(d.items, args[0])
			return MakeNull(), nil
		},
	}})

	ci.Method("pop", &Function{Name: "pop", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplDoublyLinkedList]()
			if d == nil || len(d.items) == 0 {
				ctx.Throw(NewThrowable("RuntimeException", "Can't pop from an empty datastructure"))
				if ctx.HasThrown() {
					return *new(Slot), *new(error)
				}
				return MakeNull(), nil
			}
			v := d.items[len(d.items)-1]
			d.items = d.items[:len(d.items)-1]
			return v, nil
		},
	}})

	ci.Method("shift", &Function{Name: "shift", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplDoublyLinkedList]()
			if d == nil || len(d.items) == 0 {
				ctx.Throw(NewThrowable("RuntimeException", "Can't shift from an empty datastructure"))
				if ctx.HasThrown() {
					return *new(Slot), *new(error)
				}
				return MakeNull(), nil
			}
			v := d.items[0]
			d.items = append([]Slot{}, d.items[1:]...)
			return v, nil
		},
	}})

	ci.Method("unshift", &Function{Name: "unshift", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplDoublyLinkedList]()
			if d == nil || len(args) == 0 {
				return MakeNull(), nil
			}
			d.items = append([]Slot{args[0]}, d.items...)
			return MakeNull(), nil
		},
	}})

	ci.Method("top", &Function{Name: "top", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplDoublyLinkedList]()
			if d == nil || len(d.items) == 0 {
				ctx.Throw(NewThrowable("RuntimeException", "Can't peek at an empty datastructure"))
				if ctx.HasThrown() {
					return *new(Slot), *new(error)
				}
				return MakeNull(), nil
			}
			return d.items[len(d.items)-1], nil
		},
	}})

	ci.Method("bottom", &Function{Name: "bottom", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplDoublyLinkedList]()
			if d == nil || len(d.items) == 0 {
				ctx.Throw(NewThrowable("RuntimeException", "Can't peek at an empty datastructure"))
				if ctx.HasThrown() {
					return *new(Slot), *new(error)
				}
				return MakeNull(), nil
			}
			return d.items[0], nil
		},
	}})

	ci.Method("count", &Function{Name: "count", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplDoublyLinkedList]()
			if d == nil {
				return MakeInt(0), nil
			}
			return MakeInt(int64(len(d.items))), nil
		},
	}})

	ci.Method("isEmpty", &Function{Name: "isEmpty", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplDoublyLinkedList]()
			return MakeBool(d == nil || len(d.items) == 0), nil
		},
	}})

	ci.Method("add", &Function{Name: "add", NumArgs: 2, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplDoublyLinkedList]()
			if d == nil || len(args) < 2 {
				return MakeNull(), nil
			}
			idx := int(args[0].ToInt())
			if idx < 0 || idx > len(d.items) {
				ctx.Throw(NewThrowable("OutOfRangeException", "Offset out of range"))
				if ctx.HasThrown() {
					return *new(Slot), *new(error)
				}
				return MakeNull(), nil
			}
			d.items = append(d.items, Slot{})
			copy(d.items[idx+1:], d.items[idx:])
			d.items[idx] = args[1]
			return MakeNull(), nil
		},
	}})

	ci.Method("offsetExists", &Function{Name: "offsetExists", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplDoublyLinkedList]()
			if d == nil || len(args) == 0 {
				return MakeBool(false), nil
			}
			i := int(args[0].ToInt())
			return MakeBool(i >= 0 && i < len(d.items)), nil
		},
	}})

	ci.Method("offsetGet", &Function{Name: "offsetGet", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplDoublyLinkedList]()
			if d == nil || len(args) == 0 {
				return MakeNull(), nil
			}
			i := int(args[0].ToInt())
			if i < 0 || i >= len(d.items) {
				ctx.Throw(NewThrowable("OutOfRangeException", "Offset invalid or out of range"))
				if ctx.HasThrown() {
					return *new(Slot), *new(error)
				}
				return MakeNull(), nil
			}
			return d.items[i], nil
		},
	}})

	ci.Method("offsetSet", &Function{Name: "offsetSet", NumArgs: 2, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplDoublyLinkedList]()
			if d == nil || len(args) < 2 {
				return MakeNull(), nil
			}
			i := int(args[0].ToInt())
			if i < 0 || i >= len(d.items) {
				ctx.Throw(NewThrowable("OutOfRangeException", "Offset invalid or out of range"))
				if ctx.HasThrown() {
					return *new(Slot), *new(error)
				}
				return MakeNull(), nil
			}
			d.items[i] = args[1]
			return MakeNull(), nil
		},
	}})

	ci.Method("offsetUnset", &Function{Name: "offsetUnset", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplDoublyLinkedList]()
			if d == nil || len(args) == 0 {
				return MakeNull(), nil
			}
			i := int(args[0].ToInt())
			if i < 0 || i >= len(d.items) {
				ctx.Throw(NewThrowable("OutOfRangeException", "Offset invalid or out of range"))
				if ctx.HasThrown() {
					return *new(Slot), *new(error)
				}
				return MakeNull(), nil
			}
			d.items = append(d.items[:i], d.items[i+1:]...)
			if d.pos > len(d.items) {
				d.pos = len(d.items)
			}
			return MakeNull(), nil
		},
	}})

	ci.Method("getIteratorMode", &Function{Name: "getIteratorMode", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplDoublyLinkedList]()
			if d == nil {
				return MakeInt(0), nil
			}
			return MakeInt(d.mode), nil
		},
	}})

	ci.Method("setIteratorMode", &Function{Name: "setIteratorMode", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplDoublyLinkedList]()
			if d == nil || len(args) == 0 {
				return MakeInt(0), nil
			}
			o := ctx.ThisObj()
			if o != nil && o.ClassName() != "SplDoublyLinkedList" {
				ctx.Throw(NewThrowable("RuntimeException", "Iterators' LIFO/FIFO modes for SplStack/SplQueue objects are frozen"))
				if ctx.HasThrown() {
					return *new(Slot), *new(error)
				}
				return MakeNull(), nil
			}
			d.mode = args[0].ToInt()
			return MakeInt(d.mode), nil
		},
	}})

	ci.Method("rewind", &Function{Name: "rewind", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			if d := ctx.This[SplDoublyLinkedList](); d != nil {
				d.pos = 0
			}
			return MakeNull(), nil
		},
	}})

	ci.Method("valid", &Function{Name: "valid", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplDoublyLinkedList]()
			return MakeBool(d != nil && d.pos >= 0 && d.pos < len(d.items)), nil
		},
	}})

	ci.Method("current", &Function{Name: "current", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplDoublyLinkedList]()
			if d == nil {
				return MakeNull(), nil
			}
			i, ok := splListPhysicalIndex(d)
			if !ok {
				return MakeNull(), nil
			}
			return d.items[i], nil
		},
	}})

	ci.Method("key", &Function{Name: "key", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplDoublyLinkedList]()
			if d == nil {
				return MakeNull(), nil
			}
			i, ok := splListPhysicalIndex(d)
			if !ok {
				return MakeNull(), nil
			}
			return MakeInt(int64(i)), nil
		},
	}})

	ci.Method("next", &Function{Name: "next", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			if d := ctx.This[SplDoublyLinkedList](); d != nil {
				d.pos++
			}
			return MakeNull(), nil
		},
	}})

	ci.Method("prev", &Function{Name: "prev", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			if d := ctx.This[SplDoublyLinkedList](); d != nil {
				d.pos--
			}
			return MakeNull(), nil
		},
	}})
}

// splListPhysicalIndex переводит позицию итератора в физический индекс с учётом
// LIFO/FIFO-режима.
func splListPhysicalIndex(d *SplDoublyLinkedList) (int, bool) {
	n := len(d.items)
	if d.pos < 0 || d.pos >= n {
		return 0, false
	}
	if d.mode&2 != 0 { // IT_MODE_LIFO
		return n - 1 - d.pos, true
	}
	return d.pos, true
}

func registerSplStackMembers(ci *runtimeClass) {
	ci.Constructor(&Function{Name: "__construct", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			if d := ctx.This[SplDoublyLinkedList](); d != nil {
				d.mode = 6 // IT_MODE_LIFO | 4 (как в PHP 8.3 для SplStack)
			}
			return MakeNull(), nil
		},
	}})
}

func registerSplQueueMembers(ci *runtimeClass) {
	ci.Constructor(&Function{Name: "__construct", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			if d := ctx.This[SplDoublyLinkedList](); d != nil {
				d.mode = 4 // FIFO (как в PHP 8.3 для SplQueue)
			}
			return MakeNull(), nil
		},
	}})

	ci.Method("enqueue", &Function{Name: "enqueue", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplDoublyLinkedList]()
			if d == nil || len(args) == 0 {
				return MakeNull(), nil
			}
			d.items = append(d.items, args[0])
			return MakeNull(), nil
		},
	}})

	ci.Method("dequeue", &Function{Name: "dequeue", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplDoublyLinkedList]()
			if d == nil || len(d.items) == 0 {
				ctx.Throw(NewThrowable("RuntimeException", "Can't dequeue from an empty datastructure"))
				if ctx.HasThrown() {
					return *new(Slot), *new(error)
				}
				return MakeNull(), nil
			}
			v := d.items[0]
			d.items = append([]Slot{}, d.items[1:]...)
			return v, nil
		},
	}})
}

// --- SplObjectStorage ---

func registerSplObjectStorageMembers(ci *runtimeClass) {
	ci.Method("attach", &Function{Name: "attach", NumArgs: 2, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splObjectStorageData]()
			o := ctx.vm.splArgObj(args, 0)
			if d == nil || o == nil {
				return MakeNull(), nil
			}
			info := MakeNull()
			if len(args) > 1 {
				info = args[1]
			}
			if i := splStorageFind(d, o); i >= 0 {
				d.entries[i].info = info
			} else {
				d.entries = append(d.entries, splObjectEntry{obj: deref(args[0]), info: info})
			}
			return MakeNull(), nil
		},
	}})

	ci.Method("detach", &Function{Name: "detach", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splObjectStorageData]()
			o := ctx.vm.splArgObj(args, 0)
			if d == nil || o == nil {
				return MakeNull(), nil
			}
			if i := splStorageFind(d, o); i >= 0 {
				d.entries = append(d.entries[:i], d.entries[i+1:]...)
			}
			return MakeNull(), nil
		},
	}})

	ci.Method("contains", &Function{Name: "contains", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splObjectStorageData]()
			o := ctx.vm.splArgObj(args, 0)
			return MakeBool(d != nil && o != nil && splStorageFind(d, o) >= 0), nil
		},
	}})

	ci.Method("count", &Function{Name: "count", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splObjectStorageData]()
			if d == nil {
				return MakeInt(0), nil
			}
			return MakeInt(int64(len(d.entries))), nil
		},
	}})

	ci.Method("rewind", &Function{Name: "rewind", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			if d := ctx.This[splObjectStorageData](); d != nil {
				d.pos = 0
			}
			return MakeNull(), nil
		},
	}})

	ci.Method("valid", &Function{Name: "valid", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splObjectStorageData]()
			return MakeBool(d != nil && d.pos >= 0 && d.pos < len(d.entries)), nil
		},
	}})

	ci.Method("current", &Function{Name: "current", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splObjectStorageData]()
			if d == nil || d.pos < 0 || d.pos >= len(d.entries) {
				return MakeNull(), nil
			}
			return d.entries[d.pos].obj, nil
		},
	}})

	ci.Method("key", &Function{Name: "key", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splObjectStorageData]()
			if d == nil {
				return MakeNull(), nil
			}
			return MakeInt(int64(d.pos)), nil
		},
	}})

	ci.Method("next", &Function{Name: "next", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			if d := ctx.This[splObjectStorageData](); d != nil {
				d.pos++
			}
			return MakeNull(), nil
		},
	}})

	ci.Method("getInfo", &Function{Name: "getInfo", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splObjectStorageData]()
			if d == nil || d.pos < 0 || d.pos >= len(d.entries) {
				return MakeNull(), nil
			}
			return d.entries[d.pos].info, nil
		},
	}})

	ci.Method("setInfo", &Function{Name: "setInfo", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splObjectStorageData]()
			if d == nil || len(args) == 0 || d.pos < 0 || d.pos >= len(d.entries) {
				return MakeNull(), nil
			}
			d.entries[d.pos].info = args[0]
			return MakeNull(), nil
		},
	}})

	ci.Method("addAll", &Function{Name: "addAll", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splObjectStorageData]()
			src := ctx.vm.splStorageArg(args, 0)
			if d == nil || src == nil {
				return MakeNull(), nil
			}
			for _, e := range src.entries {
				o := deref(e.obj).obj()
				if o == nil {
					continue
				}
				if i := splStorageFind(d, o); i >= 0 {
					d.entries[i].info = e.info
				} else {
					d.entries = append(d.entries, e)
				}
			}
			return MakeNull(), nil
		},
	}})

	ci.Method("removeAll", &Function{Name: "removeAll", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splObjectStorageData]()
			src := ctx.vm.splStorageArg(args, 0)
			if d == nil || src == nil {
				return MakeNull(), nil
			}
			keep := d.entries[:0]
			for _, e := range d.entries {
				o := deref(e.obj).obj()
				if o != nil && splStorageFind(src, o) >= 0 {
					continue
				}
				keep = append(keep, e)
			}
			d.entries = keep
			return MakeNull(), nil
		},
	}})

	ci.Method("offsetExists", &Function{Name: "offsetExists", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splObjectStorageData]()
			o := ctx.vm.splArgObj(args, 0)
			return MakeBool(d != nil && o != nil && splStorageFind(d, o) >= 0), nil
		},
	}})

	ci.Method("offsetGet", &Function{Name: "offsetGet", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splObjectStorageData]()
			o := ctx.vm.splArgObj(args, 0)
			if d == nil || o == nil {
				return MakeNull(), nil
			}
			if i := splStorageFind(d, o); i >= 0 {
				return d.entries[i].info, nil
			}
			return MakeNull(), nil
		},
	}})

	ci.Method("offsetSet", &Function{Name: "offsetSet", NumArgs: 2, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splObjectStorageData]()
			o := ctx.vm.splArgObj(args, 0)
			if d == nil || o == nil || len(args) < 2 {
				return MakeNull(), nil
			}
			if i := splStorageFind(d, o); i >= 0 {
				d.entries[i].info = args[1]
			} else {
				d.entries = append(d.entries, splObjectEntry{obj: deref(args[0]), info: args[1]})
			}
			return MakeNull(), nil
		},
	}})

	ci.Method("offsetUnset", &Function{Name: "offsetUnset", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[splObjectStorageData]()
			o := ctx.vm.splArgObj(args, 0)
			if d == nil || o == nil {
				return MakeNull(), nil
			}
			if i := splStorageFind(d, o); i >= 0 {
				d.entries = append(d.entries[:i], d.entries[i+1:]...)
			}
			return MakeNull(), nil
		},
	}})

	ci.Method("getHash", &Function{Name: "getHash", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			o := ctx.vm.splArgObj(args, 0)
			if o == nil {
				return MakeString(""), nil
			}
			return MakeString(fmt.Sprintf("%032x", uint64(uintptr(unsafe.Pointer(o))))), nil
		},
	}})
}

func splStorageFind(d *splObjectStorageData, o *Object) int {
	for i := range d.entries {
		if deref(d.entries[i].obj).obj() == o {
			return i
		}
	}
	return -1
}

func (vm *VM) splStorageArg(args []Slot, i int) *splObjectStorageData {
	if i < 0 || i >= len(args) {
		return nil
	}
	s := deref(args[i])
	if s.typ != ObjectType {
		return nil
	}
	o := vm.objectOf(s)
	if o == nil {
		return nil
	}
	return objectAs[splObjectStorageData](o, vm.findClass("SplObjectStorage"))
}

// --- WeakMap ---

// weakMapData — данные экземпляра WeakMap: пары объект-ключ → значение.
// Полноценная слабая семантика (удаление записи при уничтожении ключа) не
// моделируется; для VM достаточно идентичности объекта-ключа.
type weakMapData struct {
	Object
	entries []weakMapEntry
}

type weakMapEntry struct {
	obj Slot
	val Slot
}

func weakMapFind(d *weakMapData, o *Object) int {
	for i := range d.entries {
		if deref(d.entries[i].obj).obj() == o {
			return i
		}
	}
	return -1
}

// weakMapEntryIsNull сообщает, что запись существует, но её значение — null
// (isset($map[$k]) для WeakMap — false, как для обычного массива).
func weakMapEntryIsNull(d *weakMapData, o *Object) bool {
	if i := weakMapFind(d, o); i >= 0 {
		return deref(d.entries[i].val).typ == NullType
	}
	return false
}

// weakMapKeyErr формирует TypeError «WeakMap key must be an object».
func weakMapKeyErr(ctx *Context) {
	ctx.Throw(NewThrowable("TypeError", "WeakMap key must be an object"))
}

// WeakMapDumpEntries — пары WeakMap для var_dump (ключ и значение в
// двухэлементном массиве, как у внутреннего итератора Zend). ok=false, если
// объект — не WeakMap.
func (vm *VM) WeakMapDumpEntries(o *Object) ([]DumpEntry, bool) {
	if o == nil || o.ClassName() != "WeakMap" {
		return nil, false
	}
	d := objectAs[weakMapData](o, o.Class())
	if d == nil {
		return nil, false
	}
	out := make([]DumpEntry, 0, len(d.entries))
	for i, e := range d.entries {
		pair := NewArray()
		_ = pair.Set(MakeStrKey("key"), e.obj)
		_ = pair.Set(MakeStrKey("value"), e.val)
		out = append(out, DumpEntry{Key: intToString(int64(i)), KeyInt: true, Val: MakeArray(pair)})
	}
	return out, true
}

func registerWeakMapMembers(ci *runtimeClass) {
	ci.Method("count", &Function{Name: "count", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			ctx.vm.ArmHandles()
			d := ctx.This[weakMapData]()
			if d == nil {
				return MakeInt(0), nil
			}
			return MakeInt(int64(len(d.entries))), nil
		},
	}})

	ci.Method("offsetExists", &Function{Name: "offsetExists", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			ctx.vm.ArmHandles()
			d := ctx.This[weakMapData]()
			o := ctx.vm.splArgObj(args, 0)
			if o == nil {
				weakMapKeyErr(ctx)
				if ctx.HasThrown() {
					return *new(Slot), *new(error)
				}
				return MakeNull(), nil
			}
			return MakeBool(d != nil && weakMapFind(d, o) >= 0 && !weakMapEntryIsNull(d, o)), nil
		},
	}})

	ci.Method("offsetGet", &Function{Name: "offsetGet", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			ctx.vm.ArmHandles()
			d := ctx.This[weakMapData]()
			o := ctx.vm.splArgObj(args, 0)
			if o == nil {
				weakMapKeyErr(ctx)
				if ctx.HasThrown() {
					return *new(Slot), *new(error)
				}
				return MakeNull(), nil
			}
			if d != nil {
				if i := weakMapFind(d, o); i >= 0 {
					return d.entries[i].val, nil
				}
			}
			ctx.vm.markObj(o)
			ctx.Throw(NewThrowable("Error",
				"Object "+ClassDisplayName(o.ClassName())+"#"+intToString(int64(o.ID()))+" not contained in WeakMap"))
			if ctx.HasThrown() {
				return *new(Slot), *new(error)
			}
			return MakeNull(), nil
		},
	}})

	ci.Method("offsetSet", &Function{Name: "offsetSet", NumArgs: 2, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			ctx.vm.ArmHandles()
			d := ctx.This[weakMapData]()
			if len(args) > 0 && deref(args[0]).typ == NullType {
				// $map[] = v — WeakMap не поддерживает append.
				ctx.Throw(NewThrowable("Error", "Cannot append to WeakMap"))
				if ctx.HasThrown() {
					return *new(Slot), *new(error)
				}
				return MakeNull(), nil
			}
			o := ctx.vm.splArgObj(args, 0)
			if o == nil {
				weakMapKeyErr(ctx)
				if ctx.HasThrown() {
					return *new(Slot), *new(error)
				}
				return MakeNull(), nil
			}
			if d == nil || len(args) < 2 {
				return MakeNull(), nil
			}
			if i := weakMapFind(d, o); i >= 0 {
				old := d.entries[i].val
				ctx.vm.noteStored(args[1])
				d.entries[i].val = args[1]
				// Прежнее значение (и ключ-объект, если на него больше нет
				// ссылок) теряют ссылку: сборщик вызовет __destruct.
				ctx.vm.noteDropped(old)
			} else {
				ctx.vm.noteStored(args[1])
				d.entries = append(d.entries, weakMapEntry{obj: deref(args[0]), val: args[1]})
			}
			return MakeNull(), nil
		},
	}})

	ci.Method("offsetUnset", &Function{Name: "offsetUnset", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			ctx.vm.ArmHandles()
			d := ctx.This[weakMapData]()
			o := ctx.vm.splArgObj(args, 0)
			if o == nil {
				weakMapKeyErr(ctx)
				if ctx.HasThrown() {
					return *new(Slot), *new(error)
				}
				return MakeNull(), nil
			}
			if d != nil {
				if i := weakMapFind(d, o); i >= 0 {
					d.entries = append(d.entries[:i], d.entries[i+1:]...)
				}
			}
			return MakeNull(), nil
		},
	}})

	ci.Method("getIterator", &Function{Name: "getIterator", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			ctx.vm.ArmHandles()
			d := ctx.This[weakMapData]()
			arr := NewArray()
			if d != nil {
				for _, e := range d.entries {
					_, _ = arr.Append(e.obj)
				}
			}
			return MakeArray(arr), nil
		},
	}})
}

// --- WeakReference ---

// weakReferenceData — данные WeakReference: «слабая» ссылка на объект. Наша
// VM владеет объектами через Go GC и таблицу liveObjs, поэтому ссылка хранит
// сильный слот: get() возвращает объект, пока тот жив в программе.
type weakReferenceData struct {
	Object
	target Slot
}

func registerWeakReferenceMembers(ci *runtimeClass) {
	ci.Constructor(&Function{Name: "__construct", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			ctx.Throw(NewThrowable("Error", "Direct instantiation of WeakReference is not allowed, use WeakReference::create instead"))
			if ctx.HasThrown() {
				return *new(Slot), *new(error)
			}
			return MakeNull(), nil
		},
	}})

	ci.Method("create", &Function{Name: "create", NumArgs: 1, NumRequired: 1, IsStatic: true, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			ctx.vm.ArmHandles()
			o := ctx.vm.splArgObj(args, 0)
			if o == nil {
				ctx.Throw(NewThrowable("TypeError", "WeakReference::create(): Argument #1 ($object) must be of type object"))
				if ctx.HasThrown() {
					return *new(Slot), *new(error)
				}
				return MakeNull(), nil
			}
			// Zend: WeakReference::create на тот же referent возвращает уже
			// созданную ссылку (zend_weakref_find), а не новую — наблюдаемо
			// по хендлу объекта (#N) и по debug_zval_dump.
			if wr := ctx.vm.weakRefs[o]; wr != nil && !wr.dead {
				return ctx.vm.slotOf(wr), nil
			}
			d := new(weakReferenceData)
			d.target = deref(args[0])
			sl := nativeObject[weakReferenceData](ctx.vm, ci, d)
			wr := ctx.vm.SlotObject(sl)
			if wr != nil {
				if ctx.vm.weakRefs == nil {
					ctx.vm.weakRefs = map[*Object]*Object{}
				}
				ctx.vm.weakRefs[o] = wr
			}
			return sl, nil
		},
	}})

	ci.Method("get", &Function{Name: "get", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			ctx.vm.ArmHandles()
			d := ctx.This[weakReferenceData]()
			if d == nil || deref(d.target).typ != ObjectType {
				return MakeNull(), nil
			}
			return d.target, nil
		},
	}})
}

// --- SplFixedArray ---

func registerSplFixedArrayMembers(ci *runtimeClass) {
	ci.Constructor(&Function{Name: "__construct", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplFixedArray]()
			if d == nil {
				return MakeNull(), nil
			}
			size := int64(0)
			if len(args) > 0 {
				size = args[0].ToInt()
			}
			if size < 0 {
				ctx.Throw(NewThrowable("ValueError", "SplFixedArray::__construct(): Argument #1 ($size) must be greater than or equal to 0"))
				if ctx.HasThrown() {
					return *new(Slot), *new(error)
				}
				return MakeNull(), nil
			}
			d.items = make([]Slot, size)
			return MakeNull(), nil
		},
	}})

	ci.Method("count", &Function{Name: "count", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplFixedArray]()
			if d == nil {
				return MakeInt(0), nil
			}
			return MakeInt(int64(len(d.items))), nil
		},
	}})

	ci.Method("getSize", &Function{Name: "getSize", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplFixedArray]()
			if d == nil {
				return MakeInt(0), nil
			}
			return MakeInt(int64(len(d.items))), nil
		},
	}})

	ci.Method("offsetExists", &Function{Name: "offsetExists", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplFixedArray]()
			if d == nil || len(args) == 0 || deref(args[0]).typ != IntType {
				return MakeBool(false), nil
			}
			i := args[0].ToInt()
			return MakeBool(i >= 0 && i < int64(len(d.items))), nil
		},
	}})

	ci.Method("offsetGet", &Function{Name: "offsetGet", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplFixedArray]()
			if d == nil || len(args) == 0 {
				return MakeNull(), nil
			}
			i, ok := splFixedIndex(args[0])
			if !ok || i < 0 || i >= int64(len(d.items)) {
				ctx.Throw(NewThrowable("RuntimeException", "Index invalid or out of range"))
				if ctx.HasThrown() {
					return *new(Slot), *new(error)
				}
				return MakeNull(), nil
			}
			return d.items[i], nil
		},
	}})

	ci.Method("offsetSet", &Function{Name: "offsetSet", NumArgs: 2, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplFixedArray]()
			if d == nil || len(args) < 2 {
				return MakeNull(), nil
			}
			i, ok := splFixedIndex(args[0])
			if !ok || i < 0 || i >= int64(len(d.items)) {
				ctx.Throw(NewThrowable("RuntimeException", "Index invalid or out of range"))
				if ctx.HasThrown() {
					return *new(Slot), *new(error)
				}
				return MakeNull(), nil
			}
			d.items[i] = args[1]
			return MakeNull(), nil
		},
	}})

	ci.Method("offsetUnset", &Function{Name: "offsetUnset", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplFixedArray]()
			if d == nil || len(args) == 0 {
				return MakeNull(), nil
			}
			i, ok := splFixedIndex(args[0])
			if !ok || i < 0 || i >= int64(len(d.items)) {
				ctx.Throw(NewThrowable("RuntimeException", "Index invalid or out of range"))
				if ctx.HasThrown() {
					return *new(Slot), *new(error)
				}
				return MakeNull(), nil
			}
			d.items[i] = MakeNull()
			return MakeNull(), nil
		},
	}})

	ci.Method("setSize", &Function{Name: "setSize", NumArgs: 1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplFixedArray]()
			if d == nil || len(args) == 0 {
				return MakeNull(), nil
			}
			n := args[0].ToInt()
			if n < 0 {
				ctx.Throw(NewThrowable("ValueError", "SplFixedArray::setSize(): Argument #1 ($size) must be greater than or equal to 0"))
				if ctx.HasThrown() {
					return *new(Slot), *new(error)
				}
				return MakeNull(), nil
			}
			if n == int64(len(d.items)) {
				return MakeNull(), nil
			}
			ns := make([]Slot, n)
			copy(ns, d.items)
			d.items = ns
			return MakeNull(), nil
		},
	}})

	ci.Method("toArray", &Function{Name: "toArray", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplFixedArray]()
			out := newArray()
			if d == nil {
				return MakeArray(out), nil
			}
			for i, v := range d.items {
				_ = out.SetInt(int64(i), v)
			}
			return MakeArray(out), nil
		},
	}})

	ci.Method("getIterator", &Function{Name: "getIterator", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplFixedArray]()
			itClass := ctx.vm.findClass("ArrayIterator")
			if itClass == nil {
				return MakeNull(), nil
			}
			arr := newArray()
			if d != nil {
				for i, v := range d.items {
					_ = arr.SetInt(int64(i), v)
				}
			}
			return nativeObject(ctx.vm, itClass, &splArrayData{arr: arr}), nil
		},
	}})

	ci.Method("fromArray", &Function{Name: "fromArray", NumArgs: 1, IsStatic: true, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			cls := ctx.Self()
			if cls == nil || len(args) == 0 {
				return MakeNull(), nil
			}
			src := deref(args[0])
			if src.typ != ArrayType {
				ctx.Throw(NewThrowable("TypeError", ArgTypeError("SplFixedArray::fromArray", 1, "array", "array", "", "", 0)))
				if ctx.HasThrown() {
					return *new(Slot), *new(error)
				}
				return MakeNull(), nil
			}
			d := &SplFixedArray{}
			for _, v := range src.AsArray().Values() {
				d.items = append(d.items, v)
			}
			return nativeObject(ctx.vm, cls, d), nil
		},
	}})

	ci.Method("jsonSerialize", &Function{Name: "jsonSerialize", NumArgs: 0, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[SplFixedArray]()
			out := newArray()
			if d == nil {
				return MakeArray(out), nil
			}
			for i, v := range d.items {
				_ = out.SetInt(int64(i), v)
			}
			return MakeArray(out), nil
		},
	}})
}

// splFixedIndex приводит ключ к индексу; string-ключ — TypeError (как PHP).
func splFixedIndex(s Slot) (int64, bool) {
	s = deref(s)
	switch s.typ {
	case IntType:
		return s.Int(), true
	case BoolType:
		if s.BoolVal() {
			return 1, true
		}
		return 0, true
	case FloatType:
		return floatToInt(s.Float()), true
	}
	return 0, false
}
