package vm

import "testing"

// User-friendly ABI: функция — func(ctx *Context) K, K — PHP-тип.
// Аргументы — ctx.Arg[T](i). Slot в ABI не входит.
func TestRegisterABI(t *testing.T) {
	add := Register("add", func(ctx *Context) Int {
		return ctx.Arg[Int](0) + ctx.Arg[Int](1)
	})
	greet := Register("greet", func(ctx *Context) Str {
		return "hello, " + ctx.Arg[Str](0)
	})

	main := NewChunk()
	// r0 = add(40, 2); echo
	main.Emit(Instruction{Op: OpLoadK, A: 0, C: int32(main.AddConst(IntConst(40)))})
	main.Emit(Instruction{Op: OpLoadK, A: 1, C: int32(main.AddConst(IntConst(2)))})
	main.Emit(Instruction{Op: OpCall, A: 2, B: 0, C: 0})
	main.Emit(Instruction{Op: OpEcho, A: 2})
	// r0 = greet("world"); echo
	main.Emit(Instruction{Op: OpLoadK, A: 0, C: int32(main.AddConst(StrConst("world")))})
	main.Emit(Instruction{Op: OpCall, A: 1, B: 0, C: 1})
	main.Emit(Instruction{Op: OpEcho, A: 1})
	main.Emit(Instruction{Op: OpHalt})

	got, err := runProgram(t, main, []*Function{add, greet})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != "42hello, world" {
		t.Fatalf("got %q", got)
	}
}
