package default_value

import (
	"context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"php-vm/internal/compiler"
	"php-vm/internal/vm"
	"strings"
	"testing"
)

func TestDefaultValue(t *testing.T) {
	input, err := os.ReadFile("./default.php")
	require.NoError(t, err)

	instructions := vm.Instructions([]uint64{
		uint64(vm.OpInitCall) << 32,
		uint64(vm.OpCall) << 32,
		uint64(vm.OpEcho)<<32 + 1,
		uint64(vm.OpReturn) << 32,
	})

	comp := compiler.NewCompiler(nil)
	var result strings.Builder
	ctx := vm.NewGlobalContext(context.TODO(), nil, &result)
	fn := comp.Compile(input, &ctx)
	assert.Equal(t, instructions, fn.Executable)
	assert.Equal(t, []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(1)}, ctx.Constants)
	ctx.Run(fn)
	assert.EqualValues(t, "1", result.String())
}
