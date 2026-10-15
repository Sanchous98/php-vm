package vm

import (
	"math/rand"
	"testing"
)

// Дифференциальный тест: оптимизированный bitwiseStrings против наивной
// побайтовой версии на всех длинах/содержимом и всех операциях.
func TestBitwiseStringsDiff(t *testing.T) {
	naive := func(a, b string, op byte) string {
		switch op {
		case bitOr:
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
		default:
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
	}
	rnd := rand.New(rand.NewSource(42))
	for _, op := range []byte{bitAnd, bitOr, bitXor} {
		for la := 0; la <= 40; la++ {
			for lb := 0; lb <= 40; lb++ {
				ab := make([]byte, la)
				bb := make([]byte, lb)
				rnd.Read(ab)
				rnd.Read(bb)
				a, b := string(ab), string(bb)
				if got, want := bitwiseStrings(a, b, op), naive(a, b, op); got != want {
					t.Fatalf("op=%d la=%d lb=%d: got %q want %q", op, la, lb, got, want)
				}
			}
		}
	}
}
