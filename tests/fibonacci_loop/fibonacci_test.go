package fibonacci_loop

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"php-vm/internal/compiler"
	"php-vm/internal/vm"
	"testing"
)

func TestFibonacciLoop(t *testing.T) {
	input, err := os.ReadFile("fibonacci.php")
	require.NoError(t, err)

	instructions := vm.Instructions([]uint64{
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
	})

	comp := compiler.NewCompiler(nil)
	ctx := new(vm.GlobalContext)
	_ = comp.Compile(input, ctx)
	assert.Equal(t, instructions, ctx.Functions[0].(vm.CompiledFunction).Instructions)
	assert.Equal(t, []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(0), vm.Int(2), vm.Int(1)}, ctx.Constants)
}
