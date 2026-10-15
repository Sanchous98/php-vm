package vm

import (
	"fmt"
	"strings"
	"testing"
)

var bitopsBenchSink string

// bitwiseStringsOld — прежняя побайтовая реализация (для сравнения).
func bitwiseStringsOld(a, b string, op byte) string {
	if op == bitOr {
		n := max(len(a), len(b))
		buf := make([]byte, n)
		for i := 0; i < n; i++ {
			var x, y byte
			if i < len(a) {
				x = a[i]
			}
			if i < len(b) {
				y = b[i]
			}
			buf[i] = x | y
		}
		return string(buf)
	}
	n := min(len(a), len(b))
	buf := make([]byte, n)
	for i := 0; i < n; i++ {
		if op == bitAnd {
			buf[i] = a[i] & b[i]
		} else {
			buf[i] = a[i] ^ b[i]
		}
	}
	return string(buf)
}

func TestInterleavedBitops(t *testing.T) {
	mk := func(n int) string { return strings.Repeat("ab", n/2) }
	type variant struct {
		name string
		f    func(string, string, byte) string
	}
	variants := []variant{
		{"old", bitwiseStringsOld},
		{"new", bitwiseStrings},
	}
	for _, size := range []int{64, 1024, 65536} {
		for _, op := range []byte{bitAnd, bitOr, bitXor} {
			sa, sb := mk(size), mk(size+size%8)
			best := make([]int64, len(variants))
			allocs := make([]int64, len(variants))
			for rep := 0; rep < 5; rep++ {
				for vi, v := range variants {
					f := v.f
					opc := op
					res := testing.Benchmark(func(b *testing.B) {
						b.SetBytes(int64(size))
						b.ReportAllocs()
						for i := 0; i < b.N; i++ {
							bitopsBenchSink = f(sa, sb, opc)
						}
					})
					if ns := res.NsPerOp(); best[vi] == 0 || ns < best[vi] {
						best[vi] = ns
						allocs[vi] = res.AllocsPerOp()
					}
				}
			}
			t.Log(fmt.Sprintf("op=%s %8d:  old=%6.0f MB/s (%d all)   new=%6.0f MB/s (%d all)   x%.2f",
				opName(op), size,
				float64(size)*1000/float64(best[0]), allocs[0],
				float64(size)*1000/float64(best[1]), allocs[1],
				float64(best[0])/float64(best[1])))
		}
	}
}

func opName(op byte) string {
	switch op {
	case bitAnd:
		return "&"
	case bitOr:
		return "|"
	default:
		return "^"
	}
}
