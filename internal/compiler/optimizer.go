package compiler

import (
	"php-vm/internal/vm"
	"php-vm/pkg/slices"
)

func Optimizer(bytecode vm.Instructions) vm.Instructions {
	//bytecode = JoinPops(bytecode)
	return removeNops(bytecode)
}

// NOOP => _
func removeNops(bytecode vm.Instructions) vm.Instructions {
	return slices.Filter(bytecode, func(_ int, instruction uint64) bool {
		return vm.Operator(instruction>>32) != vm.OpNoop
	})
}
