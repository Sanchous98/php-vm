//go:build goexperiment.simd

package vm

import (
	"simd"
	"unsafe"
)

// bitNotStringWide возвращает побайтовое дополнение строки.
//
// С GOEXPERIMENT=simd: основной проход — вектор Std SIMD (длина берётся из
// нулевого значения: 32 байта на AVX2), инверсия во время переноса. Хвост
// (< длины вектора) обрабатывается частичной загрузкой LoadUint8sPart/StorePart,
// которая принимает слайс любой длины, тогда как полные LoadUint8s/Store
// требуют не меньше длины вектора. Источник читается из view
// (unsafe.Slice + StringData) — без копии строки; результат отдаётся прямо по
// буферу (unsafe.String) — одна аллокация и один проход.
func bitNotStringWide(s string) string {
	n := len(s)
	if n == 0 {
		return ""
	}
	buf := make([]byte, n)
	src := unsafe.Slice(unsafe.StringData(s), n) // view, без копии строки
	vl := simd.Uint8s{}.Len()
	remaining := n - n%vl
	for i := 0; i < remaining; i += vl {
		simd.LoadUint8s(src[i:]).Not().Store(buf[i:])
	}
	// Хвост (< длины вектора). При remaining == n слайс пуст, но частичные
	// Load/Store принимают нулевую длину — отдельный гард не нужен.
	v, _ := simd.LoadUint8sPart(src[remaining:])
	v.Not().StorePart(buf[remaining:])
	return unsafe.String(&buf[0], n)
}
