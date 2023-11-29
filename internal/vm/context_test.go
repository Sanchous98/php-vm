package vm

import (
	"encoding/binary"
	"testing"
)

func BenchmarkCompiledFunction_Invoke(b *testing.B) {
	f := CompiledFunction{Args: 1}
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, uint64(OpLoad))
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, 0)
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, uint64(OpConst))
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, 1)
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, uint64(OpIdentical))
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, uint64(OpJumpFalse))
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, 10)
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, uint64(OpConst))
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, 1)
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, uint64(OpReturnValue))
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, uint64(OpLoad))
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, 0)
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, uint64(OpConst))
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, 2)
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, uint64(OpIdentical))
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, uint64(OpJumpFalse))
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, 20)
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, uint64(OpConst))
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, 2)
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, uint64(OpReturnValue))
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, uint64(OpLoad))
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, 0)
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, uint64(OpConst))
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, 2)
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, uint64(OpSub))
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, uint64(OpCall))
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, 0)
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, uint64(OpLoad))
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, 0)
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, uint64(OpConst))
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, 3)
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, uint64(OpSub))
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, uint64(OpCall))
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, 0)
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, uint64(OpAdd))
	f.Instructions = binary.NativeEndian.AppendUint64(f.Instructions, uint64(OpReturnValue))

	ctx := GlobalContext{Functions: []Callable{f}, Constants: []Value{Int(20), Int(0), Int(1), Int(2)}}
	ctx.Init()

	var bytecode Bytecode
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpConst))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, 0)
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpAssertType))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(IntType))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpCall))
	bytecode = binary.NativeEndian.AppendUint64(bytecode, 0)
	bytecode = binary.NativeEndian.AppendUint64(bytecode, uint64(OpReturn))

	fn := CompiledFunction{
		Instructions: bytecode,
	}
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ctx.Run(fn)
	}
}

func fibonacci(ctx Context, args ...Value) Int {
	n := args[0].(Int)

	if n == 0 || n == 1 {
		return n
	}

	return fibonacci(ctx, n-2) + fibonacci(ctx, n-1)
}

func nativeFibonacci(n int) int {
	if n == 0 || n == 1 {
		return n
	}

	return nativeFibonacci(n-2) + nativeFibonacci(n-1)
}

func BenchmarkBuiltInFunction_Invoke(b *testing.B) {
	f := BuiltInFunction[Int]{
		Args: []Arg{
			{Name: "n", Type: IntType},
		},
		Fn: fibonacci,
	}

	ctx := GlobalContext{Functions: []Callable{f}, Constants: []Value{Int(10)}}
	ctx.Init()

	var bytecode Bytecode

	for _, op := range []uint64{
		uint64(OpConst), 0,
		uint64(OpAssertType), uint64(IntType),
		uint64(OpCall), 0,
		uint64(OpReturn),
	} {
		bytecode = binary.NativeEndian.AppendUint64(bytecode, op)
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

	for i := 0; i < b.N; i++ {
		fibonacci(nil, Int(10))
	}
}

func Benchmark_nativeFibonacci(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		nativeFibonacci(10)
	}
}
