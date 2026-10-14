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

// UnserVerifyPropAssign проверяет (strict, без приведения) совместимость
// значения v с объявленным типом свойства name при unserialize. Возвращает
// TypeError при несовместимости.
func (vm *VM) UnserVerifyPropAssign(ci ClassInfo, name string, v Slot) *Throwable {
	pd, decl, ok := propInfoForScope(nil, ci, name)
	if !ok || pd == nil || !pd.hasType {
		return nil
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
