package vm

import (
	"bytes"
	"math"
	"testing"
)

// buildFuncChunk собирает чанк функции.
func mkFunc(name string, numArgs int, c *Chunk) *Function {
	return &Function{
		Name:    name,
		NumArgs: numArgs,
		Executable: &CompiledFunction{
			chunk: c,
		},
	}
}

// runProgram запускает главный чанк с таблицей функций.
func runProgram(t *testing.T, main *Chunk, funcs []*Function) (string, error) {
	t.Helper()
	var vm VM
	vm.Init(main, funcs, nil)
	var buf bytes.Buffer
	vm.SetOutput(&buf)
	err := vm.Run()
	return buf.String(), err
}

// Тест: вызов функции. double(n) = n + n; main: r0=21; r1=double(r0); echo r1 => 42
func TestCallSimple(t *testing.T) {
	// функция double: аргумент в r0, результат = r0 + r0
	fc := NewChunk()
	fc.Emit(Instruction{Op: OpAddInt, A: 1, B: 0, C: 0}) // r1 = r0 + r0
	fc.Emit(Instruction{Op: OpRet, A: 1})
	double := mkFunc("double", 1, fc)

	// main
	main := NewChunk()
	main.Emit(Instruction{Op: OpLoadK, A: 0, C: int32(main.AddConst(IntConst(21)))}) // r0 = 21
	main.Emit(Instruction{Op: OpCall, A: 0, B: 0, C: 0})                             // r0 = double(r0)
	main.Emit(Instruction{Op: OpEcho, A: 0})
	main.Emit(Instruction{Op: OpHalt})

	got, err := runProgram(t, main, []*Function{double})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != "42" {
		t.Fatalf("got %q, want 42", got)
	}
}

// Тест: рекурсия. fact(n) = n<2 ? 1 : n*fact(n-1)
func TestCallRecursion(t *testing.T) {
	// fact: r0 = n (аргумент)
	//   r1 = (n < 2)
	//   JumpIfFalse r1 -> else
	//   r2 = 1; Ret r2
	// else:
	//   r3 = n - 1
	//   r4 = fact(r3)          <- CALL, результат в r4
	//   r5 = n * r4
	//   Ret r5
	fc := NewChunk()
	c1 := int32(fc.AddConst(IntConst(1)))
	c2 := int32(fc.AddConst(IntConst(2)))
	fc.Emit(Instruction{Op: OpLessIntK, A: 1, B: 0, C: c2}) // r1 = (n < 2)
	jmp := fc.Len()
	fc.Emit(Instruction{Op: OpJumpIfFalse, A: 1})  // -> else
	fc.Emit(Instruction{Op: OpLoadK, A: 2, C: c1}) // r2 = 1
	fc.Emit(Instruction{Op: OpRet, A: 2})
	elsePos := int32(fc.Len())
	fc.SetJump(jmp, elsePos)
	// r3 = n - 1
	fc.Emit(Instruction{Op: OpSubIntK, A: 3, B: 0, C: c1}) // r3 = n - 1
	// r4 = fact(r3)  — аргумент в r3, результат в r4
	fc.Emit(Instruction{Op: OpCall, A: 4, B: 3, C: 0})
	// r5 = n * r4
	fc.Emit(Instruction{Op: OpMulInt, A: 5, B: 0, C: 4})
	fc.Emit(Instruction{Op: OpRet, A: 5})
	fact := mkFunc("fact", 1, fc)

	main := NewChunk()
	main.Emit(Instruction{Op: OpLoadK, A: 0, C: int32(main.AddConst(IntConst(5)))})
	main.Emit(Instruction{Op: OpCall, A: 0, B: 0, C: 0})
	main.Emit(Instruction{Op: OpEcho, A: 0})
	main.Emit(Instruction{Op: OpHalt})

	got, err := runProgram(t, main, []*Function{fact})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != "120" {
		t.Fatalf("got %q, want 120", got)
	}
}

// Проверка, что регистры вызывающего не портятся вызовом.
func TestCallPreservesCallerRegs(t *testing.T) {
	// f() возвращает 7, но использует r0..r5 внутри.
	fc := NewChunk()
	for i := 0; i < 6; i++ {
		fc.Emit(Instruction{Op: OpLoadK, A: uint8(i), C: int32(fc.AddConst(IntConst(int64(i + 100))))})
	}
	fc.Emit(Instruction{Op: OpLoadK, A: 0, C: int32(fc.AddConst(IntConst(7)))})
	fc.Emit(Instruction{Op: OpRet, A: 0})
	f := mkFunc("f", 0, fc)

	main := NewChunk()
	main.Emit(Instruction{Op: OpLoadK, A: 5, C: int32(main.AddConst(IntConst(999)))}) // caller r5 = 999
	main.Emit(Instruction{Op: OpCall, A: 0, B: 0, C: 0})                              // r0 = f()
	main.Emit(Instruction{Op: OpEcho, A: 5})                                          // должно быть 999
	main.Emit(Instruction{Op: OpHalt})

	got, err := runProgram(t, main, []*Function{f})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != "999" {
		t.Fatalf("caller reg clobbered: got %q, want 999", got)
	}
}

// Нативная функция через ABI.
func TestNativeFunction(t *testing.T) {
	// addOne(ctx, args) -> args[0] + 1
	addOne := &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
		return Add(args[0], MakeInt(1)), nil
	}}
	fn := &Function{Name: "addOne", NumArgs: 1, Executable: addOne}

	main := NewChunk()
	main.Emit(Instruction{Op: OpLoadK, A: 0, C: int32(main.AddConst(IntConst(41)))})
	main.Emit(Instruction{Op: OpCall, A: 1, B: 0, C: 0})
	main.Emit(Instruction{Op: OpEcho, A: 1})
	main.Emit(Instruction{Op: OpHalt})

	got, err := runProgram(t, main, []*Function{fn})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != "42" {
		t.Fatalf("got %q, want 42", got)
	}
}

// Нативная функция может выбросить исключение через error.
func TestNativeThrow(t *testing.T) {
	boom := &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
		return Slot{}, NewThrowable("RuntimeException", "boom")
	}}
	fn := &Function{Name: "boom", Executable: boom}

	main := NewChunk()
	main.Emit(Instruction{Op: OpCall, A: 0, B: 0, C: 0})
	main.Emit(Instruction{Op: OpHalt})

	_, err := runProgram(t, main, []*Function{fn})
	if err == nil {
		t.Fatal("expected error")
	}
	if th, ok := err.(*Throwable); !ok || th.Message != "boom" {
		t.Fatalf("got %v", err)
	}
}

var _ = math.MaxInt64
