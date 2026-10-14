package vm

import (
	"bytes"
	"testing"
)

// buildNullLoop строит чанк: batch подряд идущих OpNull + счётчик и переход
// назад. Даёт ровно batches*batch «пустых» инструкций при компактном коде
// (не раздувает side-table inline-кэшей).
func buildNullLoop(batch, batches int64) *Chunk {
	c := NewChunk()
	c0 := int32(c.AddConst(IntConst(0)))
	c1 := int32(c.AddConst(IntConst(1)))
	cN := int32(c.AddConst(IntConst(batches)))

	// r0 — счётчик циклов, r1 — скретч под OpNull, r2 — условие.
	c.Emit(Instruction{Op: OpLoadK, A: 0, C: c0})
	top := int32(c.Len())
	for i := int64(0); i < batch; i++ {
		c.Emit(Instruction{Op: OpNull, A: 1})
	}
	c.Emit(Instruction{Op: OpAddIntK, A: 0, B: 0, C: c1})
	c.Emit(Instruction{Op: OpLessIntK, A: 2, B: 0, C: cN})
	exit := c.Emit(Instruction{Op: OpJumpIfFalse, A: 2})
	c.Emit(Instruction{Op: OpJump, C: top})
	c.SetJump(exit, int32(c.Len()))
	c.Emit(Instruction{Op: OpHalt})
	return c
}

// BenchmarkNullDispatch20M измеряет холостой dispatch на ~20M OpNull-инструкций.
func BenchmarkNullDispatch20M(b *testing.B) {
	const batch = 500
	const total = 20_000_000
	chunk := buildNullLoop(batch, total/batch)
	var vm VM
	vm.Init(chunk, nil, nil)
	vm.SetOutput(&bytes.Buffer{})
	_ = vm.Run()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.ip = 0
		if err := vm.Run(); err != nil {
			b.Fatal(err)
		}
	}
}
