package vm

import (
	"encoding/binary"
	"testing"
)

func BenchmarkCompiledFunction_Invoke(b *testing.B) {
	f := CompiledFunction{Vars: 1}
	var bytecode []byte
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpLoad))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, 0)
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpConst))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, 1)
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpIdentical))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpJumpFalse))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, 10)
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpConst))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, 1)
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpReturnValue))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpLoad))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, 0)
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpConst))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, 2)
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpIdentical))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpJumpFalse))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, 20)
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpConst))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, 2)
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpReturnValue))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpInitCall))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, 0)
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpLoad))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, 0)
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpConst))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, 2)
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpSub))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpCall))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, 1)
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpInitCall))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, 0)
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpLoad))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, 0)
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpConst))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, 3)
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpSub))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpCall))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, 1)
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpAdd))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpReturnValue))
	f.Instructions = NewInstructions(bytecode)

	ctx := GlobalContext{
		Functions:  []Callable{f},
		Constants:  []Value{Int(10), Int(0), Int(1), Int(2)},
		Classes:    []Class{&StdClass{}},
		ClassNames: []String{"stdClass"},
	}
	ctx.Init()

	fn := CompiledFunction{Instructions: []uint64{
		uint64(OpInitCall), 0,
		uint64(OpConst), 0,
		uint64(OpCall), 1,
		uint64(OpPop),
		uint64(OpReturn),
	}}
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ctx.Run(fn)
	}
}

func fibonacci(ctx *FunctionContext) Int {
	var n Int
	ParseParameters(ctx, &n)

	if n == 0 || n == 1 {
		return n
	}

	return fibonacci(ctx) + fibonacci(ctx)
}

func nativeFibonacci(n int) int {
	if n == 0 || n == 1 {
		return n
	}

	return nativeFibonacci(n-2) + nativeFibonacci(n-1)
}

func BenchmarkBuiltInFunction_Invoke(b *testing.B) {
	f := BuiltInFunction[Int]{
		Fn: fibonacci,
	}

	ctx := GlobalContext{Functions: []Callable{f}, Constants: []Value{Int(10)}}
	ctx.Init()

	var bytecode = []uint64{
		uint64(OpConst), 0,
		uint64(OpCall), 0,
		uint64(OpReturn),
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ctx.Run(CompiledFunction{
			Instructions: bytecode,
		})
	}
}

func Benchmark_fibonacci(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	parent := GlobalContext{}
	parent.Init()

	for i := 0; i < b.N; i++ {
		var ctx FunctionContext
		parent.Push(Int(10))
		parent.Child(&ctx, 1, nil, nil)
		fibonacci(&ctx)
	}
}

func Benchmark_nativeFibonacci(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		nativeFibonacci(10)
	}
}
