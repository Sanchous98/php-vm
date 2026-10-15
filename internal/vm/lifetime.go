package vm

// lifetime.go — детерминированное время жизни объектов.
//
// В VM нет refcount: памятью владеет Go GC. Но PHP-семантика наблюдаема:
// __destruct вызывается, когда на объект не остаётся ссылок (unset, конец
// области видимости, перезапись), PHP-хендлы (#N) переиспользуются из
// free-list, WeakMap/WeakReference теряют записи, незавершённые генераторы и
// файберы закрываются при уничтожении.
//
// Модель: учёт ссылок ведётся В МОМЕНТ ЗАПИСИ (writeback, как переключение
// контекста CPU): регистровый файл — «физические регистры», кадр — «контекст».
// Запись в регистр отслеживаемого значения (объект/массив/ссылка/замыкание)
// увеличивает оценку и ставит бит в Frame.tracked; запись, вытесняющая
// отслеживаемое значение, оценку уменьшает. Выход из кадра (возврат и
// исключительное раскручивание) вызывает dropFrame — списание по битам.
// Сборщик по-прежнему запускается в точках OpSweep, но без сканирования
// регистров: кандидаты появляются непосредственно при записи.
// Сборщик помечает объекты/массивы, достижимые из «корней» VM (живые регистры
// кадров по SSA-маскам, глобалы, статики, props, нативные данные через
// рефлексию и т.п.), а затем уничтожает недостижимые — в порядке, который
// имитирует каскад refcount: сначала узлы без входящих ссылок из недостижимого
// множества, затем те, чьи ссылки освободились. Циклы (у которых «refcount»
// никогда не падает до нуля) не уничтожаются до gc_collect_cycles() — как в
// Zend.
//
// Точная проверка достижимости запускается экономно: по срочным кандидатам
// (объекты с __destruct, незавершённые генераторы/файберы), когда программа
// уже печатала хендлы (#N) и есть нулевые кандидаты, а также из
// gc_collect_cycles()/завершения скрипта. Проход допустим только при чистом
// userland-стеке: нативные кадры держат ссылки в Go-локальных переменных,
// которые сборщик не видит.

import (
	"math/bits"
	"reflect"
	"strings"
	"unsafe"
)

// lifetimeAllowed сообщает, что сбор можно выполнять: в стеке вызовов нет
// активных нативных кадров (их Go-локальные переменные могут удерживать
// объекты, которые сборщик не видит).
func (vm *VM) lifetimeAllowed() bool {
	for i := range vm.callStack {
		if isNativeCallee(vm.callStack[i].callee) {
			return false
		}
	}
	return true
}

// noteDropped вызывается при перезаписи/удалении места хранения: значение
// могло потерять ссылки на объекты. Уменьшает оценки ссылок; при обнулении
// ставит кандидата на проверку достижимости.
func (vm *VM) noteDropped(old Slot) {
	if old.typ < ObjectType || old.typ == ResourceType {
		return
	}
	if d := deref(old); d.typ == ObjectType {
		if o := d.obj(); o != nil {
		}
	}
	vm.decRefs(old, 0)
}

// noteStored вызывается при записи значения в контейнер (свойство, элемент
// массива, статическое свойство, ячейку-ссылку): увеличивает оценку ссылок.
func (vm *VM) noteStored(v Slot) {
	if v.typ < ObjectType || v.typ == ResourceType {
		return
	}
	vm.incRefs(v, 0)
}

func (vm *VM) incRefs(s Slot, depth int) {
	if depth > 8 {
		return
	}
	for i := 0; s.typ == RefType && i < 100; i++ {
		r := s.Ref()
		if r == nil {
			return
		}
		s = r.v
	}
	switch s.typ {
	case ObjectType:
		if o := s.obj(); o != nil && !o.dead {
			wasZero := o.refs <= 0
			o.refs++
			if wasZero && o.refs > 0 {
				vm.zeroObjs--
			}
		}
	case ArrayType:
		// Ссылки внутри массива учитываются поштучно при записи элементов;
		// при копировании массива (COW) второй владелец не считается —
		// недосчёт безопасен (лишняя проверка достижимости).
	case ClosureType:
		// Захваты замыкания не считаются: недосчёт безопасен.
	}
}

// decRefs уменьшает оценки ссылок для значения слота. Для массивов и
// замыканий обходит содержимое (элементы/захваты) — при разделении (COW)
// возможен недосчёт, безопасный для точной проверки достижимости. Циклы
// (массив/замыкание ссылается на себя) обходятся один раз: повторный визит
// не даёт новых объектов, а без защиты дал бы экспоненциальный обход.
func (vm *VM) decRefs(s Slot, depth int) {
	vm.decRefsSeen(s, 0, nil, nil)
}

func (vm *VM) decRefsSeen(s Slot, depth int, seenA map[*Array]bool, seenC map[*Closure]bool) {
	if depth > 8 {
		return
	}
	for i := 0; s.typ == RefType && i < 100; i++ {
		r := s.Ref()
		if r == nil {
			return
		}
		s = r.v
	}
	switch s.typ {
	case ObjectType:
		o := s.obj()
		if o == nil || o.dead {
			return
		}
		o.refs--
		vm.afterDecObject(o)
	case ResourceType:
		// Только верхний уровень: элементы массивов/захваты владельцами
		// ресурсов не считаются (noteStored пропускает ресурсы).
		if depth == 0 {
			vm.dropResourceRef(s)
		}
	case ClosureType:
		cl := s.asClosure()
		if cl == nil {
			return
		}
		if seenC == nil {
			seenC = map[*Closure]bool{}
		}
		if seenC[cl] {
			return
		}
		seenC[cl] = true
		for _, c := range cl.Captures {
			vm.decRefsSeen(c, depth+1, seenA, seenC)
		}
		vm.decRefsSeen(cl.BoundThis, depth+1, seenA, seenC)
	case ArrayType:
		a := s.AsArray()
		if a == nil || !a.objRefs {
			return
		}
		if seenA == nil {
			seenA = map[*Array]bool{}
		}
		if seenA[a] {
			return
		}
		seenA[a] = true
		for _, v := range a.All() {
			vm.decRefsSeen(v, depth+1, seenA, seenC)
		}
	}
}

// isLifetimeSpecial сообщает, что объект требует детерминированного
// уничтожения (генератор/файбер: закрытие с finally; enum-кейс не считается).
func (vm *VM) isLifetimeSpecial(o *Object) bool {
	if o == nil || o.class == nil {
		return false
	}
	if vm.genClass != nil && o.class.isA(vm.genClass) {
		return true
	}
	if vm.fiberClass != nil && o.class.isA(vm.fiberClass) {
		return true
	}
	return false
}

// ArmHandles включает точный сбор PHP-хендлов: дальнейшие потери ссылок
// приводят к немедленному проходу сборщика, чтобы нумерация #N совпадала с
// Zend. Вызывается функциями, печатающими хендлы (var_dump и т.п.).
func (vm *VM) ArmHandles() {
	if !vm.handlesArmed {
		vm.handlesArmed = true
		if vm.lifeCandidate || vm.zeroObjs > 0 {
			vm.FlushLifetime()
		}
	}
}

// --- Writeback учёта ссылок ---
//
// Запись в регистр выполняется парой «прочитать старое → записать новое →
// если затронут отслеживаемый тип, вызвать медленный путь». Быстрый путь
// (скаляры/ресурсы с обеих сторон) компилируется в пару сравнений и ветку,
// которую предсказывает процессор.

// trackedType сообщает, что тег значения учитывается вручную (объект,
// массив, ссылка, замыкание, а также ресурс с OnDrop — процесс proc_open;
// остальные ресурсы бит не получают). Теги ObjectType..ResourceType идут
// подряд, поэтому проверка — одна операция вычитания и сравнение (как и до
// включения ресурсов: добавленный тег не стоит лишней ветки на скалярных
// записях).
func trackedType(t Type) bool {
	return t-ObjectType <= ResourceType-ObjectType
}

// trackedOr сообщает, что хотя бы один из тегов учитывается вручную.
func trackedOr(a, b Type) bool {
	return a-ObjectType <= ResourceType-ObjectType || b-ObjectType <= ResourceType-ObjectType
}

// wRegSlow — медленный путь записи регистра: хотя бы одно из значений
// отслеживается. Новое значение v уже записано в регистр vm.cur.regs[i].
func (vm *VM) wRegSlow(i int, old Slot) {
	f := vm.cur
	if f == nil || uint(i) >= maxRegsHard {
		return
	}
	w := &f.tracked[i>>6]
	bit := uint64(1) << uint(i&63)
	v := f.regs[i]
	if *w&bit != 0 {
		if old == v {
			return
		}
		// Частый случай — объект: списываем без вызова decRefs.
		if old.typ == ObjectType {
			if o := old.obj(); o != nil && !o.dead {
				o.refs--
				vm.afterDecObject(o)
			}
		} else {
			vm.decRefs(old, 0)
		}
		*w &^= bit
		f.nTracked--
	}
	if v.typ == ObjectType {
		if o := v.obj(); o != nil && !o.dead {
			if o.refs <= 0 {
				vm.zeroObjs--
			}
			o.refs++
		}
		*w |= bit
		f.nTracked++
	} else if v.typ == ArrayType || v.typ == RefType || v.typ == ClosureType {
		// Быстрый путь для массивов/ссылок/замыканий: то же число проверок,
		// что у прежнего trackedType (без ветки ресурсов).
		vm.incRefs(v, 0)
		*w |= bit
		f.nTracked++
	} else if v.typ == ResourceType {
		if r := AsResource(v); r != nil && r.OnDrop != nil {
			if r.dropped && r.Refs <= 0 {
				r.dropped = false
			}
			r.Refs++
			*w |= bit
			f.nTracked++
		}
	}
}

// dropResourceRef списывает владение ресурсом с OnDrop. Ноль владельцев
// помещает ресурс в очередь отложенного уничтожения (обрабатывается в точке
// OpSweep, как и сборка объектов): промежуточный ноль при возврате значения из
// функции успевает перекрыться записью у вызывающего.
func (vm *VM) dropResourceRef(s Slot) {
	d := deref(s)
	if d.typ != ResourceType {
		return
	}
	r := AsResource(d)
	if r == nil || r.OnDrop == nil {
		return
	}
	r.Refs--
	if r.Refs <= 0 && !r.dropped {
		r.dropped = true
		vm.resDropPending = true
		vm.resDrops = append(vm.resDrops, r)
		// Финализатор выполняется ближайшим проходом (точка OpSweep): будим
		// проход как срочного кандидата, чтобы код точки не обрастал
		// отдельными проверками (perf-хрупкий участок).
		vm.lifeCandidate = true
		vm.lifeUrgent = true
	}
}

// flushResourceDrops выполняет отложенные финализаторы ресурсов.
func (vm *VM) flushResourceDrops() {
	if !vm.resDropPending {
		return
	}
	vm.resDropPending = false
	drops := vm.resDrops
	vm.resDrops = vm.resDrops[:0]
	for _, r := range drops {
		if r == nil || !r.dropped || r.Refs > 0 {
			continue
		}
		if f := r.OnDrop; f != nil {
			r.OnDrop = nil
			f()
		}
	}
}

// decObject списывает ссылку регистра на объект; при обнулении ставит
// кандидата (и urgent при наличии __destruct/спец-объекта).
func (vm *VM) decObject(o *Object) {
	if o == nil || o.dead {
		return
	}
	o.refs--
	vm.afterDecObject(o)
}

// afterDecObject обрабатывает уменьшение оценки ссылок объекта: ноль делает
// его кандидатом на проход сборщика, а с __destruct/спец-объектом — срочным.
// Если запись произошла внутри выполняющегося деструктора, объект уничтожается
// сразу (Zend: вложенное уничтожение; порядок деструкторов и хендлов
// наблюдаем — GH-10168).
func (vm *VM) afterDecObject(o *Object) {
	if o == nil || o.dead || o.refs > 0 {
		return
	}
	// Ноль достигнут внутри выполняющегося деструктора: Zend уничтожает такой
	// объект по refcount (немедленно), а не как «белый» цикл — в число
	// собранных gc_collect_cycles он не попадает.
	if vm.destroying > 0 {
		o.gcNested = true
	}
	vm.lifeCandidate = true
	if classHasDtor(o.class) || vm.isLifetimeSpecial(o) || o.TypedRefs() {
		vm.lifeUrgent = true
		if vm.destroying > 0 {
			vm.destroyDroppedNow(o)
		}
	}
}

// droppedObjUrgent помечает снятый объект кандидатом немедленного прохода:
// Zend уничтожает старое значение в точке записи, поэтому проход сборщика
// должен состояться на ближайшей точке OpSweep, даже если оценка ссылок ещё
// положительна (учёт бывает завышен синхронизацией с маркировкой).
func (vm *VM) droppedObjUrgent(old Slot) {
	if vm.destroying > 0 {
		return // вложенный случай обрабатывает nestedDropped
	}
	d := deref(old)
	if d.typ != ObjectType {
		return
	}
	o := d.obj()
	if o == nil || o.dead {
		return
	}
	if classHasDtor(o.class) || vm.isLifetimeSpecial(o) {
		vm.lifeCandidate = true
		vm.lifeUrgent = true
	}
}

// nestedDropped — точка записи ЗАМЕНИЛА значение ячейки-ссылки. Внутри
// выполняющегося деструктора Zend уничтожает снятое значение сразу, даже
// если оценка ссылок ещё положительна. Точную достижимость проверяет
// destroyDroppedNow.
func (vm *VM) nestedDropped(old Slot) {
	if vm.destroying == 0 {
		return
	}
	d := deref(old)
	if d.typ != ObjectType {
		return
	}
	o := d.obj()
	if o == nil || o.dead {
		return
	}
	if !classHasDtor(o.class) && !vm.isLifetimeSpecial(o) {
		return
	}
	vm.destroyDroppedNow(o)
}

// destroyDroppedNow уничтожает объект, потерявший последнюю (по оценке)
// ссылку внутри деструктора. Оценка может быть занижена (COW-массивы,
// замыкания), поэтому перед уничтожением выполняется точная проверка
// достижимости от корней; достижимый объект остаётся (счётчик затем
// синхронизирует ближайший проход сборщика).
func (vm *VM) destroyDroppedNow(o *Object) {
	if o == nil || o.dead || o.gcPending {
		return
	}
	if !vm.lifetimeAllowed() {
		return
	}
	m := newLifeVisit(vm)
	pc := vm.ip - 1
	if pc < 0 {
		pc = 0
	}
	vm.markRoots(m, vm.chunk, pc)
	m.finishWeakMaps()
	if m.objs[o] {
		return
	}
	if err := vm.destroyObject(o); err != nil {
		if vm.pendingDestroyErr == nil {
			vm.pendingDestroyErr = err
		}
	}
}

// wDropReg снимает учёт старого значения регистра перед записью скаляра
// (быстрые пути арифметики: d.typ, d.num, d.ptr = ...). Новое значение
// скалярно и учёта не требует.
func (vm *VM) wDropReg(i int) {
	f := vm.cur
	if f == nil || uint(i) >= maxRegsHard {
		return
	}
	w := &f.tracked[i>>6]
	bit := uint64(1) << uint(i&63)
	if *w&bit != 0 {
		*w &^= bit
		f.nTracked--
		vm.decRefs(f.regs[i], 0)
	}
}

// dropTempRegs списывает учтённые регистры кадра, кроме именованных
// переменных (chunk.varRegs): имитирует освобождение временных операндов при
// раскрутке непойманного исключения из main (Zend освобождает их до печати
// ошибки — bug69740: генератор-временный foreach закрывается с finally).
func (vm *VM) dropTempRegs(chunk *Chunk, pc int) {
	f := vm.cur
	if f == nil || chunk == nil {
		return
	}
	nwords := (chunk.numRegs + 63) / 64
	if nwords > len(f.tracked) {
		nwords = len(f.tracked)
	}
	keep := chunk.varRegs
	if chunk.hasUserVarRegs {
		keep = chunk.userVarRegs
	}
	for w := 0; w < nwords; w++ {
		m := f.tracked[w]
		if m == 0 {
			continue
		}
		if w < len(keep) {
			m &^= keep[w]
		}
		if m == 0 {
			continue
		}
		f.tracked[w] &^= m
		f.nTracked -= int32(bits.OnesCount64(m))
		for m != 0 {
			r := w*64 + bits.TrailingZeros64(m)
			m &= m - 1
			if r >= len(f.regs) {
				continue
			}
			if v := deref(f.regs[r]); v.typ == ObjectType {
				vm.decObject(v.obj())
			} else if v.typ == ResourceType {
				vm.dropResourceRef(v)
			}
			f.regs[r] = MakeNull()
		}
	}
}

// dropDeadRegs списывает учёт регистров, значения которых в точке pc мертвы
// по SSA-маскам: мёртвый регистр не должен удерживать объект от __destruct.
// Побитовая маска «мёртвых» регистров считается из SSA-маски живости и
// varRegs (именованные переменные не списываются); при нулевой маске работа —
// несколько сравнений на слово, без обхода битов.
func (vm *VM) dropDeadRegs(chunk *Chunk, pc int) {
	f := vm.cur
	if f == nil || chunk == nil || chunk.liveMasks == nil {
		return
	}
	off := pc * chunk.liveStride
	if pc < 0 || chunk.liveStride != liveMaskStride || chunk.numRegs <= 0 ||
		off < 0 || off+len(f.tracked) > len(chunk.liveMasks) {
		// pc вне таблицы живости (ручные чанки/нестандартный вызов) —
		// консервативно, поштучно.
		vm.dropDeadRegsSlow(chunk, pc)
		return
	}
	// Биты учёта выставляются только регистрам инструкций чанка, поэтому
	// за пределами numRegs их нет и обходить лишние слова не нужно.
	nwords := (chunk.numRegs + 63) / 64
	if nwords > len(f.tracked) {
		nwords = len(f.tracked)
	}
	for w := 0; w < nwords; w++ {
		m := f.tracked[w]
		if m == 0 {
			continue
		}
		m &^= chunk.liveMasks[off+w] | chunk.varRegs[w]
		if m == 0 {
			continue
		}
		f.tracked[w] &^= m
		f.nTracked -= int32(bits.OnesCount64(m))
		for m != 0 {
			r := w*64 + bits.TrailingZeros64(m)
			m &= m - 1
			// Счётчик удерживают только объекты: массивы/замыкания при
			// записи в регистр не считаются (incRefs — no-op), а их
			// поэлементный обход стоил бы O(размера) на каждом переходе
			// живости.
			if v := deref(f.regs[r]); v.typ == ObjectType {
				vm.decObject(v.obj())
			} else if v.typ == ResourceType {
				vm.dropResourceRef(v)
			}
		}
	}
}

// deadTrackedWide — общий случай проверки «есть списываемый учтённый
// регистр» (широкий регистровый файл, ручные чанки). Консервативен там, где
// таблица живости неприменима: возвращает true.
func (vm *VM) deadTrackedWide(chunk *Chunk, pc int) bool {
	f := vm.cur
	if f == nil || chunk == nil || chunk.liveStride != liveMaskStride {
		return true
	}
	off := pc * chunk.liveStride
	if pc < 0 || off < 0 || off+len(f.tracked) > len(chunk.liveMasks) {
		return true
	}
	nwords := (chunk.numRegs + 63) / 64
	if nwords > len(f.tracked) {
		nwords = len(f.tracked)
	}
	if nwords <= 0 {
		return true
	}
	var m uint64
	for w := 0; w < nwords; w++ {
		m |= f.tracked[w] &^ (chunk.liveMasks[off+w] | chunk.varRegs[w])
	}
	return m != 0
}

// dropDeadRegsSlow — поштучный вариант для pc вне таблицы живости.
func (vm *VM) dropDeadRegsSlow(chunk *Chunk, pc int) {
	f := vm.cur
	for w := range f.tracked {
		m := f.tracked[w]
		for m != 0 {
			r := w*64 + bits.TrailingZeros64(m)
			m &= m - 1
			if r < len(f.regs) && chunk.varRegs[r>>6]&(1<<uint(r&63)) == 0 && !chunk.liveRegAt(pc, r) {
				f.tracked[w] &^= uint64(1) << uint(r&63)
				f.nTracked--
				if v := deref(f.regs[r]); v.typ == ObjectType {
					vm.decObject(v.obj())
				} else if v.typ == ResourceType {
					vm.dropResourceRef(v)
				}
			}
		}
	}
}

// dropFrameIfTracked — dropFrame только при непустой карте учёта: вызовы без
// учитываемых значений (типичный случай) не платят за проход.
func (vm *VM) dropFrameIfTracked(f *Frame) {
	if f == nil || f.nTracked == 0 {
		return
	}
	vm.dropFrame(f)
}

// dropFrame списывает ссылки, удерживаемые отслеживаемыми регистрами кадра
// (обычно 0–2 значения), и очищает карту. Вызывается при выходе из кадра:
// нормальный возврат, исключительное раскручивание, конец области
// генератора/файбера.
func (vm *VM) dropFrame(f *Frame) {
	if f == nil {
		return
	}
	f.nTracked = 0
	for w := range f.tracked {
		m := f.tracked[w]
		f.tracked[w] = 0
		for m != 0 {
			r := w*64 + bits.TrailingZeros64(m)
			m &= m - 1
			if r < len(f.regs) {
				vm.decRefs(f.regs[r], 0)
				// Отпускаем значение: слот кадра нарезан из общего regPool, и
				// если оставить в нём объект, Go GC будет держать его живым до
				// следующей перезаписи этого слота.
				f.regs[r] = MakeNull()
			}
		}
	}
}

// FlushLifetime немедленно выполняет проход сборщика, если есть кандидаты.
func (vm *VM) FlushLifetime() {
	if (!vm.lifeCandidate && vm.zeroObjs <= 0) || vm.sweepBusy {
		return
	}
	_ = vm.sweepLifetime(false, vm.chunk, vm.ip-1)
}

// noteDroppedValue — как noteDropped для Value-границы (значения свойств).
func (vm *VM) noteDroppedValue(v Value) {
	if s, ok := v.(Slot); ok {
		vm.noteDropped(s)
	}
}

// noteStoredValue — как noteStored для Value-границы.
func (vm *VM) noteStoredValue(v Value) {
	if s, ok := v.(Slot); ok {
		vm.noteStored(s)
	}
}

// lifeVisit — фаза пометки: обход объектов/массивов/замыканий от корней.
type lifeVisit struct {
	vm   *VM
	objs map[*Object]bool
	arrs map[*Array]bool
	clos map[*Closure]bool
	// refs — уже учтённые ячейки-ссылки: алиасы одной ячейки (переменная и
	// захват `use (&$x)`) не должны считаться разными владельцами объекта.
	refs map[*Ref]bool
	// inRefs — сколько ссылок на объект найдено в живом графе (корни +
	// достижимые контейнеры). После прохода refs выживших синхронизируются с
	// этим числом, чтобы оценка не «забывала» ссылки и не плодила ложных
	// кандидатов.
	inRefs map[*Object]int32

	// wmaps — встреченные WeakMap: их значения трактуются как сильные ссылки,
	// только если жив ключ записи (Zend gc: zend_weakmap_get_key_entry_gc),
	// поэтому обход значений откладывается до finishWeakMaps.
	wmaps []*Object

	// reachDead — обходить и уничтоженные (dead) объекты: проверка
	// воскрешения после деструкторов (gc_041): $GLOBALS['x'] = $this.
	reachDead bool

	// depth — защита от абсурдно глубокой рекурсии рефлексии.
	depth int
}

var (
	slotType      = reflect.TypeOf(Slot{})
	objectType    = reflect.TypeOf(Object{})
	arrayPtrType  = reflect.TypeOf((*Array)(nil))
	objPtrType    = reflect.TypeOf((*Object)(nil))
	closPtrType   = reflect.TypeOf((*Closure)(nil))
	rclassPtrType = reflect.TypeOf((*runtimeClass)(nil))
)

func newLifeVisit(vm *VM) *lifeVisit {
	return &lifeVisit{
		vm:     vm,
		objs:   map[*Object]bool{},
		arrs:   map[*Array]bool{},
		clos:   map[*Closure]bool{},
		refs:   map[*Ref]bool{},
		inRefs: map[*Object]int32{},
	}
}

// slot помечает значение слота (разворачивая ссылки).
func (m *lifeVisit) slot(s Slot) {
	for i := 0; s.typ == RefType && i < 100; i++ {
		r := s.Ref()
		if r == nil {
			return
		}
		s = r.v
	}
	if s.typ == RefType {
		// Алиас переменной: объект учитывается один раз на ячейку.
		r := s.Ref()
		if r == nil {
			return
		}
		if m.refs[r] {
			m.slotBare(s)
			return
		}
		m.refs[r] = true
		s = deref(s)
	}
	switch s.typ {
	case ObjectType:
		if o := s.obj(); o != nil {
			m.inRefs[o]++
			m.obj(o)
		}
	case ClosureType:
		m.closure(s.asClosure())
	case ArrayType:
		m.array(s.AsArray())
	}
}

// slotMirror помечает значение из снимка $GLOBALS: массив-зеркало не должен
// учитывать прямые ссылки (они уже учтены по таблице globals), но объекты,
// на которые он указывает, обходятся как обычно.
func (m *lifeVisit) slotMirror(s Slot) {
	for i := 0; s.typ == RefType && i < 100; i++ {
		r := s.Ref()
		if r == nil {
			return
		}
		s = r.v
	}
	switch s.typ {
	case ObjectType:
		m.obj(s.obj())
	case ClosureType:
		m.closure(s.asClosure())
	case ArrayType:
		a := s.AsArray()
		if a == nil || m.arrs[a] {
			return
		}
		m.arrs[a] = true
		if !a.objRefs {
			return
		}
		for _, v := range a.All() {
			m.slotMirror(v)
		}
	}
}

// slotBare помечает значение, не учитывая его в inRefs: слоты регистров и
// прочие «временные» места, чья потеря не отслеживается счётчиком ссылок.
func (m *lifeVisit) slotBare(s Slot) {
	for i := 0; s.typ == RefType && i < 100; i++ {
		r := s.Ref()
		if r == nil {
			return
		}
		s = r.v
	}
	switch s.typ {
	case ObjectType:
		m.obj(s.obj())
	case ClosureType:
		m.closure(s.asClosure())
	case ArrayType:
		m.array(s.AsArray())
	}
}

func (m *lifeVisit) objBare(o *Object) { m.obj(o) }

func (m *lifeVisit) obj(o *Object) {
	if o == nil || m.objs[o] || (o.dead && !m.reachDead) {
		return
	}
	m.objs[o] = true
	if o.class == nil {
		return
	}
	// Слабые ссылки: ключи WeakMap и цель WeakReference не корни.
	switch o.ClassName() {
	case "WeakMap":
		if d := objectAs[weakMapData](o, o.class); d != nil {
			_ = d
			m.wmaps = append(m.wmaps, o)
			return
		}
	case "WeakReference":
		if d := objectAs[weakReferenceData](o, o.class); d != nil {
			_ = d
			return
		}
	}
	o.forEachPropValue(func(s Slot) { m.slot(s) })
	m.native(o)
}

// finishWeakMaps завершает марк-фазу: значения WeakMap помечаются только
// тогда, когда жив их ключ (Zend: zend_weakmap_get_key_entry_gc — запись не
// держит ключ, но и ключ не держит запись своим значением). Пометка значения
// может открыть новые карты/ключи, поэтому проход идёт до неподвижной точки
// (wmaps дополняется во время обхода).
func (m *lifeVisit) finishWeakMaps() {
	for i := 0; i < len(m.wmaps); i++ {
		o := m.wmaps[i]
		if o == nil || o.dead || o.class == nil {
			continue
		}
		d := objectAs[weakMapData](o, o.class)
		if d == nil {
			continue
		}
		for j := range d.entries {
			key := deref(d.entries[j].obj).obj()
			if key == nil || key.dead || !m.objs[key] {
				continue
			}
			m.slot(d.entries[j].val)
		}
	}
}

// value помечает Value-интерфейс (значение свойства).
func (m *lifeVisit) value(v Value) {
	if v == nil {
		return
	}
	if s, ok := v.(Slot); ok {
		m.slot(s)
		return
	}
	m.rvalue(reflect.ValueOf(v), 0)
}

func (m *lifeVisit) closure(c *Closure) {
	if c == nil || m.clos[c] {
		return
	}
	m.clos[c] = true
	for _, s := range c.Captures {
		m.slot(s)
	}
	m.slot(c.BoundThis)
}

func (m *lifeVisit) array(a *Array) {
	if a == nil || m.arrs[a] {
		return
	}
	m.arrs[a] = true
	if !a.objRefs {
		return
	}
	for _, v := range a.All() {
		m.slot(v)
	}
}

// native обходит нативные данные объекта рефлексией: любые поля Slot,
// *Object, *Array, *Closure, вложенные структуры/срезы/карты.
func (m *lifeVisit) native(o *Object) {
	rc, ok := o.class.(*runtimeClass)
	if !ok || rc == nil || rc.nativeType == nil {
		return
	}
	v := reflect.NewAt(rc.nativeType, unsafe.Pointer(o)).Elem()
	m.rvalue(v, 0)
}

// rvalue рекурсивно помечает ссылки внутри произвольного Go-значения.
func (m *lifeVisit) rvalue(v reflect.Value, depth int) {
	if !v.IsValid() || depth > 12 {
		return
	}
	m.depth = depth
	t := v.Type()
	if t == slotType {
		m.slot(slotFromReflect(v))
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		switch t {
		case rclassPtrType:
			return
		}
		switch t {
		case arrayPtrType:
			m.array((*Array)(v.UnsafePointer()))
			return
		case objPtrType:
			m.obj((*Object)(v.UnsafePointer()))
			return
		case closPtrType:
			m.closure((*Closure)(v.UnsafePointer()))
			return
		}
		et := t.Elem()
		if et.Kind() == reflect.Struct && et.NumField() > 0 && et.Field(0).Type == objectType {
			// Указатель на нативное хранилище (напр. Generator.inner).
			m.obj((*Object)(v.UnsafePointer()))
			return
		}
		m.rvalue(v.Elem(), depth+1)
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		ev := v.Elem()
		// Инфраструктурные интерфейсы (ClassInfo и т.п.) пропускаем.
		if ev.Kind() == reflect.Pointer {
			switch ev.Type() {
			case rclassPtrType:
				return
			}
			if ev.Type() == reflect.TypeOf((*VM)(nil)) || ev.Type() == reflect.TypeOf((*Chunk)(nil)) || ev.Type() == reflect.TypeOf((*Function)(nil)) {
				return
			}
		}
		m.rvalue(ev, depth+1)
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" && !f.Anonymous && f.Type != slotType && !containsHeapField(f.Type) {
				// Неэкспортированные скалярные поля пропускаем; структуры,
				// которые могут держать слоты, всё равно обходим ниже.
			}
			fv := v.Field(i)
			switch fv.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
				reflect.Float32, reflect.Float64, reflect.Bool, reflect.String,
				reflect.Chan, reflect.Func:
				continue
			}
			m.rvalue(fv, depth+1)
		}
	case reflect.Slice, reflect.Array:
		if v.Len() == 0 {
			return
		}
		if t.Elem() == slotType {
			for i := 0; i < v.Len(); i++ {
				m.slot(slotFromReflect(v.Index(i)))
			}
			return
		}
		if t.Elem() == objectType {
			for i := 0; i < v.Len(); i++ {
				m.obj((*Object)(unsafe.Pointer(v.Index(i).UnsafeAddr())))
			}
			return
		}
		for i := 0; i < v.Len(); i++ {
			m.rvalue(v.Index(i), depth+1)
		}
	case reflect.Map:
		if v.IsNil() {
			return
		}
		it := v.MapRange()
		for it.Next() {
			m.rvalue(it.Value(), depth+1)
		}
	}
}

// containsHeapField — грубая проверка, может ли тип содержать Slot.
func containsHeapField(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == slotType {
		return true
	}
	switch t.Kind() {
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if containsHeapField(t.Field(i).Type) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		return containsHeapField(t.Elem())
	}
	return false
}

// slotFromReflect собирает Slot из отражённого значения (без Interface(),
// чтобы работать и с неэкспортированными полями).
func slotFromReflect(v reflect.Value) Slot {
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	var s Slot
	// Поля Slot: typ Type (uint8), num uint64, ptr unsafe.Pointer.
	t := v.Type()
	for i := 0; i < t.NumField() && i < 3; i++ {
		switch i {
		case 0:
			s.typ = Type(v.Field(0).Uint())
		case 1:
			s.num = v.Field(1).Uint()
		case 2:
			if p := v.Field(2); p.Kind() == reflect.UnsafePointer {
				s.ptr = p.UnsafePointer()
			}
		}
	}
	return s
}

// --- Сбор ---

// lifetimeSweep выполняет один проход сборщика. force=true (gc_collect_cycles)
// дополнительно уничтожает циклы. Возвращает ошибку деструктора (error-
// протокол) и, для gc, число собранных узлов.
func (vm *VM) lifetimeSweep(force bool, chunk *Chunk, pc int) execResult {
	n := vm.sweepLifetime(force, chunk, pc)
	if n.err != nil {
		return execResult{err: n.err}
	}
	return execResult{}
}

type sweepOutcome struct {
	err   error
	count int
}

// sweepLifetime — основной проход.
func (vm *VM) sweepLifetime(force bool, chunk *Chunk, pc int) sweepOutcome {
	if vm.sweepBusy {
		return sweepOutcome{}
	}
	// Финализаторы ресурсов (процесс proc_open, потерявший владельца)
	// выполняются до марк-фазы.
	if vm.resDropPending {
		vm.flushResourceDrops()
	}
	// Флаги относятся к текущему проходу; новые кандидаты, найденные во время
	// уничтожения (например, значение удалённой записи WeakMap), выставят их
	// снова — вызывающий цикл выполнит ещё один проход.
	vm.lifeCandidate = false
	vm.lifeUrgent = false
	defer vm.compactLiveObjs()
	m := newLifeVisit(vm)
	vm.markRoots(m, chunk, pc)
	m.finishWeakMaps()

	// Непомеченные объекты (все, что получили хендл). При принудительном
	// сборе к ним добавляется отложенный циклический мусор: его массивы тоже
	// должны попасть в подсчёт.
	uObjs := make([]*Object, 0, 16)
	for _, o := range vm.liveObjs {
		if o == nil || o.dead || o.gcPending || m.objs[o] {
			if o != nil {
			}
			continue
		}
		uObjs = append(uObjs, o)
	}
	if force {
		uObjs = append(uObjs, vm.gcPendingObjs...)
	}
	// Непомеченные массивы, достижимые из непомеченных объектов.
	uArrs := map[*Array]bool{}
	var discoverArr func(a *Array)
	discoverArr = func(a *Array) {
		if a == nil || m.arrs[a] || uArrs[a] {
			return
		}
		uArrs[a] = true
		if !a.objRefs {
			return
		}
		for _, v := range a.All() {
			vm.discoverFromSlot(m, v, &uObjs, discoverArr)
		}
	}
	for _, o := range uObjs {
		vm.discoverFromObject(m, o, &uObjs, discoverArr)
	}
	// Оценки ссылок выживших синхронизируются с числом ссылок, найденных в
	// живом графе (не меньше 1): иначе перезапись/сброс счётчика плодила бы
	// ложных кандидатов и лишние проходы.
	for o := range m.objs {
		old := o.refs
		o.refs = m.inRefs[o]
		if old <= 0 && o.refs > 0 {
			vm.zeroObjs--
		} else if old > 0 && o.refs <= 0 {
			vm.zeroObjs++
		}
	}
	if !force && len(uObjs) == 0 {
		return sweepOutcome{}
	}

	// Входящие ссылки внутри недостижимого множества (аналог refcount).
	indeg := map[*Object]int{}
	for _, o := range uObjs {
		indeg[o] = 0
	}
	var decr func(Slot)
	seenDec := map[*Array]bool{}
	seenDecClos := map[*Closure]bool{}
	decr = func(s Slot) {
		for i := 0; s.typ == RefType && i < 100; i++ {
			r := s.Ref()
			if r == nil {
				return
			}
			s = r.v
		}
		switch s.typ {
		case ObjectType:
			if o := s.obj(); o != nil {
				if _, ok := indeg[o]; ok {
					indeg[o]++
				}
			}
		case ClosureType:
			// Захваты недостижимого замыкания считаются входящими ссылками:
			// цикл «объект ↔ замыкание в его свойстве/файбере» обязан
			// дожидаться gc_collect_cycles, а не разрываться в обычном
			// проходе (fibers/gc-cycle-callback).
			if c := s.asClosure(); c != nil && !seenDecClos[c] {
				seenDecClos[c] = true
				for _, cs := range c.Captures {
					decr(cs)
				}
				decr(c.BoundThis)
			}
		case ArrayType:
			if a := s.AsArray(); a != nil {
				if _, ok := uArrs[a]; ok && !seenDec[a] {
					seenDec[a] = true
					for _, v := range a.All() {
						decr(v)
					}
				}
			}
		}
	}
	for _, o := range uObjs {
		vm.walkObjectRefsOwned(o, decr)
	}
	for a := range uArrs {
		// Массивы, уже пройденные через слот объекта (seenDec), учтены выше:
		// повторный обход их элементов удвоил бы входные ссылки.
		if seenDec[a] {
			continue
		}
		for _, v := range a.All() {
			decr(v)
		}
	}

	// Каскад: уничтожаем узлы с нулевым входом, освобождая ссылки.
	queue := make([]*Object, 0, len(uObjs))
	for _, o := range uObjs {
		if indeg[o] == 0 {
			queue = append(queue, o)
		}
	}
	vm.sweepBusy = true
	defer func() { vm.sweepBusy = false }()
	collected := 0
	for len(queue) > 0 {
		o := queue[0]
		queue = queue[1:]
		if o.dead {
			continue
		}
		// Освобождаем исходящие ссылки объекта.
		var err error
		vm.walkObjectRefs(o, func(s Slot) {
			vm.releaseUnreachable(s, indeg, &queue)
		})
		if derr := vm.destroyObject(o); derr != nil {
			err = derr
			vm.finishSweep()
			return sweepOutcome{err: err, count: collected}
		}
		// Смерть по refcount (нулевой вход): Zend уничтожил бы такой узел в
		// точке записи, gc_collect_cycles его не считает.
	}
	if force {
		// Циклы: уничтожаем всё оставшееся (Zend: gc_collect_cycles).
		dtorsBefore := vm.gcDtorCount
		var countedObjs []*Object
		var countedArrs []*Array
		var sweepErr error
		for _, o := range uObjs {
			if o.dead {
				continue
			}
			// gcNested — ноль достигнут внутри деструктора: Zend уничтожает
			// такой объект по refcount, в счёт он не входит (gc_017/gc_028),
			// но деструктор всё равно должен выполниться.
			nested := o.gcNested
			if derr := vm.destroyObject(o); derr != nil {
				// Zend продолжает вызывать деструкторы остальных узлов и
				// сцепляет исключения (previous), печатая цепочку в конце
				// (gc_030).
				if nth, ok := asThrowable(derr); ok {
					if prev, ok2 := asThrowable(sweepErr); ok2 {
						vm.chainPrevious(nth, prev)
					}
					sweepErr = derr
				} else if sweepErr == nil {
					sweepErr = derr
				}
				continue
			}
			if !nested {
				countedObjs = append(countedObjs, o)
			}
		}
		// Массивы циклического мусора Zend тоже считает собранными.
		seenA := map[*Array]bool{}
		seenO := map[*Object]bool{}
		// Нативные массивы собранных объектов (storage ArrayObject/ArrayIterator
		// и т.п.): Zend get_gc возвращает их как отдельные белые узлы, и они
		// входят в счёт (gc_024: объект + его storage = 2).
		arrSeen := map[*Array]bool{}
		for _, o := range countedObjs {
			vm.walkObjectNativeArrays(o, func(a *Array) {
				if a == nil || arrSeen[a] || m.arrs[a] {
					return
				}
				arrSeen[a] = true
				countedArrs = append(countedArrs, a)
			})
		}
		for a := range uArrs {
			if seenA[a] || arrSeen[a] {
				continue
			}
			if vm.arrayCyclic(a, seenA, seenO) {
				arrSeen[a] = true
				countedArrs = append(countedArrs, a)
			}
		}
		for a := range vm.cyclicArrs {
			if seenA[a] || arrSeen[a] {
				continue
			}
			seenA[a] = true
			if m.arrs[a] {
				// Массив ещё достижим: цикл жив, ждём следующего сбора.
				continue
			}
			// Проверка с ЧИСТЫМИ картами: общий seenA от предыдущих обходов
			// мог «поглотить» этот массив (порядок map-итерации), и тогда
			// циклический мусор не посчитался бы (gc_022).
			if !vm.arrayCyclic(a, map[*Array]bool{}, map[*Object]bool{}) {
				// Регистрация устарела: отвязка ссылки (executor PZVAL_UNLOCK,
				// присваивание через ту же ячейку) сняла само-цикл — массив
				// умер по refcount (gc_020).
				delete(vm.cyclicArrs, a)
				continue
			}
			arrSeen[a] = true
			countedArrs = append(countedArrs, a)
			delete(vm.cyclicArrs, a)
		}
		collected = len(countedObjs) + len(countedArrs)
		if vm.gcDtorCount != dtorsBefore && collected > 0 {
			// Деструкторы могли воскресить узлы ($GLOBALS['x'] = $this):
			// достижимые после их выполнения Zend не считает собранными
			// (gc_041).
			collected -= vm.gcUncountResurrected(countedObjs, countedArrs, chunk, pc)
		}
		vm.gcPendingObjs = vm.gcPendingObjs[:0]
		if sweepErr != nil {
			vm.finishSweep()
			return sweepOutcome{err: sweepErr, count: collected}
		}
		return sweepOutcome{count: collected}
	}
	// Оставшиеся недостижимые — циклический мусор (или достижимые только из
	// него): Zend не уничтожает их до сборки циклов. Переносим в отдельный
	// список, чтобы не сканировать на каждом проходе.
	if len(queue) == 0 {
		for _, o := range uObjs {
			if o.dead || o.gcPending {
				continue
			}
			if o.refs <= 0 {
				vm.zeroObjs--
			}
			o.gcPending = true
			vm.gcPendingObjs = append(vm.gcPendingObjs, o)
		}
	}
	return sweepOutcome{count: collected}
}

// gcUncountResurrected возвращает число собранных узлов, которые после фазы
// деструкторов снова достижимы от корней (деструктор записал $this в глобалы —
// gc_041). Обход не останавливается на уничтоженных объектах (reachDead).
func (vm *VM) gcUncountResurrected(objs []*Object, arrs []*Array, chunk *Chunk, pc int) int {
	m := newLifeVisit(vm)
	m.reachDead = true
	vm.markRoots(m, chunk, pc)
	m.finishWeakMaps()
	n := 0
	for _, o := range objs {
		if o != nil && m.objs[o] {
			n++
		}
	}
	for _, a := range arrs {
		if a != nil && m.arrs[a] {
			n++
		}
	}
	return n
}

// compactLiveObjs удаляет уничтоженные объекты из реестра: иначе каждый
// проход (и уведомление WeakMap) сканировал бы всё, что когда-либо жило.
func (vm *VM) compactLiveObjs() {
	w := 0
	for _, o := range vm.liveObjs {
		if o != nil && !o.dead && !o.gcPending {
			vm.liveObjs[w] = o
			w++
		}
	}
	for i := w; i < len(vm.liveObjs); i++ {
		vm.liveObjs[i] = nil
	}
	vm.liveObjs = vm.liveObjs[:w]
}

// finishSweep сбрасывает флаг кандидатов после обработки.
func (vm *VM) finishSweep() {
	vm.lifeCandidate = false
	vm.lifeUrgent = false
}

// releaseUnreachable уменьшает счётчик входящих ссылок и добавляет в очередь
// обнулившиеся узлы.
func (vm *VM) releaseUnreachable(s Slot, indeg map[*Object]int, queue *[]*Object) {
	vm.releaseUnreachableSeen(s, indeg, queue, map[*Array]bool{})
}

func (vm *VM) releaseUnreachableSeen(s Slot, indeg map[*Object]int, queue *[]*Object, seen map[*Array]bool) {
	for i := 0; s.typ == RefType && i < 100; i++ {
		r := s.Ref()
		if r == nil {
			return
		}
		s = r.v
	}
	switch s.typ {
	case ObjectType:
		if o := s.obj(); o != nil {
			if n, ok := indeg[o]; ok {
				n--
				if n <= 0 {
					indeg[o] = 0
					*queue = append(*queue, o)
				} else {
					indeg[o] = n
				}
			}
		}
	case ArrayType:
		if a := s.AsArray(); a != nil && !seen[a] {
			seen[a] = true
			for _, v := range a.All() {
				vm.releaseUnreachableSeen(v, indeg, queue, seen)
			}
		}
	}
}

// discoverFromSlot донаходит непомеченные объекты и массивы.
func (vm *VM) discoverFromSlot(m *lifeVisit, s Slot, uObjs *[]*Object, discoverArr func(*Array)) {
	for i := 0; s.typ == RefType && i < 100; i++ {
		r := s.Ref()
		if r == nil {
			return
		}
		s = r.v
	}
	switch s.typ {
	case ObjectType:
		if o := s.obj(); o != nil && !o.dead && !m.objs[o] {
			m.objs[o] = true // локальная пометка, чтобы не дублировать
			*uObjs = append(*uObjs, o)
			vm.discoverFromObject(m, o, uObjs, discoverArr)
		}
	case ArrayType:
		if a := s.AsArray(); a != nil && !m.arrs[a] {
			discoverArr(a)
		}
	}
}

// discoverFromObject обходит ссылки непомеченного объекта.
func (vm *VM) discoverFromObject(m *lifeVisit, o *Object, uObjs *[]*Object, discoverArr func(*Array)) {
	vm.walkObjectRefs(o, func(s Slot) {
		vm.discoverFromSlot(m, s, uObjs, discoverArr)
	})
}

// markRoots помечает всё, достижимое из корней VM.
func (vm *VM) markRoots(m *lifeVisit, chunk *Chunk, pc int) {
	// Регистры текущего кадра: только живые (SSA-маски). Регистры не
	// учитываются в inRefs (счётчик ссылок отслеживает только контейнеры).
	if vm.cur != nil {
		vm.markFrameRegs(m, vm.cur, chunk, pc)
	}

	// Вызывающие кадры 0..fp-1. Каждая запись callStack описывает вызов из
	// текущего кадра-вызывающего: userland-вызов создаёт следующий кадр,
	// нативный — нет.
	frame := 0
	for i := range vm.callStack {
		e := &vm.callStack[i]
		if e.this.typ != NullType {
			m.slotBare(e.this)
		}
		for _, a := range e.args {
			m.slotBare(a)
		}
		// Позиция кадра-вызывающего в момент вызова.
		var ch *Chunk
		if e.callerChunk != 0 {
			ch = (*Chunk)(unsafe.Pointer(e.callerChunk))
		}
		if frame < vm.fp && frame < len(vm.frames) {
			f := &vm.frames[frame]
			vm.markFrameRegs(m, f, ch, int(e.callerPC&^objMethodBit))
		}
		if !isNativeCallee(e.callee) {
			frame++
		}
	}
	// Если соответствие кадров и записей нарушено (напр. ещё не разобранные
	// пути), консервативно помечаем оставшиеся вызывающие кадры целиком
	// (текущий кадр fp уже помечен по маскам).
	for ; frame < vm.fp && frame < len(vm.frames); frame++ {
		vm.markFrameRegs(m, &vm.frames[frame], nil, 0)
	}

	for k, v := range vm.globals {
		// Снимок $GLOBALS — зеркало таблицы глобальных: учёт ссылок из него
		// давал бы каждому глобальному объекту лишнюю «ссылку» и откладывал
		// __destruct (снимок достижим и через самого себя только если им
		// пользуется код — тогда он обходится оттуда).
		if k == "$GLOBALS" {
			m.slotMirror(v)
			continue
		}
		m.slot(v)
	}
	for _, v := range vm.consts {
		m.slot(v)
	}
	m.slot(vm.errHandler)
	m.slot(vm.exceptionHandler)
	for i := range vm.exceptionHandlers {
		m.slot(vm.exceptionHandlers[i])
	}
	m.slotBare(vm.pendingThis)
	for _, s := range vm.autoloaders {
		m.slot(s)
	}
	for _, h := range vm.shutdownFuncs {
		m.slot(h.cb)
		for _, a := range h.args {
			m.slot(a)
		}
	}
	for _, s := range vm.headerCallbacks {
		m.slot(s)
	}
	for _, s := range vm.curFuncArgs {
		m.slotBare(s)
	}
	for _, s := range vm.pendingFuncArgs {
		m.slotBare(s)
	}
	if vm.namedVariadicTail != nil {
		m.array(vm.namedVariadicTail)
	}
	for _, ob := range vm.obStack {
		if ob != nil {
			m.slot(ob.callback)
		}
	}
	for _, g := range vm.runningGens {
		if g != nil {
			m.objBare(&g.Object)
		}
	}
	if vm.currentFiber != nil {
		m.objBare(&vm.currentFiber.Object)
	}
	if vm.forceClosingFiber != nil {
		m.objBare(&vm.forceClosingFiber.Object)
	}
	if vm.forceClosing != nil {
		m.objBare(&vm.forceClosing.Object)
	}
	if t := vm.ctx.thrown; t != nil {
		m.slotBare(t.Value)
	}
	// Отложенные finally-исключения и раскручиваемое сейчас исключение:
	// Zend держит их в fast_call-слотах/EG(exception) — это корни, пока
	// исключение не поймано/не перевозбуждено.
	for i := range vm.tries {
		if e := vm.tries[i].savedErr; e != nil {
			m.slotBare(e.Value)
		}
	}
	if vm.inFlightTh != nil {
		m.slotBare(vm.inFlightTh.Value)
	}
	// Статические свойства всех классов.
	for _, rc := range vm.staticRootClasses() {
		for k := rc; k != nil; k = k.parent {
			for _, s := range k.staticProps {
				if s != nil {
					m.slot(*s)
				}
			}
			for _, s := range k.enumCaseSlots {
				m.slot(s)
			}
		}
	}
	if p := vm.pendingDefaultErr; p != nil {
		m.rvalue(reflect.ValueOf(p), 0)
	}
	m.rvalue(reflect.ValueOf(vm.unserState), 0)
	m.rvalue(reflect.ValueOf(vm.serState), 0)
}

// staticRootClasses возвращает классы, у которых есть статические свойства
// или объекты-кейсы enum (кэш; сбрасывается при линковке новых классов).
func (vm *VM) staticRootClasses() []*runtimeClass {
	if vm.staticRootsCache != nil && vm.staticRootsLen == len(vm.classes) {
		return vm.staticRootsCache
	}
	var out []*runtimeClass
	for _, ci := range vm.classes {
		rc, ok := ci.(*runtimeClass)
		if !ok || rc == nil {
			continue
		}
		for k := rc; k != nil; k = k.parent {
			if len(k.staticProps) > 0 || len(k.enumCaseSlots) > 0 {
				out = append(out, rc)
				break
			}
		}
	}
	vm.staticRootsCache = out
	vm.staticRootsLen = len(vm.classes)
	return out
}

// markFrameRegs помечает живые регистры кадра; ch == nil — живость неизвестна.
func (vm *VM) markFrameRegs(m *lifeVisit, f *Frame, ch *Chunk, pc int) {
	if f == nil {
		return
	}
	// Корни — по фактическому состоянию регистров, а не по статическим маскам
	// живости: бит tracked выставлен writeback-учётом ровно тогда, когда в
	// регистре лежит учитываемое значение (объект/массив/замыкание/ссылка).
	// Это точнее масок (fallback-маски консервативны) и не зависит от того,
	// занулён ли слот.
	for w := range f.tracked {
		mask := f.tracked[w]
		for mask != 0 {
			i := w*64 + bits.TrailingZeros64(mask)
			mask &= mask - 1
			if i < len(f.regs) {
				m.slotBare(f.regs[i])
			}
		}
	}
	for _, v := range f.dynLocals {
		m.slotBare(v)
	}
	m.slotBare(f.this)
}

// walkObjectRefs вызывает f для каждой ссылки объекта на слот (props, нативные
// данные, захваты замыканий). Для WeakMap обходятся только значения.
func (vm *VM) walkObjectRefs(o *Object, f func(Slot)) {
	if o == nil || o.class == nil {
		return
	}
	switch o.ClassName() {
	case "WeakMap":
		if d := objectAs[weakMapData](o, o.class); d != nil {
			for i := range d.entries {
				f(d.entries[i].val)
			}
			return
		}
	case "WeakReference":
		return
	}
	o.forEachPropValue(f)
	// Нативные данные.
	rc, ok := o.class.(*runtimeClass)
	if !ok || rc == nil || rc.nativeType == nil {
		return
	}
	vv := reflect.NewAt(rc.nativeType, unsafe.Pointer(o)).Elem()
	walkReflect(vv, 0, f)
}

// walkObjectRefsOwned — как walkObjectRefs, но для файбера пропускает снимок
// возобновителя (resumer) и кэш его трейса (resumerTrace): это копии состояния
// живой стороны, а не владение файбера. Учёт ссылок из них создавал бы ложный
// цикл «файбер → resumer.frames/resumerTrace → файбер» и откладывал
// уничтожение (и принудительное закрытие) файбера до gc_collect_cycles
// (Zend/tests/fibers/suspend-in-force-close-fiber-catching-exception).
// Марк-фаза по-прежнему обходит их консервативно.
func (vm *VM) walkObjectRefsOwned(o *Object, fn func(Slot)) {
	if o == nil || o.class == nil {
		return
	}
	if vm.fiberClass != nil && o.class.isA(vm.fiberClass) {
		f := objectAs[Fiber](o, vm.fiberClass)
		o.forEachPropValue(fn)
		rc, ok := o.class.(*runtimeClass)
		if !ok || rc == nil || rc.nativeType == nil {
			return
		}
		vv := reflect.NewAt(rc.nativeType, unsafe.Pointer(o)).Elem()
		t := vv.Type()
		for i := 0; i < t.NumField(); i++ {
			fname := t.Field(i).Name
			switch fname {
			case "resumer", "resumerTrace":
				continue
			case "saved":
				if f != nil {
					walkFiberExecStateOwned(&f.saved, fn)
				}
				continue
			}
			walkReflect(vv.Field(i), 0, fn)
		}
		return
	}
	vm.walkObjectRefs(o, fn)
}

// walkReflect обходит Go-значение и вызывает f на каждом Slot.
func walkReflect(v reflect.Value, depth int, f func(Slot)) {
	if !v.IsValid() || depth > 12 {
		return
	}
	t := v.Type()
	if t == slotType {
		f(slotFromReflect(v))
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		switch t {
		case rclassPtrType, objPtrType, closPtrType:
			return // обрабатываются через props/владельца
		case arrayPtrType:
			// Нативное поле-массив (storage ArrayObject/ArrayIterator и т.п.):
			// его элементы — ссылки объекта, Zend get_gc их возвращает
			// (gc_024: цикл объект↔storage).
			if a := (*Array)(v.UnsafePointer()); a != nil && a.objRefs {
				for _, s := range a.All() {
					f(s)
				}
			}
			return
		}
		et := t.Elem()
		if et.Kind() == reflect.Struct && et.NumField() > 0 && et.Field(0).Type == objectType {
			return // вложенный нативный объект — обходится как объект
		}
		walkReflect(v.Elem(), depth+1, f)
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		ev := v.Elem()
		if ev.Kind() == reflect.Pointer {
			switch ev.Type() {
			case rclassPtrType:
				return
			}
		}
		walkReflect(ev, depth+1, f)
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			fv := v.Field(i)
			switch fv.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
				reflect.Float32, reflect.Float64, reflect.Bool, reflect.String,
				reflect.Chan, reflect.Func:
				continue
			}
			walkReflect(fv, depth+1, f)
		}
	case reflect.Slice, reflect.Array:
		if v.Len() == 0 {
			return
		}
		if t.Elem() == slotType {
			for i := 0; i < v.Len(); i++ {
				f(slotFromReflect(v.Index(i)))
			}
			return
		}
		if t.Elem() == objectType {
			for i := 0; i < v.Len(); i++ {
				// Вложенный объект: его слоты принадлежат ему, а не
				// владельцу; ссылка на объект уже видна через указатель.
				_ = i
			}
			return
		}
		for i := 0; i < v.Len(); i++ {
			walkReflect(v.Index(i), depth+1, f)
		}
	case reflect.Map:
		if v.IsNil() {
			return
		}
		it := v.MapRange()
		for it.Next() {
			walkReflect(it.Value(), depth+1, f)
		}
	}
}

// walkObjectNativeArrays вызывает f для нативных массивов объекта
// (поля типа *Array в нативных данных: storage ArrayObject и т.п.).
func (vm *VM) walkObjectNativeArrays(o *Object, f func(*Array)) {
	if o == nil || o.class == nil {
		return
	}
	rc, ok := o.class.(*runtimeClass)
	if !ok || rc == nil || rc.nativeType == nil {
		return
	}
	vv := reflect.NewAt(rc.nativeType, unsafe.Pointer(o)).Elem()
	walkNativeArrays(vv, 0, f)
}

// walkNativeArrays рекурсивно ищет поля-массивы в нативных данных.
func walkNativeArrays(v reflect.Value, depth int, f func(*Array)) {
	if !v.IsValid() || depth > 12 {
		return
	}
	t := v.Type()
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		if t == arrayPtrType {
			f((*Array)(v.UnsafePointer()))
			return
		}
		switch t {
		case rclassPtrType, objPtrType, closPtrType:
			return
		}
		et := t.Elem()
		if et.Kind() == reflect.Struct && et.NumField() > 0 && et.Field(0).Type == objectType {
			return
		}
		walkNativeArrays(v.Elem(), depth+1, f)
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		ev := v.Elem()
		if ev.Kind() == reflect.Pointer && ev.Type() == rclassPtrType {
			return
		}
		walkNativeArrays(ev, depth+1, f)
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			fv := v.Field(i)
			switch fv.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
				reflect.Float32, reflect.Float64, reflect.Bool, reflect.String,
				reflect.Chan, reflect.Func:
				continue
			}
			walkNativeArrays(fv, depth+1, f)
		}
	case reflect.Slice, reflect.Array:
		if v.Len() == 0 {
			return
		}
		if t.Elem() == slotType {
			return
		}
		if t.Elem() == arrayPtrType {
			for i := 0; i < v.Len(); i++ {
				if p := v.Index(i); !p.IsNil() {
					f((*Array)(p.UnsafePointer()))
				}
			}
			return
		}
		for i := 0; i < v.Len(); i++ {
			walkNativeArrays(v.Index(i), depth+1, f)
		}
	case reflect.Map:
		if v.IsNil() {
			return
		}
		it := v.MapRange()
		for it.Next() {
			walkNativeArrays(it.Value(), depth+1, f)
		}
	}
}

// destroyObject уничтожает объект: __destruct (или закрытие генератора/
// файбера), слабые ссылки, хендл в free-list.
func (vm *VM) destroyObject(o *Object) error {
	if o == nil || o.dead {
		return nil
	}
	o.dead = true
	if o.refs <= 0 && !o.gcPending {
		vm.zeroObjs--
	}
	// Уничтожение WeakReference снимает регистрацию по referent'у: следующая
	// create() создаст новую ссылку.
	if o.ClassName() == "WeakReference" {
		if d := objectAs[weakReferenceData](o, o.class); d != nil {
			if ref := deref(d.target).obj(); ref != nil && vm.weakRefs[ref] == o {
				delete(vm.weakRefs, ref)
			}
			d.target = MakeNull()
		}
	}
	ci := o.class
	vm.destroying++
	err := vm.runObjectDestructor(o, ci)
	vm.destroying--
	// Ошибка вложенного уничтожения (деструктор объекта, потерявшего ссылку
	// внутри этого деструктора): поднимаем её как ошибку внешнего вызова.
	if err == nil && vm.pendingDestroyErr != nil {
		err = vm.pendingDestroyErr
		vm.pendingDestroyErr = nil
	}
	// Zend zend_object_std_dtor: типизированные свойства уничтожаемого объекта
	// перестают быть источниками типов держимых ими ссылок.
	vm.removeObjectRefSources(o)
	// Уведомление WeakMap/WeakReference до освобождения хендла: Zend при
	// уничтожении объекта-ключа сначала запускает dtor-хендлер карты,
	// уничтожая значения записей (вложенное уничтожение), и только потом
	// возвращает хендл объекта в free-list. Иначе порядок переиспользования
	// #N переворачивается (weakmap_weakness).
	vm.destroying++
	vm.notifyWeakDestroyed(o)
	vm.destroying--
	// Хендл освобождается после деструктора (Zend: free-list пушится после
	// вызова __destruct, поэтому объекты, созданные в деструкторе, не
	// переиспользуют его номер).
	if o.objID != 0 {
		vm.freeObjIDs = append(vm.freeObjIDs, o.objID)
		o.objID = 0
	}
	return err
}

// removeObjectRefSources снимает источники типизированных свойств уничтожаемого
// объекта с ячеек-ссылок, которые они держали (Zend zend_object_std_dtor:
// ZEND_REF_DEL_TYPE_SOURCE по prop_info каждого типизированного слота).
func (vm *VM) removeObjectRefSources(o *Object) {
	if o == nil || o.class == nil || !o.typedRefs {
		// Быстрый путь: объект не держал типизированных ссылок (обычный случай).
		return
	}
	rc, ok := asRuntimeClass(o.class)
	if !ok || rc == nil {
		return
	}
	// Идём от самого производного класса: объявление свойства в потомке
	// (в том числе нетипизированное) затеняет предка — у слота одно prop_info.
	seen := map[string]bool{}
	for k := rc; k != nil; k = k.parent {
		for name, pd := range k.ownProps {
			if pd.static {
				continue
			}
			key := name
			if pd.vis == Private {
				key = mangledPropKey(k, name)
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			if !pd.hasType || strings.EqualFold(pd.typ, "mixed") {
				continue
			}
			v, found := o.propValueRaw(key)
			if !found {
				continue
			}
			s := v
			if s.typ == RefType {
				if r := s.Ref(); r != nil {
					r.delSource(k, name)
				}
			}
		}
	}
}

// addCloneRefSources регистрирует клона как держателя типизированных ссылок:
// Zend zend_objects_clone_members добавляет prop_info клона к источникам
// скопированных ссылок (дубликат того же prop_info).
func (vm *VM) addCloneRefSources(o *Object) {
	if o == nil || o.class == nil {
		return
	}
	rc, ok := asRuntimeClass(o.class)
	if !ok || rc == nil {
		return
	}
	seen := map[string]bool{}
	for k := rc; k != nil; k = k.parent {
		for name, pd := range k.ownProps {
			if pd.static {
				continue
			}
			key := name
			if pd.vis == Private {
				key = mangledPropKey(k, name)
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			if !pd.hasType || strings.EqualFold(pd.typ, "mixed") {
				continue
			}
			v, found := o.propValueRaw(key)
			if !found {
				continue
			}
			s := v
			if s.typ == RefType {
				if r := s.Ref(); r != nil {
					if g := guardFor(pd, k, name); g != nil {
						r.addSourceDup(g)
					}
					o.MarkTypedRef()
				}
			}
		}
	}
}

// runObjectDestructor вызывает __destruct (или закрывает генератор/файбер)
// без освобождения хендла.
func (vm *VM) runObjectDestructor(o *Object, ci ClassInfo) error {
	if ci == nil {
		return nil
	}
	if vm.genClass != nil {
		if g := objectAs[Generator](o, vm.genClass); g != nil {
			// atShutdown влияет на кадр трассировки закрытия: при
			// уничтожении в ходе работы скрипта место resume — вызывающий
			// код, а не "[internal function]" (bug71604_3).
			vm.gcDtorCount++
			return vm.closeGenerator(g, vm.inShutdown)
		}
	}
	if vm.fiberClass != nil {
		if f := objectAs[Fiber](o, vm.fiberClass); f != nil {
			vm.gcDtorCount++
			return vm.closeFiber(f, true)
		}
	}
	if vm.noDtor[o] {
		return nil
	}
	m := ci.lookupMethod("__destruct")
	if m == nil {
		return nil
	}
	vm.gcDtorCount++
	_, decl, vis := ci.methodInfo("__destruct")
	scope := vm.ctx.self
	if !canAccess(vis, decl, scope) {
		from := "global scope"
		if scope != nil {
			from = "scope " + ClassDisplayName(scope.Name())
		}
		return NewThrowable("Error", "Call to "+visWord(vis)+" "+ClassDisplayName(ci.Name())+"::__destruct() from "+from)
	}
	slot := vm.slotOf(o)
	// Деструктор, вызванный движком в точке выполнения (потеря ссылки,
	// сборщик), в трейсе получает позицию текущего опкода (bug78973);
	// кадр "[internal function]" выставляется для проходов вне точки кода
	// (gc_collect_cycles, разрушение стека файбера) и shutdown-цикла
	// (см. dtorInternal и runShutdownDestructors).
	// Переключение файберов в деструкторе запрещено (dd1/dd2, Zend
	// zend_fiber_switch_block): Fiber::suspend/resume/start внутри __destruct
	// дают FiberError "Cannot switch fibers in current execution context".
	savedNoSwitch := vm.noSwitch
	vm.noSwitch = true
	if vm.dtorInternal {
		vm.markNextFrameInternal = true
	}
	defer func() { vm.noSwitch = savedNoSwitch }()
	_, err := vm.invokeMethod(m, slot, ci, nil)
	return err
}

// notifyWeakDestroyed удаляет уничтоженный объект из WeakMap и очищает
// WeakReference.
func (vm *VM) notifyWeakDestroyed(o *Object) {
	for _, w := range vm.liveObjs {
		if w == nil || w.dead || w.class == nil {
			continue
		}
		switch w.ClassName() {
		case "WeakMap":
			d := objectAs[weakMapData](w, w.class)
			if d == nil {
				continue
			}
			out := d.entries[:0]
			var dropped []Slot
			changed := false
			for i := range d.entries {
				if deref(d.entries[i].obj).obj() != o {
					out = append(out, d.entries[i])
					continue
				}
				// Значение удалённой записи теряет ссылку из карты: учитываем
				// как потерю (кандидат/срочность), чтобы __destruct значения
				// сработал в этом же проходе сборщика (weakmap_weakness).
				// Запись из карты уже исключена — иначе проверка достижимости
				// видела бы значение всё ещё живым через саму карту.
				dropped = append(dropped, d.entries[i].val)
				changed = true
			}
			d.entries = out
			for _, val := range dropped {
				vm.noteDropped(val)
			}
			if changed {
				vm.lifeCandidate = true
			}
		case "WeakReference":
			d := objectAs[weakReferenceData](w, w.class)
			if d == nil {
				continue
			}
			if deref(d.target).obj() == o {
				d.target = MakeNull()
				delete(vm.weakRefs, o)
			}
		}
	}
}

// CollectCycles выполняет принудительный сбор (gc_collect_cycles): уничтожает
// в том числе циклический мусор. Возвращает число собранных объектов и ошибку
// деструктора (Zend: ошибка из __destruct внутри сборщика поднимается как
// непойманная — fibers/no-switch-gc).
func (vm *VM) CollectCycles() (int, error) {
	if vm.sweepBusy {
		return 0, nil
	}
	vm.lifeCandidate = false
	vm.lifeUrgent = false
	// Деструкторы, вызванные сборщиком циклов, в трейсе не имеют позиции в
	// коде (Zend: gc_call_destructors → zend_objects_destroy_object).
	savedInternal := vm.dtorInternal
	vm.dtorInternal = true
	out := vm.sweepLifetime(true, vm.chunk, vm.ip-1)
	vm.dtorInternal = savedInternal
	vm.gcRuns++
	vm.gcCollected += int64(out.count)
	return out.count, out.err
}

// GCRuns возвращает число проходов сборщика (gc_status()["runs"]).
func (vm *VM) GCRuns() int64 { return vm.gcRuns }

// GCCollected возвращает суммарно собранные узлы (gc_status()["collected"]).
func (vm *VM) GCCollected() int64 { return vm.gcCollected }

// GCRootCount возвращает размер буфера возможных корней (gc_status()["roots"]).
func (vm *VM) GCRootCount() int64 { return 0 }

// NoteRefCycle — экспорт noteRefArrayCycle для встроенных функций
// (unserialize строит само-ссылочные массивы через R:-ссылки: gc_022).
func (vm *VM) NoteRefCycle(val Slot, r *Ref) { vm.noteRefArrayCycle(val, r) }

// noteSelfRefArray регистрирует массив с прямой само-ссылкой
// (`$a[] =& $a`, `$a[0][0] =& $a[0]`): Zend считает такие массивы при
// gc_collect_cycles.
func (vm *VM) noteSelfRefArray(a *Array, v Slot) {
	if a == nil {
		return
	}
	if deref(v).AsArray() == a {
		if vm.cyclicArrs == nil {
			vm.cyclicArrs = map[*Array]bool{}
		}
		vm.cyclicArrs[a] = true
	}
}

// arrayCyclic сообщает, что массив недостижим, но достижим из самого себя
// (циклический мусор: refcount не падает до нуля).
func (vm *VM) arrayCyclic(a *Array, seenA map[*Array]bool, seenO map[*Object]bool) bool {
	seenA[a] = true
	cyclic := false
	var walkSlot func(s Slot, depth int)
	walkSlot = func(s Slot, depth int) {
		if depth > 32 || cyclic {
			return
		}
		s = deref(s)
		switch s.typ {
		case ObjectType:
			o := s.obj()
			if o == nil || seenO[o] {
				return
			}
			seenO[o] = true
			vm.walkObjectRefs(o, func(c Slot) { walkSlot(c, depth+1) })
		case ArrayType:
			b := s.AsArray()
			if b == nil {
				return
			}
			if b == a {
				cyclic = true
				return
			}
			if seenA[b] {
				return
			}
			seenA[b] = true
			for _, v := range b.All() {
				walkSlot(v, depth+1)
			}
		}
	}
	for _, v := range a.All() {
		walkSlot(v, 0)
		if cyclic {
			return true
		}
	}
	return false
}

// arrayContainsRef ищет ячейку r внутри массива a (в т.ч. во вложенных
// массивах) — для распознавания цикла `$a[0][0] =& $a[0]`.
func (vm *VM) arrayContainsRef(a *Array, r *Ref, depth int, seen map[*Array]bool) bool {
	if a == nil || r == nil || depth > 8 {
		return false
	}
	if seen == nil {
		seen = map[*Array]bool{}
	}
	if seen[a] {
		return false
	}
	seen[a] = true
	for _, v := range a.All() {
		if v.typ == RefType && v.Ref() == r {
			return true
		}
		s := deref(v)
		if s.typ == ArrayType {
			if vm.arrayContainsRef(s.AsArray(), r, depth+1, seen) {
				return true
			}
		}
	}
	return false
}

// noteRefArrayCycle регистрирует массив, который стал само-циклическим через
// ячейку-ссылку r (значение ячейки — сам массив или он содержит ячейку).
func (vm *VM) noteRefArrayCycle(val Slot, r *Ref) {
	d := deref(val)
	if d.typ != ArrayType || r == nil {
		return
	}
	a := d.AsArray()
	if a == nil || !a.objRefs {
		return
	}
	if vm.arrayContainsRef(a, r, 0, nil) {
		if vm.cyclicArrs == nil {
			vm.cyclicArrs = map[*Array]bool{}
		}
		vm.cyclicArrs[a] = true
	}
}

// classHasDtor сообщает, что у класса есть __destruct (кэшируется в
// дескрипторе).
func classHasDtor(ci ClassInfo) bool {
	rc, ok := ci.(*runtimeClass)
	if !ok || rc == nil {
		return false
	}
	if !rc.dtorChecked {
		rc.hasDtor = rc.lookupMethod("__destruct") != nil
		rc.dtorChecked = true
	}
	return rc.hasDtor
}
