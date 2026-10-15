//go:build goexperiment.simd

package vm

import (
	"simd"
	"unsafe"
)

// bitNotStringWide возвращает побайтовое дополнение строки.
//
// С GOEXPERIMENT=simd: вектор Std SIMD (длина из нулевого значения — 32 байта
// на AVX2), инверсия во время переноса, хвост — частичной загрузкой
// (LoadUint8sPart/StorePart) + скаляром; строка возвращается прямо по буферу
// (unsafe.String) — одна аллокация и один проход.
//
// Замер перемежением (min из 7): 1K 3618 против 3021 MB/s, 64K 5273 против
// 3761 MB/s у словарного пути; на 4M оба упираются в память (~3.3 GB/s).
func bitNotStringWide(s string) string {
	n := len(s)
	if n == 0 {
		return ""
	}
	buf := make([]byte, n)
	src := unsafe.Slice(unsafe.StringData(s), n) // view, без копии
	var zero simd.Uint8s
	vl := zero.Len()
	i := 0
	for ; i+vl <= n; i += vl {
		simd.LoadUint8s(src[i:]).Not().Store(buf[i:])
	}
	if i < n {
		v, _ := simd.LoadUint8sPart(src[i:])
		i += v.Not().StorePart(buf[i:])
	}
	for ; i < n; i++ {
		buf[i] = ^s[i]
	}
	return unsafe.String(&buf[0], n)
}
