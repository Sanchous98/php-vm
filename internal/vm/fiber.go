package vm

import (
	"fmt"
	"math/bits"
	"reflect"
	"strconv"
	"strings"
	"unsafe"
)

// Fiber (PHP 8.1) — кооперативные файберы.
//
// Модель реализации. Тело файбера исполняется в ОТДЕЛЬНОЙ горутине. Пока файбер
// работает, горутина возобновителя заблокирована на канале (не трогает VM), а
// когда файбер вызывает Fiber::suspend() — наоборот. Поскольку в любой момент
// VM-код исполняет ровно одна горутина, гонок нет; передача через каналы даёт
// happens-before.
//
// VM-состояние (кадры, ip, chunk, callStack, tries, scope, ...) — общее для всех
// горутин. Поэтому при каждом переключении сохраняется состояние уходящей
// стороны и восстанавливается состояние приходящей (полный context switch), см.
// fiberExecState / saveExecState / restoreExecState.
//
// Это естественно ложится на рекурсивную по Go-стеку VM: переключение не
// разворачивает стек, а замораживает его.

// ---------------------------------------------------------------------------
// Состояние исполнения VM (снимок для переключения контекста)
// ---------------------------------------------------------------------------

// fiberFrameState — снимок одного кадра. regs хранится отдельным буфером,
// чтобы содержимое не затиралось кадрами другой стороны.
type fiberFrameState struct {
	regs      []Slot
	tracked   [maxRegsHard / 64]uint64
	nTracked  int32
	pc        int
	this      Slot
	self      ClassInfo
	called    ClassInfo
	dynLocals map[string]Slot
	dynOrder  []string
}

// fiberExecState — полный снимок изменяемой части VM, относящейся к текущему
// потоку исполнения. Глобальные таблицы (globals, classes, functions, out,
// liveObjs, error_reporting, ...) намеренно НЕ входят и разделяются.
type fiberExecState struct {
	fp    int
	ip    int
	chunk *Chunk

	frames []fiberFrameState

	callStack []callEntry
	tries     []tryEntry

	ctxThis   Slot
	ctxSelf   ClassInfo
	ctxCalled ClassInfo
	ctxArgs   []Slot

	curFile   string
	curFileID int32
	curLine   int

	suppress int
	// errReporting/suppressStack — состояние `@`-подавления стороны: `@`
	// меняет EG(error_reporting) в Zend, а он часть VM-состояния файбера.
	errReporting  int64
	suppressStack []int64

	retVal      Slot
	retImplicit bool
	retLine     int

	pendingThis Slot

	suppressNextFrame     bool
	markNextFrameInternal bool
	argNoLoc              int

	curFuncArgs     []Slot
	pendingFuncArgs []Slot

	funcParams   int
	funcVariadic bool
	funcNumFixed int
	inFunction   bool

	runningGens  []*Generator
	forceClosing *Generator

	pendingDefaultErr error

	unserState    any
	serState      any
	serializeLock int

	currentFiber *Fiber
}

func copySlots(dst, src []Slot) []Slot {
	if cap(dst) < len(src) {
		dst = make([]Slot, len(src))
	} else {
		dst = dst[:len(src)]
	}
	copy(dst, src)
	return dst
}

// saveExecState копирует текущее изменяемое состояние VM в s.
func (vm *VM) saveExecState(s *fiberExecState) {
	s.fp = vm.fp
	s.ip = vm.ip
	s.chunk = vm.chunk

	n := vm.fp + 1
	if n < 0 {
		n = 0
	}
	for cap(s.frames) < n {
		s.frames = append(s.frames, fiberFrameState{})
	}
	s.frames = s.frames[:n]
	for i := 0; i < n; i++ {
		f := &vm.frames[i]
		sf := &s.frames[i]
		sf.regs = copySlots(sf.regs, f.regs)
		// Учёт ссылок переезжает в сохранённое состояние: владельцем учёта
		// становится ровно одна сторона переключения.
		sf.tracked = f.tracked
		sf.nTracked = f.nTracked
		f.tracked = [maxRegsHard / 64]uint64{}
		f.nTracked = 0
		sf.pc = f.pc
		sf.this = f.this
		sf.self = f.self
		sf.called = f.called
		if f.dynLocals != nil {
			m := make(map[string]Slot, len(f.dynLocals))
			for k, v := range f.dynLocals {
				m[k] = v
			}
			sf.dynLocals = m
		} else {
			sf.dynLocals = nil
		}
		sf.dynOrder = append(sf.dynOrder[:0], f.dynOrder...)
	}

	s.callStack = cloneCallStack(s.callStack, vm.callStack)
	s.tries = append(s.tries[:0], vm.tries...)

	s.ctxThis, s.ctxSelf, s.ctxCalled = vm.ctx.this, vm.ctx.self, vm.ctx.called
	s.ctxArgs = copySlots(s.ctxArgs, vm.ctx.args)

	s.curFile, s.curFileID, s.curLine = vm.curFile, vm.curFileID, vm.curLine
	s.suppress = vm.suppress
	s.errReporting = vm.errReporting
	s.suppressStack = append(s.suppressStack[:0], vm.suppressStack...)

	s.retVal = vm.retVal
	s.retImplicit = vm.retImplicit
	s.retLine = vm.retLine
	s.pendingThis = vm.pendingThis

	s.suppressNextFrame = vm.suppressNextFrame
	s.markNextFrameInternal = vm.markNextFrameInternal
	s.argNoLoc = vm.argNoLoc

	s.curFuncArgs = copySlots(s.curFuncArgs, vm.curFuncArgs)
	s.pendingFuncArgs = copySlots(s.pendingFuncArgs, vm.pendingFuncArgs)

	s.funcParams, s.funcVariadic, s.funcNumFixed, s.inFunction =
		vm.funcParams, vm.funcVariadic, vm.funcNumFixed, vm.inFunction

	s.runningGens = append(s.runningGens[:0], vm.runningGens...)
	s.forceClosing = vm.forceClosing

	s.pendingDefaultErr = vm.pendingDefaultErr
	s.unserState = vm.unserState
	s.serState = vm.serState
	s.serializeLock = vm.serializeLock
	s.currentFiber = vm.currentFiber
}

// restoreExecState восстанавливает состояние VM из s. Кадры копируются в уже
// существующие буферы кадров (их нельзя переприсваивать: на Go-стеке у exec
// есть локальная ссылка regs), кроме случая нехватки длины.
func (vm *VM) restoreExecState(s *fiberExecState) {
	vm.fp = s.fp
	vm.ip = s.ip
	vm.chunk = s.chunk

	for i := 0; i < len(s.frames); i++ {
		sf := &s.frames[i]
		f := &vm.frames[i]
		if len(f.regs) < len(sf.regs) {
			f.regs = make([]Slot, len(sf.regs))
		}
		f.regs = f.regs[:len(sf.regs)]
		copy(f.regs, sf.regs)
		f.tracked = sf.tracked
		f.nTracked = sf.nTracked
		f.pc = sf.pc
		f.this = sf.this
		f.self = sf.self
		f.called = sf.called
		f.dynLocals = sf.dynLocals
		f.dynOrder = append(f.dynOrder[:0], sf.dynOrder...)
	}
	if vm.fp < 0 {
		vm.fp = 0
	}
	if vm.fp >= maxFrames {
		vm.fp = maxFrames - 1
	}
	vm.cur = &vm.frames[vm.fp]

	// callStack копируется в собственный массив: срез-вид на saved-состояние
	// делил бы буфер с файбером, и последующие push после всплытия ниже len
	// перезаписывали бы «снимок» файбера (запись __construct в resumer
	// удерживала объект живым — markRoots видел его как корень).
	vm.callStack = cloneCallStack(nil, s.callStack)
	if cap(vm.tries) < len(s.tries) {
		vm.tries = make([]tryEntry, len(s.tries))
	}
	vm.tries = vm.tries[:len(s.tries)]
	copy(vm.tries, s.tries)

	vm.ctx.this, vm.ctx.self, vm.ctx.called = s.ctxThis, s.ctxSelf, s.ctxCalled
	vm.ctx.args = s.ctxArgs[:len(s.ctxArgs):len(s.ctxArgs)]

	vm.curFile, vm.curFileID, vm.curLine = s.curFile, s.curFileID, s.curLine
	vm.suppress = s.suppress
	vm.errReporting = s.errReporting
	vm.suppressStack = append(vm.suppressStack[:0], s.suppressStack...)

	vm.retVal = s.retVal
	vm.retImplicit = s.retImplicit
	vm.retLine = s.retLine
	vm.pendingThis = s.pendingThis

	vm.suppressNextFrame = s.suppressNextFrame
	vm.markNextFrameInternal = s.markNextFrameInternal
	vm.argNoLoc = s.argNoLoc

	vm.curFuncArgs = s.curFuncArgs[:len(s.curFuncArgs):len(s.curFuncArgs)]
	vm.pendingFuncArgs = s.pendingFuncArgs[:len(s.pendingFuncArgs):len(s.pendingFuncArgs)]

	vm.funcParams, vm.funcVariadic, vm.funcNumFixed, vm.inFunction =
		s.funcParams, s.funcVariadic, s.funcNumFixed, s.inFunction

	if cap(vm.runningGens) < len(s.runningGens) {
		vm.runningGens = make([]*Generator, len(s.runningGens))
	}
	vm.runningGens = vm.runningGens[:len(s.runningGens):len(s.runningGens)]
	copy(vm.runningGens, s.runningGens)
	vm.forceClosing = s.forceClosing

	vm.pendingDefaultErr = s.pendingDefaultErr
	vm.unserState = s.unserState
	vm.serState = s.serState
	vm.serializeLock = s.serializeLock
	vm.currentFiber = s.currentFiber
}

// traceFrames возвращает полный стек вызовов с учётом цепочки возобновителей,
// если исполнение идёт внутри файбера. Порядок среза — от внешнего к внутреннему:
//
//	[resumer outermost ... resumer resume-frame] ++ [fiber closure ... current]
func (vm *VM) traceFrames() []callFrameInfo {
	base := vm.resolveStack()
	if vm.currentFiber == nil {
		return base
	}
	rt := vm.currentFiber.resumerTrace
	if len(rt) == 0 {
		return base
	}
	out := make([]callFrameInfo, 0, len(rt)+len(base))
	out = append(out, rt...)
	out = append(out, base...)
	return out
}

// resolveStack раскрывает компактный стек вызовов в callFrameInfo, вычисляя
// строки. Вызывается только при запросе трейса.
func (vm *VM) resolveStack() []callFrameInfo {
	n := len(vm.callStack)
	if n == 0 {
		return nil
	}
	out := make([]callFrameInfo, 0, n)
	// Живые регистры кадра читаются только по индексу, записанному вызовом
	// при входе в функцию (callEntry.frameIdx): у вызовов, упавших на проверке
	// арности/типов, кадра нет, и позиционное сопоставление подставляло бы
	// регистры вызывающего (attributes/030).
	constExprSeen := false
	for i := range vm.callStack {
		e := &vm.callStack[i]
		// Вложенное вычисление константного выражения не создаёт новый кадр:
		// Zend показывает один кадр «[constant expression]» с местом внешнего
		// обращения (bug41633_3: цепочка self-ссылок даёт #0 … [constant
		// expression]() и #1 {main}).
		if e.callee != nil && e.callee.Name == "[constant expression]" {
			if constExprSeen {
				continue
			}
			constExprSeen = true
		}
		if e.callerChunk != 0 && len((*Chunk)(unsafe.Pointer(e.callerChunk)).inlineRegions) > 0 {
			out = vm.appendInlineFrames(out, e)
		}
		out = append(out, vm.infoAt(e, int(e.frameIdx)))
	}
	return out
}

// includeFrameFn — дескриптор кадра include/require для синтетических кадров
// вклеенных include (Zend исполняет подключаемый файл отдельным кадром).
var includeFrameFns = map[string]*Function{}

func includeFrameFn(name string) *Function {
	if fn, ok := includeFrameFns[name]; ok {
		return fn
	}
	if name == "" {
		name = "include"
	}
	// Executable — пустая нативная функция: кадр не должен считаться
	// userland-вызовом при выравнивании кадров в markRoots.
	fn := &Function{Name: name, Executable: &NativeFunction{fn: func(*Context, []Slot) (Slot, error) {
		return Slot{}, nil
	}}}
	includeFrameFns[name] = fn
	return fn
}

// appendInlineFrames добавляет синтетические кадры include/require, вклеенных
// на этапе компиляции: у кадра вызова из подключаемого файла позиция вызова
// лежит внутри inline-региона чанка, и Zend показал бы между этим кадром и
// вызывающим кадром конструкции require. Кадры добавляются от внешнего
// региона к внутреннему (порядок out — «от main к самым глубоким»).
func (vm *VM) appendInlineFrames(out []callFrameInfo, e *callEntry) []callFrameInfo {
	c := (*Chunk)(unsafe.Pointer(e.callerChunk))
	idx := c.inlineRegionAt(int(e.callerPC &^ objMethodBit))
	if idx < 0 {
		return out
	}
	var chain []int32
	for i := idx; i >= 0; i = c.inlineRegions[i].parent {
		chain = append(chain, i)
	}
	for i := len(chain) - 1; i >= 0; i-- {
		r := &c.inlineRegions[chain[i]]
		out = append(out, callFrameInfo{
			callee:       includeFrameFn(r.callName),
			callerFile:   r.callFile,
			callerLine:   r.callLine,
			args:         []Slot{MakeString(r.file)},
			includeFrame: true,
		})
	}
	return out
}

// cloneCallStack сохраняет компактный стек вызовов, копируя args (они ссылаются
// на регистры кадров, которые будут переиспользованы другой стороной при
// переключении контекста файбера).
func cloneCallStack(dst, src []callEntry) []callEntry {
	if cap(dst) < len(src) {
		dst = make([]callEntry, len(src))
	} else {
		dst = dst[:len(src)]
	}
	for i := range src {
		e := src[i]
		if len(e.args) > 0 {
			e.args = append([]Slot(nil), e.args...)
		}
		dst[i] = e
	}
	return dst
}

// ---------------------------------------------------------------------------
// Fiber — состояние файбера
// ---------------------------------------------------------------------------

type fiberMsgKind int

const (
	fbResume fiberMsgKind = iota // возобновить (передать значение)
	fbThrow                      // бросить исключение в точке suspend
	fbClose                      // принудительно закрыть (finally)
	fbYield                      // файбер -> возобновитель: suspend
	fbDone                       // файбер -> возобновитель: завершился
)

type fiberMsg struct {
	kind fiberMsgKind
	val  Slot
	exc  *Throwable
	err  error
}

// Fiber — нативный объект класса "Fiber". Встраивает Object первым полем.
type Fiber struct {
	Object

	callable    Slot
	constructed bool

	started    bool
	suspended  bool
	running    bool
	terminated bool
	wasThrew   bool
	wasFatal   bool

	ret Slot

	resumeCh chan fiberMsg
	yieldCh  chan fiberMsg

	saved   fiberExecState
	resumer fiberExecState

	// resumerTrace — снимок стека возобновителя на момент последнего
	// возобновления. Добавляется к трейсам, снятым внутри файбера.
	resumerTrace []callFrameInfo

	forceClose bool
	closing    bool
}

// SetFiberStackSize задаёт INI fiber.stack_size (для проверки при старте).
func (vm *VM) SetFiberStackSize(s string) {
	vm.fiberStackSize = s
	vm.fiberStackSizeSet = true
}

// fiberStackTooSmall сообщает, что заданный fiber.stack_size некорректен.
const minFiberStackSize = 16384

func (vm *VM) fiberStackTooSmall() bool {
	if !vm.fiberStackSizeSet {
		return false
	}
	n, err := strconv.Atoi(strings.TrimSpace(vm.fiberStackSize))
	if err != nil {
		return true
	}
	return n < minFiberStackSize
}

func fiberErrorMessage(msg string) *Throwable { return NewThrowable("FiberError", msg) }

// ---------------------------------------------------------------------------
// Переключение контекста
// ---------------------------------------------------------------------------

// fiberSwitch передаёт управление файберу f с сообщением msg, дожидается
// ответа (suspend/завершение) и возвращает его.
func (vm *VM) fiberSwitch(f *Fiber, msg fiberMsg) fiberMsg {
	vm.saveExecState(&f.resumer)
	f.resumerTrace = deepCopyFrames(vm.traceFrames())
	f.resumeCh <- msg
	out := <-f.yieldCh
	vm.restoreExecState(&f.resumer)
	return out
}

// fiberMain — тело горутины файбера.
func (vm *VM) fiberMain(f *Fiber, args []Slot) {
	<-f.resumeCh // ждём start

	vm.freshExecState()
	vm.currentFiber = f
	f.running = true

	res, err := vm.CallCallable(&vm.ctx, f.callable, args)

	f.running = false
	f.terminated = true
	f.suspended = false
	f.ret = res

	// Стек файбера разрушается в его собственном контексте (Zend:
	// zend_vm_stack_destroy перед финальным переключением). Отложенные
	// деструкторы локальных объектов обязаны выполниться здесь, пока
	// vm.currentFiber == f: иначе __destruct увидел бы главный контекст, и
	// Fiber::suspend() дал бы "Cannot suspend outside of a fiber" вместо
	// "Cannot switch fibers in current execution context"
	// (Zend/tests/fibers/no-switch-dtor-suspend).
	// Возвращаемое значение уже сохранено в f.ret (корень через объект
	// файбера): снятые кадры не должны уронить его раньше времени
	// (gc-cycle-result).
	if ferr := vm.flushFiberTeardown(); ferr != nil && err == nil {
		err = ferr
	}

	if err != nil {
		if th, ok := asThrowable(err); ok {
			f.wasThrew = true
			f.yieldCh <- fiberMsg{kind: fbDone, val: res, exc: th, err: err}
			return
		}
		f.yieldCh <- fiberMsg{kind: fbDone, val: res, err: err}
		return
	}
	f.yieldCh <- fiberMsg{kind: fbDone, val: res}
}

// walkFiberExecStateOwned обходит слоты состояния исполнения файбера, которые
// оно удерживает как владелец. Регистры кадров берутся по карте учёта
// (tracked): незанятые слоты буфера кадра содержат остатки предыдущего
// использования (restore перезаписывает регистры целиком) и владением не
// являются — иначе файбер видел бы ложную входящую ссылку на себя
// (Zend/tests/fibers/suspend-in-force-close-fiber-catching-exception).
// Остальные поля обходятся консервативно, как в walkReflect.
func walkFiberExecStateOwned(s *fiberExecState, fn func(Slot)) {
	for i := range s.frames {
		sf := &s.frames[i]
		for w := range sf.tracked {
			mask := sf.tracked[w]
			for mask != 0 {
				j := w*64 + bits.TrailingZeros64(mask)
				mask &= mask - 1
				if j < len(sf.regs) {
					fn(sf.regs[j])
				}
			}
		}
		for _, v := range sf.dynLocals {
			fn(v)
		}
		fn(sf.this)
	}
	fn(s.ctxThis)
	for _, v := range s.ctxArgs {
		fn(v)
	}
	fn(s.retVal)
	fn(s.pendingThis)
	for _, v := range s.curFuncArgs {
		fn(v)
	}
	for _, v := range s.pendingFuncArgs {
		fn(v)
	}
	for i := range s.callStack {
		e := &s.callStack[i]
		fn(e.this)
		for _, a := range e.args {
			fn(a)
		}
	}
	walkReflect(reflect.ValueOf(&s.tries).Elem(), 0, fn)
	walkReflect(reflect.ValueOf(&s.pendingDefaultErr).Elem(), 0, fn)
	walkReflect(reflect.ValueOf(&s.unserState).Elem(), 0, fn)
	walkReflect(reflect.ValueOf(&s.serState).Elem(), 0, fn)
}

// flushFiberTeardown выполняет отложенные потери ссылок в контексте
// завершающегося файбера. Вызывается из fiberMain, пока vm.currentFiber == f и
// стек файбера ещё «текущий»: Zend разрушает кадры файбера (и запускает
// __destruct его локальных объектов) до финального переключения контекста.
func (vm *VM) flushFiberTeardown() error {
	if vm.sweepBusy {
		return nil
	}
	if !vm.lifeCandidate && vm.zeroObjs <= 0 && !vm.resDropPending {
		return nil
	}
	// Кадры __destruct, запущенных разрушением стека файбера, не имеют
	// позиции в коде (Zend: zend_fiber_cleanup → zend_vm_stack_destroy).
	savedInternal := vm.dtorInternal
	vm.dtorInternal = true
	out := vm.sweepLifetime(false, vm.chunk, vm.ip-1)
	vm.dtorInternal = savedInternal
	return out.err
}

// freshExecState сбрасывает изменяемое состояние VM в «чистое» (для старта
// файбера). Глобальные таблицы не трогаются.
func (vm *VM) freshExecState() {
	vm.fp = 0
	vm.ip = 0
	vm.chunk = nil
	vm.clearRegs(&vm.frames[0])
	vm.cur = &vm.frames[0]
	vm.callStack = nil
	vm.tries = vm.tries[:0]
	vm.ctx.this = Slot{}
	vm.ctx.self = nil
	vm.ctx.called = nil
	vm.ctx.args = nil
	// Файбер стартует с уровнем INI-директивы error_reporting, а не с
	// текущим: `@` и error_reporting() вызывающего в него не протекают
	// (Zend: zend_fiber_execute).
	vm.suppress = 0
	vm.suppressStack = nil
	vm.errReporting = vm.errReportingIni
	vm.retVal = Slot{}
	vm.retImplicit = false
	vm.retLine = 0
	vm.pendingThis = Slot{}
	vm.suppressNextFrame = false
	vm.markNextFrameInternal = false
	vm.argNoLoc = 0
	vm.curFuncArgs = nil
	vm.pendingFuncArgs = nil
	vm.funcParams, vm.funcVariadic, vm.funcNumFixed, vm.inFunction = 0, false, 0, false
	vm.runningGens = vm.runningGens[:0]
	vm.forceClosing = nil
	vm.pendingDefaultErr = nil
	vm.unserState = nil
	vm.serState = nil
	vm.serializeLock = 0
}

// ---------------------------------------------------------------------------
// Fiber::suspend()
// ---------------------------------------------------------------------------

// fiberSuspendYield приостанавливает текущий файбер, передавая значение val
// возобновителю. Возвращает значение, переданное в resume (или err при throw).
func fiberSuspendYield(vm *VM, val Slot) (Slot, error) {
	f := vm.currentFiber
	if f == nil {
		return Slot{}, fiberErrorMessage("Cannot suspend outside of a fiber")
	}
	if f.forceClose {
		return Slot{}, fiberErrorMessage("Cannot suspend in a force-closed fiber")
	}
	if vm.noSwitch || vm.forceClosingFiber != nil {
		return Slot{}, fiberErrorMessage("Cannot switch fibers in current execution context")
	}

	vm.saveExecState(&f.saved)
	f.suspended = true
	f.running = false
	f.yieldCh <- fiberMsg{kind: fbYield, val: val}
	in := <-f.resumeCh
	vm.restoreExecState(&f.saved)
	vm.currentFiber = f
	f.suspended = false
	f.running = true

	switch in.kind {
	case fbThrow:
		return Slot{}, in.exc
	case fbClose:
		vm.forceClosingFiber = f
		f.forceClose = true
		return Slot{}, errForceClose
	default:
		return in.val, nil
	}
}

// ---------------------------------------------------------------------------
// Методы файбера
// ---------------------------------------------------------------------------

func fiberStartFn(vm *VM, f *Fiber, args []Slot) (Slot, error) {
	if vm.noSwitch || vm.forceClosingFiber != nil {
		return Slot{}, fiberErrorMessage("Cannot switch fibers in current execution context")
	}
	if f == nil || f.started {
		return Slot{}, fiberErrorMessage("Cannot start a fiber that has already been started")
	}
	if !f.constructed || deref(f.callable).typ == NullType {
		return Slot{}, fiberErrorMessage("Cannot start a fiber that has already been started")
	}
	if vm.fiberStackTooSmall() {
		return Slot{}, NewThrowable("Exception", fmt.Sprintf("Fiber stack size is too small, it needs to be at least %d bytes", minFiberStackSize))
	}
	f.resumeCh = make(chan fiberMsg, 1)
	f.yieldCh = make(chan fiberMsg, 1)
	f.started = true
	f.suspended = false
	f.terminated = false
	cp := append([]Slot(nil), args...)
	go vm.fiberMain(f, cp)
	out := vm.fiberSwitch(f, fiberMsg{kind: fbResume})
	return fiberSwitchResult(out)
}

func fiberResumeFn(vm *VM, f *Fiber, val Slot) (Slot, error) {
	if vm.noSwitch || vm.forceClosingFiber != nil {
		return Slot{}, fiberErrorMessage("Cannot switch fibers in current execution context")
	}
	if f == nil || !f.suspended {
		return Slot{}, fiberErrorMessage("Cannot resume a fiber that is not suspended")
	}
	out := vm.fiberSwitch(f, fiberMsg{kind: fbResume, val: val})
	return fiberSwitchResult(out)
}

func fiberThrowFn(vm *VM, f *Fiber, th *Throwable) (Slot, error) {
	if vm.noSwitch || vm.forceClosingFiber != nil {
		return Slot{}, fiberErrorMessage("Cannot switch fibers in current execution context")
	}
	if f == nil || !f.suspended {
		return Slot{}, fiberErrorMessage("Cannot resume a fiber that is not suspended")
	}
	out := vm.fiberSwitch(f, fiberMsg{kind: fbThrow, exc: th})
	return fiberSwitchResult(out)
}

// fiberSwitchResult превращает ответ файбера в результат start/resume/throw:
// при завершении — null либо ошибку, при suspend — переданное значение.
func fiberSwitchResult(out fiberMsg) (Slot, error) {
	switch out.kind {
	case fbDone:
		if out.err != nil {
			return Slot{}, out.err
		}
		return MakeNull(), nil
	default:
		return out.val, nil
	}
}

func fiberGetReturnFn(f *Fiber) (Slot, error) {
	if f == nil || !f.started {
		return Slot{}, fiberErrorMessage("Cannot get fiber return value: The fiber has not been started")
	}
	if !f.terminated {
		return Slot{}, fiberErrorMessage("Cannot get fiber return value: The fiber has not returned")
	}
	if f.wasThrew {
		return Slot{}, fiberErrorMessage("Cannot get fiber return value: The fiber threw an exception")
	}
	if f.wasFatal {
		return Slot{}, fiberErrorMessage("Cannot get fiber return value: The fiber exited with a fatal error")
	}
	return f.ret, nil
}

func fiberGetCurrentFn(vm *VM) Slot {
	if vm.currentFiber == nil {
		return MakeNull()
	}
	return vm.slotOf(&vm.currentFiber.Object)
}

// ---------------------------------------------------------------------------
// Принудительное закрытие
// ---------------------------------------------------------------------------

// CloseIfFiber закрывает незавершённый (приостановленный) файбер в слоте:
// прогоняет его finally-блоки. Возвращает ошибку, поднятую из finally.
func (vm *VM) CloseIfFiber(s Slot) error {
	if deref(s).typ != ObjectType || vm.fiberClass == nil {
		return nil
	}
	f := objectAs[Fiber](vm.objectOf(s), vm.fiberClass)
	if f == nil {
		return nil
	}
	return vm.closeFiber(f, false)
}

// closeFiber принудительно завершает приостановленный файбер.
func (vm *VM) closeFiber(f *Fiber, atShutdown bool) error {
	if f == nil || f.terminated || !f.started || f.closing {
		return nil
	}
	// Закрываем только «настоящую» приостановку (файбер ждёт возобновления в
	// Fiber::suspend). Файбер, заблокированный в старте вложенного, закрыть
	// здесь нельзя: его стек занят другим файбером.
	if !f.suspended {
		return nil
	}
	f.closing = true
	defer func() { f.closing = false }()

	savedForceClosing := vm.forceClosingFiber
	vm.saveExecState(&f.resumer)
	f.resumerTrace = deepCopyFrames(vm.traceFrames())
	f.resumeCh <- fiberMsg{kind: fbClose}
	out := <-f.yieldCh
	vm.restoreExecState(&f.resumer)
	vm.forceClosingFiber = savedForceClosing

	if out.err == nil || out.err == errForceClose {
		return nil
	}
	return out.err
}

// ---------------------------------------------------------------------------
// Регистрация класса
// ---------------------------------------------------------------------------

// newFiberClass создаёт дескриптор класса Fiber.
func newFiberClass() *runtimeClass {
	ci := RegisterClass[Fiber]("Fiber", nil)
	ci.SetFinal()
	ci.SetNotSerializable()
	ci.SetNoDynamicProps()

	ctorImpl := func(ctx *Context, args []Slot) (Slot, error) {
		f := ctx.This[Fiber]()
		if f == nil || f.constructed {
			return Slot{}, fiberErrorMessage("Cannot call constructor twice")
		}
		if len(args) < 1 {
			return Slot{}, NewThrowable("ArgumentCountError", "Fiber::__construct() expects exactly 1 argument, 0 given")
		}
		if !ctx.vm.IsCallable(deref(args[0])) {
			return Slot{}, NewThrowable("TypeError", "Fiber::__construct(): Argument #1 ($callback) must be a valid callback")
		}
		f.callable = args[0]
		f.constructed = true
		return MakeNull(), nil
	}
	ctorFn := &Function{
		Name:        "__construct",
		NumArgs:     1,
		NumRequired: 1,
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			res, err := ctorImpl(ctx, args)
			if err != nil {
				if th, ok := err.(*Throwable); ok && ctx.vm != nil {
					ctx.vm.fillThrowableTrace(th)
				}
			}
			return res, err
		}},
	}
	ci.Constructor(ctorFn)
	ci.Method("__construct", ctorFn)

	fiberMethod := func(name string, numArgs int, fn func(vm *VM, f *Fiber, args []Slot) (Slot, error)) {
		ci.Method(name, &Function{
			Name:    name,
			NumArgs: numArgs,
			Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
				res, err := fn(ctx.vm, ctx.This[Fiber](), args)
				if err != nil {
					if th, ok := err.(*Throwable); ok && ctx.vm != nil {
						ctx.vm.fillThrowableTrace(th)
					}
				}
				return res, err
			}},
		})
	}

	fiberMethod("start", -1, func(vm *VM, f *Fiber, args []Slot) (Slot, error) {
		return fiberStartFn(vm, f, args)
	})
	fiberMethod("resume", 1, func(vm *VM, f *Fiber, args []Slot) (Slot, error) {
		var v Slot
		if len(args) > 0 {
			v = args[0]
		} else {
			v = MakeNull()
		}
		return fiberResumeFn(vm, f, v)
	})
	fiberMethod("throw", 1, func(vm *VM, f *Fiber, args []Slot) (Slot, error) {
		if len(args) < 1 {
			return Slot{}, NewThrowable("ArgumentCountError", "Fiber::throw() expects exactly 1 argument, 0 given")
		}
		if deref(args[0]).typ != ObjectType {
			return Slot{}, NewThrowable("TypeError", "Fiber::throw(): Argument #1 ($exception) must be of type Throwable")
		}
		ci := vm.classInfo(args[0])
		if ci == nil || !ci.isA(vm.findClass("Throwable")) {
			return Slot{}, NewThrowable("TypeError", "Fiber::throw(): Argument #1 ($exception) must be of type Throwable")
		}
		th := vm.makeThrowable(args[0]).(*Throwable)
		return fiberThrowFn(vm, f, th)
	})
	fiberMethod("isStarted", 0, func(_ *VM, f *Fiber, _ []Slot) (Slot, error) {
		return MakeBool(f != nil && f.started), nil
	})
	fiberMethod("isRunning", 0, func(_ *VM, f *Fiber, _ []Slot) (Slot, error) {
		return MakeBool(f != nil && f.started && !f.suspended && !f.terminated), nil
	})
	fiberMethod("isSuspended", 0, func(_ *VM, f *Fiber, _ []Slot) (Slot, error) {
		return MakeBool(f != nil && f.suspended), nil
	})
	fiberMethod("isTerminated", 0, func(_ *VM, f *Fiber, _ []Slot) (Slot, error) {
		return MakeBool(f != nil && f.terminated), nil
	})
	fiberMethod("getReturn", 0, func(_ *VM, f *Fiber, _ []Slot) (Slot, error) {
		return fiberGetReturnFn(f)
	})

	ci.Method("suspend", &Function{
		Name:     "suspend",
		NumArgs:  -1,
		IsStatic: true,
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			var val Slot
			if len(args) > 0 {
				val = args[0]
			} else {
				val = MakeNull()
			}
			res, err := fiberSuspendYield(ctx.vm, val)
			if err != nil {
				if th, ok := err.(*Throwable); ok && ctx.vm != nil {
					ctx.vm.fillThrowableTrace(th)
				}
			}
			return res, err
		}},
	})
	ci.Method("getCurrent", &Function{
		Name:     "getCurrent",
		NumArgs:  0,
		IsStatic: true,
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			return fiberGetCurrentFn(ctx.vm), nil
		}},
	})

	return ci
}
