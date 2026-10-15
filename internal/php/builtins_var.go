package php

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"phpvm/internal/vm"
	"runtime"
	"strconv"
	"strings"
)

// Файл содержит функции переменных, констант и вывода значений
// (var_dump/var_export/print_r, gettype/count/strlen).

// --- переменные ---

func globalsArray(ctx *vm.Context) *vm.Array {
	if v, ok := ctx.VM().GlobalGet("$GLOBALS"); ok && v.Type() == vm.ArrayType {
		return v.AsArray()
	}
	if v, ok := ctx.VM().GlobalGet("GLOBALS"); ok && v.Type() == vm.ArrayType {
		return v.AsArray()
	}
	return ctx.VM().GlobalsSnapshot().AsArray()
}

func getDefinedVarsFn(ctx *vm.Context) vm.Value {
	m := ctx.VM()
	if m.InFunction() {
		// В области функции возвращаются только локальные переменные
		// (суперглобалы не включаются), как zend_get_defined_vars().
		out := vm.NewArray()
		for _, e := range m.DefinedLocalVars() {
			_ = out.SetStr(e.Name, e.Val)
		}
		return vm.MakeArray(out)
	}
	return vm.MakeArray(globalsArray(ctx))
}

func compactFn(ctx *vm.Context) vm.Value {
	if ctx.VM().InDynamicCall() {
		ctx.Throw(vm.NewThrowable("Error", "Cannot call compact() dynamically"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	out := vm.NewArray()
	seen := map[string]bool{}
	recursed := false
	// names собирает имена переменных; nonString — тип первого невалидного
	// значения (для warning'а "must be string or array of strings").
	var add func(s vm.Slot, path map[*vm.Array]bool)
	add = func(s vm.Slot, path map[*vm.Array]bool) {
		if recursed {
			return
		}
		switch s.Type() {
		case vm.ArrayType:
			a := s.AsArray()
			if path[a] {
				recursed = true
				return
			}
			path[a] = true
			for _, v := range a.Values() {
				add(v, path)
				if recursed {
					break
				}
			}
			delete(path, a)
			return
		case vm.StringType:
			name := s.ToString()
			if name == "" || seen[name] {
				return
			}
			seen[name] = true
			if name == "this" {
				// $this не является обычной переменной: без объектного
				// контекста compact('this') молча пропускается (без warning).
				if ts := ctx.ThisSlot(); ts.Type() == vm.ObjectType {
					_ = out.SetStr(name, ts)
				}
				return
			}
			if gv, ok := ctx.VM().SymtabGet(name); ok {
				_ = out.SetStr(name, vm.Deref(gv))
			} else {
				ctx.VM().Warning("compact(): Undefined variable $" + name)
			}
			return
		}
	}
	for i, a := range ctx.Args() {
		recursed = false
		add(a, map[*vm.Array]bool{})
		if recursed {
			ctx.Throw(vm.NewThrowable("Error", "Recursion detected"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.MakeNull()
		}
		// Валидация типа аргумента: допустимы string и array of strings.
		// PHP сообщает тип первого невалидного значения (для массива — тип
		// «плохого» элемента).
		if badType, bad := compactArgBad(a, map[*vm.Array]bool{}); bad {
			ctx.VM().Warning(fmt.Sprintf("compact(): Argument #%d must be string or array of strings, %s given",
				i+1, badType))
		}
	}
	return vm.MakeArray(out)
}

// compactArgBad возвращает имя типа первого невалидного значения (не строка и
// не массив строк) и признак наличия такого значения.
func compactArgBad(s vm.Slot, path map[*vm.Array]bool) (string, bool) {
	switch s.Type() {
	case vm.StringType:
		return "", false
	case vm.ArrayType:
		a := s.AsArray()
		if path[a] {
			return "", false
		}
		path[a] = true
		defer delete(path, a)
		for _, v := range a.Values() {
			if t, bad := compactArgBad(v, path); bad {
				return t, true
			}
		}
		return "", false
	}
	return slotTypeName(s), true
}

// slotTypeName возвращает имя PHP-типа слота для диагностик.
func slotTypeName(s vm.Slot) string {
	switch s.Type() {
	case vm.NullType:
		return "null"
	case vm.BoolType:
		if s.ToBool() {
			return "true"
		}
		return "false"
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
	case vm.ClosureType:
		return "Closure"
	case vm.ResourceType:
		return "resource"
	}
	return s.Type().String()
}

func validVarName(name string) bool {
	if name == "" {
		return false
	}
	// Правило Zend: [a-zA-Z_\x80-\xff][a-zA-Z0-9_\x80-\xff]* (байтовое,
	// поэтому не-ASCII идентификаторы вроде $æ считаются валидными).
	for i := 0; i < len(name); i++ {
		ch := name[i]
		if ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch >= 0x80 {
			continue
		}
		if i > 0 && ch >= '0' && ch <= '9' {
			continue
		}
		return false
	}
	return true
}

func extractFn(ctx *vm.Context) vm.Value {
	arr := requireArrayArg(ctx, 0, "extract")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	flags := int64(0)
	if len(ctx.Args()) >= 2 {
		flags = arg(ctx, 1).ToInt()
	}
	refsFlag := flags&256 != 0
	var arrSet func(*vm.Array)
	if refsFlag {
		if s0 := arg(ctx, 0); s0.IsRef() && s0.Ref() != nil {
			r0 := s0.Ref()
			arrSet = func(na *vm.Array) { r0.Set(vm.MakeArray(na)) }
		}
		// extract($GLOBALS, EXTR_REFS): привязка ссылок к снимку $GLOBALS ломает
		// наблюдаемость реальных глобалов — для $GLOBALS оставляем копирование.
		if g, ok := ctx.VM().GlobalGet("$GLOBALS"); ok {
			if ga := vm.Deref(g).AsArray(); ga != nil && ga == arr {
				refsFlag = false
				arrSet = nil
			}
		}
	}
	prefix := ""
	if len(ctx.Args()) >= 3 {
		prefix = arg(ctx, 2).ToString()
	}
	extrType := flags & 0xFF
	if extrType < 0 || extrType > 6 {
		ctx.Throw(vm.NewThrowable("ValueError",
			"extract(): Argument #2 ($flags) must be a valid extract type"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	needPrefix := extrType == 2 || extrType == 3 || extrType == 4 || extrType == 5
	if needPrefix {
		if len(ctx.Args()) < 3 {
			ctx.Throw(vm.NewThrowable("ValueError",
				"extract(): Argument #3 ($prefix) is required when using this extract type"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.MakeNull()
		}
		if prefix == "" {
			// Пустой префикс в PHP трактуется как "_".
			prefix = "_"
		} else if !validVarName(prefix) {
			ctx.Throw(vm.NewThrowable("ValueError",
				"extract(): Argument #3 ($prefix) must be a valid identifier"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.MakeNull()
		}
	}
	m := ctx.VM()
	count := 0
	for k, v := range arr.All() {
		name := k.PHPString()
		// PHP не создаёт переменную из пустого строкового ключа при
		// EXTR_PREFIX_ALL (валидируется итоговое имя, но пустой ключ
		// отбрасывается отдельно).
		if extrType == 3 && k.IsStr() && name == "" {
			continue
		}
		valid := validVarName(name)
		exists := m.SymtabHas(name)
		// $this — особая переменная: extract() с ключом "this" бросает Error
		// независимо от того, привязан ли $this (проверено на php 8.3).
		isThis := name == "this"
		if isThis {
			// $this — особая переменная: переприсвоить нельзя.
			switch extrType {
			case 0: // EXTR_OVERWRITE
				ctx.Throw(vm.NewThrowable("Error", "Cannot re-assign $this"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
				return vm.MakeNull()
			case 1, 5, 6: // SKIP / PREFIX_IF_EXISTS / IF_EXISTS
				continue
			default: // PREFIX_SAME / PREFIX_ALL / PREFIX_INVALID
				name = prefix + "_" + name
				throwCallable(ctx, m.SymtabSet(name, v))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
				count++
				continue
			}
		}
		switch extrType {
		case 1: // EXTR_SKIP
			if exists {
				continue
			}
		case 2: // EXTR_PREFIX_SAME
			if exists {
				name = prefix + "_" + name
			}
		case 3: // EXTR_PREFIX_ALL
			name = prefix + "_" + name
		case 4: // EXTR_PREFIX_INVALID
			if !valid {
				name = prefix + "_" + name
			}
		case 5: // EXTR_PREFIX_IF_EXISTS
			if exists {
				name = prefix + "_" + name
			} else {
				continue
			}
		case 6: // EXTR_IF_EXISTS
			if !exists {
				continue
			}
		}
		if !validVarName(name) {
			continue
		}
		if name == "GLOBALS" {
			// Нельзя перезаписать $GLOBALS, но PHP всё равно учитывает его.
			count++
			continue
		}
		if refsFlag {
			slot := v
			if !(v.IsRef() && v.Ref() != nil) {
				slot = vm.NewRef(v)
				aa := arr.Unshare()
				if err := aa.Set(k, slot); err == nil {
					arr = aa
				} else {
					continue
				}
			}
			m.SymtabBindRef(name, slot)
			count++
			continue
		}
		throwCallable(ctx, m.SymtabSet(name, v))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		count++
	}
	if refsFlag && arrSet != nil {
		arrSet(arr)
	}
	return vm.Int(int64(count))
}

// --- ошибки ---

func triggerErrorFn(ctx *vm.Context) vm.Value {
	a := ctx.Args()
	msg := ""
	if len(a) > 0 {
		msg = a[0].ToString()
	}
	level := int64(1024) // E_USER_NOTICE
	if len(a) > 1 {
		level = a[1].ToInt()
		switch level {
		case 256, 512, 1024, 16384:
		default:
			ctx.Throw(vm.NewThrowable("ValueError",
				"trigger_error(): Argument #2 ($error_level) must be one of E_USER_ERROR, E_USER_WARNING, E_USER_NOTICE, or E_USER_DEPRECATED"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
	}
	lastErrorArray = vm.NewArray()
	_ = lastErrorArray.SetStr("type", vm.MakeInt(level))
	_ = lastErrorArray.SetStr("message", vm.MakeString(msg))
	_ = lastErrorArray.SetStr("file", vm.MakeString("php"))
	_ = lastErrorArray.SetStr("line", vm.MakeInt(0))
	ctx.VM().UserError(level, msg)
	return vm.Bool(true)
}

func errorGetLastFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) > 0 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "error_get_last() expects exactly 0 arguments, "+strconv.Itoa(len(ctx.Args()))+" given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	level, msg, file, line, ok := ctx.VM().LastError()
	if !ok {
		return vm.MakeNull()
	}
	out := vm.NewArray()
	_ = out.SetStr("type", vm.MakeInt(level))
	_ = out.SetStr("message", vm.MakeString(msg))
	_ = out.SetStr("file", vm.MakeString(file))
	_ = out.SetStr("line", vm.MakeInt(int64(line)))
	return vm.MakeArray(out)
}

// --- отладка ---

func debugBacktraceFn(ctx *vm.Context) vm.Value {
	options := int64(1) // DEBUG_BACKTRACE_PROVIDE_OBJECT
	limit := 0
	a := ctx.Args()
	if len(a) >= 1 {
		options = derefSlot(a[0]).ToInt()
	}
	if len(a) >= 2 {
		limit = int(derefSlot(a[1]).ToInt())
	}
	return ctx.VM().DebugBacktraceArray(options, limit)
}

// --- константы/функции ---

func getDefinedConstantsFn(ctx *vm.Context) vm.Value {
	categorize := false
	if len(ctx.Args()) >= 1 {
		categorize = arg0(ctx).ToBool()
	}
	internal := vm.NewArray()
	for _, n := range definedConstNames {
		if v, ok := stdConst(n); ok {
			_ = internal.SetStr(n, v)
		}
	}
	user := vm.NewArray()
	for _, n := range userConstNames {
		if v, ok := ctx.VM().Const(n); ok {
			_ = user.SetStr(n, v)
		}
	}
	if categorize {
		out := vm.NewArray()
		_ = out.SetStr("internal", vm.MakeArray(internal))
		_ = out.SetStr("user", vm.MakeArray(user))
		return vm.MakeArray(out)
	}
	// Без аргумента/при false — плоский массив всех констант.
	out := vm.NewArray()
	for k, v := range internal.All() {
		if k.IsStr() {
			_ = out.SetStr(k.StrVal(), v)
		} else {
			_ = out.SetInt(k.IntVal(), v)
		}
	}
	for k, v := range user.All() {
		if k.IsStr() {
			_ = out.SetStr(k.StrVal(), v)
		} else {
			_ = out.SetInt(k.IntVal(), v)
		}
	}
	return vm.MakeArray(out)
}

func getDefinedFunctionsFn(ctx *vm.Context) vm.Value {
	// $exclude_disabled жёстко зафиксирован в true; передача false выдаёт
	// Deprecated, но поведение не меняется (см. bug #31875).
	if len(ctx.Args()) >= 1 && arg0(ctx).Type() == vm.BoolType && !arg0(ctx).ToBool() {
		ctx.VM().Deprecated("get_defined_functions(): Setting $exclude_disabled to false has no effect")
	}
	disabled := disabledFunctionSet()
	out := vm.NewArray()
	internal := vm.NewArray()
	for _, n := range internalFuncNames {
		if disabled[n] {
			continue
		}
		_, _ = internal.Append(vm.MakeString(n))
	}
	// Пользовательские функции: только функции (не методы), в нижнем регистре
	// (Zend хранит ключи таблицы функций в нижнем регистре).
	internalSet := map[string]bool{}
	for _, n := range internalFuncNames {
		internalSet[n] = true
	}
	user := vm.NewArray()
	for _, f := range ctx.VMInstance().Functions() {
		name := f.Name
		if name == "" || strings.Contains(name, "::") || strings.HasPrefix(name, "{closure") {
			continue
		}
		if internalSet[name] || strings.HasPrefix(name, "__php_") {
			continue
		}
		if _, isNative := f.Executable.(*vm.NativeFunction); isNative {
			continue
		}
		_, _ = user.Append(vm.MakeString(strings.ToLower(name)))
	}
	_ = out.SetStr("internal", vm.MakeArray(internal))
	_ = out.SetStr("user", vm.MakeArray(user))
	return vm.MakeArray(out)
}

func defineFn(ctx *vm.Context) vm.Value {
	// define(string $constant_name, mixed $value): массив (как и объект без
	// __toString) в weak-режиме не приводится к строке — TypeError.
	if a0 := arg(ctx, 0); a0.Type() == vm.ArrayType ||
		(a0.Type() == vm.ObjectType && !ctx.VM().IsStringable(a0)) ||
		a0.Type() == vm.ResourceType ||
		(ctx.VM().StrictTypes() && a0.Type() != vm.StringType) {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError("define", 1, "constant_name", "string", phpTypeName(ctx, 0), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	name := arg(ctx, 0).ToString()
	// Имя __COMPILER_HALT_OFFSET__ зарезервировано Zend (регистрируется при
	// старте как специальная константа): define() предупреждает и не определяет.
	if name == "__COMPILER_HALT_OFFSET__" {
		ctx.VM().Warn("Constant __COMPILER_HALT_OFFSET__ already defined")
		return vm.Bool(true)
	}
	if strings.Contains(name, "::") {
		ctx.Throw(vm.NewThrowable("ValueError", "define(): Argument #1 ($constant_name) cannot be a class constant"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	val := vm.MakeNull()
	if len(ctx.Args()) >= 2 {
		val = ctx.Args()[1]
	}
	// Повторное определение константы: Zend печатает Warning и возвращает
	// false (для define() и для `const X = ...;`, который компилируется в этот
	// же вызов).
	if _, ok := ctx.VMInstance().Const(name); ok {
		ctx.VM().Warn("Constant " + name + " already defined")
		return vm.Bool(false)
	}
	if _, ok := constValue(name); ok {
		ctx.VM().Warn("Constant " + name + " already defined")
		return vm.Bool(false)
	}
	if _, ok := stdConst(name); ok {
		ctx.VM().Warn("Constant " + name + " already defined")
		return vm.Bool(false)
	}
	dv, derr := vm.DeepDerefSlot(val)
	if derr != nil {
		ctx.Throw(vm.NewThrowable("ValueError", derr.Error()))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	ctx.VMInstance().DefineConst(name, dv)
	if !userConstSet[name] {
		userConstSet[name] = true
		userConstNames = append(userConstNames, name)
	}
	return vm.Bool(true)
}

func definedFn(ctx *vm.Context) vm.Value {
	name := strings.TrimPrefix(arg0(ctx).ToString(), "\\")
	if _, ok := ctx.VMInstance().Const(name); ok {
		return vm.Bool(true)
	}
	if _, ok := constValue(name); ok {
		return vm.Bool(true)
	}
	if _, ok := stdConst(name); ok {
		return vm.Bool(true)
	}
	if _, ok := reflectClassConst(ctx, name); ok {
		return vm.Bool(true)
	}
	return vm.Bool(false)
}

func constantFn(ctx *vm.Context) vm.Value {
	name := strings.TrimPrefix(arg0(ctx).ToString(), "\\")
	if v, ok := ctx.VMInstance().Const(name); ok {
		return v
	}
	return constLookupTail(ctx, name, name)
}

// constLookupFn — рантайм-разрешение ссылки на константу, неизвестной на этапе
// компиляции. PHP сначала ищет имя в текущем пространстве имён, затем (для
// неквалифицированного имени) глобальный fallback; в диагностике используется
// первичное имя (как Zend).
func constLookupFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	name := strings.TrimPrefix(args[0].ToString(), "\\")
	if v, ok := ctx.VMInstance().Const(name); ok {
		return v
	}
	if len(args) > 1 {
		fb := strings.TrimPrefix(args[1].ToString(), "\\")
		if fb != "" && fb != name {
			if v, ok := ctx.VMInstance().Const(fb); ok {
				return v
			}
			// Встроенные константы существуют только под глобальным именем.
			return constLookupTail(ctx, fb, name)
		}
	}
	return constLookupTail(ctx, name, name)
}

// constLookupTail — общий хвост поиска: константы классов, встроенные
// константы, ресурсы; при неудаче — Error с первичным именем reportName.
func constLookupTail(ctx *vm.Context, name, reportName string) vm.Value {
	if strings.HasPrefix(name, "ASSERT_") || name == "FILE_BINARY" || name == "FILE_TEXT" {
		ctx.VM().Deprecated("Constant " + name + " is deprecated")
	}
	if i := strings.Index(name, "::"); i >= 0 {
		if ctx.VM().ScopedClass(name[:i]) == nil {
			// Zend вызывает автозагрузчик (FETCH_CONSTANT); его исключения
			// пробрасываются наружу.
			if _, aerr := ctx.VMInstance().Autoload(nil, name[:i]); aerr != nil {
				if vm.IsExitSignal(aerr) {
					// Автозагрузчик завершил скрипт (exit/die): останавливаем
					// исполнение, а не превращаем сигнал в Error (bug42798).
					ctx.VM().RequestHalt()
					return *new(vm.Value)
				}
				if th, ok := aerr.(*vm.Throwable); ok {
					ctx.Throw(th)
				} else {
					ctx.Throw(vm.NewThrowable("Error", aerr.Error()))
				}
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
				return vm.MakeNull()
			}
		}
		ci := ctx.VM().ScopedClass(name[:i])
		if ci == nil {
			ctx.Throw(vm.NewThrowable("Error", "Class \""+name[:i]+"\" not found"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.MakeNull()
		}
		full := ci.Name() + "::" + name[i+2:]
		v, ok, err := ctx.VM().ClassConstAccess(full)
		if err != nil {
			if th, ok := err.(*vm.Throwable); ok {
				ctx.Throw(th)
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			} else {
				ctx.Throw(vm.NewThrowable("Error", err.Error()))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			return vm.MakeNull()
		}
		if ok {
			return v
		}
		// Как Zend: class-константа с ненайденным именем — Error без кавычек
		// ("Undefined constant A::B1", bug51791).
		ctx.Throw(vm.NewThrowable("Error", "Undefined constant "+name))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	if cv, ok := constValue(name); ok {
		return cv.Slot()
	}
	if v, ok := stdStreamConst(name, ctx.VM()); ok {
		return v
	}
	if v, ok := stdConst(name); ok {
		return v
	}
	if v, ok := reflectClassConst(ctx, name); ok {
		return v
	}
	// PHP 8: неопределённая константа — Error (даже для constant()).
	ctx.Throw(vm.NewThrowable("Error", `Undefined constant "`+reportName+`"`))
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return vm.MakeNull()
}

// constExprConstantFn — как constantFn, но для константных выражений:
// неопределённая глобальная константа — фатал (PHP: Undefined constant "X").
// escapeshellarg проверяет длину/нулевые байты как PHP и экранирует аргумент.
const escapeShellMaxLen = 2097152

func escapeShellArgFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	if strings.IndexByte(s, 0) >= 0 {
		ctx.Throw(vm.NewThrowable("ValueError", "escapeshellarg(): Argument #1 ($arg) must not contain any null bytes"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	if len(s) > escapeShellMaxLen {
		ctx.Throw(vm.NewFatal("escapeshellarg(): Argument exceeds the allowed length of 2097152 bytes"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	return vm.Str(escapeshellarg(s))
}

func escapeShellCmdFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	if strings.IndexByte(s, 0) >= 0 {
		ctx.Throw(vm.NewThrowable("ValueError", "escapeshellcmd(): Argument #1 ($command) must not contain any null bytes"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	if len(s) > escapeShellMaxLen {
		ctx.Throw(vm.NewFatal("escapeshellcmd(): Command exceeds the allowed length of 2097152 bytes"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	return vm.Str(escapeshellcmd(s))
}

func constExprConstantFn(ctx *vm.Context) vm.Value {
	name := strings.TrimPrefix(arg0(ctx).ToString(), "\\")
	if v, ok := ctx.VMInstance().Const(name); ok {
		return v
	}
	if i := strings.Index(name, "::"); i >= 0 {
		if ctx.VM().ScopedClass(name[:i]) == nil {
			if _, aerr := ctx.VMInstance().Autoload(nil, name[:i]); aerr != nil {
				if vm.IsExitSignal(aerr) {
					// Автозагрузчик завершил скрипт (exit/die): останавливаем
					// исполнение, а не превращаем сигнал в Error (bug42798).
					ctx.VM().RequestHalt()
					return *new(vm.Value)
				}
				if th, ok := aerr.(*vm.Throwable); ok {
					ctx.Throw(th)
				} else {
					ctx.Throw(vm.NewThrowable("Error", aerr.Error()))
				}
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
				return vm.MakeNull()
			}
		}
		if ctx.VM().ScopedClass(name[:i]) == nil {
			ctx.Throw(vm.NewThrowable("Error", "Class \""+name[:i]+"\" not found"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.MakeNull()
		}
		if v, ok := reflectClassConst(ctx, name); ok {
			return v
		}
		ctx.Throw(vm.NewThrowable("Error", "Undefined constant "+name))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	if cv, ok := constValue(name); ok {
		return cv.Slot()
	}
	if strings.HasPrefix(name, "ASSERT_") || name == "FILE_BINARY" || name == "FILE_TEXT" {
		ctx.VM().Deprecated("Constant " + name + " is deprecated")
	}
	if v, ok := stdStreamConst(name, ctx.VM()); ok {
		return v
	}
	if v, ok := stdConst(name); ok {
		return v
	}
	if v, ok := reflectClassConst(ctx, name); ok {
		return v
	}
	ctx.Throw(vm.NewThrowable("Error", "Undefined constant \""+name+"\""))
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return vm.MakeNull()
}

// stdConst возвращает значения стандартных констант, которые не «зашиты» на
// этапе компиляции (компилятор направляет неизвестные константы в constant()).
// phpOSFamily — значение константы PHP_OS_FAMILY (платформа сборки).
func phpOSFamily() string {
	switch runtime.GOOS {
	case "linux":
		return "Linux"
	case "darwin":
		return "Darwin"
	case "windows":
		return "Windows"
	case "freebsd", "openbsd", "netbsd", "dragonfly":
		return "BSD"
	case "solaris", "illumos":
		return "Solaris"
	}
	return "Unknown"
}

// phpBinaryPath — значение константы PHP_BINARY: путь к запущенному php
// (тестовый харнесс выставляет TEST_PHP_EXECUTABLE).
func phpBinaryPath() string {
	if p := os.Getenv("TEST_PHP_EXECUTABLE"); p != "" {
		return p
	}
	if p, err := exec.LookPath("php"); err == nil {
		return p
	}
	return "php"
}

func stdConst(name string) (vm.Slot, bool) {
	switch name {
	case "ASSERT_ACTIVE":
		return vm.MakeInt(1), true
	case "ASSERT_CALLBACK":
		return vm.MakeInt(2), true
	case "ASSERT_BAIL":
		return vm.MakeInt(3), true
	case "ASSERT_WARNING":
		return vm.MakeInt(4), true
	case "ASSERT_EXCEPTION":
		return vm.MakeInt(5), true
	case "EXTR_OVERWRITE":
		return vm.MakeInt(0), true
	case "EXTR_SKIP":
		return vm.MakeInt(1), true
	case "EXTR_PREFIX_SAME":
		return vm.MakeInt(2), true
	case "EXTR_PREFIX_ALL":
		return vm.MakeInt(3), true
	case "EXTR_PREFIX_INVALID":
		return vm.MakeInt(4), true
	case "EXTR_PREFIX_IF_EXISTS":
		return vm.MakeInt(5), true
	case "EXTR_IF_EXISTS":
		return vm.MakeInt(6), true
	case "EXTR_REFS":
		return vm.MakeInt(256), true
	case "E_ERROR":
		return vm.MakeInt(1), true
	case "E_WARNING":
		return vm.MakeInt(2), true
	case "E_PARSE":
		return vm.MakeInt(4), true
	case "E_NOTICE":
		return vm.MakeInt(8), true
	case "E_CORE_ERROR":
		return vm.MakeInt(16), true
	case "E_CORE_WARNING":
		return vm.MakeInt(32), true
	case "E_COMPILE_ERROR":
		return vm.MakeInt(64), true
	case "E_COMPILE_WARNING":
		return vm.MakeInt(128), true
	case "E_USER_ERROR":
		return vm.MakeInt(256), true
	case "E_USER_WARNING":
		return vm.MakeInt(512), true
	case "E_USER_NOTICE":
		return vm.MakeInt(1024), true
	case "E_STRICT":
		return vm.MakeInt(2048), true
	case "E_RECOVERABLE_ERROR":
		return vm.MakeInt(4096), true
	case "E_DEPRECATED":
		return vm.MakeInt(8192), true
	case "E_USER_DEPRECATED":
		return vm.MakeInt(16384), true
	case "E_ALL":
		return vm.MakeInt(32767), true
	case "PHP_OUTPUT_HANDLER_START":
		return vm.MakeInt(1), true
	case "PHP_OUTPUT_HANDLER_WRITE":
		return vm.MakeInt(0), true
	case "PHP_OUTPUT_HANDLER_FLUSH":
		return vm.MakeInt(4), true
	case "PHP_OUTPUT_HANDLER_CLEAN":
		return vm.MakeInt(2), true
	case "PHP_OUTPUT_HANDLER_FINAL":
		return vm.MakeInt(8), true
	case "PHP_OUTPUT_HANDLER_CONT":
		return vm.MakeInt(0), true
	case "PHP_OUTPUT_HANDLER_END":
		return vm.MakeInt(8), true
	case "PHP_OUTPUT_HANDLER_CLEANABLE":
		return vm.MakeInt(16), true
	case "PHP_OUTPUT_HANDLER_FLUSHABLE":
		return vm.MakeInt(32), true
	case "PHP_OUTPUT_HANDLER_REMOVABLE":
		return vm.MakeInt(64), true
	case "PHP_OUTPUT_HANDLER_STDFLAGS":
		return vm.MakeInt(112), true
	case "INI_SCANNER_NORMAL":
		return vm.MakeInt(0), true
	case "INI_SCANNER_RAW":
		return vm.MakeInt(1), true
	case "INI_SCANNER_TYPED":
		return vm.MakeInt(2), true
	case "CREDITS_GROUP":
		return vm.MakeInt(1), true
	case "CREDITS_GENERAL":
		return vm.MakeInt(2), true
	case "CREDITS_SAPI":
		return vm.MakeInt(4), true
	case "CREDITS_MODULES":
		return vm.MakeInt(8), true
	case "CREDITS_DOCS":
		return vm.MakeInt(16), true
	case "CREDITS_FULLPAGE":
		return vm.MakeInt(32), true
	case "CREDITS_QA":
		return vm.MakeInt(64), true
	case "CREDITS_ALL":
		return vm.MakeInt(-1), true
	case "INFO_GENERAL":
		return vm.MakeInt(1), true
	case "INFO_CREDITS":
		return vm.MakeInt(2), true
	case "INFO_CONFIGURATION":
		return vm.MakeInt(4), true
	case "INFO_MODULES":
		return vm.MakeInt(8), true
	case "INFO_ENVIRONMENT":
		return vm.MakeInt(16), true
	case "INFO_VARIABLES":
		return vm.MakeInt(32), true
	case "INFO_LICENSE":
		return vm.MakeInt(64), true
	case "INFO_ALL":
		return vm.MakeInt(-1), true
	case "ENT_NOQUOTES":
		return vm.MakeInt(0), true
	case "ENT_COMPAT":
		return vm.MakeInt(2), true
	case "ENT_QUOTES":
		return vm.MakeInt(3), true
	case "HTML_SPECIALCHARS":
		return vm.MakeInt(0), true
	case "HTML_ENTITIES":
		return vm.MakeInt(1), true
	case "ENT_IGNORE":
		return vm.MakeInt(4), true
	case "ENT_SUBSTITUTE":
		return vm.MakeInt(8), true
	case "ENT_DISALLOWED":
		return vm.MakeInt(128), true
	case "ENT_HTML401":
		return vm.MakeInt(0), true
	case "ENT_XML1":
		return vm.MakeInt(16), true
	case "ENT_XHTML":
		return vm.MakeInt(32), true
	case "ENT_HTML5":
		return vm.MakeInt(48), true
	case "LC_ALL":
		return vm.MakeInt(6), true
	case "LC_COLLATE":
		return vm.MakeInt(3), true
	case "LC_CTYPE":
		return vm.MakeInt(0), true
	case "LC_MONETARY":
		return vm.MakeInt(4), true
	case "LC_NUMERIC":
		return vm.MakeInt(1), true
	case "LC_TIME":
		return vm.MakeInt(2), true
	case "LC_MESSAGES":
		return vm.MakeInt(5), true
	case "IMAGETYPE_UNKNOWN":
		return vm.MakeInt(imageFiletypeUnknown), true
	case "IMAGETYPE_GIF":
		return vm.MakeInt(imageFiletypeGIF), true
	case "IMAGETYPE_JPEG":
		return vm.MakeInt(imageFiletypeJPEG), true
	case "IMAGETYPE_PNG":
		return vm.MakeInt(imageFiletypePNG), true
	case "IMAGETYPE_SWF":
		return vm.MakeInt(imageFiletypeSWF), true
	case "IMAGETYPE_PSD":
		return vm.MakeInt(imageFiletypePSD), true
	case "IMAGETYPE_BMP":
		return vm.MakeInt(imageFiletypeBMP), true
	case "IMAGETYPE_TIFF_II":
		return vm.MakeInt(imageFiletypeTIFFII), true
	case "IMAGETYPE_TIFF_MM":
		return vm.MakeInt(imageFiletypeTIFFMM), true
	case "IMAGETYPE_JPC":
		return vm.MakeInt(imageFiletypeJPC), true
	case "IMAGETYPE_JPEG2000":
		return vm.MakeInt(imageFiletypeJPC), true
	case "IMAGETYPE_JP2":
		return vm.MakeInt(imageFiletypeJP2), true
	case "IMAGETYPE_JPX":
		return vm.MakeInt(imageFiletypeJPX), true
	case "IMAGETYPE_JB2":
		return vm.MakeInt(imageFiletypeJB2), true
	case "IMAGETYPE_SWC":
		return vm.MakeInt(imageFiletypeSWC), true
	case "IMAGETYPE_IFF":
		return vm.MakeInt(imageFiletypeIFF), true
	case "IMAGETYPE_WBMP":
		return vm.MakeInt(imageFiletypeWBMP), true
	case "IMAGETYPE_XBM":
		return vm.MakeInt(imageFiletypeXBM), true
	case "IMAGETYPE_ICO":
		return vm.MakeInt(imageFiletypeICO), true
	case "IMAGETYPE_WEBP":
		return vm.MakeInt(imageFiletypeWEBP), true
	case "IMAGETYPE_AVIF":
		return vm.MakeInt(imageFiletypeAVIF), true
	case "DNS_A":
		return vm.MakeInt(dnsA), true
	case "DNS_NS":
		return vm.MakeInt(dnsNS), true
	case "DNS_CNAME":
		return vm.MakeInt(dnsCNAME), true
	case "DNS_SOA":
		return vm.MakeInt(dnsSOA), true
	case "DNS_PTR":
		return vm.MakeInt(dnsPTR), true
	case "DNS_HINFO":
		return vm.MakeInt(dnsHINFO), true
	case "DNS_MX":
		return vm.MakeInt(dnsMX), true
	case "DNS_TXT":
		return vm.MakeInt(dnsTXT), true
	case "DNS_SRV":
		return vm.MakeInt(dnsSRV), true
	case "DNS_NAPTR":
		return vm.MakeInt(dnsNAPTR), true
	case "DNS_AAAA":
		return vm.MakeInt(dnsAAAA), true
	case "DNS_A6":
		return vm.MakeInt(dnsA6), true
	case "DNS_ANY":
		return vm.MakeInt(dnsANY), true
	case "DNS_ALL":
		return vm.MakeInt(dnsALL), true
	case "PASSWORD_BCRYPT", "PASSWORD_DEFAULT":
		return vm.MakeString(passwordBcryptAlgo), true
	case "PASSWORD_BCRYPT_DEFAULT_COST":
		return vm.MakeInt(passwordBcryptDefaultCost), true
	case "PASSWORD_ARGON2I":
		return vm.MakeString("argon2i"), true
	case "PASSWORD_ARGON2ID":
		return vm.MakeString("argon2id"), true
	case "PASSWORD_ARGON2_DEFAULT_MEMORY_COST":
		return vm.MakeInt(65536), true
	case "PASSWORD_ARGON2_DEFAULT_TIME_COST":
		return vm.MakeInt(4), true
	case "PASSWORD_ARGON2_DEFAULT_THREADS":
		return vm.MakeInt(1), true
	case "PHP_ROUND_HALF_UP":
		return vm.MakeInt(1), true
	case "PHP_ROUND_HALF_DOWN":
		return vm.MakeInt(2), true
	case "PHP_ROUND_HALF_EVEN":
		return vm.MakeInt(3), true
	case "PHP_ROUND_HALF_ODD":
		return vm.MakeInt(4), true
	case "STR_PAD_LEFT":
		return vm.MakeInt(0), true
	case "STR_PAD_RIGHT":
		return vm.MakeInt(1), true
	case "STR_PAD_BOTH":
		return vm.MakeInt(2), true
	case "COUNT_NORMAL":
		return vm.MakeInt(0), true
	case "COUNT_RECURSIVE":
		return vm.MakeInt(1), true
	case "CASE_LOWER":
		return vm.MakeInt(0), true
	case "CASE_UPPER":
		return vm.MakeInt(1), true
	case "SORT_REGULAR":
		return vm.MakeInt(0), true
	case "SORT_NUMERIC":
		return vm.MakeInt(1), true
	case "SORT_STRING":
		return vm.MakeInt(2), true
	case "SORT_NATURAL":
		return vm.MakeInt(6), true
	case "SORT_FLAG_CASE":
		return vm.MakeInt(8), true
	case "SORT_DESC":
		return vm.MakeInt(3), true
	case "INI_USER":
		return vm.MakeInt(1), true
	case "INI_PERDIR":
		return vm.MakeInt(2), true
	case "INI_SYSTEM":
		return vm.MakeInt(4), true
	case "INI_ALL":
		return vm.MakeInt(7), true
	case "CONNECTION_NORMAL":
		return vm.MakeInt(0), true
	case "CONNECTION_ABORTED":
		return vm.MakeInt(1), true
	case "CONNECTION_TIMEOUT":
		return vm.MakeInt(2), true
	case "LOG_EMERG":
		return vm.MakeInt(0), true
	case "LOG_ALERT":
		return vm.MakeInt(1), true
	case "LOG_CRIT":
		return vm.MakeInt(2), true
	case "LOG_ERR":
		return vm.MakeInt(3), true
	case "LOG_WARNING":
		return vm.MakeInt(4), true
	case "LOG_NOTICE":
		return vm.MakeInt(5), true
	case "LOG_INFO":
		return vm.MakeInt(6), true
	case "LOG_DEBUG":
		return vm.MakeInt(7), true
	case "LOG_CONS", "LOG_ODELAY":
		return vm.MakeInt(4), true
	case "LOG_NDELAY", "LOG_USER":
		return vm.MakeInt(8), true
	case "LOG_NOWAIT":
		return vm.MakeInt(16), true
	case "LOG_PERROR":
		return vm.MakeInt(32), true
	case "LOG_PID":
		return vm.MakeInt(1), true
	case "STREAM_URL_STAT_LINK":
		return vm.MakeInt(1), true
	case "STREAM_URL_STAT_QUIET":
		return vm.MakeInt(2), true
	case "PHP_MAXPATHLEN":
		return vm.MakeInt(4096), true
	case "PHP_SAPI":
		return vm.MakeString("cli"), true
	case "PHP_OS_FAMILY":
		return vm.MakeString(phpOSFamily()), true
	case "PHP_BINARY":
		return vm.MakeString(phpBinaryPath()), true
	case "DEBUG_BACKTRACE_IGNORE_ARGS":
		return vm.MakeInt(2), true
	case "DEBUG_BACKTRACE_PROVIDE_OBJECT":
		return vm.MakeInt(1), true
	case "SORT_ASC":
		return vm.MakeInt(4), true
	case "SORT_LOCALE_STRING":
		return vm.MakeInt(5), true
	case "ARRAY_FILTER_USE_BOTH":
		return vm.MakeInt(1), true
	case "ARRAY_FILTER_USE_KEY":
		return vm.MakeInt(2), true
	case "JSON_PRETTY_PRINT":
		return vm.MakeInt(128), true
	case "JSON_UNESCAPED_SLASHES":
		return vm.MakeInt(64), true
	case "JSON_UNESCAPED_UNICODE":
		return vm.MakeInt(256), true
	case "PHP_FLOAT_MAX":
		return vm.MakeFloat(math.MaxFloat64), true
	case "PHP_FLOAT_MIN":
		return vm.MakeFloat(math.SmallestNonzeroFloat64), true
	case "PHP_FLOAT_DIG":
		return vm.MakeInt(15), true
	case "M_LOG2E":
		return vm.MakeFloat(1.4426950408889634074), true
	case "M_PI":
		return vm.MakeFloat(3.14159265358979323846), true
	case "M_E":
		return vm.MakeFloat(2.7182818284590452354), true
	case "M_LOG10E":
		return vm.MakeFloat(0.43429448190325182765), true
	case "M_LN2":
		return vm.MakeFloat(0.69314718055994530942), true
	case "M_LN10":
		return vm.MakeFloat(2.30258509299404568402), true
	case "M_PI_2":
		return vm.MakeFloat(1.57079632679489661923), true
	case "M_PI_4":
		return vm.MakeFloat(0.78539816339744830962), true
	case "M_1_PI":
		return vm.MakeFloat(0.31830988618379067154), true
	case "M_2_PI":
		return vm.MakeFloat(0.63661977236758134308), true
	case "M_SQRTPI":
		return vm.MakeFloat(1.77245385090551602729), true
	case "M_2_SQRTPI":
		return vm.MakeFloat(1.12837916709551257390), true
	case "M_SQRT2":
		return vm.MakeFloat(1.41421356237309504880), true
	case "M_SQRT3":
		return vm.MakeFloat(1.73205080756887729352), true
	case "M_SQRT1_2":
		return vm.MakeFloat(0.70710678118654752440), true
	case "M_EULER":
		return vm.MakeFloat(0.57721566490153286061), true
	case "M_LNPI":
		return vm.MakeFloat(1.14472988584940017414), true
	case "PATHINFO_DIRNAME":
		return vm.MakeInt(1), true
	case "PATHINFO_BASENAME":
		return vm.MakeInt(2), true
	case "PATHINFO_EXTENSION":
		return vm.MakeInt(4), true
	case "PATHINFO_FILENAME":
		return vm.MakeInt(8), true
	case "PATHINFO_ALL":
		return vm.MakeInt(15), true
	case "SEEK_SET":
		return vm.MakeInt(0), true
	case "SEEK_CUR":
		return vm.MakeInt(1), true
	case "SEEK_END":
		return vm.MakeInt(2), true
	case "PSFS_PASS_ON":
		return vm.MakeInt(1), true
	case "PSFS_FEED_ME":
		return vm.MakeInt(0), true
	case "PSFS_ERR_FATAL":
		return vm.MakeInt(2), true
	case "STREAM_FILTER_READ":
		return vm.MakeInt(1), true
	case "STREAM_FILTER_WRITE":
		return vm.MakeInt(2), true
	case "STREAM_FILTER_ALL":
		return vm.MakeInt(3), true
	case "STREAM_USE_PATH":
		return vm.MakeInt(1), true
	case "STREAM_REPORT_ERRORS":
		return vm.MakeInt(8), true
	case "STREAM_OPTION_BLOCKING":
		return vm.MakeInt(1), true
	case "STREAM_OPTION_READ_TIMEOUT":
		return vm.MakeInt(4), true
	case "STREAM_OPTION_WRITE_BUFFER":
		return vm.MakeInt(3), true
	case "STREAM_BUFFER_NONE":
		return vm.MakeInt(0), true
	case "STREAM_BUFFER_LINE":
		return vm.MakeInt(1), true
	case "STREAM_BUFFER_FULL":
		return vm.MakeInt(2), true
	case "STREAM_IS_URL":
		return vm.MakeInt(1), true
	case "STREAM_MKDIR_RECURSIVE":
		return vm.MakeInt(1), true
	case "STREAM_CLIENT_CONNECT":
		return vm.MakeInt(4), true
	case "STREAM_CLIENT_ASYNC_CONNECT":
		return vm.MakeInt(1), true
	case "STREAM_CLIENT_PERSISTENT":
		return vm.MakeInt(2), true
	case "STREAM_SERVER_BIND":
		return vm.MakeInt(4), true
	case "STREAM_SERVER_LISTEN":
		return vm.MakeInt(8), true
	case "STREAM_PF_INET":
		return vm.MakeInt(2), true
	case "STREAM_PF_INET6":
		return vm.MakeInt(10), true
	case "STREAM_PF_UNIX":
		return vm.MakeInt(1), true
	case "STREAM_SOCK_STREAM":
		return vm.MakeInt(1), true
	case "STREAM_SOCK_DGRAM":
		return vm.MakeInt(2), true
	case "STREAM_IPPROTO_IP":
		return vm.MakeInt(0), true
	case "STREAM_IPPROTO_TCP":
		return vm.MakeInt(6), true
	case "STREAM_OOB":
		return vm.MakeInt(1), true
	case "STREAM_PEEK":
		return vm.MakeInt(2), true
	case "STREAM_SHUT_RD":
		return vm.MakeInt(0), true
	case "STREAM_SHUT_WR":
		return vm.MakeInt(1), true
	case "STREAM_SHUT_RDWR":
		return vm.MakeInt(2), true
	case "STREAM_NOTIFY_RESOLVE":
		return vm.MakeInt(1), true
	case "STREAM_NOTIFY_CONNECT":
		return vm.MakeInt(2), true
	case "STREAM_NOTIFY_AUTH_REQUIRED":
		return vm.MakeInt(3), true
	case "STREAM_NOTIFY_MIME_TYPE_IS":
		return vm.MakeInt(4), true
	case "STREAM_NOTIFY_FILE_SIZE_IS":
		return vm.MakeInt(5), true
	case "STREAM_NOTIFY_REDIRECTED":
		return vm.MakeInt(6), true
	case "STREAM_NOTIFY_PROGRESS":
		return vm.MakeInt(7), true
	case "STREAM_NOTIFY_COMPLETED":
		return vm.MakeInt(8), true
	case "STREAM_NOTIFY_FAILURE":
		return vm.MakeInt(9), true
	case "STREAM_NOTIFY_AUTH_RESULT":
		return vm.MakeInt(10), true
	case "STREAM_NOTIFY_SEVERITY_INFO":
		return vm.MakeInt(0), true
	case "STREAM_NOTIFY_SEVERITY_WARN":
		return vm.MakeInt(1), true
	case "STREAM_NOTIFY_SEVERITY_ERR":
		return vm.MakeInt(2), true
	case "LOCK_SH":
		return vm.MakeInt(1), true
	case "LOCK_EX":
		return vm.MakeInt(2), true
	case "LOCK_UN":
		return vm.MakeInt(3), true
	case "LOCK_NB":
		return vm.MakeInt(4), true
	case "FILE_USE_INCLUDE_PATH":
		return vm.MakeInt(1), true
	case "FILE_IGNORE_NEW_LINES":
		return vm.MakeInt(2), true
	case "FILE_SKIP_EMPTY_LINES":
		return vm.MakeInt(4), true
	case "FILE_APPEND":
		return vm.MakeInt(8), true
	case "FILE_NO_DEFAULT_CONTEXT":
		return vm.MakeInt(16), true
	case "FILE_TEXT":
		return vm.MakeInt(0), true
	case "FILE_BINARY":
		return vm.MakeInt(0), true
	case "GLOB_ERR":
		return vm.MakeInt(1), true
	case "GLOB_MARK":
		return vm.MakeInt(2), true
	case "GLOB_NOSORT":
		return vm.MakeInt(4), true
	case "GLOB_NOCHECK":
		return vm.MakeInt(16), true
	case "GLOB_NOESCAPE":
		return vm.MakeInt(64), true
	case "GLOB_BRACE":
		return vm.MakeInt(128), true
	case "GLOB_ONLYDIR":
		return vm.MakeInt(8192), true
	case "SCANDIR_SORT_ASCENDING":
		return vm.MakeInt(0), true
	case "SCANDIR_SORT_DESCENDING":
		return vm.MakeInt(1), true
	case "SCANDIR_SORT_NONE":
		return vm.MakeInt(2), true
	case "FNM_NOESCAPE":
		return vm.MakeInt(2), true
	case "FNM_PATHNAME":
		return vm.MakeInt(1), true
	case "FNM_PERIOD":
		return vm.MakeInt(4), true
	case "FNM_CASEFOLD":
		return vm.MakeInt(16), true

	// --- PREG_* (pregAgent) ---
	case "PREG_PATTERN_ORDER":
		return vm.MakeInt(1), true
	case "PREG_SET_ORDER":
		return vm.MakeInt(2), true
	case "PREG_OFFSET_CAPTURE":
		return vm.MakeInt(256), true
	case "PREG_UNMATCHED_AS_NULL":
		return vm.MakeInt(512), true
	case "PREG_SPLIT_NO_EMPTY":
		return vm.MakeInt(1), true
	case "PREG_SPLIT_DELIM_CAPTURE":
		return vm.MakeInt(2), true
	case "PREG_SPLIT_OFFSET_CAPTURE":
		return vm.MakeInt(4), true
	case "PREG_GREP_INVERT":
		return vm.MakeInt(1), true
	case "PREG_NO_ERROR":
		return vm.MakeInt(0), true
	case "PREG_INTERNAL_ERROR":
		return vm.MakeInt(1), true
	case "PREG_BACKTRACK_LIMIT_ERROR":
		return vm.MakeInt(2), true
	case "PREG_RECURSION_LIMIT_ERROR":
		return vm.MakeInt(3), true
	case "PREG_BAD_UTF8_ERROR":
		return vm.MakeInt(4), true
	case "PREG_BAD_UTF8_OFFSET_ERROR":
		return vm.MakeInt(5), true
	case "PREG_JIT_STACKLIMIT_ERROR":
		return vm.MakeInt(6), true

	// --- DATE_* (dateAgent) ---
	case "DATE_ATOM":
		return vm.MakeString("Y-m-d\\TH:i:sP"), true
	case "DATE_COOKIE":
		return vm.MakeString("l, d-M-Y H:i:s T"), true
	case "DATE_ISO8601":
		return vm.MakeString("Y-m-d\\TH:i:sO"), true
	case "DATE_RFC822":
		return vm.MakeString("D, d M y H:i:s O"), true
	case "DATE_RFC850":
		return vm.MakeString("l, d-M-y H:i:s T"), true
	case "DATE_RFC1036":
		return vm.MakeString("D, d M y H:i:s O"), true
	case "DATE_RFC1123":
		return vm.MakeString("D, d M Y H:i:s O"), true
	case "DATE_RFC7231":
		return vm.MakeString("D, d M Y H:i:s \\G\\M\\T"), true
	case "DATE_RFC2822":
		return vm.MakeString("D, d M Y H:i:s O"), true
	case "DATE_RFC3339":
		return vm.MakeString("Y-m-d\\TH:i:sP"), true
	case "DATE_RFC3339_EXTENDED":
		return vm.MakeString("Y-m-d\\TH:i:s.vP"), true
	case "DATE_RSS":
		return vm.MakeString("D, d M Y H:i:s O"), true
	case "DATE_W3C":
		return vm.MakeString("Y-m-d\\TH:i:sP"), true

	// --- JSON_* (dateAgent) ---
	case "JSON_HEX_TAG":
		return vm.MakeInt(1), true
	case "JSON_HEX_AMP":
		return vm.MakeInt(2), true
	case "JSON_HEX_APOS":
		return vm.MakeInt(4), true
	case "JSON_HEX_QUOT":
		return vm.MakeInt(8), true
	case "JSON_FORCE_OBJECT":
		return vm.MakeInt(16), true
	case "JSON_NUMERIC_CHECK":
		return vm.MakeInt(32), true
	case "JSON_UNESCAPED_LINE_TERMINATORS":
		return vm.MakeInt(2048), true
	case "JSON_INVALID_UTF8_IGNORE":
		return vm.MakeInt(1048576), true
	case "JSON_INVALID_UTF8_SUBSTITUTE":
		return vm.MakeInt(2097152), true
	case "JSON_THROW_ON_ERROR":
		return vm.MakeInt(4194304), true
	case "JSON_OBJECT_AS_ARRAY":
		return vm.MakeInt(1), true
	case "JSON_BIGINT_AS_STRING":
		return vm.MakeInt(2), true
	case "JSON_PARTIAL_OUTPUT_ON_ERROR":
		return vm.MakeInt(512), true
	case "JSON_PRESERVE_ZERO_FRACTION":
		return vm.MakeInt(1024), true
	case "JSON_ERROR_NONE":
		return vm.MakeInt(0), true
	case "JSON_ERROR_DEPTH":
		return vm.MakeInt(1), true
	case "JSON_ERROR_STATE_MISMATCH":
		return vm.MakeInt(2), true
	case "JSON_ERROR_CTRL_CHAR":
		return vm.MakeInt(3), true
	case "JSON_ERROR_SYNTAX":
		return vm.MakeInt(4), true
	case "JSON_ERROR_UTF8":
		return vm.MakeInt(5), true
	case "JSON_ERROR_RECURSION":
		return vm.MakeInt(6), true
	case "JSON_ERROR_INF_OR_NAN":
		return vm.MakeInt(7), true
	case "JSON_ERROR_UNSUPPORTED_TYPE":
		return vm.MakeInt(8), true
	case "JSON_ERROR_INVALID_PROPERTY_NAME":
		return vm.MakeInt(9), true
	case "JSON_ERROR_UTF16":
		return vm.MakeInt(10), true
	case "JSON_ERROR_NON_BACKED_ENUM":
		return vm.MakeInt(11), true

	// --- PHP_URL_* (dateAgent) ---
	case "PHP_URL_SCHEME":
		return vm.MakeInt(0), true
	case "PHP_URL_HOST":
		return vm.MakeInt(1), true
	case "PHP_URL_PORT":
		return vm.MakeInt(2), true
	case "PHP_URL_USER":
		return vm.MakeInt(3), true
	case "PHP_URL_PASS":
		return vm.MakeInt(4), true
	case "PHP_URL_PATH":
		return vm.MakeInt(5), true
	case "PHP_URL_QUERY":
		return vm.MakeInt(6), true
	case "PHP_URL_FRAGMENT":
		return vm.MakeInt(7), true

	// --- CRYPT_* (dateAgent) ---
	case "CRYPT_SALT_LENGTH":
		return vm.MakeInt(123), true
	case "CRYPT_STD_DES":
		return vm.MakeInt(1), true
	case "CRYPT_EXT_DES":
		return vm.MakeInt(1), true
	case "CRYPT_MD5":
		return vm.MakeInt(1), true
	case "CRYPT_BLOWFISH":
		return vm.MakeInt(1), true
	case "CRYPT_SHA256":
		return vm.MakeInt(1), true
	case "CRYPT_SHA512":
		return vm.MakeInt(1), true

	// --- HTTP_QUERY_* ---
	case "PHP_QUERY_RFC1738":
		return vm.MakeInt(1), true
	case "PHP_QUERY_RFC3986":
		return vm.MakeInt(2), true
	}
	return vm.Slot{}, false
}

func gettypeFn(ctx *vm.Context) vm.Value {
	switch arg0(ctx).Type() {
	case vm.NullType:
		return vm.Str("NULL")
	case vm.BoolType:
		return vm.Str("boolean")
	case vm.IntType:
		return vm.Str("integer")
	case vm.FloatType:
		return vm.Str("double")
	case vm.StringType:
		return vm.Str("string")
	case vm.ArrayType:
		return vm.Str("array")
	case vm.ObjectType:
		return vm.Str("object")
	case vm.ResourceType:
		if vm.ResourceClosed(arg0(ctx)) {
			return vm.Str("resource (closed)")
		}
		return vm.Str("resource")
	}
	return vm.Str("unknown type")
}

// getDebugTypeFn реализует get_debug_type(): как gettype, но с
// пространствами имён классов и уточнением ресурсов (resource (stream)).
func getDebugTypeFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx)
	switch s.Type() {
	case vm.NullType:
		return vm.Str("null")
	case vm.BoolType:
		return vm.Str("bool")
	case vm.IntType:
		return vm.Str("int")
	case vm.FloatType:
		return vm.Str("float")
	case vm.StringType:
		return vm.Str("string")
	case vm.ArrayType:
		return vm.Str("array")
	case vm.ObjectType:
		if o := ctx.Arg[*vm.Object](0); o != nil {
			if n := o.ClassName(); n != "" {
				if strings.HasPrefix(n, "\x00anon#") {
					ci := o.Class()
					if p := ci.Parent(); p != nil {
						return vm.Str(p.Name() + "@anonymous")
					}
					if ifaces := ci.Interfaces(); len(ifaces) > 0 {
						return vm.Str(ifaces[0].Name() + "@anonymous")
					}
					return vm.Str("class@anonymous")
				}
				return vm.Str(vm.ClassDisplayName(n))
			}
		}
		return vm.Str("object")
	case vm.ClosureType:
		return vm.Str("Closure")
	case vm.ResourceType:
		if r := vm.AsResource(s); r != nil && !r.Closed {
			return vm.Str("resource (" + r.Kind + ")")
		}
		return vm.Str("resource (closed)")
	}
	return vm.Str("unknown type")
}

func countFn(ctx *vm.Context) vm.Value {
	return vm.CountBuiltin(ctx)
}

// --- var_dump / var_export / print_r ---

// prettyEnter/prettyLeave ограничивают рекурсию при печати структур, чтобы
// самоссылающиеся объекты/массивы не приводили к переполнению/OOM.
var prettyDepth int

func prettyEnter() bool {
	prettyDepth++
	return prettyDepth <= 200
}

func prettyLeave() { prettyDepth-- }

func varDumpFn(ctx *vm.Context) vm.Value {
	if v := ctx.VM(); v != nil {
		// Проход сборщика при включении хендлов может вызвать деструктор,
		// бросивший исключение (fe_fetch_dtor_exception): оно поднимается
		// до печати значения.
		if ferr := v.ArmHandles(); ferr != nil {
			throwCallable(ctx, ferr)
			return *new(vm.Value)
		}
	}
	var b strings.Builder
	args := ctx.Args()
	st := &dumpState{vm: ctx.VM(), objIDs: map[*vm.Object]int{}, arrs: map[*vm.Array]bool{}, objs: map[*vm.Object]bool{}, refCount: map[*vm.Ref]int{}, arrCount: map[*vm.Array]int{}, roots: args}
	seenA := map[*vm.Array]bool{}
	seenO := map[*vm.Object]bool{}
	for _, s := range args {
		countRefs(ctx.VM(), s, st.refCount, st.arrCount, seenA, seenO)
	}
	for _, s := range args {
		if st.err != nil {
			break
		}
		dumpSlot(&b, s, 0, st)
	}
	ctx.Print(b.String())
	if st.err != nil {
		if th, ok := st.err.(*vm.Throwable); ok {
			ctx.Throw(th)
		} else {
			ctx.Throw(vm.NewThrowable("Error", st.err.Error()))
		}
		return *new(vm.Value)
	}
	return vm.MakeNull()
}

// countRefs подсчитывает, сколько раз каждая Ref-ячейка встречается в
// структуре (для печати `&` только при refcount>1, как в Zend), а также
// вхождения массивов (для refcount в debug_zval_dump).
func countRefs(v *vm.VM, s vm.Slot, counts map[*vm.Ref]int, arrCounts map[*vm.Array]int, seenA map[*vm.Array]bool, seenO map[*vm.Object]bool) {
	if s.IsRef() {
		if r := s.Ref(); r != nil {
			counts[r]++
		}
		s = vm.Deref(s)
	}
	switch s.Type() {
	case vm.ArrayType:
		a := s.AsArray()
		if a == nil {
			return
		}
		if arrCounts != nil {
			arrCounts[a]++
		}
		if seenA[a] {
			return
		}
		seenA[a] = true
		for _, x := range a.All() {
			countRefs(v, x, counts, arrCounts, seenA, seenO)
		}
	case vm.ObjectType:
		o := v.SlotObject(s)
		if o == nil || seenO[o] {
			return
		}
		seenO[o] = true
		for _, de := range v.ObjectDumpEntries(o) {
			if de.Uninit {
				continue
			}
			countRefs(v, de.Val, counts, arrCounts, seenA, seenO)
		}
	}
}

// dumpState назначает объектам номера (#N) и отслеживает рекурсию.
type dumpState struct {
	vm       *vm.VM
	objIDs   map[*vm.Object]int
	nextID   int
	arrs     map[*vm.Array]bool
	objs     map[*vm.Object]bool
	closures map[*vm.Closure]bool
	refCount map[*vm.Ref]int
	// arrCount — вхождения массивов (refcount в debug_zval_dump: обход
	// аргумента плюс внешние держатели-переменные).
	arrCount map[*vm.Array]int
	roots    []vm.Slot
	// err — фатальная ошибка, возникшая при дампе (__debugInfo не массив):
	// печать до неё уже выведена, ошибку поднимает varDumpFn.
	err error
}

func (st *dumpState) objID(o *vm.Object) int {
	if id := o.ID(); id > 0 {
		return id
	}
	if id, ok := st.objIDs[o]; ok {
		return id
	}
	st.nextID++
	st.objIDs[o] = st.nextID
	return st.nextID
}

// dumpElem печатает значение-элемент (элемент массива или свойство объекта).
// В отличие от top-level аргументов var_dump, ссылки здесь помечаются `&`.
func dumpElem(b *strings.Builder, s vm.Slot, indent int, st *dumpState) {
	if s.IsRef() {
		inner := vm.Deref(s)
		// Ссылка на уже выводимую структуру печатается как *RECURSION* без
		// маркера `&` (как в PHP).
		switch inner.Type() {
		case vm.ArrayType:
			if st.arrs[inner.AsArray()] {
				b.WriteString("*RECURSION*\n")
				return
			}
		case vm.ObjectType:
			if o := st.vm.SlotObject(inner); o != nil && st.objs[o] {
				b.WriteString("*RECURSION*\n")
				return
			}
		case vm.ClosureType:
			if cl := inner.AsClosure(); cl != nil && st.closures[cl] {
				b.WriteString("*RECURSION*\n")
				return
			}
		}
		if st.vm.CountRefHoldersDisplay(st.roots, s.Ref()) > 1 {
			b.WriteString("&")
		}
		dumpSlot(b, inner, indent, st)
		return
	}
	dumpSlot(b, s, indent, st)
}

func dumpSlot(b *strings.Builder, s vm.Slot, indent int, st *dumpState) {
	switch s.Type() {
	case vm.NullType:
		b.WriteString("NULL\n")
	case vm.BoolType:
		if s.ToBool() {
			b.WriteString("bool(true)\n")
		} else {
			b.WriteString("bool(false)\n")
		}
	case vm.IntType:
		b.WriteString("int(" + strconv.FormatInt(s.ToInt(), 10) + ")\n")
	case vm.FloatType:
		b.WriteString("float(" + phpFloat(s.ToFloat()) + ")\n")
	case vm.StringType:
		v := s.StringVal()
		b.WriteString("string(" + strconv.Itoa(len(v)) + ") \"" + v + "\"\n")
	case vm.ArrayType:
		a := s.AsArray()
		pad := strings.Repeat("  ", indent)
		if st.arrs[a] {
			b.WriteString("*RECURSION*\n")
			return
		}
		st.arrs[a] = true
		defer delete(st.arrs, a)
		b.WriteString("array(" + strconv.Itoa(a.Len()) + ") {\n")
		for k, v := range a.All() {
			b.WriteString(pad + "  ")
			if k.IsStr() {
				b.WriteString(`["` + k.StrVal() + "\"]=>\n")
			} else {
				b.WriteString("[" + strconv.FormatInt(k.IntVal(), 10) + "]=>\n")
			}
			b.WriteString(pad + "  ")
			dumpElem(b, v, indent+1, st)
		}
		b.WriteString(pad + "}\n")
	case vm.ClosureType:
		cl := s.AsClosure()
		if cl != nil {
			// Рекурсивное замыкание (use (&$x) на само себя): на пути дампа
			// повторный вход печатает *RECURSION*, иначе бесконечная рекурсия.
			if st.closures[cl] {
				b.WriteString("*RECURSION*\n")
				return
			}
			if st.closures == nil {
				st.closures = map[*vm.Closure]bool{}
			}
			st.closures[cl] = true
			defer delete(st.closures, cl)
		}
		id := s.ClosureID()
		if id == 0 {
			st.nextID++
			id = st.nextID
		}
		var funcName string
		var paramNames []string
		var capNames []string
		var capVals []vm.Slot
		var staticNames []string
		var staticKeys []string
		var staticDefaults []vm.Slot
		hasThis := false
		if cl != nil {
			if cl.Fake && cl.Fn != nil && cl.Fn.Name != "" && cl.Fn.Name != "{closure}" {
				funcName = cl.Fn.Name
				if cl.Fn.Scope != nil {
					funcName = cl.Fn.Scope.Name() + "::" + funcName
				}
				paramNames = cl.Fn.ParamNames
			}
			if cl.Fn != nil && len(cl.Fn.CaptureNames) == len(cl.Captures) {
				capNames = cl.Fn.CaptureNames
			} else {
				capNames = make([]string, len(cl.Captures))
			}
			capVals = cl.Captures
			if cl.Fn != nil && len(cl.Fn.StaticNames) > 0 {
				staticNames = cl.Fn.StaticNames
				staticKeys = cl.Fn.StaticKeys
				staticDefaults = cl.Fn.StaticDefaults
			}
			hasThis = derefSlot(cl.BoundThis).Type() == vm.ObjectType
		}
		n := 0
		if funcName != "" {
			n++
		}
		if len(paramNames) > 0 {
			n++
		}
		if len(capVals) > 0 || len(staticNames) > 0 {
			n++
		}
		if hasThis {
			n++
		}
		pad := strings.Repeat("  ", indent)
		b.WriteString("object(Closure)#" + strconv.Itoa(id) + " (" + strconv.Itoa(n) + ") {\n")
		inner := pad + "  "
		if funcName != "" {
			b.WriteString(inner + `["function"]=>` + "\n")
			b.WriteString(inner)
			dumpElem(b, vm.MakeString(funcName), indent+1, st)
		}
		if len(paramNames) > 0 {
			b.WriteString(inner + `["parameter"]=>` + "\n")
			parr := vm.NewArray()
			for _, pn := range paramNames {
				name := pn
				if !strings.HasPrefix(name, "$") {
					name = "$" + name
				}
				_ = parr.SetStr(name, vm.MakeString("<required>"))
			}
			b.WriteString(inner)
			dumpElem(b, vm.MakeArray(parr), indent+1, st)
		}
		if len(capVals) > 0 || len(staticNames) > 0 {
			b.WriteString(inner + `["static"]=>` + "\n")
			sarr := vm.NewArray()
			for i, v := range capVals {
				name := ""
				if i < len(capNames) {
					name = capNames[i]
				}
				_ = sarr.SetStr(name, v)
			}
			// Статические переменные функции: текущее значение ячейки в
			// globals, иначе значение по умолчанию (Zend static_variables).
			for i, name := range staticNames {
				val := vm.MakeNull()
				got := false
				if i < len(staticKeys) && st.vm != nil {
					if v, ok := st.vm.GlobalRef(staticKeys[i]); ok {
						val, got = v, true
					}
				}
				if !got && i < len(staticDefaults) {
					val = staticDefaults[i]
				}
				_ = sarr.SetStr(name, val)
			}
			b.WriteString(inner)
			dumpElem(b, vm.MakeArray(sarr), indent+1, st)
		}
		if hasThis {
			b.WriteString(inner + `["this"]=>` + "\n")
			b.WriteString(inner)
			dumpElem(b, cl.BoundThis, indent+1, st)
		}
		b.WriteString(pad + "}\n")
	case vm.ResourceType:
		r := vm.AsResource(s)
		if r == nil {
			b.WriteString("NULL\n")
			return
		}
		if r.Closed {
			b.WriteString("resource(" + strconv.FormatInt(r.ID, 10) + ") of type (Unknown)\n")
			return
		}
		b.WriteString("resource(" + strconv.FormatInt(r.ID, 10) + ") of type (" + r.Kind + ")\n")
	case vm.ObjectType:
		if d, ok := st.vm.EnumDump(s); ok {
			b.WriteString(d + "\n")
			return
		}
		o := st.vm.SlotObject(s)
		if o == nil {
			b.WriteString("NULL\n")
			return
		}
		pad := strings.Repeat("  ", indent)
		if st.objs[o] {
			b.WriteString("*RECURSION*\n")
			return
		}
		st.objs[o] = true
		defer delete(st.objs, o)
		// __debugInfo(): пользовательский набор свойств (Zend вызывает метод
		// при любом дампе объекта; не-массив — фатал).
		if des, magic, err := st.vm.DebugInfoProps(o); err != nil {
			if st.err == nil {
				st.err = err
			}
			return
		} else if magic {
			b.WriteString("object(" + st.vm.DisplayClassNameOf(o.Class()) + ")#" + strconv.Itoa(st.objID(o)) + " (" + strconv.Itoa(len(des)) + ") {\n")
			for _, de := range des {
				if de.KeyInt {
					b.WriteString(pad + "  [" + de.Key + "]=>\n")
				} else {
					b.WriteString(pad + "  [\"" + de.Key + "\"" + de.Suffix + "]=>\n")
				}
				b.WriteString(pad + "  ")
				dumpElem(b, de.Val, indent+1, st)
			}
			b.WriteString(pad + "}\n")
			return
		}
		if ents, ok := st.vm.WeakMapDumpEntries(o); ok {
			// WeakMap печатает пары key/value двухэлементными массивами.
			// Пара — временный контейнер: добавляем её в корни подсчёта
			// ссылок, чтобы значение-ссылка считалась как в Zend (запись карты
			// + копия в паре => refcount 2).
			b.WriteString("object(WeakMap)#" + strconv.Itoa(st.objID(o)) + " (" + strconv.Itoa(len(ents)) + ") {\n")
			for _, de := range ents {
				b.WriteString(pad + "  [" + de.Key + "]=>\n")
				b.WriteString(pad + "  ")
				n := len(st.roots)
				st.roots = append(st.roots, de.Val)
				dumpElem(b, de.Val, indent+1, st)
				st.roots = st.roots[:n]
			}
			b.WriteString(pad + "}\n")
			return
		}
		ents := objectJSONEntries(st.vm, o)
		if st.vm.IsDumpPlainObject(o) {
			des := st.vm.ObjectDumpEntries(o)
			cnt := 0
			for _, de := range des {
				if !de.Uninit {
					cnt++
				}
			}
			b.WriteString("object(" + st.vm.DisplayClassNameOf(o.Class()) + ")#" + strconv.Itoa(st.objID(o)) + " (" + strconv.Itoa(cnt) + ") {\n")
			for _, de := range des {
				b.WriteString(pad + "  [\"" + de.Key + "\"" + de.Suffix + "]=>\n")
				b.WriteString(pad + "  ")
				if de.Uninit {
					b.WriteString("uninitialized(" + vm.TypeString(de.Type) + ")\n")
					continue
				}
				dumpElem(b, de.Val, indent+1, st)
			}
			b.WriteString(pad + "}\n")
			return
		}
		b.WriteString("object(" + st.vm.DisplayClassNameOf(o.Class()) + ")#" + strconv.Itoa(st.objID(o)) + " (" + strconv.Itoa(len(ents)) + ") {\n")
		for _, en := range ents {
			b.WriteString(pad + "  [\"" + en.key + "\"]=>\n")
			b.WriteString(pad + "  ")
			dumpElem(b, en.val, indent+1, st)
		}
		b.WriteString(pad + "}\n")
	}
}

func phpFloat(f float64) string {
	return phpFormatFloat(f, false)
}

// phpExportFloat — формат var_export: у целых float сохраняется ".0".
func phpExportFloat(f float64) string {
	return phpFormatFloat(f, true)
}

// phpFormatFloat повторяет var_dump/var_export-форматирование float в PHP
// (serialize_precision=-1, кратчайшее представление с round-trip):
// фиксированная запись при -4 <= exp10 <= 16, иначе научная (E+/-),
// у мантиссы научной записи всегда есть точка, zeroFraction добавляет ".0"
// к целым в фиксированной записи (для var_export).
func phpFormatFloat(f float64, zeroFraction bool) string {
	if math.IsInf(f, 0) {
		if f < 0 {
			return "-INF"
		}
		return "INF"
	}
	if math.IsNaN(f) {
		return "NAN"
	}
	if f == 0 {
		neg := math.Signbit(f)
		base := "0"
		if zeroFraction {
			base = "0.0"
		}
		if neg {
			return "-" + base
		}
		return base
	}
	neg := math.Signbit(f)
	ndigit := serializePrecisionDigits()
	if ndigit < 0 {
		// Кратчайшее round-trip представление; порог экспоненты — 17.
		s := strconv.FormatFloat(math.Abs(f), 'e', -1, 64)
		ei := strings.IndexByte(s, 'e')
		exp10, _ := strconv.Atoi(s[ei+1:])
		digits := strings.Replace(s[:ei], ".", "", 1)
		return gcvtFormat(digits, exp10+1, neg, 17, zeroFraction)
	}
	// Явная точность (serialize_precision): ndigits значащих цифр, хвостовые
	// нули отбрасываются (dtoa mode 2), условие экспоненты — по исходному ndigit.
	gen := ndigit
	if gen == 0 {
		gen = 1
	}
	// var_export (zeroFraction) моделирует snprintf: precise 0 трактуется как
	// 1 (smart_str_append_double: zend_gcvt(num, precision ? precision : 1)),
	// включая решение о научной записи; serialize зовёт gcvt с сырым 0.
	decision := ndigit
	if zeroFraction && decision == 0 {
		decision = 1
	}
	s := strconv.FormatFloat(math.Abs(f), 'e', gen-1, 64)
	ei := strings.IndexByte(s, 'e')
	exp10, _ := strconv.Atoi(s[ei+1:])
	digits := strings.Replace(s[:ei], ".", "", 1)
	digits = strings.TrimRight(digits, "0")
	if digits == "" {
		digits = "0"
	}
	return gcvtFormat(digits, exp10+1, neg, decision, zeroFraction)
}

// serializePrecisionDigits возвращает значение serialize_precision (-1 по
// умолчанию — кратчайшее представление).
func serializePrecisionDigits() int {
	v, ok := iniValues["serialize_precision"]
	if !ok {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return -1
	}
	return n
}

// gcvtFormat повторяет хвостовую часть zend_gcvt: digits — значащие цифры без
// точки, decpt — позиция десятичной точки (value = 0.digits * 10^decpt).
func gcvtFormat(digits string, decpt int, neg bool, ndigit int, zeroFraction bool) string {
	if digits == "" {
		digits = "0"
	}
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	if (decpt >= 0 && decpt > ndigit) || decpt < -3 {
		e := decpt - 1
		esign := byte('+')
		if e < 0 {
			esign = '-'
			e = -e
		}
		b.WriteByte(digits[0])
		b.WriteByte('.')
		if len(digits) == 1 {
			b.WriteByte('0')
		} else {
			b.WriteString(digits[1:])
		}
		b.WriteByte('E')
		b.WriteByte(esign)
		b.WriteString(strconv.Itoa(e))
		return b.String()
	}
	if decpt < 0 {
		b.WriteString("0.")
		for i := 0; i < -decpt; i++ {
			b.WriteByte('0')
		}
		b.WriteString(digits)
		return b.String()
	}
	hadDigits := false
	for i := 0; i < decpt; i++ {
		if i < len(digits) {
			b.WriteByte(digits[i])
			hadDigits = true
		} else {
			b.WriteByte('0')
		}
	}
	if decpt == 0 {
		b.WriteByte('0')
	}
	if len(digits) > decpt {
		b.WriteByte('.')
		b.WriteString(digits[decpt:])
		hadDigits = true
	} else if zeroFraction {
		b.WriteString(".0")
		hadDigits = true
	}
	if !hadDigits {
		b.WriteByte('0')
	}
	return b.String()
}

func varExportFn(ctx *vm.Context) vm.Value {
	var b strings.Builder
	st := &exportState{arrays: map[*vm.Array]bool{}, objs: map[*vm.Object]bool{}}
	exportSlot(ctx.VM(), &b, arg0(ctx), 0, st)
	if len(ctx.Args()) >= 2 && arg(ctx, 1).ToBool() {
		return vm.Str(b.String())
	}
	ctx.Print(b.String())
	return vm.MakeNull()
}

// exportState отслеживает массивы/объекты на текущем пути рекурсии для
// обнаружения циклических ссылок (var_export их не поддерживает).
type exportState struct {
	arrays map[*vm.Array]bool
	objs   map[*vm.Object]bool
}

func exportSlot(m *vm.VM, b *strings.Builder, s vm.Slot, indent int, st *exportState) {
	if cls, name, _, ok := m.EnumCaseInfo(s); ok {
		b.WriteString("\\" + cls + "::" + name)
		return
	}
	if s.Type() == vm.ArrayType {
		a := s.AsArray()
		if st.arrays[a] {
			m.Warn("var_export does not handle circular references")
			b.WriteString("NULL")
			return
		}
		st.arrays[a] = true
		defer delete(st.arrays, a)
	} else if s.Type() == vm.ObjectType {
		if o := m.SlotObject(s); o != nil {
			if st.objs[o] {
				m.Warn("var_export does not handle circular references")
				b.WriteString("NULL")
				return
			}
			st.objs[o] = true
			defer delete(st.objs, o)
		}
	}
	if !prettyEnter() {
		b.WriteString("NULL")
		return
	}
	defer prettyLeave()
	switch s.Type() {
	case vm.NullType:
		b.WriteString("NULL")
	case vm.BoolType:
		if s.ToBool() {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case vm.IntType:
		b.WriteString(strconv.FormatInt(s.ToInt(), 10))
	case vm.FloatType:
		b.WriteString(phpExportFloat(s.ToFloat()))
	case vm.StringType:
		exportStringSlot(b, s.StringVal())
	case vm.ArrayType:
		a := s.AsArray()
		b.WriteString("array (\n")
		for k, v := range a.All() {
			b.WriteString(spaces(indent + 2))
			if k.IsStr() {
				exportStringSlot(b, k.StrVal())
			} else {
				b.WriteString(strconv.FormatInt(k.IntVal(), 10))
			}
			b.WriteString(" => ")
			exportValue(m, b, v, indent+2, st)
			b.WriteString(",\n")
		}
		b.WriteString(spaces(indent) + ")")
	case vm.ObjectType:
		exportObject(m, b, s, indent, st)
	case vm.ResourceType:
		// var_export ресурса в PHP печатает NULL.
		b.WriteString("NULL")
	}
}

// exportValue пишет значение, следующее за "key => ": скаляры — на той же
// строке, массивы и объекты — с новой строки с отступом indent (как PHP).
func exportValue(m *vm.VM, b *strings.Builder, v vm.Slot, indent int, st *exportState) {
	switch v.Type() {
	case vm.ArrayType, vm.ObjectType:
		b.WriteString("\n" + spaces(indent))
	}
	exportSlot(m, b, v, indent, st)
}

// exportObject повторяет var_export объекта PHP: stdClass — (object) array(...),
// остальные — \Class::__set_state(array(...)). Свойства внутри объекта PHP
// печатает с отступом indent+3, вложенные массивы/объекты — с indent+2.
//
// Ограничение: видимость свойств в vm не хранится, поэтому все свойства
// печатаются как public, а манглированные имена (private/protected) не
// восстанавливаются.
func exportObject(m *vm.VM, b *strings.Builder, s vm.Slot, indent int, st *exportState) {
	o := m.SlotObject(s)
	if o == nil {
		b.WriteString("NULL")
		return
	}
	name := o.ClassName()
	std := name == "" || name == "stdClass"
	if !std {
		// ArrayObject/ArrayIterator: var_export отдаёт storage-массив напрямую
		// (get_properties_for), без обёртки в 'storage'.
		if arr, ok := m.SplArrayStorage(o); ok {
			b.WriteString("\\" + name + "::__set_state(array(\n")
			for k, v := range arr.All() {
				b.WriteString(spaces(indent + 3))
				if k.IsStr() {
					exportStringSlot(b, k.StrVal())
				} else {
					b.WriteString(strconv.FormatInt(k.IntVal(), 10))
				}
				b.WriteString(" => ")
				exportValue(m, b, v, indent+2, st)
				b.WriteString(",\n")
			}
			b.WriteString(spaces(indent) + "))")
			return
		}
	}
	if std {
		b.WriteString("(object) array(\n")
	} else {
		b.WriteString("\\" + name + "::__set_state(array(\n")
	}
	for _, en := range m.ObjectDumpEntries(o) {
		if en.Uninit {
			continue
		}
		b.WriteString(spaces(indent + 3))
		exportStringSlot(b, en.Key)
		b.WriteString(" => ")
		exportValue(m, b, en.Val, indent+2, st)
		b.WriteString(",\n")
	}
	if std {
		b.WriteString(spaces(indent) + ")")
	} else {
		b.WriteString(spaces(indent) + "))")
	}
}

// exportStringSlot печатает строку как var_export: NUL-байты (в PHP-строках
// разделители манглированных имён) выводятся как `'a' . "\0" . 'b'`.
func exportStringSlot(b *strings.Builder, v string) {
	parts := strings.Split(v, "\x00")
	for i, p := range parts {
		if i > 0 {
			b.WriteString(` . "\0" . `)
		}
		b.WriteString(exportString(p))
	}
}

// spaces возвращает строку из n пробелов (отступ var_export/print_r).
func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}

// exportString форматирует строку как var_export: одинарные кавычки,
// экранирование '\' и '\”.
func exportString(s string) string {
	esc := strings.ReplaceAll(s, "\\", "\\\\")
	esc = strings.ReplaceAll(esc, "'", "\\'")
	return "'" + esc + "'"
}

func printRFn(ctx *vm.Context) vm.Value {
	if v := ctx.VM(); v != nil {
		if ferr := v.ArmHandles(); ferr != nil {
			throwCallable(ctx, ferr)
			return *new(vm.Value)
		}
	}
	a := ctx.Args()
	var b strings.Builder
	if len(a) > 0 {
		st := &printState{vm: ctx.VM(), arrs: map[*vm.Array]bool{}, objs: map[*vm.Object]bool{}}
		printR(&b, a[0], 0, st)
	}
	if len(a) >= 2 && a[1].ToBool() {
		return vm.Str(b.String())
	}
	ctx.Print(b.String())
	return vm.Bool(true)
}

// printState отслеживает массивы/объекты на текущем пути, чтобы напечатать
// *RECURSION* (иначе циклические ссылки привели бы к бесконечной рекурсии).
type printState struct {
	vm   *vm.VM
	arrs map[*vm.Array]bool
	objs map[*vm.Object]bool
}

func (st *printState) recursive(s vm.Slot) bool {
	switch s.Type() {
	case vm.ArrayType:
		return st.arrs[s.AsArray()]
	case vm.ObjectType:
		return st.objs[st.vm.SlotObject(s)]
	}
	return false
}

func printR(b *strings.Builder, s vm.Slot, lvl int, st *printState) {
	switch s.Type() {
	case vm.ArrayType:
		a := s.AsArray()
		if st.arrs[a] {
			b.WriteString("Array\n *RECURSION*\n")
			return
		}
		st.arrs[a] = true
		defer delete(st.arrs, a)
		pad := strings.Repeat(" ", lvl)
		b.WriteString("Array\n" + pad + "(\n")
		for k, v := range a.All() {
			b.WriteString(strings.Repeat(" ", lvl+4) + "[")
			if k.IsStr() {
				b.WriteString(k.StrVal())
			} else {
				b.WriteString(strconv.FormatInt(k.IntVal(), 10))
			}
			b.WriteString("] => ")
			printRValue(b, v, lvl, st)
		}
		b.WriteString(pad + ")\n")
	case vm.ObjectType:
		o := st.vm.SlotObject(s)
		if o == nil {
			b.WriteString("\n")
			return
		}
		if cls, _, backing, ok := st.vm.EnumCaseInfo(s); ok {
			st.objs[o] = true
			defer delete(st.objs, o)
			pad := strings.Repeat(" ", lvl)
			hdr := cls + " Enum"
			if backing != "" {
				hdr += ":" + backing
			}
			b.WriteString(hdr + "\n" + pad + "(\n")
			for _, de := range st.vm.ObjectDumpEntries(o) {
				if de.Uninit {
					continue
				}
				suffix := strings.ReplaceAll(de.Suffix, "\"", "")
				b.WriteString(strings.Repeat(" ", lvl+4) + "[" + de.Key + suffix + "] => ")
				printRValue(b, de.Val, lvl, st)
			}
			b.WriteString(pad + ")\n")
			return
		}
		if st.objs[o] {
			b.WriteString(o.ClassName() + " Object\n *RECURSION*\n")
			return
		}
		st.objs[o] = true
		defer delete(st.objs, o)
		pad := strings.Repeat(" ", lvl)
		b.WriteString(o.ClassName() + " Object\n" + pad + "(\n")
		if st.vm.IsDumpPlainObject(o) {
			for _, de := range st.vm.ObjectDumpEntries(o) {
				if de.Uninit {
					continue
				}
				suffix := strings.ReplaceAll(de.Suffix, "\"", "")
				b.WriteString(strings.Repeat(" ", lvl+4) + "[" + de.Key + suffix + "] => ")
				printRValue(b, de.Val, lvl, st)
			}
		} else {
			for _, en := range objectJSONEntries(st.vm, o) {
				b.WriteString(strings.Repeat(" ", lvl+4) + "[" + en.key + "] => ")
				printRValue(b, en.val, lvl, st)
			}
		}
		b.WriteString(pad + ")\n")
	default:
		b.WriteString(s.ToString())
	}
}

// printRValue печатает значение после "key => " и добавляет пустую строку
// после вложенного массива/объекта (но не после *RECURSION*).
func printRValue(b *strings.Builder, v vm.Slot, lvl int, st *printState) {
	switch v.Type() {
	case vm.ArrayType, vm.ObjectType:
		rec := st.recursive(v)
		printR(b, v, lvl+8, st)
		if !rec {
			b.WriteString("\n")
		}
	default:
		b.WriteString(v.ToString() + "\n")
	}
}

// splObjectIdFn — spl_object_id(): PHP-хендл объекта.
func splObjectIdFn(ctx *vm.Context) vm.Value {
	if v := ctx.VM(); v != nil {
		if ferr := v.ArmHandles(); ferr != nil {
			throwCallable(ctx, ferr)
			return *new(vm.Value)
		}
	}
	s := arg0(ctx)
	if s.Type() != vm.ObjectType {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError("spl_object_id", 1, "object", "object", reflectTypeName(s), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	return vm.Int(ctx.VM().SplObjectID(s))
}

// splObjectHashFn — spl_object_hash(): 32-символьный hex-идентификатор
// объекта (уникален в пределах запуска; строится из хендла).
func splObjectHashFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx)
	if s.Type() != vm.ObjectType {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError("spl_object_hash", 1, "object", "object", reflectTypeName(s), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	id := ctx.VM().SplObjectID(s)
	return vm.Str(fmt.Sprintf("%032x", uint64(id)))
}

// getMangledObjectVarsFn — get_mangled_object_vars(): свойства объекта под
// внутренними (манглированными) именами.
func getMangledObjectVarsFn(ctx *vm.Context) vm.Value {
	s := reflectArg(ctx, 0)
	if s.Type() != vm.ObjectType {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError("get_mangled_object_vars", 1, "object", "object", reflectTypeName(s), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.MakeNull()
	}
	o := ctx.Arg[*vm.Object](0)
	return vm.MakeArray(ctx.VM().MangledObjectVars(o))
}

// getIncludedFilesFn — get_included_files(): пути подключённых файлов.
func getIncludedFilesFn(ctx *vm.Context) vm.Value {
	out := vm.NewArray()
	for _, p := range ctx.VM().IncludedFiles() {
		out.Append(vm.MakeString(p))
	}
	return vm.MakeArray(out)
}

// referenceOnlyFuncs — имена встроенных функций эталонного php 8.3, которые
// перечисляются в get_defined_functions()/get_extension_funcs(), хотя VM их не
// реализует (тесты сверяют список и disable_functions).
var referenceOnlyFuncs = []string{
	"hash", "hash_hmac", "hash_algos", "hash_file", "crc32", "md5_file", "sha1_file",
}
