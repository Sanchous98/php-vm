package vm

import "testing"

// Базовые операции массива через байткод: array/append/index/len/unset/index-set.
func TestRegArrayOps(t *testing.T) {
	a := newAsm()
	a.emit(OpArray, 0, 0, 0) // r0 = []
	a.lk(1, IntConst(10))
	a.emit(OpAppend, 0, 1, 0) // r0[] = 10
	a.lk(1, IntConst(20))
	a.emit(OpAppend, 0, 1, 0) // r0[] = 20
	a.lk(3, IntConst(0))
	a.emit(OpIndex, 2, 0, 3) // r2 = r0[0] = 10
	a.emit(OpEcho, 2, 0, 0)
	a.emit(OpLen, 4, 0, 0) // r4 = count(r0) = 2
	a.emit(OpEcho, 4, 0, 0)
	a.emit(OpUnset, 0, 3, 0) // unset r0[0]
	a.emit(OpLen, 5, 0, 0)   // count = 1
	a.emit(OpEcho, 5, 0, 0)
	a.lk(3, IntConst(5))
	a.lk(6, IntConst(99))
	a.emit(OpIndexSet, 0, 3, 6) // r0[5] = 99
	a.emit(OpLen, 7, 0, 0)      // count = 2
	a.emit(OpEcho, 7, 0, 0)
	a.emit(OpHalt, 0, 0, 0)

	if got := a.run(t); got != "10212" {
		t.Fatalf("got %q, want %q", got, "10212")
	}
}

// COW через байткод: $b = $a; $b[0] = 99; $a[0] не меняется.
func TestRegArrayCOW(t *testing.T) {
	a := newAsm()
	a.emit(OpArray, 0, 0, 0)
	for _, v := range []int64{1, 2, 3} {
		a.lk(1, IntConst(v))
		a.emit(OpAppend, 0, 1, 0)
	}
	a.emit(OpMove, 1, 0, 0) // r1 = r0 (share -> COW)
	a.lk(2, IntConst(99))
	a.lk(3, IntConst(0))
	a.emit(OpIndexSet, 1, 3, 2) // r1[0] = 99
	a.emit(OpIndex, 4, 0, 3)    // r0[0]
	a.emit(OpEcho, 4, 0, 0)
	a.emit(OpIndex, 5, 1, 3) // r1[0]
	a.emit(OpEcho, 5, 0, 0)
	a.emit(OpHalt, 0, 0, 0)

	if got := a.run(t); got != "199" {
		t.Fatalf("got %q, want %q (a[0]=1, b[0]=99)", got, "199")
	}
}
