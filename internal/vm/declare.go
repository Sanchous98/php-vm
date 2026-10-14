package vm

import "fmt"

// declareClassKind возвращает слово для сообщения о повторном объявлении.
func declareClassKind(rc *runtimeClass) string {
	switch {
	case rc == nil:
		return "class"
	case rc.IsEnum():
		return "enum"
	case rc.iface:
		return "interface"
	case rc.declTrait:
		return "trait"
	}
	return "class"
}

// opDeclareClass выполняет рантайм-объявление класса/интерфейса/трейта
// (OpDeclareClass). Дескриптор vm.classes[in.C] построен компилятором, но был
// скрыт (HideClass); здесь он становится видимым по имени, а отложенные
// родитель/интерфейсы разрешаются (с автозагрузкой) и линкуются.
//
// Повторное выполнение того же объявления (класс уже виден) — фатал Zend
// «Cannot declare ... because the name is already in use».
func (vm *VM) opDeclareClass(chunk *Chunk, in Instruction) execResult {
	idx := int(in.C)
	if idx < 0 || idx >= len(vm.classes) {
		return execResult{}
	}
	ci := vm.classes[idx]
	rc, ok := ci.(*runtimeClass)
	if !ok || rc == nil {
		return execResult{}
	}
	if !rc.hidden {
		file := vm.curFile
		if file == "" {
			file = "php"
		}
		line := vm.line()
		if line <= 0 {
			line = 1
		}
		fmt.Fprintf(vm.out, "\nFatal error: Cannot declare %s %s, because the name is already in use in %s on line %d\n",
			declareClassKind(rc), rc.name, file, line)
		return execResult{err: exitSignal{}}
	}
	// Пока выполняется объявление, класс виден отложенным проверкам
	// совместимости как «почти слинкованный» (Zend регистрирует класс в
	// таблице до связывания).
	rc.declaring = true
	defer func() { rc.declaring = false }()

	// Разрешаем отложенного родителя. Состояние дескриптора не меняем, пока не
	// разрешились все зависимости: иначе неудачное объявление (перехваченное
	// исключение) нельзя повторить в цикле.
	var parent ClassInfo
	if rc.parent != nil {
		parent = rc.parent
	}
	if parent == nil && rc.missingParent != "" {
		name := rc.missingParent
		var aerr error
		if vm.findClass(name) == nil {
			if !validClassAutoName(name) {
				if res, ok := vm.varianceDepsFailure(rc, nil); ok {
					return res
				}
				return vm.declareFail(rc, "Class", name, chunk)
			}
			if _, aerr = vm.Autoload(&vm.ctx, name); aerr != nil {
				if res, ok := vm.varianceDepsFailure(rc, aerr); ok {
					return res
				}
				return execResult{err: aerr}
			}
		}
		p := vm.FindClass(name)
		if p == nil {
			if res, ok := vm.varianceDepsFailure(rc, aerr); ok {
				return res
			}
			return vm.declareFail(rc, "Class", name, chunk)
		}
		parent = p
	}

	// Разрешаем отложенные интерфейсы.
	var ifaces []ClassInfo
	for _, name := range rc.missingIfaces {
		var aerr error
		if vm.findClass(name) == nil {
			if !validClassAutoName(name) {
				if res, ok := vm.varianceDepsFailure(rc, nil); ok {
					return res
				}
				return vm.declareFail(rc, "Interface", name, chunk)
			}
			if _, aerr = vm.Autoload(&vm.ctx, name); aerr != nil {
				if res, ok := vm.varianceDepsFailure(rc, aerr); ok {
					return res
				}
				return execResult{err: aerr}
			}
		}
		i := vm.FindClass(name)
		if i == nil {
			if res, ok := vm.varianceDepsFailure(rc, aerr); ok {
				return res
			}
			return vm.declareFail(rc, "Interface", name, chunk)
		}
		ifaces = append(ifaces, i)
	}

	// Все зависимости разрешены — фиксируем их в дескрипторе.
	// Zend zend_do_implement_interfaces: «интерфейс», оказавшийся классом, —
	// фатальная ошибка (для рантайм-объявлений проверка отложена сюда).
	for _, i := range append(append([]ClassInfo(nil), rc.ifaces...), ifaces...) {
		if i == nil || i.IsInterface() {
			continue
		}
		file := vm.curFile
		if file == "" {
			file = "php"
		}
		line := vm.line()
		if line <= 0 {
			line = 1
		}
		fmt.Fprintf(vm.out, "\nFatal error: %s cannot implement %s - it is not an interface in %s on line %d\n",
			rc.name, i.Name(), file, line)
		return execResult{err: exitSignal{}}
	}
	if parent != nil {
		rc.SetParent(parent)
	}
	if len(ifaces) > 0 {
		rc.Implements(ifaces...)
	}
	rc.missingParent = ""
	rc.missingIfaces = nil
	rc.linked = false
	rc.link()
	// Класс публикуется до проверок совместимости: Zend вносит запись в
	// таблицу классов до связывания, и вложенные объявления (автозагрузка)
	// видят его как «почти слинкованный».
	rc.hidden = false

	// Отложенные проверки совместимости сигнатур (variance obligations).
	// Зависимости от «почти слинкованных» предков разрешаются в конце
	// объявления этого класса.
	for _, d := range append([]ClassInfo{rc.parent}, rc.ifaces...) {
		drc, ok := d.(*runtimeClass)
		if !ok || drc == nil || drc == rc {
			continue
		}
		if drc.variancePending || len(drc.deps) > 0 {
			rc.deps = append(rc.deps, drc)
		}
	}
	if rc.checkData != nil {
		probe, err := vm.checkVariance(rc)
		if err != nil {
			return vm.emitVarianceFatal(err)
		}
		if len(probe.Unresolved) > 0 {
			rc.variancePending = true
			vm.registerDelayedAutoload(probe.Unresolved...)
		}
	}
	if rc.variancePending || len(rc.deps) > 0 {
		if len(vm.delayedAutoloads) > 0 {
			if err := vm.loadDelayedClasses(); err != nil {
				return execResult{err: err}
			}
		}
		if err := vm.resolveVariance(rc); err != nil {
			return vm.emitVarianceFatal(err)
		}
	}
	return execResult{}
}

// RuntimeVarianceCheckIdx выполняет отложенную проверку совместимости
// сигнатур класса верхнего уровня по индексу в vm.classes (служебная функция
// __php_variance_check): недостающие классы автозагружаются, проверка
// повторяется; при неустранимой ошибке фатальная уже напечатана, возвращается
// сигнал выхода.
func (vm *VM) RuntimeVarianceCheckIdx(idx int) error {
	if idx < 0 || idx >= len(vm.classes) {
		return nil
	}
	rc, ok := vm.classes[idx].(*runtimeClass)
	if !ok || rc == nil || rc.checkData == nil {
		return nil
	}
	probe, err := vm.checkVariance(rc)
	if err != nil {
		return vm.emitVarianceFatal(err).err
	}
	if len(probe.Unresolved) > 0 {
		rc.variancePending = true
		vm.registerDelayedAutoload(probe.Unresolved...)
	}
	if rc.variancePending || len(rc.deps) > 0 {
		if len(vm.delayedAutoloads) > 0 {
			if err := vm.loadDelayedClasses(); err != nil {
				return err
			}
		}
		if err := vm.resolveVariance(rc); err != nil {
			return vm.emitVarianceFatal(err).err
		}
	}
	return nil
}

// declareFail бросает Error «Class/Interface "name" not found» (перехватываемый).
// Дескриптор остаётся скрытым, чтобы объявление можно было повторить.
func (vm *VM) declareFail(rc *runtimeClass, kind, name string, chunk *Chunk) execResult {
	err := NewThrowable("Error", kind+" \""+name+"\" not found")
	if !vm.handleThrow(chunk, err) {
		return execResult{err: err}
	}
	return execResult{next: true}
}
