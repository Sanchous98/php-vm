package vm

import (
	"bytes"
	"math"
	"testing"
)

// asm — маленький ассемблер для тестов (регистровая VM).
type asm struct{ c *Chunk }

func newAsm() *asm { return &asm{c: NewChunk()} }

func (a *asm) emit(op Opcode, A, B uint8, C int32) *asm {
	a.c.Emit(Instruction{Op: op, A: A, B: B, C: C})
	return a
}

// lk: rA = const index C
func (a *asm) lk(A uint8, c Const) *asm {
	return a.emit(OpLoadK, A, 0, int32(a.c.AddConst(c)))
}

// run выполняет программу и возвращает stdout.
func (a *asm) run(t *testing.T) string {
	t.Helper()
	var vm VM
	vm.Init(a.c, nil, nil)
	var buf bytes.Buffer
	vm.SetOutput(&buf)
	if err := vm.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	return buf.String()
}

func TestRegArithmetic(t *testing.T) {
	// r0 = 1; r1 = 2; r2 = r0 + r1; r3 = r2 * 3; echo r3  => 9
	a := newAsm()
	a.lk(0, IntConst(1)).lk(1, IntConst(2))
	a.emit(OpAddInt, 2, 0, 1) // r2 = r0 + r1
	a.lk(3, IntConst(3))
	a.emit(OpMulInt, 4, 2, 3) // r4 = r2 * r3
	a.emit(OpEcho, 4, 0, 0).emit(OpHalt, 0, 0, 0)
	if got := a.run(t); got != "9" {
		t.Fatalf("got %q, want 9", got)
	}
}

func TestRegLoop(t *testing.T) {
	// s = 0; i = 0; while (i < 5) { s = s + 1; i = i + 1 } echo s => 5
	const c0, c1, c5 = 0, 1, 2
	a := newAsm()
	a.c.AddConst(IntConst(0)) // c0
	a.c.AddConst(IntConst(1)) // c1
	a.c.AddConst(IntConst(5)) // c5
	// r0 = s, r1 = i, r2 = tmp
	a.emit(OpLoadK, 0, 0, c0) // s = 0
	a.emit(OpLoadK, 1, 0, c0) // i = 0
	cond := int32(a.c.Len())
	a.emit(OpLessIntK, 2, 1, c5) // r2 = (i < 5)
	exit := int32(a.c.Len())
	a.emit(OpJumpIfFalse, 2, 0, 0) // patched
	a.emit(OpAddIntK, 0, 0, c1)    // s = s + 1
	a.emit(OpAddIntK, 1, 1, c1)    // i = i + 1
	a.emit(OpJump, 0, 0, cond)
	a.c.SetJump(int(exit), int32(a.c.Len()))
	a.emit(OpEcho, 0, 0, 0).emit(OpHalt, 0, 0, 0)

	if got := a.run(t); got != "5" {
		t.Fatalf("got %q, want 5", got)
	}
}

func TestRegIntOverflow(t *testing.T) {
	a := newAsm()
	a.lk(0, IntConst(math.MaxInt64))
	a.emit(OpAddIntK, 1, 0, int32(a.c.AddConst(IntConst(1))))
	a.emit(OpEcho, 1, 0, 0).emit(OpHalt, 0, 0, 0)
	if got := a.run(t); got != "9.2233720368548E+18" {
		t.Fatalf("got %q", got)
	}
}

func TestRegConcatAndEqual(t *testing.T) {
	// r0 = "hello "; r1 = "world"; r2 = r0 . r1; r3 = 42; r4 = r2 . r3; echo r4
	a := newAsm()
	a.lk(0, StrConst("hello ")).lk(1, StrConst("world"))
	a.emit(OpConcat, 2, 0, 1)
	a.lk(3, IntConst(42))
	a.emit(OpConcat, 4, 2, 3)
	a.emit(OpEcho, 4, 0, 0).emit(OpHalt, 0, 0, 0)
	if got := a.run(t); got != "hello world42" {
		t.Fatalf("got %q", got)
	}

	// "10" == 10 -> true
	b := newAsm()
	b.lk(0, StrConst("10")).lk(1, IntConst(10))
	b.emit(OpEqual, 2, 0, 1)
	b.emit(OpEcho, 2, 0, 0).emit(OpHalt, 0, 0, 0)
	if got := b.run(t); got != "1" {
		t.Fatalf(`"10" == 10 : got %q, want 1`, got)
	}
}

func TestRegDivByZero(t *testing.T) {
	a := newAsm()
	a.lk(0, IntConst(1)).lk(1, IntConst(0))
	a.emit(OpDiv, 2, 0, 1).emit(OpHalt, 0, 0, 0)
	var vm VM
	vm.Init(a.c, nil, nil)
	if err := vm.Run(); err == nil {
		t.Fatal("expected division by zero")
	}
}

func TestRegNumRegs(t *testing.T) {
	a := newAsm()
	a.lk(0, IntConst(1)).lk(7, IntConst(2))
	a.emit(OpAddInt, 9, 0, 7)
	if got := a.c.NumRegs(); got != 10 {
		t.Fatalf("NumRegs = %d, want 10", got)
	}
}
