package vm

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Флаги буфера вывода (совместимы с PHP_OUTPUT_HANDLER_*).
const (
	obFlagStart     = 1
	obFlagWrite     = 0
	obFlagClean     = 2
	obFlagFlush     = 4
	obFlagFinal     = 8
	obFlagCleanable = 16
	obFlagFlushable = 32
	obFlagRemovable = 64
	// obFlagStarted/obFlagProcessed — внутренние флаги Zend (PHP_OUTPUT_HANDLER_STARTED
	// и _PROCESSED): обработчик уже вызывался.
	obFlagStarted   = 4096
	obFlagProcessed = 16384
	// obStdFlags — набор по умолчанию (CLEANABLE|FLUSHABLE|REMOVABLE = 112).
	obStdFlags = obFlagCleanable | obFlagFlushable | obFlagRemovable
)

// fatalSignal — сигнал фатальной ошибки (напр. попытки буферизации из
// обработчика вывода). Это НЕ исключение, а нелокальный выход: он паникует и
// гасится единственным recover в Run.
type fatalSignal struct{}

// obBuffer — один активный буфер вывода.
type obBuffer struct {
	b         strings.Builder
	hasCB     bool
	callback  Slot
	chunkSize int64
	flags     int64
	removable bool
	cleanable bool
	flushable bool
	started   bool
	// disabled — обработчик бросил исключение: Zend помечает его DISABLED и
	// больше не вызывает (буфер при этом остаётся).
	disabled bool
	name     string
}

// ObStatus — снимок состояния буфера (для ob_get_status).
type ObStatus struct {
	Name       string
	Type       int64
	Flags      int64
	Level      int64
	ChunkSize  int64
	BufferSize int64
	BufferUsed int64
}

// Write направляет строку в верхний буфер вывода, если он есть, иначе в out.
func (vm *VM) Write(s string) { vm.writeAt(len(vm.obStack)-1, s) }

// HeadersSent сообщает, был ли уже вывод, и где он начался (для header()).
func (vm *VM) HeadersSent() (bool, string, int) {
	return vm.headersSent, vm.headersSentFile, vm.headersSentLine
}

// writeAt пишет в буфер уровня level (-1 — непосредственно в out) и, при
// необходимости, сбрасывает его по достижении chunk_size.
func (vm *VM) writeAt(level int, s string) {
	if level < 0 {
		if !vm.headersSent && s != "" {
			vm.headersSent = true
			f := vm.curFile
			if f == "" {
				f = "php"
			}
			vm.headersSentFile = f
			vm.headersSentLine = vm.line()
		}
		io.WriteString(vm.out, s)
		return
	}
	b := vm.obStack[level]
	b.b.WriteString(s)
	if b.chunkSize > 0 && int64(b.b.Len()) >= b.chunkSize {
		vm.obChunkFlush(level)
	}
}

// obChunkFlush сбрасывает буфер уровня level из-за переполнения chunk_size.
func (vm *VM) obChunkFlush(level int) {
	b := vm.obStack[level]
	contents := b.b.String()
	b.b.Reset()
	out := contents
	if b.hasCB && !b.disabled {
		res, _, err := vm.obCall(b, contents, obFlagWrite)
		if err != nil {
			// Исключение обработчика: Zend выводит исходное содержимое
			// буфера, а затем поднимает исключение.
			vm.writeAt(level-1, contents)
			vm.ctx.Throw(toThrowable(err))
			return
		}
		out = res
	}
	vm.writeAt(level-1, out)
}

// obCall вызывает обработчик буфера. Флаг START добавляется при первом вызове.
// ok=false означает, что обработчик вернул false: Zend считает это отказом и
// оставляет исходное содержимое буфера.
func (vm *VM) obCall(b *obBuffer, contents string, phase int64) (string, bool, error) {
	flags := phase
	if !b.started {
		flags |= obFlagStart
		b.started = true
	}
	savedIn := vm.obInHandler
	vm.obInHandler = true
	defer func() { vm.obInHandler = savedIn }()
	savedArgs := vm.ctx.args
	defer func() { vm.ctx.args = savedArgs }()
	res, err := vm.CallCallable(&vm.ctx, b.callback, []Slot{MakeString(contents), MakeInt(flags)})
	if err != nil {
		b.disabled = true
		return "", false, err
	}
	r := deref(res)
	if r.typ == BoolType && !r.BoolVal() {
		return contents, false, nil
	}
	// Zend после успешного вызова обработчика выставляет STARTED|PROCESSED
	// (транзитные флаги операции сбрасываются).
	b.flags = (b.flags &^ int64(obFlagWrite|obFlagClean|obFlagFlush|obFlagFinal)) | obFlagStarted | obFlagProcessed
	return r.ToString(), true, nil
}

// obCallbackName возвращает имя обработчика для ob_list_handlers.
func obCallbackName(cb Slot) string {
	cb = deref(cb)
	switch cb.typ {
	case StringType:
		return strings.TrimLeft(cb.StringVal(), "\\")
	case ArrayType:
		a := cb.AsArray()
		if a != nil && a.Len() == 2 {
			vals := a.Values()
			first := deref(vals[0])
			name := ""
			switch first.typ {
			case ObjectType:
				if first.ptr != nil {
					name = (*Object)(first.ptr).ClassName()
				}
			case ClosureType:
				name = "Closure"
			default:
				name = first.ToString()
			}
			return strings.TrimLeft(name, "\\") + "::" + deref(vals[1]).ToString()
		}
	case ObjectType:
		return "Closure::__invoke"
	}
	return "default output handler"
}

// ObStart начинает новый буфер вывода. callback — обработчик (null — нет),
// chunkSize — размер порции (0 — без), flags — флаги PHP_OUTPUT_HANDLER_*.
func (vm *VM) ObStart(callback Slot, chunkSize, flags int64) {
	if vm.obInHandler {
		vm.obFatal("ob_start")
		return
	}
	b := &obBuffer{chunkSize: chunkSize, flags: flags}
	if flags != 0 {
		b.removable = flags&obFlagRemovable != 0
		b.cleanable = flags&obFlagCleanable != 0
		b.flushable = flags&obFlagFlushable != 0
	}
	cb := deref(callback)
	if cb.typ != NullType {
		b.hasCB = true
		b.callback = cb
		b.name = obCallbackName(cb)
	} else {
		b.name = "default output handler"
	}
	vm.obStack = append(vm.obStack, b)
}

// ObGetLevel возвращает уровень вложенности буферов.
func (vm *VM) ObGetLevel() int { return len(vm.obStack) }

// ObGetContents возвращает текущее содержимое верхнего буфера.
func (vm *VM) ObGetContents() (string, bool) {
	if len(vm.obStack) == 0 {
		return "", false
	}
	return vm.obStack[len(vm.obStack)-1].b.String(), true
}

// ObGetLength возвращает длину содержимого верхнего буфера.
func (vm *VM) ObGetLength() (int, bool) {
	if len(vm.obStack) == 0 {
		return 0, false
	}
	return vm.obStack[len(vm.obStack)-1].b.Len(), true
}

// ObClean очищает верхний буфер, не удаляя его.
func (vm *VM) ObClean() bool {
	if vm.obInHandler {
		vm.obFatal("ob_clean")
		return false
	}
	if len(vm.obStack) == 0 {
		vm.Notice("ob_clean(): Failed to delete buffer. No buffer to delete")
		return false
	}
	top := vm.obStack[len(vm.obStack)-1]
	if !top.cleanable {
		vm.Notice(fmt.Sprintf("ob_clean(): Failed to delete buffer of callback (%d)", top.chunkSize))
		return false
	}
	if top.hasCB && !top.disabled {
		if _, _, err := vm.obCall(top, top.b.String(), obFlagClean); err != nil {
			vm.ctx.Throw(toThrowable(err))
			return false
		}
	}
	top.b.Reset()
	return true
}

// ObFlush отправляет содержимое верхнего буфера наружу, не удаляя буфер.
func (vm *VM) ObFlush() bool {
	if vm.obInHandler {
		vm.obFatal("ob_flush")
		return false
	}
	if len(vm.obStack) == 0 {
		vm.Notice("ob_flush(): Failed to flush buffer. No buffer to flush")
		return false
	}
	level := len(vm.obStack) - 1
	top := vm.obStack[level]
	if !top.flushable {
		vm.Notice(fmt.Sprintf("ob_flush(): Failed to flush buffer of callback (%d)", top.chunkSize))
		return false
	}
	contents := top.b.String()
	top.b.Reset()
	out := contents
	if top.hasCB && !top.disabled {
		res, _, err := vm.obCall(top, contents, obFlagFlush)
		if err != nil {
			vm.writeAt(level-1, contents)
			vm.ctx.Throw(toThrowable(err))
			return false
		}
		out = res
	}
	vm.writeAt(level-1, out)
	return true
}

// ObEndClean удаляет верхний буфер, отбрасывая его содержимое.
func (vm *VM) ObEndClean() bool {
	if vm.obInHandler {
		vm.obFatal("ob_end_clean")
		return false
	}
	if len(vm.obStack) == 0 {
		vm.Notice("ob_end_clean(): Failed to delete buffer. No buffer to delete")
		return false
	}
	top := vm.obStack[len(vm.obStack)-1]
	if !top.removable || !top.cleanable {
		vm.Notice(fmt.Sprintf("ob_end_clean(): Failed to discard buffer of callback (%d)", top.chunkSize))
		return false
	}
	if top.hasCB && !top.disabled {
		// Буфер снимаем до вызова обработчика: если он бросит исключение,
		// буфер уже удалён и при shutdown повторно не вызывается (как в Zend).
		vm.obStack = vm.obStack[:len(vm.obStack)-1]
		if _, _, err := vm.obCall(top, top.b.String(), obFlagFinal|obFlagClean); err != nil {
			vm.ctx.Throw(toThrowable(err))
			return false
		}
		return true
	}
	vm.obStack = vm.obStack[:len(vm.obStack)-1]
	return true
}

// ObEndFlush удаляет верхний буфер, выводя его содержимое наружу.
func (vm *VM) ObEndFlush() bool {
	if vm.obInHandler {
		vm.obFatal("ob_end_flush")
		return false
	}
	if len(vm.obStack) == 0 {
		vm.Notice("ob_end_flush(): Failed to delete and flush buffer. No buffer to delete or flush")
		return false
	}
	level := len(vm.obStack) - 1
	top := vm.obStack[level]
	if !top.removable || !top.flushable {
		vm.Notice(fmt.Sprintf("ob_end_flush(): Failed to send buffer of callback (%d)", top.chunkSize))
		return false
	}
	contents := top.b.String()
	vm.obStack = vm.obStack[:level]
	out := contents
	if top.hasCB && !top.disabled {
		res, _, err := vm.obCall(top, contents, obFlagFinal)
		if err != nil {
			vm.writeAt(level-1, contents)
			vm.ctx.Throw(toThrowable(err))
			return false
		}
		out = res
	}
	vm.writeAt(level-1, out)
	return true
}

// ObGetClean возвращает содержимое верхнего буфера и удаляет его.
func (vm *VM) ObGetClean() (string, bool) {
	if vm.obInHandler {
		vm.obFatal("ob_get_clean")
		return "", false
	}
	if len(vm.obStack) == 0 {
		return "", false
	}
	top := vm.obStack[len(vm.obStack)-1]
	contents := top.b.String()
	if !top.removable || !top.cleanable {
		vm.Notice(fmt.Sprintf("ob_get_clean(): Failed to discard buffer of callback (%d)", top.chunkSize))
		vm.Notice(fmt.Sprintf("ob_get_clean(): Failed to delete buffer of callback (%d)", top.chunkSize))
		return contents, true
	}
	vm.obStack = vm.obStack[:len(vm.obStack)-1]
	return contents, true
}

// ObGetFlush возвращает содержимое верхнего буфера, выводит его и удаляет буфер.
func (vm *VM) ObGetFlush() (string, bool) {
	if vm.obInHandler {
		vm.obFatal("ob_get_flush")
		return "", false
	}
	if len(vm.obStack) == 0 {
		vm.Notice("ob_get_flush(): Failed to delete and flush buffer. No buffer to delete or flush")
		return "", false
	}
	level := len(vm.obStack) - 1
	top := vm.obStack[level]
	contents := top.b.String()
	if !top.removable || !top.flushable {
		vm.Notice(fmt.Sprintf("ob_get_flush(): Failed to send buffer of callback (%d)", top.chunkSize))
		vm.Notice(fmt.Sprintf("ob_get_flush(): Failed to delete buffer of callback (%d)", top.chunkSize))
		return contents, true
	}
	vm.obStack = vm.obStack[:level]
	out := contents
	if top.hasCB && !top.disabled {
		res, _, err := vm.obCall(top, contents, obFlagFinal)
		if err != nil {
			vm.ctx.Throw(toThrowable(err))
			return contents, false
		}
		out = res
	}
	vm.writeAt(level-1, out)
	return contents, true
}

// ObListHandlers возвращает имена активных обработчиков снизу вверх.
func (vm *VM) ObListHandlers() []string {
	out := make([]string, 0, len(vm.obStack))
	for _, b := range vm.obStack {
		out = append(out, b.name)
	}
	return out
}

// ObGetStatus возвращает снимок состояния буферов. all=true — все снизу вверх,
// иначе только верхний.
func (vm *VM) ObGetStatus(all bool) []ObStatus {
	var out []ObStatus
	if !all {
		if len(vm.obStack) == 0 {
			return nil
		}
		b := vm.obStack[len(vm.obStack)-1]
		return []ObStatus{obStatusOf(b, int64(len(vm.obStack)-1))}
	}
	for i, b := range vm.obStack {
		out = append(out, obStatusOf(b, int64(i)))
	}
	return out
}

func obStatusOf(b *obBuffer, level int64) ObStatus {
	typ := int64(0)
	flags := b.flags
	if b.hasCB {
		typ = 1
		if flags == 0 {
			flags = obStdFlags
		}
		flags |= obFlagStart
	}
	return ObStatus{
		Name:       b.name,
		Type:       typ,
		Flags:      flags,
		Level:      level,
		ChunkSize:  b.chunkSize,
		BufferSize: 16384,
		BufferUsed: int64(b.b.Len()),
	}
}

// ObImplicitFlush устанавливает режим неявного сброса. Он влияет только на
// верхний уровень (не на пользовательские буферы), поэтому здесь no-op.
func (vm *VM) ObImplicitFlush(on bool) { vm.obImplicitFlush = on }

// flushOutputBuffers сбрасывает все активные буферы (вызывается при завершении
// скрипта), применяя обработчики.
func (vm *VM) flushOutputBuffers() {
	for len(vm.obStack) > 0 {
		level := len(vm.obStack) - 1
		b := vm.obStack[level]
		contents := b.b.String()
		vm.obStack = vm.obStack[:level]
		out := contents
		if b.hasCB && !b.disabled {
			res, _, err := vm.obCall(b, contents, obFlagFinal)
			if err != nil {
				vm.ctx.Throw(toThrowable(err))
				return
			}
			out = res
		}
		vm.writeAt(level-1, out)
	}
}

// ResetOutputBuffers очищает состояние буферизации (для повторного Init).
func (vm *VM) ResetOutputBuffers() {
	vm.obStack = vm.obStack[:0]
	vm.obInHandler = false
}

// Notice печатает PHP-совместимое уведомление (Notice) с текущим именем файла
// и номером строки. Учитывает error_reporting и подавление `@`.
func (vm *VM) Notice(msg string) { vm.reportDiag("Notice", E_NOTICE, msg) }

// Warning печатает PHP-совместимое предупреждение (Warning).
func (vm *VM) Warning(msg string) { vm.reportDiag("Warning", E_WARNING, msg) }

// Warn — синоним Warning.
func (vm *VM) Warn(msg string) { vm.reportDiag("Warning", E_WARNING, msg) }

// WarnAt печатает предупреждение с явной локацией (диагностики компиляции
// eval/include: файл отличается от текущего кадра).
func (vm *VM) WarnAt(msg, file string, line int) {
	savedFile, savedLine := vm.curFile, vm.curLine
	if file != "" {
		vm.setCurFile(file)
	}
	if line > 0 {
		vm.curLine = line
	}
	vm.Warning(msg)
	vm.setCurFile(savedFile)
	vm.curLine = savedLine
}

// PrintStartupDeprecated печатает стартовую Deprecated-диагностику чанка
// (eval/include): текст уже содержит файл и строку, поэтому выводится так же,
// как startup-диагностики главного чанка в Run.
func (vm *VM) PrintStartupDeprecated(msg string) {
	if vm.errReporting&E_DEPRECATED != 0 && vm.suppress == 0 {
		vm.Write("\nDeprecated: " + msg + "\n")
	}
}

// Deprecated печатает PHP-совместимое предупреждение об устаревании (Deprecated).
func (vm *VM) Deprecated(msg string) { vm.reportDiag("Deprecated", E_DEPRECATED, msg) }

// Уровни error_reporting (совместимы с PHP).
const (
	E_ERROR         int64 = 1
	E_WARNING       int64 = 2
	E_PARSE         int64 = 4
	E_NOTICE        int64 = 8
	E_CORE_ERROR    int64 = 16
	E_CORE_WARNING  int64 = 32
	E_COMPILE_ERROR int64 = 64
	E_COMPILE_WARN  int64 = 128
	E_USER_ERROR    int64 = 256
	E_USER_WARNING  int64 = 512
	E_USER_NOTICE   int64 = 1024
	E_STRICT        int64 = 2048
	E_RECOVERABLE   int64 = 4096
	E_DEPRECATED    int64 = 8192
	E_USER_DEPRECAT int64 = 16384
	E_ALL           int64 = 32767

	// eFatalErrors — E_FATAL_ERRORS Zend: биты, которые оператор `@` не глушит.
	eFatalErrors = E_ERROR | E_CORE_ERROR | E_COMPILE_ERROR | E_USER_ERROR | E_RECOVERABLE | E_PARSE
)

// onlyFatalErrors повторяет E_HAS_ONLY_FATAL_ERRORS.
func onlyFatalErrors(mask int64) bool { return mask&^eFatalErrors == 0 }

// reportDiag печатает диагностику (Warning/Notice/Deprecated) в формате PHP,
// если её уровень разрешён error_reporting и она не подавлена через `@`.
func (vm *VM) reportDiag(kind string, level int64, msg string) {
	file := vm.curFileAt()
	if file == "" {
		file = "php"
	}
	line := vm.line()
	if vm.diagLineOverride > 0 {
		line = vm.diagLineOverride
	}
	vm.lastErrLevel = level
	vm.lastErrMsg = msg
	vm.lastErrFile = file
	vm.lastErrLine = line
	vm.lastErrSet = true
	if level&eFatalErrors != 0 {
		// Фатальная диагностика (в т.ч. trigger_error(E_USER_ERROR)): в Zend
		// она завершает скрипт. VM продолжает выполнение (исторически), но
		// проверки `never`/неявного возврата после неё подавляются — иначе
		// появляются ошибки, которых в PHP нет.
		vm.fatalDiag = true
	}
	if vm.errReporting&level == 0 {
		return
	}
	// Пользовательский обработчик (set_error_handler) получает управление
	// вместо стандартного вывода; если он вернул false — печатаем как обычно.
	if deref(vm.errHandler).typ != NullType && !vm.inErrHandler {
		vm.inErrHandler = true
		pc := 0
		if vm.ip > 0 {
			pc = vm.ip - 1
		}
		// Zend зовёт обработчик из точки ошибки: кадр handler'а получает
		// файл/строку ошибки, а не "[internal function]".
		res, err := vm.CallCallableAt(&vm.ctx, vm.errHandler, []Slot{
			MakeInt(level), MakeString(msg), MakeString(file), MakeInt(int64(line)),
		}, file, vm.chunk, pc, line)
		vm.inErrHandler = false
		if err != nil {
			// Исключение из пользовательского обработчика ошибок:
			// фиксируем и возвращаемся (поднимет цикл VM/wrapper).
			vm.ctx.Throw(toThrowable(err))
			return
		}
		handled := true
		if r := deref(res); r.typ == BoolType && !r.ToBool() {
			handled = false
		}
		if handled {
			return
		}
	}
	vm.logDiag(kind, msg, file, line)
	// html_errors=1: диагностика в HTML-формате (main.c php_error_cb).
	if vm.iniLookup != nil {
		if v, ok := vm.iniLookup("html_errors"); ok && iniBool(v) {
			vm.Write(fmt.Sprintf("<br />\n<b>%s</b>:  %s in <b>%s</b> on line <b>%d</b><br />\n",
				kind, errHTML(msg), file, line))
			return
		}
	}
	vm.Write(fmt.Sprintf("\n%s: %s in %s on line %d\n", kind, msg, file, line))
}

// errHTML повторяет escape_html() для текста диагностики (кавычки-одиночки и
// прочие символы не трогаются).
func errHTML(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// SetIniLookup устанавливает доступ к INI-настройкам для журналирования
// диагностик в error_log (log_errors/error_log/date.timezone).
func (vm *VM) SetIniLookup(fn func(name string) (string, bool)) { vm.iniLookup = fn }

// iniBool интерпретирует значение INI как bool (как php_ini).
func iniBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "on", "true", "yes":
		return true
	}
	return false
}

// AppendErrorLog дописывает строку в error-лог с меткой времени и зоной из
// INI date.timezone (формат PHP: "[d-M-Y H:i:s TZ] текст").
func (vm *VM) AppendErrorLog(path, text string) {
	if path == "" {
		return
	}
	// CLI: error_log с пустой директивой пишет в stderr без префикса времени
	// (run-tests объединяет потоки — bug79919).
	if path == "php://stderr" || path == "php://stdout" {
		fmt.Fprintln(vm.out, text)
		return
	}
	tz := "UTC"
	if vm.iniLookup != nil {
		if t, ok := vm.iniLookup("date.timezone"); ok && t != "" {
			tz = t
		}
	}
	loc := time.UTC
	if l, err := time.LoadLocation(tz); err == nil {
		loc = l
	}
	now := time.Now().In(loc).Format("02-Jan-2006 15:04:05")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "[%s %s] %s\n", now, tz, text)
}

// logDiag записывает диагностику в error_log, если log_errors=On и задан
// error_log-файл (аналог zend_error -> php_log_err).
func (vm *VM) logDiag(kind, msg, file string, line int) {
	if vm.iniLookup == nil {
		return
	}
	lv, ok := vm.iniLookup("log_errors")
	if !ok || !iniBool(lv) {
		return
	}
	path, ok := vm.iniLookup("error_log")
	if !ok || path == "" {
		return
	}
	vm.AppendErrorLog(path, "PHP "+kind+":  "+fmt.Sprintf("%s in %s on line %d", msg, file, line))
}

// LastError возвращает снимок последней диагностики для error_get_last().
func (vm *VM) LastError() (level int64, msg, file string, line int, ok bool) {
	return vm.lastErrLevel, vm.lastErrMsg, vm.lastErrFile, vm.lastErrLine, vm.lastErrSet
}

// ClearLastError сбрасывает снимок последней диагностики (error_clear_last).
func (vm *VM) ClearLastError() { vm.lastErrSet = false }

// UserError поднимает диагностику пользовательского уровня (trigger_error).
func (vm *VM) UserError(level int64, msg string) {
	kind := "Warning"
	switch level {
	case E_USER_ERROR:
		kind = "Fatal error"
	case E_USER_NOTICE:
		kind = "Notice"
	case E_USER_DEPRECAT:
		kind = "Deprecated"
	}
	vm.reportDiag(kind, level, msg)
}

// SetErrorHandler устанавливает пользовательский обработчик ошибок и
// возвращает предыдущий (set_error_handler).
func (vm *VM) SetErrorHandler(cb Slot) Slot {
	old := vm.errHandler
	if old.typ == 0 {
		old = MakeNull()
	}
	vm.errHandler = deref(cb)
	return old
}

// SetExceptionHandler устанавливает обработчик непойманных исключений
// (set_exception_handler) и возвращает предыдущий.
func (vm *VM) SetExceptionHandler(cb Slot) Slot {
	old := vm.exceptionHandler
	if old.typ == 0 {
		old = MakeNull()
	}
	vm.exceptionHandler = deref(cb)
	return old
}

// ErrorReportingGet возвращает текущий уровень error_reporting.
func (vm *VM) ErrorReportingGet() int64 { return vm.errReporting }

// ErrorReportingSet устанавливает уровень error_reporting (INI-старт запроса)
// и возвращает прежний. Значение запоминается и как INI-директива: с него
// начинают файберы.
func (vm *VM) ErrorReportingSet(level int64) int64 {
	old := vm.errReporting
	vm.errReporting = level
	vm.errReportingIni = level
	return old
}

// SuppressOn/Off управляют подавлением диагностик (оператор `@`). Zend
// реализует `@` через error_reporting: не-фатальные биты маскируются, а
// END_SILENCE восстанавливает прежнее значение, только если внутри его не
// меняли явным error_reporting() (иначе новое значение сохраняется).
func (vm *VM) SuppressOn() {
	vm.suppressStack = append(vm.suppressStack, vm.errReporting)
	if !onlyFatalErrors(vm.errReporting) {
		vm.errReporting &= eFatalErrors
	}
	vm.suppress = len(vm.suppressStack)
}

func (vm *VM) SuppressOff() {
	n := len(vm.suppressStack)
	if n == 0 {
		return
	}
	saved := vm.suppressStack[n-1]
	vm.suppressStack = vm.suppressStack[:n-1]
	if onlyFatalErrors(vm.errReporting) && !onlyFatalErrors(saved) {
		vm.errReporting = saved
	}
	vm.suppress = len(vm.suppressStack)
}

// ErrorReportingBuiltin — реализация error_reporting(): без аргументов
// возвращает текущий уровень, с аргументом устанавливает и возвращает прежний.
func ErrorReportingBuiltin(ctx *Context) Slot {
	if len(ctx.args) == 0 {
		return MakeInt(ctx.vm.errReporting)
	}
	a := deref(ctx.args[0])
	switch a.typ {
	case IntType, NullType, BoolType, FloatType, StringType:
	default:
		// Zend ZPP: error_reporting(): Argument #1 ($error_level) must be of
		// type ?int, stdClass given (bug72162).
		ctx.Throw(NewThrowable("TypeError",
			"error_reporting(): Argument #1 ($error_level) must be of type ?int, "+
				valueTypeName(ctx.vm, a)+" given"))
		if ctx.HasThrown() {
			return *new(Slot)
		}
		return MakeInt(0)
	}
	old := ctx.vm.errReporting
	ctx.vm.errReporting = a.ToInt()
	// Zend: error_reporting($n) меняет и INI-директиву (PHP_INI_USER) —
	// файберы, стартующие позже, видят новое значение.
	ctx.vm.errReportingIni = ctx.vm.errReporting
	return MakeInt(old)
}

// obFatal печатает фатальную ошибку и прерывает исполнение.
func (vm *VM) obFatal(fn string) {
	file := vm.curFile
	if file == "" {
		file = vm.obFile
	}
	if file == "" {
		file = "php"
	}
	line := vm.line()
	if line <= 0 {
		line = vm.curLine
	}
	fmt.Fprintf(vm.out, "\nFatal error: %s(): Cannot use output buffering in output buffering display handlers in %s on line %d\n", fn, file, line)
	panic(fatalSignal{})
}

// SetFile задаёт имя файла для диагностических сообщений.
func (vm *VM) SetFile(name string) { vm.obFile = name }

// ObInHandler сообщает, выполняется ли сейчас обработчик буфера вывода.
func (vm *VM) ObInHandler() bool { return vm.obInHandler }

// toThrowable приводит ошибку вызова обработчика к *Throwable.
func toThrowable(err error) *Throwable {
	if th, ok := err.(*Throwable); ok {
		return th
	}
	return NewThrowable("Error", err.Error())
}
