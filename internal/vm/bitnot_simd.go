//go:build goexperiment.simd

package vm

import (
	"simd"
	"unsafe"
)

// bitNotStringWide возвращает побайтовое дополнение строки.
//
// С GOEXPERIMENT=simd: основной проход — вектор Std SIMD (длина берётся из
// нулевого значения: 32 байта на AVX2), инверсия во время переноса. Слайсы
// фиксированной длины [i-vl:i] — так проверка длины вектора в обёртке simd
// элидируется (в отличие от открытого src[i:]). Хвост (< длины вектора)
// обрабатывается частичной загрузкой LoadUint8sPart/StorePart. Источник
// читается из view (unsafe.Slice + StringData) — без копии строки; результат
// отдаётся прямо по буферу (unsafe.String) — одна аллокация и один проход.
func bitNotStringWide(s string) string {
	n := len(s)
	if n == 0 {
		return ""
	}
	buf := make([]byte, n)
	view := unsafe.Slice(unsafe.StringData(s), n) // view, без копии строки
	var v simd.Uint8s
	vl := v.Len()
	remaining := n - n%vl
	for i := vl; i <= remaining; i += vl {
		v = simd.LoadUint8s(view[i-vl : i])
		v.Not().Store(buf[i-vl : i])
	}
	if remaining < n {
		v, _ = simd.LoadUint8sPart(view[remaining:])
		v.Not().StorePart(buf[remaining:])
	}
	return unsafe.String(&buf[0], n)
}
