package compiler

import (
	"encoding/binary"
	"fmt"
	"github.com/stretchr/testify/assert"
	"php-vm/internal/vm"
	"testing"
)

type compilerTestCase struct {
	input                string
	expectedConstants    []vm.Value
	expectedInstructions vm.Bytecode
}

func instructionsToBytecode(i []uint64) (b vm.Bytecode) {
	for _, instruction := range i {
		b = binary.NativeEndian.AppendUint64(b, instruction)
	}

	return
}

func TestFibonacciRecursive(t *testing.T) {
	const input = `<?php
	function fibonacci($n)
	{
		if ($n === 0) {
			return 0;
		}

		if ($n === 1) {
			return 1;
		}

		return fibonacci($n - 1) + fibonacci($n - 2);
	}`

	var instructions vm.Bytecode

	instructions = binary.NativeEndian.AppendUint64(instructions, uint64(vm.OpLoad))
	instructions = binary.NativeEndian.AppendUint64(instructions, 0)
	instructions = binary.NativeEndian.AppendUint64(instructions, uint64(vm.OpConst))
	instructions = binary.NativeEndian.AppendUint64(instructions, 3)
	instructions = binary.NativeEndian.AppendUint64(instructions, uint64(vm.OpIdentical))
	instructions = binary.NativeEndian.AppendUint64(instructions, uint64(vm.OpJumpFalse))
	instructions = binary.NativeEndian.AppendUint64(instructions, 10)
	instructions = binary.NativeEndian.AppendUint64(instructions, uint64(vm.OpConst))
	instructions = binary.NativeEndian.AppendUint64(instructions, 3)
	instructions = binary.NativeEndian.AppendUint64(instructions, uint64(vm.OpReturnValue))
	instructions = binary.NativeEndian.AppendUint64(instructions, uint64(vm.OpLoad))
	instructions = binary.NativeEndian.AppendUint64(instructions, 0)
	instructions = binary.NativeEndian.AppendUint64(instructions, uint64(vm.OpConst))
	instructions = binary.NativeEndian.AppendUint64(instructions, 4)
	instructions = binary.NativeEndian.AppendUint64(instructions, uint64(vm.OpIdentical))
	instructions = binary.NativeEndian.AppendUint64(instructions, uint64(vm.OpJumpFalse))
	instructions = binary.NativeEndian.AppendUint64(instructions, 20)
	instructions = binary.NativeEndian.AppendUint64(instructions, uint64(vm.OpConst))
	instructions = binary.NativeEndian.AppendUint64(instructions, 4)
	instructions = binary.NativeEndian.AppendUint64(instructions, uint64(vm.OpReturnValue))
	instructions = binary.NativeEndian.AppendUint64(instructions, uint64(vm.OpLoad))
	instructions = binary.NativeEndian.AppendUint64(instructions, 0)
	instructions = binary.NativeEndian.AppendUint64(instructions, uint64(vm.OpConst))
	instructions = binary.NativeEndian.AppendUint64(instructions, 4)
	instructions = binary.NativeEndian.AppendUint64(instructions, uint64(vm.OpSub))
	instructions = binary.NativeEndian.AppendUint64(instructions, uint64(vm.OpCall))
	instructions = binary.NativeEndian.AppendUint64(instructions, 0)
	instructions = binary.NativeEndian.AppendUint64(instructions, uint64(vm.OpLoad))
	instructions = binary.NativeEndian.AppendUint64(instructions, 0)
	instructions = binary.NativeEndian.AppendUint64(instructions, uint64(vm.OpConst))
	instructions = binary.NativeEndian.AppendUint64(instructions, 5)
	instructions = binary.NativeEndian.AppendUint64(instructions, uint64(vm.OpSub))
	instructions = binary.NativeEndian.AppendUint64(instructions, uint64(vm.OpCall))
	instructions = binary.NativeEndian.AppendUint64(instructions, 0)
	instructions = binary.NativeEndian.AppendUint64(instructions, uint64(vm.OpAdd))
	instructions = binary.NativeEndian.AppendUint64(instructions, uint64(vm.OpReturnValue))

	t.Run("fibonacci", func(t *testing.T) {
		compiler := NewCompiler(nil)
		ctx := new(vm.GlobalContext)
		_ = compiler.Compile([]byte(fmt.Sprintf("<?php\n%s;", input)), ctx)
		assert.Equal(t, instructions.String(), ctx.Functions[0].(vm.CompiledFunction).Instructions.String())
		assert.Equal(t, []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(0), vm.Int(1), vm.Int(2)}, ctx.Constants)
	})
}

func TestFibonacciLoop(t *testing.T) {
	const input = `<?php
	function fibonacci($n)
	{
		if ($n === 0) {
			return 0;
		}

		if ($n < 2) {
			return 1;
		}

		$prev = 1;
		$current = 1;

		for ($i = 2; $i < $n; $i++) {
			$temp = $prev + $current;
			$prev = $current;
			$current = $temp;
		}
		
		return $current;
	}
	return fibonacci(10);`

	instructions := []uint64{
		uint64(vm.OpLoad), 0, // 00000: LOAD          0
		uint64(vm.OpConst), 3, // 00002: CONST         3
		uint64(vm.OpIdentical),     // 00004: IDENTICAL
		uint64(vm.OpJumpFalse), 10, // 00005: JUMP_NZ       10
		uint64(vm.OpConst), 3, // 00007: CONST         3
		uint64(vm.OpReturnValue), // 00009: RETURN_VAL
		uint64(vm.OpLoad), 0,     // 00010: LOAD          0
		uint64(vm.OpConst), 4, // 00012: CONST         4
		uint64(vm.OpLess),          // 00014: LT
		uint64(vm.OpJumpFalse), 20, // 00015: JUMP_NZ       20
		uint64(vm.OpConst), 5, // 00017: CONST         5
		uint64(vm.OpReturnValue), // 00019: RETURN_VAL
		uint64(vm.OpConst), 5,    // 00020: CONST         5
		uint64(vm.OpAssign), 1, // 00022: ASSIGN        1
		uint64(vm.OpPop),      // 00024: POP
		uint64(vm.OpConst), 5, // 00025: CONST         5
		uint64(vm.OpAssign), 2, // 00027: ASSIGN        2
		uint64(vm.OpPop),      // 00029: POP
		uint64(vm.OpConst), 4, // 00030: CONST         4
		uint64(vm.OpAssign), 3, // 00032: ASSIGN        3
		uint64(vm.OpPop),     // 00034: POP
		uint64(vm.OpLoad), 3, // 00035: LOAD          3
		uint64(vm.OpLoad), 0, // 00037: LOAD          0
		uint64(vm.OpLess),          // 00039: LT
		uint64(vm.OpJumpFalse), 65, // 00040: JUMP_NZ       65
		uint64(vm.OpLoad), 1, // 00042: LOAD          1
		uint64(vm.OpLoad), 2, // 00044: LOAD          2
		uint64(vm.OpAdd),       // 00046: ADD
		uint64(vm.OpAssign), 4, // 00047: ASSIGN        4
		uint64(vm.OpPop),     // 00049: POP
		uint64(vm.OpLoad), 2, // 00050: LOAD          2
		uint64(vm.OpAssign), 1, // 00052: ASSIGN        1
		uint64(vm.OpPop),     // 00054: POP
		uint64(vm.OpLoad), 4, // 00055: LOAD          4
		uint64(vm.OpAssign), 2, // 00057: ASSIGN        2
		uint64(vm.OpPop),              // 00059: POP
		uint64(vm.OpPostIncrement), 3, // 00060: POST_INC      3
		uint64(vm.OpPop),      // 00062: POP
		uint64(vm.OpJump), 35, // 00063: JUMP          35
		uint64(vm.OpLoad), 2, // 00065: LOAD          2
		uint64(vm.OpReturnValue), // 00067: RETURN_VAL
	}

	t.Run("fibonacci", func(t *testing.T) {
		compiler := NewCompiler(nil)
		ctx := new(vm.GlobalContext)
		fn := compiler.Compile([]byte(input), ctx)
		assert.Equal(t, instructionsToBytecode(instructions).String(), ctx.Functions[0].(vm.CompiledFunction).Instructions.String())
		assert.Equal(t, []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(0), vm.Int(2), vm.Int(1), vm.Int(10)}, ctx.Constants)
		assert.Equal(t, vm.Int(55), ctx.Run(fn))
	})
}

func TestLoop(t *testing.T) {
	cases := [...]compilerTestCase{
		{
			input:                "while($i<5){ $i++; }",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(5)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpLoad), 0, uint64(vm.OpConst), 3, uint64(vm.OpLess), uint64(vm.OpJumpFalse), 12, uint64(vm.OpPostIncrement), 0, uint64(vm.OpPop), uint64(vm.OpJump), 0, uint64(vm.OpNoop), uint64(vm.OpReturn)}),
		},
		{
			input:                "for($i=0;$i<5;$i++){}",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(0), vm.Int(5)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpAssign), 0, uint64(vm.OpPop), uint64(vm.OpLoad), 0, uint64(vm.OpConst), 4, uint64(vm.OpLess), uint64(vm.OpJumpFalse), 17, uint64(vm.OpPostIncrement), 0, uint64(vm.OpPop), uint64(vm.OpJump), 5, uint64(vm.OpNoop), uint64(vm.OpReturn)}),
		},
		{
			input:                "do{ $i++; } while($i < 5)",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(5)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpPostIncrement), 0, uint64(vm.OpPop), uint64(vm.OpLoad), 0, uint64(vm.OpConst), 3, uint64(vm.OpLess), uint64(vm.OpJumpFalse), 0, uint64(vm.OpReturn)}),
		},
	}

	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			compiler := NewCompiler(nil)
			ctx := new(vm.GlobalContext)
			fn := compiler.Compile([]byte(fmt.Sprintf("<?php\n%s;", c.input)), ctx)
			assert.Equal(t, c.expectedInstructions, fn.Instructions)
			assert.Equal(t, c.expectedConstants, ctx.Constants)
		})
	}
}

func TestVariable(t *testing.T) {
	cases := [...]compilerTestCase{
		{
			input:                "$x = 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpAssign), 0, uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "$x = $y + 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpLoad), 0, uint64(vm.OpConst), 3, uint64(vm.OpAdd), uint64(vm.OpAssign), 1, uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "$x += 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpAssignAdd), 0, uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "$x -= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpAssignSub), 0, uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "$x *= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpAssignMul), 0, uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "$x /= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpAssignDiv), 0, uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "$x **= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpAssignPow), 0, uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "$x &= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpAssignBwAnd), 0, uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "$x |= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpAssignBwOr), 0, uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "$x ^= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpAssignBwXor), 0, uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "$x <<= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpAssignShiftLeft), 0, uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "$x >>= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpAssignShiftRight), 0, uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "$x .= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpAssignConcat), 0, uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "$x++",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpPostIncrement), 0, uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "$x--",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpPostDecrement), 0, uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "++$x",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpPreIncrement), 0, uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "--$x",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpPreDecrement), 0, uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
	}

	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			compiler := NewCompiler(nil)
			ctx := new(vm.GlobalContext)
			fn := compiler.Compile([]byte(fmt.Sprintf("<?php\n%s;", c.input)), ctx)
			assert.Equal(t, c.expectedInstructions, fn.Instructions)
			assert.Equal(t, c.expectedConstants, ctx.Constants)
		})
	}
}

func TestBranches(t *testing.T) {
	cases := [...]compilerTestCase{
		{
			input:                "if (true) {}\n",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 0, uint64(vm.OpJumpFalse), 4, uint64(vm.OpNoop), uint64(vm.OpReturn)}),
		},
	}

	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			compiler := NewCompiler(nil)
			ctx := new(vm.GlobalContext)
			fn := compiler.Compile([]byte(fmt.Sprintf("<?php\n%s;", c.input)), ctx)
			assert.Equal(t, c.expectedInstructions, fn.Instructions)
			assert.Equal(t, c.expectedConstants, ctx.Constants)
		})
	}
}

func TestArithmetic(t *testing.T) {
	cases := [...]compilerTestCase{
		{
			input:                "1 + 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1), vm.Int(2)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpConst), 4, uint64(vm.OpAdd), uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "1 - 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1), vm.Int(2)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpConst), 4, uint64(vm.OpSub), uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "1 * 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1), vm.Int(2)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpConst), 4, uint64(vm.OpMul), uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "1 / 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1), vm.Int(2)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpConst), 4, uint64(vm.OpDiv), uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "1 % 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1), vm.Int(2)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpConst), 4, uint64(vm.OpMod), uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "1 ** 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1), vm.Int(2)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpConst), 4, uint64(vm.OpPow), uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "1 & 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1), vm.Int(2)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpConst), 4, uint64(vm.OpBwAnd), uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "1 | 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1), vm.Int(2)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpConst), 4, uint64(vm.OpBwOr), uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "1 ^ 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1), vm.Int(2)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpConst), 4, uint64(vm.OpBwXor), uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "~1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpBwNot), uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "2 << 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(2), vm.Int(1)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpConst), 4, uint64(vm.OpShiftLeft), uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
		{
			input:                "2 >> 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(2), vm.Int(1)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpConst), 4, uint64(vm.OpShiftRight), uint64(vm.OpPop), uint64(vm.OpReturn)}),
		},
	}

	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			compiler := NewCompiler(nil)
			ctx := new(vm.GlobalContext)
			fn := compiler.Compile([]byte(fmt.Sprintf("<?php\n%s;", c.input)), ctx)
			assert.Equal(t, c.expectedInstructions, fn.Instructions)
			assert.Equal(t, c.expectedConstants, ctx.Constants)
		})
	}
}

func BenchmarkFibonacci(b *testing.B) {
	var input = []byte(`<?php
	function fibonacci(int $n)
	{
		if ($n === 0) {
			return 0;
		}

		if ($n === 1) {
			return 1;
		}

		return fibonacci($n-1) + fibonacci($n-2);
	}
	echo fibonacci(10);
	return;
	`)

	b.ReportAllocs()
	b.ResetTimer()

	ctx := new(vm.GlobalContext)
	compiler := NewCompiler(nil)
	fn := compiler.Compile(input, ctx)

	for i := 0; i < b.N; i++ {
		ctx.Run(fn)
	}
}
