package arrays

import (
	"encoding/binary"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"php-vm/internal/compiler"
	"php-vm/internal/vm"
	"testing"
)

func TestArrays(t *testing.T) {
	input, err := os.ReadFile("arrays.php")
	require.NoError(t, err)

	instructions := []uint64{
		uint64(vm.OpArrayInit),
		uint64(vm.OpConst), 3,
		uint64(vm.OpArrayPush),
		uint64(vm.OpConst), 4,
		uint64(vm.OpConst), 5,
		uint64(vm.OpArrayInsert),
		uint64(vm.OpAssign), 0,
		uint64(vm.OpPop),
		uint64(vm.OpLoad), 0,
		uint64(vm.OpConst), 6,
		uint64(vm.OpArrayPush),
		uint64(vm.OpPop),
		uint64(vm.OpLoad), 0,
		uint64(vm.OpConst), 7,
		uint64(vm.OpConst), 8,
		uint64(vm.OpArrayInsert),
		uint64(vm.OpPop),
		uint64(vm.OpLoad), 0,
		uint64(vm.OpConst), 4,
		uint64(vm.OpArrayLookup),
		uint64(vm.OpAssign), 1,
		uint64(vm.OpPop),
		uint64(vm.OpLoad), 0,
		uint64(vm.OpReturnValue),
		uint64(vm.OpReturn),
	}

	comp := compiler.NewCompiler(nil)
	ctx := new(vm.GlobalContext)
	fn := comp.Compile(input, ctx)
	assert.Equal(t, instructionsToBytecode(instructions).String(), fn.Instructions.String())
	assert.Equal(t, []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1), vm.String("test"), vm.Int(2), vm.Int(3), vm.String("test2"), vm.Int(4)}, ctx.Constants)
	assert.Equal(t, vm.Array{
		vm.Int(0):          vm.Int(1),
		vm.String("test"):  vm.Int(2),
		vm.Int(1):          vm.Int(3),
		vm.String("test2"): vm.Int(4),
	}, ctx.Run(fn))
}

func instructionsToBytecode(i []uint64) (b vm.Bytecode) {
	for _, instruction := range i {
		b = binary.NativeEndian.AppendUint64(b, instruction)
	}

	return
}
