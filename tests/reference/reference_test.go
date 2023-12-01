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
		uint64(vm.OpInitCall), 0,
		uint64(vm.OpLoadRef), 0,
		uint64(vm.OpCall), 1,
		uint64(vm.OpPop),
		uint64(vm.OpReturn),
	})

	comp := compiler.NewCompiler(nil)
	ctx := new(vm.GlobalContext)
	fn := comp.Compile(input, ctx)
	assert.Equal(t, instructions, fn.Instructions)
}
