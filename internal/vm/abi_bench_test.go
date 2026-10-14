package vm

import (
	"io"
	"testing"
)

var sinkInt2 int64

func runChunkBench(b *testing.B, main *Chunk, funcs []*Function, classes []ClassInfo) {
	var vm VM
	vm.Init(main, funcs, classes)
	vm.SetOutput(io.Discard)
	_ = vm.Run()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.ip = 0
		vm.cur.regs[0] = MakeNull()
		vm.cur.regs[1] = MakeNull()
		vm.cur.regs[2] = MakeNull()
		if err := vm.Run(); err != nil {
			b.Fatal(err)
		}
	}
}

// ctx.Arg[Int] — цена чтения аргумента.
func BenchmarkArgInt(b *testing.B) {
	ctx := &Context{args: []Slot{MakeInt(42)}}
	var x int64
	for i := 0; i < b.N; i++ {
		x = int64(ctx.Arg[Int](0))
	}
	sinkInt2 = x
}

// Полный нативный вызов через OpCall (Register + ctx.Arg).
func BenchmarkNativeCall(b *testing.B) {
	fn := Register("double", func(ctx *Context) Int {
		return ctx.Arg[Int](0) * 2
	})
	main := NewChunk()
	main.Emit(Instruction{Op: OpLoadK, A: 0, C: int32(main.AddConst(IntConst(21)))})
	main.Emit(Instruction{Op: OpCall, A: 2, B: 0, C: 0})
	main.Emit(Instruction{Op: OpHalt})
	runChunkBench(b, main, []*Function{fn}, nil)
}

// Вызов нативного метода Go-класса через OpMethodCall.
func BenchmarkMethodCall(b *testing.B) {
	cls := RegisterClass[point]("Point", nil)
	cls.Method("sum", &Function{Name: "sum", Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			p := ctx.This[point]()
			return MakeInt(int64(p.x + p.y)), nil
		},
	}})
	main := NewChunk()
	main.Emit(Instruction{Op: OpNew, A: 0, B: 0, C: 0})
	cSum := int32(main.AddConst(StrConst("sum")))
	main.Emit(Instruction{Op: OpMethodCall, A: 2, B: 0, C: cSum})
	main.Emit(Instruction{Op: OpHalt})
	runChunkBench(b, main, nil, []ClassInfo{cls})
}

// Чтение/запись свойства (props).
func BenchmarkPropGetSet(b *testing.B) {
	cls := RegisterClass[point]("Point", nil)
	main := NewChunk()
	main.Emit(Instruction{Op: OpNew, A: 0, B: 0, C: 0})
	main.Emit(Instruction{Op: OpLoadK, A: 1, C: int32(main.AddConst(IntConst(7)))})
	cx := int32(main.AddConst(StrConst("x")))
	main.Emit(Instruction{Op: OpPropSet, A: 0, B: 1, C: cx})
	main.Emit(Instruction{Op: OpPropGet, A: 2, B: 0, C: cx})
	main.Emit(Instruction{Op: OpHalt})
	runChunkBench(b, main, nil, []ClassInfo{cls})
}

// Нативный вызов без defer/recover (сырой NativeFunction) — для сравнения.
func BenchmarkNativeCallNoDefer(b *testing.B) {
	fn := &Function{Name: "double", NumArgs: -1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			ctx.args = args
			return Slot{typ: IntType, num: uint64(ctx.Arg[Int](0) * 2)}, nil
		},
	}}
	main := NewChunk()
	main.Emit(Instruction{Op: OpLoadK, A: 0, C: int32(main.AddConst(IntConst(21)))})
	main.Emit(Instruction{Op: OpCall, A: 2, B: 0, C: 0})
	main.Emit(Instruction{Op: OpHalt})
	runChunkBench(b, main, []*Function{fn}, nil)
}

// 1000 вызовов за один Run — верхнеуровневый defer амортизируется.
func BenchmarkNativeCallMany(b *testing.B) {
	fn := Register("double", func(ctx *Context) Int { return ctx.Arg[Int](0) * 2 })
	main := NewChunk()
	main.Emit(Instruction{Op: OpLoadK, A: 0, C: int32(main.AddConst(IntConst(21)))})
	for i := 0; i < 1000; i++ {
		main.Emit(Instruction{Op: OpCall, A: 2, B: 0, C: 0})
	}
	main.Emit(Instruction{Op: OpHalt})
	var vm VM
	vm.Init(main, []*Function{fn}, nil)
	vm.SetOutput(io.Discard)
	_ = vm.Run()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm.ip = 0
		vm.cur.regs[0] = MakeInt(21)
		if err := vm.Run(); err != nil {
			b.Fatal(err)
		}
	}
}
