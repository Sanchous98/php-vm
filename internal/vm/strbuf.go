package vm

import "unsafe"

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
