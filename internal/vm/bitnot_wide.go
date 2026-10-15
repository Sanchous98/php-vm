//go:build !goexperiment.simd

package vm

import "unsafe"

// bitNotStringWide возвращает побайтовое дополнение строки.
//
// Без GOEXPERIMENT=simd: 8-байтовые слова, инверсия во время переноса, хвост
// скаляром; строка возвращается прямо по буферу (unsafe.String) — одна
// аллокация и один проход.
func bitNotStringWide(s string) string {
	n := len(s)
	if n == 0 {
		return ""
	}
	buf := make([]byte, n)
	w := n / 8
	if w > 0 {
		sw := unsafe.Slice((*uint64)(unsafe.Pointer(unsafe.StringData(s))), w)
		dw := unsafe.Slice((*uint64)(unsafe.Pointer(&buf[0])), w)
		for i := 0; i < w; i++ {
			dw[i] = ^sw[i]
		}
	}
	for i := w * 8; i < n; i++ {
		buf[i] = ^s[i]
	}
	return unsafe.String(&buf[0], n)
}
