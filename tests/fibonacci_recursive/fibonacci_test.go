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

	var instructions vm.Instructions

	instructions = append(instructions, uint64(vm.OpLoad))
	instructions = append(instructions, 0)
	instructions = append(instructions, uint64(vm.OpConst))
	instructions = append(instructions, 3)
	instructions = append(instructions, uint64(vm.OpIdentical))
	instructions = append(instructions, uint64(vm.OpJumpFalse))
	instructions = append(instructions, 10)
	instructions = append(instructions, uint64(vm.OpConst))
	instructions = append(instructions, 3)
	instructions = append(instructions, uint64(vm.OpReturnValue))
	instructions = append(instructions, uint64(vm.OpLoad))
	instructions = append(instructions, 0)
	instructions = append(instructions, uint64(vm.OpConst))
	instructions = append(instructions, 4)
	instructions = append(instructions, uint64(vm.OpIdentical))
	instructions = append(instructions, uint64(vm.OpJumpFalse))
	instructions = append(instructions, 20)
	instructions = append(instructions, uint64(vm.OpConst))
	instructions = append(instructions, 4)
	instructions = append(instructions, uint64(vm.OpReturnValue))
	instructions = append(instructions, uint64(vm.OpInitCall))
	instructions = append(instructions, 0)
	instructions = append(instructions, uint64(vm.OpLoad))
	instructions = append(instructions, 0)
	instructions = append(instructions, uint64(vm.OpConst))
	instructions = append(instructions, 4)
	instructions = append(instructions, uint64(vm.OpSub))
	instructions = append(instructions, uint64(vm.OpCall))
	instructions = append(instructions, 1)
	instructions = append(instructions, uint64(vm.OpInitCall))
	instructions = append(instructions, 0)
	instructions = append(instructions, uint64(vm.OpLoad))
	instructions = append(instructions, 0)
	instructions = append(instructions, uint64(vm.OpConst))
	instructions = append(instructions, 5)
	instructions = append(instructions, uint64(vm.OpSub))
	instructions = append(instructions, uint64(vm.OpCall))
	instructions = append(instructions, 1)
	instructions = append(instructions, uint64(vm.OpAdd))
	instructions = append(instructions, uint64(vm.OpReturnValue))

	comp := compiler.NewCompiler(nil)
	ctx := new(vm.GlobalContext)
	_ = comp.Compile(input, ctx)
	assert.Equal(t, instructions, ctx.Functions[0].(vm.CompiledFunction).Instructions)
	assert.Equal(t, []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(0), vm.Int(1), vm.Int(2), vm.Int(10)}, ctx.Constants)
}
