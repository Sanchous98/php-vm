package vm

import (
	"fmt"
	"iter"
	"math"
	"sort"
	"strconv"
	"strings"
	"unsafe"
)

// maxEntries — предел индекса int32 (2^31-1). PHP сам ограничивает массивы
// HT_MAX_SIZE = 2^30, так что для PHP-совместимых программ предел недостижим;
// guard оставлен для строгости.
const maxEntries = math.MaxInt32

// packedKind — способ хранения значений packed-массива (ключи ровно
// 0..storeLen()-1, дыр нет). Горячие однородные случаи хранятся типизированно:
//
//   - packedGeneric -> values []Slot    (24 Б/эл);
//   - packedInt     -> ints   []int64   (8 Б/эл);
//   - packedFloat   -> floats []float64 (8 Б/эл);
//   - packedStr     -> strs   []pstr    (16 Б/эл — та же пара (ptr,num), что и в
//     строковом слоте, вместе с битами interned/buf: семантика 1:1).
//
// Вид выбирается при первой записи в пустой массив и держится, пока пишут
// значения того же типа. Первое «неподходящее» значение один раз конвертирует
// массив в packedGeneric (копия O(n)); обратной типизации нет. int->float
// промоушен не делается: смена типа значения наблюдаема (var_dump/===/is_int),
// поэтому массив из int и float уходит в generic.
type packedKind uint8

const (
	packedGeneric packedKind = iota
	packedInt
	packedFloat
	packedStr
)

// pstr — элемент packedStr: те же (ptr, num), что у строкового слота. ptr —
// интерьерный указатель на данные строки (GC держит объект живым), num —
// len | internedBit | bufBit. Тип (StringType) подразумевается.
type pstr struct {
	ptr unsafe.Pointer
	num uint64
}

func strPstr(s Slot) pstr { return pstr{ptr: s.ptr, num: s.num} }
func (p pstr) slot() Slot { return Slot{typ: StringType, num: p.num, ptr: p.ptr} }

// Array — PHP-массив: упорядоченная хеш-таблица с copy-on-write.
//
// Две репрезентации (как packed/hash в Zend):
//
//   - packed: ключи ровно 0..storeLen()-1, значения подряд в типизированном
//     хранилище (см. packedKind). Чтение $a[$i] — прямой индекс, без хеша.
//     Доминирующий случай списков.
//   - mixed: произвольные ключи (int|string), порядок вставки, tombstone после
//     unset. Значения лежат в упорядоченном массиве entries ([]KV), а карты
//     хранят только ключ -> позицию в entries (int32). Итерация идёт ТОЛЬКО по
//     entries (последовательно, без хеша); чтение — карта + entries[idx].val.
//
// Переход packed->mixed — при первом «непоследовательном»/строковом ключе или
// unset. Обратно никогда (как в PHP).
//
// Памятью владеет Go GC. shared — признак разделения для COW (не владение).
type Array struct {
	packed bool
	kind   packedKind

	// packed-представление: активно ровно одно из полей по kind.
	values []Slot    // packedGeneric
	ints   []int64   // packedInt
	floats []float64 // packedFloat
	strs   []pstr    // packedStr

	// mixed-представление
	entries []KV             // порядок вставки; значения и tombstone здесь
	mStr    map[string]int32 // строковый ключ -> позиция в entries
	mInt    map[int64]int32  // int-ключ      -> позиция в entries

	// nextFree — следующий авто-индекс (zend nNextFreeElement). Сентинел
	// math.MinInt64 — «не задан»; при добавлении отрицательное значение
	// трактуется как 0. Обновляется при любом int-ключе h: h >= nextFree ->
	// nextFree = h+1 (не сбрасывается при unset/pop).
	nextFree int64
	shared   bool

	// objRefs — в массиве есть (или может быть) значение, транзитивно
	// удерживающее объект: Object/Closure/Ref или вложенный массив.
	// Монотонный флаг для сборщика (lifetime.go): позволяет не запускать
	// проход при потере «скалярного» массива. Выставляется при записи
	// элементов (noteVal); при COW-копировании переносится.
	objRefs bool

	// iterState — этот массив — состояние позиционного by-ref обхода
	// (ключи mode/cell/aid). Флаг позволяет при перестройке массивов быстро
	// отсеивать регистры без обхода хеш-таблицы состояния; снимается, когда
	// обход завершён (см. iterArrayRefValid). iterPz — позиция курсора и
	// признак "в pos+1 был живой элемент", упакованные как pos*2+zlive:
	// горячий цикл обхода не платит за лишние обращения к хеш-таблице.
	iterState bool
	iterPz    int32

	// pos — внутренний указатель массива (как nInternalPointer в Zend):
	// current/next/prev/reset/end/key. ГЛОБАЛЬНОЕ состояние массива: копируется
	// при копировании значения и разделяет массив при изменении (COW). foreach
	// (All()) этот указатель НЕ трогает.
	pos int

	// id — идентификатор объекта массива для foreach by reference: состояние
	// обхода запоминает id источника и при его смене (переприсваивание
	// источника) сбрасывает позицию курсора, как zend_hash_iterator_pos_ex при
	// смене HashTable. Ноль — id ещё не выдан (см. ensureIterID); при
	// перестройке массива (array_shift/unshift/splice) новый объект
	// наследует id перестроенного (AdoptIterID), чтобы обход не прерывался.
	id uint64
}

// KV — элемент mixed-массива в порядке вставки.
type KV struct {
	key  ArrayKey
	val  Slot
	live bool // false -> tombstone после unset
}

// ArrayKey — ключ PHP-массива: только int или string.
type ArrayKey struct {
	str bool
	i   int64
	s   string
}

func intKey(i int64) ArrayKey     { return ArrayKey{i: i} }
func stringKey(s string) ArrayKey { return ArrayKey{str: true, s: s} }

// MakeIntKey / MakeStrKey — публичные конструкторы ключей (для встроенных).
func MakeIntKey(i int64) ArrayKey  { return intKey(i) }
func MakeStrKey(s string) ArrayKey { return stringKey(s) }

// KeyFromString канонизирует строковый ключ по правилам PHP-массива
// («1» → int-ключ 1) — как keyFromSlot для строк; нужно встроенным,
// строящим массивы из внешних данных (json_decode).
func KeyFromString(s string) ArrayKey {
	if i, ok := canonicalIntKey(s); ok {
		return intKey(i)
	}
	return stringKey(s)
}

// SetStr — установка значения по строковому ключу (SetInt см. ниже).
func (a *Array) SetStr(k string, v Slot) error { return a.Set(stringKey(k), v) }

// phpString возвращает ключ в виде, как его печатает PHP (для отладки/тестов).
func (k ArrayKey) phpString() string {
	if k.str {
		return k.s
	}
	return strconv.FormatInt(k.i, 10)
}

// keyFromSlot приводит слот к ключу массива по правилам PHP.
// ok == false — недопустимый тип ключа (array/object), ошибка на уровне VM.
func keyFromSlot(s Slot) (ArrayKey, bool) {
	s = deref(s)
	switch s.typ {
	case IntType:
		return intKey(s.Int()), true
	case BoolType:
		if s.BoolVal() {
			return intKey(1), true
		}
		return intKey(0), true
	case FloatType:
		return intKey(floatToInt(s.Float())), true
	case NullType:
		return stringKey(""), true
	case StringType:
		str := s.StringVal()
		if i, ok := canonicalIntKey(str); ok {
			return intKey(i), true
		}
		return stringKey(str), true
	}
	return ArrayKey{}, false
}

// KeyOfWarn — как keyFromSlot, но для resource-ключа выдаёт PHP-предупреждение
// «Resource ID#N used as offset, casting to integer (N)» и приводит к int.
func (vm *VM) KeyOfWarn(s Slot) (ArrayKey, bool) {
	s = deref(s)
	if s.typ == ResourceType {
		id := int64(s.num)
		ids := intToString(id)
		vm.Warn("Resource ID#" + ids + " used as offset, casting to integer (" + ids + ")")
		return intKey(id), true
	}
	if s.typ == FloatType {
		// PHP 8.1+: неявное float->int для ключа массива — Deprecated.
		vm.floatToIntWarn(s)
	}
	return keyFromSlot(s)
}

// stringOffsetIndex приводит ключ доступа к строке к числовому индексу.
// illegal=true означает, что ключ — ведуще-числовая, но не вполне числовая
// строка (Zend печатает "Illegal string offset" и берёт числовой префикс).
// ok=false — ключ несовместим (TypeError).
func stringOffsetIndex(k Slot) (i int64, ok bool, illegal bool) {
	k = deref(k)
	switch k.typ {
	case ArrayType, ObjectType, ClosureType, ResourceType:
		// Zend: "Cannot access offset of type X on string".
		return 0, false, false
	case StringType:
		// Zend: is_numeric_string(allow_errors=true). Пробелы по краям
		// отбрасываются, шестнадцатеричные/двоичные/восьмеричные префиксы и
		// «числовой префикс с хвостом» дают offset 0/значение с диагностикой
		// "Illegal string offset", а дробные строки — TypeError.
		s := strings.Trim(k.StringVal(), " \t\n\r\v\f")
		if s == "" {
			return 0, false, false
		}
		if len(s) >= 2 && s[0] == '0' {
			switch s[1] {
			case 'x', 'X', 'b', 'B', 'o', 'O':
				return 0, true, true
			}
		}
		j := 0
		neg := false
		if s[0] == '+' || s[0] == '-' {
			neg = s[0] == '-'
			j++
		}
		ds := j
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j == ds {
			return 0, false, false
		}
		if j < len(s) && (s[j] == '.' || s[j] == 'e' || s[j] == 'E') {
			return 0, false, false
		}
		v, err := strconv.ParseInt(s[ds:j], 10, 64)
		if err != nil {
			return 0, false, false
		}
		if neg {
			v = -v
		}
		return v, true, j != len(s)
	}
	return k.ToInt(), true, false
}

// stringOffsetKey приводит ключ доступа к строке: недопустимые типы (массив,
// объект, ресурс) дают TypeError. Для float Zend НЕ печатает Deprecated о
// потере точности (только "String offset cast occurred" у вызывающего).
func (vm *VM) stringOffsetKey(k Slot) (int64, bool, bool) {
	return stringOffsetIndex(k)
}

// canonicalIntKey сообщает, является ли строка каноническим десятичным целым,
// которое PHP приводит к int-ключу. Канонический вид: опциональный '-', затем
// "0" либо [1-9][0-9]*, без ведущих нулей, знака '+' и пробелов; значение
// должно помещаться в int64. Проверено: "10"->int, "01"/"1e2"/"+5"/" 10"/
// "-0"/"00"->string, "-9223372036854775808"->int (MinInt64).
func canonicalIntKey(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	neg := false
	body := s
	if s[0] == '-' {
		neg = true
		body = s[1:]
	}
	if body == "" {
		return 0, false
	}
	if body[0] == '0' && (body != "0" || neg) {
		return 0, false // ведущий ноль недопустим; "-0" тоже не каноничен
	}
	for i := 0; i < len(body); i++ {
		if body[i] < '0' || body[i] > '9' {
			return 0, false
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false // переполнение int64 -> остаётся строкой
	}
	return v, true
}

// newArray создаёт пустой packed-массив.
func newArray() *Array { return &Array{packed: true, nextFree: math.MinInt64} }

// NewArray создаёт пустой packed-массив (для встроенных функций).
func NewArray() *Array { return newArray() }

// kindForType — типизированный вид хранилища для значения типа t, либо
// packedGeneric, если тип не поддерживается типизированно.
func kindForType(t Type) packedKind {
	switch t {
	case IntType:
		return packedInt
	case FloatType:
		return packedFloat
	case StringType:
		return packedStr
	}
	return packedGeneric
}

// storeLen — длина packed-хранилища (для packed совпадает с Len()).
func (a *Array) storeLen() int {
	if !a.packed {
		return len(a.entries)
	}
	switch a.kind {
	case packedInt:
		return len(a.ints)
	case packedFloat:
		return len(a.floats)
	case packedStr:
		return len(a.strs)
	}
	return len(a.values)
}

// Len возвращает число «живых» элементов.
func (a *Array) Len() int {
	if a.packed {
		switch a.kind {
		case packedInt:
			return len(a.ints)
		case packedFloat:
			return len(a.floats)
		case packedStr:
			return len(a.strs)
		}
		return len(a.values)
	}
	return len(a.mStr) + len(a.mInt)
}

// packedSlot — значение packed-элемента i как слот.
func (a *Array) packedSlot(i int) Slot {
	switch a.kind {
	case packedInt:
		return MakeInt(a.ints[i])
	case packedFloat:
		return MakeFloat(a.floats[i])
	case packedStr:
		return a.strs[i].slot()
	}
	return a.values[i]
}

// getPacked — чтение packed-элемента по int-индексу.
func (a *Array) getPacked(i int64) (Slot, bool) {
	switch a.kind {
	case packedInt:
		if uint64(i) < uint64(len(a.ints)) {
			return MakeInt(a.ints[i]), true
		}
	case packedFloat:
		if uint64(i) < uint64(len(a.floats)) {
			return MakeFloat(a.floats[i]), true
		}
	case packedStr:
		if uint64(i) < uint64(len(a.strs)) {
			return a.strs[i].slot(), true
		}
	default:
		if uint64(i) < uint64(len(a.values)) {
			return a.values[i], true
		}
	}
	return Slot{}, false
}

// GetInt — чтение по int-ключу (горячий путь $a[$i] для int $i).
func (a *Array) GetInt(i int64) (Slot, bool) {
	return a.Get(intKey(i))
}

// Ints / Floats / Strs — типизированное хранилище packed-массива (nil, если
// массив не этого вида). Только чтение: срез нельзя мутировать.
func (a *Array) Ints() []int64 {
	if a != nil && a.packed && a.kind == packedInt {
		return a.ints
	}
	return nil
}

func (a *Array) Floats() []float64 {
	if a != nil && a.packed && a.kind == packedFloat {
		return a.floats
	}
	return nil
}

// StrAt — строковый элемент packedStr (для встроенных, которым нужна строка).
func (a *Array) StrAt(i int) (string, bool) {
	if a != nil && a.packed && a.kind == packedStr && uint64(i) < uint64(len(a.strs)) {
		return a.strs[i].slot().StringVal(), true
	}
	return "", false
}

// Get возвращает значение по ключу. Самым частым видам (packedInt, packed
// generic и mixed) соответствует прямой разбор без дополнительных вызовов;
// редкие packedFloat/packedStr уходят в getPacked.
func (a *Array) Get(k ArrayKey) (Slot, bool) {
	if !k.str && a.packed {
		i := k.i
		if a.kind == packedInt {
			if uint64(i) < uint64(len(a.ints)) {
				return Slot{typ: IntType, num: uint64(a.ints[i])}, true
			}
			return Slot{}, false
		}
		if a.kind == packedGeneric {
			if uint64(i) < uint64(len(a.values)) {
				return a.values[i], true
			}
			return Slot{}, false
		}
		return a.getPacked(i)
	}
	if a.packed {
		return Slot{}, false // строковых ключей в packed нет
	}
	if k.str {
		if idx, ok := a.mStr[k.s]; ok {
			return a.entries[idx].val, true
		}
		return Slot{}, false
	}
	if idx, ok := a.mInt[k.i]; ok {
		return a.entries[idx].val, true
	}
	return Slot{}, false
}

// Рост типизированных хранилищ: удвоение ёмкости (как у packed-таблиц Zend).
// Go-append для больших срезов растёт на ~25%, из-за чего суммарный объём
// промежуточных копий доходит до ~5x финального размера; удвоение даёт ~2x и
// заметно снижает и время, и пиковый RSS на больших массивах.
func appendInt64(s []int64, x int64) []int64 {
	if len(s) < cap(s) {
		return append(s, x)
	}
	n := len(s) + 1
	c := cap(s) * 2
	if c < n {
		c = n
	}
	if c < 8 {
		c = 8
	}
	ns := make([]int64, n, c)
	copy(ns, s)
	ns[len(s)] = x
	return ns
}

func appendFloat64(s []float64, x float64) []float64 {
	if len(s) < cap(s) {
		return append(s, x)
	}
	n := len(s) + 1
	c := cap(s) * 2
	if c < n {
		c = n
	}
	if c < 8 {
		c = 8
	}
	ns := make([]float64, n, c)
	copy(ns, s)
	ns[len(s)] = x
	return ns
}

func appendPstr(s []pstr, x pstr) []pstr {
	if len(s) < cap(s) {
		return append(s, x)
	}
	n := len(s) + 1
	c := cap(s) * 2
	if c < n {
		c = n
	}
	if c < 8 {
		c = 8
	}
	ns := make([]pstr, n, c)
	copy(ns, s)
	ns[len(s)] = x
	return ns
}

// storeAppend добавляет значение в конец packed-хранилища. Вызывающий уже
// проверил вместимость; вид хранилища выбирается/меняется здесь.
// noteVal отмечает, что значение транзитивно может удерживать объекты
// (для сборщика времени жизни, см. objRefs).
func (a *Array) noteVal(v Slot) {
	if !a.objRefs {
		switch v.typ {
		case ObjectType, ClosureType, RefType, ArrayType:
			a.objRefs = true
		case ResourceType:
			// Ресурс с OnDrop (процесс proc_open) должен быть виден
			// марк-фазе: иначе финализатор закрыл бы пайпы, пока ресурс
			// удерживается элементом массива.
			if r := AsResource(v); r != nil && r.OnDrop != nil {
				a.objRefs = true
			}
		}
	}
}

func (a *Array) storeAppend(v Slot) {
	a.noteVal(v)
	t := v.typ
	if a.kind == packedGeneric && len(a.values) == 0 {
		// Пустой массив: выбираем типизированное хранилище по первому значению.
		a.kind = kindForType(t)
	} else if a.kind != packedGeneric && a.kind != kindForType(t) {
		// Неподходящий тип: одноразовый переход в generic.
		a.toGeneric()
	}
	switch a.kind {
	case packedInt:
		a.ints = appendInt64(a.ints, v.Int())
	case packedFloat:
		a.floats = appendFloat64(a.floats, v.Float())
	case packedStr:
		a.strs = appendPstr(a.strs, strPstr(v))
	default:
		a.values = append(a.values, v)
	}
}

// setAt записывает значение в существующий packed-элемент i.
func (a *Array) setAt(i int, v Slot) {
	a.noteVal(v)
	if a.kind != packedGeneric && a.kind != kindForType(v.typ) {
		a.toGeneric()
	}
	switch a.kind {
	case packedInt:
		a.ints[i] = v.Int()
	case packedFloat:
		a.floats[i] = v.Float()
	case packedStr:
		a.strs[i] = strPstr(v)
	default:
		a.values[i] = v
	}
}

// Set записывает значение по ключу. Вызывающий обязан предварительно сделать
// Unshare (COW). Ошибка возможна только при недостижимом переполнении индекса.
func (a *Array) Set(k ArrayKey, v Slot) error {
	if k.str {
		if a.packed {
			a.toMixed()
		}
		if idx, ok := a.mStr[k.s]; ok {
			a.entries[idx].val = v
			a.noteVal(v)
			return nil
		}
		if len(a.entries) >= maxEntries {
			return errArrayTooLarge
		}
		if a.mStr == nil {
			a.mStr = make(map[string]int32)
		}
		a.mStr[k.s] = int32(len(a.entries))
		a.entries = append(a.entries, KV{key: k, val: v, live: true})
		a.noteVal(v)
		return nil
	}
	return a.SetInt(k.i, v)
}

// SetInt — установка значения по int-ключу (горячий путь $a[$i] = v).
func (a *Array) SetInt(k int64, v Slot) error {
	if a.packed {
		if k >= 0 {
			n := int64(a.storeLen())
			if k < n {
				a.setAt(int(k), v)
				return nil
			}
			if k == n {
				if n >= maxEntries {
					return errArrayTooLarge
				}
				a.storeAppend(v)
				a.nextFree = k + 1
				return nil
			}
		}
		a.toMixed()
	}
	if idx, ok := a.mInt[k]; ok {
		a.entries[idx].val = v
		a.noteVal(v)
		return nil
	}
	if len(a.entries) >= maxEntries {
		return errArrayTooLarge
	}
	if a.mInt == nil {
		a.mInt = make(map[int64]int32)
	}
	a.mInt[k] = int32(len(a.entries))
	a.entries = append(a.entries, KV{key: intKey(k), val: v, live: true})
	a.noteVal(v)
	if k >= a.nextFree {
		if k < math.MaxInt64 {
			a.nextFree = k + 1
		} else {
			a.nextFree = math.MaxInt64
		}
	}
	return nil
}

// Unset удаляет ключ (tombstone: соседи не сдвигаются). Вызывающий обязан
// предварительно сделать Unshare.
func (a *Array) Unset(k ArrayKey) {
	if k.str {
		if a.packed {
			return // строковых ключей в packed нет
		}
		if idx, ok := a.mStr[k.s]; ok {
			delete(a.mStr, k.s)
			a.entries[idx].live = false
			a.entries[idx].val = Slot{}
			if a.pos == int(idx) {
				a.pos = a.nextLive(int(idx)) // указатель под удалённым -> на следующий
			}
		}
		return
	}
	a.UnsetInt(k.i)
}

// UnsetInt — удаление по int-ключу (быстрый путь unset($a[$i])).
func (a *Array) UnsetInt(k int64) {
	if a.packed {
		if k < 0 || k >= int64(a.storeLen()) {
			return // такого ключа нет
		}
		a.toMixed()
	}
	if idx, ok := a.mInt[k]; ok {
		delete(a.mInt, k)
		a.entries[idx].live = false
		a.entries[idx].val = Slot{}
		if a.pos == int(idx) {
			a.pos = a.nextLive(int(idx))
		}
	}
}

// Append добавляет значение с авто-ключом nextFree и возвращает этот ключ.
// Вызывающий обязан предварительно сделать Unshare.
//
// Если следующий ключ переполняет int64 (максимальный int-ключ = PHP_INT_MAX),
// возвращается errNextOccupied — как PHP: "Cannot add element to the array as
// the next element is already occupied".
func (a *Array) Append(v Slot) (int64, error) {
	if a.packed {
		n := a.storeLen()
		if a.nextFree != math.MinInt64 && a.nextFree != int64(n) {
			a.toMixed()
		} else {
			if n >= maxEntries {
				return 0, errArrayTooLarge
			}
			a.storeAppend(v)
			a.nextFree = int64(a.storeLen())
			return a.nextFree - 1, nil
		}
	}
	if a.nextFree == math.MaxInt64 {
		return 0, errNextOccupied
	}
	k := a.nextFree
	if k == math.MinInt64 {
		k = 0
	}
	if err := a.Set(intKey(k), v); err != nil {
		return 0, err
	}
	return k, nil
}

// AppendInt добавляет int-значение (быстрый путь встроенных: без упаковки в
// Slot и без проверки вида хранилища, когда массив уже packedInt).
func (a *Array) AppendInt(x int64) error {
	if a.packed && a.kind == packedInt && (a.nextFree == math.MinInt64 || a.nextFree == int64(len(a.ints))) {
		if len(a.ints) >= maxEntries {
			return errArrayTooLarge
		}
		a.ints = appendInt64(a.ints, x)
		a.nextFree = int64(len(a.ints))
		return nil
	}
	_, err := a.Append(MakeInt(x))
	return err
}

// PackedStrs сообщает, что массив хранится как packedStr.
func (a *Array) PackedStrs() bool { return a != nil && a.packed && a.kind == packedStr }

// SortStrs сортирует packedStr по байтовому сравнению строк (SORT_STRING) и
// сообщает, что массив был packedStr. Элементы сортируются на месте — вызывать
// только на Unshare()-нутом массиве.
func (a *Array) SortStrs(desc bool) bool {
	if !a.packed || a.kind != packedStr {
		return false
	}
	if desc {
		sort.SliceStable(a.strs, func(i, j int) bool {
			return a.strs[i].slot().StringVal() > a.strs[j].slot().StringVal()
		})
	} else {
		sort.SliceStable(a.strs, func(i, j int) bool {
			return a.strs[i].slot().StringVal() < a.strs[j].slot().StringVal()
		})
	}
	return true
}

// ReserveInts резервирует место под n int-значений (array_keys/range и т.п.):
// убирает промежуточные копии при геометрическом росте. Для непустого
// generic-массива — no-op.
func (a *Array) ReserveInts(n int) {
	if !a.packed || n <= 0 {
		return
	}
	switch a.kind {
	case packedInt:
		if cap(a.ints) < n {
			ns := make([]int64, len(a.ints), n)
			copy(ns, a.ints)
			a.ints = ns
		}
	case packedGeneric:
		if len(a.values) == 0 {
			a.kind = packedInt
			a.ints = make([]int64, 0, n)
		}
	}
}

// elementAt возвращает значение живого элемента по позиции хранения (для
// позиционной итерации foreach по ссылке) и признак «элемент жив».
func (a *Array) elementAt(i int) (Slot, bool) {
	if a.packed {
		if i >= 0 && i < a.storeLen() {
			return a.packedSlot(i), true
		}
		return Slot{}, false
	}
	if i >= 0 && i < len(a.entries) && a.entries[i].live {
		return a.entries[i].val, true
	}
	return Slot{}, false
}

// keyAt — ключ живого элемента по позиции хранения (ok == false — позиция
// пуста). Аналог elementAt для ключей.
func (a *Array) keyAt(i int) (ArrayKey, bool) {
	if a.packed {
		if i >= 0 && i < a.storeLen() {
			return intKey(int64(i)), true
		}
		return ArrayKey{}, false
	}
	if i >= 0 && i < len(a.entries) && a.entries[i].live {
		return a.entries[i].key, true
	}
	return ArrayKey{}, false
}

// storeRefAt записывает Ref-ячейку в позицию хранения i (foreach по ссылке
// превращает текущий элемент в настоящую ссылку в массиве). Вызывать только на
// Unshare()-нутом массиве; для packed хранилищ конверсия в generic происходит
// внутри setAt (Ref не влезает в типизированные виды).
func (a *Array) storeRefAt(i int, ref Slot) {
	if a.packed {
		if i >= 0 && i < a.storeLen() {
			a.setAt(i, ref)
		}
		return
	}
	if i >= 0 && i < len(a.entries) && a.entries[i].live {
		a.noteVal(ref)
		a.entries[i].val = ref
	}
}

// All возвращает итератор по живым элементам в порядке вставки. Итерация идёт
// ТОЛЬКО по упорядоченному хранилищу (values/ints/floats/strs для packed,
// entries для mixed), последовательно и без хеша:
// for k, v := range a.All() { ... }.
func (a *Array) All() iter.Seq2[ArrayKey, Slot] {
	return func(yield func(ArrayKey, Slot) bool) {
		if a.packed {
			switch a.kind {
			case packedInt:
				for i, x := range a.ints {
					if !yield(intKey(int64(i)), MakeInt(x)) {
						return
					}
				}
				return
			case packedFloat:
				for i, x := range a.floats {
					if !yield(intKey(int64(i)), MakeFloat(x)) {
						return
					}
				}
				return
			case packedStr:
				for i := range a.strs {
					if !yield(intKey(int64(i)), a.strs[i].slot()) {
						return
					}
				}
				return
			}
			for i := range a.values {
				if !yield(intKey(int64(i)), a.values[i]) {
					return
				}
			}
			return
		}
		for i := range a.entries {
			if a.entries[i].live {
				if !yield(a.entries[i].key, a.entries[i].val) {
					return
				}
			}
		}
	}
}

// DeepDerefSlot возвращает значение без ссылок внутри массивов: define()
// копирует массив в константу, разрывая ссылки (Zend zend_hash_copy с
// deref; тест Zend/tests/constant_arrays.phpt). Рекурсивные массивы
// отвергаются (ValueError "Recursion detected").
func DeepDerefSlot(s Slot) (Slot, error) {
	return deepDerefSlot(s, nil)
}

func deepDerefSlot(s Slot, seen map[*Array]bool) (Slot, error) {
	s = deref(s)
	if s.typ != ArrayType {
		return s, nil
	}
	src := s.AsArray()
	if seen != nil && seen[src] {
		return Slot{}, fmt.Errorf("define(): Argument #2 ($value) cannot be a recursive array")
	}
	if seen == nil {
		seen = map[*Array]bool{}
	}
	seen[src] = true
	defer delete(seen, src)
	out := newArray()
	for k, v := range src.All() {
		dv, err := deepDerefSlot(v, seen)
		if err != nil {
			return Slot{}, err
		}
		_ = out.Set(k, dv)
	}
	return MakeArray(out), nil
}

// WalkNextLive — первая живая позиция хранения >= i (для позиционной
// итерации array_walk: томбстоуны пропускаются, как HT-итератор Zend).
func (a *Array) WalkNextLive(i int) (int, bool) {
	n := a.nextLive(i)
	return n, n < a.storeLen()
}

// WalkElementAt — значение живого элемента по позиции хранения.
func (a *Array) WalkElementAt(i int) (Slot, bool) { return a.elementAt(i) }

// WalkKeyAt — ключ живого элемента по позиции хранения.
func (a *Array) WalkKeyAt(i int) (ArrayKey, bool) { return a.keyAt(i) }

// WalkSetAt — замена значения живого элемента по позиции хранения (только на
// Unshare()-нутом массиве).
func (a *Array) WalkSetAt(i int, v Slot) {
	if a.packed {
		if i >= 0 && i < a.storeLen() {
			a.setAt(i, v)
		}
		return
	}
	if i >= 0 && i < len(a.entries) && a.entries[i].live {
		a.noteVal(v)
		a.entries[i].val = v
	}
}

// HasStringKeys сообщает, есть ли в массиве строковые ключи.
func (a *Array) HasStringKeys() bool {
	if a == nil || a.packed {
		return false
	}
	for i := range a.entries {
		if a.entries[i].live && a.entries[i].key.str {
			return true
		}
	}
	return false
}

// Values возвращает значения в порядке следования ключей (для упаковки
// аргументов вызова), без ключей.
func (a *Array) Values() []Slot {
	if a.packed {
		n := a.storeLen()
		out := make([]Slot, n)
		for i := 0; i < n; i++ {
			out[i] = a.packedSlot(i)
		}
		return out
	}
	out := make([]Slot, 0, len(a.mStr)+len(a.mInt))
	for i := range a.entries {
		if a.entries[i].live {
			out = append(out, a.entries[i].val)
		}
	}
	return out
}

// --- Внутренний указатель (current/next/prev/reset/end/key) ---
//
// Это отдельный механизм от All() (foreach): он глобальный для массива.
// Изменяющие указатель операции (Next/Prev/Reset/End) должны вызываться на
// уже Unshare()-нутом массиве — в PHP они разделяют значение (COW).

func (a *Array) liveAt(i int) bool {
	if a.packed {
		return i >= 0 && i < a.storeLen()
	}
	return i >= 0 && i < len(a.entries) && a.entries[i].live
}

// nextLive — первый живой индекс >= i, иначе storeLen() (за концом).
func (a *Array) nextLive(i int) int {
	n := a.storeLen()
	if i < 0 {
		i = 0
	}
	for i < n && !a.liveAt(i) {
		i++
	}
	if i > n {
		return n
	}
	return i
}

// prevLive — последний живой индекс <= i; если нет — первый живой (как PHP).
func (a *Array) prevLive(i int) int {
	for i >= 0 && !a.liveAt(i) {
		i--
	}
	if i < 0 {
		return a.nextLive(0)
	}
	return i
}

// Reset — reset($a): указатель на первый элемент.
func (a *Array) Reset() { a.pos = a.nextLive(0) }

// End — end($a): указатель на последний элемент.
func (a *Array) End() { a.pos = a.prevLive(a.storeLen() - 1) }

// Next — next($a): указатель на следующий живой (или за конец).
func (a *Array) Next() { a.pos = a.nextLive(a.pos + 1) }

// Prev — prev($a): указатель на предыдущий живой.
func (a *Array) Prev() { a.pos = a.prevLive(a.pos - 1) }

// Current — current($a): значение под указателем, или false.
func (a *Array) Current() (Slot, bool) {
	i := a.pos
	if a.packed {
		if i >= 0 && i < a.storeLen() {
			return a.packedSlot(i), true
		}
		return Slot{}, false
	}
	if i >= 0 && i < len(a.entries) && a.entries[i].live {
		return a.entries[i].val, true
	}
	return Slot{}, false
}

// Key — key($a): ключ под указателем, или null (ok == false).
func (a *Array) Key() (ArrayKey, bool) {
	i := a.pos
	if a.packed {
		if i >= 0 && i < a.storeLen() {
			return intKey(int64(i)), true
		}
		return ArrayKey{}, false
	}
	if i >= 0 && i < len(a.entries) && a.entries[i].live {
		return a.entries[i].key, true
	}
	return ArrayKey{}, false
}

// toGeneric переводит типизированный packed в packedGeneric (значения
// несовместимы с текущим видом хранилища). Для packedGeneric — no-op.
func (a *Array) toGeneric() {
	if a.kind == packedGeneric {
		return
	}
	n := a.storeLen()
	if n == 0 {
		a.ints, a.floats, a.strs = nil, nil, nil
		a.kind = packedGeneric
		return
	}
	vals := make([]Slot, n)
	for i := 0; i < n; i++ {
		vals[i] = a.packedSlot(i)
	}
	a.values = vals
	a.ints, a.floats, a.strs = nil, nil, nil
	a.kind = packedGeneric
}

// toMixed переводит packed в mixed (после unset или непоследовательного ключа).
func (a *Array) toMixed() {
	n := a.storeLen()
	a.entries = make([]KV, n)
	if n > 0 {
		a.mInt = make(map[int64]int32, n)
	}
	for i := 0; i < n; i++ {
		a.entries[i] = KV{key: intKey(int64(i)), val: a.packedSlot(i), live: true}
		a.mInt[int64(i)] = int32(i)
	}
	a.values, a.ints, a.floats, a.strs = nil, nil, nil, nil
	if n > 0 && a.nextFree < int64(n) {
		a.nextFree = int64(n)
	}
	a.packed = false
}

// NextFree возвращает следующий авто-индекс (zend nNextFreeElement).
func (a *Array) NextFree() int64 { return a.nextFree }

// SetNextFree задаёт следующий авто-индекс.
func (a *Array) SetNextFree(n int64) { a.nextFree = n }

// MarkShared помечает массив разделённым (значение скопировано в другой слот).
// Память при этом не трогается: владелец — Go GC.
func (a *Array) MarkShared() { a.shared = true }

// Shared сообщает, что массив помечен разделённым: перед записью нужен
// Unshare (COW). Ложные срабатывания безопасны, но дороги.
func (a *Array) Shared() bool { return a.shared }

// Packed сообщает, что массив хранится в packed-представлении: ключи ровно
// 0..Len()-1 в порядке следования (быстрый путь итерации/ключей).
func (a *Array) Packed() bool { return a != nil && a.packed }

// Unshare возвращает массив, пригодный для записи: сам a, если он не разделён,
// иначе поверхностную копию. Оригинал остаётся разделённым. Вложенные
// массивы-значения помечаются разделёнными — иначе запись в них была бы видна
// обоим владельцам (рекурсивный COW).
func (a *Array) Unshare() *Array {
	if !a.shared {
		return a
	}
	c := &Array{packed: a.packed, kind: a.kind, nextFree: a.nextFree, pos: a.pos, objRefs: a.objRefs, id: a.id}
	if a.packed {
		switch a.kind {
		case packedInt:
			c.ints = make([]int64, len(a.ints))
			copy(c.ints, a.ints)
		case packedFloat:
			c.floats = make([]float64, len(a.floats))
			copy(c.floats, a.floats)
		case packedStr:
			c.strs = make([]pstr, len(a.strs))
			copy(c.strs, a.strs)
		default:
			c.values = make([]Slot, len(a.values))
			copy(c.values, a.values)
		}
	} else {
		c.entries = make([]KV, len(a.entries))
		copy(c.entries, a.entries)
		if a.mStr != nil {
			c.mStr = make(map[string]int32, len(a.mStr))
			for k, idx := range a.mStr {
				c.mStr[k] = idx
			}
		}
		if a.mInt != nil {
			c.mInt = make(map[int64]int32, len(a.mInt))
			for k, idx := range a.mInt {
				c.mInt[k] = idx
			}
		}
	}
	// И a, и c теперь ссылаются на вложенные массивы -> они разделены.
	for i := range c.values {
		if c.values[i].typ == ArrayType {
			c.values[i].AsArray().MarkShared()
		}
	}
	for i := range c.entries {
		if c.entries[i].live && c.entries[i].val.typ == ArrayType {
			c.entries[i].val.AsArray().MarkShared()
		}
	}
	return c
}

// Экспорт ключа для встроенных функций (var_dump и т.п.).
func (k ArrayKey) IsStr() bool       { return k.str }
func (k ArrayKey) StrVal() string    { return k.s }
func (k ArrayKey) IntVal() int64     { return k.i }
func (k ArrayKey) PHPString() string { return k.phpString() }

// stringOffsetWarnKey — подготовка ключа строкового offset в R/RW-режиме:
// ресурс/объект/массив (и замыкание) дают TypeError "Cannot access offset of
// type X on string" (Zend не печатает предупреждение о приведении), bool/null/
// float сопровождаются Warning "String offset cast occurred".
func (vm *VM) stringOffsetWarnKey(k Slot) *Throwable {
	switch deref(k).typ {
	case ResourceType, ObjectType, ArrayType, ClosureType:
		return NewThrowable("TypeError", "Cannot access offset of type "+valueTypeName(vm, k)+" on string")
	case IntType, StringType:
		return nil
	}
	vm.Warn("String offset cast occurred")
	return nil
}
