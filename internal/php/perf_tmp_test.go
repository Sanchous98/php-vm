package php

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"phpvm/internal/vm"
)

func runChunkTimed(t *testing.T, name string, ch *vm.Chunk, funcs []*vm.Function, classes []vm.ClassInfo) {
	t.Helper()
	var m vm.VM
	m.Init(ch, funcs, classes)
	m.SetStepLimit(0)
	var out bytes.Buffer
	m.SetOutput(&out)
	start := time.Now()
	if err := m.Run(); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	fmt.Printf("%-16s %8.1f ms  out=%s\n", name, float64(time.Since(start).Microseconds())/1000, out.String())
}

func TestHandOpt(t *testing.T) {
	src := `<?php $s=0; for($i=0;$i<10000000;$i++){ $s=$s+$i; } echo $s;`
	c, err := Compile(src)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("=== compiler bytecode (main) ===")
	fmt.Print(c.Main.Disassemble())
	runChunkTimed(t, "compiler", c.Main, c.Funcs, c.Classes)

	// Ручной «оптимальный» чанк: локальные регистры, специализированные опкоды,
	// K-формы, 5 инструкций на итерацию.
	ch := vm.NewChunk()
	r0, r1, r2 := uint8(0), uint8(1), uint8(2)
	ch.Emit(vm.Instruction{Op: vm.OpLoadK, A: r0, C: int32(ch.AddConst(vm.IntConst(0)))})
	ch.Emit(vm.Instruction{Op: vm.OpLoadK, A: r1, C: int32(ch.AddConst(vm.IntConst(0)))})
	cTenM := int32(ch.AddConst(vm.IntConst(10000000)))
	cOne := int32(ch.AddConst(vm.IntConst(1)))
	loop := ch.Len()
	ch.Emit(vm.Instruction{Op: vm.OpLessIntK, A: r2, B: r1, C: cTenM})
	jf := ch.Emit(vm.Instruction{Op: vm.OpJumpIfFalse, A: r2})
	ch.Emit(vm.Instruction{Op: vm.OpAddInt, A: r0, B: r0, C: int32(r1)})
	ch.Emit(vm.Instruction{Op: vm.OpAddIntK, A: r1, B: r1, C: cOne})
	ch.Emit(vm.Instruction{Op: vm.OpJump, C: int32(loop)})
	ch.SetJump(jf, int32(ch.Len()))
	ch.Emit(vm.Instruction{Op: vm.OpEcho, A: r0})
	ch.Emit(vm.Instruction{Op: vm.OpHalt})
	fmt.Println("=== hand-optimized bytecode ===")
	fmt.Print(ch.Disassemble())
	runChunkTimed(t, "hand-opt", ch, nil, nil)
}
