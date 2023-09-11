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
			input:                "for($i=0;$i<5;$i++){}",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(0), vm.Int(5)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpAssign), 0, uint64(vm.OpPop), uint64(vm.OpLoad), 0, uint64(vm.OpConst), 4, uint64(vm.OpLess), uint64(vm.OpJumpFalse), 17, uint64(vm.OpPostIncrement), 0, uint64(vm.OpPop), uint64(vm.OpJump), 5, uint64(vm.OpNoop), uint64(vm.OpReturn)}),
		},
		{
			input:                "do{ $i++; } while($i < 5)",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(5)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpPostIncrement), 0, uint64(vm.OpPop), uint64(vm.OpLoad), 0, uint64(vm.OpConst), 3, uint64(vm.OpLess), uint64(vm.OpJumpFalse), 0, uint64(vm.OpReturn)}),
		},
		{
			input:                "for($i=0;$i<5;$i++){ $x = &$i; }",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(0), vm.Int(5)},
			expectedInstructions: instructionsToBytecode([]uint64{uint64(vm.OpConst), 3, uint64(vm.OpAssign), 0, uint64(vm.OpPop), uint64(vm.OpLoad), 0, uint64(vm.OpConst), 4, uint64(vm.OpLess), uint64(vm.OpJumpFalse), 22, uint64(vm.OpLoadRef), 0, uint64(vm.OpAssign), 1, uint64(vm.OpPop), uint64(vm.OpPostIncrement), 0, uint64(vm.OpPop), uint64(vm.OpJump), 5, uint64(vm.OpNoop), uint64(vm.OpReturn)}),
		},
		{
			input:                "foreach([1,2] as $key => $val){  }",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(0), vm.Int(5)},
			expectedInstructions: instructionsToBytecode([]uint64{}),
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
