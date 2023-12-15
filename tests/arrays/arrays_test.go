package arrays

import (
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

	instructions := vm.Instructions{
		uint64(vm.OpConst)<<32 + 3,
		uint64(vm.OpAssign) << 32,
		uint64(vm.OpPop) << 32,
		// for ($i = 0; $i < 100; ...)
		uint64(vm.OpLoad) << 32,
		uint64(vm.OpConst)<<32 + 4,
		uint64(vm.OpLess) << 32,
		uint64(vm.OpJumpFalse)<<32 + 57,
		// $arr = [1, "test" => 2];
		uint64(vm.OpArrayNew) << 32,
		uint64(vm.OpArrayAccessPush) << 32,
		uint64(vm.OpConst)<<32 + 5,
		uint64(vm.OpAssignRef) << 32,
		uint64(vm.OpPop) << 32,
		uint64(vm.OpConst)<<32 + 6,
		uint64(vm.OpArrayAccessWrite) << 32,
		uint64(vm.OpConst)<<32 + 7,
		uint64(vm.OpAssignRef) << 32,
		uint64(vm.OpPop) << 32,
		uint64(vm.OpAssign)<<32 + 1,
		uint64(vm.OpPop) << 32,
		// $arr[] = 3;
		uint64(vm.OpLoad)<<32 + 1,
		uint64(vm.OpArrayAccessPush) << 32,
		uint64(vm.OpConst)<<32 + 8,
		uint64(vm.OpAssignRef) << 32,
		uint64(vm.OpPop) << 32,
		uint64(vm.OpPop) << 32,
		// $arr['test4'][] = 4;
		uint64(vm.OpLoad)<<32 + 1,
		uint64(vm.OpConst)<<32 + 9,
		uint64(vm.OpArrayAccessWrite) << 32,
		uint64(vm.OpArrayAccessPush) << 32,
		uint64(vm.OpConst)<<32 + 10,
		uint64(vm.OpAssignRef) << 32,
		uint64(vm.OpPop) << 32,
		uint64(vm.OpPop) << 32,
		// $arr['test4']['test2'] = 5;
		uint64(vm.OpLoad)<<32 + 1,
		uint64(vm.OpConst)<<32 + 9,
		uint64(vm.OpArrayAccessWrite) << 32,
		uint64(vm.OpConst)<<32 + 11,
		uint64(vm.OpArrayAccessWrite) << 32,
		uint64(vm.OpConst)<<32 + 12,
		uint64(vm.OpAssignRef) << 32,
		uint64(vm.OpPop) << 32,
		uint64(vm.OpPop) << 32,
		// $arr["test3"] = 6;
		uint64(vm.OpLoad)<<32 + 1,
		uint64(vm.OpConst)<<32 + 13,
		uint64(vm.OpArrayAccessWrite) << 32,
		uint64(vm.OpConst)<<32 + 14,
		uint64(vm.OpAssignRef) << 32,
		uint64(vm.OpPop) << 32,
		uint64(vm.OpPop) << 32,
		// $read = $arr["test"];
		uint64(vm.OpLoad)<<32 + 1,
		uint64(vm.OpConst)<<32 + 6,
		uint64(vm.OpArrayAccessRead) << 32,
		uint64(vm.OpAssign)<<32 + 2,
		uint64(vm.OpPop) << 32,
		// for (...; $i++)
		uint64(vm.OpPostIncrement) << 32,
		uint64(vm.OpPop) << 32,
		uint64(vm.OpJump)<<32 + 3,
		uint64(vm.OpReturn) << 32,
	}

	var ctx vm.GlobalContext
	comp := compiler.NewCompiler(nil)
	fn := comp.Compile(input, &ctx)

	assert.Equal(t, instructions, fn.Executable)
	assert.Equal(t, []vm.Value{vm.Bool(true), vm.Bool(false), vm.Null{}, vm.Int(0), vm.Int(10000), vm.Int(1), vm.String("test"), vm.Int(2), vm.Int(3), vm.String("test4"), vm.Int(4), vm.String("test2"), vm.Int(5), vm.String("test3"), vm.Int(6)}, ctx.Constants)
	ctx.Run(fn)
	assert.Equal(t, -1, ctx.TopIndex())
}
