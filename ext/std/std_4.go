package std

import (
	"php-vm/internal/vm"
	"php-vm/pkg/stdlib"
)

func microtime(args ...vm.Value) vm.Value {
	asNumber := vm.Bool(false)

	if args[0] != nil {
		asNumber = args[0].(vm.Bool)
	}

	num, str := stdlib.Microtime(bool(asNumber))

	if asNumber {
		return vm.Float(num)
	}

	return vm.String(str)
}
