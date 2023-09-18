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
	input, err := os.ReadFile("./arrays.php")
	require.NoError(t, err)

	instructions := [...]uint64{
		uint64(vm.OpConst), 3,
		uint64(vm.OpAssign), 0,
		uint64(vm.OpPop),
		uint64(vm.OpLoad), 0,
		uint64(vm.OpConst), 4,
		uint64(vm.OpLess),
		uint64(vm.OpJumpFalse), 83,
		uint64(vm.OpArrayNew),
		uint64(vm.OpArrayAccessPush),
		uint64(vm.OpConst), 5,
		uint64(vm.OpAssignRef),
		uint64(vm.OpPop),
		uint64(vm.OpConst), 6,
		uint64(vm.OpArrayAccessWrite),
		uint64(vm.OpConst), 7,
		uint64(vm.OpAssignRef),
		uint64(vm.OpPop),
		uint64(vm.OpAssign), 1,
		uint64(vm.OpPop),
		uint64(vm.OpLoad), 1,
		uint64(vm.OpArrayAccessPush),
		uint64(vm.OpConst), 8,
		uint64(vm.OpAssignRef),
		uint64(vm.OpPop),
		uint64(vm.OpPop),
		uint64(vm.OpLoad), 1,
		uint64(vm.OpConst), 9,
		uint64(vm.OpArrayAccessWrite),
		uint64(vm.OpArrayAccessPush),
		uint64(vm.OpConst), 10,
		uint64(vm.OpAssignRef),
		uint64(vm.OpPop),
		uint64(vm.OpPop),
		uint64(vm.OpLoad), 1,
		uint64(vm.OpConst), 9,
		uint64(vm.OpArrayAccessWrite),
		uint64(vm.OpConst), 11,
		uint64(vm.OpArrayAccessWrite),
		uint64(vm.OpConst), 12,
		uint64(vm.OpAssignRef),
		uint64(vm.OpPop),
		uint64(vm.OpPop),
		uint64(vm.OpLoad), 1,
		uint64(vm.OpConst), 13,
		uint64(vm.OpArrayAccessWrite),
		uint64(vm.OpConst), 14,
		uint64(vm.OpAssignRef),
		uint64(vm.OpPop),
		uint64(vm.OpPop),
		uint64(vm.OpLoad), 1,
		uint64(vm.OpConst), 6,
		uint64(vm.OpArrayAccessRead),
		uint64(vm.OpAssign), 2,
		uint64(vm.OpPop),
		uint64(vm.OpPostIncrement), 0,
		uint64(vm.OpPop),
		uint64(vm.OpJump), 5,
		uint64(vm.OpReturn),
	}

	comp := compiler.NewCompiler(nil)
	ctx := new(vm.GlobalContext)
	fn := comp.Compile(input, ctx)
	assert.Equal(t, instructionsToBytecode(instructions[:]).String(), fn.Instructions.String())
	assert.Equal(t, []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(0), vm.Int(100000), vm.Int(1), vm.String("test"), vm.Int(2), vm.Int(3), vm.String("test4"), vm.Int(4), vm.String("test2"), vm.Int(5), vm.String("test3"), vm.Int(6)}, ctx.Constants)
	ctx.Run(fn)
	assert.Equal(t, -1, ctx.TopIndex())
}

func instructionsToBytecode(i []uint64) (b vm.Bytecode) {
	for _, instruction := range i {
		b = binary.NativeEndian.AppendUint64(b, instruction)
	}

	return
}
