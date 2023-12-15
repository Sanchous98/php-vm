package fibonacci_recursive

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"php-vm/internal/compiler"
	"php-vm/internal/vm"
	"testing"
)

func TestFibonacciRecursive(t *testing.T) {
	input, err := os.ReadFile("fibonacci.php")
	require.NoError(t, err)

	instructions := vm.Instructions{
		uint64(vm.OpLoad) << 32,
		uint64(vm.OpConst)<<32 + 3,
		uint64(vm.OpIdentical) << 32,
		uint64(vm.OpJumpFalse)<<32 + 6,
		uint64(vm.OpConst)<<32 + 3,
		uint64(vm.OpReturnValue) << 32,
		uint64(vm.OpLoad) << 32,
		uint64(vm.OpConst)<<32 + 4,
		uint64(vm.OpIdentical) << 32,
		uint64(vm.OpJumpFalse)<<32 + 12,
		uint64(vm.OpConst)<<32 + 4,
		uint64(vm.OpReturnValue) << 32,
		uint64(vm.OpInitCall) << 32,
		uint64(vm.OpLoad) << 32,
		uint64(vm.OpConst)<<32 + 4,
		uint64(vm.OpSub) << 32,
		uint64(vm.OpCall)<<32 + 1,
		uint64(vm.OpInitCall) << 32,
		uint64(vm.OpLoad) << 32,
		uint64(vm.OpConst)<<32 + 5,
		uint64(vm.OpSub) << 32,
		uint64(vm.OpCall)<<32 + 1,
		uint64(vm.OpAdd) << 32,
		uint64(vm.OpReturnValue) << 32,
	}

	comp := compiler.NewCompiler(nil)
	ctx := new(vm.GlobalContext)
	_ = comp.Compile(input, ctx)
	assert.Equal(t, instructions, ctx.Functions[0].Executable)
	assert.Equal(t, []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(0), vm.Int(1), vm.Int(2), vm.Int(10)}, ctx.Constants)
}
