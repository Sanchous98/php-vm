package compiler

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"php-vm/vm"
	"testing"
)

type compilerTestCase struct {
	input                string
	expectedConstants    []vm.Value
	expectedInstructions vm.Bytecode
}

func TestFibonacci(t *testing.T) {
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

	instructions := vm.Bytecode{
		byte(vm.OpLoad), 0,
		byte(vm.OpConst), 2,
		byte(vm.OpIdentical),
		byte(vm.OpJumpNZ), 10,
		byte(vm.OpConst), 2,
		byte(vm.OpReturnValue),
		byte(vm.OpLoad), 0,
		byte(vm.OpConst), 3,
		byte(vm.OpIdentical),
		byte(vm.OpJumpNZ), 20,
		byte(vm.OpConst), 3,
		byte(vm.OpReturnValue),
		byte(vm.OpLoad), 0,
		byte(vm.OpConst), 3,
		byte(vm.OpSub),
		byte(vm.OpCall), 0,
		byte(vm.OpLoad), 0,
		byte(vm.OpConst), 4,
		byte(vm.OpSub),
		byte(vm.OpCall), 0,
		byte(vm.OpAdd),
		byte(vm.OpReturnValue),
	}

	t.Run("fibonacci", func(t *testing.T) {
		var compiler Compiler
		_, global := compiler.Compile([]byte(input))
		assert.Equal(t, instructions.String(), global.Functions[0].(vm.CompiledFunction).Instructions.String())
		assert.Equal(t, []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(0), vm.Int(1), vm.Int(2)}, global.Constants)
	})
}

func TestVariable(t *testing.T) {
	cases := [...]compilerTestCase{
		{
			input:                "$x = 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpAssign), 0},
		},
		{
			input:                "$x = $y + 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1)},
			expectedInstructions: vm.Bytecode{byte(vm.OpLoad), 0, byte(vm.OpConst), 2, byte(vm.OpAdd), byte(vm.OpAssign), 1},
		},
		{
			input:                "$x += 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpAssignAdd), 0},
		},
		{
			input:                "$x -= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpAssignSub), 0},
		},
		{
			input:                "$x *= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpAssignMul), 0},
		},
		{
			input:                "$x /= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpAssignDiv), 0},
		},
		{
			input:                "$x **= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpAssignPow), 0},
		},
		{
			input:                "$x &= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpAssignBwAnd), 0},
		},
		{
			input:                "$x |= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpAssignBwOr), 0},
		},
		{
			input:                "$x ^= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpAssignBwXor), 0},
		},
		{
			input:                "$x <<= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpAssignShiftLeft), 0},
		},
		{
			input:                "$x >>= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpAssignShiftRight), 0},
		},
		{
			input:                "$x .= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpAssignConcat), 0},
		},
		{
			input:                "$x++",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false)},
			expectedInstructions: vm.Bytecode{byte(vm.OpPostIncrement), 0},
		},
		{
			input:                "$x--",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false)},
			expectedInstructions: vm.Bytecode{byte(vm.OpPostDecrement), 0},
		},
		{
			input:                "++$x",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false)},
			expectedInstructions: vm.Bytecode{byte(vm.OpPreIncrement), 0},
		},
		{
			input:                "--$x",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false)},
			expectedInstructions: vm.Bytecode{byte(vm.OpPreDecrement), 0},
		},
	}

	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			var compiler Compiler
			fn, global := compiler.Compile([]byte(fmt.Sprintf("<?php\n%s;", c.input)))
			assert.Equal(t, c.expectedInstructions, fn.Instructions)
			assert.Equal(t, c.expectedConstants, global.Constants)
		})
	}
}

func TestBranches(t *testing.T) {
	cases := [...]compilerTestCase{
		{
			input:                "if (true) {}\nreturn;",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 0, byte(vm.OpJumpNZ), 4, byte(vm.OpReturn), byte(vm.OpNoop)},
		},
	}

	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			var compiler Compiler
			fn, global := compiler.Compile([]byte(fmt.Sprintf("<?php\n%s;", c.input)))
			assert.Equal(t, c.expectedInstructions, fn.Instructions)
			assert.Equal(t, c.expectedConstants, global.Constants)
		})
	}
}

func TestArithmetic(t *testing.T) {
	cases := [...]compilerTestCase{
		{
			input:                "1 + 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1), vm.Int(2)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpConst), 3, byte(vm.OpAdd)},
		},
		{
			input:                "1 - 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1), vm.Int(2)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpConst), 3, byte(vm.OpSub)},
		},
		{
			input:                "1 * 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1), vm.Int(2)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpConst), 3, byte(vm.OpMul)},
		},
		{
			input:                "1 / 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1), vm.Int(2)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpConst), 3, byte(vm.OpDiv)},
		},
		{
			input:                "1 % 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1), vm.Int(2)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpConst), 3, byte(vm.OpMod)},
		},
		{
			input:                "1 ** 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1), vm.Int(2)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpConst), 3, byte(vm.OpPow)},
		},
		{
			input:                "1 & 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1), vm.Int(2)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpConst), 3, byte(vm.OpBwAnd)},
		},
		{
			input:                "1 | 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1), vm.Int(2)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpConst), 3, byte(vm.OpBwOr)},
		},
		{
			input:                "1 ^ 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1), vm.Int(2)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpConst), 3, byte(vm.OpBwXor)},
		},
		{
			input:                "~1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(1)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpBwNot)},
		},
		{
			input:                "2 << 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(2), vm.Int(1)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpConst), 3, byte(vm.OpShiftLeft)},
		},
		{
			input:                "2 >> 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Int(2), vm.Int(1)},
			expectedInstructions: vm.Bytecode{byte(vm.OpConst), 2, byte(vm.OpConst), 3, byte(vm.OpShiftRight)},
		},
	}

	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			var compiler Compiler
			fn, global := compiler.Compile([]byte(fmt.Sprintf("<?php\n%s;", c.input)))
			assert.Equal(t, c.expectedInstructions, fn.Instructions)
			assert.Equal(t, c.expectedConstants, global.Constants)
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
	fibonacci(10);
	return;
	`)

	b.ReportAllocs()
	b.ResetTimer()

	var compiler Compiler
	fn, ctx := compiler.Compile(input)

	for i := 0; i < b.N; i++ {
		ctx.Run(fn)
	}
}
