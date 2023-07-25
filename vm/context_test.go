package vm

import (
	"testing"
)

func BenchmarkFunction_Run(b *testing.B) {
	f := CompiledFunction{Constants: []Value{Int(0), Int(1), Int(2)}, NumVars: 1}
	/**
	function fibonacci(int $n)
	{
		if ($n === 0) {
			return 0;
		}

		if ($n === 1) {
			return 1;
		}

		return fibonacci($n-1) + fibonacci($n-2);
	}~
	return fibonacci(10);
	*/
	// $n === 0
	f.Instructions = append(f.Instructions, byte(OpLoad), 0)
	f.Instructions = append(f.Instructions, byte(OpConst), 0)
	f.Instructions = append(f.Instructions, byte(OpIdentical))
	f.Instructions = append(f.Instructions, byte(OpJumpNZ), 9)
	f.Instructions = append(f.Instructions, byte(OpConst), 0)
	f.Instructions = append(f.Instructions, byte(OpReturn))
	// $n === 1
	f.Instructions = append(f.Instructions, byte(OpLoad), 0)
	f.Instructions = append(f.Instructions, byte(OpConst), 1)
	f.Instructions = append(f.Instructions, byte(OpIdentical))
	f.Instructions = append(f.Instructions, byte(OpJumpNZ), 19)
	f.Instructions = append(f.Instructions, byte(OpConst), 1)
	f.Instructions = append(f.Instructions, byte(OpReturn))
	// fibonacci($n-1)
	f.Instructions = append(f.Instructions, byte(OpLoad), 0)
	f.Instructions = append(f.Instructions, byte(OpConst), 1)
	f.Instructions = append(f.Instructions, byte(OpSubInt))
	f.Instructions = append(f.Instructions, byte(OpAssertType), byte(IntType))
	f.Instructions = append(f.Instructions, byte(OpCall), 0, 1)
	// fibonacci($n-2)
	f.Instructions = append(f.Instructions, byte(OpLoad), 0)
	f.Instructions = append(f.Instructions, byte(OpConst), 2)
	f.Instructions = append(f.Instructions, byte(OpSubInt))
	f.Instructions = append(f.Instructions, byte(OpAssertType), byte(IntType))
	f.Instructions = append(f.Instructions, byte(OpCall), 0, 1)
	// fibonacci($n-1) + fibonacci($n-2)
	f.Instructions = append(f.Instructions, byte(OpAddInt))
	f.Instructions = append(f.Instructions, byte(OpReturn))

	ctx := &GlobalContext{Functions: []Callable{f}}
	ctx.Init()

	program := CompiledFunction{Constants: []Value{Int(2)}}
	program.Instructions = append(program.Instructions, byte(OpConst), 0)
	program.Instructions = append(program.Instructions, byte(OpAssertType), byte(IntType))
	program.Instructions = append(program.Instructions, byte(OpCall), 0, 1)
	program.Instructions = append(program.Instructions, byte(OpReturn))

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if program.Call(ctx.Child(0)) != Int(1) {
			panic("")
		}
	}
}
