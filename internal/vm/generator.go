package vm

// Генераторы PHP (yield / yield from) — ЛЕНИВАЯ модель исполнения.
//
// При вызове функции-генератора тело НЕ исполняется: создаётся объект
// Generator со свежим кадром (аргументы разложены в регистры), started=false.
// При первом обращении (current/valid/key/next/send/rewind/foreach) кадр
// восстанавливается и exec исполняет тело РОВНО до первого OpYield. На OpYield
// кадр (регистры + ip + scope + активные try) сохраняется в Generator, а exec
// возвращает yieldSignal. Следующий advance проталкивает сохранённый кадр
// обратно, кладёт send-значение в регистр результата yield-выражения и
// продолжает exec с того же места. Никакого предвычисления и горутин.
//
// Generator — нативный объект класса "Generator" (реализует Iterator):
// структура Generator встраивает Object. get_class/instanceof работают через
// обычный механизм классов.
//
// Ограничение: при уничтожении незавершённого генератора блоки finally НЕ
// исполняются (в Zend генератор «закрывается» и прогоняет finally). Это
// задокументированное упрощение.

// YieldNoKey — маркер «нет явного ключа» в поле B инструкции OpYield (тогда
// ключ авто-инкрементный). Регистры 0..255, так что 0xFF свободен как маркер.
const YieldNoKey = 0xFF

// YieldFromBit — флаг в поле C инструкции OpYield: yield сгенерирован
// разворачиванием `yield from`. Такие yield не сдвигают авто-ключ внешнего
// генератора (ключи берутся у внутреннего итерируемого).
const YieldFromBit = 1 << 8

// yieldSignal — сигнал приостановки генератора на OpYield. Возвращается из
// exec и перехватывается в advance.
type yieldSignal struct {
	val       Slot
	key       Slot
	hasKey    bool
	from      bool
	resumeReg int
}

func (yieldSignal) Error() string { return "phpvm: yield" }

// Generator — состояние ленивого генератора. Встраивает Object первым полем,
// поэтому объектный слот класса Generator указывает одновременно на шапку и на
// эти данные.
type Generator struct {
	Object
	chunk *Chunk
	// fn — функция-генератор (для кадров stack trace при resume).
	fn *Function
	// args — аргументы, с которыми был создан генератор (для func_get_args и
	// кадров stack trace). Копия, чтобы не зависеть от регистров вызывающего.
	args []Slot

	// Кадр, сохранённый на момент последнего yield (или исходный для
	// неначатого генератора). Восстанавливается в реальный кадр VM в advance.
	// Срез — собственный буфер генератора (не алиасит пул кадров); состояние
	// переносится копированием.
	regs []Slot
	// gTracked — карта учёта ссылок регистров кадра: при возобновлении
	// переносится в кадр, при yield — обратно. Владельцем учёта всегда
	// является ровно одно из двух мест.
	gTracked  [maxRegsHard / 64]uint64
	gNTracked int32
	pc        int
	this      Slot
	self      ClassInfo
	called    ClassInfo

	started  bool // был хотя бы один advance
	done     bool // тело завершилось (return/конец/неперехваченное исключение)
	aborted  bool // завершилось из-за необработанного исключения
	advanced bool // вызывали next/send/throw — rewind после этого запрещён
	hasCur   bool // приостановлен на yield (current/valid актуальны)
	running  bool // тело исполняется прямо сейчас (защита от re-entry)

	ret Slot // значение return

	cur    Slot
	curKey Slot

	// autoKey — очередной авто-инкрементный ключ (аналог largest+1 в Zend).
	autoKey int64

	// resumeReg — регистр, куда при следующем resume попадёт send-значение.
	// -1, если генератор ещё не доходил до yield.
	resumeReg int

	// tries — активные обработчики try тела на момент yield. Хранятся без
	// привязки к fp: при resume перенумеровываются на актуальный fp.
	tries []tryEntry

	// forceClose — генератор принудительно закрывается (unset/GC): yield в
	// этом режиме запрещён ("Cannot yield from finally...").
	forceClose bool

	// inner — генератор, из которого сейчас выполняется `yield from` (для
	// каскадного закрытия при force-close вложенных генераторов).
	inner *Generator

	// delegating — генератор приостановлен на yield, порождённом `yield from`
	// по генератору. В этом состоянии current/key/valid/next делегируются
	// внутреннему генератору без возобновления собственного кадра (модель
	// Zend zend_generator_yield_from): так корректно работает совместное
	// использование одного генератора несколькими yield from.
	delegating bool

	// pendingThrow — исключение, которое нужно бросить во внешний генератор
	// при resume вида resumeThrowErr (переброс из вложенного yield from).
	pendingThrow *Throwable

	// doInit — аналог ZEND_GENERATOR_DO_INIT: генератор только что начал
	// делегировать (yield from по генератору), и первый resume, если у
	// текущего генератора уже есть значение, не продвигает цепочку
	// (иначе yield from «съел» бы первый элемент — backtrace_multi_yield_from).
	doInit bool
}

// SetCurrentYieldFromInner запоминает внутренний генератор текущего yield from.
func (vm *VM) SetCurrentYieldFromInner(s Slot) {
	if n := len(vm.runningGens); n > 0 {
		vm.runningGens[n-1].inner = vm.genOfSlot(s)
	}
}

// dropSavedRegs списывает учтённые ссылки сохранённого кадра генератора
// (аналог dropFrame для кадра) и освобождает его слоты. Вызывается, когда
// кадр генератора уничтожается: закрытие незавершённого генератора или
// завершение тела (Zend zend_generator_close/zend_free_compiled_variables) —
// локальные переменные освобождаются, деструкторы объектов срабатывают в этой
// точке (bug71013, bug71601: ссылка на вложенный генератор отпускается).
func (vm *VM) dropSavedRegs(g *Generator) {
	if g == nil {
		return
	}
	if g.gNTracked > 0 {
		var fr Frame
		fr.regs = g.regs
		fr.tracked = g.gTracked
		fr.nTracked = g.gNTracked
		vm.dropFrame(&fr)
	}
	g.gTracked = [maxRegsHard / 64]uint64{}
	g.gNTracked = 0
	for i := range g.regs {
		g.regs[i] = Slot{}
	}
	g.regs = nil
	// Zend освобождает локальные переменные кадра немедленно
	// (zend_free_compiled_variables -> zval_ptr_dtor): деструкторы тел,
	// брошенных исключением или завершившихся, должны сработать здесь.
	if vm.lifeCandidate {
		if err := vm.FlushLifetime(); err != nil && vm.pendingDestroyErr == nil {
			// Ошибка деструктора локальной переменной закрываемого кадра:
			// Zend поднимает её как текущее исключение (previous — исходное)
			// в момент уничтожения кадра (gh14387).
			vm.pendingDestroyErr = err
		}
	}
}

// releaseInnerRef отпускает ссылку на вложенный генератор (yield from),
// хранимую полем inner и регистрами сохранённого кадра. После этого
// неиспользуемый вложенный генератор уничтожается (его finally исполняется
// раньше finally внешнего — Zend zend_generator_dtor_storage).
func (vm *VM) releaseInnerRef(g *Generator) {
	if g == nil || g.inner == nil || g.inner == g {
		return
	}
	inner := g.inner
	target := &inner.Object
	g.inner = nil
	g.delegating = false
	// Ссылки из сохранённого кадра на вложенный генератор: счётчик учитывает
	// только те объекты, чьи ссылки регистрировались (refs > 0). При refs == 0
	// учёт не велся (вложенный генератор пришёл извне — напр. аргументом),
	// такой генератор НЕ закрываем: его может держать вызывающий код
	// (repeated_yield_from_with_immediate_release).
	tracked := target.Refs() > 0
	for r := range g.regs {
		d := deref(g.regs[r])
		if d.typ != ObjectType || vm.objectOf(d) != target {
			continue
		}
		if g.gTracked[r/64]&(uint64(1)<<uint(r%64)) != 0 {
			g.gTracked[r/64] &^= uint64(1) << uint(r%64)
			if g.gNTracked > 0 {
				g.gNTracked--
			}
		}
		if tracked {
			vm.decRefs(g.regs[r], 0)
		}
		g.regs[r] = Slot{}
		if vm.cur != nil && r < len(vm.cur.regs) {
			if d2 := deref(vm.cur.regs[r]); d2.typ == ObjectType && vm.objectOf(d2) == target {
				vm.cur.regs[r] = Slot{}
			}
		}
	}
	if tracked && target.Refs() <= 0 && !target.dead {
		// Последняя ссылка снята: закрываем вложенный генератор сейчас, его
		// finally исполняется раньше finally внешнего (Zend
		// zend_generator_dtor_storage: OBJ_RELEASE(node.parent)).
		_ = vm.closeGenerator(inner, false)
	}
}

// errForceClose — внутренний сигнал принудительного закрытия генератора:
// разворачивает finally-блоки, не будучи пойманным catch.
var errForceClose = &Throwable{close: true}

// closeDispatchErr возвращает эффективную ошибку после неуспешной раскрутки
// сигнала закрытия генератора: реальное исключение, поднятое из отложенных
// finally-слотов внешних try (bug71604), либо nil при штатном закрытии.
func (vm *VM) closeDispatchErr() error {
	if v := vm.dispTh; v != nil && !v.close {
		return v
	}
	return nil
}

// closeGenerator принудительно завершает генератор, исполняя его finally-
// блоки (Zend: zend_generator_close). atShutdown влияет лишь на кадр трассировки.
func (vm *VM) closeGenerator(g *Generator, atShutdown bool) error {
	if g == nil || g.done || !g.started || g.running {
		return nil
	}
	if vm.fp+1 >= maxFrames {
		return errStackOverflow
	}
	callerPC := vm.ip
	savedThis, savedSelf, savedCalled := vm.ctx.this, vm.ctx.self, vm.ctx.called
	savedPending := vm.pendingThis
	savedLine := vm.curLine
	savedChunk := vm.chunk
	savedArgs := vm.curFuncArgs
	savedParams, savedVar, savedFixed, savedIn := vm.funcParams, vm.funcVariadic, vm.funcNumFixed, vm.inFunction
	savedForce := vm.forceClosing
	defer func() {
		vm.ctx.this, vm.ctx.self, vm.ctx.called = savedThis, savedSelf, savedCalled
		vm.pendingThis = savedPending
		vm.curLine = savedLine
		vm.chunk = savedChunk
		vm.curFuncArgs = savedArgs
		vm.funcParams, vm.funcVariadic, vm.funcNumFixed, vm.inFunction = savedParams, savedVar, savedFixed, savedIn
		vm.forceClosing = savedForce
	}()

	// Место вызова (resume) фиксируем до подмены vm.chunk/vm.ip на кадр
	// генератора: строка вычисляется лениво из callerChunk/callerPC.
	callerFile := vm.curFile
	callerChunk := vm.chunk
	callerLoc := vm.ip - 1
	callerFallback := vm.curLine
	if atShutdown {
		callerFile, callerChunk, callerLoc, callerFallback = "[internal function]", nil, 0, 0
	}

	genFP := vm.fp + 1
	g.running = true
	defer func() { g.running = false }()

	vm.fp = genFP
	vm.cur = &vm.frames[genFP]
	vm.cur.dynLocals = nil
	vm.cur.dynOrder = nil
	vm.dropFrame(vm.cur)
	vm.widenFrame(vm.cur, len(g.regs))
	copy(vm.cur.regs, g.regs)
	// Учёт ссылок переходит из сохранённого состояния генератора в кадр.
	vm.cur.tracked = g.gTracked
	vm.cur.nTracked = g.gNTracked
	g.gTracked = [maxRegsHard / 64]uint64{}
	g.gNTracked = 0
	vm.cur.this = g.this
	vm.ip = g.pc
	vm.chunk = g.chunk
	vm.ctx.this = g.this
	vm.ctx.self = g.self
	vm.ctx.called = g.called
	vm.pendingThis = Slot{}
	vm.curFuncArgs = g.args
	vm.inFunction = true
	if cf, ok := g.fn.Executable.(*CompiledFunction); ok {
		vm.funcParams, vm.funcVariadic, vm.funcNumFixed = cf.numParams, cf.variadic, cf.numFixed
	}

	vm.runningGens = append(vm.runningGens, g)
	defer func() { vm.runningGens = vm.runningGens[:len(vm.runningGens)-1] }()

	for i := range g.tries {
		e := g.tries[i]
		e.fp = genFP
		vm.tries = append(vm.tries, e)
	}
	g.tries = g.tries[:0]

	// Zend zend_generator_dtor_storage: записи, чей finally выполняется прямо
	// в точке yield (приостановка внутри finally), снимаются вместе с их
	// отложенным исключением — оно сбрасывается, а не перевозбуждается
	// (Zend/tests/generators/yield_in_finally_cleanup). Останавливаемся на
	// первой записи, приостановленной в ТЕЛЕ try: её finally дожигается
	// возобновлением, а внешние finally-слоты разбирает диспетчер VM
	// (bug71604: отложенные исключения перевозбуждаются с previous).
	for len(vm.tries) > 0 {
		h := &vm.tries[len(vm.tries)-1]
		if h.fp != genFP || !h.inFinally {
			break
		}
		h.savedErr = nil
		vm.tries = vm.tries[:len(vm.tries)-1]
	}

	traced := g.fn != nil
	if traced {
		vm.pushCallAt(g.fn, callerFile, callerChunk, callerLoc, callerFallback, g.args)
	}

	g.forceClose = true
	vm.forceClosing = g
	defer func() { g.forceClose = false }()

	// Вложенный генератор (yield from) НЕ закрываем принудительно: в Zend он
	// закрывается только когда пропадает последняя ссылка на него. Ссылку
	// держит кадр внешнего генератора (регистр-источник yield from), поэтому
	// после очистки регистров ниже неиспользуемый вложенный генератор
	// закрывается сам (zend_generator_dtor_storage через refcount), а всё ещё
	// используемый снаружи (repeated_yield_from_with_immediate_release)
	// остаётся живым.

	// Ссылка на вложенный генератор (yield from) отпускается ДО finally
	// внешнего: в Zend zend_generator_dtor_storage делает OBJ_RELEASE(node.parent)
	// раньше zend_generator_close, поэтому finally вложенного генератора
	// исполняется первым (bug71601).
	vm.releaseInnerRef(g)
	// Немедленное уничтожение вложенного генератора (его finally до finally
	// внешнего): отметки времени жизни выполняются проходом сейчас, а не в
	// следующей точке.
	if g != nil {
		vm.FlushLifetime()
	}

	var err error
	if !vm.handleThrow(g.chunk, errForceClose) {
		err = vm.closeDispatchErr()
	} else {
		for {
			e := vm.exec(g.chunk)
			if e == nil {
				err = nil
				break
			}
			if ts, ok := e.(*Throwable); ok && ts.close {
				if !vm.handleThrow(g.chunk, ts) {
					err = vm.closeDispatchErr()
					break
				}
				continue
			}
			err = e
			break
		}
	}

	if traced {
		vm.popCall()
	}
	vm.saveTriesForFrame(g, genFP)
	copy(g.regs, vm.cur.regs)
	g.gTracked = vm.cur.tracked
	g.gNTracked = vm.cur.nTracked
	g.pc = vm.ip
	g.this = vm.cur.this
	vm.clearRegs(vm.cur)
	vm.fp = genFP - 1
	vm.cur = &vm.frames[genFP-1]
	vm.ip = callerPC
	// Кадр закрытого генератора уничтожается (Zend zend_generator_close):
	// локальные переменные и ссылка на вложенный генератор отпускаются здесь,
	// поэтому их finally/деструкторы срабатывают до finally внешнего
	// генератора (bug71601).
	vm.dropSavedRegs(g)
	g.done = true
	g.hasCur = false
	return err
}

// makeGenerator создаёт ленивый генератор для функции f. Тело не исполняется.
func (vm *VM) makeGenerator(f *CompiledFunction, ctx *Context, args []Slot) (Slot, error) {
	if vm.genClass == nil {
		return Slot{}, NewThrowable("Error", "generator support is not initialised")
	}
	g := &Generator{
		chunk:     f.chunk,
		fn:        f.Owner(),
		args:      append([]Slot(nil), args...),
		this:      ctx.this,
		self:      ctx.self,
		called:    ctx.called,
		ret:       MakeNull(),
		resumeReg: -1,
	}
	w := f.chunk.NumRegs()
	if len(args) > w {
		w = len(args)
	}
	g.regs = make([]Slot, w)
	for i, a := range args {
		g.regs[i] = a
		shareSlot(a)
	}
	g.class = vm.genClass
	return vm.genClass.objectSlot(&g.Object, vm.genClassIdx), nil
}

// resumeKind — способ возобновления генератора.
type resumeKind int

const (
	resumeStart    resumeKind = iota // первый запуск: исполнить до первого yield
	resumeSend                       // продолжить, положив значение в регистр yield
	resumeThrow                      // бросить исключение в точке yield
	resumeThrowErr                   // перебросить исключение из вложенного yield from
)

// advance возобновляет выполнение тела генератора. Если !started — стартует с
// pc=0; иначе восстанавливает сохранённый кадр. Исполняет exec до следующего
// OpYield (новая пара cur/curKey) / return (done=true) / конца.
func (vm *VM) advance(g *Generator, kind resumeKind, val Slot) error {
	if g.running {
		return NewThrowable("Error", "Cannot resume an already running generator")
	}
	outermost := vm.genResumeDepth == 0
	vm.genResumeDepth++
	defer func() { vm.genResumeDepth-- }()
	if g.done {
		return nil
	}
	if vm.fp+1 >= maxFrames {
		return errStackOverflow
	}

	genFP := vm.fp + 1
	callerPC := vm.ip
	savedThis, savedSelf, savedCalled := vm.ctx.this, vm.ctx.self, vm.ctx.called
	savedPending := vm.pendingThis
	savedLine := vm.curLine
	savedChunk := vm.chunk
	defer func() {
		vm.ctx.this, vm.ctx.self, vm.ctx.called = savedThis, savedSelf, savedCalled
		vm.pendingThis = savedPending
		vm.curLine = savedLine
		vm.chunk = savedChunk
	}()

	g.running = true
	defer func() { g.running = false }()

	savedArgs := vm.curFuncArgs
	savedParams, savedVar, savedFixed, savedIn := vm.funcParams, vm.funcVariadic, vm.funcNumFixed, vm.inFunction
	vm.curFuncArgs = g.args
	vm.inFunction = true
	if cf, ok := g.fn.Executable.(*CompiledFunction); ok {
		vm.funcParams, vm.funcVariadic, vm.funcNumFixed = cf.numParams, cf.variadic, cf.numFixed
	}
	defer func() {
		vm.curFuncArgs = savedArgs
		vm.funcParams, vm.funcVariadic, vm.funcNumFixed, vm.inFunction = savedParams, savedVar, savedFixed, savedIn
	}()

	vm.runningGens = append(vm.runningGens, g)
	defer func() { vm.runningGens = vm.runningGens[:len(vm.runningGens)-1] }()

	vm.fp = genFP
	vm.cur = &vm.frames[genFP]
	vm.cur.dynLocals = nil
	vm.cur.dynOrder = nil
	vm.dropFrame(vm.cur)
	vm.widenFrame(vm.cur, len(g.regs))
	copy(vm.cur.regs, g.regs)
	// Учёт ссылок переходит из сохранённого состояния генератора в кадр.
	vm.cur.tracked = g.gTracked
	vm.cur.nTracked = g.gNTracked
	g.gTracked = [maxRegsHard / 64]uint64{}
	g.gNTracked = 0
	vm.cur.this = g.this
	vm.ip = g.pc
	vm.ctx.this = g.this
	vm.ctx.self = g.self
	vm.ctx.called = g.called
	vm.pendingThis = Slot{}

	// Кадр тела генератора для stack trace (вызывающий — место resume).
	traced := g.fn != nil
	if traced {
		vm.pushGenCall(g)
	}

	// Вернуть сохранённые обработчики try, перенумеровав их на текущий fp.
	for i := range g.tries {
		e := g.tries[i]
		e.fp = genFP
		vm.tries = append(vm.tries, e)
	}
	g.tries = g.tries[:0]

	var err error
	switch kind {
	case resumeSend:
		if g.resumeReg >= 0 {
			{
				old := vm.cur.regs[g.resumeReg]
				wv := val
				vm.cur.regs[g.resumeReg] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(g.resumeReg), old)
				}
			}
			shareSlot(val)
		}
	case resumeThrow:
		th := vm.makeThrowable(val)
		if !vm.handleThrow(g.chunk, th) {
			err = th
		}
	case resumeThrowErr:
		th := g.pendingThrow
		g.pendingThrow = nil
		if th == nil {
			th = NewThrowable("Error", "no pending exception")
		}
		if !vm.handleThrow(g.chunk, th) {
			err = th
		}
	}
	if err == nil {
		vm.chunk = g.chunk
		err = vm.exec(g.chunk)
	}
	// Позиция тела генератора в точке выхода/ошибки: кадр деструктора при
	// уничтожении локалов брошенного генератора получает её, а не
	// "[internal function]" (gh14387, yield_from_throw_with_throwing_exception).
	excPC := vm.ip
	excChunk := vm.chunk

	if traced {
		// Кадр снимаем до освобождения локальных переменных: Zend при
		// закрытии/завершении генератора обнуляет execute_data раньше, чем
		// уничтожает CV, поэтому деструкторы локалов видят в трейсе
		// вызывающий кадр (gh15851: Foo->__destruct <- bar()).
		vm.popCall()
	}
	if outermost {
		// Zend zend_generator_resume: флаг DO_INIT снимается у генератора,
		// чей resume был внешним (вложенные resume внутри цепочки свой флаг
		// сохраняют).
		g.doInit = false
	}

	// Снять оставшиеся обработчики генератора и вернуть состояние.
	vm.saveTriesForFrame(g, genFP)
	copy(g.regs, vm.cur.regs)
	g.gTracked = vm.cur.tracked
	g.gNTracked = vm.cur.nTracked
	g.pc = vm.ip
	g.this = vm.cur.this
	// Кадр VM больше не нужен: копия состояния живёт в генераторе. Чистим
	// регистры, чтобы кадр не удерживал значения завершённого/усыплённого
	// генератора и не мешал GC.
	vm.clearRegs(vm.cur)
	vm.fp = genFP - 1
	vm.cur = &vm.frames[genFP-1]
	vm.ip = callerPC

	switch e := err.(type) {
	case yieldSignal:
		g.hasCur = true
		g.cur = e.val
		if e.hasKey {
			k := deref(e.key)
			g.curKey = k
			if !e.from && k.typ == IntType && k.Int() >= g.autoKey {
				g.autoKey = k.Int() + 1
			}
		} else {
			g.curKey = MakeInt(g.autoKey)
			g.autoKey++
		}
		g.resumeReg = e.resumeReg
		// yield, порождённый `yield from` по генератору: запоминаем делегацию.
		// Во всех остальных случаях (обычный yield, yield from массива/итератора)
		// делегации нет.
		if e.from && g.inner != nil && g.inner != g {
			// Флаг DO_INIT ставится только в начале делегации и только если
			// кадр возобновлялся вложенно (внешний resume снимает флаг у
			// своего генератора — Zend zend_generator_resume).
			if !g.delegating {
				g.doInit = !outermost
			}
			g.delegating = true
		} else {
			g.delegating = false
			g.inner = nil
		}
		return nil
	case retSignal:
		g.done = true
		g.hasCur = false
		g.ret = vm.retVal
		// Тело завершилось: локальные переменные кадра освобождаются
		// (Zend zend_generator_close при выходе из тела — bug71013).
		vm.dropSavedRegs(g)
		return nil
	case nil:
		g.done = true
		g.hasCur = false
		g.ret = MakeNull()
		vm.dropSavedRegs(g)
		return nil
	default:
		g.done = true
		g.hasCur = false
		g.ret = MakeNull()
		// Zend: кадр брошенного генератора ещё в цепочке execute_data, поэтому
		// деструкторы локалов видят его как "[internal function]" (gh14387).
		if traced {
			vm.pushGenCall(g)
		}
		sSet, sFile, sChunk, sPC, sLine := vm.nextFrameSet, vm.nextFrameFile, vm.nextFrameChunk, vm.nextFramePC, vm.nextFrameLine
		if excChunk != nil && excPC > 0 {
			vm.nextFrameSet = true
			vm.nextFrameFile = excChunk.File
			vm.nextFrameChunk, vm.nextFramePC = excChunk, excPC
			vm.nextFrameLine = excChunk.LineAt(excPC - 1)
		}
		vm.dropSavedRegs(g)
		vm.nextFrameSet, vm.nextFrameFile, vm.nextFrameChunk, vm.nextFramePC, vm.nextFrameLine = sSet, sFile, sChunk, sPC, sLine
		if traced {
			vm.popCall()
		}
		if derr := vm.takePendingDestroyErr(); derr != nil {
			// Деструктор локальной переменной бросил при раскрутке кадра
			// генератора: его исключение заменяет исходное, сцепляясь через
			// previous (gh14387, yield_from_throw_with_throwing_exception).
			if nth, ok := asThrowable(derr); ok {
				if cur, ok2 := asThrowable(err); ok2 && cur != nth {
					vm.chainPrevious(nth, cur)
				}
				err = nth
			}
		}
		if _, ok := asThrowable(err); ok {
			g.aborted = true
		}
		return err
	}
}

// isThrowableValue сообщает, что слот — объект, реализующий Throwable.
func (vm *VM) isThrowableValue(v Slot) bool {
	if deref(v).typ != ObjectType {
		return false
	}
	ci := vm.classInfo(v)
	thr := vm.findClass("Throwable")
	return ci != nil && thr != nil && ci.isA(thr)
}

// genResumeCallerInternal сообщает, что непосредственно вызывающий — нативный
// метод (Generator->*): Zend печатает такой кадр как "[internal function]".
func (vm *VM) genResumeCallerInternal() bool {
	if n := len(vm.callStack); n > 0 {
		if callee := vm.callStack[n-1].callee; callee != nil {
			if _, ok := callee.Executable.(*NativeFunction); ok {
				return true
			}
		}
	}
	return false
}

// suspendedLine возвращает строку приостановленного кадра генератора: для
// делегирующего генератора это строка его `yield from` — Zend показывает её
// как место вызова вложенного генератора в трейсе.
func (g *Generator) suspendedLine() int {
	if g == nil || g.chunk == nil || g.pc <= 0 {
		return 0
	}
	if l := g.chunk.LineAt(g.pc - 1); l > 0 {
		return l
	}
	return 0
}

// pushGenCall регистрирует кадр тела генератора для stack trace. Место вызова
// — текущая позиция (место resume) либо "[internal function]" для нативного
// вызывающего. Строка вычисляется лениво.
func (vm *VM) pushGenCall(g *Generator) {
	if d := vm.genDelegPos; d != nil && d != g {
		// Вложенный генератор вызван из кадра делегирующего: место вызова —
		// его приостановленный yield from (backtrace_multi_yield_from).
		vm.pushCallAt(g.fn, vm.curFile, nil, 0, d.suspendedLine(), g.args)
		return
	}
	if vm.genResumeCallerInternal() {
		vm.pushCallAt(g.fn, "[internal function]", nil, 0, 0, g.args)
		return
	}
	vm.pushCall(g.fn, g.args)
}

// saveTriesForFrame переносит обработчики кадра fp (и глубже) в генератор,
// сохраняя порядок. Вызывается на приостановке и на завершении.
func (vm *VM) saveTriesForFrame(g *Generator, fp int) {
	for len(vm.tries) > 0 && vm.tries[len(vm.tries)-1].fp >= fp {
		g.tries = append(g.tries, vm.tries[len(vm.tries)-1])
		vm.tries = vm.tries[:len(vm.tries)-1]
	}
	for i, j := 0, len(g.tries)-1; i < j; i, j = i+1, j-1 {
		g.tries[i], g.tries[j] = g.tries[j], g.tries[i]
	}
}

// genOf извлекает *Generator из $this нативного метода. Проверяем, что объект
// действительно экземпляр класса Generator (иначе - nil).
func genOf(ctx *Context) *Generator {
	if ctx.vm == nil {
		return nil
	}
	return objectAs[Generator](ctx.ThisObj(), ctx.vm.genClass)
}

// genOfSlot извлекает *Generator из произвольного объектного слота.
func (vm *VM) genOfSlot(s Slot) *Generator {
	if vm.genClass == nil {
		// У VM без класса Generator (собранные вручную) objectAs с
		// nil-владельцем принял бы за генератор любой объект.
		return nil
	}
	return objectAs[Generator](vm.objectOf(s), vm.genClass)
}

// IsGeneratorSlot сообщает, что слот — объект класса Generator.
func (vm *VM) IsGeneratorSlot(s Slot) bool { return vm.genOfSlot(s) != nil }

// CloseIfGenerator закрывает незавершённый генератор в слоте (при unset/
// переприсваивании), исполняя его finally-блоки.
func (vm *VM) CloseIfGenerator(s Slot) error {
	return vm.CloseIfGeneratorEx(s)
}

// CloseIfGeneratorEx — как CloseIfGenerator, но не закрывает генератор, пока
// на него есть ссылки в других регистрах/глобалах (exclude — индексы регистров
// текущего кадра, которые ссылкой не считаются: сама переменная и её копия).
func (vm *VM) CloseIfGeneratorEx(s Slot, exclude ...int) error {
	if g := vm.genOfSlot(s); g != nil {
		if vm.objReferencedElsewhere(s, exclude) {
			return nil
		}
		return vm.closeGenerator(g, false)
	}
	return nil
}

// objReferencedElsewhere ищет ссылку на объект из s в живых регистрах (кроме
// исключённых в текущем кадре), глобалах и аргументах кадров.
func (vm *VM) objReferencedElsewhere(s Slot, exclude []int) bool {
	target := vm.objectOf(s)
	if target == nil {
		return false
	}
	skip := map[int]bool{}
	for _, i := range exclude {
		skip[i] = true
	}
	for i := 0; i <= vm.fp && i < len(vm.frames); i++ {
		for j, r := range vm.frames[i].regs {
			if i == vm.fp && skip[j] {
				continue
			}
			if slotHasObject(r, target, 0) {
				return true
			}
		}
	}
	for _, r := range vm.globals {
		if slotHasObject(r, target, 0) {
			return true
		}
	}
	for i := range vm.callStack {
		for _, a := range vm.callStack[i].args {
			if slotHasObject(a, target, 0) {
				return true
			}
		}
	}
	return false
}

func slotHasObject(s Slot, o *Object, depth int) bool {
	if depth > 2 {
		return false
	}
	for i := 0; s.typ == RefType && i < 10; i++ {
		r := s.Ref()
		if r == nil {
			return false
		}
		s = r.v
	}
	switch s.typ {
	case ObjectType:
		return s.obj() == o
	case ArrayType:
		if a := s.AsArray(); a != nil {
			for _, v := range a.All() {
				if slotHasObject(v, o, depth+1) {
					return true
				}
			}
		}
	}
	return false
}

// genMethod регистрирует нативный метод класса Generator.
func genMethod(ci *runtimeClass, name string, numArgs int, fn func(vm *VM, g *Generator, args []Slot) (Slot, error)) {
	ci.Method(name, &Function{Name: name, NumArgs: numArgs, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			res, err := fn(ctx.vm, genOf(ctx), args)
			if err != nil {
				if th, ok := err.(*Throwable); ok && ctx.vm != nil {
					// Трейс снимаем, пока кадр метода на стеке (Generator->next/
					// rewind/...), иначе он потеряется.
					ctx.vm.fillThrowableTrace(th)
				}
			}
			return res, err
		},
	}})
}

// fillThrowableTrace заполняет трейс/локацию исключения текущим стеком, если
// они ещё не заданы.
func (vm *VM) fillThrowableTrace(th *Throwable) {
	if th == nil || th.Trace != nil {
		return
	}
	th.Trace = vm.captureExceptionTrace()
	if th.LocLine == 0 && !th.LocOverridden {
		if n := len(th.Trace); n > 0 {
			th.LocFile = th.Trace[n-1].callerFile
			th.LocLine = th.Trace[n-1].callerLine
		}
		if th.LocLine == 0 {
			th.LocFile = vm.curFile
			th.LocLine = vm.line()
		}
	}
}

// delegationCurrent возвращает самый вложенный генератор цепочки yield from.
func (g *Generator) delegationCurrent() *Generator {
	cur := g
	for cur.delegating && cur.inner != nil && cur.inner != cur {
		cur = cur.inner
	}
	return cur
}

// doInitSwallow реализует ZEND_GENERATOR_DO_INIT: если генератор только что
// начал делегировать, а у текущего (самого вложенного) генератора уже есть
// значение, resume не продвигает цепочку — флаг снимается, вызов no-op.
func (g *Generator) doInitSwallow() bool {
	if !g.doInit {
		return false
	}
	cur := g.delegationCurrent()
	g.doInit = false
	return cur != g && cur.hasCur
}

// genValid/... реализуют методы Iterator для генератора с учётом делегации
// `yield from` (модель Zend zend_generator_yield_from). Пока генератор
// приостановлен на yield, порождённом вложенным генератором, чтение и
// продвижение идут через вложенный генератор, не возобновляя собственный кадр.

func (vm *VM) genValid(g *Generator) (bool, error) {
	if err := vm.ensureStarted(g); err != nil {
		return false, err
	}
	if g.done {
		return false, nil
	}
	if g.delegating && g.inner != nil && g.inner != g {
		// Пока внешний генератор не возобновлён, он валиден, даже если
		// вложенный уже завершён (Zend: valid() не возобновляет внешний).
		return true, nil
	}
	return g.hasCur, nil
}

func (vm *VM) genCurrent(g *Generator) (Slot, error) {
	if err := vm.ensureStarted(g); err != nil {
		return MakeNull(), err
	}
	if g.done {
		return MakeNull(), nil
	}
	if g.delegating && g.inner != nil && g.inner != g {
		ig := g.inner
		if ig.done {
			if ig.aborted {
				if err := vm.delegateThrow(g, "ClosedGeneratorException", "Generator yielded from aborted, no return value available"); err != nil {
					return MakeNull(), err
				}
				if g.done || !g.hasCur {
					return MakeNull(), nil
				}
				return deref(g.cur), nil
			}
			// Внутренний завершён: current() не возобновляет внешний, а отдаёт
			// последнее значение внутреннего (модель Zend).
			g.cur = ig.cur
			g.curKey = ig.curKey
			return deref(ig.cur), nil
		}
		if err := vm.ensureStarted(ig); err != nil {
			return MakeNull(), err
		}
		return vm.genCurrent(ig)
	}
	if !g.hasCur {
		return MakeNull(), nil
	}
	return deref(g.cur), nil
}

func (vm *VM) genKey(g *Generator) (Slot, error) {
	if err := vm.ensureStarted(g); err != nil {
		return MakeNull(), err
	}
	if g.done {
		return MakeNull(), nil
	}
	if g.delegating && g.inner != nil && g.inner != g {
		ig := g.inner
		if ig.done {
			if ig.aborted {
				if err := vm.delegateThrow(g, "ClosedGeneratorException", "Generator yielded from aborted, no return value available"); err != nil {
					return MakeNull(), err
				}
				if g.done || !g.hasCur {
					return MakeNull(), nil
				}
				return g.curKey, nil
			}
			g.cur = ig.cur
			g.curKey = ig.curKey
			return ig.curKey, nil
		}
		if err := vm.ensureStarted(ig); err != nil {
			return MakeNull(), err
		}
		return vm.genKey(ig)
	}
	if !g.hasCur {
		return MakeNull(), nil
	}
	return g.curKey, nil
}

func (vm *VM) genNext(g *Generator) error {
	top := vm.genCallDepth == 0
	vm.genCallDepth++
	defer func() { vm.genCallDepth-- }()
	g.advanced = true
	if err := vm.ensureStarted(g); err != nil {
		return err
	}
	if g.done {
		return nil
	}
	// Zend проверяет DO_INIT только у генератора, чей метод вызван снаружи;
	// вложенные шаги цепочки проверку не проходят.
	if top && g.doInitSwallow() {
		return nil
	}
	if g.delegating && g.inner != nil && g.inner != g {
		ig := g.inner
		if ig.done && ig.aborted {
			return vm.delegateThrow(g, "ClosedGeneratorException", "Generator yielded from aborted, no return value available")
		}
		// Кадр внешнего генератора присутствует в стеке, пока идёт делегация
		// (нужно для php-совместимого stack trace исключений из вложенного).
		traced := g.fn != nil
		if traced {
			vm.pushGenCall(g)
		}
		savedDeleg := vm.genDelegPos
		if traced {
			vm.genDelegPos = g
		}
		err := vm.genNext(ig)
		vm.genDelegPos = savedDeleg
		var vok bool
		if err == nil {
			vok, err = vm.genValid(ig)
		}
		if traced {
			vm.popCall()
		}
		if err != nil {
			return vm.rethrowInto(g, err)
		}
		if vok {
			return nil
		}
		return vm.advance(g, resumeSend, MakeNull())
	}
	return vm.advance(g, resumeSend, MakeNull())
}

// rethrowInto перебрасывает исключение, возникшее во вложенном генераторе
// (yield from), во внешний генератор: обработчики try/catch внешнего должны его
// увидеть. Если исключение не Throwable — возвращается как есть.
func (vm *VM) rethrowInto(g *Generator, err error) error {
	if th, ok := asThrowable(err); ok {
		g.pendingThrow = th
		return vm.advance(g, resumeThrowErr, MakeNull())
	}
	return err
}

// delegateThrow бросает заданное исключение во внешний генератор в точке
// yield from (возвращая управление его catch, если есть).
func (vm *VM) delegateThrow(g *Generator, class, msg string) error {
	g.pendingThrow = NewThrowable(class, msg)
	return vm.advance(g, resumeThrowErr, MakeNull())
}

// genSend реализует Generator::send с учётом делегации yield from: значение
// пересылается вложенному генератору.
func (vm *VM) genSend(g *Generator, v Slot) (Slot, error) {
	top := vm.genCallDepth == 0
	vm.genCallDepth++
	defer func() { vm.genCallDepth-- }()
	if err := vm.ensureStarted(g); err != nil {
		return Slot{}, err
	}
	if g.done {
		return MakeNull(), nil
	}
	g.advanced = true
	if top && g.doInitSwallow() {
		return g.cur, nil
	}
	if g.delegating && g.inner != nil && g.inner != g {
		res, err := vm.genSend(g.inner, v)
		if err != nil {
			return Slot{}, vm.rethrowInto(g, err)
		}
		if !g.inner.done {
			return res, nil
		}
		// Внутренний завершился: продолжаем внешний.
		if err := vm.advance(g, resumeSend, MakeNull()); err != nil {
			return Slot{}, err
		}
		if g.done || !g.hasCur {
			return MakeNull(), nil
		}
		return g.cur, nil
	}
	if err := vm.advance(g, resumeSend, v); err != nil {
		return Slot{}, err
	}
	if g.done || !g.hasCur {
		return MakeNull(), nil
	}
	return g.cur, nil
}

// genThrow реализует Generator::throw с учётом делегации yield from:
// исключение бросается во вложенный генератор.
func (vm *VM) genThrow(g *Generator, v Slot) (Slot, error) {
	top := vm.genCallDepth == 0
	vm.genCallDepth++
	defer func() { vm.genCallDepth-- }()
	if err := vm.ensureStarted(g); err != nil {
		return Slot{}, err
	}
	if g.done {
		// Генератор уже завершён: исключение уходит в вызывающего.
		return Slot{}, vm.makeThrowable(v)
	}
	g.advanced = true
	if top && g.doInitSwallow() {
		return g.cur, nil
	}
	if g.delegating && g.inner != nil && g.inner != g {
		res, err := vm.genThrow(g.inner, v)
		if err != nil {
			return Slot{}, vm.rethrowInto(g, err)
		}
		if !g.inner.done {
			return res, nil
		}
		if err := vm.advance(g, resumeSend, MakeNull()); err != nil {
			return Slot{}, err
		}
		if g.done || !g.hasCur {
			return MakeNull(), nil
		}
		return g.cur, nil
	}
	if err := vm.advance(g, resumeThrow, v); err != nil {
		return Slot{}, err
	}
	if g.done || !g.hasCur {
		return MakeNull(), nil
	}
	return g.cur, nil
}

// registerGeneratorMembers реализует методы Iterator и специфичные для
// Generator методы (send/getReturn/throw) поверх ленивой модели.
func registerGeneratorMembers(ci *runtimeClass) {
	genMethod(ci, "rewind", 0, func(vm *VM, g *Generator, _ []Slot) (Slot, error) {
		if g == nil {
			return MakeNull(), nil
		}
		if g.done {
			return Slot{}, NewThrowable("Exception", "Cannot traverse an already closed generator")
		}
		if g.advanced {
			return Slot{}, NewThrowable("Exception", "Cannot rewind a generator that was already run")
		}
		if !g.started {
			g.started = true
			if err := vm.advance(g, resumeStart, MakeNull()); err != nil {
				return Slot{}, err
			}
		}
		return MakeNull(), nil
	})
	genMethod(ci, "valid", 0, func(vm *VM, g *Generator, _ []Slot) (Slot, error) {
		if g == nil {
			return MakeBool(false), nil
		}
		ok, err := vm.genValid(g)
		if err != nil {
			return Slot{}, err
		}
		return MakeBool(ok), nil
	})
	genMethod(ci, "current", 0, func(vm *VM, g *Generator, _ []Slot) (Slot, error) {
		if g == nil {
			return MakeNull(), nil
		}
		return vm.genCurrent(g)
	})
	genMethod(ci, "key", 0, func(vm *VM, g *Generator, _ []Slot) (Slot, error) {
		if g == nil {
			return MakeNull(), nil
		}
		return vm.genKey(g)
	})
	genMethod(ci, "next", 0, func(vm *VM, g *Generator, _ []Slot) (Slot, error) {
		if g == nil {
			return MakeNull(), nil
		}
		if err := vm.genNext(g); err != nil {
			return Slot{}, err
		}
		return MakeNull(), nil
	})
	// send(): если генератор не начат — сначала запускаем до первого yield,
	// затем посылаем значение (в PHP send() до старта делает именно это).
	genMethod(ci, "send", 1, func(vm *VM, g *Generator, args []Slot) (Slot, error) {
		if g == nil {
			return MakeNull(), nil
		}
		var v Slot = MakeNull()
		if len(args) > 0 {
			// Динамический вызов метода передаёт простые переменные как
			// Ref-ячейки (возможный by-ref параметр). send() — by-value:
			// разыменовываем, иначе send-значение попадало в yield-выражение
			// как &-ссылка.
			v = deref(args[0])
		}
		return vm.genSend(g, v)
	})
	genMethod(ci, "getReturn", 0, func(vm *VM, g *Generator, _ []Slot) (Slot, error) {
		if g == nil {
			return MakeNull(), nil
		}
		// В PHP getReturn() автоматически «праймит» неначатый генератор:
		// прогоняет его до return или до первого yield (или до исключения).
		if err := vm.ensureStarted(g); err != nil {
			return Slot{}, err
		}
		if g.done && !g.aborted {
			return g.ret, nil
		}
		return Slot{}, NewThrowable("Exception", "Cannot get return value of a generator that hasn't returned")
	})
	// throw($e): возобновляет генератор, бросая исключение в точке yield.
	genMethod(ci, "throw", 1, func(vm *VM, g *Generator, args []Slot) (Slot, error) {
		if g == nil {
			return MakeNull(), nil
		}
		var v Slot = MakeNull()
		if len(args) > 0 {
			v = args[0]
		}
		// Generator::throw(Throwable $e): аргумент должен реализовывать
		// Throwable, иначе TypeError (как проверка типа параметра в Zend).
		if !vm.isThrowableValue(v) {
			got := valueTypeName(vm, deref(v))
			return Slot{}, vm.argTypeError("Generator::throw", 1, "exception", "Throwable", got, "", 0)
		}
		return vm.genThrow(g, v)
	})
}

// ensureStarted запускает генератор до первого yield, если он ещё не начат.
func (vm *VM) ensureStarted(g *Generator) error {
	if g.started {
		return nil
	}
	g.started = true
	return vm.advance(g, resumeStart, MakeNull())
}

// YieldFromRetBuiltin — реализация служебной функции __php_yield_from_ret:
// значение выражения `yield from`: return внутреннего генератора, если
// источник — Generator, иначе null.
func YieldFromRetBuiltin(ctx *Context) Value {
	args := ctx.Args()
	if len(args) == 0 {
		return MakeNull()
	}
	s := deref(args[0])
	if s.typ == ObjectType {
		if g := ctx.vm.genOfSlot(s); g != nil {
			if g.done {
				if g.aborted {
					ctx.Throw(NewThrowable("Error", "Generator passed to yield from was aborted without proper return and is unable to continue"))
					if ctx.HasThrown() {
						return *new(Value)
					}
					return MakeNull()
				}
				return g.ret
			}
		}
	}
	return MakeNull()
}

// YieldFromBeginBuiltin — служебная функция __php_yield_from_begin($src):
// готовит состояние обхода для `yield from`. В отличие от foreach, для
// Generator НЕ вызывает rewind (генератор мог быть уже запущен: Zend не
// «перематывает» вложенный генератор). Для прочих Traversable — обычный
// iterInit (с rewind). Для не-Traversable — ошибка.
func YieldFromBeginBuiltin(ctx *Context) Value {
	// Принудительное закрытие генератора: `yield from` запрещён (Zend:
	// "Cannot use \"yield from\" in a force-closed generator").
	if ctx.VM().forceClosing != nil {
		ctx.Throw(NewThrowable("Error", `Cannot use "yield from" in a force-closed generator`))
		if ctx.HasThrown() {
			return *new(Value)
		}
		return MakeNull()
	}
	args := ctx.Args()
	if len(args) == 0 {
		ctx.Throw(NewThrowable("Error", `Can use "yield from" only with arrays and Traversables`))
		if ctx.HasThrown() {
			return *new(Value)
		}
		return MakeNull()
	}
	src := deref(args[0])
	if src.typ == ObjectType {
		state := newArray()
		state.SetStr("mode", MakeInt(iterModeMethods))
		state.SetStr("o", src)
		if gen := ctx.vm.genOfSlot(src); gen != nil {
			for _, rg := range ctx.vm.runningGens {
				if rg == gen {
					ctx.Throw(NewThrowable("Error", "Impossible to yield from the Generator being currently run"))
					if ctx.HasThrown() {
						return *new(Value)
					}
					return MakeNull()
				}
			}
			return MakeArray(state)
		}
		ci := ctx.vm.classInfo(src)
		if ci != nil {
			if m := ci.lookupMethod("getIterator"); m != nil {
				res, err := ctx.vm.invokeMethod(m, src, ci, nil)
				if err != nil {
					ctx.Throw(NewThrowable("Error", err.Error()))
					if ctx.HasThrown() {
						return *new(Value)
					}
					return MakeNull()
				}
				inner := deref(res)
				state.SetStr("o", inner)
				if err := ctx.vm.iterInvoke(inner, "rewind", nil); err != nil {
					if t, ok := err.(*Throwable); ok {
						ctx.Throw(t)
						if ctx.HasThrown() {
							return *new(Value)
						}
					}
					ctx.Throw(NewThrowable("Error", err.Error()))
					if ctx.HasThrown() {
						return *new(Value)
					}
					return MakeNull()
				}
				return MakeArray(state)
			}
			if ci.lookupMethod("rewind") != nil && ci.lookupMethod("valid") != nil &&
				ci.lookupMethod("current") != nil {
				if err := ctx.vm.iterInvoke(src, "rewind", nil); err != nil {
					if t, ok := err.(*Throwable); ok {
						ctx.Throw(t)
						if ctx.HasThrown() {
							return *new(Value)
						}
					}
					ctx.Throw(NewThrowable("Error", err.Error()))
					if ctx.HasThrown() {
						return *new(Value)
					}
					return MakeNull()
				}
				return MakeArray(state)
			}
		}
	}
	ctx.Throw(NewThrowable("Error", `Can use "yield from" only with arrays and Traversables`))
	if ctx.HasThrown() {
		return *new(Value)
	}
	return MakeNull()
}

// expandIterable вызывает fn для каждой пары key/value источника: PHP-массив
// или объект, реализующий Iterator/IteratorAggregate (включая Generator).
func (vm *VM) expandIterable(src Slot, fn func(k, v Slot) error) error {
	src = deref(src)
	switch src.typ {
	case ArrayType:
		for k, v := range src.AsArray().All() {
			var ks Slot
			if k.IsStr() {
				ks = MakeString(k.StrVal())
			} else {
				ks = MakeInt(k.IntVal())
			}
			if err := fn(ks, v); err != nil {
				return err
			}
		}
		return nil
	case ObjectType:
		st, err := vm.iterInit(src)
		if err != nil {
			return err
		}
		for {
			ok, err := vm.iterValid(st)
			if err != nil {
				return err
			}
			if !ok {
				break
			}
			k, err := vm.iterKey(st)
			if err != nil {
				return err
			}
			v, err := vm.iterCurrent(st)
			if err != nil {
				return err
			}
			if err := fn(k, v); err != nil {
				return err
			}
			if err := vm.iterNext(st); err != nil {
				return err
			}
		}
		return nil
	}
	return NewThrowable("Error", `Can use "yield from" only with arrays and Traversables`)
}

// IteratorToArrayBuiltin — реализация iterator_to_array($iterable[, $preserve]).
// По умолчанию ключи сохраняются; при $preserve_keys=false значения
// перенумеровываются.
func IteratorToArrayBuiltin(ctx *Context) Value {
	args := ctx.Args()
	src := MakeNull()
	preserve := true
	if len(args) > 0 {
		src = args[0]
	}
	if len(args) > 1 {
		preserve = args[1].ToBool()
	}
	arr := newArray()
	err := ctx.vm.expandIterable(src, func(k, v Slot) error {
		if preserve {
			ak, ok := ctx.vm.KeyOfWarn(k)
			if !ok {
				return NewThrowable("TypeError", "Illegal offset type")
			}
			_ = arr.Set(ak, v)
		} else {
			_, _ = arr.Append(v)
		}
		return nil
	})
	if err != nil {
		if t, ok := err.(*Throwable); ok {
			ctx.Throw(t)
			if ctx.HasThrown() {
				return *new(Value)
			}
		}
		ctx.Throw(NewThrowable("Error", err.Error()))
		if ctx.HasThrown() {
			return *new(Value)
		}
		return MakeNull()
	}
	return MakeArray(arr)
}
