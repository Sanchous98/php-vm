package std

import (
	"fmt"
	"php-vm/internal/vm"
	"php-vm/pkg/stdlib"
)

func microtime(args ...vm.Value) vm.Value {
	asNumber := args[0].(vm.Bool)

	num, str := stdlib.Microtime(bool(asNumber))

	if asNumber {
		return vm.Float(num)
	}

	return vm.String(str)
}

func varDump(args ...vm.Value) vm.Value {
	for _, arg := range args {
		fmt.Println(arg.DebugInfo())
	}

	return nil
}
