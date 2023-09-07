package fibonacci_recursive

import (
	"encoding/binary"
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

	comp := compiler.NewCompiler(nil)
	ctx := new(vm.GlobalContext)
	_ = comp.Compile(input, ctx)
	assert.Equal(t, instructions.String(), ctx.Functions[0].(vm.CompiledFunction).Instructions.String())
	assert.Equal(t, []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(0), vm.Int(1), vm.Int(2), vm.Int(10)}, ctx.Constants)
}
