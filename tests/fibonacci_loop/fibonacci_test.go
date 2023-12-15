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
		uint64(vm.OpLoad)<<32 + 0,          // 00000: LOAD          0
		uint64(vm.OpConst)<<32 + 3,         // 00002: CONST         3
		uint64(vm.OpIdentical) << 32,       // 00004: IDENTICAL
		uint64(vm.OpJumpFalse)<<32 + 6,     // 00005: JUMP_NZ       6
		uint64(vm.OpConst)<<32 + 3,         // 00007: CONST         3
		uint64(vm.OpReturnValue) << 32,     // 00009: RETURN_VAL
		uint64(vm.OpLoad)<<32 + 0,          // 00010: LOAD          0
		uint64(vm.OpConst)<<32 + 4,         // 00012: CONST         4
		uint64(vm.OpLess) << 32,            // 00014: LT
		uint64(vm.OpJumpFalse)<<32 + 12,    // 00015: JUMP_NZ       12
		uint64(vm.OpConst)<<32 + 5,         // 00017: CONST         5
		uint64(vm.OpReturnValue) << 32,     // 00019: RETURN_VAL
		uint64(vm.OpConst)<<32 + 5,         // 00020: CONST         5
		uint64(vm.OpAssign)<<32 + 1,        // 00022: ASSIGN        1
		uint64(vm.OpPop) << 32,             // 00024: POP
		uint64(vm.OpConst)<<32 + 5,         // 00025: CONST         5
		uint64(vm.OpAssign)<<32 + 2,        // 00027: ASSIGN        2
		uint64(vm.OpPop) << 32,             // 00029: POP
		uint64(vm.OpConst)<<32 + 4,         // 00030: CONST         4
		uint64(vm.OpAssign)<<32 + 3,        // 00032: ASSIGN        3
		uint64(vm.OpPop) << 32,             // 00034: POP
		uint64(vm.OpLoad)<<32 + 3,          // 00035: LOAD          3
		uint64(vm.OpLoad)<<32 + 0,          // 00037: LOAD          0
		uint64(vm.OpLess) << 32,            // 00039: LT
		uint64(vm.OpJumpFalse)<<32 + 39,    // 00040: JUMP_NZ       65
		uint64(vm.OpLoad)<<32 + 1,          // 00042: LOAD          1
		uint64(vm.OpLoad)<<32 + 2,          // 00044: LOAD          2
		uint64(vm.OpAdd) << 32,             // 00046: ADD
		uint64(vm.OpAssign)<<32 + 4,        // 00047: ASSIGN        4
		uint64(vm.OpPop) << 32,             // 00049: POP
		uint64(vm.OpLoad)<<32 + 2,          // 00050: LOAD          2
		uint64(vm.OpAssign)<<32 + 1,        // 00052: ASSIGN        1
		uint64(vm.OpPop) << 32,             // 00054: POP
		uint64(vm.OpLoad)<<32 + 4,          // 00055: LOAD          4
		uint64(vm.OpAssign)<<32 + 2,        // 00057: ASSIGN        2
		uint64(vm.OpPop) << 32,             // 00059: POP
		uint64(vm.OpPostIncrement)<<32 + 3, // 00060: POST_INC      3
		uint64(vm.OpPop) << 32,             // 00062: POP
		uint64(vm.OpJump)<<32 + 21,         // 00063: JUMP          35
		uint64(vm.OpLoad)<<32 + 2,          // 00065: LOAD          2
		uint64(vm.OpReturnValue) << 32,     // 00067: RETURN_VAL
	})

	comp := compiler.NewCompiler(nil)
	ctx := new(vm.GlobalContext)
	_ = comp.Compile(input, ctx)
	assert.Equal(t, instructions, ctx.Functions[0].Executable)
	assert.Equal(t, []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(0), vm.Int(2), vm.Int(1)}, ctx.Constants)
}
