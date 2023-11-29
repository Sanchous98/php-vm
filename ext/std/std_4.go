package std

import (
	"fmt"
	"php-vm/internal/vm"
	"php-vm/pkg/stdlib"
)

func microtime(_ vm.Context, args ...vm.Value) vm.Value {
	asNumber := args[0].(vm.Bool)

	num, str := stdlib.Microtime(bool(asNumber))

	if asNumber {
		return vm.Float(num)
	}

	return vm.String(str)
}

func varDump(ctx vm.Context, args ...vm.Value) vm.Value {
	for _, arg := range args {
		fmt.Fprintln(ctx.Output(), arg.DebugInfo(ctx))
	}

	return nil
}
