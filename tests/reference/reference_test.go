package reference

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
	input, err := os.ReadFile("reference.php")
	require.NoError(t, err)

	instructions := []uint64{
		uint64(vm.OpLoadRef), 0,
		uint64(vm.OpCall), 0,
		uint64(vm.OpPop),
		uint64(vm.OpLoad), 0,
		uint64(vm.OpReturnValue),
		uint64(vm.OpReturn),
	}

	comp := compiler.NewCompiler(nil)
	ctx := new(vm.GlobalContext)
	fn := comp.Compile(input, ctx)
	assert.Equal(t, instructionsToBytecode(instructions).String(), fn.Instructions.String())
}

func instructionsToBytecode(i []uint64) (b vm.Bytecode) {
	for _, instruction := range i {
		b = binary.NativeEndian.AppendUint64(b, instruction)
	}

	return
}
