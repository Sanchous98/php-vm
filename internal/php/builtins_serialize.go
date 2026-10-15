package php

import (
	"math"
	"phpvm/internal/vm"
	"strconv"
	"strings"
)

// Файл содержит serialize/unserialize и их внутренние структуры.

// --- serialize / unserialize ---

// phpSer сериализует значение в формат PHP. Счётчик id растёт для КАЖДОГО
// значения (как в Zend): повторные ссылки печатаются как R:<id>, повторные
// объекты — как r:<id>.
//
// Хуки: __serialize() (приоритет), Serializable::serialize() (C-формат),
// __sleep() (список имён свойств), иначе — все свойства объекта.
type phpSer struct {
	vm     *vm.VM
	ctx    *vm.Context
	b      strings.Builder
	shared *serShared
}

// serShared — общее для вложенных serialize() состояние (var_hash Zend):
// таблицы ссылок/объектов и счётчик id.
type serShared struct {
	refs map[*vm.Ref]int
	objs map[*vm.Object]int
	n    int
}

// kv — пара «сериализованный ключ свойства» + значение.
type serKV struct {
	key string
	val vm.Slot
}

func phpSerialize(ctx *vm.Context, s vm.Slot) string {
	vmc := ctx.VM()
	if cur, _ := vmc.SerState().(*serShared); cur != nil && !vmc.SerializeLocked() {
		p := &phpSer{vm: vmc, ctx: ctx, shared: cur}
		p.write(s)
		if ctx.HasThrown() {
			return *new(string)
		}
		return p.b.String()
	}
	sh := &serShared{refs: map[*vm.Ref]int{}, objs: map[*vm.Object]int{}}
	prev := vmc.SerState()
	vmc.SetSerState(sh)
	defer vmc.SetSerState(prev)
	p := &phpSer{vm: vmc, ctx: ctx, shared: sh}
	p.write(s)
	if ctx.HasThrown() {
		return *new(string)
	}
	return p.b.String()
}

func (p *phpSer) write(s vm.Slot) {
	if s.IsRef() && s.Ref() != nil {
		r := s.Ref()
		if id, ok := p.shared.refs[r]; ok {
			p.b.WriteString("R:" + strconv.Itoa(id) + ";")
			return
		}
		// Ссылка на уже сериализованный объект: Zend печатает R:<id объекта>
		// (а не r:<id>), чтобы при unserialize() свойство осталось ссылкой.
		if ov := r.Value(); ov.Type() == vm.ObjectType {
			if o := p.vm.SlotObject(ov); o != nil {
				if id, ok := p.shared.objs[o]; ok {
					if id < 0 {
						// serialize() объекта не удался: Zend помечает id как
						// -1 и на каждое повторное появление печатает N;,
						// не откатывая счётчик (свежий id сгорает).
						p.shared.n++
						p.b.WriteString("N;")
						return
					}
					p.shared.refs[r] = id
					p.b.WriteString("R:" + strconv.Itoa(id) + ";")
					return
				}
			}
		}
		p.shared.n++
		p.shared.refs[r] = p.shared.n
		p.writeValue(r.Value(), true)
		// Неудачный Serializable-объект: ссылка на него в Zend не
		// запоминается (var_hash знает только -1 по указателю объекта).
		if ov := r.Value(); ov.Type() == vm.ObjectType {
			if o := p.vm.SlotObject(ov); o != nil {
				if id, ok := p.shared.objs[o]; ok && id < 0 {
					delete(p.shared.refs, r)
				}
			}
		}
		if p.ctx.HasThrown() {
			return
		}
		return
	}
	p.writeValue(s, false)
	if p.ctx.HasThrown() {
		return
	}
}

func (p *phpSer) writeValue(s vm.Slot, counted bool) {
	switch s.Type() {
	case vm.NullType:
		if !counted {
			p.shared.n++
		}
		p.b.WriteString("N;")
	case vm.BoolType:
		if !counted {
			p.shared.n++
		}
		if s.ToBool() {
			p.b.WriteString("b:1;")
		} else {
			p.b.WriteString("b:0;")
		}
	case vm.IntType:
		if !counted {
			p.shared.n++
		}
		p.b.WriteString("i:" + strconv.FormatInt(s.ToInt(), 10) + ";")
	case vm.FloatType:
		if !counted {
			p.shared.n++
		}
		p.writeFloat(s.ToFloat())
	case vm.StringType:
		if !counted {
			p.shared.n++
		}
		v := s.StringVal()
		p.b.WriteString("s:" + strconv.Itoa(len(v)) + `:"` + v + `";`)
	case vm.ArrayType:
		// Массивы не идентифицируются по ссылке (кроме Ref-ячейки), но каждое
		// появление в потоке получает собственный id, как в Zend.
		if !counted {
			p.shared.n++
		}
		a := s.AsArray()
		p.b.WriteString("a:" + strconv.Itoa(a.Len()) + ":{")
		for k, v := range a.All() {
			p.writeKey(k)
			p.write(v)
			if p.ctx.HasThrown() {
				return
			}
		}
		p.b.WriteString("}")
	case vm.ObjectType:
		p.writeObject(s, counted)
		if p.ctx.HasThrown() {
			return
		}
	default:
		if !counted {
			p.shared.n++
		}
		// Ресурсы (и прочие несразбираемые типы) Zend кодирует как int(0).
		p.b.WriteString("i:0;")
	}
}

func (p *phpSer) writeKey(k vm.ArrayKey) {
	if k.IsStr() {
		key := k.StrVal()
		p.b.WriteString("s:" + strconv.Itoa(len(key)) + `:"` + key + `";`)
	} else {
		p.b.WriteString("i:" + strconv.FormatInt(k.IntVal(), 10) + ";")
	}
}

func (p *phpSer) writeObject(s vm.Slot, counted bool) {
	o := p.vm.SlotObject(s)
	if o == nil {
		if !counted {
			p.shared.n++
		}
		p.b.WriteString("N;")
		return
	}
	if id, ok := p.shared.objs[o]; ok {
		if id < 0 {
			// Повторное появление объекта с провалившимся serialize():
			// N; и свежий id (Zend не откатывает data->n для -1).
			p.shared.n++
			p.b.WriteString("N;")
			return
		}
		p.b.WriteString("r:" + strconv.Itoa(id) + ";")
		return
	}
	if !counted {
		p.shared.n++
	}
	p.shared.objs[o] = p.shared.n
	ci0 := o.Class()
	if ci0 != nil && ci0.IsEnum() {
		nm := ""
		if v, ok := o.GetProp("name"); ok {
			nm = v.ToString()
		}
		body := vm.ClassDisplayName(ci0.Name()) + ":" + nm
		p.b.WriteString("E:" + strconv.Itoa(len(body)) + `:"` + body + `";`)
		return
	}
	name := o.ClassName()
	if name == incompleteClassName {
		p.writeIncomplete(o)
		if p.ctx.HasThrown() {
			return
		}
		return
	}
	ci := o.Class()
	if ci != nil && ci.IsNotSerializable() {
		p.ctx.Throw(vm.NewThrowable("Exception", "Serialization of '"+vm.ClassDisplayName(ci.Name())+"' is not allowed"))
		if p.ctx.HasThrown() {
			return
		}
		return
	}
	if ci != nil && ci.HasMethod("__serialize") {
		p.writeHookSerialized(o, ci)
		if p.ctx.HasThrown() {
			return
		}
		return
	}
	if ci != nil && implementsSerializable(ci) && ci.HasMethod("serialize") {
		p.writeSerializable(o, ci)
		if p.ctx.HasThrown() {
			return
		}
		return
	}
	if ci != nil && ci.HasMethod("__sleep") {
		p.writeSleep(o, ci)
		if p.ctx.HasThrown() {
			return
		}
		return
	}
	p.writeDefaultObject(o, name)
	if p.ctx.HasThrown() {
		return
	}
}

// writeDefaultObject сериализует объект по всем его свойствам.
func (p *phpSer) writeDefaultObject(o *vm.Object, name string) {
	pairs := make([]serKV, 0)
	for _, de := range p.vm.ObjectDumpEntries(o) {
		if de.Uninit {
			continue
		}
		pairs = append(pairs, serKV{serMangleKey(de), de.Val})
	}
	p.writeObjectBody(name, pairs)
	if p.ctx.HasThrown() {
		return
	}
}

func (p *phpSer) writeObjectBody(name string, pairs []serKV) {
	p.b.WriteString("O:" + strconv.Itoa(len(name)) + `:"` + name + `":` + strconv.Itoa(len(pairs)) + ":{")
	for _, kv := range pairs {
		p.b.WriteString("s:" + strconv.Itoa(len(kv.key)) + `:"` + kv.key + `";`)
		p.write(kv.val)
		if p.ctx.HasThrown() {
			return
		}
	}
	p.b.WriteString("}")
}

// writeHookSerialized использует __serialize(): результат — массив, его ключи
// и значения становятся парами O:-записи.
func (p *phpSer) writeHookSerialized(o *vm.Object, ci vm.ClassInfo) {
	name := o.ClassName()
	var res vm.Slot
	var err error
	p.vm.WithSerializeLock(func() {
		res, err = ci.Call(o, p.ctx, "__serialize", nil)
	})
	rethrowCall(p.ctx, err)
	if p.ctx.HasThrown() {
		return
	}
	if res.Type() != vm.ArrayType {
		p.ctx.Throw(vm.NewThrowable("TypeError", name+"::__serialize() must return an array"))
		if p.ctx.HasThrown() {
			return
		}
	}
	a := res.AsArray()
	p.b.WriteString("O:" + strconv.Itoa(len(name)) + `:"` + name + `":` + strconv.Itoa(a.Len()) + ":{")
	for k, v := range a.All() {
		p.writeKey(k)
		p.write(v)
		if p.ctx.HasThrown() {
			return
		}
	}
	p.b.WriteString("}")
}

// writeSerializable использует Serializable::serialize() (C-формат).
func (p *phpSer) writeSerializable(o *vm.Object, ci vm.ClassInfo) {
	name := o.ClassName()
	res, err := ci.Call(o, p.ctx, "serialize", nil)
	rethrowCall(p.ctx, err)
	if p.ctx.HasThrown() {
		return
	}
	if res.Type() == vm.NullType {
		// serialize() вернул NULL: PHP сериализует объект как N; и помечает
		// его id как -1 — повторные появления (в том числе по ссылке) тоже
		// дадут N; (см. php_add_var_hash/php_var_serialize_intern).
		p.shared.objs[o] = -1
		p.b.WriteString("N;")
		return
	}
	if res.Type() != vm.StringType {
		p.ctx.Throw(vm.NewThrowable("Exception", name+"::serialize() must return a string or NULL"))
		if p.ctx.HasThrown() {
			return
		}
	}
	data := res.ToString()
	p.b.WriteString("C:" + strconv.Itoa(len(name)) + `:"` + name + `":` + strconv.Itoa(len(data)) + ":{")
	p.b.WriteString(data)
	p.b.WriteString("}")
}

// writeSleep использует __sleep(): возвращённый массив — список имён свойств.
func (p *phpSer) writeSleep(o *vm.Object, ci vm.ClassInfo) {
	name := o.ClassName()
	var res vm.Slot
	var err error
	p.vm.WithSerializeLock(func() {
		res, err = ci.Call(o, p.ctx, "__sleep", nil)
	})
	rethrowCall(p.ctx, err)
	if p.ctx.HasThrown() {
		return
	}
	if res.Type() != vm.ArrayType {
		p.vm.Warn("serialize(): " + name + "::__sleep() should return an array only containing the names of instance-variables to serialize")
		// Как в PHP: недесериализуемый __sleep даёт N;.
		p.b.WriteString("N;")
		return
	}
	byKey := map[string]vm.DumpEntry{}
	for _, de := range p.vm.ObjectDumpEntries(o) {
		byKey[serMangleKey(de)] = de
	}
	className := o.ClassName()
	arr := res.AsArray()
	badElem := false
	for _, v := range arr.All() {
		if v.Type() != vm.StringType {
			badElem = true
			break
		}
	}
	if badElem {
		p.vm.Warn("serialize(): " + name + "::__sleep() should return an array only containing the names of instance-variables to serialize")
	}
	var pairs []serKV
	seen := map[string]bool{}
	for _, v := range arr.All() {
		raw := v.ToString()
		mkey := raw
		de, found := byKey[raw]
		if !found {
			mkey = "\x00" + className + "\x00" + raw
			de, found = byKey[mkey]
		}
		if !found {
			mkey = "\x00*\x00" + raw
			de, found = byKey[mkey]
		}
		if !found {
			p.vm.Warn("serialize(): \"" + raw + "\" returned as member variable from __sleep() but does not exist")
			continue
		}
		if de.Uninit {
			// Объявленное, но не инициализированное (типизированное) свойство
			// молча пропускается — как в Zend.
			continue
		}
		if seen[mkey] {
			p.vm.Warn("serialize(): \"" + raw + "\" is returned from __sleep() multiple times")
			continue
		}
		seen[mkey] = true
		pairs = append(pairs, serKV{mkey, de.Val})
	}
	p.writeObjectBody(name, pairs)
	if p.ctx.HasThrown() {
		return
	}
}

// writeIncomplete сериализует объект __PHP_Incomplete_Class под исходным именем
// класса (без служебного свойства __PHP_Incomplete_Class_Name).
func (p *phpSer) writeIncomplete(o *vm.Object) {
	nv, ok := o.GetProp(incompleteNameProp)
	if !ok || nv == nil {
		// Прямое `new __PHP_Incomplete_Class` (без служебного свойства):
		// сериализуется как обычный объект этого класса.
		p.writeDefaultObject(o, incompleteClassName)
		if p.ctx.HasThrown() {
			return
		}
		return
	}
	name := nv.ToString()
	var pairs []serKV
	for _, de := range p.vm.ObjectDumpEntries(o) {
		if de.Uninit || de.Key == incompleteNameProp {
			continue
		}
		pairs = append(pairs, serKV{serMangleKey(de), de.Val})
	}
	p.writeObjectBody(name, pairs)
	if p.ctx.HasThrown() {
		return
	}
}

// serMangleKey восстанавливает внутреннее (манглированное) имя свойства по
// DumpEntry: private -> \0Class\0name, protected -> \0*\0name.
func serMangleKey(de vm.DumpEntry) string {
	switch {
	case de.Suffix == ":protected":
		return "\x00*\x00" + de.Key
	case strings.HasPrefix(de.Suffix, `:"`) && strings.HasSuffix(de.Suffix, `":private`):
		cls := de.Suffix[2 : len(de.Suffix)-len(`":private`)]
		return "\x00" + cls + "\x00" + de.Key
	}
	return de.Key
}

func implementsSerializable(ci vm.ClassInfo) bool {
	if ci == nil {
		return false
	}
	for _, n := range ci.InterfaceNames() {
		if n == "Serializable" {
			return true
		}
	}
	return false
}

// rethrowCall превращает ошибку вызова пользовательского метода в исключение
// VM через error-протокол (ctx.Throw).
func rethrowCall(ctx *vm.Context, err error) {
	if err == nil {
		return
	}
	if th, ok := err.(*vm.Throwable); ok {
		ctx.Throw(th)
		if ctx.HasThrown() {
			return
		}
	}
	ctx.Throw(vm.NewThrowable("Error", err.Error()))
	if ctx.HasThrown() {
		return
	}
}

func (p *phpSer) writeFloat(f float64) {
	switch {
	case math.IsNaN(f):
		p.b.WriteString("d:NAN;")
	case math.IsInf(f, 1):
		p.b.WriteString("d:INF;")
	case math.IsInf(f, -1):
		p.b.WriteString("d:-INF;")
	default:
		p.b.WriteString("d:" + phpFormatFloat(f, false) + ";")
	}
}

func serializeFn(ctx *vm.Context) vm.Value {
	if ctx.VM().IsGeneratorSlot(arg0(ctx)) {
		ctx.Throw(vm.NewThrowable("Exception", "Serialization of 'Generator' is not allowed"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	return vm.Str(phpSerialize(ctx, arg0(ctx)))
}

// --- unserialize ---

// incompleteClassName / incompleteNameProp — неполный класс (класс не найден при
// unserialize): PHP создаёт объект с именем класса и служебным свойством.
const (
	incompleteClassName = "__PHP_Incomplete_Class"
	incompleteNameProp  = "__PHP_Incomplete_Class_Name"
)

// incompleteClassInfo — дескриптор неполного класса. Регистрируется в списке
// классов VM (см. compiler), чтобы vm.classInfo корректно резолвил слот.
var incompleteClassInfo = vm.NewClass(incompleteClassName, nil)

// __PHP_Incomplete_Class — final (PHP 8.3, FR #78638).
func init() { incompleteClassInfo.SetFinal() }

// phpUnser разбирает serialize-формат. vals — таблица значений по id (1-based),
// как var_hash в Zend: нужна для R:<id> (ссылки) и r:<id> (повторные объекты).
type phpUnser struct {
	vm    *vm.VM
	ctx   *vm.Context
	s     string
	depth int
	// shared — общее состояние var_hash для вложенных unserialize() (PHP
	// переиспользует один var_hash, пока внешний вызов не завершён).
	shared *unserShared
	// pendingLoc — локация текущего значения: если при разборе значения
	// регистрируется новый uVal, к нему привязывается сеттер (для апгрейда
	// при появлении R:-ссылки).
	pendingLoc func(vm.Slot)
	// warnPrefix — префикс диагностик ("unserialize():" или
	// "ArrayObject::unserialize():" для нативного C-разбора).
	warnPrefix string
	// allowed — разрешённые классы (allowed_classes); restrict — ограничение
	// включено. denyAll — запрещены все (allowed_classes=false).
	allowed  map[string]bool
	restrict bool
	denyAll  bool
	// errPos/failed — позиция первой ошибки разбора (для сообщения
	// "Error at offset N of M bytes").
	errPos int
	failed bool
	// conts — стек активных контейнеров (массив/объект/данные __unserialize):
	// для каждого ключа хранятся var_hash-id значений, записанных в его ячейку.
	// Повторная запись по тому же ключу переиспользует ячейку (Zend хранит в
	// var_hash указатели на zval-ячейки), поэтому R:/r: на такой id — ошибка.
	conts []contIds
	// destIds — id значений, делящих текущую ячейку-приёмник (повторный ключ).
	destIds []int64
}

// contIds — карты "ключ → var_hash-id" для одного контейнера: у массива ключи
// ArrayKey, у объекта — store-имена свойств.
type contIds struct {
	arr map[vm.ArrayKey][]int64
	obj map[string][]int64
}

// prefix возвращает префикс диагностик (по умолчанию "unserialize():").
func (u *phpUnser) prefix() string {
	if u.warnPrefix == "" {
		return "unserialize():"
	}
	return u.warnPrefix
}

// unserShared — общая для вложенных вызовов таблица значений и отложенных
// хуков (аналог php_unserialize_data).
type unserShared struct {
	vals    []uVal
	pending []uPending
}

// fail фиксирует позицию ошибки (первая ошибка выигрывает).
func (u *phpUnser) fail(pos int) {
	if !u.failed {
		u.failed = true
		u.errPos = pos
	}
}

// pushCont/popCont ведут стек контейнеров для отслеживания переиспользования
// ячеек при повторных ключах (см. contIds).
func (u *phpUnser) pushCont(c contIds) { u.conts = append(u.conts, c) }

func (u *phpUnser) popCont() { u.conts = u.conts[:len(u.conts)-1] }

// destForArr возвращает id прошлых значений для ключа массива (если ключ уже
// присутствует) и фиксирует приёмник текущего значения.
func (u *phpUnser) destForArr(arr *vm.Array, key vm.ArrayKey) []int64 {
	if _, exists := arr.Get(key); !exists {
		return nil
	}
	return u.conts[len(u.conts)-1].arr[key]
}

// recordArrDest запоминает id значения, записанного в ячейку ключа, если
// разбор значения зарегистрировал новую запись var_hash (R: записей не плодит).
func (u *phpUnser) recordArrDest(key vm.ArrayKey, before int) {
	if len(u.shared.vals) > before {
		c := &u.conts[len(u.conts)-1]
		if c.arr == nil {
			c.arr = map[vm.ArrayKey][]int64{}
		}
		c.arr[key] = append(c.arr[key], int64(before+1))
	}
}

// destForObj/recordObjDest — то же для свойств объекта (ключ — store-имя).
func (u *phpUnser) destForObj(o *vm.Object, store string) []int64 {
	if _, exists := o.GetProp(store); !exists {
		return nil
	}
	return u.conts[len(u.conts)-1].obj[store]
}

func (u *phpUnser) recordObjDest(store string, before int) {
	if len(u.shared.vals) > before {
		c := &u.conts[len(u.conts)-1]
		if c.obj == nil {
			c.obj = map[string][]int64{}
		}
		c.obj[store] = append(c.obj[store], int64(before+1))
	}
}

// destConflict сообщает, что R:/r: ссылается на ячейку, совпадающую с текущим
// приёмником значения (Zend: rval_ref == rval → ошибка разбора).
func (u *phpUnser) destConflict(id int64) bool {
	for _, d := range u.destIds {
		if d == id {
			return true
		}
	}
	return false
}

type uVal struct {
	slot vm.Slot
	ref  *vm.Ref
	obj  *vm.Object
	// sharedDest — значение записано в ячейку, у которой уже была запись
	// var_hash (повторный ключ): финальная запись должна положить саму
	// ячейку-ссылку, а не значение (typed_property_ref_overwrite2).
	sharedDest bool
	// setters — локации, где хранится это значение; вызываются, когда значение
	// впервые становится целью R:-ссылки, чтобы превратить их в Ref (PHP
	// «апгрейдит» zval до ссылки).
	setters []func()
	// upgraded — сеттеры уже применены.
	upgraded bool
	// invalid — значение создано неуспешным вложенным unserialize(); ссылки
	// (R:/r:) на него делают весь разбор ошибочным (Zend не сохраняет
	// частично разобранные значения).
	invalid bool
}

// uPending — отложенный вызов хука после восстановления всего графа:
// kind 0 — __wakeup(), kind 1 — __unserialize(array).
type uPending struct {
	obj  *vm.Object
	kind int
	data vm.Slot
}

func unserializeFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	allowed, restrict, denyAll := checkAllowedClassesOption(ctx)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	vmc := ctx.VM()
	if cur, _ := vmc.UnserState().(*phpUnser); cur != nil && !vmc.SerializeLocked() {
		// Вложенный вызов без serialize_lock: PHP переиспользует тот же
		// var_hash, пока внешний unserialize() не завершён.
		child := &phpUnser{vm: vmc, ctx: ctx, s: s, shared: cur.shared, warnPrefix: "unserialize():"}
		if denyAll {
			child.restrict, child.denyAll = true, true
		} else if restrict {
			child.restrict, child.allowed = true, allowed
		}
		startVals := len(child.shared.vals)
		v, np, ok := child.value(0)
		if !ok {
			if !child.failed {
				child.failed = true
				child.errPos = 0
			}
			// Неуспешный вложенный разбор: Zend помечает добавленные им
			// значения var_hash как NULL, но НЕ откатывает счётчик id —
			// последующие значения получают следующие номера, а R:/r: на
			// помеченные id дают ошибку (bug70436: ошибка на R:5 после
			// провалившегося вложенного C:-объекта).
			for i := startVals; i < len(child.shared.vals); i++ {
				child.shared.vals[i].invalid = true
				vmc.MarkNoDestruct(child.shared.vals[i].obj)
			}
			vmc.Warn("unserialize(): Error at offset " + strconv.Itoa(child.errPos) + " of " + strconv.Itoa(len(s)) + " bytes")
			return vm.Bool(false)
		}
		if np < len(s) {
			vmc.Warn("unserialize(): Extra data starting at offset " + strconv.Itoa(np) + " of " + strconv.Itoa(len(s)) + " bytes")
		}
		return v
	}
	u := &phpUnser{vm: vmc, ctx: ctx, s: s, shared: &unserShared{}, warnPrefix: "unserialize():"}
	if denyAll {
		u.restrict, u.denyAll = true, true
	} else if restrict {
		u.restrict, u.allowed = true, allowed
	}
	prev := vmc.UnserState()
	vmc.SetUnserState(u)
	defer vmc.SetUnserState(prev)
	// Верхнеуровневое значение может стать целью R:1 (Zend превращает его
	// zval в ссылку): пишем его через локацию, как элемент массива.
	var topRef vm.Slot
	topUpgraded := false
	u.pendingLoc = func(sl vm.Slot) {
		topRef = sl
		topUpgraded = true
	}
	v, np, ok := u.value(0)
	u.pendingLoc = nil
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if !ok {
		if !u.failed {
			u.failed = true
			u.errPos = 0
		}
		for _, e := range u.shared.vals {
			vmc.MarkNoDestruct(e.obj)
		}
		vmc.Warn("unserialize(): Error at offset " + strconv.Itoa(u.errPos) + " of " + strconv.Itoa(len(s)) + " bytes")
		return vm.Bool(false)
	}
	if np < len(s) {
		vmc.Warn("unserialize(): Extra data starting at offset " + strconv.Itoa(np) + " of " + strconv.Itoa(len(s)) + " bytes")
	}
	u.runPending()
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if topUpgraded {
		// php_unserialize_with_options в конце разворачивает ссылку
		// (zend_unwrap_reference) — возвращается значение на момент после
		// __wakeup().
		return derefSlot(topRef)
	}
	return v
}

// checkAllowedClassesOption проверяет тип опции allowed_classes и возвращает
// множество разрешённых классов (lowercase), а также признаки ограничения.
func checkAllowedClassesOption(ctx *vm.Context) (map[string]bool, bool, bool) {
	opts := vm.Deref(arg(ctx, 1))
	if opts.Type() != vm.ArrayType {
		return nil, false, false
	}
	ac, ok := opts.AsArray().Get(vm.MakeStrKey("allowed_classes"))
	if !ok {
		return nil, false, false
	}
	switch ac.Type() {
	case vm.BoolType:
		if ac.ToBool() {
			return nil, false, false
		}
		return nil, true, true
	case vm.ArrayType:
		set := map[string]bool{}
		for _, v := range ac.AsArray().Values() {
			set[strings.ToLower(strings.TrimLeft(v.ToString(), "\\"))] = true
		}
		return set, true, false
	}
	ctx.Throw(vm.NewThrowable("TypeError",
		`unserialize(): Option "allowed_classes" must be of type array|bool, `+phpValueTypeName(ac)+` given`))
	if ctx.HasThrown() {
		return *new(map[string]bool), *new(bool), *new(bool)
	}
	return nil, false, false
}

// phpValueTypeName — имя PHP-типа значения для сообщений о типе.
func phpValueTypeName(s vm.Slot) string {
	switch s.Type() {
	case vm.NullType:
		return "null"
	case vm.BoolType:
		return "bool"
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
	}
	return "mixed"
}

func (u *phpUnser) runPending() {
	for _, pw := range u.shared.pending {
		ci := pw.obj.Class()
		if ci == nil {
			continue
		}
		var err error
		u.vm.WithSerializeLock(func() {
			if pw.kind == 1 {
				_, err = ci.Call(pw.obj, u.ctx, "__unserialize", []vm.Slot{pw.data})
			} else {
				_, err = ci.Call(pw.obj, u.ctx, "__wakeup", nil)
			}
		})
		if err != nil {
			u.vm.MarkNoDestruct(pw.obj)
		}
		rethrowCall(u.ctx, err)
		if u.ctx.HasThrown() {
			return
		}
	}
}

// destSharedRef возвращает ячейку-ссылку прошлой записи в тот же приёмник:
// Zend хранит в var_hash указатели на zval-ячейки, поэтому повторная запись по
// тому же ключу продолжает ту же ячейку (R:/r: на любую из записей видят
// текущее значение — typed_property_ref_overwrite2).
func (u *phpUnser) destSharedRef() *vm.Ref {
	if u.pendingLoc == nil || len(u.destIds) == 0 {
		return nil
	}
	idx := u.destIds[len(u.destIds)-1]
	if idx < 1 || int(idx) > len(u.shared.vals) {
		return nil
	}
	e := &u.shared.vals[idx-1]
	if e.ref == nil || e.invalid {
		return nil
	}
	return e.ref
}

func (u *phpUnser) register(v vm.Slot, o *vm.Object) int {
	ref := u.destSharedRef()
	shared := ref != nil
	if ref == nil {
		ref = vm.NewRef(v).Ref()
		ref.MarkCounted()
	} else {
		ref.Set(v)
	}
	e := uVal{slot: v, obj: o, ref: ref, sharedDest: shared}
	u.shared.vals = append(u.shared.vals, e)
	if loc := u.pendingLoc; loc != nil {
		u.pendingLoc = nil
		idx := len(u.shared.vals) - 1
		ref := e.ref
		u.shared.vals[idx].setters = append(u.shared.vals[idx].setters, func() {
			loc(vm.MakeRef(ref))
		})
	}
	return len(u.shared.vals)
}

// reserve выделяет id в var_hash до создания объекта (Zend делает var_push
// в начале разбора значения — до автозагрузки класса). fillReserved затем
// заполняет слот/объект.
func (u *phpUnser) reserve() int {
	ref := u.destSharedRef()
	shared := ref != nil
	if ref == nil {
		ref = vm.NewRef(vm.MakeNull()).Ref()
		ref.MarkCounted()
	}
	e := uVal{ref: ref, sharedDest: shared}
	u.shared.vals = append(u.shared.vals, e)
	if loc := u.pendingLoc; loc != nil {
		u.pendingLoc = nil
		ref := e.ref
		u.shared.vals[len(u.shared.vals)-1].setters = append(u.shared.vals[len(u.shared.vals)-1].setters, func() {
			loc(vm.MakeRef(ref))
		})
	}
	return len(u.shared.vals)
}

// fillReserved записывает в заранее выделенный id реальный слот/объект.
func (u *phpUnser) fillReserved(idx int, slot vm.Slot, o *vm.Object) {
	e := &u.shared.vals[idx-1]
	e.slot = slot
	e.obj = o
	e.ref.Set(slot)
	// Общая ячейка у нескольких записей (повторный ключ): объект виден всем
	// (r:<id> на любую из них).
	if o != nil {
		for i := range u.shared.vals {
			if u.shared.vals[i].ref == e.ref {
				u.shared.vals[i].obj = o
				u.shared.vals[i].slot = slot
			}
		}
	}
}

// upgradeRef превращает значение id (1-based) в ссылку: применяет сеттеры.
func (u *phpUnser) upgradeRef(id int64) *vm.Ref {
	e := &u.shared.vals[id-1]
	if !e.upgraded {
		e.upgraded = true
		for _, f := range e.setters {
			f()
		}
	}
	return e.ref
}

// value разбирает одно значение. Ключи массивов/свойств разбираются отдельно
// (key) и в таблицу id не попадают — как в serialize.
//
// Zend делает var_push ДО разбора значения (кроме 'R'), поэтому неуспешный
// разбор тоже занимает id: иначе последующие id в вложенных контекстах
// сдвигаются (bug70436: провалившееся вложение сдвигает R:5).
func (u *phpUnser) value(pos int) (vm.Slot, int, bool) {
	startVals := len(u.shared.vals)
	v, np, ok := u.valueInner(pos)
	if !ok && len(u.shared.vals) == startVals {
		u.shared.vals = append(u.shared.vals, uVal{invalid: true})
	}
	return v, np, ok
}

// valueInner — собственно разбор значения (см. value).
func (u *phpUnser) valueInner(pos int) (vm.Slot, int, bool) {
	if u.depth > 4096 {
		u.fail(pos)
		return vm.MakeNull(), pos, false
	}
	u.depth++
	defer func() { u.depth-- }()
	if pos >= len(u.s) {
		u.fail(pos)
		return vm.MakeNull(), pos, false
	}
	switch u.s[pos] {
	case 'N':
		if pos+1 >= len(u.s) || u.s[pos+1] != ';' {
			u.fail(pos)
			return vm.MakeNull(), pos, false
		}
		v := vm.MakeNull()
		u.register(v, nil)
		return v, pos + 2, true
	case 'b':
		if pos+3 >= len(u.s) || u.s[pos+1] != ':' || u.s[pos+3] != ';' {
			u.fail(pos)
			return vm.MakeNull(), pos, false
		}
		if u.s[pos+2] != '0' && u.s[pos+2] != '1' {
			u.fail(pos)
			return vm.MakeNull(), pos, false
		}
		v := vm.MakeBool(u.s[pos+2] == '1')
		u.register(v, nil)
		return v, pos + 4, true
	case 'i':
		n, np, ok := u.parseInt(pos)
		if !ok {
			return vm.MakeNull(), pos, false
		}
		v := vm.MakeInt(n)
		u.register(v, nil)
		return v, np, true
	case 'd':
		end := strings.IndexByte(u.s[pos:], ';')
		if end < 2 {
			u.fail(pos)
			return vm.MakeNull(), pos, false
		}
		body := u.s[pos+2 : pos+end]
		var f float64
		switch body {
		case "INF":
			f = math.Inf(1)
		case "-INF":
			f = math.Inf(-1)
		case "NAN":
			f = math.NaN()
		default:
			parsed, err := strconv.ParseFloat(body, 64)
			if err != nil {
				u.fail(pos)
				return vm.MakeNull(), pos, false
			}
			f = parsed
		}
		v := vm.MakeFloat(f)
		u.register(v, nil)
		return v, pos + end + 1, true
	case 's':
		v, np, ok := u.parseString(pos, true)
		if !ok {
			u.fail(pos)
		}
		return v, np, ok
	case 'a':
		return u.parseArray(pos)
	case 'O':
		return u.parseObject(pos)
	case 'C':
		return u.parseCustom(pos)
	case 'E':
		return u.parseEnum(pos)
	case 'R':
		if pos+1 >= len(u.s) || u.s[pos+1] != ':' {
			u.fail(pos)
			return vm.MakeNull(), pos, false
		}
		id, np, ok := u.readUint(pos+2, ';')
		if !ok {
			u.fail(pos)
			return vm.MakeNull(), pos, false
		}
		if id < 1 || id > int64(len(u.shared.vals)) || u.shared.vals[id-1].ref == nil || u.shared.vals[id-1].invalid {
			u.fail(np)
			return vm.MakeNull(), pos, false
		}
		if u.destConflict(id) {
			u.fail(np)
			return vm.MakeNull(), pos, false
		}
		return vm.MakeRef(u.upgradeRef(id)), np, true
	case 'r':
		if pos+1 >= len(u.s) || u.s[pos+1] != ':' {
			u.fail(pos)
			return vm.MakeNull(), pos, false
		}
		id, np, ok := u.readUint(pos+2, ';')
		if !ok {
			u.fail(pos)
			return vm.MakeNull(), pos, false
		}
		if id < 1 || id > int64(len(u.shared.vals)) || u.shared.vals[id-1].obj == nil || u.shared.vals[id-1].invalid {
			u.fail(np)
			return vm.MakeNull(), pos, false
		}
		if u.destConflict(id) {
			u.fail(np)
			return vm.MakeNull(), pos, false
		}
		v := u.shared.vals[id-1].slot
		u.register(v, nil)
		return v, np, true
	case '}':
		// В Zend состояние "}" в позиции значения даёт отдельную диагностику.
		u.vm.Warn(u.prefix() + " Unexpected end of serialized data")
		u.fail(pos)
		return vm.MakeNull(), pos, false
	}
	u.fail(pos)
	return vm.MakeNull(), pos, false
}

func (u *phpUnser) parseString(pos int, reg bool) (vm.Slot, int, bool) {
	if pos+1 >= len(u.s) || u.s[pos+1] != ':' {
		u.fail(pos)
		return vm.MakeNull(), pos, false
	}
	l, np, ok := u.readUint(pos+2, ':')
	if !ok {
		u.fail(pos)
		return vm.MakeNull(), pos, false
	}
	if np >= len(u.s) || u.s[np] != '"' {
		u.fail(np)
		return vm.MakeNull(), pos, false
	}
	start := np + 1
	if l > int64(len(u.s)-start) {
		u.fail(pos + 2)
		return vm.MakeNull(), pos, false
	}
	end := start + int(l)
	if end >= len(u.s) || u.s[end] != '"' {
		u.fail(end)
		return vm.MakeNull(), pos, false
	}
	if end+1 >= len(u.s) || u.s[end+1] != ';' {
		u.fail(end + 1)
		return vm.MakeNull(), pos, false
	}
	v := vm.MakeString(u.s[start:end])
	if reg {
		u.register(v, nil)
	}
	return v, end + 2, true
}

// parseInt разбирает i:<iv>; и зеркалит поведение Zend: токен обязан быть
// корректным (иначе ошибка на позиции pos), а переполнение даёт предупреждение
// "Numerical result out of range" и clamp к границам int64.
func (u *phpUnser) parseInt(pos int) (int64, int, bool) {
	q := pos + 2
	if q < len(u.s) && (u.s[q] == '-' || u.s[q] == '+') {
		q++
	}
	d0 := q
	for q < len(u.s) && u.s[q] >= '0' && u.s[q] <= '9' {
		q++
	}
	if q == d0 || q >= len(u.s) || u.s[q] != ';' {
		u.fail(pos)
		return 0, pos, false
	}
	n, _ := u.parseIV2(pos + 2)
	return n, q + 1, true
}

// parseIV2 повторяет parse_iv2 из var_unserializer.re: знак, пропуск ведущих
// нулей, накопление цифр. При переполнении печатает warning и возвращает
// границу int64. Возвращает значение и позицию сразу после последней цифры.
func (u *phpUnser) parseIV2(pos int) (int64, int) {
	s := u.s
	neg := false
	if pos < len(s) && s[pos] == '-' {
		neg = true
		pos++
	} else if pos < len(s) && s[pos] == '+' {
		pos++
	}
	for pos < len(s) && s[pos] == '0' {
		pos++
	}
	start := pos
	var mag uint64
	overflow := false
	for pos < len(s) && s[pos] >= '0' && s[pos] <= '9' {
		d := uint64(s[pos] - '0')
		if mag > (math.MaxUint64-d)/10 {
			overflow = true
			mag = math.MaxUint64
			pos++
			continue
		}
		mag = mag*10 + d
		pos++
	}
	if pos-start > 19 {
		overflow = true
	}
	if !overflow {
		if !neg && mag > math.MaxInt64 {
			overflow = true
		}
		if neg && mag > uint64(math.MaxInt64)+1 {
			overflow = true
		}
	}
	if overflow {
		u.vm.Warn(u.prefix() + " Numerical result out of range")
		if neg {
			return math.MinInt64, pos
		}
		return math.MaxInt64, pos
	}
	if neg {
		return -int64(mag), pos
	}
	return int64(mag), pos
}

// fakeElemCount повторяет IS_FAKE_ELEM_COUNT: каждый элемент занимает минимум
// 2 байта, поэтому elements > len/2 заведомо ложно.
func fakeElemCount(elements, serializedLen int64) bool {
	return elements > serializedLen/2
}

// setArr кладёт значение в элемент массива и регистрирует само-цикл
// (`R:`-ссылка на сам массив): иначе gc_collect_cycles не увидит мусор
// (gc_022: циклический массив, созданный unserialize во временном значении).
func (u *phpUnser) setArr(arr *vm.Array, key vm.ArrayKey, s vm.Slot) {
	arr.Set(key, s)
	if s.IsRef() {
		if r := s.Ref(); r != nil {
			u.vm.NoteRefCycle(vm.MakeArray(arr), r)
		}
	}
}

func (u *phpUnser) parseArray(pos int) (vm.Slot, int, bool) {
	if pos+1 >= len(u.s) || u.s[pos+1] != ':' {
		u.fail(pos)
		return vm.MakeNull(), pos, false
	}
	// a:<uiv>:{ ; токен проверяем как re2c.
	q := pos + 2
	d0 := q
	for q < len(u.s) && u.s[q] >= '0' && u.s[q] <= '9' {
		q++
	}
	if q == d0 || q >= len(u.s) || u.s[q] != ':' || q+1 >= len(u.s) || u.s[q+1] != '{' {
		u.fail(pos)
		return vm.MakeNull(), pos, false
	}
	n, _ := u.parseIV2(pos + 2)
	body := q + 2
	if n < 0 || fakeElemCount(n, int64(len(u.s)-body)) {
		u.fail(body)
		return vm.MakeNull(), pos, false
	}
	arr := vm.NewArray()
	v := vm.MakeArray(arr)
	u.register(v, nil)
	u.pushCont(contIds{})
	defer u.popCont()
	p := body
	for i := int64(0); i < n; i++ {
		if p < len(u.s) && u.s[p] == '}' {
			u.vm.Warn(u.prefix() + " Unexpected end of serialized data")
			u.fail(p)
			return vm.MakeNull(), pos, false
		}
		key, kp, ok := u.key(p)
		if !ok {
			return vm.MakeNull(), pos, false
		}
		k2 := key
		placed := false
		u.pendingLoc = func(s vm.Slot) { u.setArr(arr, k2, s); placed = true }
		savedDest := u.destIds
		u.destIds = u.destForArr(arr, key)
		before := len(u.shared.vals)
		val, vp, ok := u.value(kp)
		u.destIds = savedDest
		if u.ctx.HasThrown() {
			return *new(vm.Slot), *new(int), *new(bool)
		}
		u.pendingLoc = nil
		if !ok {
			return vm.MakeNull(), pos, false
		}
		if !placed {
			u.setArr(arr, key, val)
		}
		u.recordArrDest(key, before)
		p = vp
	}
	if p >= len(u.s) || u.s[p] != '}' {
		u.fail(p)
		return vm.MakeNull(), pos, false
	}
	return v, p + 1, true
}

// parseEnum разбирает E:<len>:"<Class>:<Case>"; (сериализация enum-кейса).
func (u *phpUnser) parseEnum(pos int) (vm.Slot, int, bool) {
	if pos+1 >= len(u.s) || u.s[pos+1] != ':' {
		u.fail(pos)
		return vm.MakeNull(), pos, false
	}
	l, np, ok := u.readUint(pos+2, ':')
	if !ok {
		u.fail(pos)
		return vm.MakeNull(), pos, false
	}
	if np >= len(u.s) || u.s[np] != '"' {
		u.fail(np)
		return vm.MakeNull(), pos, false
	}
	start := np + 1
	if l > int64(len(u.s)-start) {
		u.fail(pos)
		return vm.MakeNull(), pos, false
	}
	end := start + int(l)
	if end >= len(u.s) || u.s[end] != '"' {
		u.fail(end)
		return vm.MakeNull(), pos, false
	}
	after := end + 1
	if after >= len(u.s) || u.s[after] != ';' {
		u.fail(after)
		return vm.MakeNull(), pos, false
	}
	next := after + 1
	body := u.s[start:end]
	colon := strings.IndexByte(body, ':')
	if colon < 0 {
		u.vm.Warn(u.prefix() + " Invalid enum name '" + body + "' (missing colon)")
		u.fail(pos)
		return vm.MakeNull(), pos, false
	}
	name := body[:colon]
	caseName := body[colon+1:]
	ci := u.vm.FindClass(name)
	if ci == nil {
		var aerr error
		u.vm.WithSerializeLock(func() { _, aerr = u.vm.Autoload(u.ctx, name) })
		if aerr != nil {
			rethrowCall(u.ctx, aerr)
			if u.ctx.HasThrown() {
				return *new(vm.Slot), *new(int), *new(bool)
			}
		}
		ci = u.vm.FindClass(name)
	}
	if ci == nil {
		u.vm.Warn(u.prefix() + " Class '" + name + "' not found")
		u.fail(pos)
		return vm.MakeNull(), pos, false
	}
	if !ci.IsEnum() {
		u.vm.Warn(u.prefix() + " Class '" + name + "' is not an enum")
		u.fail(pos)
		return vm.MakeNull(), pos, false
	}
	slot, isCase := u.vm.EnumCaseByName(ci, caseName)
	if !isCase {
		if ci.HasConst(caseName) {
			u.vm.Warn(u.prefix() + " " + name + "::" + caseName + " is not an enum case")
		} else {
			u.vm.Warn(u.prefix() + " Undefined constant " + name + "::" + caseName)
		}
		u.fail(next)
		return vm.MakeNull(), pos, false
	}
	u.register(slot, u.vm.SlotObject(slot))
	return slot, next, true
}

func (u *phpUnser) parseObject(pos int) (vm.Slot, int, bool) {
	// O:<len>:"Class":<nprops>:{...}
	if pos+1 >= len(u.s) || u.s[pos+1] != ':' {
		u.fail(pos)
		return vm.MakeNull(), pos, false
	}
	name, np, ok := u.parseName(pos + 2)
	if !ok {
		u.fail(pos)
		return vm.MakeNull(), pos, false
	}
	if np >= len(u.s) || u.s[np] != ':' {
		u.fail(np)
		return vm.MakeNull(), pos, false
	}
	if name == "" || name[0] == 0 || name[0] == '\\' || strings.IndexByte(name, 0) >= 0 {
		u.fail(pos)
		return vm.MakeNull(), pos, false
	}
	closingQuote := np - 1
	if closingQuote >= len(u.s)-2 {
		u.vm.Warn("Bad unserialize data")
		u.fail(closingQuote)
		return vm.MakeNull(), pos, false
	}
	nprops, afterDigits := u.parseIV2(np + 1)
	if nprops < 0 || fakeElemCount(nprops, int64(len(u.s)-closingQuote)) {
		u.fail(afterDigits)
		return vm.MakeNull(), pos, false
	}
	if afterDigits >= len(u.s) || u.s[afterDigits] != ':' {
		u.fail(afterDigits)
		return vm.MakeNull(), pos, false
	}
	if afterDigits+1 >= len(u.s) || u.s[afterDigits+1] != '{' {
		u.fail(afterDigits + 1)
		return vm.MakeNull(), pos, false
	}
	body := afterDigits + 2
	idx := u.reserve()
	slot, incomplete := u.newObjectSlot(name)
	if u.ctx.HasThrown() {
		return *new(vm.Slot), *new(int), *new(bool)
	}
	o := u.vm.SlotObject(slot)
	u.fillReserved(idx, slot, o)
	ci := o.Class()
	if ci != nil && !incomplete && ci.IsNotSerializable() {
		u.ctx.Throw(vm.NewThrowable("Exception", "Unserialization of '"+vm.ClassDisplayName(ci.Name())+"' is not allowed"))
		if u.ctx.HasThrown() {
			return *new(vm.Slot), *new(int), *new(bool)
		}
	}
	if ci != nil && implementsSerializable(ci) && ci.HasMethod("serialize") && !ci.HasMethod("__unserialize") {
		u.vm.Warn("Erroneous data format for unserializing '" + ci.Name() + "'")
		u.fail(body)
		return vm.MakeNull(), pos, false
	}
	usesHook := !incomplete && ci != nil && ci.HasMethod("__unserialize")
	var dataArr *vm.Array
	if usesHook {
		dataArr = vm.NewArray()
	}
	u.pushCont(contIds{})
	defer u.popCont()
	if usesHook {
		u.pushCont(contIds{})
		defer u.popCont()
	}
	p := body
	for i := int64(0); i < nprops; i++ {
		if usesHook {
			if p < len(u.s) && u.s[p] == '}' {
				u.vm.Warn(u.prefix() + " Unexpected end of serialized data")
				u.fail(p)
				return vm.MakeNull(), pos, false
			}
			key, kp, ok := u.key(p)
			if !ok {
				u.fail(p)
				return vm.MakeNull(), pos, false
			}
			k2 := key
			placed := false
			u.pendingLoc = func(s vm.Slot) { dataArr.Set(k2, s); placed = true }
			savedDest := u.destIds
			u.destIds = u.destForArr(dataArr, key)
			before := len(u.shared.vals)
			val, vp, ok := u.value(kp)
			u.destIds = savedDest
			if u.ctx.HasThrown() {
				return *new(vm.Slot), *new(int), *new(bool)
			}
			u.pendingLoc = nil
			if !ok {
				return vm.MakeNull(), pos, false
			}
			if !placed {
				dataArr.Set(key, val)
			}
			u.recordArrDest(key, before)
			p = vp
			continue
		}
		// Ключ свойства: строка (s:...;) либо int (i:...;), который PHP
		// приводит к десятичной строке.
		var propName string
		var storeName string
		if p < len(u.s) && u.s[p] == 'i' {
			ik, kp, ok := u.key(p)
			if !ok {
				return vm.MakeNull(), pos, false
			}
			propName = strconv.FormatInt(ik.IntVal(), 10)
			storeName = propName
			p = kp
		} else if p < len(u.s) && u.s[p] == 's' {
			keySlot, kp, ok := u.parseString(p, false)
			if !ok {
				return vm.MakeNull(), pos, false
			}
			rawKey := keySlot.StringVal()
			propName = decodePropName(rawKey)
			storeName = propName
			// Необъявленное или манглированное private-свойство сохраняем под
			// исходным ключом (внутренний формат props / маркер видимости);
			// объявленные свойства (в т.ч. private) принимают значение в свой
			// слот, даже если ключ пришёл без манглирования (Zend:
			// is_property_visibility_changed).
			if incomplete || ci == nil {
				storeName = rawKey
			} else {
				storeName = u.vm.PropStoreKeyUnserialize(ci, rawKey, propName)
			}
			p = kp
		} else {
			if p < len(u.s) && u.s[p] == '}' {
				u.vm.Warn(u.prefix() + " Unexpected end of serialized data")
			}
			u.fail(p)
			return vm.MakeNull(), pos, false
		}
		placed := false
		if _, exists := o.GetProp(storeName); !exists {
			u.vm.MaybeDeprecateDynamicProp(ci, o, propName)
		}
		u.pendingLoc = func(s vm.Slot) {
			if u.vm.UnserIgnorePropWrite(ci, propName, s) {
				placed = true
				return
			}
			o.SetProp(storeName, s)
			u.vm.UnserAttachTypedRefSource(o, ci, propName, s)
			placed = true
		}
		savedDest := u.destIds
		u.destIds = u.destForObj(o, storeName)
		before := len(u.shared.vals)
		val, vp, ok := u.value(p)
		u.destIds = savedDest
		if u.ctx.HasThrown() {
			return *new(vm.Slot), *new(int), *new(bool)
		}
		u.pendingLoc = nil
		if !ok {
			return vm.MakeNull(), pos, false
		}
		if !placed {
			if len(u.shared.vals) > before {
				// Повторный ключ: в ячейке уже была запись var_hash — кладём
				// саму ячейку-ссылку (Zend: переменные указывают на один zval).
				if last := &u.shared.vals[len(u.shared.vals)-1]; last.sharedDest {
					val = vm.MakeRef(last.ref)
				}
			}
			if !u.vm.UnserIgnorePropWrite(ci, propName, val) {
				o.SetProp(storeName, val)
				u.vm.UnserAttachTypedRefSource(o, ci, propName, val)
			}
		} else if !incomplete && ci != nil {
			// Значение уже записано сеттером R:-ссылки; проверяем тип здесь.
			if verr := u.vm.UnserVerifyPropAssign(ci, propName, val); verr != nil {
				u.ctx.Throw(verr)
				if u.ctx.HasThrown() {
					return *new(vm.Slot), *new(int), *new(bool)
				}
			}
		}
		u.recordObjDest(storeName, before)
		if !placed && !incomplete && ci != nil {
			if verr := u.vm.UnserVerifyPropAssign(ci, propName, val); verr != nil {
				u.ctx.Throw(verr)
				if u.ctx.HasThrown() {
					return *new(vm.Slot), *new(int), *new(bool)
				}
			}
		}
		p = vp
	}
	if p >= len(u.s) || u.s[p] != '}' {
		u.fail(p)
		return vm.MakeNull(), pos, false
	}
	if ci != nil {
		if usesHook {
			u.shared.pending = append(u.shared.pending, uPending{obj: o, kind: 1, data: vm.MakeArray(dataArr)})
		} else if !incomplete && ci.HasMethod("__wakeup") {
			u.shared.pending = append(u.shared.pending, uPending{obj: o, kind: 0})
		}
	}
	return slot, p + 1, true
}

// parseCustom разбирает C:-форму (Serializable). Если класс найден и объявляет
// unserialize($data) — метод вызывается; иначе объект создаётся неполным.
func (u *phpUnser) parseCustom(pos int) (vm.Slot, int, bool) {
	if pos+1 >= len(u.s) || u.s[pos+1] != ':' {
		u.fail(pos)
		return vm.MakeNull(), pos, false
	}
	name, np, ok := u.parseName(pos + 2)
	if !ok {
		u.fail(pos)
		return vm.MakeNull(), pos, false
	}
	if np >= len(u.s) || u.s[np] != ':' {
		u.fail(np)
		return vm.MakeNull(), pos, false
	}
	if name == "" || name[0] == 0 || name[0] == '\\' || strings.IndexByte(name, 0) >= 0 {
		u.fail(pos)
		return vm.MakeNull(), pos, false
	}
	dlen, afterDigits := u.parseIV2(np + 1)
	if len(u.s)-afterDigits < 2 {
		u.fail(afterDigits)
		return vm.MakeNull(), pos, false
	}
	if u.s[afterDigits] != ':' {
		u.fail(afterDigits)
		return vm.MakeNull(), pos, false
	}
	if u.s[afterDigits+1] != '{' {
		u.fail(afterDigits + 1)
		return vm.MakeNull(), pos, false
	}
	body := afterDigits + 2
	if dlen < 0 || int64(len(u.s)-body) <= dlen {
		u.vm.Warn("Insufficient data for unserializing - " + strconv.FormatInt(dlen, 10) +
			" required, " + strconv.FormatInt(int64(len(u.s)-body), 10) + " present")
		u.fail(body)
		return vm.MakeNull(), pos, false
	}
	end := body + int(dlen)
	if u.s[end] != '}' {
		u.fail(end)
		return vm.MakeNull(), pos, false
	}
	idx := u.reserve()
	slot, incomplete := u.newObjectSlot(name)
	if u.ctx.HasThrown() {
		return *new(vm.Slot), *new(int), *new(bool)
	}
	o := u.vm.SlotObject(slot)
	u.fillReserved(idx, slot, o)
	ci := o.Class()
	if ci != nil && !incomplete && ci.IsNotSerializable() {
		u.ctx.Throw(vm.NewThrowable("Exception", "Unserialization of '"+vm.ClassDisplayName(ci.Name())+"' is not allowed"))
		if u.ctx.HasThrown() {
			return *new(vm.Slot), *new(int), *new(bool)
		}
	}
	switch {
	case incomplete:
		u.ctx.VM().Warn("Class " + incompleteClassName + " has no unserializer")
	case ci != nil && ci.HasMethod("unserialize"):
		_, err := ci.Call(o, u.ctx, "unserialize", []vm.Slot{vm.MakeString(u.s[body:end])})
		rethrowCall(u.ctx, err)
		if u.ctx.HasThrown() {
			return *new(vm.Slot), *new(int), *new(bool)
		}
	case ci != nil && isSplNativeSerializable(ci.Name()):
		u.parseSplCustom(ci, o, u.s[body:end])
		if u.ctx.HasThrown() {
			return *new(vm.Slot), *new(int), *new(bool)
		}
	default:
		u.ctx.VM().Warn("Class " + name + " has no unserializer")
	}
	return slot, end + 1, true
}

// isSplNativeSerializable сообщает, что класс использует нативный C-формат
// Serializable (ArrayObject/ArrayIterator/SplObjectStorage).
func isSplNativeSerializable(name string) bool {
	switch name {
	case "ArrayObject", "ArrayIterator", "SplObjectStorage":
		return true
	}
	return false
}

// parseSplCustom разбирает внутренний (C-)формат Serializable для нативных
// SPL-классов, переиспользуя общий var_hash.
func (u *phpUnser) parseSplCustom(ci vm.ClassInfo, o *vm.Object, data string) {
	popFrame := u.vm.PushInternalMethodFrame(ci, "unserialize", []vm.Slot{vm.MakeString(data)})
	defer popFrame()
	child := &phpUnser{vm: u.vm, ctx: u.ctx, s: data, shared: u.shared, warnPrefix: ci.Name() + "::unserialize():"}
	child.restrict, child.allowed, child.denyAll = u.restrict, u.allowed, u.denyAll
	failEx := func() {
		u.ctx.Throw(vm.NewThrowable("UnexpectedValueException",
			"Error at offset "+strconv.Itoa(child.errPos)+" of "+strconv.Itoa(len(data))+" bytes"))
		if u.ctx.HasThrown() {
			return
		}
	}
	if len(data) < 2 || data[0] != 'x' || data[1] != ':' {
		child.fail(0)
		failEx()
		if u.ctx.HasThrown() {
			return
		}
		return
	}
	p := 2
	first, np, ok := child.value(p)
	if !ok || first.Type() != vm.IntType {
		if ok {
			child.fail(p)
		}
		failEx()
		if u.ctx.HasThrown() {
			return
		}
		return
	}
	if ci.Name() == "SplObjectStorage" {
		u.parseSplStorageCustom(ci, o, data, child, first.ToInt(), np, failEx)
		if u.ctx.HasThrown() {
			return
		}
		return
	}
	p = np
	fl := first.ToInt()
	isSelf := fl&0x01000000 != 0
	storage := vm.Slot(vm.MakeNull())
	if !isSelf {
		if p >= len(data) || (data[p] != 'a' && data[p] != 'O' && data[p] != 'C' && data[p] != 'r') {
			child.fail(p)
			failEx()
			if u.ctx.HasThrown() {
				return
			}
			return
		}
		v, np2, ok := child.value(p)
		if !ok {
			failEx()
			if u.ctx.HasThrown() {
				return
			}
			return
		}
		storage = v
		p = np2
		if p >= len(data) || data[p] != ';' {
			child.fail(p)
			failEx()
			if u.ctx.HasThrown() {
				return
			}
			return
		}
		p++
	}
	if p+1 >= len(data) || data[p] != 'm' || data[p+1] != ':' {
		child.fail(p)
		failEx()
		if u.ctx.HasThrown() {
			return
		}
		return
	}
	p += 2
	members, _, ok := child.value(p)
	if !ok || members.Type() != vm.ArrayType {
		if ok {
			child.fail(p)
		}
		failEx()
		if u.ctx.HasThrown() {
			return
		}
		return
	}
	apply := vm.NewArray()
	apply.Set(vm.MakeIntKey(0), vm.MakeInt(fl))
	apply.Set(vm.MakeIntKey(1), storage)
	apply.Set(vm.MakeIntKey(2), members)
	apply.Set(vm.MakeIntKey(3), vm.MakeNull())
	if _, err := ci.Call(o, u.ctx, "__unserialize", []vm.Slot{vm.MakeArray(apply)}); err != nil {
		rethrowCall(u.ctx, err)
		if u.ctx.HasThrown() {
			return
		}
	}
}

// parseSplStorageCustom разбирает C-формат SplObjectStorage.
func (u *phpUnser) parseSplStorageCustom(ci vm.ClassInfo, o *vm.Object, data string, child *phpUnser, count int64, np int, failEx func()) {
	p := np - 1 // ';' после count
	pairs := vm.NewArray()
	var idx int64
	for ; count > 0; count-- {
		if p >= len(data) || data[p] != ';' {
			child.fail(p)
			failEx()
			return
		}
		p++
		if p >= len(data) || (data[p] != 'O' && data[p] != 'C' && data[p] != 'r') {
			child.fail(p)
			failEx()
			return
		}
		obj, np2, ok := child.value(p)
		if !ok || obj.Type() != vm.ObjectType {
			if ok {
				child.fail(p)
			}
			failEx()
			return
		}
		p = np2
		info := vm.Slot(vm.MakeNull())
		if p < len(data) && data[p] == ',' {
			p++
			inf, np3, ok := child.value(p)
			if !ok {
				failEx()
				return
			}
			info = inf
			p = np3
		}
		pairs.Set(vm.MakeIntKey(idx), obj)
		idx++
		pairs.Set(vm.MakeIntKey(idx), info)
		idx++
	}
	if p >= len(data) || data[p] != ';' {
		child.fail(p)
		failEx()
		return
	}
	p++
	if p+1 >= len(data) || data[p] != 'm' || data[p+1] != ':' {
		child.fail(p)
		failEx()
		return
	}
	p += 2
	members, _, ok := child.value(p)
	if !ok || members.Type() != vm.ArrayType {
		if ok {
			child.fail(p)
		}
		failEx()
		return
	}
	apply := vm.NewArray()
	apply.Set(vm.MakeIntKey(0), vm.MakeArray(pairs))
	apply.Set(vm.MakeIntKey(1), vm.MakeArray(vm.NewArray()))
	if _, err := ci.Call(o, u.ctx, "__unserialize", []vm.Slot{vm.MakeArray(apply)}); err != nil {
		rethrowCall(u.ctx, err)
		if u.ctx.HasThrown() {
			return
		}
	}
}

// key разбирает ключ массива/имя свойства (i:...; или s:...;) без регистрации id.
func (u *phpUnser) key(pos int) (vm.ArrayKey, int, bool) {
	if pos >= len(u.s) {
		u.fail(pos)
		return vm.ArrayKey{}, pos, false
	}
	switch u.s[pos] {
	case 'i':
		if pos+1 >= len(u.s) || u.s[pos+1] != ':' {
			u.fail(pos)
			return vm.ArrayKey{}, pos, false
		}
		n, np, ok := u.readNum(pos+2, ';')
		if !ok {
			u.fail(pos)
			return vm.ArrayKey{}, pos, false
		}
		return vm.MakeIntKey(n), np, true
	case 's':
		v, np, ok := u.parseString(pos, false)
		if !ok {
			u.fail(pos)
			return vm.ArrayKey{}, pos, false
		}
		s := v.StringVal()
		if n, ok := canonicalIntKey(s); ok {
			return vm.MakeIntKey(n), np, true
		}
		return vm.MakeStrKey(s), np, true
	case 'a':
		// Ключом не может быть массив; Zend парсит заголовок a:<n>:{ и падает
		// уже за '{' (var_hash для ключей NULL).
		q := pos + 2
		d0 := q
		for q < len(u.s) && u.s[q] >= '0' && u.s[q] <= '9' {
			q++
		}
		if q > d0 && q+1 < len(u.s) && u.s[q] == ':' && u.s[q+1] == '{' {
			u.fail(q + 2)
		} else {
			u.fail(pos)
		}
		return vm.ArrayKey{}, pos, false
	case 'R', 'r':
		// Ключ-ссылка: *p доводится до ';', затем падение.
		if pos+1 < len(u.s) && u.s[pos+1] == ':' {
			if _, np, ok := u.readUint(pos+2, ';'); ok {
				u.fail(np)
				return vm.ArrayKey{}, pos, false
			}
		}
		u.fail(pos)
		return vm.ArrayKey{}, pos, false
	}
	u.fail(pos)
	return vm.ArrayKey{}, pos, false
}

// canonicalIntKey повторяет правило PHP: каноническая десятичная строка
// (опц. '-', без ведущих нулей/плюса/пробелов, влезает в int64) становится
// int-ключом массива при unserialize.
func canonicalIntKey(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	body := s
	if s[0] == '-' {
		body = s[1:]
	}
	if body == "" {
		return 0, false
	}
	if body[0] == '0' && (body != "0" || s[0] == '-') {
		return 0, false
	}
	for i := 0; i < len(body); i++ {
		if body[i] < '0' || body[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// parseName читает <len>:"<bytes>" (без завершающей ';') и возвращает имя.
// Длина беззнаковая; при знаке возвращает ошибку без фиксации позиции (её
// выставляет вызывающий — как в Zend).
func (u *phpUnser) parseName(pos int) (string, int, bool) {
	l, np, ok := u.readUint(pos, ':')
	if !ok {
		return "", pos, false
	}
	if np >= len(u.s) || u.s[np] != '"' {
		u.fail(np)
		return "", pos, false
	}
	start := np + 1
	if l > int64(len(u.s)-start) {
		u.fail(pos)
		return "", pos, false
	}
	end := start + int(l)
	if end >= len(u.s) || u.s[end] != '"' {
		u.fail(end)
		return "", pos, false
	}
	return u.s[start:end], end + 1, true
}

// readUint читает беззнаковое десятичное число, ожидая delim сразу после цифр.
// Знак (+/-) считается ошибкой (длины и счётчики в PHP-сериализации
// беззнаковые).
func (u *phpUnser) readUint(pos int, delim byte) (int64, int, bool) {
	if pos >= len(u.s) || u.s[pos] < '0' || u.s[pos] > '9' {
		return 0, pos, false
	}
	var mag uint64
	for pos < len(u.s) && u.s[pos] >= '0' && u.s[pos] <= '9' {
		if mag > (1<<63-1)/10 {
			return 0, pos, false
		}
		mag = mag*10 + uint64(u.s[pos]-'0')
		pos++
	}
	if pos >= len(u.s) || u.s[pos] != delim {
		return 0, pos, false
	}
	return int64(mag), pos + 1, true
}

// readNum читает десятичное число (возможно отрицательное), начиная с pos,
// ожидая delim сразу после цифр. Возвращает значение и позицию за delim.
// Поддерживает весь диапазон int64 (для i:-значений); длины проверяются
// вызывающим по фактическому размеру входа.
func (u *phpUnser) readNum(pos int, delim byte) (int64, int, bool) {
	if pos >= len(u.s) {
		return 0, pos, false
	}
	neg := false
	if u.s[pos] == '-' {
		neg = true
		pos++
	}
	if pos >= len(u.s) || u.s[pos] < '0' || u.s[pos] > '9' {
		return 0, pos, false
	}
	// Накапливаем в uint64, чтобы принять -9223372036854775808 (PHP_INT_MIN).
	var mag uint64
	for pos < len(u.s) && u.s[pos] >= '0' && u.s[pos] <= '9' {
		if mag > (1<<64-1-9)/10 {
			return 0, pos, false
		}
		mag = mag*10 + uint64(u.s[pos]-'0')
		pos++
	}
	if pos >= len(u.s) || u.s[pos] != delim {
		return 0, pos, false
	}
	var n int64
	if neg {
		if mag > 1<<63 {
			return 0, pos, false
		}
		n = -int64(mag)
	} else {
		if mag > 1<<63-1 {
			return 0, pos, false
		}
		n = int64(mag)
	}
	return n, pos + 1, true
}

// decodePropName снимает манглирование private/protected (\0Class\0name /
// \0*\0name).
func decodePropName(k string) string {
	if len(k) > 0 && k[0] == 0 {
		if i := strings.IndexByte(k[1:], 0); i >= 0 {
			return k[1+i+1:]
		}
	}
	return k
}

// newObjectSlot создаёт объект заданного класса; для неизвестных классов —
// объект __PHP_Incomplete_Class с записанным именем.
func (u *phpUnser) newObjectSlot(name string) (vm.Slot, bool) {
	if name == "Generator" {
		u.ctx.Throw(vm.NewThrowable("Exception", "Unserialization of 'Generator' is not allowed"))
		if u.ctx.HasThrown() {
			return *new(vm.Slot), *new(bool)
		}
	}
	if u.restrict {
		lc := strings.ToLower(strings.TrimLeft(name, "\\"))
		if u.denyAll || !u.allowed[lc] {
			slot, o := u.vm.NewObjectOf(incompleteClassInfo)
			if o != nil {
				o.SetProp(incompleteNameProp, vm.MakeString(name))
			}
			return slot, true
		}
	}
	ci := u.vm.FindClass(name)
	if ci == nil {
		var aerr error
		u.vm.WithSerializeLock(func() { _, aerr = u.vm.Autoload(u.ctx, name) })
		if aerr != nil {
			rethrowCall(u.ctx, aerr)
			if u.ctx.HasThrown() {
				return *new(vm.Slot), *new(bool)
			}
		}
		ci = u.vm.FindClass(name)
	}
	if ci == nil {
		if cb, ok := IniValue("unserialize_callback_func"); ok && cb != "" {
			var cerr error
			u.vm.WithSerializeLock(func() {
				_, cerr = u.vm.CallCallable(u.ctx, vm.MakeString(cb), []vm.Slot{vm.MakeString(name)})
			})
			if cerr != nil {
				if strings.Contains(cerr.Error(), "undefined function") {
					u.ctx.Throw(vm.NewThrowable("Error", "Invalid callback "+cb+", function \""+cb+"\" not found or invalid function name"))
					if u.ctx.HasThrown() {
						return *new(vm.Slot), *new(bool)
					}
				}
				rethrowCall(u.ctx, cerr)
				if u.ctx.HasThrown() {
					return *new(vm.Slot), *new(bool)
				}
			}
			// Zend повторно пытается найти класс (что снова дёргает автозагрузчики).
			var aerr2 error
			u.vm.WithSerializeLock(func() { _, aerr2 = u.vm.Autoload(u.ctx, name) })
			if aerr2 != nil {
				rethrowCall(u.ctx, aerr2)
				if u.ctx.HasThrown() {
					return *new(vm.Slot), *new(bool)
				}
			}
			ci = u.vm.FindClass(name)
			if ci == nil {
				u.vm.Warn("unserialize(): Function " + cb + "() hasn't defined the class it was called for")
			}
		}
	}
	if ci == nil {
		slot, o := u.vm.NewObjectOf(incompleteClassInfo)
		if o != nil {
			o.SetProp(incompleteNameProp, vm.MakeString(name))
		}
		return slot, true
	}
	if ci.IsAbstract() {
		u.ctx.Throw(vm.NewThrowable("Error", "Cannot instantiate abstract class "+vm.ClassDisplayName(ci.Name())))
		if u.ctx.HasThrown() {
			return *new(vm.Slot), *new(bool)
		}
	}
	slot, _ := u.vm.NewObjectOf(ci)
	return slot, false
}
