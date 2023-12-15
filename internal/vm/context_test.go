package vm

import (
	"testing"
)

func BenchmarkCompiledFunction_Invoke(b *testing.B) {
	f := Function{Vars: []String{"n"}, Executable: Instructions{
		uint64(OpLoad) << 32,
		uint64(OpConst)<<32 + 1,
		uint64(OpIdentical) << 32,
		uint64(OpJumpFalse)<<32 + 6,
		uint64(OpConst)<<32 + 1,
		uint64(OpReturnValue) << 32,
		uint64(OpLoad) << 32,
		uint64(OpConst)<<32 + 2,
		uint64(OpIdentical) << 32,
		uint64(OpJumpFalse)<<32 + 12,
		uint64(OpConst)<<32 + 2,
		uint64(OpReturnValue) << 32,
		uint64(OpInitCall) << 32,
		uint64(OpLoad) << 32,
		uint64(OpConst)<<32 + 2,
		uint64(OpSub) << 32,
		uint64(OpCall)<<32 + 1,
		uint64(OpInitCall) << 32,
		uint64(OpLoad) << 32,
		uint64(OpConst)<<32 + 3,
		uint64(OpSub) << 32,
		uint64(OpCall)<<32 + 1,
		uint64(OpAdd) << 32,
		uint64(OpReturnValue) << 32,
	}}

	ctx := GlobalContext{
		Functions: []*Function{&f},
		Constants: []Value{Int(35), Int(0), Int(1), Int(2)},
	}
	ctx.Init()

	fn := Function{Executable: Instructions{
		uint64(OpInitCall) << 32,
		uint64(OpConst) << 32,
		uint64(OpCall)<<32 + 1,
		uint64(OpPop) << 32,
		uint64(OpReturn) << 32,
	}}
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ctx.Run(fn)
	}
}

func wFibonacci(ctx *FunctionContext) Int {
	var n int
	ParseParameters(ctx, &n)
	return Int(nativeFibonacci(n))
}

func nativeFibonacci(n int) int {
	if n == 0 || n == 1 {
		return n
	}

	return nativeFibonacci(n-2) + nativeFibonacci(n-1)
}

func BenchmarkBuiltInFunction_Invoke(b *testing.B) {
	f := BuiltInFunction[Int](wFibonacci)

	ctx := GlobalContext{Functions: []*Function{{Executable: f}}, Constants: []Value{Int(10)}}
	ctx.Init()

	bytecode := Instructions{
		uint64(OpInitCall) << 32,
		uint64(OpConst) << 32,
		uint64(OpCall)<<32 + 1,
		uint64(OpPop) << 32,
		uint64(OpReturn) << 32,
	}

	fn := Function{Executable: bytecode}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ctx.Run(fn)
	}
}

func Benchmark_nativeFibonacci(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		nativeFibonacci(10)
	}
}
