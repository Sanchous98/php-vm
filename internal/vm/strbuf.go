package vm

import (
	"strconv"
	"unsafe"
)

// Строковый буфер — один heap-объект вида [заголовок][данные...]:
//
//	type strBufHdr struct { cap, used uint64 }
//
// Слот строки-буфера хранит интерьерный указатель на начало данных и длину в
// num (бит bufBit): GC держит весь объект живым, пока жив слот, а заголовок
// достижим как ptr - sizeof(заголовок).
//
// Инвариант: у каждого слота, ссылающегося на буфер, длина <= used. Дописывание
// всегда идёт в позицию used, поэтому остальные слоты (их видимые префиксы не
// длиннее used) записи не наблюдают. Если длина слота меньше used — буфер уже
// дописан другим владельцем (копия слота), тогда дописывание делает копию:
// COW по фактическому разделению, без отдельного флага shared.
//
// Отсюда бесплатная корректность всех мест копирования слотов (OpMove, запись
// в массив/свойство, передача по значению, return, ...): копия слота несёт ту
// же длину, а used может только расти, поэтому чужой хвост копия не видит.

const strBufHdrSize = 16

type strBufHdr struct {
	cap  uint64
	used uint64
}

// strBufHeader возвращает заголовок буфера по указателю на данные.
func strBufHeader(ptr unsafe.Pointer) *strBufHdr {
	return (*strBufHdr)(unsafe.Add(ptr, -strBufHdrSize))
}

// bufStringLen — длина строки-буфера.
func bufStringLen(s Slot) int { return int(s.num & lenMask) }

// newBufCap — стартовая ёмкость буфера: не меньше длины, но не меньше 32,
// чтобы короткие накопления не росли повторно на каждом байте.
func newBufCap(need int) int {
	if need < 32 {
		return 32
	}
	return need
}

// growBufCap — ёмкость при переполнении: удвоение (амортизированное O(1) по
// дописанным байтам), но не меньше требуемого.
func growBufCap(old, need int) int {
	c := old * 2
	if c < need {
		c = need
	}
	if c < 32 {
		c = 32
	}
	return c
}

// newStrBufParts выделяет буфер ёмкостью cap (>= len(ls)+len(rs)) и копирует
// в него ls и rs. used = длине результата.
func newStrBufParts(ls, rs string, cap int) Slot {
	need := len(ls) + len(rs)
	buf := make([]byte, strBufHdrSize+cap)
	h := (*strBufHdr)(unsafe.Pointer(&buf[0]))
	h.cap = uint64(cap)
	h.used = uint64(need)
	data := unsafe.Pointer(&buf[strBufHdrSize])
	dst := unsafe.Slice((*byte)(data), cap)
	copy(dst, ls)
	copy(dst[len(ls):], rs)
	return makeBufString(data, need)
}

// newStrBufPartsBytes — как newStrBufParts, но вторая часть задана байтами
// (для форматирования int прямо в буфер без промежуточной строки).
func newStrBufPartsBytes(ls string, rb []byte, cap int) Slot {
	need := len(ls) + len(rb)
	buf := make([]byte, strBufHdrSize+cap)
	h := (*strBufHdr)(unsafe.Pointer(&buf[0]))
	h.cap = uint64(cap)
	h.used = uint64(need)
	data := unsafe.Pointer(&buf[strBufHdrSize])
	dst := unsafe.Slice((*byte)(data), cap)
	copy(dst, ls)
	copy(dst[len(ls):], rb)
	return makeBufString(data, need)
}

// appendStringSlot возвращает ls . rs, переиспользуя буфер слота s, если он
// есть (OpConcatApp/OpConcat). ls — строковое представление s (для строк —
// s.StringVal()). s должен быть разыменованным слотом (без Ref).
func appendStringSlot(s Slot, ls, rs string) Slot {
	if len(ls) == 0 && len(rs) == 0 {
		return MakeString("")
	}
	if s.typ == StringType && s.num&bufBit != 0 {
		h := strBufHeader(s.ptr)
		n := bufStringLen(s)
		used := int(h.used)
		need := n + len(rs)
		if n == used {
			if need <= int(h.cap) {
				// Дописывание на месте: байты за пределами used никому не видны.
				if len(rs) > 0 {
					buf := unsafe.Slice((*byte)(s.ptr), int(h.cap))
					copy(buf[n:], rs)
				}
				h.used = uint64(need)
				return makeBufString(s.ptr, need)
			}
			return newStrBufParts(ls, rs, growBufCap(int(h.cap), need))
		}
		// Буфер уже дописан другим владельцем (n < used) — копия префикса.
		return newStrBufParts(ls, rs, newBufCap(need))
	}
	// Обычная строка либо не-строка: новый буфер.
	return newStrBufParts(ls, rs, newBufCap(len(ls)+len(rs)))
}

// intDigits — число десятичных цифр в int64 (для точного размера буфера
// склейки). Форматирование идёт в стековый буфер, без аллокаций.
func intDigits(i int64) int {
	var tb [20]byte
	return len(strconv.AppendInt(tb[:0], i, 10))
}

// concatStrInt собирает ls . itoa(i) одной аллокацией: десятичные цифры
// форматируются в стековый буфер (без промежуточной строки intToString), затем
// результат собирается ровно одного размера и слоту отдаётся zero-copy
// интерьерный указатель (GC держит массив живым). Заменяет путь
// stringify(int) + MakeString(ls+rs), где было две аллокации.
func concatStrInt(ls string, i int64) Slot {
	var tmp [20]byte
	b := strconv.AppendInt(tmp[:0], i, 10)
	n := len(ls) + len(b)
	if n == 0 {
		return MakeString("")
	}
	buf := make([]byte, n)
	off := copy(buf, ls)
	copy(buf[off:], b)
	return MakeString(unsafe.String(&buf[0], n))
}

// concatIntInt собирает itoa(i) . itoa(j) одной аллокацией (без промежуточных
// int-строк). Буфер выделяется на heap — слот держит интерьерный указатель,
// поэтому stack-буфер здесь использовать нельзя.
func concatIntInt(i, j int64) Slot {
	var tmp [40]byte
	b := strconv.AppendInt(tmp[:0], i, 10)
	b = strconv.AppendInt(b, j, 10)
	if len(b) == 0 {
		return MakeString("")
	}
	buf := make([]byte, len(b))
	copy(buf, b)
	return MakeString(unsafe.String(&buf[0], len(buf)))
}

// appendStrBufInt дописывает десятичное представление i в буферный слот s
// (OpConcatApp для строки . int): цифры форматируются прямо в буфере, без
// промежуточной строки. Для не-буферного s заводится новый буфер.
func appendStrBufInt(s Slot, ls string, i int64) Slot {
	if s.typ == StringType && s.num&bufBit != 0 {
		h := strBufHeader(s.ptr)
		n := bufStringLen(s)
		if n == int(h.used) {
			var tmp [20]byte
			b := strconv.AppendInt(tmp[:0], i, 10)
			need := n + len(b)
			if need <= int(h.cap) {
				// Дописывание на месте: байты за пределами used никому не видны.
				buf := unsafe.Slice((*byte)(s.ptr), int(h.cap))
				copy(buf[n:], b)
				h.used = uint64(need)
				return makeBufString(s.ptr, need)
			}
			return newStrBufPartsBytes(ls, b, growBufCap(int(h.cap), need))
		}
		// Буфер дописан другим владельцем (n < used) — копия префикса.
		var tmp [20]byte
		b := strconv.AppendInt(tmp[:0], i, 10)
		return newStrBufPartsBytes(ls, b, newBufCap(n+len(b)))
	}
	// Обычная строка либо не-строка: новый буфер.
	var tmp [20]byte
	b := strconv.AppendInt(tmp[:0], i, 10)
	return newStrBufPartsBytes(ls, b, newBufCap(len(ls)+len(b)))
}

// concatNResult собирает n частей в одну строку ровно одной аллокацией.
// parts — уже приведённые к строкам части (в порядке конкатенации), total —
// их суммарная длина. Строка отдаётся zero-copy: слот хранит интерьерный
// указатель &buf[0], GC держит массив живым (не buf.String() — там копия).
func concatNResult(parts []string, total int) Slot {
	if total == 0 {
		return MakeString("")
	}
	buf := make([]byte, total)
	off := 0
	for _, p := range parts {
		off += copy(buf[off:], p)
	}
	return MakeString(unsafe.String(&buf[0], total))
}

// concatNResultSlots собирает n частей одной аллокацией, форматируя int-части
// прямо в результирующий буфер (без промежуточных intToString). vals — снимки
// значений частей (уже разыменованные, в порядке конкатенации); parts — уже
// приведённые строки для частей, которые не являются string/int (для
// остальных значение не используется). Снимок обязателен: приведение более
// поздней части может через __toString изменить значение ссылки-части.
func concatNResultSlots(parts []string, vals []Slot, total int) Slot {
	if total == 0 {
		return MakeString("")
	}
	buf := make([]byte, total)
	off := 0
	for i := range vals {
		switch vals[i].typ {
		case StringType:
			off += copy(buf[off:], vals[i].StringVal())
		case IntType:
			var tb [20]byte
			b := strconv.AppendInt(tb[:0], vals[i].Int(), 10)
			off += copy(buf[off:], b)
		default:
			off += copy(buf[off:], parts[i])
		}
	}
	return MakeString(unsafe.String(&buf[0], total))
}
