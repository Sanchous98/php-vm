package vm

import (
	"strconv"
	"strings"
)

// ArgTypeError строит текст диагностики неверного типа аргумента в формате
// Zend (PHP 8.3):
//
//	<fn>(): Argument #<n> (<$param>) must be of type <want>, <got> given
//
// param может быть пустым — тогда "($param)" опускается (так PHP печатает
// вариативные аргументы builtin-функций без имени). got может быть пустым —
// тогда опускается ", <got> given". Если calledFile не пуст, к тексту
// добавляется ", called in <file> on line <line>".
//
// argNum — номер аргумента, начиная с 1 (как в тексте PHP).
func ArgTypeError(fnName string, argNum int, param, want, got, calledFile string, calledLine int) string {
	var b strings.Builder
	b.Grow(len(fnName) + len(param) + len(want) + len(got) + len(calledFile) + 48)
	b.WriteString(fnName)
	b.WriteString("(): Argument #")
	b.WriteString(strconv.Itoa(argNum))
	if param != "" {
		b.WriteString(" ($")
		b.WriteString(param)
		b.WriteString(")")
	}
	b.WriteString(" must be of type ")
	b.WriteString(want)
	if got != "" {
		b.WriteString(", ")
		b.WriteString(got)
		b.WriteString(" given")
	}
	if calledFile != "" {
		b.WriteString(", called in ")
		b.WriteString(calledFile)
		b.WriteString(" on line ")
		b.WriteString(strconv.Itoa(calledLine))
	}
	return b.String()
}

// argTypeError — TypeError с текстом ArgTypeError (для диагностик внутри VM).
func (vm *VM) argTypeError(fnName string, argNum int, param, want, got, calledFile string, calledLine int) *Throwable {
	return NewThrowable("TypeError", ArgTypeError(fnName, argNum, param, want, got, calledFile, calledLine))
}

// returnTypeError — TypeError "<fn>(): Return value must be of type <want>,
// <got> returned" (got == "none", если функция завершилась без return).
func (vm *VM) returnTypeError(fnName, want, got string) *Throwable {
	return NewThrowable("TypeError", fnName+"(): Return value must be of type "+want+", "+got+" returned")
}

// propTypeError — TypeError "Cannot assign <kind> to [reference held by]
// property <class>::$<name> of type <typ>".
func (vm *VM) propTypeError(kind, class, name, typ string, byRef bool) *Throwable {
	prefix := "Cannot assign " + kind + " to "
	if byRef {
		prefix += "reference held by "
	}
	return NewThrowable("TypeError", prefix+"property "+class+"::$"+name+" of type "+typ)
}

// UnserIgnorePropWrite сообщает, что запись значения в объявленное свойство
// при unserialize() должна быть молча пропущена: внутренние классы
// (DateInterval/DateTime и подобные) объявляют скалярные свойства виртуальными
// обработчиками, которые игнорируют массив/объект (bug69425).
func (vm *VM) UnserIgnorePropWrite(ci ClassInfo, name string, v Slot) bool {
	pd, decl, ok := propInfoForScope(nil, ci, name)
	if !ok || pd == nil || !pd.hasType {
		return false
	}
	rc, ok2 := asRuntimeClass(decl)
	if !ok2 || rc == nil || !rc.internal || !isScalarTypedProp(pd.typ) {
		return false
	}
	val := v
	if val.typ == RefType {
		val = deref(val)
	}
	switch val.typ {
	case ArrayType, ObjectType, ClosureType:
		return true
	}
	return false
}

// UnserAttachTypedRefSource помечает ячейку-ссылку, записанную при
// unserialize в объявленное типизированное свойство, источником типа (Zend
// ZEND_REF_ADD_TYPE_SOURCE в zend_std_write_property). Иначе последующая
// запись этой ссылки в свойство с другим типом не даёт TypeError
// (typed_property_refs).
func (vm *VM) UnserAttachTypedRefSource(o *Object, ci ClassInfo, name string, v Slot) {
	if v.typ != RefType {
		return
	}
	r := v.Ref()
	if r == nil {
		return
	}
	pd, decl, ok := propInfoForScope(nil, ci, name)
	if !ok || pd == nil || !pd.hasType {
		return
	}
	if g := guardFor(pd, decl, name); g != nil {
		r.addSourceDup(g)
	}
	if o != nil {
		o.MarkTypedRef()
	}
}

// isScalarTypedProp сообщает, что объявленный тип свойства — скалярный
// (int/float/string/bool/mixed): виртуальные обработчики внутренних классов
// молча игнорируют присваивание массива/объекта такому свойству.
func isScalarTypedProp(typ string) bool {
	switch strings.ToLower(typ) {
	case "int", "float", "string", "bool", "mixed":
		return true
	}
	return false
}

// UnserVerifyPropAssign проверяет (strict, без приведения) совместимость
// значения v с объявленным типом свойства name при unserialize. Возвращает
// TypeError при несовместимости.
func (vm *VM) UnserVerifyPropAssign(ci ClassInfo, name string, v Slot) *Throwable {
	pd, decl, ok := propInfoForScope(nil, ci, name)
	if !ok || pd == nil || !pd.hasType {
		return nil
	}
	if rc, ok2 := asRuntimeClass(decl); ok2 && rc != nil && rc.internal && isScalarTypedProp(pd.typ) {
		// Внутренние классы (DateInterval и подобные) объявляют скалярные
		// свойства через виртуальные обработчики: несовместимое значение
		// (массив/объект) молча игнорируется (bug69425).
		val := v
		if val.typ == RefType {
			val = deref(val)
		}
		switch val.typ {
		case ArrayType, ObjectType, ClosureType:
			return nil
		}
	}
	if v.typ == RefType {
		// Ссылка с источниками типа (Zend zend_verify_prop_assignable_by_ref
		// со strict=1 в var_unserializer): при строгой несовместимости
		// (result == 0) — обычная ошибка типа свойства; при result < 0
		// (только int->float) и успешном слабом приведении — «Reference with
		// value of type …» (typed_property_refs).
		if r := v.Ref(); r != nil && r.hasSources() {
			val := deref(v)
			res := vm.typeAssignableResult(pd.typ, val, true)
			if res > 0 {
				return nil
			}
			if res < 0 && vm.typeAssignableResult(pd.typ, val, false) < 0 {
				return vm.refTypeErrorType(r.sources[0], pd, decl, name, val)
			}
			return vm.propTypeError(valueTypeName(vm, val), classDisplayName(decl), name, vm.displayTypeResolved(pd.typ), false)
		}
	}
	val := v
	if val.typ == RefType {
		val = deref(val)
	}
	if ok, _ := vm.coerceReturnMode(pd.typ, val, true); ok {
		return nil
	}
	kind := valueTypeName(vm, val)
	return vm.propTypeError(kind, classDisplayName(decl), name, vm.displayTypeResolved(pd.typ), false)
}
