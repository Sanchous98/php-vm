package compiler

import (
	"fmt"
	"github.com/stretchr/testify/assert"
	"php-vm/internal/vm"
	"testing"
)

type compilerTestCase struct {
	input                string
	expectedConstants    []vm.Value
	expectedInstructions vm.Instructions
}

func TestLoop(t *testing.T) {
	cases := [...]compilerTestCase{
		{
			input:                "while($i<5){ $i++; }",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(5)},
			expectedInstructions: []uint64{uint64(vm.OpLoad) << 32, uint64(vm.OpConst)<<32 + 3, uint64(vm.OpLess) << 32, uint64(vm.OpJumpFalse)<<32 + 7, uint64(vm.OpPostIncrement) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpJump) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "for($i=0;$i<5;$i++){}",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(0), vm.Int(5)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpAssign) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpLoad) << 32, uint64(vm.OpConst)<<32 + 4, uint64(vm.OpLess) << 32, uint64(vm.OpJumpFalse)<<32 + 10, uint64(vm.OpPostIncrement) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpJump)<<32 + 3, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "for($i=0;$i<5;$i++){}",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(0), vm.Int(5)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpAssign) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpLoad) << 32, uint64(vm.OpConst)<<32 + 4, uint64(vm.OpLess) << 32, uint64(vm.OpJumpFalse)<<32 + 10, uint64(vm.OpPostIncrement) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpJump)<<32 + 3, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "do{ $i++; } while($i < 5)",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(5)},
			expectedInstructions: []uint64{uint64(vm.OpPostIncrement) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpLoad) << 32, uint64(vm.OpConst)<<32 + 3, uint64(vm.OpLess) << 32, uint64(vm.OpJumpFalse) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "for($i=0;$i<5;$i++){ $x = &$i; }",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(0), vm.Int(5)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpAssign) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpLoad) << 32, uint64(vm.OpConst)<<32 + 4, uint64(vm.OpLess) << 32, uint64(vm.OpJumpFalse)<<32 + 13, uint64(vm.OpLoadRef) << 32, uint64(vm.OpAssign)<<32 + 1, uint64(vm.OpPop) << 32, uint64(vm.OpPostIncrement) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpJump)<<32 + 3, uint64(vm.OpReturn) << 32},
		},
	}

	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			compiler := NewCompiler(nil)
			ctx := new(vm.GlobalContext)
			fn := compiler.Compile([]byte(fmt.Sprintf("<?php\n%s;", c.input)), ctx)
			assert.Equal(t, c.expectedInstructions, fn.Executable)
			assert.Equal(t, c.expectedConstants, ctx.Constants)
		})
	}
}

func TestVariable(t *testing.T) {
	cases := [...]compilerTestCase{
		{
			input:                "$x = 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpAssign) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "$x = $y + 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: []uint64{uint64(vm.OpLoad) << 32, uint64(vm.OpConst)<<32 + 3, uint64(vm.OpAdd) << 32, uint64(vm.OpAssign)<<32 + 1, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "$x += 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpAssignAdd) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "$x -= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpAssignSub) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "$x *= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpAssignMul) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "$x /= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpAssignDiv) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "$x **= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpAssignPow) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "$x &= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpAssignBwAnd) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "$x |= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpAssignBwOr) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "$x ^= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpAssignBwXor) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "$x <<= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpAssignShiftLeft) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "$x >>= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpAssignShiftRight) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "$x .= 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpAssignConcat) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "$x++",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}},
			expectedInstructions: []uint64{uint64(vm.OpPostIncrement) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "$x--",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}},
			expectedInstructions: []uint64{uint64(vm.OpPostDecrement) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "++$x",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}},
			expectedInstructions: []uint64{uint64(vm.OpPreIncrement) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "--$x",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}},
			expectedInstructions: []uint64{uint64(vm.OpPreDecrement) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
	}

	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			compiler := NewCompiler(nil)
			ctx := new(vm.GlobalContext)
			fn := compiler.Compile([]byte(fmt.Sprintf("<?php\n%s;", c.input)), ctx)
			assert.Equal(t, c.expectedInstructions, fn.Executable)
			assert.Equal(t, c.expectedConstants, ctx.Constants)
		})
	}
}

func TestBranches(t *testing.T) {
	cases := [...]compilerTestCase{
		{
			input:                "if (true) {}\n",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}},
			expectedInstructions: []uint64{uint64(vm.OpConst) << 32, uint64(vm.OpJumpFalse)<<32 + 2, uint64(vm.OpReturn) << 32},
		},
	}

	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			compiler := NewCompiler(nil)
			ctx := new(vm.GlobalContext)
			fn := compiler.Compile([]byte(fmt.Sprintf("<?php\n%s;", c.input)), ctx)
			assert.Equal(t, c.expectedInstructions, fn.Executable)
			assert.Equal(t, c.expectedConstants, ctx.Constants)
		})
	}
}

func TestArithmetic(t *testing.T) {
	cases := [...]compilerTestCase{
		{
			input:                "1 + 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1), vm.Int(2)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpConst)<<32 + 4, uint64(vm.OpAdd) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "1 - 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1), vm.Int(2)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpConst)<<32 + 4, uint64(vm.OpSub) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "1 * 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1), vm.Int(2)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpConst)<<32 + 4, uint64(vm.OpMul) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "1 / 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1), vm.Int(2)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpConst)<<32 + 4, uint64(vm.OpDiv) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "1 % 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1), vm.Int(2)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpConst)<<32 + 4, uint64(vm.OpMod) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "1 ** 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1), vm.Int(2)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpConst)<<32 + 4, uint64(vm.OpPow) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "1 & 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1), vm.Int(2)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpConst)<<32 + 4, uint64(vm.OpBwAnd) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "1 | 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1), vm.Int(2)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpConst)<<32 + 4, uint64(vm.OpBwOr) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "1 ^ 2",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1), vm.Int(2)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpConst)<<32 + 4, uint64(vm.OpBwXor) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "~1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpBwNot) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "2 << 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(2), vm.Int(1)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpConst)<<32 + 4, uint64(vm.OpShiftLeft) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
		{
			input:                "2 >> 1",
			expectedConstants:    []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(2), vm.Int(1)},
			expectedInstructions: []uint64{uint64(vm.OpConst)<<32 + 3, uint64(vm.OpConst)<<32 + 4, uint64(vm.OpShiftRight) << 32, uint64(vm.OpPop) << 32, uint64(vm.OpReturn) << 32},
		},
	}

	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			compiler := NewCompiler(nil)
			ctx := new(vm.GlobalContext)
			fn := compiler.Compile([]byte(fmt.Sprintf("<?php\n%s;", c.input)), ctx)
			assert.Equal(t, c.expectedInstructions, fn.Executable)
			assert.Equal(t, c.expectedConstants, ctx.Constants)
		})
	}
}

func TestIterators(t *testing.T) {
	cases := [...]compilerTestCase{
		{
			input:             "foreach([1,2] as $key => $val){  }",
			expectedConstants: []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1), vm.Int(2)},
			expectedInstructions: []uint64{
				uint64(vm.OpArrayNew) << 32,
				uint64(vm.OpArrayAccessPush) << 32,
				uint64(vm.OpConst)<<32 + 3,
				uint64(vm.OpAssignRef) << 32,
				uint64(vm.OpPop) << 32,
				uint64(vm.OpArrayAccessPush) << 32,
				uint64(vm.OpConst)<<32 + 4,
				uint64(vm.OpAssignRef) << 32,
				uint64(vm.OpPop) << 32,
				uint64(vm.OpForEachInit) << 32,
				uint64(vm.OpForEachValid) << 32,
				uint64(vm.OpJumpFalse)<<32 + 17,
				uint64(vm.OpForEachValue) << 32,
				uint64(vm.OpForEachKey)<<32 + 1,
				uint64(vm.OpForEachNext) << 32,
				uint64(vm.OpJump)<<32 + 10,
				uint64(vm.OpPop) << 32,
				uint64(vm.OpReturn) << 32,
			},
		},
		{
			input:             "foreach([1,2] as $val){  }",
			expectedConstants: []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1), vm.Int(2)},
			expectedInstructions: []uint64{
				uint64(vm.OpArrayNew) << 32,
				uint64(vm.OpArrayAccessPush) << 32,
				uint64(vm.OpConst)<<32 + 3,
				uint64(vm.OpAssignRef) << 32,
				uint64(vm.OpPop) << 32,
				uint64(vm.OpArrayAccessPush) << 32,
				uint64(vm.OpConst)<<32 + 4,
				uint64(vm.OpAssignRef) << 32,
				uint64(vm.OpPop) << 32,
				uint64(vm.OpForEachInit) << 32,
				uint64(vm.OpForEachValid) << 32,
				uint64(vm.OpJumpFalse)<<32 + 16,
				uint64(vm.OpForEachValue) << 32,
				uint64(vm.OpForEachNext) << 32,
				uint64(vm.OpJump)<<32 + 10,
				uint64(vm.OpPop) << 32,
				uint64(vm.OpReturn) << 32,
			},
		},
		{
			input:             "foreach([1,2] as &$val){  }",
			expectedConstants: []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1), vm.Int(2)},
			expectedInstructions: []uint64{
				uint64(vm.OpArrayNew) << 32,
				uint64(vm.OpArrayAccessPush) << 32,
				uint64(vm.OpConst)<<32 + 3,
				uint64(vm.OpAssignRef) << 32,
				uint64(vm.OpPop) << 32,
				uint64(vm.OpArrayAccessPush) << 32,
				uint64(vm.OpConst)<<32 + 4,
				uint64(vm.OpAssignRef) << 32,
				uint64(vm.OpPop) << 32,
				uint64(vm.OpForEachInit) << 32,
				uint64(vm.OpForEachValid) << 32,
				uint64(vm.OpJumpFalse)<<32 + 16,
				uint64(vm.OpForEachValueRef) << 32,
				uint64(vm.OpForEachNext) << 32,
				uint64(vm.OpJump)<<32 + 10,
				uint64(vm.OpPop) << 32,
				uint64(vm.OpReturn) << 32,
			},
		},
	}

	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			compiler := NewCompiler(nil)
			ctx := new(vm.GlobalContext)
			fn := compiler.Compile([]byte(fmt.Sprintf("<?php\n%s;", c.input)), ctx)
			assert.Equal(t, c.expectedInstructions, fn.Executable)
			assert.Equal(t, c.expectedConstants, ctx.Constants)
		})
	}
}
