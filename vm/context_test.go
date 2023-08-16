package vm

import (
	"testing"
)

func BenchmarkCompiledFunction_Invoke(b *testing.B) {
	f := CompiledFunction{Constants: []Value{Int(0), Int(1), Int(2)}, Args: 1}
	/**
	function fibonacci(int $n)
	{
		if ($n === 0) {
			return 0;
		}

		if ($n === 1) {)
			return 1;
		}

		return fibonacci($n-1) + fibonacci($n-2);
	}
	return fibonacci(10);
	*/
	// $n === 0
	f.Instructions = append(f.Instructions, byte(OpLoad), 0)
	f.Instructions = append(f.Instructions, byte(OpConst), 0)
	f.Instructions = append(f.Instructions, byte(OpIdentical))
	f.Instructions = append(f.Instructions, byte(OpJumpNZ), 10)
	f.Instructions = append(f.Instructions, byte(OpConst), 0)
	f.Instructions = append(f.Instructions, byte(OpReturn))
	// $n === 1
	f.Instructions = append(f.Instructions, byte(OpLoad), 0)
	f.Instructions = append(f.Instructions, byte(OpConst), 1)
	f.Instructions = append(f.Instructions, byte(OpIdentical))
	f.Instructions = append(f.Instructions, byte(OpJumpNZ), 20)
	f.Instructions = append(f.Instructions, byte(OpConst), 1)
	f.Instructions = append(f.Instructions, byte(OpReturn))
	// fibonacci($n-1)
	f.Instructions = append(f.Instructions, byte(OpLoad), 0)
	f.Instructions = append(f.Instructions, byte(OpConst), 1)
	f.Instructions = append(f.Instructions, byte(OpSubInt))
	f.Instructions = append(f.Instructions, byte(OpCall), 0)
	// fibonacci($n-2)
	f.Instructions = append(f.Instructions, byte(OpLoad), 0)
	f.Instructions = append(f.Instructions, byte(OpConst), 2)
	f.Instructions = append(f.Instructions, byte(OpSubInt))
	f.Instructions = append(f.Instructions, byte(OpCall), 0)
	// fibonacci($n-1) + fibonacci($n-2)
	f.Instructions = append(f.Instructions, byte(OpAddInt))
	f.Instructions = append(f.Instructions, byte(OpReturn))

	ctx := &GlobalContext{Functions: []Callable{f}}
	ctx.Init()

	program := CompiledFunction{Constants: []Value{Int(35)}}
	program.Instructions = append(program.Instructions, byte(OpConst), 0)
	program.Instructions = append(program.Instructions, byte(OpAssertType), byte(IntType))
	program.Instructions = append(program.Instructions, byte(OpCall), 0)
	program.Instructions = append(program.Instructions, byte(OpReturn))

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		program.Invoke(ctx)
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

	program := CompiledFunction{Constants: []Value{Int(10)}}
	program.Instructions = append(program.Instructions, byte(OpConst), 0)
	program.Instructions = append(program.Instructions, byte(OpAssertType), byte(IntType))
	program.Instructions = append(program.Instructions, byte(OpCall), 0)
	program.Instructions = append(program.Instructions, byte(OpReturn))

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		program.Invoke(ctx)
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
