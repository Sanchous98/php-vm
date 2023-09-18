package default_value

import (
	"encoding/binary"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"php-vm/internal/compiler"
	"php-vm/internal/vm"
	"testing"
)

func TestDefaultValue(t *testing.T) {
	input, err := os.ReadFile("./default.php")
	require.NoError(t, err)

	instructions := [...]uint64{
		uint64(vm.OpConst), 3,
		uint64(vm.OpCall), 0,
		uint64(vm.OpReturnValue),
	}

	comp := compiler.NewCompiler(nil)
	ctx := new(vm.GlobalContext)
	fn := comp.Compile(input, ctx)
	assert.Equal(t, instructionsToBytecode(instructions[:]).String(), fn.Instructions.String())
	assert.Equal(t, []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)}, ctx.Constants)
	assert.Equal(t, vm.Int(1), ctx.Run(fn))
}

func instructionsToBytecode(i []uint64) (b vm.Bytecode) {
	for _, instruction := range i {
		b = binary.NativeEndian.AppendUint64(b, instruction)
	}

	return
}
