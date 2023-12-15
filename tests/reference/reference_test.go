package reference

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"php-vm/internal/compiler"
	"php-vm/internal/vm"
	"testing"
)

func TestReference(t *testing.T) {
	input, err := os.ReadFile("reference.php")
	require.NoError(t, err)

	instructions := vm.Instructions([]uint64{
		uint64(vm.OpInitCall) << 32,
		uint64(vm.OpLoadRef) << 32,
		uint64(vm.OpCall)<<32 + 1,
		uint64(vm.OpPop) << 32,
		uint64(vm.OpReturn) << 32,
	})

	comp := compiler.NewCompiler(nil)
	ctx := new(vm.GlobalContext)
	fn := comp.Compile(input, ctx)
	assert.Equal(t, instructions, fn.Executable)
}
