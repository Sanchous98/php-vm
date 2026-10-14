package vm

import "math/bits"

// mul64 возвращает 128-битное произведение беззнаковых a и b: (hi, lo).
func mul64(a, b uint64) (hi, lo uint64) { return bits.Mul64(a, b) }
