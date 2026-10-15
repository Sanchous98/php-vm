package vm

import (
	"bytes"
	"math/rand"
	"testing"
)

// BitNot на строках обязан совпадать с побайтовым ^ (в т.ч. пустая строка и
// хвост не кратный 8 — unsafe-путь в bitwise.go).
func TestBitNotStringWide(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	for n := 0; n <= 300; n++ {
		src := make([]byte, n)
		rnd.Read(src)
		want := make([]byte, n)
		for i := range src {
			want[i] = ^src[i]
		}
		slot, err := BitNot(MakeString(string(src)))
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if got := slot.ToString(); !bytes.Equal([]byte(got), want) {
			t.Fatalf("n=%d: mismatch got=%q want=%q", n, got, want)
		}
	}
}
