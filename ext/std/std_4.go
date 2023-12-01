package std

import (
	"fmt"
	"php-vm/internal/vm"
	"php-vm/pkg/stdlib"
)

func microtime(ctx *vm.FunctionContext) vm.Value {
	var asNumber bool
	vm.ParseParameters(ctx, &asNumber)

	num, str := stdlib.Microtime(asNumber)

	if asNumber {
		return vm.Float(num)
	}

	return vm.String(str)
}

func varDump(ctx *vm.FunctionContext) vm.Value {
	var value vm.Value
	var values vm.Array
	vm.ParseParameters(ctx, &value, &values)
	fmt.Fprintln(ctx.Output(), value.DebugInfo(ctx))

	for it := values.GetIterator(ctx); it.Valid(ctx); it.Next(ctx) {
		arg := it.Current(ctx)
		fmt.Fprintln(ctx.Output(), arg.DebugInfo(ctx))
	}

	return nil
}
