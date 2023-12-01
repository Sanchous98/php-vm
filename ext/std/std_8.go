package std

import "php-vm/internal/vm"

func count(ctx *vm.FunctionContext) vm.Value {
	var countable vm.Value
	vm.ParseParameters(ctx, &countable)

	switch countable.(type) {
	case vm.Countable:
		return countable.(vm.Countable).Count(ctx)
	default:
		ctx.Throw(vm.NewThrowable("invalid type", vm.EError))
		return nil
	}
}
