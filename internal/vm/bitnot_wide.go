//go:build !goexperiment.simd

package vm

import "unsafe"

// bitNotStringWide возвращает побайтовое дополнение строки.
//
// Без GOEXPERIMENT=simd: 8-байтовые слова, инверсия во время переноса, цикл
// развёрнут на 4 слова (32 байта за итерацию) — меньше накладных на байт;
// хвост скаляром; строка возвращается прямо по буферу (unsafe.String) — одна
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
		i := 0
		for ; i+4 <= w; i += 4 {
			dw[i] = ^sw[i]
			dw[i+1] = ^sw[i+1]
			dw[i+2] = ^sw[i+2]
			dw[i+3] = ^sw[i+3]
		}
		for ; i < w; i++ {
			dw[i] = ^sw[i]
		}
	}
	for i := w * 8; i < n; i++ {
		buf[i] = ^s[i]
	}
	return unsafe.String(&buf[0], n)
}
