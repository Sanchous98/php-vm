package vm

import (
	"fmt"
	"strings"
)

// Отложенные проверки совместимости сигнатур (Zend variance obligations).
//
// Zend проверяет совместимость переопределённых методов при связывании класса.
// Если тип в сигнатуре ссылается на класс, которого ещё нет в таблице классов,
// проверка откладывается: класс публикуется «почти слинкованным», недостающие
// имена ставятся в очередь на автозагрузку, и проверка повторяется после
// загрузки. Если после автозагрузки класс так и не появился — фатальная ошибка
// «Could not check compatibility ... because class X is not available».

// VarianceFatal — фатальная ошибка (E_COMPILE_ERROR) отложенной проверки,
// привязанная к месту объявления метода.
type VarianceFatal struct {
	Msg  string
	File string
	Line int
}

func (e *VarianceFatal) Error() string { return e.Msg }

// VarianceProbe — результат выполнения плана проверок класса.
type VarianceProbe struct {
	// Unresolved — имена классов, которых нет и которые нужно автозагрузить.
	Unresolved []string
	// ChildSig/ParentSig — сигнатуры первой нерешённой проверки (для сообщения
	// «Could not check compatibility ...»).
	ChildSig  string
	ParentSig string
	File      string
	Line      int
}

// VarianceHook вычисляет план отложенной проверки (реализуется php-пакетом).
var VarianceHook func(vm *VM, plan any) (VarianceProbe, error)

// registerDelayedAutoload добавляет имена в очередь отложенной автозагрузки
// (порядок вставки сохраняется, дубликаты отбрасываются).
func (vm *VM) registerDelayedAutoload(names ...string) {
	for _, n := range names {
		n = strings.TrimLeft(n, "\\")
		if n == "" {
			continue
		}
		key := strings.ToLower(n)
		if vm.delayedAutoloadSet == nil {
			vm.delayedAutoloadSet = map[string]bool{}
		}
		if vm.delayedAutoloadSet[key] {
			continue
		}
		vm.delayedAutoloadSet[key] = true
		vm.delayedAutoloads = append(vm.delayedAutoloads, n)
	}
}

// LookupClassVariance возвращает класс по имени для отложенной проверки:
// опубликованные классы и классы, объявление которых сейчас выполняется
// (Zend: lookup_class с ALLOW_UNLINKED, без автозагрузки). Классы, объявление
// которых ещё не началось (скрытые и не declaring), недоступны.
func (vm *VM) LookupClassVariance(name string) ClassInfo {
	key := strings.ToLower(strings.TrimLeft(name, "\\"))
	if key == "" {
		return nil
	}
	for i := len(vm.classes) - 1; i >= 0; i-- {
		ci := vm.classes[i]
		if ci == nil {
			continue
		}
		if strings.ToLower(strings.TrimLeft(ci.Name(), "\\")) != key {
			continue
		}
		if rc, ok := ci.(*runtimeClass); ok {
			if rc.hidden && !rc.declaring {
				continue
			}
		} else if ClassHidden(ci) {
			continue
		}
		return ci
	}
	if ci, ok := vm.classAliases[key]; ok {
		return ci
	}
	return nil
}

// LookupClassVarianceUse — как LookupClassVariance, но помечает найденный
// несвязанный класс как использованный отложенной проверкой (Zend
// CG(unlinked_uses)): если его объявление затем не сможет разрешить родителя,
// мягкий откат невозможен.
func (vm *VM) LookupClassVarianceUse(name string) ClassInfo {
	ci := vm.LookupClassVariance(name)
	if rc, ok := ci.(*runtimeClass); ok && rc != nil && !rc.linked {
		if vm.unlinkedUses == nil {
			vm.unlinkedUses = map[*runtimeClass]bool{}
		}
		vm.unlinkedUses[rc] = true
	}
	return ci
}

// varianceDepsFailure — Zend check_unrecoverable_load_failure: объявление
// класса уже использовано отложенной проверкой совместимости; при провале
// загрузки родителя «мягкий» откат невозможен, Zend фаталит с исходным
// исключением: «During inheritance of X with variance dependencies».
func (vm *VM) varianceDepsFailure(rc *runtimeClass, cause error) (execResult, bool) {
	if rc == nil || !vm.unlinkedUses[rc] {
		return execResult{}, false
	}
	th, ok := asThrowable(cause)
	if !ok || th == nil {
		return execResult{}, false
	}
	file := rc.defFile
	if file == "" {
		file = vm.curFile
	}
	if file == "" {
		file = "php"
	}
	line := rc.defStart
	if line <= 0 {
		line = vm.line()
	}
	if line <= 0 {
		line = 1
	}
	fmt.Fprintf(vm.out, "\nFatal error: During inheritance of %s with variance dependencies: Uncaught %s in %s on line %d\n",
		rc.name, vm.exceptionString(th), file, line)
	return execResult{err: exitSignal{}}, true
}

// exceptionString рендерит исключение как Exception::__toString
// («Класс: сообщение in file:line\nStack trace:\n...») — для сообщений
// zend_exception_uncaught_error.
func (vm *VM) exceptionString(th *Throwable) string {
	class := th.Class
	if class == "" {
		class = "Error"
	}
	var b strings.Builder
	b.WriteString(class)
	if th.Message != "" {
		b.WriteString(": ")
		b.WriteString(th.Message)
	}
	file, line := th.LocFile, th.LocLine
	if file == "" && line == 0 && len(th.Trace) > 0 {
		f := th.Trace[len(th.Trace)-1]
		if f.callerFile != "" {
			file, line = f.callerFile, f.callerLine
		}
	}
	if file != "" {
		fmt.Fprintf(&b, " in %s:%d", file, line)
	} else if line > 0 {
		fmt.Fprintf(&b, " in :%d", line)
	}
	b.WriteString("\nStack trace:\n")
	b.WriteString(vm.traceString(th.Trace, file, line))
	return b.String()
}

// UnlinkedIsA повторяет zend unlinked_instanceof: проверяет наследование даже
// для классов, связывание которых ещё не завершено (объявление в процессе),
// обходя имена родителя/интерфейсов без автозагрузки.
func (vm *VM) UnlinkedIsA(child, parent ClassInfo) bool {
	return vm.unlinkedIsA(child, parent, 0)
}

func (vm *VM) unlinkedIsA(child, parent ClassInfo, depth int) bool {
	if child == nil || parent == nil || depth > 100 {
		return false
	}
	if child == parent {
		return true
	}
	rc, ok := child.(*runtimeClass)
	if !ok || rc == nil {
		return child.IsA(parent)
	}
	if rc.linked {
		return rc.IsA(parent)
	}
	if rc.parent != nil {
		if vm.unlinkedIsA(rc.parent, parent, depth+1) {
			return true
		}
	} else if rc.missingParent != "" {
		if p := vm.LookupClassVariance(rc.missingParent); p != nil && p != child {
			if vm.unlinkedIsA(p, parent, depth+1) {
				return true
			}
		}
	}
	if len(rc.ifaces) > 0 {
		for _, i := range rc.ifaces {
			if i != nil && i != child && vm.unlinkedIsA(i, parent, depth+1) {
				return true
			}
		}
	} else {
		for _, n := range rc.missingIfaces {
			p := vm.LookupClassVariance(n)
			if p != nil && p != child && vm.unlinkedIsA(p, parent, depth+1) {
				return true
			}
		}
	}
	return false
}

// checkVariance выполняет план отложенных проверок класса.
func (vm *VM) checkVariance(rc *runtimeClass) (VarianceProbe, error) {
	if rc == nil || rc.checkData == nil || VarianceHook == nil {
		return VarianceProbe{}, nil
	}
	return VarianceHook(vm, rc.checkData)
}

// loadDelayedClasses автозагружает накопленные имена (Zend load_delayed_classes):
// цикл «взять и загрузить первый» — автозагрузка может объявить новые классы,
// которые доберут остаток очереди при своём объявлении.
func (vm *VM) loadDelayedClasses() error {
	for len(vm.delayedAutoloads) > 0 {
		name := vm.delayedAutoloads[0]
		vm.delayedAutoloads = vm.delayedAutoloads[1:]
		delete(vm.delayedAutoloadSet, strings.ToLower(name))
		raw := strings.TrimLeft(name, "\\")
		if raw == "" {
			continue
		}
		// Класс уже существует или объявляется прямо сейчас — не грузим.
		if vm.LookupClassVariance(raw) != nil {
			continue
		}
		if !validClassAutoName(raw) {
			continue
		}
		if _, err := vm.Autoload(&vm.ctx, raw); err != nil {
			return err
		}
	}
	return nil
}

// resolveVariance разрешает отложенные проверки класса и его зависимостей
// (Zend resolve_delayed_variance_obligations). Если проверка после автозагрузки
// всё ещё не может быть выполнена — фатальная ошибка.
func (vm *VM) resolveVariance(rc *runtimeClass) error {
	if rc == nil {
		return nil
	}
	if vm.resolvingVariance == nil {
		vm.resolvingVariance = map[*runtimeClass]bool{}
	}
	if vm.resolvingVariance[rc] {
		return nil
	}
	vm.resolvingVariance[rc] = true
	defer delete(vm.resolvingVariance, rc)
	for _, d := range rc.deps {
		drc, ok := d.(*runtimeClass)
		if !ok || drc == nil {
			continue
		}
		if drc.variancePending || len(drc.deps) > 0 {
			if err := vm.resolveVariance(drc); err != nil {
				return err
			}
		}
	}
	rc.deps = nil
	if !rc.variancePending {
		return nil
	}
	probe, err := vm.checkVariance(rc)
	if err != nil {
		return err
	}
	if len(probe.Unresolved) == 0 {
		rc.variancePending = false
		return nil
	}
	rc.variancePending = false
	file := probe.File
	if file == "" {
		file = vm.curFile
	}
	if file == "" {
		file = "php"
	}
	line := probe.Line
	if line <= 0 {
		line = vm.line()
	}
	if line <= 0 {
		line = 1
	}
	return &VarianceFatal{
		Msg: "Could not check compatibility between " + probe.ChildSig +
			" and " + probe.ParentSig + ", because class " + probe.Unresolved[0] + " is not available",
		File: file,
		Line: line,
	}
}

// emitVarianceFatal печатает фатальную ошибку E_COMPILE_ERROR и завершает
// выполнение (как zend_error_at(E_COMPILE_ERROR, ...)).
func (vm *VM) emitVarianceFatal(err error) execResult {
	msg, file, line := "", "", 0
	if fe, ok := err.(*VarianceFatal); ok {
		msg, file, line = fe.Msg, fe.File, fe.Line
	} else {
		msg = err.Error()
	}
	if file == "" {
		file = vm.curFile
	}
	if file == "" {
		file = "php"
	}
	if line <= 0 {
		line = vm.line()
	}
	if line <= 0 {
		line = 1
	}
	fmt.Fprintf(vm.out, "\nFatal error: %s in %s on line %d\n", msg, file, line)
	return execResult{err: exitSignal{}}
}
