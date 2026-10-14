package vm

import (
	"bytes"
	"testing"
)

// buildRegCountLoop: s = 0; i = 0; while (i < n) { s = s + 1; i = i + 1 }
// Тот же цикл, что мерили на стековой VM.
func buildRegCountLoop(n int64) *Chunk {
	a := newAsm()
	c0 := int32(a.c.AddConst(IntConst(0)))
	c1 := int32(a.c.AddConst(IntConst(1)))
	cn := int32(a.c.AddConst(IntConst(n)))

	// r0 = s, r1 = i, r2 = tmp
	a.emit(OpLoadK, 0, 0, c0)
	a.emit(OpLoadK, 1, 0, c0)
	cond := int32(a.c.Len())
	a.emit(OpLessIntK, 2, 1, cn)
	exit := int32(a.c.Len())
	a.emit(OpJumpIfFalse, 2, 0, 0)
	a.emit(OpAddIntK, 0, 0, c1)
	a.emit(OpAddIntK, 1, 1, c1)
	a.emit(OpJump, 0, 0, cond)
	a.c.SetJump(int(exit), int32(a.c.Len()))
	a.emit(OpHalt, 0, 0, 0)
	return a.c
}

func benchRegVM(b *testing.B) {
	chunk := buildRegCountLoop(1000000)
	var vm VM
	vm.Init(chunk, nil, nil)
	vm.SetOutput(&bytes.Buffer{})
	_ = vm.Run()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.ip = 0
		vm.cur.regs[0] = MakeNull()
		vm.cur.regs[1] = MakeNull()
		if err := vm.Run(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRegVMCount(b *testing.B) { benchRegVM(b) }

func BenchmarkRegVMInstruction(b *testing.B) {
	// Изолированно: одна арифметическая инструкция в цикле.
	a := newAsm()
	c1 := int32(a.c.AddConst(IntConst(1)))
	a.lk(0, IntConst(0))
	for i := 0; i < 1024; i++ {
		a.emit(OpAddIntK, 0, 0, c1)
	}
	a.emit(OpHalt, 0, 0, 0)
	var vm VM
	vm.Init(a.c, nil, nil)
	vm.SetOutput(&bytes.Buffer{})
	_ = vm.Run()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.ip = 0
		vm.cur.regs[0] = MakeInt(0)
		if err := vm.Run(); err != nil {
			b.Fatal(err)
		}
	}
}
