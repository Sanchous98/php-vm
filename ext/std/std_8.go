package std

import "php-vm/internal/vm"

func count(ctx vm.Context, args ...vm.Value) vm.Value {
	switch args[0].(type) {
	case vm.Countable:
		return args[0].(vm.Countable).Count(ctx)
	default:
		ctx.Throw(vm.NewThrowable("invalid type", vm.EError))
		return nil
	}
}
