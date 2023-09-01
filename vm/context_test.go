package vm

import (
	"testing"
)

func BenchmarkCompiledFunction_Invoke(b *testing.B) {
	f := CompiledFunction{Args: 1}
	f.Instructions = Bytecode{
		byte(OpLoad), 0,
		byte(OpConst), 1,
		byte(OpIdentical),
		byte(OpJumpNZ), 10,
		byte(OpConst), 1,
		byte(OpReturnValue),
		byte(OpLoad), 0,
		byte(OpConst), 2,
		byte(OpIdentical),
		byte(OpJumpNZ), 20,
		byte(OpConst), 2,
		byte(OpReturnValue),
		byte(OpLoad), 0,
		byte(OpConst), 2,
		byte(OpSubInt),
		byte(OpCall), 0,
		byte(OpLoad), 0,
		byte(OpConst), 3,
		byte(OpSubInt),
		byte(OpCall), 0,
		byte(OpAddInt),
		byte(OpReturnValue),
	}

	ctx := &GlobalContext{Functions: []Callable{f}, Constants: []Value{Int(10), Int(0), Int(1), Int(2)}}
	ctx.Init()

	bytecode := Bytecode{
		byte(OpConst), 0,
		byte(OpAssertType), byte(IntType),
		byte(OpCall), 0,
		byte(OpReturnValue),
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ctx.Run(CompiledFunction{
			Instructions: bytecode,
		})

		if ctx.TopIndex() > 0 {
			panic("")
		}
	}
}

func fibonacci(args ...Value) Int {
	n := args[0].(Int)

	if n == 0 || n == 1 {
		return n
	}

	return fibonacci(n-2) + fibonacci(n-1)
}

func nativeFibonacci(n int) int {
	if n == 0 || n == 1 {
		return n
	}

	return nativeFibonacci(n-2) + nativeFibonacci(n-1)
}

func BenchmarkBuiltInFunction_Invoke(b *testing.B) {
	f := BuiltInFunction[Int]{
		Args: 1,
		Fn:   fibonacci,
	}

	ctx := &GlobalContext{Functions: []Callable{f}}
	ctx.Init()

	bytecode := []byte{
		byte(OpConst), 0,
		byte(OpAssertType), byte(IntType),
		byte(OpCall), 0,
		byte(OpReturn),
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
		fibonacci(Int(10))
	}
}

func Benchmark_nativeFibonacci(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		nativeFibonacci(10)
	}
}
