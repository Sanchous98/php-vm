package vm

import (
	"sort"
	"sync/atomic"
)

// Перестройка массивов и активные позиционные итераторы (foreach by reference).
//
// Zend хранит позиции обхода в реестре хеш-итераторов и обновляет их при
// перестройке HashTable (zend_hash_iterators_update/advance): array_shift,
// array_unshift и array_splice сдвигают элементы, а курсоры активных foreach
// by ref должны продолжить обход с тех же элементов. В этой VM позиция
// итератора лежит в полях PHP-массива состояния (Array.iterPz, режим
// iterModeArrayRef), а состояния — в регистрах кадров. Перед заменой
// содержимого массива (set(out) в builtins) просматриваются регистры с
// состояниями обхода (vm.iterHints, см. opIterInit C=2) и для состояний с той
// же Ref-ячейкой, что и аргумент функции, позиция пересчитывается по правилам
// Zend.
//
// Отличие от Zend: наш курсор во время тела цикла — индекс ТЕКУЩЕГО элемента
// (IterNext выполняется после тела), а Zend свой курсор продвигает уже внутри
// FETCH. Поэтому пересчёт: z = pos+1 — курсор Zend сразу после выборки, к
// нему применяются правила Zend, и результат возвращается как pos' = z'-1.
// Второй бит iterPz ("zlive") — был ли элемент в позиции z живым в момент
// установки курсора: tombstone в z при zlive=1 означает удаление элемента под
// курсором, и Zend продвинул бы курсор к следующему живому
// (zend_hash_packed_del_val / zend_hash_del_el_ex); дырка, стоявшая там
// изначально (zlive=0), курсор не двигает.
//
// Завершённые обходы помечаются (Array.iterState=false): их состояния больше
// не пересчитываются, поэтому типичный шаблон «foreach, затем серия
// array_shift в том же кадре» не платит за сканирование.

var arrayIDSeq atomic.Uint64

// iterHint — регистр кадра, куда записывалось состояние позиционного обхода
// (OpIterInit C=2). Список vm.iterHints ограничивает пересчёт курсоров при
// перестройке массива только реально использованными регистрами.
type iterHint struct {
	frame int32
	reg   int32
}

// addIterHint регистрирует (кадр, регистр) состояния by-ref обхода.
func (vm *VM) addIterHint(frame, reg int) {
	for _, h := range vm.iterHints {
		if int(h.frame) == frame && int(h.reg) == reg {
			return
		}
	}
	vm.iterHints = append(vm.iterHints, iterHint{int32(frame), int32(reg)})
}

// newArrayID выдаёт уникальный идентификатор массива (см. Array.id).
func newArrayID() uint64 {
	for {
		id := arrayIDSeq.Add(1)
		if id != 0 {
			return id
		}
	}
}

// ensureIterID присваивает массиву идентификатор, если его ещё нет, и
// возвращает его. Идентификатор нужен, чтобы отличить смену объекта массива
// в ячейке итератора (переприсваивание $a = ...) от перестройки того же
// массива: при смене Zend пересинхронизирует итератор на позицию 0.
func (a *Array) ensureIterID() uint64 {
	if a.id == 0 {
		a.id = newArrayID()
	}
	return a.id
}

// AdoptIterID переносит идентичность массива на перестроенную копию: foreach
// by ref не должен замечать замену объекта массива после array_shift/unshift/
// splice и прочих перестроек (set в refArrayArg).
func (a *Array) AdoptIterID(old *Array) {
	if old != nil && old.id != 0 {
		a.id = old.id
	}
}

// liveIndices возвращает позиции хранения живых элементов в порядке
// следования (nil для packed-массива: позиции совпадают с индексами).
func (a *Array) liveIndices() []int {
	if a.packed {
		return nil
	}
	out := make([]int, 0, a.Len())
	for i := range a.entries {
		if a.entries[i].live {
			out = append(out, i)
		}
	}
	return out
}

// iterView — структура живых элементов массива для пересчёта курсоров.
// Для packed-массива live == nil и позиции совпадают с порядковыми номерами.
type iterView struct {
	packed   bool
	live     []int
	storeLen int
}

func makeIterView(a *Array) iterView {
	return iterView{packed: a.packed, live: a.liveIndices(), storeLen: a.storeLen()}
}

// count — число живых элементов.
func (v *iterView) count() int {
	if v.packed {
		return v.storeLen
	}
	return len(v.live)
}

// isLive — позиция хранения z — живой элемент.
func (v *iterView) isLive(z int) bool {
	if v.packed {
		return z >= 0 && z < v.storeLen
	}
	i := sort.SearchInts(v.live, z)
	return i < len(v.live) && v.live[i] == z
}

// order — порядковый номер живого элемента в позиции z; ok=false, если в z
// не живой элемент (дырка/за концом); i при ok=false — число живых до z.
func (v *iterView) order(z int) (i int, ok bool) {
	if v.packed {
		if z >= 0 && z < v.storeLen {
			return z, true
		}
		return v.storeLen, false
	}
	i = sort.SearchInts(v.live, z)
	if i < len(v.live) && v.live[i] == z {
		return i, true
	}
	return i, false
}

// nextLive — позиция первого живого элемента в z или позже; ok=false, если
// такого нет (возвращается storeLen).
func (v *iterView) nextLive(z int) (idx int, ok bool) {
	if v.packed {
		if z < 0 {
			z = 0
		}
		if z < v.storeLen {
			return z, true
		}
		return v.storeLen, false
	}
	i := sort.SearchInts(v.live, z)
	if i < len(v.live) {
		return v.live[i], true
	}
	return v.storeLen, false
}

// isArrayRefIterState — быстрая проверка, что массив — состояние обхода by ref
// (ключи mode/cell/aid + флаг iterState).
func isArrayRefIterState(st *Array) bool {
	if st == nil || !st.iterState || st.mStr == nil || len(st.mStr) != 3 {
		return false
	}
	mi, ok := st.mStr["mode"]
	if !ok || int(mi) >= len(st.entries) || !st.entries[mi].live {
		return false
	}
	v := st.entries[mi].val
	return v.typ == IntType && v.Int() == iterModeArrayRef
}

// iterCursor переводит "текущий+1" (курсор Zend сразу после выборки) с учётом
// удалений под курсором. hole=true — курсор Zend стоит на дырке, которая была
// там уже в момент установки (zlive=0): такую позицию shift/splice не трогают.
func iterCursor(pos, zlive int, v *iterView) (z int, hole bool) {
	z = pos + 1
	if z >= v.storeLen {
		return z, false
	}
	if v.isLive(z) {
		return z, false
	}
	if zlive == 0 {
		return z, true
	}
	idx, ok := v.nextLive(z)
	if !ok {
		return v.storeLen, false
	}
	return idx, false
}

// remapIters применяет fn к позиции каждого активного позиционного итератора,
// ячейка которого — та же Ref-ячейка, что и аргумент перестраивающего функцию
// builtin'а. fn получает индекс текущего элемента; возвращает новый индекс
// текущего элемента. Если массив в ячейке был переприсвоен (id не совпадает),
// состояние пересинхронизируется на позицию 0 — как zend_hash_iterator_pos_ex
// при смене HashTable.
func (vm *VM) remapIters(cell Slot, fn func(pos, zlive int, v *iterView) int) {
	if !vm.anyArrayRefIter || cell.typ != RefType || cell.Ref() == nil {
		return
	}
	cv := deref(cell)
	if cv.typ != ArrayType {
		return
	}
	arr := cv.AsArray()
	if arr == nil {
		return
	}
	ref := cell.Ref()
	var view *iterView
	var tid uint64
	for _, h := range vm.iterHints {
		if int(h.frame) > vm.fp {
			continue
		}
		regs := vm.frames[h.frame].regs
		if int(h.reg) >= len(regs) {
			continue
		}
		s := regs[h.reg]
		if s.typ != ArrayType {
			continue
		}
		st := s.AsArray()
		if !isArrayRefIterState(st) {
			continue
		}
		ci, ok := st.mStr["cell"]
		if !ok || int(ci) >= len(st.entries) {
			continue
		}
		c := st.entries[ci].val
		if c.typ != RefType || c.Ref() != ref {
			continue
		}
		if view == nil {
			v := makeIterView(arr)
			view = &v
			tid = arr.ensureIterID()
		}
		ai, ok := st.mStr["aid"]
		if !ok || int(ai) >= len(st.entries) || st.entries[ai].val.typ != IntType ||
			uint64(st.entries[ai].val.Int()) != tid {
			// Ячейка уже указывает на другой массив (переприсваивание
			// источника): курсор Zend пересинхронизируется на 0.
			iterStateSetPz(st, 0, 0)
			st.SetStr("aid", MakeInt(int64(tid)))
			continue
		}
		pos, zlive := iterStatePz(st)
		np := fn(pos, zlive, view)
		iterStateSetPz(st, np, zlive)
	}
}

// RemapArrayShiftIters обновляет позиции активных by-ref итераторов при
// array_shift($cell): первый живой элемент удаляется, остальные сдвигаются к
// началу. Курсор, указывающий на удалённый/сдвигаемый элемент, переезжает на
// его новую позицию; дырка под курсором (zlive=0) остаётся как в Zend.
func (vm *VM) RemapArrayShiftIters(cell Slot) {
	vm.remapIters(cell, func(pos, zlive int, v *iterView) int {
		z, hole := iterCursor(pos, zlive, v)
		if hole {
			return pos
		}
		i, live := v.order(z)
		if !live {
			return pos // за концом: новый массив короче, обход завершится
		}
		if i == 0 {
			return -1 // элемент под курсором удалён (или уедет в 0)
		}
		return i - 2
	})
}

// RemapArrayUnshiftIters обновляет позиции активных by-ref итераторов при
// array_unshift($cell, ...): Zend вызывает zend_hash_iterators_advance(ht,
// argc) — все курсоры сдвигаются на число добавленных в начало элементов.
func (vm *VM) RemapArrayUnshiftIters(cell Slot, argc int) {
	if argc <= 0 {
		return
	}
	vm.remapIters(cell, func(pos, zlive int, v *iterView) int {
		z, _ := iterCursor(pos, zlive, v)
		return z + argc - 1
	})
}

// RemapArraySpliceIters обновляет позиции активных by-ref итераторов при
// array_splice($cell, off, length, replacement). off/length — в терминах
// живых элементов (как в php_splice), replLen — число вставленных значений.
// Префикс сохраняет порядковые позиции, удалённый курсор переходит на первый
// уцелевший элемент после вырезанного диапазона, суффикс сдвигается на
// replLen-length.
func (vm *VM) RemapArraySpliceIters(cell Slot, off, length, replLen int) {
	vm.remapIters(cell, func(pos, zlive int, v *iterView) int {
		n := v.count()
		if off < 0 {
			off = 0
		}
		if off > n {
			off = n
		}
		if length < 0 {
			length = 0
		}
		if off+length > n {
			length = n - off
		}
		end := off + length
		z, hole := iterCursor(pos, zlive, v)
		if hole {
			return pos // Zend не трогает курсор на дырке
		}
		i, live := v.order(z)
		if !live {
			return pos // за концом: Zend курсор не трогает
		}
		switch {
		case i < off:
			return i - 1
		case i >= end:
			return off + replLen + (i - end) - 1
		default:
			// Курсор указывал на удалённый элемент: Zend продвигает его к
			// первому уцелевшему после диапазона (или за конец массива).
			if end < n {
				return off + replLen - 1
			}
			return v.storeLen - 1
		}
	})
}
