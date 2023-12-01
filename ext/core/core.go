package core

import "php-vm/internal/vm"

func funcNumArgs(ctx *vm.FunctionContext) vm.Int {
	parent, ok := ctx.Parent().(*vm.FunctionContext)

	if !ok {
		panic("called from global scope")
	}

	return vm.Int(len(parent.Args))
}

func funcGetArg(ctx *vm.FunctionContext) vm.Value {
	parent, ok := ctx.Parent().(*vm.FunctionContext)

	if !ok {
		panic("called from global scope")
	}

	var num int
	vm.ParseParameters(ctx, &num)

	if len(parent.Args) <= num {
		panic("not enough argument")
	}

	return ctx.Args[num]
}
